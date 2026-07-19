package aggregate

import (
	"math"
	"strings"
	"testing"
	"time"

	"gpttop/internal/domain"
	"gpttop/internal/history"
	"gpttop/internal/prom"
)

func TestBuildSnapshotDerivesEndpointModelRow(t *testing.T) {
	base := time.Unix(100, 0)
	store := history.NewStore(time.Minute)
	addParsedSample(t, store, firstMetrics, base)
	addParsedSample(t, store, secondMetrics, base.Add(10*time.Second))
	store.AddStatus(domain.ScrapeStatus{
		EndpointName: "local",
		MetricsURL:   "http://localhost:8000/metrics",
		At:           base.Add(10 * time.Second),
		Success:      true,
		LastSuccess:  base.Add(10 * time.Second),
		Recognized:   true,
	})

	snap := BuildSnapshot(store, domain.RuntimeConfig{
		CurrentWindow:   10 * time.Second,
		RefreshInterval: time.Second,
		Windows:         []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute},
		Endpoints:       []domain.EndpointConfig{{Name: "local", URL: "http://localhost:8000", MetricsURL: "http://localhost:8000/metrics"}},
	}, base.Add(10*time.Second))
	if len(snap.Rows) != 1 {
		t.Fatalf("rows = %d, want 1: %#v", len(snap.Rows), snap.Rows)
	}
	row := snap.Rows[0]
	if row.Model != "llama" {
		t.Fatalf("model = %q", row.Model)
	}
	if row.ObservationDuration != 10*time.Second {
		t.Fatalf("observation duration = %s, want 10s", row.ObservationDuration)
	}
	assertWindow(t, "rps", row.Overview.RPS, 3.7)
	assertWindow(t, "running", row.Overview.Running, 3)
	assertWindow(t, "prefix", row.Overview.PrefixHitRate, 50)
	assertWindow(t, "kv", row.Overview.KVCache, 75)
	assertWindow(t, "cpu", row.Overview.APICPU, 0.2)
	assertWindow(t, "ttft p95", row.Overview.TTFTP95, 0.19)
	assertWindow(t, "errors", row.Overview.Errors, 4)
	if len(row.EngineOutcomes[time.Minute]) == 0 {
		t.Fatal("engine outcomes missing")
	}
	if len(row.HTTPOutcomes[time.Minute]) == 0 {
		t.Fatal("http outcomes missing")
	}
	assertOutcomeCount(t, "all-time engine stop", row.EngineOutcomes[domain.OutcomeAllTime], "stop", 30)
	assertHTTPOutcomeCount(t, "all-time HTTP 200", row.HTTPOutcomes[domain.OutcomeAllTime], "200", 34)
}

func TestBuildSnapshotMarksMissingUnavailable(t *testing.T) {
	base := time.Unix(100, 0)
	store := history.NewStore(time.Minute)
	addParsedSample(t, store, `# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="m"} 1
`, base)
	snap := BuildSnapshot(store, domain.RuntimeConfig{CurrentWindow: 10 * time.Second}, base)
	if len(snap.Rows) != 1 {
		t.Fatalf("rows = %d", len(snap.Rows))
	}
	if snap.Rows[0].Overview.RPS.Available {
		t.Fatalf("missing counter became available: %#v", snap.Rows[0].Overview.RPS)
	}
}

func TestExtremaGaugePartialAndFullCoverage(t *testing.T) {
	base := time.Unix(1000, 0)
	store := history.NewStore(20 * time.Minute)
	addRawSample(store, rawSample(base, []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 5)}, nil, nil))
	addRawSample(store, rawSample(base.Add(5*time.Second), []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 2)}, nil, nil))
	addRawSample(store, rawSample(base.Add(10*time.Second), []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 7)}, nil, nil))
	addStatus(store, base.Add(10*time.Second))

	snap := BuildSnapshot(store, extremaConfig(), base.Add(10*time.Second))
	row := metricByKey(t, snap.Rows[0], domain.SemanticRequestsRunning)
	assertWindow(t, "running min", row.MinFifteen, 2)
	assertWindow(t, "running max", row.MaxFifteen, 7)
	if !row.MinFifteen.Partial || !row.MaxFifteen.Partial {
		t.Fatalf("short extrema coverage should be partial: min=%#v max=%#v", row.MinFifteen, row.MaxFifteen)
	}

	full := history.NewStore(20 * time.Minute)
	addRawSample(full, rawSample(base, []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 3)}, nil, nil))
	addRawSample(full, rawSample(base.Add(5*time.Minute), []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 4)}, nil, nil))
	addRawSample(full, rawSample(base.Add(10*time.Minute), []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 1)}, nil, nil))
	addRawSample(full, rawSample(base.Add(15*time.Minute), []domain.SeriesPoint{scalar(domain.SemanticRequestsRunning, 6)}, nil, nil))
	addStatus(full, base.Add(15*time.Minute))
	snap = BuildSnapshot(full, extremaConfig(), base.Add(15*time.Minute))
	row = metricByKey(t, snap.Rows[0], domain.SemanticRequestsRunning)
	assertWindow(t, "full running min", row.MinFifteen, 1)
	assertWindow(t, "full running max", row.MaxFifteen, 6)
	if row.MinFifteen.Partial || row.MaxFifteen.Partial {
		t.Fatalf("full extrema coverage should not be partial: min=%#v max=%#v", row.MinFifteen, row.MaxFifteen)
	}
}

func TestExtremaCounterRatesIncludeZeroAndIgnoreRestartSpike(t *testing.T) {
	base := time.Unix(2000, 0)
	store := history.NewStore(20 * time.Minute)
	startA := 1.0
	startB := 2.0
	addRawSample(store, rawSample(base, []domain.SeriesPoint{scalar(domain.SemanticCompletionOutcomes, 100)}, nil, &startA))
	addRawSample(store, rawSample(base.Add(10*time.Second), []domain.SeriesPoint{scalar(domain.SemanticCompletionOutcomes, 110)}, nil, &startA))
	addRawSample(store, rawSample(base.Add(20*time.Second), []domain.SeriesPoint{scalar(domain.SemanticCompletionOutcomes, 110)}, nil, &startA))
	addRawSample(store, rawSample(base.Add(30*time.Second), []domain.SeriesPoint{scalar(domain.SemanticCompletionOutcomes, 140)}, nil, &startA))
	addRawSample(store, rawSample(base.Add(40*time.Second), []domain.SeriesPoint{scalar(domain.SemanticCompletionOutcomes, 5)}, nil, &startB))
	addRawSample(store, rawSample(base.Add(50*time.Second), []domain.SeriesPoint{scalar(domain.SemanticCompletionOutcomes, 15)}, nil, &startB))
	addStatus(store, base.Add(50*time.Second))

	cfg := extremaConfig()
	cfg.CurrentWindow = time.Second
	snap := BuildSnapshot(store, cfg, base.Add(50*time.Second))
	row := metricByKey(t, snap.Rows[0], domain.SemanticCompletedRequestRate)
	assertWindow(t, "counter min", row.MinFifteen, 0)
	assertWindow(t, "counter max", row.MaxFifteen, 3)
	if row.MinFifteen.Value < 0 || row.MaxFifteen.Value > 3 {
		t.Fatalf("restart produced a counter spike: min=%#v max=%#v", row.MinFifteen, row.MaxFifteen)
	}
}

func TestExtremaPrefixRatioSkipsZeroQueryWindow(t *testing.T) {
	base := time.Unix(3000, 0)
	store := history.NewStore(20 * time.Minute)
	addRawSample(store, rawSample(base, []domain.SeriesPoint{
		scalar(domain.SemanticPrefixHits, 5),
		scalar(domain.SemanticPrefixQueries, 10),
	}, nil, nil))
	addRawSample(store, rawSample(base.Add(10*time.Second), []domain.SeriesPoint{
		scalar(domain.SemanticPrefixHits, 10),
		scalar(domain.SemanticPrefixQueries, 20),
	}, nil, nil))
	addRawSample(store, rawSample(base.Add(20*time.Second), []domain.SeriesPoint{
		scalar(domain.SemanticPrefixHits, 10),
		scalar(domain.SemanticPrefixQueries, 20),
	}, nil, nil))
	addRawSample(store, rawSample(base.Add(30*time.Second), []domain.SeriesPoint{
		scalar(domain.SemanticPrefixHits, 30),
		scalar(domain.SemanticPrefixQueries, 40),
	}, nil, nil))
	addStatus(store, base.Add(30*time.Second))

	snap := BuildSnapshot(store, extremaConfig(), base.Add(30*time.Second))
	row := metricByKey(t, snap.Rows[0], domain.SemanticPrefixHitRatio)
	assertWindow(t, "ratio min", row.MinFifteen, 50)
	assertWindow(t, "ratio max", row.MaxFifteen, 100)
}

func TestExtremaHistogramQuantileAndEmptyWindow(t *testing.T) {
	base := time.Unix(4000, 0)
	store := history.NewStore(20 * time.Minute)
	addRawSample(store, rawSample(base, nil, histogram(domain.SemanticTTFT, 0, 0, 0), nil))
	addRawSample(store, rawSample(base.Add(10*time.Second), nil, histogram(domain.SemanticTTFT, 5, 10, 10), nil))
	addRawSample(store, rawSample(base.Add(20*time.Second), nil, histogram(domain.SemanticTTFT, 15, 20, 20), nil))
	addStatus(store, base.Add(20*time.Second))

	cfg := extremaConfig()
	cfg.CurrentWindow = time.Second
	snap := BuildSnapshot(store, cfg, base.Add(20*time.Second))
	row := metricByKey(t, snap.Rows[0], domain.SemanticTTFTP95)
	assertWindow(t, "hist min", row.MinFifteen, 0.95)
	assertWindow(t, "hist max", row.MaxFifteen, 1.9)

	missing := metricByKey(t, snap.Rows[0], domain.SemanticE2EP95)
	if missing.MinFifteen.Available || missing.MaxFifteen.Available {
		t.Fatalf("empty histogram extrema should be unavailable: min=%#v max=%#v", missing.MinFifteen, missing.MaxFifteen)
	}
}

func addParsedSample(t *testing.T, store *history.Store, body string, at time.Time) {
	t.Helper()
	sample, err := prom.Parse(strings.NewReader(body), "local", "http://localhost:8000", "http://localhost:8000/metrics", at)
	if err != nil {
		t.Fatal(err)
	}
	store.AddSample(sample)
}

func extremaConfig() domain.RuntimeConfig {
	return domain.RuntimeConfig{
		CurrentWindow:   10 * time.Second,
		RefreshInterval: 10 * time.Second,
		Windows:         []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute},
		Endpoints:       []domain.EndpointConfig{{Name: "local", URL: "http://localhost:8000", MetricsURL: "http://localhost:8000/metrics"}},
	}
}

func addRawSample(store *history.Store, sample domain.RawSample) {
	store.AddSample(&sample)
}

func addStatus(store *history.Store, at time.Time) {
	store.AddStatus(domain.ScrapeStatus{
		EndpointName: "local",
		MetricsURL:   "http://localhost:8000/metrics",
		At:           at,
		Success:      true,
		LastSuccess:  at,
		Recognized:   true,
	})
}

func rawSample(at time.Time, series []domain.SeriesPoint, histograms []domain.HistogramPoint, processStart *float64) domain.RawSample {
	return domain.RawSample{
		EndpointName: "local",
		EndpointURL:  "http://localhost:8000",
		MetricsURL:   "http://localhost:8000/metrics",
		At:           at,
		Series:       series,
		Histograms:   histograms,
		ProcessStart: processStart,
		Recognized:   len(series) > 0 || len(histograms) > 0,
	}
}

func scalar(semantic domain.Semantic, value float64) domain.SeriesPoint {
	kind := domain.KindCounter
	if semantic == domain.SemanticRequestsRunning {
		kind = domain.KindGauge
	}
	return domain.SeriesPoint{
		Name:     string(semantic),
		Semantic: semantic,
		Kind:     kind,
		Labels:   domain.LabelSet{"model_name": "llama", "finished_reason": "stop"},
		Value:    value,
	}
}

func histogram(semantic domain.Semantic, le1, le2, inf uint64) []domain.HistogramPoint {
	return []domain.HistogramPoint{{
		Name:     string(semantic),
		Semantic: semantic,
		Labels:   domain.LabelSet{"model_name": "llama"},
		Buckets: []domain.Bucket{
			{Upper: 1, Count: le1},
			{Upper: 2, Count: le2},
			{Upper: math.Inf(1), Count: inf},
		},
		Count: inf,
	}}
}

func metricByKey(t *testing.T, row domain.ModelSnapshot, key domain.Semantic) domain.MetricRow {
	t.Helper()
	for _, metric := range row.Metrics {
		if metric.Key == key {
			return metric
		}
	}
	t.Fatalf("metric %s missing from row", key)
	return domain.MetricRow{}
}

func assertWindow(t *testing.T, name string, got domain.WindowValue, want float64) {
	t.Helper()
	if !got.Available {
		t.Fatalf("%s unavailable: %#v", name, got)
	}
	if diff := got.Value - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("%s = %f want %f", name, got.Value, want)
	}
}

func assertOutcomeCount(t *testing.T, name string, outcomes []domain.EngineOutcome, reason string, want float64) {
	t.Helper()
	for _, outcome := range outcomes {
		if outcome.Reason == reason {
			assertWindow(t, name, outcome.Count, want)
			return
		}
	}
	t.Fatalf("%s missing reason %q: %#v", name, reason, outcomes)
}

func assertHTTPOutcomeCount(t *testing.T, name string, outcomes []domain.HTTPOutcome, status string, want float64) {
	t.Helper()
	for _, outcome := range outcomes {
		if outcome.Status == status {
			assertWindow(t, name, outcome.Count, want)
			return
		}
	}
	t.Fatalf("%s missing status %q: %#v", name, status, outcomes)
}

const firstMetrics = `# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="llama",engine="0"} 1
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="llama",engine="0"} 0
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="llama",engine="0"} 1000
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="llama",engine="0"} 200
# TYPE vllm:prefix_cache_hits_total counter
vllm:prefix_cache_hits_total{model_name="llama",engine="0"} 20
# TYPE vllm:prefix_cache_queries_total counter
vllm:prefix_cache_queries_total{model_name="llama",engine="0"} 40
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{model_name="llama",engine="0"} 0.5
# TYPE vllm:request_success_total counter
vllm:request_success_total{model_name="llama",finished_reason="stop"} 100
vllm:request_success_total{model_name="llama",finished_reason="length"} 0
vllm:request_success_total{model_name="llama",finished_reason="abort"} 1
# TYPE http_requests_total counter
http_requests_total{status="200",method="POST",handler="/v1/chat/completions"} 100
http_requests_total{status="500",method="POST",handler="/v1/chat/completions"} 0
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 40
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1700000000
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{model_name="llama",le="0.1"} 5
vllm:time_to_first_token_seconds_bucket{model_name="llama",le="0.2"} 10
vllm:time_to_first_token_seconds_bucket{model_name="llama",le="+Inf"} 10
vllm:time_to_first_token_seconds_sum{model_name="llama"} 1.5
vllm:time_to_first_token_seconds_count{model_name="llama"} 10
# TYPE vllm:corrupted_requests_total counter
vllm:corrupted_requests_total{model_name="llama"} 0
`

const secondMetrics = `# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="llama",engine="0"} 3
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="llama",engine="0"} 2
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="llama",engine="0"} 1100
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="llama",engine="0"} 260
# TYPE vllm:prefix_cache_hits_total counter
vllm:prefix_cache_hits_total{model_name="llama",engine="0"} 30
# TYPE vllm:prefix_cache_queries_total counter
vllm:prefix_cache_queries_total{model_name="llama",engine="0"} 60
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{model_name="llama",engine="0"} 0.75
# TYPE vllm:request_success_total counter
vllm:request_success_total{model_name="llama",finished_reason="stop"} 130
vllm:request_success_total{model_name="llama",finished_reason="length"} 5
vllm:request_success_total{model_name="llama",finished_reason="abort"} 3
# TYPE http_requests_total counter
http_requests_total{status="200",method="POST",handler="/v1/chat/completions"} 134
http_requests_total{status="500",method="POST",handler="/v1/chat/completions"} 1
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 42
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1700000000
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{model_name="llama",le="0.1"} 10
vllm:time_to_first_token_seconds_bucket{model_name="llama",le="0.2"} 20
vllm:time_to_first_token_seconds_bucket{model_name="llama",le="+Inf"} 20
vllm:time_to_first_token_seconds_sum{model_name="llama"} 3
vllm:time_to_first_token_seconds_count{model_name="llama"} 20
# TYPE vllm:corrupted_requests_total counter
vllm:corrupted_requests_total{model_name="llama"} 1
`
