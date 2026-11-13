package xpu

import (
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// PCIe throughput from the CPU's own counters.
//
// xpu-smi has no PCIe counters for the Arc Pro A40 (i915). The host side has
// them: on Skylake-X each PCIe root stack has a free-running IIO PMU
// (uncore_iio_free_running_N) counting the bytes crossing it. A card sitting
// alone behind a stack -- as each of the three here does -- is measured
// exactly by its stack's counters.
//
// Validated against ground truth (tools/pcie-copy-bench moving a known number
// of bytes each way on every card): IIO matched to within about 1% on all
// three, and xpu-smi's own figure matched on the B70s. "out" counts host->GPU
// and "in" counts GPU->host.
//
// The counters are system-wide, so opening them needs CAP_PERFMON or root
// (perf_event_paranoid is 2 here). Without it Collect reports nothing and the
// caller falls back to "unavailable".

var (
	// The root bus a card hangs off, from its resolved sysfs path.
	iioRootBusPattern = regexp.MustCompile(`/pci([0-9a-f]{4}):([0-9a-f]{2})/`)

	// A perf config for an x86 uncore event: event in bits 0-7, umask in 8-15.
	iioEventPattern = regexp.MustCompile(`event=0x([0-9a-f]+),umask=0x([0-9a-f]+)`)
)

// iioPorts is how many x4 lane groups a stack has. A card's traffic lands on
// port 0 for both an x16 and an x8 link (measured), but summing all four is
// exact for any width and costs nothing.
const iioPorts = 4

// iioBackoff is how long a card whose counters could not be opened is left
// alone before trying again, so a missing privilege costs one syscall burst a
// minute rather than one per poll.
const iioBackoff = time.Minute

// IioTraffic is one card's PCIe throughput over the last sampling interval.
type IioTraffic struct {
	HostToGPUBytesPerSec float64 // "out": the host writing to, or the GPU reading from, host memory
	GPUToHostBytesPerSec float64 // "in"
}

// iioCounter is one free-running counter; a seam so tests need no perf.
type iioCounter interface {
	read() (uint64, error)
	close()
}

type iioCard struct {
	in, out []iioCounter
	scale   float64 // bytes per count
	last    struct {
		in, out uint64
		at      time.Time
		valid   bool
	}
	retryAt time.Time // set when opening failed
}

// IioCollector reads PCIe traffic per card from the IIO uncore counters. It
// must be reused across polls: a rate is the change between two reads.
type IioCollector struct {
	now func() time.Time

	// Roots, overridable by tests.
	pciRoot   string // /sys/bus/pci/devices
	devices   string // /sys/devices
	eventRoot string // /sys/bus/event_source/devices

	open func(pmuType uint32, config uint64, cpu int) (iioCounter, error)

	mu    sync.Mutex
	cards map[string]*iioCard
}

func NewIioCollector() *IioCollector {
	return &IioCollector{
		now:       time.Now,
		pciRoot:   DefaultSysfsRoot,
		devices:   "/sys/devices",
		eventRoot: "/sys/bus/event_source/devices",
		open:      openPerfCounter,
		cards:     make(map[string]*iioCard),
	}
}

// Close releases every open counter.
func (c *IioCollector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, card := range c.cards {
		card.closeAll()
	}
	c.cards = make(map[string]*iioCard)
}

func (card *iioCard) closeAll() {
	for _, ctr := range append(card.in, card.out...) {
		ctr.close()
	}
	card.in, card.out = nil, nil
}

// Collect returns the card's throughput over the interval since the previous
// call. ok is false until a second sample exists, and whenever the counters
// cannot be used (no privilege, not an IIO platform, a card that shares its
// stack is not something this can tell apart, or a counter reset).
func (c *IioCollector) Collect(bdf string) (t IioTraffic, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	card, known := c.cards[bdf]
	if !known {
		card = &iioCard{}
		c.cards[bdf] = card
	}
	if len(card.in) == 0 {
		if now.Before(card.retryAt) {
			return t, false
		}
		if err := c.openCard(bdf, card); err != nil {
			card.retryAt = now.Add(iioBackoff)
			return t, false
		}
	}

	in, inOK := sumCounters(card.in)
	out, outOK := sumCounters(card.out)
	if !inOK || !outOK {
		card.closeAll()
		card.last.valid = false
		card.retryAt = now.Add(iioBackoff)
		return t, false
	}

	prev := card.last
	card.last.in, card.last.out, card.last.at, card.last.valid = in, out, now, true
	if !prev.valid {
		return t, false
	}
	window := now.Sub(prev.at).Seconds()
	// A decrease means the counter was reset underneath us; drop the sample.
	if window <= 0 || in < prev.in || out < prev.out {
		return t, false
	}
	return IioTraffic{
		HostToGPUBytesPerSec: float64(out-prev.out) * card.scale / window,
		GPUToHostBytesPerSec: float64(in-prev.in) * card.scale / window,
	}, true
}

func sumCounters(ctrs []iioCounter) (uint64, bool) {
	var total uint64
	for _, ctr := range ctrs {
		v, err := ctr.read()
		if err != nil {
			return 0, false
		}
		total += v
	}
	return total, true
}

// stackFor finds the uncore_iio_free_running_N stack the card's root bus
// belongs to. Only die 0 is understood, which is every single-socket board.
func (c *IioCollector) stackFor(bdf string) (int, error) {
	real, err := filepath.EvalSymlinks(filepath.Join(c.pciRoot, bdf))
	if err != nil {
		return 0, err
	}
	m := iioRootBusPattern.FindStringSubmatch(real + "/")
	if m == nil {
		return 0, fmt.Errorf("no root bus in %s", real)
	}
	want := m[1] + ":" + m[2]
	for n := 0; n < 16; n++ {
		s, ok := readTrimmed(filepath.Join(c.devices, fmt.Sprintf("uncore_iio_%d", n), "die0"))
		if ok && strings.ToLower(s) == want {
			return n, nil
		}
	}
	return 0, fmt.Errorf("root bus %s has no IIO stack", want)
}

// parseIioConfig turns "event=0xff,umask=0x20" into a perf config value.
func parseIioConfig(s string) (uint64, bool) {
	m := iioEventPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	event, err1 := strconv.ParseUint(m[1], 16, 64)
	umask, err2 := strconv.ParseUint(m[2], 16, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return event | umask<<8, true
}

func (c *IioCollector) openCard(bdf string, card *iioCard) error {
	stack, err := c.stackFor(bdf)
	if err != nil {
		return err
	}
	pmu := filepath.Join(c.eventRoot, fmt.Sprintf("uncore_iio_free_running_%d", stack))
	typ, ok := readUintFile(filepath.Join(pmu, "type"))
	if !ok {
		return fmt.Errorf("no PMU type for stack %d", stack)
	}
	cpu := 0
	if s, ok := readTrimmed(filepath.Join(pmu, "cpumask")); ok {
		if first, _, _ := strings.Cut(s, ","); first != "" {
			first, _, _ = strings.Cut(first, "-")
			if n, err := strconv.Atoi(first); err == nil {
				cpu = n
			}
		}
	}

	scale := 0.0
	var in, out []iioCounter
	fail := func(err error) error {
		for _, ctr := range append(in, out...) {
			ctr.close()
		}
		return err
	}
	for port := 0; port < iioPorts; port++ {
		for _, dir := range []string{"in", "out"} {
			name := fmt.Sprintf("bw_%s_port%d", dir, port)
			spec, ok := readTrimmed(filepath.Join(pmu, "events", name))
			if !ok {
				return fail(fmt.Errorf("stack %d has no %s event", stack, name))
			}
			config, ok := parseIioConfig(spec)
			if !ok {
				return fail(fmt.Errorf("cannot parse %q", spec))
			}
			if scale == 0 {
				// The event's .scale is MiB per count; 3.814697266e-6 is 4 bytes.
				if s, ok := readTrimmed(filepath.Join(pmu, "events", name+".scale")); ok {
					if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
						// 3.814697266e-6 MiB is the decimal spelling of exactly 4 bytes;
						// snap to the whole number rather than carry 1e-10 of noise into
						// every rate.
						scale = math.Round(v*1024*1024*1000) / 1000
					}
				}
			}
			ctr, err := c.open(uint32(typ), config, cpu)
			if err != nil {
				return fail(err)
			}
			if dir == "in" {
				in = append(in, ctr)
			} else {
				out = append(out, ctr)
			}
		}
	}
	if scale == 0 {
		scale = 4
	}
	card.in, card.out, card.scale = in, out, scale
	return nil
}

// perfCounter is a perf_event_open file descriptor read as a raw count.
type perfCounter struct{ fd int }

func (p *perfCounter) read() (uint64, error) {
	var buf [8]byte
	n, err := unix.Read(p.fd, buf[:])
	if err != nil {
		return 0, err
	}
	if n != 8 {
		return 0, fmt.Errorf("short read of %d bytes", n)
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

func (p *perfCounter) close() { unix.Close(p.fd) }

func openPerfCounter(pmuType uint32, config uint64, cpu int) (iioCounter, error) {
	attr := unix.PerfEventAttr{
		Type:   pmuType,
		Size:   uint32(unsafe.Sizeof(unix.PerfEventAttr{})),
		Config: config,
	}
	fd, err := unix.PerfEventOpen(&attr, -1, cpu, -1, unix.PERF_FLAG_FD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	return &perfCounter{fd: fd}, nil
}
