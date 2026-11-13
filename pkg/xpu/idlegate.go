package xpu

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Idle power.
//
// A card with nothing holding it drops into runtime suspend and draws roughly
// half what an awake one does. Every read
// of a device attribute -- Sysman, hwmon, frequency, gtidle, and a /proc fdinfo
// read of one of its DRM clients -- resumes it, so a monitor
// polling an idle card at 1 Hz keeps it awake forever and is itself the reason
// it draws 43 W. Only power/runtime_* is served from host memory and free.
//
// The gate below decides, per card and per tick, whether the device may be
// touched at all. It has to solve a trap the raw "is it suspended?" check does
// not: after this program wakes a card to sample it, the card reads "active"
// for its autosuspend delay, and treating that as real activity would poll it
// -- and so hold it awake -- indefinitely. So a wake-up is followed by a
// settling window in which the card is left alone whatever its state says; if it
// is still awake when the window ends, something else is holding it and normal
// polling resumes.

// IdlePolicy configures the gate.
type IdlePolicy struct {
	// Enabled false polls every card every tick regardless: the behaviour
	// before this existed.
	Enabled bool
	// Poll is how often a suspended card is woken for a sample. Zero never
	// wakes it: its last values are shown, marked idle, until something else
	// wakes it.
	Poll time.Duration
}

// DefaultIdlePolicy samples a suspended card every 30 s: one short wake per
// half minute costs a few percent of the saving, and keeps temperatures and
// memory use from going arbitrarily stale.
func DefaultIdlePolicy() IdlePolicy { return IdlePolicy{Enabled: true, Poll: 30 * time.Second} }

// ParseIdlePolicy reads a --idle-poll value: "off" (always poll), "0" (never
// wake a suspended card), or a number of seconds.
func ParseIdlePolicy(s string) (IdlePolicy, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "":
		return DefaultIdlePolicy(), nil
	case "off", "false", "no":
		return IdlePolicy{Enabled: false}, nil
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil || secs < 0 {
		return IdlePolicy{}, fmt.Errorf("--idle-poll must be off, 0, or a number of seconds, not %q", s)
	}
	return IdlePolicy{Enabled: true, Poll: time.Duration(secs * float64(time.Second))}, nil
}

const (
	// idleProbeEvery is how often a card that looks idle but is awake is left
	// alone for one settling window to find out whether it would suspend if we
	// stopped touching it. Without this a card that became idle while being
	// polled would be kept awake by the polling indefinitely.
	idleProbeEvery = 5 * time.Minute

	// settleMargin is added to the card's own autosuspend delay to get the
	// window after a wake-up (or a probe) in which it is not touched.
	settleMargin = 2 * time.Second
)

type gateState int

const (
	gatePolling  gateState = iota // touch every tick
	gateSettling                  // left alone until `until`, whatever its state
	gateIdle                      // suspended; touched only every policy.Poll
)

// idleGate is one card's gate. The zero value polls.
type idleGate struct {
	state     gateState
	until     time.Time // gateSettling: do not touch before this
	lastTouch time.Time
	nextProbe time.Time
}

// decide reports whether this tick may touch the device. pm is the card's
// runtime PM state (free to read) and looksIdle is whether its last sample
// showed nothing that would hold it awake.
func (g *idleGate) decide(now time.Time, pm RuntimePM, looksIdle bool, pol IdlePolicy) (touch bool) {
	// Without PM state there is nothing to gate on.
	if !pol.Enabled || pm.Status == "" {
		g.state, g.lastTouch = gatePolling, now
		return true
	}
	settle := time.Duration(pm.AutosuspendDelayMs)*time.Millisecond + settleMargin

	if pm.Suspended() {
		g.state = gateIdle
		if pol.Poll > 0 && now.Sub(g.lastTouch) >= pol.Poll {
			g.state, g.until, g.lastTouch = gateSettling, now.Add(settle), now
			return true // the wake-up sample
		}
		return false
	}

	// Active, or resuming.
	switch g.state {
	case gateSettling:
		if now.Before(g.until) {
			return false // our own wake-up, or a probe: leave it alone
		}
		// Still awake after any delay of our making has lapsed: something else
		// holds it, so it is not ours to leave alone.
		g.state, g.lastTouch = gatePolling, now
		g.nextProbe = now.Add(idleProbeEvery)
		return true
	case gateIdle:
		// It was suspended and is not now, and we did not wake it.
		g.state, g.lastTouch = gatePolling, now
		return true
	default:
		if looksIdle && !now.Before(g.nextProbe) {
			g.state, g.until = gateSettling, now.Add(settle)
			g.nextProbe = now.Add(idleProbeEvery)
			return false // start a quiet window to see whether it suspends
		}
		g.lastTouch = now
		return true
	}
}

// quiet reports whether the gate is currently keeping its hands off the card.
func (g *idleGate) quiet() bool { return g.state != gatePolling }

// looksIdle reports whether a card's last sample shows nothing that would hold
// it awake on its own: no monitor attached, no meaningful VRAM held, no engine
// busy. A card with a model resident holds VRAM and never suspends, so it is
// not worth probing; a card whose model was unloaded is.
func looksIdle(s *GPUStats) bool {
	if s == nil || s.Sysfs == nil || !s.Sysfs.Available {
		return false
	}
	if len(s.Sysfs.Displays) > 0 {
		return false
	}
	for _, gt := range s.Sysfs.GTs {
		if gt.Idle.BusyValid && gt.Idle.BusyPercent > 5 {
			return false
		}
	}
	if s.Fdinfo != nil {
		for _, p := range s.Fdinfo.Processes {
			if p.Memory.VramBytes >= 64<<20 {
				return false
			}
		}
		for _, e := range s.Fdinfo.Engines {
			if e.Valid && e.BusyPercent > 1 {
				return false
			}
		}
	}
	return true
}
