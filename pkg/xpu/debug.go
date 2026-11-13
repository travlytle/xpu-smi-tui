package xpu

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// debugCollector turns on phase timing for CollectStats.
//
// "previous collection still running" (collector.go) means some read below
// took long enough that the next tick's call was refused rather than queued.
// Every phase here is a synchronous, context-ignoring syscall -- a blocked
// read to a card mid-resume, or one that never returns, cannot be cancelled
// from Go once it has entered the kernel; nothing here can time it out, only
// say afterwards which one it was. With this on, a stall shows as a "start"
// line with no matching "done": whichever phase logged last is the one still
// blocked.
var debugCollector = collectorDebugEnabled(os.Getenv("XPU_TUI_DEBUG_COLLECTOR"))

func collectorDebugEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

var (
	// A pointer, not a value: a test restores the pre-test Once by swapping
	// this back, and sync.Once cannot be copied once it may have been used.
	collectorLogOnce = &sync.Once{}
	collectorLogFile *os.File
	collectorLogMu   sync.Mutex
)

// collectorDebugLogPath is fixed, not generated, so it can be pointed at
// without first getting a message out of the running program -- unlike the
// UI's own key-debug log, nothing in this package can put a path on screen.
// A var, not a plain function, so a test can point it at a scratch file
// instead of overwriting whatever a real run left in the temp directory.
var collectorDebugLogPath = func() string {
	return filepath.Join(os.TempDir(), "xpu-smi-tui-collector.log")
}

// logCollector appends one timestamped line when XPU_TUI_DEBUG_COLLECTOR is
// set. Truncates the log at first use so it covers only the current run.
func logCollector(format string, args ...any) {
	if !debugCollector {
		return
	}
	collectorLogOnce.Do(func() {
		f, err := os.Create(collectorDebugLogPath())
		if err != nil {
			return
		}
		collectorLogFile = f
		fmt.Fprintf(f, "# xpu-smi-tui collector timing log, started %s\n", time.Now().Format(time.RFC3339))
	})
	if collectorLogFile == nil {
		return
	}
	collectorLogMu.Lock()
	defer collectorLogMu.Unlock()
	fmt.Fprintf(collectorLogFile, "%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

// phase logs a "start" line now and returns a func that logs "done" with the
// elapsed time; call it with defer. A phase with no "done" line by the time
// the log stops growing is the one that never returned.
func phase(name string) func() {
	if !debugCollector {
		return func() {}
	}
	logCollector("%-28s start", name)
	start := time.Now()
	return func() {
		logCollector("%-28s done  %s", name, time.Since(start).Round(time.Millisecond))
	}
}
