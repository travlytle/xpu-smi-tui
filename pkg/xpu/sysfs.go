package xpu

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSysfsRoot is where the kernel exposes PCI devices.
const DefaultSysfsRoot = "/sys/bus/pci/devices"

// TempSensor is one labelled hwmon temperature sensor and the limits it carries.
//
// The xe driver exposes 20 of these per card -- pkg, vram, mctrl, pcie, and
// vram_ch_0..15 -- each with its own thresholds, so limits are read from sysfs
// rather than hardcoded.
type TempSensor struct {
	Index      int     // the N in tempN_input, for stable ordering
	Label      string  // pkg, vram, mctrl, pcie, vram_ch_0 ...
	ValueC     float64 // current reading
	CritC      float64 // tempN_crit, 0 if the sensor has none
	EmergencyC float64 // tempN_emergency, 0 if absent
	MaxC       float64 // tempN_max, 0 if absent
}

// IsVramChannel reports whether this is one of the 16 per-channel VRAM sensors.
func (t TempSensor) IsVramChannel() bool {
	return strings.HasPrefix(t.Label, "vram_ch_")
}

// GTFreq holds every frequency value a single GT publishes under freq0/.
type GTFreq struct {
	CurMHz  float64 // current requested
	ActMHz  float64 // actual; reads 0 during a c6 idle window, which is expected
	MinMHz  float64 // active floor (writable, and managed by the gpu-thermal daemon)
	MaxMHz  float64 // active ceiling (writable)
	RpnMHz  float64 // hardware minimum
	Rp0MHz  float64 // hardware maximum
	RpeMHz  float64 // efficient
	RpaMHz  float64 // achievable
	Profile string  // selected power_profile, e.g. "base"
}

// ThrottleFlags mirrors tile0/gtN/freq0/throttle/.
//
// VRThermalert and VRTdc are the notable ones: the voltage regulator is the
// hottest component on these cards and has no host-readable temperature sensor,
// so these flags are the only host-visible signal that it is in trouble.
type ThrottleFlags struct {
	Status       bool
	Reasons      string // "none", or a list
	PL1          bool
	PL2          bool
	PL4          bool
	Thermal      bool
	VRThermalert bool
	VRTdc        bool
	Prochot      bool
	RATL         bool
}

// AnyVR reports whether either voltage-regulator flag is asserted.
func (t ThrottleFlags) AnyVR() bool { return t.VRThermalert || t.VRTdc }

// GTIdle is per-GT idle residency. BusyPercent is derived from the change in
// ResidencyMs across two samples, so it is only meaningful once a previous
// sample exists; BusyValid says whether it does.
type GTIdle struct {
	ResidencyMs uint64
	Status      string // e.g. "gt-c0" (active) or "gt-c6" (idle)
	Name        string // e.g. "gt0-rc"
	BusyPercent float64
	BusyValid   bool
}

// GTStats aggregates everything published for one GT. gt0 is render/compute,
// gt1 is media.
type GTStats struct {
	Name     string // gt0, gt1
	Tile     string // tile0, tile1 -- with Name, unique across a multi-tile part
	Freq     GTFreq
	Throttle ThrottleFlags
	Idle     GTIdle
}

// PowerLimits are the enforced caps from hwmon, in watts.
type PowerLimits struct {
	CapW        float64 // power1_cap, else power1_max, power2_cap, power2_max
	CritW       float64 // crit of the same domain
	CapInterval float64 // the cap's averaging window in milliseconds, as hwmon reports it
}

// EnergyDomains holds the two energy counters, in joules. There is no
// power1_input on these cards: every watt figure is a delta of one of these.
type EnergyDomains struct {
	CardJ float64 // energy1, labelled "card" -- whole board
	PkgJ  float64 // energy2, labelled "pkg" -- package only
}

// DerivedPower is a watt figure computed from an energy delta, carrying the
// window it was measured over so the UI can label it honestly.
type DerivedPower struct {
	Watts   float64
	WindowS float64
	Valid   bool
}

// PcieLink describes the host PCIe link.
//
// The B70 carries an on-card PCIe switch, so the GPU function itself reports
// 2.5 GT/s x1 -- an internal downstream port, not the host link. Reporting that
// makes every user think the card is mis-seated. Link is resolved by walking up
// to the first ancestor with max_link_width > 1; the GPU's own values are kept
// in the Local fields for the inspector.
type PcieLink struct {
	SpeedGTs    float64
	Width       int
	MaxSpeedGTs float64
	MaxWidth    int
	SourceBDF   string // the ancestor actually reported
	LocalSpeed  float64
	LocalWidth  int
	Resolved    bool
}

// RuntimePM is the PCI runtime power-management state. These attributes are
// served by the PM core from host memory, so reading them never resumes the
// card -- unlike everything else in SysfsStats.
type RuntimePM struct {
	Status             string // "active" or "suspended"
	ActiveMs           uint64
	SuspendedMs        uint64
	AutosuspendDelayMs int
}

// Suspended reports whether the card is currently runtime-suspended.
func (r RuntimePM) Suspended() bool { return r.Status == "suspended" }

// SysfsStats is everything read directly from the card's sysfs nodes, covering
// what xpu-smi does not report.
//
// Fan speed is deliberately absent: it is served by LiveCollector.readFanRPM,
// which median-filters the noisy tachometer, and duplicating it here would give
// the UI two disagreeing sources for the same number.
type SysfsStats struct {
	Available bool
	// Driver is the kernel driver bound to the card, "xe" or "i915". The two
	// lay their sysfs out differently and report different things through
	// fdinfo and xpu-smi; callers use this to tell "not reported by this
	// driver" apart from "reported as zero".
	Driver      string
	Temps       []TempSensor
	TempByLabel map[string]TempSensor
	PowerLimits PowerLimits
	Energy      EnergyDomains
	CardPower   DerivedPower
	PkgPower    DerivedPower
	GTs         []GTStats
	Pcie        PcieLink
	PM          RuntimePM

	// Displays lists connectors with a monitor attached, e.g. "DP-7". A card
	// driving a display carries the compositor's framebuffers, and holds a DRM
	// client open for as long as the display session runs.
	Displays []string
}

// Temp returns the sensor with the given label.
func (s *SysfsStats) Temp(label string) (TempSensor, bool) {
	t, ok := s.TempByLabel[label]
	return t, ok
}

// VramChannels returns the 16 per-channel VRAM sensors in index order.
func (s *SysfsStats) VramChannels() []TempSensor {
	out := make([]TempSensor, 0, 16)
	for _, t := range s.Temps {
		if t.IsVramChannel() {
			out = append(out, t)
		}
	}
	return out
}

// HottestTemp returns the highest-reading sensor.
func (s *SysfsStats) HottestTemp() (TempSensor, bool) {
	var best TempSensor
	found := false
	for _, t := range s.Temps {
		if !found || t.ValueC > best.ValueC {
			best, found = t, true
		}
	}
	return best, found
}

// ThrottledGTs returns the names of GTs currently reporting a throttle.
func (s *SysfsStats) ThrottledGTs() []string {
	var out []string
	for _, gt := range s.GTs {
		if gt.Throttle.Status {
			out = append(out, gt.Name)
		}
	}
	return out
}

type energySample struct {
	joules float64
	at     time.Time
}

type idleSample struct {
	residencyMs uint64
	at          time.Time
}

// SysfsCollector reads per-card detail straight from sysfs.
//
// It holds the previous sample of every monotonic counter it turns into a rate
// (energy -> watts, idle residency -> busy %), so a collector instance must be
// reused across polls for those to be populated.
type SysfsCollector struct {
	root string

	// now is injectable so tests can drive the energy and idle-residency rate
	// derivations across controlled intervals.
	now func() time.Time

	mu       sync.Mutex
	lastCard map[string]energySample
	lastPkg  map[string]energySample
	lastIdle map[string]idleSample // keyed by bdf + "/" + gt
}

// NewSysfsCollector creates a collector rooted at the real sysfs tree. An empty
// root means DefaultSysfsRoot; tests pass a fake tree.
func NewSysfsCollector(root string) *SysfsCollector {
	if root == "" {
		root = DefaultSysfsRoot
	}
	return &SysfsCollector{
		root:     root,
		now:      time.Now,
		lastCard: make(map[string]energySample),
		lastPkg:  make(map[string]energySample),
		lastIdle: make(map[string]idleSample),
	}
}

func (c *SysfsCollector) devicePath(bdf string, parts ...string) string {
	return filepath.Join(append([]string{c.root, bdf}, parts...)...)
}

func readTrimmed(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

func readFloatFile(path string) (float64, bool) {
	s, ok := readTrimmed(path)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readUintFile(path string) (uint64, bool) {
	s, ok := readTrimmed(path)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readIntFile(path string) (int, bool) {
	v, ok := readUintFile(path)
	return int(v), ok
}

// readFlagFile reads a sysfs boolean ("0"/"1").
func readFlagFile(path string) bool {
	s, ok := readTrimmed(path)
	return ok && s == "1"
}

// parseLinkSpeed pulls the number out of a current_link_speed value, which the
// kernel formats as "8.0 GT/s PCIe".
func parseLinkSpeed(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}

// parseSelectedProfile pulls the bracketed selection out of a power_profile
// value, which is formatted as "[base]  power_saving".
func parseSelectedProfile(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "[") && strings.HasSuffix(f, "]") {
			return strings.Trim(f, "[]")
		}
	}
	return ""
}

// hwmonDir finds the card's hwmon node.
func (c *SysfsCollector) hwmonDir(bdf string) (string, bool) {
	matches, err := filepath.Glob(c.devicePath(bdf, "hwmon", "hwmon*"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	return matches[0], true
}

// readTemps reads every temperature sensor with its own limits.
//
// The xe driver labels each sensor (pkg, vram, ...). The i915 driver on
// Alchemist cards publishes a single unlabelled temp1_input, which is the
// package sensor, so an unlabelled tempN is read too: temp1 as "pkg", any
// other as "tempN".
func readTemps(hwmon string) ([]TempSensor, map[string]TempSensor) {
	inputs, err := filepath.Glob(filepath.Join(hwmon, "temp*_input"))
	if err != nil {
		return nil, nil
	}

	sensors := make([]TempSensor, 0, len(inputs))
	for _, inputPath := range inputs {
		stem := strings.TrimSuffix(filepath.Base(inputPath), "_input") // tempN
		idx, err := strconv.Atoi(strings.TrimPrefix(stem, "temp"))
		if err != nil {
			continue
		}
		label, ok := readTrimmed(filepath.Join(hwmon, stem+"_label"))
		if !ok {
			label = stem
			if idx == 1 {
				label = "pkg"
			}
		}
		value, ok := readFloatFile(inputPath)
		if !ok {
			continue
		}

		s := TempSensor{Index: idx, Label: label, ValueC: value / 1000.0}
		if v, ok := readFloatFile(filepath.Join(hwmon, stem+"_crit")); ok {
			s.CritC = v / 1000.0
		}
		if v, ok := readFloatFile(filepath.Join(hwmon, stem+"_emergency")); ok {
			s.EmergencyC = v / 1000.0
		}
		if v, ok := readFloatFile(filepath.Join(hwmon, stem+"_max")); ok {
			s.MaxC = v / 1000.0
		}
		sensors = append(sensors, s)
	}

	// Glob returns lexical order, which puts temp10 before temp2. Sort numerically
	// so vram_ch_0..15 come out in channel order.
	sort.Slice(sensors, func(i, j int) bool { return sensors[i].Index < sensors[j].Index })

	byLabel := make(map[string]TempSensor, len(sensors))
	for _, s := range sensors {
		byLabel[s.Label] = s
	}
	return sensors, byLabel
}

// readEnergyDomains reads the two energy counters by label rather than by index,
// since the index-to-domain mapping is not guaranteed.
func readEnergyDomains(hwmon string) EnergyDomains {
	var e EnergyDomains
	// i915 publishes one unlabelled energy1_input: the whole board.
	if _, labelled := readTrimmed(filepath.Join(hwmon, "energy1_label")); !labelled {
		if raw, ok := readFloatFile(filepath.Join(hwmon, "energy1_input")); ok {
			e.CardJ = raw / 1e6
		}
		return e
	}
	labels, err := filepath.Glob(filepath.Join(hwmon, "energy*_label"))
	if err != nil {
		return e
	}
	for _, labelPath := range labels {
		stem := strings.TrimSuffix(filepath.Base(labelPath), "_label")
		label, ok := readTrimmed(labelPath)
		if !ok {
			continue
		}
		raw, ok := readFloatFile(filepath.Join(hwmon, stem+"_input"))
		if !ok {
			continue
		}
		joules := raw / 1e6 // hwmon energy is microjoules
		switch label {
		case "card":
			e.CardJ = joules
		case "pkg":
			e.PkgJ = joules
		}
	}
	return e
}

func readPowerLimits(hwmon string) PowerLimits {
	var p PowerLimits
	// power1 is the card domain; cards without one (A-series on xe) publish
	// only power2, the package. i915 names its PL1 limit _max rather than _cap.
	for _, dom := range []string{"power1", "power2"} {
		for _, name := range []string{"_cap", "_max"} {
			v, ok := readFloatFile(filepath.Join(hwmon, dom+name))
			if !ok {
				continue
			}
			p.CapW = v / 1e6 // microwatts
			if iv, ok := readFloatFile(filepath.Join(hwmon, dom+name+"_interval")); ok {
				p.CapInterval = iv
			}
			if cv, ok := readFloatFile(filepath.Join(hwmon, dom+"_crit")); ok {
				p.CritW = cv / 1e6
			}
			return p
		}
	}
	return p
}

func readGTFreq(gtDir string) GTFreq {
	freq := filepath.Join(gtDir, "freq0")
	get := func(name string) float64 {
		v, _ := readFloatFile(filepath.Join(freq, name))
		return v
	}
	f := GTFreq{
		CurMHz: get("cur_freq"),
		ActMHz: get("act_freq"),
		MinMHz: get("min_freq"),
		MaxMHz: get("max_freq"),
		RpnMHz: get("rpn_freq"),
		Rp0MHz: get("rp0_freq"),
		RpeMHz: get("rpe_freq"),
		RpaMHz: get("rpa_freq"),
	}
	if s, ok := readTrimmed(filepath.Join(freq, "power_profile")); ok {
		f.Profile = parseSelectedProfile(s)
	}
	return f
}

func readThrottle(gtDir string) ThrottleFlags {
	dir := filepath.Join(gtDir, "freq0", "throttle")
	t := ThrottleFlags{
		Status:       readFlagFile(filepath.Join(dir, "status")),
		PL1:          readFlagFile(filepath.Join(dir, "reason_pl1")),
		PL2:          readFlagFile(filepath.Join(dir, "reason_pl2")),
		PL4:          readFlagFile(filepath.Join(dir, "reason_pl4")),
		Thermal:      readFlagFile(filepath.Join(dir, "reason_thermal")),
		VRThermalert: readFlagFile(filepath.Join(dir, "reason_vr_thermalert")),
		VRTdc:        readFlagFile(filepath.Join(dir, "reason_vr_tdc")),
		Prochot:      readFlagFile(filepath.Join(dir, "reason_prochot")),
		RATL:         readFlagFile(filepath.Join(dir, "reason_ratl")),
	}
	if s, ok := readTrimmed(filepath.Join(dir, "reasons")); ok {
		t.Reasons = s
	}
	return t
}

// readIdle reads gtidle and derives busy % from the change in idle residency
// against the previous sample.
// readIdle's gt argument must be unique per card -- tile/gt -- because the
// previous sample is kept per key. Keyed on the bare gt name, a multi-tile
// part's tile0/gt0 and tile1/gt0 overwrote each other's baseline.
func (c *SysfsCollector) readIdle(bdf, gt, gtDir string, now time.Time) GTIdle {
	dir := filepath.Join(gtDir, "gtidle")
	var idle GTIdle
	if s, ok := readTrimmed(filepath.Join(dir, "idle_status")); ok {
		idle.Status = s
	}
	if s, ok := readTrimmed(filepath.Join(dir, "name")); ok {
		idle.Name = s
	}
	residency, ok := readUintFile(filepath.Join(dir, "idle_residency_ms"))
	if !ok {
		return idle
	}
	return c.deriveBusy(idle, bdf+"/"+gt, residency, now)
}

// deriveBusy fills in a GTIdle's residency and, given a previous sample,
// its busy percentage. Shared by the xe (gtidle/idle_residency_ms) and i915
// (rc6_residency_ms) readers: both are a monotonic count of milliseconds
// spent in the idle power state.
func (c *SysfsCollector) deriveBusy(idle GTIdle, key string, residency uint64, now time.Time) GTIdle {
	idle.ResidencyMs = residency

	c.mu.Lock()
	prev, had := c.lastIdle[key]
	c.lastIdle[key] = idleSample{residencyMs: residency, at: now}
	c.mu.Unlock()

	if !had {
		return idle
	}
	wallMs := float64(now.Sub(prev.at).Milliseconds())
	if wallMs <= 0 {
		return idle
	}
	// The counter is monotonic; a decrease means it was reset underneath us, so
	// drop the sample rather than report a nonsense number.
	if residency < prev.residencyMs {
		return idle
	}
	busy := 100.0 - (float64(residency-prev.residencyMs)/wallMs)*100.0
	idle.BusyPercent = clampPercent(busy)
	idle.BusyValid = true
	return idle
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// readGTs enumerates every GT under every tile.
func (c *SysfsCollector) readGTs(bdf string, now time.Time) []GTStats {
	tiles, err := filepath.Glob(c.devicePath(bdf, "tile*"))
	if err != nil {
		return nil
	}
	if len(tiles) == 0 {
		return c.readI915GTs(bdf, now)
	}
	sort.Strings(tiles)

	var out []GTStats
	for _, tile := range tiles {
		gts, err := filepath.Glob(filepath.Join(tile, "gt*"))
		if err != nil {
			continue
		}
		sort.Strings(gts)
		for _, gtDir := range gts {
			name := filepath.Base(gtDir)
			tileName := filepath.Base(tile)
			out = append(out, GTStats{
				Name:     name,
				Tile:     tileName,
				Freq:     readGTFreq(gtDir),
				Throttle: readThrottle(gtDir),
				Idle:     c.readIdle(bdf, tileName+"/"+name, gtDir, now),
			})
		}
	}
	return out
}

// readI915GTs reads the i915 driver's layout, drm/cardN/gt/gtM/, which is
// flat where xe's is tile*/gt*/freq0/: rps_* frequency files, throttle_reason_*
// flags and rc6_residency_ms in place of gtidle. There is no tile level, so
// every GT is reported under tile0.
func (c *SysfsCollector) readI915GTs(bdf string, now time.Time) []GTStats {
	gts, err := filepath.Glob(c.devicePath(bdf, "drm", "card*", "gt", "gt*"))
	if err != nil || len(gts) == 0 {
		return nil
	}
	sort.Strings(gts)

	var out []GTStats
	for _, gtDir := range gts {
		name := filepath.Base(gtDir)
		gt := GTStats{
			Name:     name,
			Tile:     "tile0",
			Freq:     readI915Freq(gtDir),
			Throttle: readI915Throttle(gtDir),
			Idle:     GTIdle{Name: name + "-rc"},
		}
		if residency, ok := readUintFile(filepath.Join(gtDir, "rc6_residency_ms")); ok {
			gt.Idle = c.deriveBusy(gt.Idle, bdf+"/tile0/"+name, residency, now)
		}
		out = append(out, gt)
	}
	return out
}

func readI915Freq(gtDir string) GTFreq {
	get := func(name string) float64 {
		v, _ := readFloatFile(filepath.Join(gtDir, name))
		return v
	}
	f := GTFreq{
		CurMHz: get("rps_cur_freq_mhz"),
		ActMHz: get("rps_act_freq_mhz"),
		MinMHz: get("rps_min_freq_mhz"),
		MaxMHz: get("rps_max_freq_mhz"),
		RpnMHz: get("rps_RPn_freq_mhz"),
		Rp0MHz: get("rps_RP0_freq_mhz"),
		RpeMHz: get("rps_RP1_freq_mhz"), // RP1 is the efficient frequency
	}
	if s, ok := readTrimmed(filepath.Join(gtDir, "slpc_power_profile")); ok {
		f.Profile = parseSelectedProfile(s)
	}
	return f
}

func readI915Throttle(gtDir string) ThrottleFlags {
	flag := func(reason string) bool { return readFlagFile(filepath.Join(gtDir, "throttle_reason_"+reason)) }
	t := ThrottleFlags{
		Status:       flag("status"),
		PL1:          flag("pl1"),
		PL2:          flag("pl2"),
		PL4:          flag("pl4"),
		Thermal:      flag("thermal"),
		VRThermalert: flag("vr_thermalert"),
		VRTdc:        flag("vr_tdc"),
		Prochot:      flag("prochot"),
		RATL:         flag("ratl"),
	}
	// i915 has no combined "reasons" file, so build the same string xe gives.
	var active []string
	for _, r := range []struct {
		on   bool
		name string
	}{
		{t.PL1, "pl1"}, {t.PL2, "pl2"}, {t.PL4, "pl4"}, {t.Thermal, "thermal"},
		{t.VRThermalert, "vr_thermalert"}, {t.VRTdc, "vr_tdc"},
		{t.Prochot, "prochot"}, {t.RATL, "ratl"},
	} {
		if r.on {
			active = append(active, r.name)
		}
	}
	t.Reasons = "none"
	if len(active) > 0 {
		t.Reasons = strings.Join(active, ", ")
	}
	return t
}

// readDriver names the kernel driver bound to the card.
func (c *SysfsCollector) readDriver(bdf string) string {
	target, err := os.Readlink(c.devicePath(bdf, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func (c *SysfsCollector) readRuntimePM(bdf string) RuntimePM {
	dir := c.devicePath(bdf, "power")
	var pm RuntimePM
	if s, ok := readTrimmed(filepath.Join(dir, "runtime_status")); ok {
		pm.Status = s
	}
	if v, ok := readUintFile(filepath.Join(dir, "runtime_active_time")); ok {
		pm.ActiveMs = v
	}
	if v, ok := readUintFile(filepath.Join(dir, "runtime_suspended_time")); ok {
		pm.SuspendedMs = v
	}
	if v, ok := readIntFile(filepath.Join(dir, "autosuspend_delay_ms")); ok {
		pm.AutosuspendDelayMs = v
	}
	return pm
}

// pciBDFPattern matches a PCI address directory name, e.g. "0000:18:01.0".
var pciBDFPattern = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-9a-fA-F]$`)

// resolvePcieLink reports the real host link.
//
// Reading current_link_speed/width on the GPU function itself gives 2.5 GT/s x1
// on a B70, because that function sits behind the card's own PCIe switch. This
// walks up to the first ancestor with max_link_width > 1, which is the port
// actually facing the host. The GPU's own values are kept in the Local fields.
func (c *SysfsCollector) resolvePcieLink(bdf string) PcieLink {
	var link PcieLink
	devDir := c.devicePath(bdf)

	if s, ok := readTrimmed(filepath.Join(devDir, "current_link_speed")); ok {
		link.LocalSpeed = parseLinkSpeed(s)
	}
	if v, ok := readIntFile(filepath.Join(devDir, "current_link_width")); ok {
		link.LocalWidth = v
	}

	real, err := filepath.EvalSymlinks(devDir)
	if err != nil {
		return link
	}

	for dir := filepath.Dir(real); pciBDFPattern.MatchString(filepath.Base(dir)); dir = filepath.Dir(dir) {
		maxWidth, ok := readIntFile(filepath.Join(dir, "max_link_width"))
		if !ok || maxWidth <= 1 {
			continue
		}
		link.MaxWidth = maxWidth
		link.SourceBDF = filepath.Base(dir)
		if s, ok := readTrimmed(filepath.Join(dir, "current_link_speed")); ok {
			link.SpeedGTs = parseLinkSpeed(s)
		}
		if v, ok := readIntFile(filepath.Join(dir, "current_link_width")); ok {
			link.Width = v
		}
		if s, ok := readTrimmed(filepath.Join(dir, "max_link_speed")); ok {
			link.MaxSpeedGTs = parseLinkSpeed(s)
		}
		link.Resolved = true
		return link
	}
	return link
}

// deriveWatts turns an energy counter into a watt figure using the previous
// sample, and reports the window it covers so the UI can label it.
//
// Energy deltas across a suspend/resume can be garbage: the counter resets
// underneath the driver, which has produced negative deltas and a pkg reading of
// 87 W against a card reading of 24 W in the same sample. Anything outside
// 0..power1_crit is dropped rather than displayed.
func (c *SysfsCollector) deriveWatts(domain, bdf string, joules, critW, capW float64, now time.Time) DerivedPower {
	var out DerivedPower
	if joules <= 0 {
		return out
	}

	c.mu.Lock()
	store := c.lastCard
	if domain == "pkg" {
		store = c.lastPkg
	}
	prev, had := store[bdf]
	store[bdf] = energySample{joules: joules, at: now}
	c.mu.Unlock()

	if !had {
		return out
	}
	window := now.Sub(prev.at).Seconds()
	if window <= 0 {
		return out
	}
	delta := joules - prev.joules
	if delta < 0 {
		return out // counter reset across a suspend/resume
	}
	watts := delta / window
	// Sanity bound. power1_crit is the published one; when a card does not
	// expose it, fall back to a multiple of the cap, then a fixed ceiling,
	// rather than publishing a wrapped counter verbatim.
	limit := critW
	if limit <= 0 {
		limit = capW * 2
	}
	if limit <= 0 {
		limit = 1000
	}
	if watts > limit {
		return out // implausible; a corrupted delta
	}
	return DerivedPower{Watts: watts, WindowS: window, Valid: true}
}

// resolveBDF accepts both "0000:19:00.0" and "19:00.0".
func (c *SysfsCollector) resolveBDF(bdf string) (string, bool) {
	bdf = strings.TrimSpace(bdf)
	if bdf == "" {
		return "", false
	}
	for _, candidate := range []string{bdf, "0000:" + strings.TrimPrefix(bdf, "0000:")} {
		if _, err := os.Stat(c.devicePath(candidate)); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// Collect reads every sysfs attribute this package knows about for one card.
//
// Every read is best-effort: a missing attribute leaves its field zeroed rather
// than failing the whole collection, since which attributes exist varies by
// driver version. Available reports whether the card was found at all.
//
// Note that every attribute read here except the PM block resumes a
// runtime-suspended card. That costs nothing while a DRM client
// holds the device -- the normal case -- but callers polling a genuinely idle
// card should gate on PM.Suspended().
func (c *SysfsCollector) Collect(bdf string) *SysfsStats {
	resolved, ok := c.resolveBDF(bdf)
	if !ok {
		return &SysfsStats{TempByLabel: map[string]TempSensor{}}
	}

	now := c.now()
	stats := &SysfsStats{
		Available:   true,
		TempByLabel: map[string]TempSensor{},
		Driver:      c.readDriver(resolved),
		PM:          c.readRuntimePM(resolved),
		Displays:    c.readDisplays(resolved),
		Pcie:        c.resolvePcieLink(resolved),
		GTs:         c.readGTs(resolved, now),
	}

	if hwmon, ok := c.hwmonDir(resolved); ok {
		temps, byLabel := readTemps(hwmon)
		stats.Temps = temps
		if byLabel != nil {
			stats.TempByLabel = byLabel
		}
		stats.PowerLimits = readPowerLimits(hwmon)
		stats.Energy = readEnergyDomains(hwmon)
		stats.CardPower = c.deriveWatts("card", resolved, stats.Energy.CardJ,
			stats.PowerLimits.CritW, stats.PowerLimits.CapW, now)
		stats.PkgPower = c.deriveWatts("pkg", resolved, stats.Energy.PkgJ,
			stats.PowerLimits.CritW, stats.PowerLimits.CapW, now)
	}

	// Available is a claim about what was read, not about a path having existed
	// a moment ago: a card that vanished between the stat and the reads must not
	// come back "available" with every sensor at zero.
	stats.Available = len(stats.Temps) > 0 || len(stats.GTs) > 0 || stats.PM.Status != ""

	return stats
}

// readDisplays returns the connectors on this card that report a connected
// monitor. Connector names are cardN-<connector>; the card prefix is dropped.
func (c *SysfsCollector) readDisplays(bdf string) []string {
	statuses, err := filepath.Glob(c.devicePath(bdf, "drm", "card*", "card*-*", "status"))
	if err != nil {
		return nil
	}
	sort.Strings(statuses)
	var out []string
	for _, path := range statuses {
		if state, ok := readTrimmed(path); !ok || state != "connected" {
			continue
		}
		name := filepath.Base(filepath.Dir(path))
		if _, connector, found := strings.Cut(name, "-"); found {
			name = connector
		}
		out = append(out, name)
	}
	return out
}

// PowerCapW is the card's enforced power cap in watts, or 0 if it publishes
// none. It reads only that one attribute, so unlike Collect it can be called
// at discovery without disturbing the rate baselines Collect keeps.
func (c *SysfsCollector) PowerCapW(bdf string) float64 {
	resolved, ok := c.resolveBDF(bdf)
	if !ok {
		return 0
	}
	hwmon, ok := c.hwmonDir(resolved)
	if !ok {
		return 0
	}
	return readPowerLimits(hwmon).CapW
}

// CPUAffinity is the card's local CPU list, e.g. "0-31".
func (c *SysfsCollector) CPUAffinity(bdf string) string {
	resolved, ok := c.resolveBDF(bdf)
	if !ok {
		return ""
	}
	s, _ := readTrimmed(c.devicePath(resolved, "local_cpulist"))
	return s
}

// DrmDevice is the card's /dev/dri/cardN node, or "" if it has none.
func (c *SysfsCollector) DrmDevice(bdf string) string {
	resolved, ok := c.resolveBDF(bdf)
	if !ok {
		return ""
	}
	nodes, _ := filepath.Glob(c.devicePath(resolved, "drm", "card[0-9]*"))
	for _, n := range nodes {
		if name := filepath.Base(n); !strings.Contains(name, "-") {
			return "/dev/dri/" + name
		}
	}
	return ""
}

// RuntimePMFor is the card's runtime power-management state. It is served by
// the PM core from host memory, so unlike every other read here it never
// resumes a suspended card; it is what the idle gate decides on.
func (c *SysfsCollector) RuntimePMFor(bdf string) RuntimePM {
	resolved, ok := c.resolveBDF(bdf)
	if !ok {
		return RuntimePM{}
	}
	return c.readRuntimePM(resolved)
}

// DrmNodes lists the card's device node names ("card1", "renderD128"). Reading
// a directory listing does not touch the device.
func (c *SysfsCollector) DrmNodes(bdf string) []string {
	resolved, ok := c.resolveBDF(bdf)
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(c.devicePath(resolved, "drm"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.Contains(name, "-") && (strings.HasPrefix(name, "card") || strings.HasPrefix(name, "renderD")) {
			out = append(out, name)
		}
	}
	return out
}
