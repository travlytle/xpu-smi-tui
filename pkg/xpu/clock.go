package xpu

// Clock is the graphics clock the card is actually running at, and whether the
// render GT is parked.
//
// The driver reports two frequencies: the one it has *requested* and the one
// the GT is *actually* running at. The requested one latches: after a job it
// can sit at the hardware maximum indefinitely while the GT is parked in c6,
// drawing 7 W and executing nothing -- so a column showing it read "2800/2800"
// on an idle card. The actual clock reads 0 while parked, which is the
// truthful figure, but it also reads 0 when a once-a-second sample happens to
// land in one of the c6 gaps a busy card cycles through many times a second.
// So the busy share over the interval decides: under parkedBusy% the GT was
// asleep (gtidle residency reads a few percent of noise on an idle card, which
// used to pass a 5% test and show the latched 2800); otherwise the actual
// clock, or the requested one when the sample caught a gap. Without a busy
// reading, actual 0 is parked.
func (s *GPUStats) Clock() (mhz float64, parked bool) {
	if s.Sysfs != nil && s.Sysfs.Available && len(s.Sysfs.GTs) > 0 {
		gt := s.Sysfs.GTs[0] // render/compute
		switch {
		case gt.Idle.BusyValid && gt.Idle.BusyPercent < parkedBusy:
			return 0, true
		case gt.Freq.ActMHz > 0:
			return gt.Freq.ActMHz, false
		case gt.Idle.BusyValid:
			return s.FrequencyMHz, false
		}
		return 0, true
	}
	if s.ActualFrequencyMHz > 0 {
		return s.ActualFrequencyMHz, false
	}
	return s.FrequencyMHz, false // nothing says it is parked; show what there is
}

// parkedBusy is the busy share below which the render GT counts as parked.
const parkedBusy = 10
