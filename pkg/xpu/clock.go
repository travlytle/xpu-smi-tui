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
// So: actual when it is nonzero; while it reads 0, "parked" only if the GT is
// not busy either, and the requested clock (a fair estimate of what the work is
// running at) when it is.
func (s *GPUStats) Clock() (mhz float64, parked bool) {
	if s.Sysfs != nil && s.Sysfs.Available && len(s.Sysfs.GTs) > 0 {
		gt := s.Sysfs.GTs[0] // render/compute
		if gt.Freq.ActMHz > 0 {
			return gt.Freq.ActMHz, false
		}
		if gt.Idle.BusyValid && gt.Idle.BusyPercent > 5 {
			return s.FrequencyMHz, false
		}
		return 0, true
	}
	if s.ActualFrequencyMHz > 0 {
		return s.ActualFrequencyMHz, false
	}
	return s.FrequencyMHz, false // nothing says it is parked; show what there is
}
