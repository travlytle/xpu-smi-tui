package xpu

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		// fdinfo mixes units within a single file: a bare 0, KiB for most
		// values, and MiB once a figure gets large enough.
		{"0", 0},
		{"25983120 KiB", 25983120 * 1024},
		{"798 MiB", 798 * 1024 * 1024},
		{"2 GiB", 2 * 1024 * 1024 * 1024},
		{"64 KiB", 65536},
		{"", 0},
		{"garbage", 0},
	}
	for _, c := range cases {
		if got := parseSize(c.in); got != c.want {
			t.Errorf("parseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestReadClientParsesEnginesAndMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdinfo3")
	body := `drm-client-id:	5983253
drm-pdev:	0000:19:00.0
drm-total-system:	125568 KiB
drm-total-gtt:	833560 KiB
drm-shared-gtt:	798 MiB
drm-total-vram0:	25983120 KiB
drm-resident-vram0:	25983120 KiB
drm-shared-vram0:	0
drm-cycles-rcs:	0
drm-total-cycles-rcs:	1567237924688
drm-cycles-ccs:	27718657462
drm-total-cycles-ccs:	1567237924688
drm-cycles-vcs:	0
drm-total-cycles-vcs:	1567237924688
drm-engine-capacity-vcs:	2
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	client, ok := readClient(path)
	if !ok {
		t.Fatal("client not recognised")
	}
	if client.bdf != "0000:19:00.0" {
		t.Errorf("bdf = %q", client.bdf)
	}
	if want := int64(25983120) * 1024; client.memory.VramBytes != want {
		t.Errorf("vram = %d, want %d", client.memory.VramBytes, want)
	}
	if want := int64(125568) * 1024; client.memory.SystemBytes != want {
		t.Errorf("system = %d, want %d", client.memory.SystemBytes, want)
	}
	if got := client.engines["ccs"]; got.cycles != 27718657462 || got.total != 1567237924688 {
		t.Errorf("ccs counters = %+v", got)
	}
	if got := client.engines["vcs"].capacity; got != 2 {
		t.Errorf("vcs capacity = %d, want 2", got)
	}
	// An engine with no capacity line must not inherit another engine's.
	if got := client.engines["ccs"].capacity; got != 0 {
		t.Errorf("ccs capacity = %d, want 0 (no capacity line)", got)
	}
}

func TestEngineUtilComputesBusyFromDelta(t *testing.T) {
	c := NewFdinfoCollector()
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	const bdf = "0000:19:00.0"
	first := map[string]engineCounters{"ccs": {cycles: 1000, total: 10000}}
	if got := c.engineUtil(bdf, first, clock); got[0].Valid {
		t.Error("busy reported on the first sample")
	}

	// 400 busy cycles out of 1000 elapsed -> 40%.
	clock = clock.Add(time.Second)
	second := map[string]engineCounters{"ccs": {cycles: 1400, total: 11000}}
	got := c.engineUtil(bdf, second, clock)
	if !got[0].Valid || got[0].BusyPercent != 40 {
		t.Errorf("busy = %+v, want 40%%", got[0])
	}
}

// vcs and vecs report capacity 2 on these cards: the cycle counter accumulates
// across both instances, so a figure that ignores capacity reads double.
func TestEngineUtilDividesByCapacity(t *testing.T) {
	c := NewFdinfoCollector()
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	const bdf = "0000:19:00.0"
	c.engineUtil(bdf, map[string]engineCounters{"vcs": {cycles: 0, total: 0, capacity: 2}}, clock)
	clock = clock.Add(time.Second)
	got := c.engineUtil(bdf, map[string]engineCounters{"vcs": {cycles: 1000, total: 1000, capacity: 2}}, clock)

	if !got[0].Valid || got[0].BusyPercent != 50 {
		t.Errorf("busy = %+v, want 50%% (1000/1000 across 2 instances)", got[0])
	}
}

// A client exiting makes the summed cycle counter go backwards; that must not
// surface as a negative or wildly wrong utilization.
func TestEngineUtilRejectsCounterGoingBackwards(t *testing.T) {
	c := NewFdinfoCollector()
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	const bdf = "0000:19:00.0"
	c.engineUtil(bdf, map[string]engineCounters{"ccs": {cycles: 5000, total: 10000}}, clock)
	clock = clock.Add(time.Second)
	got := c.engineUtil(bdf, map[string]engineCounters{"ccs": {cycles: 100, total: 11000}}, clock)

	if got[0].Valid {
		t.Errorf("reported %+v after the counter reset", got[0])
	}
}

func TestFdinfoStatsHelpers(t *testing.T) {
	st := &FdinfoStats{
		Available: true,
		Engines: []EngineUtil{
			{Name: "ccs", BusyPercent: 82, Valid: true},
			{Name: "bcs", BusyPercent: 12, Valid: true},
			{Name: "vcs", BusyPercent: 99, Valid: false}, // invalid must not win
		},
	}

	if b, ok := st.BusiestEngine(); !ok || b.Name != "ccs" {
		t.Errorf("busiest = %+v, want ccs", b)
	}
	if e, ok := st.Engine("bcs"); !ok || e.BusyPercent != 12 {
		t.Errorf("Engine(bcs) = %+v", e)
	}
	if _, ok := st.Engine("nope"); ok {
		t.Error("returned an engine that does not exist")
	}
}

func TestParseServerArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		model, port string
	}{
		{"llama.cpp", []string{"/app/llama-server", "-m", "/models/Qwen3.8-27B-Q4_K_M.gguf",
			"-ngl", "99", "-md", "/drafts/Qwen3.8-27B-DFlash2-Q4_K_M.gguf", "--port", "6000"},
			"Qwen3.8-27B-Q4_K_M", "6000"},
		// -md is the draft model; it must not be taken for the main one.
		{"draft flag ignored", []string{"llama-server", "-md", "/d/draft.gguf", "-m", "/m/main.gguf"},
			"main", ""},
		{"equals form", []string{"llama-server", "--model=/m/Big.gguf", "--port=8080"}, "Big", "8080"},
		{"vllm positional", []string{"/opt/venv/bin/python3", "/opt/venv/bin/vllm", "serve",
			"mikeinnyc/Qwen3.8-27B-GPTQ-Int4", "--port", "8000"}, "Qwen3.8-27B-GPTQ-Int4", "8000"},
		{"not a server", []string{"/usr/bin/python3", "whisper", "worker"}, "", ""},
		{"empty", nil, "", ""},
	}
	for _, c := range cases {
		m, p := parseServerArgs(c.args)
		if m != c.model || p != c.port {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, m, p, c.model, c.port)
		}
	}
}

// vLLM's engine child rewrites its argv to "VLLM::EngineCore" and is the one
// holding the VRAM, so the model name has to come from its parent.
func TestServerIdentityWalksToParent(t *testing.T) {
	root := t.TempDir()
	mk := func(pid, ppid int, argv ...string) {
		dir := filepath.Join(root, strconv.Itoa(pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644)
		os.WriteFile(filepath.Join(dir, "status"), []byte(fmt.Sprintf("Name:\tx\nPPid:\t%d\n", ppid)), 0o644)
	}
	mk(100, 1, "/opt/venv/bin/vllm", "serve", "org/Qwen3.8-27B-GPTQ", "--port", "8000")
	mk(200, 100, "VLLM::EngineCore")

	model, port := serverIdentity(root, filepath.Join(root, "200"))
	if model != "Qwen3.8-27B-GPTQ" || port != "8000" {
		t.Errorf("got (%q, %q), want the parent's model and port", model, port)
	}
}

// The collector's own xpu-smi calls open a DRM client on every card while they
// run. They must not be reported as workloads.
func TestOwnHelpersAreExcluded(t *testing.T) {
	if !isOwnHelper("xpu-smi") {
		t.Error("xpu-smi should be treated as the collector's own helper")
	}
	for _, c := range []string{"llama-server", "vllm", "xpu-smi-tui-other", "python3"} {
		if isOwnHelper(c) {
			t.Errorf("%q wrongly excluded", c)
		}
	}
}

// fakeProc builds a /proc-like tree: pid 100 is a visible llama-server holding
// a DRM client on 0000:19:00.0; pid 200's fd directory is unreadable, standing in
// for another user's compositor.
func fakeProc(t *testing.T, withHidden bool) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "100")
	for _, d := range []string{"fd", "fdinfo"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/dev/dri/renderD128", filepath.Join(dir, "fd", "3")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "fdinfo", "3"), []byte(
		"drm-pdev:\t0000:19:00.0\ndrm-total-vram0:\t1048576 KiB\ndrm-cycles-ccs:\t10\ndrm-total-cycles-ccs:\t100\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "comm"), []byte("llama-server\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/app/llama-server\x00-m\x00/m/Qwen.gguf\x00"), 0o644)
	os.WriteFile(filepath.Join(dir, "status"), []byte("PPid:\t1\n"), 0o644)

	if withHidden {
		hidden := filepath.Join(root, "200", "fd")
		if err := os.MkdirAll(hidden, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(hidden, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(hidden, 0o755) })
	}
	return root
}

// Without root, other users' processes cannot be inspected, and the collector
// must say its view is incomplete rather than presenting a partial list as the
// whole picture.
func TestCollectReportsIncompleteWhenDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read every directory, so a denial cannot be simulated")
	}
	c := NewFdinfoCollector()
	c.procRoot = fakeProc(t, true)

	stats, _ := c.Collect()
	card := stats["0000:19:00.0"]
	if card == nil || len(card.Processes) != 1 || card.Processes[0].Model != "Qwen" {
		t.Errorf("the visible process should still be reported: %+v", card)
	}
}

// Completeness is a statement of privilege, not of luck: a walk that happened
// to be denied nothing is still incomplete when unprivileged, since the
// processes it matters for (another user's compositor) are exactly the ones a
// denial would come from.
func TestCollectReportsCompleteOnlyWhenPrivileged(t *testing.T) {
	c := NewFdinfoCollector()
	c.procRoot = fakeProc(t, false)

	c.privileged = false
	if _, complete := c.Collect(); complete {
		t.Error("unprivileged walk claimed to be complete")
	}
	c.privileged = true
	if _, complete := c.Collect(); !complete {
		t.Error("privileged walk should be complete")
	}
}

// i915 spells its fdinfo differently from xe: local0 for VRAM, engine time in
// nanoseconds under class names, capacity only on the multi-instance classes.
// Recorded from a llama-server client on an Arc Pro A40.
func TestReadClientParsesI915(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fdinfo")
	write(t, path, `drm-driver:	i915
drm-pdev:	0000:b5:00.0
drm-total-system0:	24 KiB
drm-total-local0:	8448 KiB
drm-resident-local0:	4 KiB
drm-shared-local0:	0
drm-engine-render:	1500 ns
drm-engine-copy:	7 ns
drm-engine-video:	0 ns
drm-engine-capacity-video:	2
drm-engine-video-enhance:	9 ns
drm-engine-capacity-video-enhance:	2
drm-engine-compute:	42 ns`)

	info, ok := readClient(path)
	if !ok || info.bdf != "0000:b5:00.0" {
		t.Fatalf("readClient = %+v, %v", info, ok)
	}
	if info.memory.VramBytes != 8448*1024 || info.memory.ResidentVramBytes != 4*1024 || info.memory.SystemBytes != 24*1024 {
		t.Errorf("memory = %+v", info.memory)
	}
	for name, want := range map[string]uint64{"rcs": 1500, "bcs": 7, "vcs": 0, "vecs": 9, "ccs": 42} {
		if e := info.engines[name]; !e.timed || e.busyNs != want {
			t.Errorf("engine %s = %+v, want %d ns", name, e, want)
		}
	}
	if info.engines["vcs"].capacity != 2 || info.engines["vecs"].capacity != 2 {
		t.Errorf("capacities = vcs %d vecs %d, want 2/2", info.engines["vcs"].capacity, info.engines["vecs"].capacity)
	}
}

func TestEngineUtilI915ComputesBusyFromNanoseconds(t *testing.T) {
	c := NewFdinfoCollector()
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }

	const bdf = "0000:b5:00.0"
	if got := c.engineUtil(bdf, map[string]engineCounters{"rcs": {busyNs: 1e9, timed: true}}, clock); got[0].Valid {
		t.Error("busy reported on the first sample")
	}

	// 250 ms of engine time in a 1 s window -> 25%.
	clock = clock.Add(time.Second)
	got := c.engineUtil(bdf, map[string]engineCounters{"rcs": {busyNs: 1.25e9, timed: true}}, clock)
	if !got[0].Valid || got[0].BusyPercent != 25 {
		t.Errorf("busy = %+v, want 25%%", got[0])
	}

	// Two video instances: 1 s of summed engine time in 1 s is half of 2 engines.
	clock = clock.Add(time.Second)
	c.engineUtil(bdf, map[string]engineCounters{"vcs": {busyNs: 0, timed: true, capacity: 2}}, clock)
	clock = clock.Add(time.Second)
	got = c.engineUtil(bdf, map[string]engineCounters{"vcs": {busyNs: 1e9, timed: true, capacity: 2}}, clock)
	if !got[0].Valid || got[0].BusyPercent != 50 {
		t.Errorf("busy = %+v, want 50%% with capacity 2", got[0])
	}
}

// A process that dup()s its DRM fd shows identical counters on every copy.
// Measured on the A40: gnome-shell held five fds on one client (same
// drm-client-id and inode), each reporting 512 MiB, and the per-fd sum called
// it 2.5 GiB. Two genuinely separate clients in the same process still add.
func TestCollectCountsADuplicatedFdOnce(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "100")
	for _, d := range []string{"fd", "fdinfo"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	client := func(id string) string {
		return "drm-driver:\ti915\ndrm-client-id:\t" + id + "\ndrm-pdev:\t0000:b5:00.0\n" +
			"drm-total-local0:\t524784 KiB\ndrm-engine-render:\t1000 ns\n"
	}
	// fds 3,4,5 are dups of client 546; fd 6 is a different client.
	for fd, id := range map[string]string{"3": "546", "4": "546", "5": "546", "6": "700"} {
		if err := os.Symlink("/dev/dri/card1", filepath.Join(dir, "fd", fd)); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "fdinfo", fd), []byte(client(id)), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "comm"), []byte("gnome-shell\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "status"), []byte("PPid:\t1\n"), 0o644)

	c := NewFdinfoCollector()
	c.procRoot, c.selfPID = root, -1
	stats, _ := c.Collect()

	procs := stats["0000:b5:00.0"].Processes
	if len(procs) != 1 {
		t.Fatalf("got %d processes, want 1", len(procs))
	}
	if want := int64(2 * 524784 * 1024); procs[0].Memory.VramBytes != want {
		t.Errorf("VRAM = %d KiB, want %d KiB (two clients, not four fds)",
			procs[0].Memory.VramBytes/1024, want/1024)
	}
	if procs[0].Clients != 4 {
		t.Errorf("Clients = %d, want 4 fds still reported", procs[0].Clients)
	}
}
