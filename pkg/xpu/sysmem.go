package xpu

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// SystemMemory is the host's RAM, from /proc/meminfo. Context for a box
// whose model files are mmap'd and whose cards evict to host RAM when their
// VRAM is full; it is not a per-card figure.
type SystemMemory struct {
	TotalBytes     int64
	AvailableBytes int64 // the kernel's MemAvailable: what a new allocation could get without swapping
}

// Used is what is not available.
func (m SystemMemory) Used() int64 { return m.TotalBytes - m.AvailableBytes }

// meminfoPath is a var so tests can point it at a fixture.
var meminfoPath = "/proc/meminfo"

// ReadSystemMemory reads the host's RAM figures. ok is false when they
// cannot be read, which no caller should treat as "0 bytes".
func ReadSystemMemory() (m SystemMemory, ok bool) {
	f, err := os.Open(meminfoPath)
	if err != nil {
		return m, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest) // "123456 kB"
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			m.TotalBytes = kb * 1024
		case "MemAvailable":
			m.AvailableBytes = kb * 1024
		}
	}
	return m, m.TotalBytes > 0 && m.AvailableBytes > 0
}
