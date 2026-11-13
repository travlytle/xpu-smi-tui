package xpu

import "testing"

func TestClockPrefersActualAndReportsParked(t *testing.T) {
	gt := func(act, busy float64, valid bool) *SysfsStats {
		return &SysfsStats{Available: true, GTs: []GTStats{{Name: "gt0",
			Freq: GTFreq{ActMHz: act}, Idle: GTIdle{BusyPercent: busy, BusyValid: valid}}}}
	}
	cases := map[string]struct {
		s      GPUStats
		mhz    float64
		parked bool
	}{
		// The case that prompted this: 7 W, gt-c6, requested latched at 2800.
		"parked with request latched at max": {GPUStats{FrequencyMHz: 2800, Sysfs: gt(0, 0, true)}, 0, true},
		"running":                            {GPUStats{FrequencyMHz: 2800, Sysfs: gt(2350, 60, true)}, 2350, false},
		// A busy card sampled inside one of its c6 gaps: not parked, and the
		// requested clock is the best estimate of what the work runs at.
		"busy but sampled in a c6 gap": {GPUStats{FrequencyMHz: 2800, Sysfs: gt(0, 60, true)}, 2800, false},
		"no busy reading yet, act 0":   {GPUStats{FrequencyMHz: 2800, Sysfs: gt(0, 0, false)}, 0, true},
		"sysman only, actual known":    {GPUStats{FrequencyMHz: 2800, ActualFrequencyMHz: 600}, 600, false},
		"sysman only, actual 0":        {GPUStats{FrequencyMHz: 600}, 600, false},
	}
	for name, c := range cases {
		mhz, parked := c.s.Clock()
		if mhz != c.mhz || parked != c.parked {
			t.Errorf("%s: Clock() = %v, %v; want %v, %v", name, mhz, parked, c.mhz, c.parked)
		}
	}
}
