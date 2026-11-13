package xpu

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeCard builds a sysfs tree shaped like a real Arc Pro B70 under t.TempDir,
// including the PCIe ancestry: the GPU function sits behind the card's own
// switch port, which sits behind the port that actually faces the host.
type fakeCard struct {
	root   string // stands in for /sys/bus/pci/devices
	devDir string // the GPU function's real directory
	hwmon  string
	bdf    string
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newFakeCard(t *testing.T) *fakeCard {
	t.Helper()
	tmp := t.TempDir()
	bdf := "0000:19:00.0"

	// Real device hierarchy: host port -> card switch port -> GPU function.
	hostPort := filepath.Join(tmp, "devices", "pci0000:16", "0000:17:00.0")
	switchPort := filepath.Join(hostPort, "0000:18:01.0")
	devDir := filepath.Join(switchPort, bdf)

	// The host-facing port carries the real link.
	write(t, filepath.Join(hostPort, "max_link_width"), "16")
	write(t, filepath.Join(hostPort, "max_link_speed"), "16.0 GT/s PCIe")
	write(t, filepath.Join(hostPort, "current_link_speed"), "8.0 GT/s PCIe")
	write(t, filepath.Join(hostPort, "current_link_width"), "16")

	// The card's internal switch port reports x1 and must be walked past.
	write(t, filepath.Join(switchPort, "max_link_width"), "1")
	write(t, filepath.Join(switchPort, "current_link_speed"), "2.5 GT/s PCIe")
	write(t, filepath.Join(switchPort, "current_link_width"), "1")

	// The GPU function itself reports the misleading x1.
	write(t, filepath.Join(devDir, "current_link_speed"), "2.5 GT/s PCIe")
	write(t, filepath.Join(devDir, "current_link_width"), "1")

	// One monitor attached, one connector empty.
	write(t, filepath.Join(devDir, "drm", "card1", "card1-DP-7", "status"), "connected")
	write(t, filepath.Join(devDir, "drm", "card1", "card1-HDMI-A-1", "status"), "disconnected")

	// Runtime PM.
	write(t, filepath.Join(devDir, "power", "runtime_status"), "suspended")
	write(t, filepath.Join(devDir, "power", "runtime_active_time"), "71867972")
	write(t, filepath.Join(devDir, "power", "runtime_suspended_time"), "54901")
	write(t, filepath.Join(devDir, "power", "autosuspend_delay_ms"), "1000")

	// hwmon: a representative subset of the 20 sensors, deliberately including
	// temp10 so numeric ordering is exercised against lexical globbing, and the
	// energy sensors whose labels collide with the temperature labels.
	hwmon := filepath.Join(devDir, "hwmon", "hwmon4")
	temps := []struct {
		n                       int
		label                   string
		input, crit, emerg, max string
	}{
		{2, "pkg", "48000", "100000", "125000", "60000"},
		{3, "vram", "52000", "105000", "120000", ""},
		{4, "mctrl", "37000", "100000", "125000", ""},
		{5, "pcie", "51000", "100000", "125000", ""},
		{6, "vram_ch_0", "48000", "105000", "120000", ""},
		{10, "vram_ch_4", "44000", "105000", "120000", ""},
	}
	for _, tc := range temps {
		stem := filepath.Join(hwmon, "temp"+itoa(tc.n))
		write(t, stem+"_label", tc.label)
		write(t, stem+"_input", tc.input)
		if tc.crit != "" {
			write(t, stem+"_crit", tc.crit)
		}
		if tc.emerg != "" {
			write(t, stem+"_emergency", tc.emerg)
		}
		if tc.max != "" {
			write(t, stem+"_max", tc.max)
		}
	}
	write(t, filepath.Join(hwmon, "energy1_label"), "card")
	write(t, filepath.Join(hwmon, "energy1_input"), "1000000000") // 1000 J
	write(t, filepath.Join(hwmon, "energy2_label"), "pkg")
	write(t, filepath.Join(hwmon, "energy2_input"), "400000000") // 400 J
	write(t, filepath.Join(hwmon, "power1_cap"), "275000000")
	write(t, filepath.Join(hwmon, "power1_crit"), "550000000")
	write(t, filepath.Join(hwmon, "power1_cap_interval"), "15")

	// Two GTs: gt0 render/compute, gt1 media.
	for _, gt := range []struct{ name, cur, act, rp0 string }{
		{"gt0", "1517", "0", "2800"},
		{"gt1", "1200", "1200", "1500"},
	} {
		freq := filepath.Join(devDir, "tile0", gt.name, "freq0")
		write(t, filepath.Join(freq, "cur_freq"), gt.cur)
		write(t, filepath.Join(freq, "act_freq"), gt.act)
		write(t, filepath.Join(freq, "min_freq"), "400")
		write(t, filepath.Join(freq, "max_freq"), gt.rp0)
		write(t, filepath.Join(freq, "rpn_freq"), "400")
		write(t, filepath.Join(freq, "rp0_freq"), gt.rp0)
		write(t, filepath.Join(freq, "rpe_freq"), "800")
		write(t, filepath.Join(freq, "rpa_freq"), "1200")
		write(t, filepath.Join(freq, "power_profile"), "[base]  power_saving")

		thr := filepath.Join(freq, "throttle")
		for _, f := range []string{"status", "reason_pl1", "reason_pl2", "reason_pl4",
			"reason_thermal", "reason_vr_thermalert", "reason_vr_tdc",
			"reason_prochot", "reason_ratl"} {
			write(t, filepath.Join(thr, f), "0")
		}
		write(t, filepath.Join(thr, "reasons"), "none")

		idle := filepath.Join(devDir, "tile0", gt.name, "gtidle")
		write(t, filepath.Join(idle, "idle_residency_ms"), "1000")
		write(t, filepath.Join(idle, "idle_status"), "gt-c6")
		write(t, filepath.Join(idle, "name"), gt.name+"-rc")
	}

	// The bus view is a symlink into the device hierarchy, as on a real system.
	root := filepath.Join(tmp, "bus", "pci", "devices")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(devDir, filepath.Join(root, bdf)); err != nil {
		t.Fatal(err)
	}

	return &fakeCard{root: root, devDir: devDir, hwmon: hwmon, bdf: bdf}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestSysfsCollectTemps(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)

	if !st.Available {
		t.Fatal("card not found")
	}
	if len(st.Temps) != 6 {
		t.Fatalf("expected 6 sensors, got %d", len(st.Temps))
	}

	// Lexical globbing would put temp10 before temp2; channel order matters.
	wantOrder := []string{"pkg", "vram", "mctrl", "pcie", "vram_ch_0", "vram_ch_4"}
	for i, want := range wantOrder {
		if st.Temps[i].Label != want {
			t.Errorf("sensor %d = %q, want %q (numeric ordering broken)", i, st.Temps[i].Label, want)
		}
	}

	pkg, ok := st.Temp("pkg")
	if !ok {
		t.Fatal("no pkg sensor")
	}
	if pkg.ValueC != 48 || pkg.CritC != 100 || pkg.EmergencyC != 125 || pkg.MaxC != 60 {
		t.Errorf("pkg = %+v, want 48/100/125/60 C", pkg)
	}
	// vram carries no _max; it must stay zero rather than inherit anything.
	if v, _ := st.Temp("vram"); v.MaxC != 0 {
		t.Errorf("vram MaxC = %v, want 0 (sensor has no _max)", v.MaxC)
	}

	if chans := st.VramChannels(); len(chans) != 2 {
		t.Errorf("expected 2 vram channels, got %d", len(chans))
	}
	if hot, _ := st.HottestTemp(); hot.Label != "vram" || hot.ValueC != 52 {
		t.Errorf("hottest = %+v, want vram at 52 C", hot)
	}
}

func TestSysfsPowerLimitsAndEnergy(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)

	if st.PowerLimits.CapW != 275 {
		t.Errorf("CapW = %v, want 275", st.PowerLimits.CapW)
	}
	if st.PowerLimits.CritW != 550 {
		t.Errorf("CritW = %v, want 550", st.PowerLimits.CritW)
	}
	if st.PowerLimits.CapInterval != 15 {
		t.Errorf("CapInterval = %v, want 15", st.PowerLimits.CapInterval)
	}
	// Labels, not indices: energy2 is pkg and energy1 is card.
	if st.Energy.CardJ != 1000 || st.Energy.PkgJ != 400 {
		t.Errorf("energy = %+v, want card=1000 pkg=400 J", st.Energy)
	}
	// A single sample cannot yield a rate.
	if st.CardPower.Valid {
		t.Error("CardPower valid on the first sample; needs a previous one")
	}
}

func TestSysfsDerivedPower(t *testing.T) {
	fc := newFakeCard(t)
	c := NewSysfsCollector(fc.root)
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	c.Collect(fc.bdf)

	// 40 J of card energy and 12 J of pkg over 2 s -> 20 W and 6 W.
	clock = clock.Add(2 * time.Second)
	write(t, filepath.Join(fc.hwmon, "energy1_input"), "1040000000")
	write(t, filepath.Join(fc.hwmon, "energy2_input"), "412000000")

	st := c.Collect(fc.bdf)
	if !st.CardPower.Valid || st.CardPower.Watts != 20 {
		t.Errorf("CardPower = %+v, want 20 W", st.CardPower)
	}
	if st.CardPower.WindowS != 2 {
		t.Errorf("window = %v s, want 2", st.CardPower.WindowS)
	}
	if !st.PkgPower.Valid || st.PkgPower.Watts != 6 {
		t.Errorf("PkgPower = %+v, want 6 W", st.PkgPower)
	}
}

// The energy counter can reset underneath the driver across a suspend/resume,
// and a corrupted delta can also land above any plausible draw. Neither may be
// displayed.
func TestSysfsDerivedPowerRejectsGarbage(t *testing.T) {
	t.Run("counter reset gives a negative delta", func(t *testing.T) {
		fc := newFakeCard(t)
		c := NewSysfsCollector(fc.root)
		clock := time.Unix(0, 0)
		c.now = func() time.Time { return clock }

		c.Collect(fc.bdf)
		clock = clock.Add(2 * time.Second)
		write(t, filepath.Join(fc.hwmon, "energy1_input"), "5000000") // reset to 5 J

		if st := c.Collect(fc.bdf); st.CardPower.Valid {
			t.Errorf("reported %+v across a counter reset", st.CardPower)
		}
	})

	t.Run("delta above power1_crit", func(t *testing.T) {
		fc := newFakeCard(t)
		c := NewSysfsCollector(fc.root)
		clock := time.Unix(0, 0)
		c.now = func() time.Time { return clock }

		c.Collect(fc.bdf)
		clock = clock.Add(time.Second)
		// +2000 J in 1 s = 2000 W, far above the 550 W critical limit.
		write(t, filepath.Join(fc.hwmon, "energy1_input"), "3000000000")

		if st := c.Collect(fc.bdf); st.CardPower.Valid {
			t.Errorf("reported %+v, above power1_crit", st.CardPower)
		}
	})
}

func TestSysfsGTs(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)

	if len(st.GTs) != 2 {
		t.Fatalf("expected 2 GTs, got %d", len(st.GTs))
	}
	gt0 := st.GTs[0]
	if gt0.Name != "gt0" {
		t.Errorf("first GT = %q, want gt0", gt0.Name)
	}
	if gt0.Freq.CurMHz != 1517 || gt0.Freq.Rp0MHz != 2800 || gt0.Freq.MinMHz != 400 {
		t.Errorf("gt0 freq = %+v", gt0.Freq)
	}
	// act_freq reading 0 during a c6 window is expected and must survive as 0.
	if gt0.Freq.ActMHz != 0 {
		t.Errorf("gt0 ActMHz = %v, want 0", gt0.Freq.ActMHz)
	}
	if gt0.Freq.Profile != "base" {
		t.Errorf("profile = %q, want base (from \"[base]  power_saving\")", gt0.Freq.Profile)
	}
	if gt0.Throttle.Status || gt0.Throttle.AnyVR() {
		t.Errorf("gt0 throttle = %+v, want all clear", gt0.Throttle)
	}
	if gt0.Idle.Status != "gt-c6" || gt0.Idle.Name != "gt0-rc" {
		t.Errorf("gt0 idle = %+v", gt0.Idle)
	}
	if gt0.Idle.BusyValid {
		t.Error("busy%% valid on the first sample")
	}
}

func TestSysfsThrottleFlags(t *testing.T) {
	fc := newFakeCard(t)
	thr := filepath.Join(fc.devDir, "tile0", "gt0", "freq0", "throttle")
	write(t, filepath.Join(thr, "status"), "1")
	write(t, filepath.Join(thr, "reason_vr_tdc"), "1")
	write(t, filepath.Join(thr, "reasons"), "vr_tdc")

	st := NewSysfsCollector(fc.root).Collect(fc.bdf)
	gt0 := st.GTs[0]

	if !gt0.Throttle.Status || !gt0.Throttle.VRTdc || !gt0.Throttle.AnyVR() {
		t.Errorf("gt0 throttle = %+v, want VR TDC asserted", gt0.Throttle)
	}
	if gt0.Throttle.Thermal || gt0.Throttle.PL1 {
		t.Errorf("unrelated flags set: %+v", gt0.Throttle)
	}
	if gt0.Throttle.Reasons != "vr_tdc" {
		t.Errorf("reasons = %q", gt0.Throttle.Reasons)
	}
	if got := st.ThrottledGTs(); len(got) != 1 || got[0] != "gt0" {
		t.Errorf("ThrottledGTs = %v, want [gt0]", got)
	}
}

// Busy % comes from the change in idle residency against wall time. The numbers
// here are the card1/gt0 case.
func TestSysfsIdleBusyPercent(t *testing.T) {
	fc := newFakeCard(t)
	c := NewSysfsCollector(fc.root)
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	c.Collect(fc.bdf)

	clock = clock.Add(2 * time.Second)
	idle := filepath.Join(fc.devDir, "tile0", "gt0", "gtidle", "idle_residency_ms")
	write(t, idle, "2650") // idled 1650 ms of 2000 ms -> 17.5% busy

	st := c.Collect(fc.bdf)
	if !st.GTs[0].Idle.BusyValid {
		t.Fatal("busy%% not valid after a second sample")
	}
	if got := st.GTs[0].Idle.BusyPercent; got != 17.5 {
		t.Errorf("busy = %.1f%%, want 17.5%%", got)
	}
}

func TestSysfsIdleRejectsCounterReset(t *testing.T) {
	fc := newFakeCard(t)
	c := NewSysfsCollector(fc.root)
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	c.Collect(fc.bdf)
	clock = clock.Add(2 * time.Second)
	write(t, filepath.Join(fc.devDir, "tile0", "gt0", "gtidle", "idle_residency_ms"), "10")

	if st := c.Collect(fc.bdf); st.GTs[0].Idle.BusyValid {
		t.Errorf("reported busy%% = %v across a counter reset", st.GTs[0].Idle.BusyPercent)
	}
}

// The GPU function reports 2.5 GT/s x1 because it sits behind the card's own
// PCIe switch. Reporting that makes every user think the card is mis-seated.
func TestSysfsPcieWalksToHostLink(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)

	if !st.Pcie.Resolved {
		t.Fatal("PCIe link not resolved")
	}
	if st.Pcie.SpeedGTs != 8.0 || st.Pcie.Width != 16 {
		t.Errorf("host link = %.1f GT/s x%d, want 8.0 x16", st.Pcie.SpeedGTs, st.Pcie.Width)
	}
	if st.Pcie.SourceBDF != "0000:17:00.0" {
		t.Errorf("resolved from %q, want the host-facing port 0000:17:00.0 "+
			"(the x1 switch port must be walked past)", st.Pcie.SourceBDF)
	}
	if st.Pcie.MaxSpeedGTs != 16.0 || st.Pcie.MaxWidth != 16 {
		t.Errorf("max = %.1f GT/s x%d, want 16.0 x16", st.Pcie.MaxSpeedGTs, st.Pcie.MaxWidth)
	}
	// The misleading local values stay available for the inspector.
	if st.Pcie.LocalSpeed != 2.5 || st.Pcie.LocalWidth != 1 {
		t.Errorf("local = %.1f x%d, want the GPU function's own 2.5 x1",
			st.Pcie.LocalSpeed, st.Pcie.LocalWidth)
	}
}

func TestSysfsRuntimePM(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)

	if !st.PM.Suspended() {
		t.Errorf("PM = %+v, want suspended", st.PM)
	}
	if st.PM.ActiveMs != 71867972 || st.PM.SuspendedMs != 54901 {
		t.Errorf("PM times = %+v", st.PM)
	}
	if st.PM.AutosuspendDelayMs != 1000 {
		t.Errorf("autosuspend delay = %d, want 1000", st.PM.AutosuspendDelayMs)
	}
}

func TestSysfsMissingCard(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect("0000:ff:00.0")

	if st.Available {
		t.Error("reported a card that does not exist")
	}
	if st.TempByLabel == nil {
		t.Error("TempByLabel must be non-nil so callers can index it safely")
	}
	if _, ok := st.Temp("pkg"); ok {
		t.Error("returned a sensor for a missing card")
	}
}

// Attributes vary by driver version, so a partial tree must degrade rather than
// fail the whole collection.
func TestSysfsToleratesMissingAttributes(t *testing.T) {
	fc := newFakeCard(t)
	os.RemoveAll(filepath.Join(fc.devDir, "tile0", "gt0", "freq0", "throttle"))
	os.Remove(filepath.Join(fc.hwmon, "power1_crit"))
	os.RemoveAll(filepath.Join(fc.devDir, "power"))

	st := NewSysfsCollector(fc.root).Collect(fc.bdf)
	if !st.Available {
		t.Fatal("collection failed outright on a partial tree")
	}
	if len(st.Temps) != 6 {
		t.Errorf("temps lost: got %d", len(st.Temps))
	}
	if st.PowerLimits.CapW != 275 {
		t.Errorf("cap lost when crit was missing: %v", st.PowerLimits.CapW)
	}
	if st.GTs[0].Throttle.Status {
		t.Error("throttle asserted from a missing directory")
	}
}

func TestSysfsAcceptsBDFWithoutDomain(t *testing.T) {
	fc := newFakeCard(t)
	if st := NewSysfsCollector(fc.root).Collect("19:00.0"); !st.Available {
		t.Error("did not resolve a BDF given without the 0000: domain prefix")
	}
}

func TestSysfsConnectedDisplays(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)
	if len(st.Displays) != 1 || st.Displays[0] != "DP-7" {
		t.Errorf("Displays = %v, want [DP-7] (the card prefix dropped, disconnected ignored)", st.Displays)
	}
}

// newFakeI915Card builds the sysfs shape of an Arc Pro A40 (DG2, i915), as
// recorded from the real card: a flat drm/cardN/gt/gt0/ in place of xe's
// tile*/gt*/freq0/, and a hwmon with unlabelled temp1/energy1 and power1_max.
func newFakeI915Card(t *testing.T) *fakeCard {
	t.Helper()
	tmp := t.TempDir()
	bdf := "0000:b5:00.0"
	devDir := filepath.Join(tmp, "devices", "pci0000:b0", bdf)
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "bus")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(devDir, filepath.Join(root, bdf)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "drivers", "i915"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "drivers", "i915"), filepath.Join(devDir, "driver")); err != nil {
		t.Fatal(err)
	}

	hwmon := filepath.Join(devDir, "hwmon", "hwmon3")
	write(t, filepath.Join(hwmon, "temp1_input"), "62000")
	write(t, filepath.Join(hwmon, "energy1_input"), "3453613")
	write(t, filepath.Join(hwmon, "power1_max"), "35000000")

	gt := filepath.Join(devDir, "drm", "card1", "gt", "gt0")
	write(t, filepath.Join(gt, "rc6_residency_ms"), "1000")
	write(t, filepath.Join(gt, "rps_cur_freq_mhz"), "600")
	write(t, filepath.Join(gt, "rps_act_freq_mhz"), "0")
	write(t, filepath.Join(gt, "rps_min_freq_mhz"), "300")
	write(t, filepath.Join(gt, "rps_max_freq_mhz"), "2450")
	write(t, filepath.Join(gt, "rps_RPn_freq_mhz"), "300")
	write(t, filepath.Join(gt, "rps_RP0_freq_mhz"), "2450")
	write(t, filepath.Join(gt, "rps_RP1_freq_mhz"), "600")
	write(t, filepath.Join(gt, "throttle_reason_status"), "1")
	write(t, filepath.Join(gt, "throttle_reason_pl1"), "1")
	write(t, filepath.Join(gt, "throttle_reason_thermal"), "0")
	write(t, filepath.Join(devDir, "power", "runtime_status"), "active")

	return &fakeCard{root: root, devDir: devDir, hwmon: hwmon, bdf: bdf}
}

func TestSysfsI915(t *testing.T) {
	fc := newFakeI915Card(t)
	c := NewSysfsCollector(fc.root)
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	c.Collect(fc.bdf)
	clock = clock.Add(2 * time.Second)
	write(t, filepath.Join(fc.devDir, "drm", "card1", "gt", "gt0", "rc6_residency_ms"), "2650")
	write(t, filepath.Join(fc.hwmon, "energy1_input"), "37453613") // +34 J over 2 s
	st := c.Collect(fc.bdf)

	if !st.Available {
		t.Fatal("card not available")
	}
	if st.Driver != "i915" {
		t.Errorf("driver = %q, want i915", st.Driver)
	}
	if temp, ok := st.Temp("pkg"); !ok || temp.ValueC != 62 {
		t.Errorf("pkg temp = %+v, %v; want 62 C from the unlabelled temp1_input", temp, ok)
	}
	if st.PowerLimits.CapW != 35 {
		t.Errorf("cap = %v W, want 35 (power1_max)", st.PowerLimits.CapW)
	}
	if !st.CardPower.Valid || st.CardPower.Watts != 17 {
		t.Errorf("card power = %+v, want 17 W from the unlabelled energy1_input", st.CardPower)
	}
	if len(st.GTs) != 1 {
		t.Fatalf("got %d GTs, want 1", len(st.GTs))
	}
	gt := st.GTs[0]
	if gt.Name != "gt0" || gt.Freq.CurMHz != 600 || gt.Freq.MaxMHz != 2450 || gt.Freq.RpeMHz != 600 {
		t.Errorf("gt = %+v", gt)
	}
	if !gt.Idle.BusyValid || gt.Idle.BusyPercent != 17.5 {
		t.Errorf("busy = %+v, want 17.5%% from rc6 residency", gt.Idle)
	}
	if !gt.Throttle.Status || !gt.Throttle.PL1 || gt.Throttle.Thermal || gt.Throttle.Reasons != "pl1" {
		t.Errorf("throttle = %+v", gt.Throttle)
	}
}

// The xe layout must not be mistaken for i915 just because tile* is missing
// something: a card with tile directories keeps reading them.
func TestSysfsXeIsNotReportedAsI915(t *testing.T) {
	fc := newFakeCard(t)
	st := NewSysfsCollector(fc.root).Collect(fc.bdf)
	if st.Driver == "i915" {
		t.Errorf("driver = %q for an xe card", st.Driver)
	}
	if len(st.GTs) == 0 || st.GTs[0].Tile != "tile0" {
		t.Errorf("xe GTs = %+v", st.GTs)
	}
}
