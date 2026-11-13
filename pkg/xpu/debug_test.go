package xpu

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A stall with no matching "done" line is the whole point: it is how a
// hang gets diagnosed after the fact, since nothing here can time out a
// blocked syscall from inside the program. This exercises both halves.
func TestPhaseLogsStartAndDone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collector.log")

	oldEnabled, oldPath, oldOnce, oldFile := debugCollector, collectorDebugLogPath, collectorLogOnce, collectorLogFile
	t.Cleanup(func() {
		debugCollector, collectorDebugLogPath, collectorLogOnce, collectorLogFile = oldEnabled, oldPath, oldOnce, oldFile
	})
	debugCollector = true
	collectorDebugLogPath = func() string { return path }
	collectorLogOnce = &sync.Once{}
	collectorLogFile = nil

	done := phase("widget scan")
	time.Sleep(5 * time.Millisecond)
	done()

	// A phase left unfinished -- the stall case -- must still have logged its
	// start line; nothing depends on the caller ever calling the returned func.
	phase("stuck read")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("log file not written: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !strings.HasPrefix(lines[0], "#") {
		t.Errorf("first line should be the header comment, got %q", lines[0])
	}
	joined := strings.Join(lines[1:], "\n")
	if !strings.Contains(joined, "widget scan") || !strings.Contains(joined, "start") || !strings.Contains(joined, "done") {
		t.Errorf("missing widget scan start/done: %q", joined)
	}
	if !strings.Contains(joined, "stuck read") || !strings.Contains(joined, "start") {
		t.Errorf("missing stuck read's start line: %q", joined)
	}
	// "stuck read" must have a start with no done anywhere for it, since done()
	// was never called -- distinguishing it from "widget scan" is the point.
	stuckLines := 0
	for _, l := range lines[1:] {
		if strings.Contains(l, "stuck read") {
			stuckLines++
		}
	}
	if stuckLines != 1 {
		t.Errorf("stuck read logged %d lines, want exactly 1 (start, no done)", stuckLines)
	}
}

// Disabled is the default, and must cost nothing: no file, no allocation
// beyond the bool check.
func TestPhaseIsSilentByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "collector.log")

	oldEnabled, oldPath, oldOnce, oldFile := debugCollector, collectorDebugLogPath, collectorLogOnce, collectorLogFile
	t.Cleanup(func() {
		debugCollector, collectorDebugLogPath, collectorLogOnce, collectorLogFile = oldEnabled, oldPath, oldOnce, oldFile
	})
	debugCollector = false
	collectorDebugLogPath = func() string { return path }
	collectorLogOnce = &sync.Once{}
	collectorLogFile = nil

	phase("should not appear")()
	logCollector("nor should this")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a log file was created while debugging was off")
	}
}
