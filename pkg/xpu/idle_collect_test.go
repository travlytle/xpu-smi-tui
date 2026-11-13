package xpu

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeSource stands in for Sysman and counts how often the device is read.
type fakeSource struct {
	mu    sync.Mutex
	calls int
	held  map[string]bool // what discovery's hold() said, by card
}

func (f *fakeSource) stats(bdf string, idx int, now time.Time) (*GPUStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return &GPUStats{DeviceIndex: idx, PciBdf: bdf, Timestamp: now, PowerW: float64(10 + f.calls)}, nil
}

func (f *fakeSource) discover(_ *SysfsCollector, hold func(string) bool, _ map[string]DeviceInfo) []DeviceInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[string]bool{}
	}
	f.held["0000:19:00.0"] = hold("0000:19:00.0")
	return []DeviceInfo{{DeviceID: 0, PciBdf: "0000:19:00.0"}}
}

func (f *fakeSource) reads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type idleRig struct {
	c     *LiveCollector
	src   *fakeSource
	fc    *fakeCard
	clock time.Time
}

func newIdleRig(t *testing.T, status string) *idleRig {
	t.Helper()
	fc := newFakeCard(t)
	write(t, filepath.Join(fc.devDir, "power", "runtime_status"), status)
	write(t, filepath.Join(fc.devDir, "power", "autosuspend_delay_ms"), "1000")

	r := &idleRig{fc: fc, src: &fakeSource{}, clock: time.Unix(2_000_000, 0)}
	c := NewLiveCollector()
	c.sysfs = NewSysfsCollector(fc.root)
	c.fdinfo.procRoot = t.TempDir() // an empty /proc: nothing to walk
	c.iio.pciRoot = fc.root         // hermetic: no real perf counters
	c.srcOnce.Do(func() {})
	c.src = r.src
	c.bdfByID = map[int]string{0: fc.bdf}
	c.now = func() time.Time { return r.clock }
	t.Cleanup(c.Close)
	r.c = c
	return r
}

func (r *idleRig) tick(t *testing.T) *GPUStats {
	t.Helper()
	r.clock = r.clock.Add(time.Second)
	st, err := r.c.CollectStats(nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	return st[0]
}

func (r *idleRig) setPM(t *testing.T, status string) {
	t.Helper()
	write(t, filepath.Join(r.fc.devDir, "power", "runtime_status"), status)
}

// The property this exists for: a card that stays suspended is read once, then
// left alone until the poll interval, and what is shown meanwhile is the last
// sample, marked idle.
func TestSuspendedCardIsNotReadBetweenIdleSamples(t *testing.T) {
	r := newIdleRig(t, "suspended")

	first := r.tick(t)
	if first.Idle || r.src.reads() != 1 {
		t.Fatalf("first look: idle=%v reads=%d, want a real sample", first.Idle, r.src.reads())
	}
	for i := 0; i < 28; i++ {
		st := r.tick(t)
		if !st.Idle {
			t.Fatalf("tick %d: not marked idle", i)
		}
		if st.PowerW != first.PowerW {
			t.Fatalf("tick %d: power %v, want the held %v", i, st.PowerW, first.PowerW)
		}
		if !st.Timestamp.Equal(first.Timestamp) {
			t.Fatalf("tick %d: timestamp moved; a held value must keep its sample time", i)
		}
	}
	if got := r.src.reads(); got != 1 {
		t.Fatalf("a suspended card was read %d times in 29 s, want 1", got)
	}

	// The poll interval elapses: exactly one wake-up sample.
	r.clock = r.clock.Add(3 * time.Second)
	if st := r.tick(t); st.Idle || r.src.reads() != 2 {
		t.Errorf("idle sample: idle=%v reads=%d, want a real second sample", st.Idle, r.src.reads())
	}
}

// A card that is awake and busy is read every tick, as before.
func TestActiveCardIsReadEveryTick(t *testing.T) {
	r := newIdleRig(t, "active")
	for i := 0; i < 10; i++ {
		if st := r.tick(t); st.Idle {
			t.Fatalf("tick %d: an active card was left alone", i)
		}
	}
	if got := r.src.reads(); got != 10 {
		t.Errorf("read %d times over 10 ticks, want 10", got)
	}
}

// The trap: our own wake-up makes the card read "active" for its autosuspend
// delay. That must not be mistaken for something else holding it.
func TestOwnWakeUpIsNotMistakenForActivity(t *testing.T) {
	r := newIdleRig(t, "suspended")
	r.tick(t) // wake sample
	r.setPM(t, "active")
	for i := 0; i < 2; i++ {
		if st := r.tick(t); !st.Idle {
			t.Fatalf("tick %d: polled a card that is awake only because we woke it", i)
		}
	}
	if r.src.reads() != 1 {
		t.Errorf("reads = %d, want 1", r.src.reads())
	}
	// Still active once the window has passed: something else does hold it.
	if st := r.tick(t); st.Idle {
		t.Error("still active after the settling window, but left alone")
	}
}

// --idle-poll off restores polling every card every tick.
func TestGateCanBeDisabled(t *testing.T) {
	r := newIdleRig(t, "suspended")
	r.c.SetIdlePolicy(IdlePolicy{Enabled: false})
	for i := 0; i < 5; i++ {
		if st := r.tick(t); st.Idle {
			t.Fatal("gate disabled but a card was held")
		}
	}
	if r.src.reads() != 5 {
		t.Errorf("reads = %d, want 5", r.src.reads())
	}
}

// Holding a card must not disturb the sample it is holding, which the UI still
// has a pointer into.
func TestHeldStatsDoNotMutateTheLastSample(t *testing.T) {
	last := &GPUStats{PciBdf: "x", PowerW: 12, Sysfs: &SysfsStats{Available: true, PM: RuntimePM{Status: "active"}}}
	held := heldStats(0, "x", last, RuntimePM{Status: "suspended"}, time.Now())
	if !held.Idle || held.PowerW != 12 || held.Sysfs.PM.Status != "suspended" {
		t.Errorf("held = %+v", held)
	}
	if last.Idle || last.Sysfs.PM.Status != "active" {
		t.Errorf("the earlier sample was modified: %+v", last)
	}
	// With nothing earlier there is still a valid, idle, empty result.
	if bare := heldStats(3, "y", nil, RuntimePM{Status: "suspended"}, time.Now()); !bare.Idle || bare.DeviceIndex != 3 || !bare.Sysfs.PM.Suspended() {
		t.Errorf("bare held = %+v", bare)
	}
}

// Rediscovery (the exporter does it every minute) must not wake a card either:
// discovery is told to hold it.
func TestDiscoveryHoldsASuspendedCard(t *testing.T) {
	r := newIdleRig(t, "suspended")
	if _, err := r.c.Discover(nil); err != nil {
		t.Fatal(err)
	}
	if !r.src.held["0000:19:00.0"] {
		t.Error("discovery was not told to leave a suspended card alone")
	}
	r.setPM(t, "active")
	if _, err := r.c.Discover(nil); err != nil {
		t.Fatal(err)
	}
	if r.src.held["0000:19:00.0"] {
		t.Error("discovery held a card that is active")
	}
}

// An fdinfo read resumes the card it belongs to, so a card being left alone
// has its DRM fds passed over -- decided from the fd's link target, which is
// free to read.
func TestFdinfoSkipsFdsOfCardsBeingLeftAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "100")
	for _, d := range []string{"fd", "fdinfo"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for fd, node := range map[string]string{"3": "renderD128", "4": "renderD129", "5": "card1"} {
		if err := os.Symlink("/dev/dri/"+node, filepath.Join(dir, "fd", fd)); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := drmFdPaths(dir, nil)
	if len(all) != 3 {
		t.Fatalf("baseline: %d paths, want 3", len(all))
	}
	got, _ := drmFdPaths(dir, map[string]bool{"renderD128": true, "card1": true})
	if len(got) != 1 || filepath.Base(got[0]) != "4" {
		t.Errorf("paths = %v, want only fd 4 (renderD129)", got)
	}
}

func TestSysfsDrmNodesAndFreePM(t *testing.T) {
	fc := newFakeCard(t)
	c := NewSysfsCollector(fc.root)
	nodes := c.DrmNodes(fc.bdf)
	if len(nodes) != 1 || nodes[0] != "card1" {
		t.Errorf("nodes = %v, want [card1] (connector directories excluded)", nodes)
	}
	if pm := c.RuntimePMFor(fc.bdf); pm.Status != "suspended" {
		t.Errorf("pm = %+v", pm)
	}
	if pm := c.RuntimePMFor("0000:ff:00.0"); pm.Status != "" {
		t.Errorf("an unknown card has PM state %+v", pm)
	}
}
