package derive

import (
	"math"
	"testing"
	"time"

	"gpttop/internal/domain"
)

func TestGaugeMeanUsesLastValueHeld(t *testing.T) {
	base := time.Unix(0, 0)
	got := GaugeMean([]TimedValue{
		{At: base, Value: 10},
		{At: base.Add(10 * time.Second), Value: 20},
	}, base.Add(20*time.Second), 20*time.Second)
	if !got.Available {
		t.Fatal("gauge mean unavailable")
	}
	assertFloat(t, got.Value, 15)
	if got.Coverage != 20*time.Second {
		t.Fatalf("coverage = %s", got.Coverage)
	}
}

func TestGaugeMeanBoundedDoesNotCarryAcrossLongGap(t *testing.T) {
	base := time.Unix(0, 0)
	got := GaugeMeanBounded([]TimedValue{
		{At: base, Value: 10},
		{At: base.Add(2 * time.Second), Value: 20},
		{At: base.Add(30 * time.Second), Value: 40},
	}, base.Add(32*time.Second), 32*time.Second, 5*time.Second)
	if !got.Available {
		t.Fatal("gauge mean unavailable")
	}
	assertFloat(t, got.Value, 25)
	if got.Coverage != 4*time.Second {
		t.Fatalf("coverage = %s, want 4s", got.Coverage)
	}
}

func TestCounterWindowHandlesReset(t *testing.T) {
	base := time.Unix(0, 0)
	got := CounterWindow([]TimedValue{
		{At: base, Value: 100},
		{At: base.Add(10 * time.Second), Value: 110},
		{At: base.Add(20 * time.Second), Value: 5},
		{At: base.Add(30 * time.Second), Value: 15},
	}, base.Add(30*time.Second), 30*time.Second)
	if !got.Available {
		t.Fatal("counter unavailable")
	}
	assertFloat(t, got.Delta, 10)
	assertFloat(t, got.Rate, 1)
	if got.Coverage != 10*time.Second {
		t.Fatalf("coverage = %s, want 10s", got.Coverage)
	}
	if got.Resets != 1 {
		t.Fatalf("resets = %d", got.Resets)
	}
}

func TestHistogramWindowAndQuantiles(t *testing.T) {
	base := time.Unix(0, 0)
	got := HistogramWindow([]TimedHistogram{
		{
			At: base,
			Buckets: []domain.Bucket{
				{Upper: 1, Count: 10},
				{Upper: 2, Count: 20},
				{Upper: math.Inf(1), Count: 20},
			},
			Count: 20,
			Sum:   30,
		},
		{
			At: base.Add(10 * time.Second),
			Buckets: []domain.Bucket{
				{Upper: 1, Count: 15},
				{Upper: 2, Count: 35},
				{Upper: math.Inf(1), Count: 40},
			},
			Count: 40,
			Sum:   65,
		},
	}, base.Add(10*time.Second), 10*time.Second)
	if !got.Available {
		t.Fatal("histogram unavailable")
	}
	if got.Count != 20 || got.Buckets[0].Count != 5 || got.Buckets[1].Count != 15 || got.Buckets[2].Count != 20 {
		t.Fatalf("unexpected histogram delta: %#v", got)
	}
	p50, ok := Quantile(0.5, got.Buckets)
	if !ok {
		t.Fatal("p50 unavailable")
	}
	assertFloat(t, p50, 1.5)
	p95, ok := Quantile(0.95, got.Buckets)
	if !ok {
		t.Fatal("p95 unavailable")
	}
	assertFloat(t, p95, 2)
}

func TestHistogramSchemaMismatchUnavailable(t *testing.T) {
	base := time.Unix(0, 0)
	got := HistogramWindow([]TimedHistogram{
		{At: base, Buckets: []domain.Bucket{{Upper: 1, Count: 1}}, Count: 1},
		{At: base.Add(time.Second), Buckets: []domain.Bucket{{Upper: 2, Count: 2}}, Count: 2},
	}, base.Add(time.Second), time.Second)
	if got.Available {
		t.Fatalf("schema mismatch should be unavailable: %#v", got)
	}
}

func TestHistogramResetStartsNewGeneration(t *testing.T) {
	base := time.Unix(0, 0)
	got := HistogramWindow([]TimedHistogram{
		{At: base, Buckets: []domain.Bucket{{Upper: 1, Count: 10}, {Upper: math.Inf(1), Count: 10}}, Count: 10, Sum: 5},
		{At: base.Add(10 * time.Second), Buckets: []domain.Bucket{{Upper: 1, Count: 2}, {Upper: math.Inf(1), Count: 2}}, Count: 2, Sum: 1},
		{At: base.Add(20 * time.Second), Buckets: []domain.Bucket{{Upper: 1, Count: 5}, {Upper: math.Inf(1), Count: 6}}, Count: 6, Sum: 4},
	}, base.Add(20*time.Second), 20*time.Second)
	if !got.Available {
		t.Fatal("histogram unavailable")
	}
	if got.Count != 4 || got.Coverage != 10*time.Second {
		t.Fatalf("unexpected post-reset histogram: %#v", got)
	}
}

func TestQuantileInInfBucketIsBounded(t *testing.T) {
	got := QuantileWithBound(0.95, []domain.Bucket{
		{Upper: 1, Count: 1},
		{Upper: 2, Count: 2},
		{Upper: math.Inf(1), Count: 100},
	})
	if !got.Available || !got.MoreThan || got.Value != 2 {
		t.Fatalf("bounded quantile = %#v", got)
	}
}

func FuzzQuantileWithBound(f *testing.F) {
	f.Add(float64(0.95), []byte{1, 2, 3, 4})
	f.Fuzz(func(t *testing.T, q float64, raw []byte) {
		buckets := make([]domain.Bucket, 0, len(raw))
		var cumulative uint64
		for i, b := range raw {
			cumulative += uint64(b)
			upper := float64(i + 1)
			if i == len(raw)-1 {
				upper = math.Inf(1)
			}
			buckets = append(buckets, domain.Bucket{Upper: upper, Count: cumulative})
		}
		_ = QuantileWithBound(q, buckets)
	})
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %f want %f", got, want)
	}
}
