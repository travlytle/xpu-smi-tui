package xpu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A collector whose xpu-smi binary fails must say so. Per-device errors used to
// be written into a channel that nothing read, so CollectStats returned a
// partial map and a nil error.
func TestCollectStatsSurfacesErrors(t *testing.T) {
	c := NewLiveCollector()
	// Otherwise this leaks a live dump stream (and the machine-wide lock it
	// holds; see stream.go's dumpLockPath) for the rest of the test binary,
	// starving every later test that also wants the stream.
	t.Cleanup(c.Close)
	stats, err := c.CollectStats(context.Background(), []int{0, 1})
	if err == nil {
		t.Fatal("every device failed and no error was returned")
	}
	if len(stats) != 0 {
		t.Errorf("expected no results, got %d", len(stats))
	}
}

// writeClient adds one DRM fd to a fake /proc pid directory.
func writeClient(t *testing.T, pidDir, fd, bdf string, vramKiB int) {
	t.Helper()
	for _, d := range []string{"fd", "fdinfo"} {
		if err := os.MkdirAll(filepath.Join(pidDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink("/dev/dri/renderD128", filepath.Join(pidDir, "fd", fd))
	os.WriteFile(filepath.Join(pidDir, "fdinfo", fd), []byte(
		"drm-pdev:\t"+bdf+"\ndrm-total-vram0:\t"+itoa(vramKiB)+" KiB\ndrm-cycles-ccs:\t0\ndrm-total-cycles-ccs:\t1\n"), 0o644)
	os.WriteFile(filepath.Join(pidDir, "comm"), []byte("llama-server\n"), 0o644)
	os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte("llama-server\x00"), 0o644)
	os.WriteFile(filepath.Join(pidDir, "status"), []byte("PPid:\t1\n"), 0o644)
}

// The live reality: one process holds two fds on one card and more on the
// other. Per card, a process's fds are summed; nothing crosses cards.
func TestFdinfoSumsPerProcessPerCard(t *testing.T) {
	root := t.TempDir()
	pid := filepath.Join(root, "100")
	writeClient(t, pid, "3", "0000:19:00.0", 26413264)
	writeClient(t, pid, "10", "0000:19:00.0", 4644)
	writeClient(t, pid, "4", "0000:67:00.0", 2842)

	c := NewFdinfoCollector()
	c.procRoot, c.selfPID = root, -1
	stats, _ := c.Collect()

	card1 := stats["0000:19:00.0"]
	if card1 == nil || len(card1.Processes) != 1 {
		t.Fatalf("card 19: expected one process, got %+v", card1)
	}
	if got := card1.Processes[0].Memory.VramBytes; got != int64(26413264+4644)*1024 {
		t.Errorf("card 19: fds not summed, got %d", got)
	}
	if card1.Processes[0].Clients != 2 {
		t.Errorf("card 19: expected 2 clients, got %d", card1.Processes[0].Clients)
	}
	card0 := stats["0000:67:00.0"]
	if card0 == nil || len(card0.Processes) != 1 || card0.Processes[0].Memory.VramBytes != 2842*1024 {
		t.Errorf("card 67: the other card's memory leaked across, got %+v", card0)
	}
}

// Completeness means "running as root"; a denial from any unrelated process
// must not flip it, since hundreds of root daemons deny the read.
func TestFdinfoCompletenessIsPrivilege(t *testing.T) {
	root := t.TempDir()
	writeClient(t, filepath.Join(root, "100"), "3", "0000:19:00.0", 1024)
	c := NewFdinfoCollector()
	c.procRoot, c.selfPID = root, -1

	c.privileged = false
	if _, complete := c.Collect(); complete {
		t.Error("unprivileged walk reported complete")
	}
	c.privileged = true
	if _, complete := c.Collect(); !complete {
		t.Error("privileged walk reported incomplete")
	}
}

// A multi-tile part must keep tile0/gt0 and tile1/gt0 apart.
func TestSysfsMultiTileGTsAreDistinct(t *testing.T) {
	fc := newFakeCard(t)
	// Clone gt0 into a second tile.
	src := filepath.Join(fc.devDir, "tile0", "gt0")
	dst := filepath.Join(fc.devDir, "tile1", "gt0")
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})

	c := NewSysfsCollector(fc.root)
	clock := time.Unix(0, 0)
	c.now = func() time.Time { return clock }
	c.Collect(fc.bdf)

	// Advance the two counters differently: tile0 fully idle, tile1 17.5% busy.
	// Keyed on the bare "gt0" the second read overwrote the first's baseline
	// and both tiles reported the same figure.
	clock = clock.Add(2 * time.Second)
	write(t, filepath.Join(src, "gtidle", "idle_residency_ms"), "3000") // idle the whole 2 s
	write(t, filepath.Join(dst, "gtidle", "idle_residency_ms"), "2650") // 17.5% busy
	st := c.Collect(fc.bdf)

	byTile := map[string]GTIdle{}
	for _, gt := range st.GTs {
		if gt.Name == "gt0" {
			byTile[gt.Tile] = gt.Idle
		}
	}
	if len(byTile) != 2 {
		t.Fatalf("expected gt0 on two tiles, got %v", byTile)
	}
	if b := byTile["tile1"]; !b.BusyValid || b.BusyPercent != 17.5 {
		t.Errorf("tile1/gt0 busy = %+v, want 17.5%%", b)
	}
	if b := byTile["tile0"]; !b.BusyValid || b.BusyPercent != 0 {
		t.Errorf("tile0/gt0 busy = %+v, want 0%% (it took tile1's counter?)", b)
	}
}

// A card that vanished between the stat and the reads is not "available".
func TestSysfsUnavailableWhenNothingRead(t *testing.T) {
	fc := newFakeCard(t)
	for _, sub := range []string{"hwmon", "tile0", "power"} {
		os.RemoveAll(filepath.Join(fc.devDir, sub))
	}
	if st := NewSysfsCollector(fc.root).Collect(fc.bdf); st.Available {
		t.Error("reported available with no sensors, tiles or PM state read")
	}
}

// The first poll has no busy% (it is a delta), so nothing is recorded; a
// fabricated 0 used to sit on the trend line for a minute.
func TestHistorySkipsInvalidBusy(t *testing.T) {
	h := NewMetricHistory(10)
	h.Record(GPUStats{Sysfs: &SysfsStats{Available: true, GTs: []GTStats{{Name: "gt0"}}}})
	if n := len(h.Busy.Values()); n != 0 {
		t.Errorf("recorded %d busy samples from an invalid reading", n)
	}
	h.Record(GPUStats{Sysfs: &SysfsStats{Available: true, GTs: []GTStats{
		{Name: "gt0", Idle: GTIdle{BusyPercent: 40, BusyValid: true}}}}})
	if v := h.Busy.Values(); len(v) != 1 || v[0] != 40 {
		t.Errorf("valid busy not recorded: %v", v)
	}
}

func TestFanUnavailableWithNoReadingAndNoHistory(t *testing.T) {
	c := NewLiveCollector()
	// A BDF with no hwmon path resolves to "not found" rather than "0 RPM".
	if _, _, ok := c.readFanRPM("0000:ff:ff.f"); ok {
		t.Error("a card with no fan node reported a fan")
	}
}

var _ = errors.New
var _ = strings.TrimSpace
