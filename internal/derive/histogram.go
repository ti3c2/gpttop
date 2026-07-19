package derive

import (
	"math"
	"sort"
	"time"

	"gpttop/internal/domain"
)

// TimedHistogram is one cumulative Prometheus histogram observation.
type TimedHistogram struct {
	At      time.Time
	Buckets []domain.Bucket
	Count   uint64
	Sum     float64
	Restart bool
}

// HistogramResult is a set of windowed, cumulative bucket deltas.
type HistogramResult struct {
	Buckets   []domain.Bucket
	Count     uint64
	Sum       float64
	Coverage  time.Duration
	Available bool
	Resets    int
}

// QuantileResult preserves whether a quantile landed in the +Inf bucket.
type QuantileResult struct {
	Value     float64
	Available bool
	MoreThan  bool
}

// HistogramWindow differences cumulative histogram buckets across a window.
func HistogramWindow(points []TimedHistogram, end time.Time, window time.Duration) HistogramResult {
	if window <= 0 {
		return HistogramResult{}
	}
	points = sortedHistograms(points)
	start := end.Add(-window)
	var usable []TimedHistogram
	for _, point := range points {
		if point.At.After(end) {
			break
		}
		if point.At.Before(start) {
			if len(usable) == 0 || usable[0].At.Before(point.At) {
				usable = []TimedHistogram{point}
			}
			continue
		}
		usable = append(usable, point)
	}
	if len(usable) < 2 {
		return HistogramResult{}
	}
	accum := make(map[float64]uint64)
	var count uint64
	var sum float64
	var coverage time.Duration
	var resets int
	for i := 1; i < len(usable); i++ {
		prev := usable[i-1]
		cur := usable[i]
		if !cur.At.After(prev.At) {
			continue
		}
		if !sameSchema(prev.Buckets, cur.Buckets) {
			accum = make(map[float64]uint64)
			count = 0
			sum = 0
			coverage = 0
			prev = cur
			continue
		}
		reset := cur.Restart || histogramReset(prev, cur)
		if reset {
			resets++
			accum = make(map[float64]uint64)
			count = 0
			sum = 0
			coverage = 0
			continue
		}
		for j, bucket := range cur.Buckets {
			accum[bucket.Upper] += bucket.Count - prev.Buckets[j].Count
		}
		count += cur.Count - prev.Count
		sum += cur.Sum - prev.Sum
		coverage += cur.At.Sub(prev.At)
	}
	if coverage <= 0 {
		return HistogramResult{}
	}
	buckets := make([]domain.Bucket, 0, len(accum))
	for upper, delta := range accum {
		buckets = append(buckets, domain.Bucket{Upper: upper, Count: delta})
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Upper < buckets[j].Upper
	})
	return HistogramResult{
		Buckets:   buckets,
		Count:     count,
		Sum:       sum,
		Coverage:  coverage,
		Available: true,
		Resets:    resets,
	}
}

// SumHistogramWindows sums compatible bucket deltas from independently-derived histograms.
func SumHistogramWindows(series map[string][]TimedHistogram, end time.Time, window time.Duration) HistogramResult {
	accum := make(map[float64]uint64)
	var schema []float64
	var out HistogramResult
	for _, points := range series {
		result := HistogramWindow(points, end, window)
		if !result.Available {
			continue
		}
		bounds := boundsOf(result.Buckets)
		if schema == nil {
			schema = bounds
		} else if !sameBounds(schema, bounds) {
			return HistogramResult{}
		}
		out.Available = true
		out.Count += result.Count
		out.Sum += result.Sum
		out.Resets += result.Resets
		if result.Coverage > out.Coverage {
			out.Coverage = result.Coverage
		}
		for _, bucket := range result.Buckets {
			accum[bucket.Upper] += bucket.Count
		}
	}
	if !out.Available || out.Coverage <= 0 {
		return HistogramResult{}
	}
	out.Buckets = make([]domain.Bucket, 0, len(accum))
	for upper, count := range accum {
		out.Buckets = append(out.Buckets, domain.Bucket{Upper: upper, Count: count})
	}
	sort.Slice(out.Buckets, func(i, j int) bool {
		return out.Buckets[i].Upper < out.Buckets[j].Upper
	})
	return out
}

// Quantile calculates a Prometheus-style quantile from cumulative bucket counts.
func Quantile(q float64, buckets []domain.Bucket) (float64, bool) {
	result := QuantileWithBound(q, buckets)
	return result.Value, result.Available
}

// QuantileWithBound calculates a Prometheus-style quantile from cumulative
// buckets and marks +Inf results as bounded by the previous finite bucket.
func QuantileWithBound(q float64, buckets []domain.Bucket) QuantileResult {
	if math.IsNaN(q) || q < 0 || q > 1 || len(buckets) == 0 {
		return QuantileResult{}
	}
	buckets = append([]domain.Bucket(nil), buckets...)
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Upper < buckets[j].Upper
	})
	ensureMonotonic(buckets)
	total := buckets[len(buckets)-1].Count
	if total == 0 {
		return QuantileResult{}
	}
	rank := q * float64(total)
	var prevUpper float64
	var prevCount uint64
	for i, bucket := range buckets {
		if float64(bucket.Count) < rank {
			prevUpper = bucket.Upper
			prevCount = bucket.Count
			continue
		}
		if math.IsInf(bucket.Upper, 1) {
			if i == 0 {
				return QuantileResult{}
			}
			return QuantileResult{Value: buckets[i-1].Upper, Available: true, MoreThan: true}
		}
		if i == 0 {
			if bucket.Upper <= 0 {
				return QuantileResult{Value: bucket.Upper, Available: true}
			}
			prevUpper = 0
		}
		bucketCount := bucket.Count - prevCount
		if bucketCount == 0 {
			return QuantileResult{Value: bucket.Upper, Available: true}
		}
		fraction := (rank - float64(prevCount)) / float64(bucketCount)
		return QuantileResult{Value: prevUpper + (bucket.Upper-prevUpper)*fraction, Available: true}
	}
	return QuantileResult{Value: buckets[len(buckets)-1].Upper, Available: true}
}

func sortedHistograms(points []TimedHistogram) []TimedHistogram {
	out := append([]TimedHistogram(nil), points...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].At.Before(out[j].At)
	})
	return out
}

func sameSchema(a, b []domain.Bucket) bool {
	return sameBounds(boundsOf(a), boundsOf(b))
}

func boundsOf(buckets []domain.Bucket) []float64 {
	out := make([]float64, len(buckets))
	for i, bucket := range buckets {
		out[i] = bucket.Upper
	}
	return out
}

func sameBounds(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func histogramReset(prev, cur TimedHistogram) bool {
	if cur.Restart || cur.Count < prev.Count || cur.Sum < prev.Sum {
		return true
	}
	for i := range cur.Buckets {
		if cur.Buckets[i].Count < prev.Buckets[i].Count {
			return true
		}
	}
	return false
}

func ensureMonotonic(buckets []domain.Bucket) {
	var max uint64
	for i := range buckets {
		if buckets[i].Count < max {
			buckets[i].Count = max
			continue
		}
		max = buckets[i].Count
	}
}
