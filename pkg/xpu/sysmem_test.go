package xpu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSystemMemory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "meminfo")
	os.WriteFile(p, []byte("MemTotal:       128000000 kB\nMemFree:         1000 kB\nMemAvailable:   120000000 kB\nBuffers: 5 kB\n"), 0o644)
	old := meminfoPath
	meminfoPath = p
	t.Cleanup(func() { meminfoPath = old })

	m, ok := ReadSystemMemory()
	if !ok || m.TotalBytes != 128000000*1024 || m.AvailableBytes != 120000000*1024 {
		t.Fatalf("got %+v, %v", m, ok)
	}
	if m.Used() != 8000000*1024 {
		t.Errorf("used = %d", m.Used())
	}

	meminfoPath = filepath.Join(t.TempDir(), "missing")
	if _, ok := ReadSystemMemory(); ok {
		t.Error("ok with no meminfo")
	}
}
