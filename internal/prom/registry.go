package prom

import (
	"strings"

	"gpttop/internal/domain"
)

// Entry describes the semantic meaning of one Prometheus metric family.
type Entry struct {
	Name      string
	Semantic  domain.Semantic
	Kind      domain.MetricKind
	Canonical string
}

var registry = map[string]Entry{}

func init() {
	add("vllm:num_requests_running", domain.SemanticRequestsRunning, domain.KindGauge)
	add("vllm:num_requests_waiting", domain.SemanticRequestsWaiting, domain.KindGauge)
	addCounter("vllm:prompt_tokens_total", domain.SemanticPromptTokens)
	addCounter("vllm:generation_tokens_total", domain.SemanticGenerationTokens)
	addCounter("vllm:prefix_cache_queries_total", domain.SemanticPrefixQueries)
	addCounter("vllm:prefix_cache_hits_total", domain.SemanticPrefixHits)
	add("vllm:kv_cache_usage_perc", domain.SemanticKVCacheUsage, domain.KindGauge)
	alias("vllm:gpu_cache_usage_perc", "vllm:kv_cache_usage_perc", domain.SemanticKVCacheUsage, domain.KindGauge)
	addCounter("vllm:request_success_total", domain.SemanticCompletionOutcomes)
	addCounter("http_requests_total", domain.SemanticHTTPOutcomes)
	addCounter("process_cpu_seconds_total", domain.SemanticAPICPU)
	add("process_start_time_seconds", domain.SemanticProcessStart, domain.KindGauge)
	addHistogram("vllm:time_to_first_token_seconds", domain.SemanticTTFT)
	addHistogram("vllm:inter_token_latency_seconds", domain.SemanticInterTokenLatency)
	alias("vllm:time_per_output_token_seconds", "vllm:inter_token_latency_seconds", domain.SemanticInterTokenLatency, domain.KindHistogram)
	addHistogram("vllm:e2e_request_latency_seconds", domain.SemanticE2ELatency)
	addHistogram("vllm:request_queue_time_seconds", domain.SemanticQueueTime)
	addHistogram("vllm:request_inference_time_seconds", domain.SemanticInferenceTime)
	addHistogram("vllm:request_prefill_time_seconds", domain.SemanticPrefillTime)
	addHistogram("vllm:request_decode_time_seconds", domain.SemanticDecodeTime)
	addCounter("vllm:num_preemptions_total", domain.SemanticPreemptions)
	addCounter("vllm:corrupted_requests_total", domain.SemanticCorruptedRequests)
	add("vllm:gpu_prefix_cache_hit_rate", domain.SemanticLegacyPrefixHitRate, domain.KindGauge)
}

func add(name string, semantic domain.Semantic, kind domain.MetricKind) {
	registry[name] = Entry{Name: name, Semantic: semantic, Kind: kind, Canonical: name}
}

func addCounter(name string, semantic domain.Semantic) {
	add(name, semantic, domain.KindCounter)
	if strings.HasSuffix(name, "_total") {
		registry[strings.TrimSuffix(name, "_total")] = Entry{
			Name:      strings.TrimSuffix(name, "_total"),
			Semantic:  semantic,
			Kind:      domain.KindCounter,
			Canonical: name,
		}
	}
}

func addHistogram(name string, semantic domain.Semantic) {
	add(name, semantic, domain.KindHistogram)
}

func alias(name, canonical string, semantic domain.Semantic, kind domain.MetricKind) {
	registry[name] = Entry{Name: name, Semantic: semantic, Kind: kind, Canonical: canonical}
	if kind == domain.KindCounter && strings.HasSuffix(name, "_total") {
		registry[strings.TrimSuffix(name, "_total")] = Entry{
			Name:      strings.TrimSuffix(name, "_total"),
			Semantic:  semantic,
			Kind:      kind,
			Canonical: canonical,
		}
	}
}

// Lookup returns the semantic registry entry for a metric family name.
func Lookup(name string) (Entry, bool) {
	if strings.HasSuffix(name, "_created") {
		return Entry{}, false
	}
	e, ok := registry[name]
	return e, ok
}

// Entries returns a copy of the central semantic registry.
func Entries() map[string]Entry {
	out := make(map[string]Entry, len(registry))
	for k, v := range registry {
		out[k] = v
	}
	return out
}
