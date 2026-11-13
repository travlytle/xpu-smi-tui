package xpu

import (
	"fmt"
	"sort"
	"strings"
)

// GPU numbers.
//
// The numbers a machine's GPUs go by are not stable on their own: xpu-smi's
// device index and the kernel's /dev/dri/cardN both follow driver probe order,
// and both can change when a card is added. What does not move is the PCI address, so the number
// shown by this tool -- and the `device` label on the exporter's series, which
// has to mean the same card next week as it does today -- is derived from
// that: cards are numbered in PCI address order unless the user names their
// own order.
//
// DeviceInfo.DeviceID is the Sysman enumeration index and is what callers pass
// back to the collector; only what a person or a dashboard sees is the
// assigned number.

// GPUNumber is the number this card is shown as. It is DeviceID until
// AssignGPUNumbers has run, so anything that builds a DeviceInfo by hand keeps
// working.
func (d DeviceInfo) GPUNumber() int {
	if d.numbered {
		return d.number
	}
	return d.DeviceID
}

// Label is how the card is named on screen: "GPU 1", or "iGPU 0" for an
// integrated GPU.
func (d DeviceInfo) Label() string {
	if d.Integrated {
		return fmt.Sprintf("iGPU %d", d.GPUNumber())
	}
	return fmt.Sprintf("GPU %d", d.GPUNumber())
}

// normalizeBDF makes "B5:00.0", "0000:b5:00.0" and "0000:B5:00.0" compare equal.
func normalizeBDF(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.Count(s, ":") == 1 {
		s = "0000:" + s
	}
	return s
}

// ParseGPUOrder splits a comma-separated list of PCI addresses.
func ParseGPUOrder(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// AssignGPUNumbers numbers the devices and returns them in that order.
//
// order lists PCI addresses; the card at position i becomes GPU i. Cards not
// named follow, in PCI address order, so an incomplete list -- or a card added
// later -- gets the next free number instead of colliding. An address in order
// that matches no card is ignored and does not use up a number. With no order,
// every card is numbered by PCI address.
func AssignGPUNumbers(devices []DeviceInfo, order []string) []DeviceInfo {
	out := make([]DeviceInfo, len(devices))
	copy(out, devices)

	rank := make(map[string]int, len(order))
	for _, o := range order {
		if _, dup := rank[normalizeBDF(o)]; !dup {
			rank[normalizeBDF(o)] = len(rank)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := rank[normalizeBDF(out[i].PciBdf)]
		rj, jok := rank[normalizeBDF(out[j].PciBdf)]
		switch {
		case iok && jok:
			return ri < rj
		case iok != jok:
			return iok // named cards come first
		}
		return normalizeBDF(out[i].PciBdf) < normalizeBDF(out[j].PciBdf)
	})
	for i := range out {
		out[i].number, out[i].numbered = i, true
	}
	return out
}
