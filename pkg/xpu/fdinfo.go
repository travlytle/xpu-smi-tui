package xpu

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DrmEngines are the xe engine classes, in the order they are worth reading.
//
// ccs is the one that matters for inference: compute. rcs is 3D render, bcs the
// blitter (host<->VRAM copies), vcs/vecs video decode and encode/enhance.
var DrmEngines = []string{"ccs", "rcs", "bcs", "vcs", "vecs"}

// EngineUtil is one engine's busy fraction across a sampling interval.
type EngineUtil struct {
	Name        string
	BusyPercent float64
	Valid       bool
}

// ProcessGPUMemory is one process's memory footprint on one card.
//
// SystemBytes is host memory the driver holds for the client. It is NOT a
// measure of VRAM overflow: every DRM client carries a baseline of about 184 MiB
// here regardless of what it does -- measured on an xpu-smi process with 2 MiB
// of VRAM and no compute at all. An earlier version of this tool labelled the
// sum as "spill", which reported a gigabyte of spill on idle cards.
type ProcessGPUMemory struct {
	VramBytes         int64
	ResidentVramBytes int64
	SharedVramBytes   int64
	GttBytes          int64
	SystemBytes       int64
}

// DrmProcess is one process's use of one card.
type DrmProcess struct {
	PID     int
	Command string
	Bdf     string
	Memory  ProcessGPUMemory
	Clients int // how many DRM fds this process holds on the card

	// Model and Port are parsed from the command line when the process is a
	// model server. llama.cpp containers typically report their command
	// as /app/llama-server, so the binary name alone cannot tell two of them
	// apart; the model file it was started with can.
	Model string
	Port  string
}

// FdinfoStats is what /proc/<pid>/fdinfo tells us about one card.
type FdinfoStats struct {
	Available bool
	Engines   []EngineUtil
	Processes []DrmProcess
}

// Engine returns the named engine's utilization.
func (f *FdinfoStats) Engine(name string) (EngineUtil, bool) {
	for _, e := range f.Engines {
		if e.Name == name {
			return e, true
		}
	}
	return EngineUtil{}, false
}

// BusiestEngine returns the engine doing the most work.
func (f *FdinfoStats) BusiestEngine() (EngineUtil, bool) {
	var best EngineUtil
	found := false
	for _, e := range f.Engines {
		if e.Valid && (!found || e.BusyPercent > best.BusyPercent) {
			best, found = e, true
		}
	}
	return best, found
}

type engineCounters struct {
	cycles   uint64
	total    uint64
	capacity uint64

	// busyNs is the i915 form of the same measurement: cumulative nanoseconds
	// of engine time, with no shared clock to divide by. timed marks that the
	// counter was present, since a real 0 and an absent key look alike.
	busyNs uint64
	timed  bool
}

// i915EngineClass maps the class names i915 uses in drm-engine-<class> keys
// onto the xe-style names the rest of the tool is keyed on.
var i915EngineClass = map[string]string{
	"render":        "rcs",
	"copy":          "bcs",
	"video":         "vcs",
	"video-enhance": "vecs",
	"compute":       "ccs",
}

type fdinfoSample struct {
	engines map[string]engineCounters
	at      time.Time
}

// FdinfoCollector reads per-client DRM counters from /proc.
//
// It must be reused across polls: engine utilization is a delta of two
// monotonic cycle counters, so a fresh collector reports nothing on its first
// call.
type FdinfoCollector struct {
	now func() time.Time

	// procRoot is /proc in production; tests point it at a fake tree.
	procRoot string
	// selfPID is skipped during the walk; a field so tests with a fake tree
	// cannot collide with the test binary's real pid.
	selfPID int
	// privileged reports whether every process's fds can be read.
	privileged bool

	mu   sync.Mutex
	last map[string]fdinfoSample // keyed by BDF
}

func NewFdinfoCollector() *FdinfoCollector {
	return &FdinfoCollector{
		now:        time.Now,
		procRoot:   "/proc",
		selfPID:    os.Getpid(),
		privileged: os.Geteuid() == 0,
		last:       make(map[string]fdinfoSample),
	}
}

// parseSize parses an fdinfo memory value, which carries a unit ("25983120 KiB",
// "798 MiB") or is a bare zero.
func parseSize(v string) int64 {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	if len(fields) == 1 {
		return n // bare number, already bytes (only ever 0 in practice)
	}
	switch strings.ToLower(fields[1]) {
	case "kib":
		return n * 1024
	case "mib":
		return n * 1024 * 1024
	case "gib":
		return n * 1024 * 1024 * 1024
	}
	return n
}

func parseCount(v string) uint64 {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return 0
	}
	n, _ := strconv.ParseUint(fields[0], 10, 64)
	return n
}

// drmFdPaths returns the fdinfo paths for a process's DRM file descriptors.
//
// Resolving the fd symlink first is much cheaper than reading every fdinfo file
// in the process and checking for a drm-pdev line.
//
// skip names device nodes ("renderD128", "card1") whose clients must not be
// read: an fdinfo read resumes the card it belongs to, so a card being left
// alone to suspend has its fds passed over. The node is known from the fd's
// link target, which is free to read.
func drmFdPaths(pidDir string, skip map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(pidDir, "fd"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(pidDir, "fd", e.Name()))
		if err != nil || !strings.HasPrefix(target, "/dev/dri/") || skip[filepath.Base(target)] {
			continue
		}
		out = append(out, filepath.Join(pidDir, "fdinfo", e.Name()))
	}
	return out, nil
}

type clientInfo struct {
	bdf string
	// id is drm-client-id: one per open DRM file. A process that dup()s its
	// DRM fd -- gnome-shell holds five on one client -- shows the identical
	// counters on every copy, so they must be counted once, not per fd.
	id      string
	memory  ProcessGPUMemory
	engines map[string]engineCounters
}

// readClient parses one fdinfo file.
func readClient(path string) (clientInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return clientInfo{}, false
	}
	defer f.Close()

	info := clientInfo{engines: make(map[string]engineCounters, len(DrmEngines))}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)

		switch {
		case key == "drm-pdev":
			info.bdf = value
		case key == "drm-client-id":
			info.id = value
		// xe names its VRAM region vram0; i915 names it local0 (local memory)
		// and suffixes the system region with 0 as well.
		case key == "drm-total-vram0", key == "drm-total-local0":
			info.memory.VramBytes = parseSize(value)
		case key == "drm-resident-vram0", key == "drm-resident-local0":
			info.memory.ResidentVramBytes = parseSize(value)
		case key == "drm-shared-vram0", key == "drm-shared-local0":
			info.memory.SharedVramBytes = parseSize(value)
		case key == "drm-total-gtt":
			info.memory.GttBytes = parseSize(value)
		case key == "drm-total-system", key == "drm-total-system0":
			info.memory.SystemBytes = parseSize(value)
		case strings.HasPrefix(key, "drm-cycles-"):
			name := strings.TrimPrefix(key, "drm-cycles-")
			e := info.engines[name]
			e.cycles = parseCount(value)
			info.engines[name] = e
		case strings.HasPrefix(key, "drm-total-cycles-"):
			name := strings.TrimPrefix(key, "drm-total-cycles-")
			e := info.engines[name]
			e.total = parseCount(value)
			info.engines[name] = e
		case strings.HasPrefix(key, "drm-engine-capacity-"):
			name := engineName(strings.TrimPrefix(key, "drm-engine-capacity-"))
			e := info.engines[name]
			e.capacity = parseCount(value)
			info.engines[name] = e
		case strings.HasPrefix(key, "drm-engine-"):
			// i915: "drm-engine-render: 123456 ns".
			name := engineName(strings.TrimPrefix(key, "drm-engine-"))
			e := info.engines[name]
			e.busyNs = parseCount(value)
			e.timed = true
			info.engines[name] = e
		}
	}

	if err := scanner.Err(); err != nil {
		// A partially read client would under-report its VRAM with no sign.
		return clientInfo{}, false
	}
	return info, info.bdf != ""
}

// engineName maps an engine class as a driver spells it to the xe-style name
// used throughout; names already in that form pass through.
func engineName(class string) string {
	if name, ok := i915EngineClass[class]; ok {
		return name
	}
	return class
}

// Collect walks /proc and returns per-card engine utilization and per-process
// memory, keyed by PCI address.
//
// complete reports whether every process could be inspected. Another user's
// file descriptors are unreadable without root, and on a machine with a display
// those include the compositor: the login screen's gnome-shell and Xwayland run
// as gdm-greeter. When complete is false the processes returned are only the
// ones this user could see, and memory they do not account for is still in use.
//
// A process holds DRM fds on every card it can see, not just the one it
// allocates on, and it can hold several on the same card -- so clients are
// grouped by (pid, drm-pdev) and summed. Taking the first matching fd
// undercounts; ignoring drm-pdev attributes one card's memory to another.
func (c *FdinfoCollector) Collect() (stats map[string]*FdinfoStats, complete bool) {
	return c.CollectExcluding(nil)
}

// CollectExcluding is Collect without reading the clients of the named device
// nodes; see drmFdPaths.
func (c *FdinfoCollector) CollectExcluding(skip map[string]bool) (stats map[string]*FdinfoStats, complete bool) {
	procs, err := filepath.Glob(filepath.Join(c.procRoot, "[0-9]*"))
	if err != nil {
		return nil, false
	}
	// "Complete" means every process on the machine could be inspected, which
	// is exactly "running as root". Inferring it from whether any read was
	// denied made it permanently false -- hundreds of root-owned daemons deny
	// the read and none are GPU clients -- so the caveat was always on and said
	// nothing.
	complete = c.privileged

	type key struct {
		pid int
		bdf string
	}
	perProcess := make(map[key]*DrmProcess)
	// Clients already counted, so a dup'd (or fork-inherited) fd is not added
	// twice. Keyed by card too: ids are only unique within a driver instance.
	counted := make(map[[2]string]bool)
	perCard := make(map[string]map[string]engineCounters)

	for _, pidDir := range procs {
		pid, err := strconv.Atoi(filepath.Base(pidDir))
		if err != nil || pid == c.selfPID {
			continue
		}
		paths, err := drmFdPaths(pidDir, skip)
		if err != nil || len(paths) == 0 {
			continue
		}
		command := readProcComm(pidDir)
		if isOwnHelper(command) {
			// The collector's own xpu-smi invocations open a DRM client on every
			// card for the length of the call. Counting them made the tool
			// report itself: each carries ~184 MiB of host memory and appears as
			// a workload on both cards.
			continue
		}
		model, port := serverIdentity(c.procRoot, pidDir)

		for _, path := range paths {
			client, ok := readClient(path)
			if !ok {
				continue
			}

			k := key{pid: pid, bdf: client.bdf}
			p, exists := perProcess[k]
			if !exists {
				p = &DrmProcess{PID: pid, Command: command, Bdf: client.bdf, Model: model, Port: port}
				perProcess[k] = p
			}
			p.Clients++
			if client.id != "" {
				id := [2]string{client.bdf, client.id}
				if counted[id] {
					continue
				}
				counted[id] = true
			}
			p.Memory.VramBytes += client.memory.VramBytes
			p.Memory.ResidentVramBytes += client.memory.ResidentVramBytes
			p.Memory.SharedVramBytes += client.memory.SharedVramBytes
			p.Memory.GttBytes += client.memory.GttBytes
			p.Memory.SystemBytes += client.memory.SystemBytes

			card := perCard[client.bdf]
			if card == nil {
				card = make(map[string]engineCounters, len(DrmEngines))
				perCard[client.bdf] = card
			}
			for name, e := range client.engines {
				agg := card[name]
				// Cycles are per-client and additive. The total-cycles counter is
				// the same clock domain for every client on the card, so it is a
				// ceiling to take the max of, not a quantity to sum -- summing it
				// would divide by N times too much and report 1/N of the real
				// utilization.
				agg.cycles += e.cycles
				agg.busyNs += e.busyNs
				agg.timed = agg.timed || e.timed
				if e.total > agg.total {
					agg.total = e.total
				}
				if e.capacity > agg.capacity {
					agg.capacity = e.capacity
				}
				card[name] = agg
			}
		}
	}

	now := c.now()
	result := make(map[string]*FdinfoStats, len(perCard))
	for bdf, engines := range perCard {
		result[bdf] = &FdinfoStats{
			Available: true,
			Engines:   c.engineUtil(bdf, engines, now),
		}
	}
	for k, p := range perProcess {
		// Every client created its card's entry above, so the key is present.
		result[k.bdf].Processes = append(result[k.bdf].Processes, *p)
	}
	for _, stats := range result {
		sort.Slice(stats.Processes, func(i, j int) bool {
			return stats.Processes[i].Memory.VramBytes > stats.Processes[j].Memory.VramBytes
		})
	}

	return result, complete
}

// engineUtil turns the cycle counters into busy percentages against the
// previous sample.
func (c *FdinfoCollector) engineUtil(bdf string, engines map[string]engineCounters, now time.Time) []EngineUtil {
	c.mu.Lock()
	prev, had := c.last[bdf]
	snapshot := make(map[string]engineCounters, len(engines))
	for k, v := range engines {
		snapshot[k] = v
	}
	c.last[bdf] = fdinfoSample{engines: snapshot, at: now}
	c.mu.Unlock()

	out := make([]EngineUtil, 0, len(DrmEngines))
	for _, name := range DrmEngines {
		cur, present := engines[name]
		if !present {
			continue
		}
		util := EngineUtil{Name: name}

		if had {
			if before, ok := prev.engines[name]; ok && cur.timed && before.timed {
				// i915: engine-time nanoseconds against wall-clock time. Both
				// the counter and the interval are summed across the class's
				// instances, so capacity divides it back to a per-engine share.
				dBusy := int64(cur.busyNs) - int64(before.busyNs)
				dWall := now.Sub(prev.at).Nanoseconds()
				if dBusy >= 0 && dWall > 0 {
					capacity := float64(cur.capacity)
					if capacity < 1 {
						capacity = 1
					}
					util.BusyPercent = clampPercent(float64(dBusy) / float64(dWall) / capacity * 100.0)
					util.Valid = true
				}
			} else if before, ok := prev.engines[name]; ok {
				dCycles := int64(cur.cycles) - int64(before.cycles)
				dTotal := int64(cur.total) - int64(before.total)
				// A client exiting can make the summed cycles go backwards.
				if dCycles >= 0 && dTotal > 0 {
					capacity := float64(cur.capacity)
					if capacity < 1 {
						capacity = 1
					}
					util.BusyPercent = clampPercent(float64(dCycles) / float64(dTotal) / capacity * 100.0)
					util.Valid = true
				}
			}
		}
		out = append(out, util)
	}
	return out
}

func readProcComm(pidDir string) string {
	data, err := os.ReadFile(filepath.Join(pidDir, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// isOwnHelper reports whether a process is one this tool spawned to collect data.
func isOwnHelper(command string) bool {
	return command == "xpu-smi"
}

func readProcCmdline(pidDir string) []string {
	data, err := os.ReadFile(filepath.Join(pidDir, "cmdline"))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
}

// parseServerArgs pulls the model file and listening port from a model server's
// arguments. It understands the llama.cpp and vLLM spellings; anything else
// yields empty strings.
func parseServerArgs(args []string) (model, port string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}

		key, value, inline := strings.Cut(arg, "=")
		if !inline {
			value = next
		}

		switch key {
		case "-m", "--model":
			if model == "" && value != "" {
				model = modelName(value)
			}
		case "--port":
			if port == "" {
				port = value
			}
		}
		// vLLM takes the model as a positional after "serve".
		if arg == "serve" && next != "" && !strings.HasPrefix(next, "-") && model == "" {
			model = modelName(next)
		}
	}
	return model, port
}

// modelName shortens a model path to something that fits in a table cell:
// "/models/Qwen3.8-27B-Q4_K_M.gguf" becomes "Qwen3.8-27B-Q4_K_M".
func modelName(path string) string {
	name := filepath.Base(strings.TrimRight(path, "/"))
	return strings.TrimSuffix(name, ".gguf")
}

// serverIdentity finds the model and port for a process, looking at its parents
// when its own command line has been rewritten.
//
// vLLM's engine runs in a child that sets its process title to
// "VLLM::EngineCore", which replaces its argv -- and that child is the one that
// holds the VRAM. The model name survives only on the parent "vllm serve"
// process, so a couple of levels are searched.
func serverIdentity(procRoot, pidDir string) (model, port string) {
	dir := pidDir
	for depth := 0; depth < 3; depth++ {
		m, p := parseServerArgs(readProcCmdline(dir))
		if model == "" {
			model = m
		}
		if port == "" {
			port = p
		}
		if model != "" {
			return model, port
		}
		ppid := parentPID(dir)
		if ppid <= 1 {
			break
		}
		dir = filepath.Join(procRoot, strconv.Itoa(ppid))
	}
	return model, port
}

func parentPID(pidDir string) int {
	data, err := os.ReadFile(filepath.Join(pidDir, "status"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "PPid:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(rest))
			return n
		}
	}
	return 0
}
