package xpu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeCounter struct {
	v   uint64
	err error
}

func (f *fakeCounter) read() (uint64, error) { return f.v, f.err }
func (f *fakeCounter) close()                {}

// fakeIio builds the sysfs a Skylake-X box shows: uncore_iio_1 sits behind
// root bus 16, and the card's PCI path runs through pci0000:16.
type fakeIio struct {
	c        *IioCollector
	counters map[uint64]*fakeCounter // by perf config
	opened   int
	openErr  error
	clock    time.Time
}

func newFakeIio(t *testing.T) *fakeIio {
	t.Helper()
	tmp := t.TempDir()
	f := &fakeIio{counters: map[uint64]*fakeCounter{}, clock: time.Unix(1000, 0)}

	dev := filepath.Join(tmp, "devices", "pci0000:16", "0000:16:00.0", "0000:19:00.0")
	write(t, filepath.Join(dev, "vendor"), "0x8086")
	if err := os.MkdirAll(filepath.Join(tmp, "pci"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dev, filepath.Join(tmp, "pci", "0000:19:00.0")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tmp, "devices", "uncore_iio_1", "die0"), "0000:16")
	write(t, filepath.Join(tmp, "devices", "uncore_iio_2", "die0"), "0000:64")

	pmu := filepath.Join(tmp, "events", "uncore_iio_free_running_1")
	write(t, filepath.Join(pmu, "type"), "46")
	write(t, filepath.Join(pmu, "cpumask"), "0")
	for port := 0; port < 4; port++ {
		for _, d := range []struct {
			name  string
			umask string
		}{{"in", "0x2" + string(rune('0'+port))}, {"out", "0x2" + string(rune('4'+port))}} {
			write(t, filepath.Join(pmu, "events", "bw_"+d.name+"_port"+string(rune('0'+port))),
				"event=0xff,umask="+d.umask)
			write(t, filepath.Join(pmu, "events", "bw_"+d.name+"_port"+string(rune('0'+port))+".scale"), "3.814697266e-6")
		}
	}

	f.c = NewIioCollector()
	f.c.pciRoot, f.c.devices, f.c.eventRoot = filepath.Join(tmp, "pci"), filepath.Join(tmp, "devices"), filepath.Join(tmp, "events")
	f.c.now = func() time.Time { return f.clock }
	f.c.open = func(typ uint32, config uint64, cpu int) (iioCounter, error) {
		if f.openErr != nil {
			return nil, f.openErr
		}
		if typ != 46 {
			t.Errorf("opened PMU type %d, want 46 (stack 1)", typ)
		}
		f.opened++
		ctr := &fakeCounter{}
		f.counters[config] = ctr
		return ctr, nil
	}
	return f
}

// add advances every counter of one direction by n counts, spread over ports.
func (f *fakeIio) add(dir string, n uint64) {
	umask := map[string]uint64{"in": 0x20, "out": 0x24}[dir]
	for p := uint64(0); p < 4; p++ {
		f.counters[0xff|(umask+p)<<8].v += n / 4
	}
}

func TestIioRatesAreBytesPerSecondFromCountDeltas(t *testing.T) {
	f := newFakeIio(t)
	const bdf = "0000:19:00.0"

	if _, ok := f.c.Collect(bdf); ok {
		t.Fatal("a rate reported on the first sample")
	}
	f.clock = f.clock.Add(2 * time.Second)
	f.add("out", 2_000_000) // 2M counts x 4 B / 2 s = 4 MB/s host->GPU
	f.add("in", 500_000)
	got, ok := f.c.Collect(bdf)
	if !ok {
		t.Fatal("no rate on the second sample")
	}
	if got.HostToGPUBytesPerSec != 4_000_000 || got.GPUToHostBytesPerSec != 1_000_000 {
		t.Errorf("rates = %+v, want 4e6 out / 1e6 in", got)
	}
	if f.opened != 8 {
		t.Errorf("opened %d counters, want 8 (4 ports x in/out)", f.opened)
	}
}

// The wrong direction reads near zero, and a stack that isn't the card's is
// never opened: the config used is the one parsed from the event file.
func TestIioParsesEventConfigFromSysfs(t *testing.T) {
	if got, ok := parseIioConfig("event=0xff,umask=0x24"); !ok || got != 0x24ff {
		t.Errorf("config = %#x, %v; want 0x24ff", got, ok)
	}
	if _, ok := parseIioConfig("garbage"); ok {
		t.Error("parsed garbage")
	}
}

// Without CAP_PERFMON the open fails with EACCES. That must not be retried on
// every poll, and must not leave a half-open card behind.
func TestIioOpenFailureBacksOff(t *testing.T) {
	f := newFakeIio(t)
	f.openErr = errors.New("permission denied")
	const bdf = "0000:19:00.0"

	attempts := 0
	inner := f.c.open
	f.c.open = func(typ uint32, cfg uint64, cpu int) (iioCounter, error) {
		attempts++
		return inner(typ, cfg, cpu)
	}
	for i := 0; i < 5; i++ {
		if _, ok := f.c.Collect(bdf); ok {
			t.Fatal("reported a rate with unopenable counters")
		}
		f.clock = f.clock.Add(time.Second)
	}
	if attempts != 1 {
		t.Errorf("%d open attempts in 5 s, want 1 (backoff %v)", attempts, iioBackoff)
	}

	// Privilege appears (or the sysctl is changed): it recovers after the backoff.
	f.openErr = nil
	f.clock = f.clock.Add(iioBackoff)
	f.c.Collect(bdf)
	f.clock = f.clock.Add(time.Second)
	f.add("out", 400)
	if _, ok := f.c.Collect(bdf); !ok {
		t.Error("did not recover once the counters could be opened")
	}
}

func TestIioDropsASampleWhenACounterGoesBackwards(t *testing.T) {
	f := newFakeIio(t)
	const bdf = "0000:19:00.0"
	f.c.Collect(bdf)
	f.clock = f.clock.Add(time.Second)
	f.add("out", 4000)
	f.c.Collect(bdf)
	for _, ctr := range f.counters {
		ctr.v = 0 // a reset
	}
	f.clock = f.clock.Add(time.Second)
	if _, ok := f.c.Collect(bdf); ok {
		t.Error("reported a rate across a counter reset")
	}
}

func TestIioUnknownCardIsNotAnError(t *testing.T) {
	f := newFakeIio(t)
	if _, ok := f.c.Collect("0000:99:00.0"); ok {
		t.Error("a card with no IIO stack reported a rate")
	}
}

// PCIe from the counters replaces whatever the driver reported, and a measured
// idle (zero) is a real reading.
func TestApplyPcieFromCounters(t *testing.T) {
	f := newFakeIio(t)
	c := &LiveCollector{iio: f.c}
	const bdf = "0000:19:00.0"

	c.applyPcieFromCounters(&GPUStats{PciBdf: bdf}) // baseline
	f.clock = f.clock.Add(time.Second)
	f.add("out", 1_048_576)                                            // 4 MiB in 1 s
	s := &GPUStats{PciBdf: bdf, PcieReadKBps: 999, PcieWriteKBps: 999} // the driver's inflated figures
	c.applyPcieFromCounters(s)
	if !s.PcieMeasured || s.PcieReadKBps != 4096 || s.PcieWriteKBps != 0 {
		t.Fatalf("stats = read %v write %v measured %v, want 4096 / 0 / true", s.PcieReadKBps, s.PcieWriteKBps, s.PcieMeasured)
	}

	// Idle next second: an honest zero.
	f.clock = f.clock.Add(time.Second)
	idle := &GPUStats{PciBdf: bdf, PcieReadKBps: 999}
	c.applyPcieFromCounters(idle)
	if !idle.PcieMeasured || idle.PcieReadKBps != 0 {
		t.Errorf("idle = %+v, want a measured zero", idle)
	}

	// No counters (no privilege, or not this platform): the card keeps what
	// the driver gave it, untouched and not marked measured.
	f.openErr = errors.New("permission denied")
	c2 := &LiveCollector{iio: NewIioCollector()}
	c2.iio.pciRoot, c2.iio.devices, c2.iio.eventRoot = f.c.pciRoot, f.c.devices, f.c.eventRoot
	c2.iio.open = f.c.open
	c2.iio.now = f.c.now
	kept := &GPUStats{PciBdf: bdf, PcieReadKBps: 7, PcieMeasured: true}
	c2.applyPcieFromCounters(kept)
	if kept.PcieReadKBps != 7 {
		t.Errorf("overwrote the driver's figure without a reading: %+v", kept)
	}
}
