package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/ui"
	"github.com/travlytle/xpu-smi-tui/pkg/xpu"

	tea "charm.land/bubbletea/v2"
)

// AppVersion is set at build time with -ldflags "-X main.AppVersion=...".
var AppVersion = ""

func version() string {
	if AppVersion != "" {
		return AppVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return "dev"
}

func main() {
	var (
		interval     = 1
		demoMode     = flag.Bool("demo", false, "Start in simulation/demo mode")
		shortDemo    = flag.Bool("d", false, "Start in simulation/demo mode (shorthand)")
		deviceIdx    = flag.Int("device", -1, "GPU index to focus at start (-1 shows all cards)")
		idlePoll     = flag.String("idle-poll", os.Getenv("XPU_IDLE_POLL"), "Seconds between samples of a runtime-suspended card, which is otherwise left alone because reading it wakes it and roughly doubles its draw: a number, 0 to never wake one, or off to always poll (default 30; also $XPU_IDLE_POLL)")
		gpuOrder     = flag.String("gpu-order", os.Getenv("XPU_GPU_ORDER"), "PCI addresses in the order GPUs are numbered, e.g. 03:00.0,04:00.0 (default: PCI address order; also $XPU_GPU_ORDER)")
		versionFlag  = flag.Bool("version", false, "Print version and exit")
		shortVersion = flag.Bool("v", false, "Print version and exit (shorthand)")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "⚡ XPU-SMI TUI - Terminal Dashboard for Intel GPUs (v%s)\n\n", version())
		fmt.Fprintf(os.Stderr, "Usage: xpu-smi-tui [options]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nBuilt with Charm (Bubble Tea & Lip Gloss) for Intel Arc and Data Center GPUs.\n")
	}

	// One value, two spellings: registering both names on the same variable
	// means the last one given wins, instead of a heuristic that could not tell
	// an explicit --interval 1 from the default.
	flag.IntVar(&interval, "interval", 1, "Telemetry refresh interval in seconds")
	flag.IntVar(&interval, "i", 1, "Telemetry refresh interval in seconds (shorthand)")
	flag.Parse()

	if *versionFlag || *shortVersion {
		fmt.Printf("xpu-smi-tui version %s\n", version())
		os.Exit(0)
	}

	if interval < 1 {
		interval = 1
	}

	demo := *demoMode || *shortDemo

	idle, err := xpu.ParseIdlePolicy(*idlePoll)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	config := ui.Config{
		RefreshInterval: time.Duration(interval) * time.Second,
		DemoMode:        demo,
		DefaultDevice:   *deviceIdx,
		GPUOrder:        xpu.ParseGPUOrder(*gpuOrder),
		IdlePolicy:      idle,
	}

	model := ui.NewModel(config)
	// Mouse mode stays off so the terminal keeps text selection.
	p := tea.NewProgram(model)

	_, err = p.Run()
	// Release the collector before exiting, whichever way Run returned.
	model.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running xpu-smi-tui: %v\n", err)
		os.Exit(1)
	}
}
