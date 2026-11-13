package xpu

import (
	"testing"
	"time"
)

var (
	pmActive    = RuntimePM{Status: "active", AutosuspendDelayMs: 1000}
	pmSuspended = RuntimePM{Status: "suspended", AutosuspendDelayMs: 1000}
	pol         = IdlePolicy{Enabled: true, Poll: 30 * time.Second}
)

func at(s int) time.Time { return time.Unix(1_000_000, 0).Add(time.Duration(s) * time.Second) }

// The whole point: a suspended card is left alone, woken every Poll for one
// sample, and then left alone again for its settling window even though the
// wake-up makes it read "active".
func TestGateSamplesASuspendedCardSparselyAndLeavesItAloneAfter(t *testing.T) {
	var g idleGate
	if !g.decide(at(0), pmSuspended, false, pol) {
		t.Fatal("no first sample from a card we know nothing about")
	}
	// Its own wake makes it read active for the autosuspend delay (1 s + 2 s
	// margin): none of that may look like activity.
	for s := 1; s <= 2; s++ {
		if g.decide(at(s), pmActive, false, pol) {
			t.Errorf("t=%ds: touched a card that is awake only because we woke it", s)
		}
	}
	// It suspends again; still nothing until Poll has elapsed.
	for s := 3; s < 30; s++ {
		if g.decide(at(s), pmSuspended, false, pol) {
			t.Errorf("t=%ds: touched a suspended card before the poll interval", s)
		}
	}
	if !g.decide(at(30), pmSuspended, false, pol) {
		t.Error("no wake-up sample once the poll interval elapsed")
	}
}

// A card still awake when the settling window ends is being held by something
// else, and gets polled normally.
func TestGateResumesPollingWhenSomethingElseHoldsTheCard(t *testing.T) {
	var g idleGate
	g.decide(at(0), pmSuspended, false, pol) // wake sample
	if g.decide(at(2), pmActive, false, pol) {
		t.Fatal("touched inside the settling window")
	}
	if !g.decide(at(4), pmActive, false, pol) {
		t.Error("still active after the settling window, but not polled: a real holder is being ignored")
	}
	for s := 5; s < 9; s++ {
		if !g.decide(at(s), pmActive, false, pol) {
			t.Errorf("t=%ds: an active, busy card was not polled", s)
		}
	}
}

// Something else waking a suspended card (a model starting) is picked up on
// the very next tick, without waiting for the poll interval.
func TestGateFollowsAnExternalWake(t *testing.T) {
	var g idleGate
	g.decide(at(0), pmSuspended, false, pol)
	g.decide(at(5), pmSuspended, false, pol) // long past the settling window
	if !g.decide(at(6), pmActive, false, pol) {
		t.Error("a card woken by someone else was not polled at once")
	}
}

// A card that became idle while being polled would be held awake by that
// polling forever; a periodic quiet window finds out.
func TestGateProbesACardThatLooksIdleButIsAwake(t *testing.T) {
	var g idleGate
	// Busy: polled every tick, never probed.
	for s := 0; s < 400; s++ {
		if !g.decide(at(s), pmActive, false, pol) {
			t.Fatalf("t=%ds: a busy card was left alone", s)
		}
	}
	// Now it looks idle. Probes are rate-limited by idleProbeEvery.
	var quietAt int = -1
	for s := 400; s < 400+int(idleProbeEvery/time.Second)+60; s++ {
		if !g.decide(at(s), pmActive, true, pol) {
			quietAt = s
			break
		}
	}
	if quietAt < 0 {
		t.Fatal("an idle-looking awake card was never given a quiet window to suspend in")
	}
	// The window lasts settle = 1 s + 2 s, then the card is polled again if it
	// did not suspend, and is not probed again for a while.
	if g.decide(at(quietAt+2), pmActive, true, pol) {
		t.Error("touched during the quiet window")
	}
	if !g.decide(at(quietAt+4), pmActive, true, pol) {
		t.Error("the card did not suspend in its quiet window, yet was not polled again")
	}
	if !g.decide(at(quietAt+30), pmActive, true, pol) {
		t.Error("probed again immediately instead of backing off")
	}
}

func TestGateNeverWakesWhenPollIsZero(t *testing.T) {
	var g idleGate
	zero := IdlePolicy{Enabled: true, Poll: 0}
	g.decide(at(0), pmSuspended, false, zero) // the unavoidable first look
	for s := 1; s < 600; s++ {
		if g.decide(at(s), pmSuspended, false, zero) {
			t.Fatalf("t=%ds: woke a suspended card with --idle-poll 0", s)
		}
	}
}

func TestGateDisabledAlwaysTouches(t *testing.T) {
	var g idleGate
	off := IdlePolicy{Enabled: false}
	for s := 0; s < 100; s++ {
		if !g.decide(at(s), pmSuspended, true, off) {
			t.Fatalf("t=%ds: gate disabled but a card was skipped", s)
		}
	}
	// And a card that publishes no PM state at all cannot be gated.
	var g2 idleGate
	if !g2.decide(at(0), RuntimePM{}, true, pol) {
		t.Error("a card with no PM state was skipped")
	}
}

func TestParseIdlePolicy(t *testing.T) {
	for in, want := range map[string]IdlePolicy{
		"":    {Enabled: true, Poll: 30 * time.Second},
		"45":  {Enabled: true, Poll: 45 * time.Second},
		"0":   {Enabled: true, Poll: 0},
		"off": {Enabled: false},
		"OFF": {Enabled: false},
	} {
		got, err := ParseIdlePolicy(in)
		if err != nil || got != want {
			t.Errorf("ParseIdlePolicy(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"soon", "-5"} {
		if _, err := ParseIdlePolicy(bad); err == nil {
			t.Errorf("ParseIdlePolicy(%q) accepted", bad)
		}
	}
}

func TestLooksIdle(t *testing.T) {
	idle := func() *GPUStats {
		return &GPUStats{Sysfs: &SysfsStats{Available: true}, Fdinfo: &FdinfoStats{Available: true}}
	}
	if !looksIdle(idle()) {
		t.Error("an empty card does not look idle")
	}
	withModel := idle()
	withModel.Fdinfo.Processes = []DrmProcess{{PID: 1, Memory: ProcessGPUMemory{VramBytes: 20 << 30}}}
	withDisplay := idle()
	withDisplay.Sysfs.Displays = []string{"DP-1"}
	busy := idle()
	busy.Sysfs.GTs = []GTStats{{Idle: GTIdle{BusyValid: true, BusyPercent: 40}}}
	stray := idle()
	stray.Fdinfo.Processes = []DrmProcess{{PID: 1, Memory: ProcessGPUMemory{VramBytes: 10 << 20}}}
	for name, s := range map[string]*GPUStats{"a resident model": withModel, "a display": withDisplay, "busy": busy} {
		if looksIdle(s) {
			t.Errorf("a card with %s looks idle", name)
		}
	}
	if !looksIdle(stray) {
		t.Error("a 10 MiB stray client made a card look busy")
	}
	if looksIdle(nil) || looksIdle(&GPUStats{}) {
		t.Error("a card with no data looks idle")
	}
}
