package xpu

import (
	"testing"
)

func TestSeries(t *testing.T) {
	s := NewSeries(5)
	for i := 1; i <= 10; i++ {
		s.Add(float64(i))
	}

	vals := s.Values()
	if len(vals) != 5 {
		t.Fatalf("expected length 5, got %d", len(vals))
	}

	if s.Latest() != 10.0 {
		t.Errorf("expected latest 10.0, got %f", s.Latest())
	}

	min, max, avg := s.MinMaxAvg()
	if min != 6.0 {
		t.Errorf("expected min 6.0, got %f", min)
	}
	if max != 10.0 {
		t.Errorf("expected max 10.0, got %f", max)
	}
	if avg != 8.0 {
		t.Errorf("expected avg 8.0, got %f", avg)
	}
}

// A held value is not a new sample: recording it would draw a flat line across
// the trend charts for as long as a card sleeps.
func TestHistorySkipsIdleTicks(t *testing.T) {
	h := NewMetricHistory(10)
	h.Record(GPUStats{PowerW: 40})
	h.Record(GPUStats{PowerW: 40, Idle: true})
	h.Record(GPUStats{PowerW: 40, Idle: true})
	if got := len(h.PowerW.Values()); got != 1 {
		t.Errorf("history holds %d samples, want 1 (idle ticks carry no new data)", got)
	}
}
