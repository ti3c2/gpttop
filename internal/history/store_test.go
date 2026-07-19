package history

import (
	"testing"
	"time"

	"gpttop/internal/domain"
)

func TestStoreRetentionAndCopies(t *testing.T) {
	store := NewStore(10 * time.Second)
	base := time.Unix(100, 0)
	for i := 0; i < 4; i++ {
		store.AddSample(&domain.RawSample{
			EndpointName: "e",
			At:           base.Add(time.Duration(i*5) * time.Second),
			Series: []domain.SeriesPoint{{
				Name:     "vllm:num_requests_running",
				Semantic: domain.SemanticRequestsRunning,
				Kind:     domain.KindGauge,
				Labels:   domain.LabelSet{"model_name": "m"},
				Value:    float64(i),
			}},
		})
	}
	samples := store.Samples("e")
	if len(samples) != 3 {
		t.Fatalf("retained samples = %d, want 3", len(samples))
	}
	samples[0].Series[0].Labels["model_name"] = "changed"
	again := store.Samples("e")
	if got := again[0].Series[0].Labels["model_name"]; got != "m" {
		t.Fatalf("store leaked mutable label set: %q", got)
	}
}

func TestStoreStatus(t *testing.T) {
	store := NewStore(time.Minute)
	status := domain.ScrapeStatus{EndpointName: "e", Success: true}
	store.AddStatus(status)
	got, ok := store.Status("e")
	if !ok || !got.Success {
		t.Fatalf("status not stored: %#v ok=%v", got, ok)
	}
}
