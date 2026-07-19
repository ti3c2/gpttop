package derive

import (
	"sort"
	"time"

	"gpttop/internal/domain"
)

// TimedValue is one scalar observation, optionally marking a process restart boundary.
type TimedValue struct {
	At      time.Time
	Value   float64
	Restart bool
}

// CounterResult is the reset-safe sum of counter increases over a window.
type CounterResult struct {
	Delta     float64
	Rate      float64
	Coverage  time.Duration
	Available bool
	Resets    int
}

// GaugeResult is the latest gauge value or LVH time-weighted mean.
type GaugeResult struct {
	Value     float64
	Coverage  time.Duration
	Available bool
}

// GaugeLatest returns the newest gauge value at or before end.
func GaugeLatest(points []TimedValue, end time.Time) GaugeResult {
	points = sortedValues(points)
	for i := len(points) - 1; i >= 0; i-- {
		if !points[i].At.After(end) && domain.IsFinite(points[i].Value) {
			return GaugeResult{Value: points[i].Value, Available: true}
		}
	}
	return GaugeResult{}
}

// GaugeMean calculates a last-value-held time-weighted mean over the window.
func GaugeMean(points []TimedValue, end time.Time, window time.Duration) GaugeResult {
	return GaugeMeanBounded(points, end, window, 0)
}

// GaugeMeanBounded calculates a last-value-held mean but does not carry values
// across gaps larger than maxGap. A non-positive maxGap disables gap cutting.
func GaugeMeanBounded(points []TimedValue, end time.Time, window, maxGap time.Duration) GaugeResult {
	if window <= 0 {
		return GaugeLatest(points, end)
	}
	points = sortedValues(points)
	start := end.Add(-window)
	var have bool
	var last TimedValue
	var current time.Time
	var area float64
	var coverage time.Duration

	for _, point := range points {
		if point.At.After(end) {
			break
		}
		if !domain.IsFinite(point.Value) {
			continue
		}
		if point.At.Before(start) || point.At.Equal(start) {
			last = point
			have = true
			current = start
			continue
		}
		if !have {
			last = point
			have = true
			current = point.At
			continue
		}
		if point.At.After(current) {
			d := point.At.Sub(current)
			if maxGap <= 0 || d <= maxGap {
				area += last.Value * d.Seconds()
				coverage += d
			}
			current = point.At
		}
		last = point
	}
	if !have {
		return GaugeResult{}
	}
	if end.After(current) {
		d := end.Sub(current)
		if maxGap <= 0 || d <= maxGap {
			area += last.Value * d.Seconds()
			coverage += d
		}
	}
	if coverage <= 0 {
		return GaugeResult{}
	}
	return GaugeResult{Value: area / coverage.Seconds(), Coverage: coverage, Available: true}
}

// CounterWindow returns reset-safe counter increase and rate over a window.
func CounterWindow(points []TimedValue, end time.Time, window time.Duration) CounterResult {
	if window <= 0 {
		return CounterResult{}
	}
	points = sortedValues(points)
	start := end.Add(-window)
	var usable []TimedValue
	for _, point := range points {
		if point.At.After(end) {
			break
		}
		if !domain.IsFinite(point.Value) {
			continue
		}
		if point.At.Before(start) {
			if len(usable) == 0 || usable[0].At.Before(point.At) {
				usable = []TimedValue{point}
			}
			continue
		}
		usable = append(usable, point)
	}
	if len(usable) < 2 {
		return CounterResult{}
	}
	var delta float64
	var coverage time.Duration
	var resets int
	for i := 1; i < len(usable); i++ {
		prev := usable[i-1]
		cur := usable[i]
		if !cur.At.After(prev.At) {
			continue
		}
		if cur.Restart || cur.Value < prev.Value {
			resets++
			delta = 0
			coverage = 0
			continue
		}
		delta += cur.Value - prev.Value
		coverage += cur.At.Sub(prev.At)
	}
	if coverage <= 0 {
		return CounterResult{}
	}
	return CounterResult{
		Delta:     delta,
		Rate:      delta / coverage.Seconds(),
		Coverage:  coverage,
		Available: true,
		Resets:    resets,
	}
}

// SumCounterWindows sums independently-derived counter deltas and divides by the widest coverage.
func SumCounterWindows(series map[string][]TimedValue, end time.Time, window time.Duration) CounterResult {
	var out CounterResult
	for _, points := range series {
		result := CounterWindow(points, end, window)
		if !result.Available {
			continue
		}
		out.Available = true
		out.Delta += result.Delta
		out.Resets += result.Resets
		if result.Coverage > out.Coverage {
			out.Coverage = result.Coverage
		}
	}
	if !out.Available || out.Coverage <= 0 {
		return CounterResult{}
	}
	out.Rate = out.Delta / out.Coverage.Seconds()
	return out
}

func sortedValues(points []TimedValue) []TimedValue {
	out := append([]TimedValue(nil), points...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.Before(out[j].At)
	})
	return out
}
