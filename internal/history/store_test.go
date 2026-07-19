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

func TestStoreKeepsResetSafeOutcomeTotalsBeyondRetention(t *testing.T) {
	store := NewStore(10 * time.Second)
	base := time.Unix(200, 0)
	startA := 1.0
	startB := 2.0
	addOutcomes := func(at time.Time, processStart *float64, engine, http float64) {
		store.AddSample(&domain.RawSample{
			EndpointName: "e",
			At:           at,
			ProcessStart: processStart,
			Series: []domain.SeriesPoint{
				{
					Name:     "vllm:request_success_total",
					Semantic: domain.SemanticCompletionOutcomes,
					Kind:     domain.KindCounter,
					Labels:   domain.LabelSet{"model_name": "m", "finished_reason": "stop"},
					Value:    engine,
				},
				{
					Name:     "http_requests_total",
					Semantic: domain.SemanticHTTPOutcomes,
					Kind:     domain.KindCounter,
					Labels:   domain.LabelSet{"status": "2xx", "method": "POST", "handler": "/v1/chat/completions"},
					Value:    http,
				},
			},
		})
	}

	addOutcomes(base, &startA, 100, 200)
	addOutcomes(base.Add(5*time.Second), &startA, 110, 212)
	addOutcomes(base.Add(15*time.Second), &startB, 50, 50)
	addOutcomes(base.Add(20*time.Second), &startB, 57, 59)
	addOutcomes(base.Add(25*time.Second), &startB, 2, 3)
	addOutcomes(base.Add(30*time.Second), &startB, 5, 7)

	if got := len(store.Samples("e")); got != 3 {
		t.Fatalf("retained samples = %d, want 3", got)
	}
	totals := store.AllOutcomeTotals()["e"]
	if len(totals) != 2 {
		t.Fatalf("outcome totals = %d, want 2: %#v", len(totals), totals)
	}
	got := map[domain.Semantic]domain.OutcomeCounterTotal{}
	for _, total := range totals {
		got[total.Series.Semantic] = total
	}
	if total := got[domain.SemanticCompletionOutcomes]; total.Delta != 20 || total.Coverage != 30*time.Second {
		t.Fatalf("engine total = %#v, want delta 20 over 30s", total)
	}
	if total := got[domain.SemanticHTTPOutcomes]; total.Delta != 25 || total.Coverage != 30*time.Second {
		t.Fatalf("HTTP total = %#v, want delta 25 over 30s", total)
	}

	totals[0].Series.Labels["mutated"] = "yes"
	for _, total := range store.AllOutcomeTotals()["e"] {
		if total.Series.Labels["mutated"] != "" {
			t.Fatal("store leaked mutable outcome labels")
		}
	}
}
