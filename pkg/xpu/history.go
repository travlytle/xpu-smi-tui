package xpu

import "sync"

const DefaultHistoryCapacity = 60

// Series holds a rolling fixed-size window of float64 data points
type Series struct {
	capacity int
	data     []float64
}

func NewSeries(capacity int) *Series {
	if capacity <= 0 {
		capacity = DefaultHistoryCapacity
	}
	return &Series{
		capacity: capacity,
		data:     make([]float64, 0, capacity),
	}
}

func (s *Series) Add(val float64) {
	if len(s.data) >= s.capacity {
		s.data = append(s.data[1:], val)
	} else {
		s.data = append(s.data, val)
	}
}

func (s *Series) Values() []float64 {
	out := make([]float64, len(s.data))
	copy(out, s.data)
	return out
}

func (s *Series) Latest() float64 {
	if len(s.data) == 0 {
		return 0
	}
	return s.data[len(s.data)-1]
}

func (s *Series) MinMaxAvg() (min float64, max float64, avg float64) {
	if len(s.data) == 0 {
		return 0, 0, 0
	}
	min = s.data[0]
	max = s.data[0]
	sum := 0.0
	for _, v := range s.data {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}
	avg = sum / float64(len(s.data))
	return min, max, avg
}

// MetricHistory holds multiple series for a single GPU
type MetricHistory struct {
	mu         sync.RWMutex
	PowerW     *Series
	CoreTemp   *Series
	MemoryTemp *Series
	MemoryUtil *Series
	Frequency  *Series
	FanRPM     *Series
	Busy       *Series // busiest graphics tile, from idle residency
	Compute    *Series // compute engine (ccs) busy %, from per-client cycles
	Bandwidth  *Series // VRAM bandwidth as % of peak
}

func NewMetricHistory(capacity int) *MetricHistory {
	return &MetricHistory{
		PowerW:     NewSeries(capacity),
		CoreTemp:   NewSeries(capacity),
		MemoryTemp: NewSeries(capacity),
		MemoryUtil: NewSeries(capacity),
		Frequency:  NewSeries(capacity),
		FanRPM:     NewSeries(capacity),
		Busy:       NewSeries(capacity),
		Compute:    NewSeries(capacity),
		Bandwidth:  NewSeries(capacity),
	}
}

func (h *MetricHistory) Record(stats GPUStats) {
	if stats.Idle {
		return // carried-over values are not a new sample
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	h.PowerW.Add(stats.PowerW)
	h.CoreTemp.Add(stats.CoreTempC)
	h.MemoryTemp.Add(stats.MemoryTempC)
	if !stats.MemoryUsedUnavailable && !stats.MemoryUsedEstimated { // an estimate has no utilization %
		h.MemoryUtil.Add(stats.MemoryUtilPercent)
	}
	mhz, _ := stats.Clock()
	h.Frequency.Add(mhz)
	h.FanRPM.Add(stats.FanSpeedRPM)
	// Only a measured value goes into the series. The first poll has no busy%
	// yet (it is a delta), and recording it as 0 plotted a fabricated idle
	// sample that sat on the trend line for a minute.
	if busy, ok := busiestTile(stats); ok {
		h.Busy.Add(busy)
	}
	if stats.Fdinfo != nil {
		if ccs, ok := stats.Fdinfo.Engine("ccs"); ok && ccs.Valid {
			h.Compute.Add(ccs.BusyPercent)
		}
	}
	if !stats.TrafficUnavailable {
		h.Bandwidth.Add(stats.MemoryBandwidthPercent)
	}
}

// busiestTile is the highest graphics-tile utilization in a sample, and whether
// any tile had a valid reading at all.
func busiestTile(stats GPUStats) (float64, bool) {
	if stats.Sysfs == nil {
		return 0, false
	}
	busiest, valid := 0.0, false
	for _, gt := range stats.Sysfs.GTs {
		if gt.Idle.BusyValid && (!valid || gt.Idle.BusyPercent > busiest) {
			busiest, valid = gt.Idle.BusyPercent, true
		}
	}
	return busiest, valid
}
