package ui

import (
	"time"

	"github.com/travlytle/xpu-smi-tui/pkg/xpu"
)

// TickMsg signals the periodic telemetry refresh. Gen identifies the tick
// chain it belongs to: changing the refresh interval starts a new chain and
// ticks from the old one are ignored, so an interval change takes effect at
// once instead of after the old timer fires -- up to 30 s later -- and no
// second chain is ever left running alongside the first.
type TickMsg struct {
	At  time.Time
	Gen int
}

// DiscoveredMsg carries the result of device discovery plus the first fetch.
// It was an anonymous struct duplicated at the producer and the consumer;
// anonymous structs match structurally, so adding a field to one side compiled
// cleanly and silently stopped the message being handled.
type DiscoveredMsg struct {
	Devices  []xpu.DeviceInfo
	Stats    map[int]*xpu.GPUStats
	Health   map[int]xpu.DeviceHealth
	Topology *xpu.TopologyData
	// Err is a stats-collection failure after discovery itself succeeded.
	Err error
}

// DataLoadedMsg contains freshly polled data from the collector
type DataLoadedMsg struct {
	Stats  map[int]*xpu.GPUStats
	Health map[int]xpu.DeviceHealth
	// Err is a partial failure: some devices reported, some did not.
	Err error
}

// ErrorMsg indicates a failure during telemetry polling
type ErrorMsg struct {
	Err error
	// Discovery reports that the failure was in device discovery itself.
	Discovery bool
}

// StatusClearMsg clears a temporary status message. Seq must match the message
// it was armed for: an untagged clear from an earlier timer erased whatever
// newer message had replaced it.
type StatusClearMsg struct{ Seq int }
