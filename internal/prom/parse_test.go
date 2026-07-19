package prom

import (
	"os"
	"strings"
	"testing"
	"time"

	"gpttop/internal/domain"
)

func TestParseCurrentMetricsNormalizesSemantics(t *testing.T) {
	f, err := os.Open("../testdata/metrics/current.prom")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sample, err := Parse(f, "local", "http://localhost:8000", "http://localhost:8000/metrics", time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !sample.Recognized {
		t.Fatal("expected recognized metrics")
	}
	if sample.ProcessStart == nil || *sample.ProcessStart != 1700000000 {
		t.Fatalf("process start not parsed: %#v", sample.ProcessStart)
	}
	assertSeries(t, sample.Series, "vllm:generation_tokens_total", domain.SemanticGenerationTokens)
	assertSeries(t, sample.Series, "vllm:prompt_tokens_total", domain.SemanticPromptTokens)
	assertSeries(t, sample.Series, "vllm:request_success_total", domain.SemanticCompletionOutcomes)
	assertHistogram(t, sample.Histograms, "vllm:time_to_first_token_seconds", domain.SemanticTTFT)
	assertLabelValue(t, sample.Series, domain.SemanticHTTPOutcomes, "note", "escaped\nquote\"slash\\")
	for _, series := range sample.Series {
		if strings.HasSuffix(series.Name, "_created") {
			t.Fatalf("created metric was not ignored: %#v", series)
		}
		if strings.Contains(series.Name, "unknown_metric") {
			t.Fatalf("unknown metric was not ignored: %#v", series)
		}
	}
}

func TestParseLegacyAliases(t *testing.T) {
	f, err := os.Open("../testdata/metrics/legacy.prom")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sample, err := Parse(f, "legacy", "", "", time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	assertSeries(t, sample.Series, "vllm:kv_cache_usage_perc", domain.SemanticKVCacheUsage)
	assertSeries(t, sample.Series, "vllm:gpu_prefix_cache_hit_rate", domain.SemanticLegacyPrefixHitRate)
	assertHistogram(t, sample.Histograms, "vllm:inter_token_latency_seconds", domain.SemanticInterTokenLatency)
}

func TestParseMalformedExposition(t *testing.T) {
	_, err := Parse(strings.NewReader("not a metric\n"), "bad", "", "", time.Unix(0, 0))
	if err == nil {
		t.Fatal("expected malformed exposition error")
	}
	if !strings.Contains(err.Error(), "parse prometheus metrics") {
		t.Fatalf("error = %v", err)
	}
}

func TestSeriesIdentityUsesSortedLabels(t *testing.T) {
	metrics := `# TYPE vllm:request_success_total counter
vllm:request_success_total{finished_reason="stop",model_name="m"} 1
vllm:request_success_total{model_name="m",finished_reason="stop"} 2
`
	sample, err := Parse(strings.NewReader(metrics), "e", "", "", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(sample.Series) != 2 {
		t.Fatalf("series count = %d", len(sample.Series))
	}
	if sample.Series[0].ID() != sample.Series[1].ID() {
		t.Fatalf("label ordering affected identity: %q != %q", sample.Series[0].ID(), sample.Series[1].ID())
	}
}

func FuzzLookupCounterNormalization(f *testing.F) {
	f.Add("vllm:prompt_tokens_total")
	f.Add("vllm:prompt_tokens")
	f.Add("vllm:prompt_tokens_created")
	f.Fuzz(func(t *testing.T, name string) {
		entry, ok := Lookup(name)
		if strings.HasSuffix(name, "_created") && ok {
			t.Fatalf("%q should be ignored", name)
		}
		if ok && entry.Kind == domain.KindCounter && strings.HasSuffix(entry.Canonical, "_created") {
			t.Fatalf("counter canonicalized to created metric: %#v", entry)
		}
	})
}

func assertSeries(t *testing.T, series []domain.SeriesPoint, name string, semantic domain.Semantic) {
	t.Helper()
	for _, point := range series {
		if point.Name == name && point.Semantic == semantic {
			return
		}
	}
	t.Fatalf("series %s/%s not found in %#v", name, semantic, series)
}

func assertHistogram(t *testing.T, hists []domain.HistogramPoint, name string, semantic domain.Semantic) {
	t.Helper()
	for _, hist := range hists {
		if hist.Name == name && hist.Semantic == semantic {
			if len(hist.Buckets) == 0 {
				t.Fatalf("histogram %s had no buckets", name)
			}
			return
		}
	}
	t.Fatalf("histogram %s/%s not found in %#v", name, semantic, hists)
}

func assertLabelValue(t *testing.T, series []domain.SeriesPoint, semantic domain.Semantic, key, want string) {
	t.Helper()
	for _, point := range series {
		if point.Semantic == semantic && point.Labels[key] == want {
			return
		}
	}
	t.Fatalf("label %s=%q not found for semantic %s in %#v", key, want, semantic, series)
}
