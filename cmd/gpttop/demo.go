package main

import (
	"math"
	"time"

	"gpttop/internal/domain"
	"gpttop/internal/history"
)

func addDemoScrape(store *history.Store, cfg domain.RuntimeConfig, at time.Time, step int) {
	for i, endpoint := range cfg.Endpoints {
		sample := demoSample(endpoint, at, step, i)
		store.AddSample(sample)
		store.AddStatus(domain.ScrapeStatus{
			EndpointName: endpoint.Name,
			MetricsURL:   endpoint.MetricsURL,
			SanitizedURL: endpoint.SanitizedURL,
			At:           at,
			Success:      true,
			Duration:     28*time.Millisecond + time.Duration(i*11)*time.Millisecond,
			LastSuccess:  at,
			Recognized:   true,
		})
	}
}

func demoSample(endpoint domain.EndpointConfig, at time.Time, step, index int) *domain.RawSample {
	model := "google/gemma-3-27b-it"
	if index%2 == 1 {
		model = "meta/llama-3.1-8b-instruct"
	}
	pressure := index%2 == 1
	running := 8 + float64((step+index)%5)
	waiting := float64((step + index) % 3)
	kv := 0.42 + float64((step+index)%8)*0.015
	if pressure {
		running = 24 + float64((step*3)%17)
		waiting = 4 + float64((step*5)%31)
		kv = 0.72 + math.Min(0.25, float64(step)*0.015)
	}

	base := float64(1000 + index*500)
	completed := base + float64(step*(9+index*4))
	length := float64(step / 7)
	abort := float64(step / 11)
	prompt := base*120 + float64(step*(1800+index*650))
	gen := base*24 + float64(step*(320+index*80))
	queries := base*3 + float64(step*(40+index*10))
	hits := base + float64(step*(18+index*2))
	preemptions := float64(step / 9)
	corrupted := float64(0)
	if pressure {
		corrupted = float64(step / 17)
	}
	cpu := float64(100+index*20) + float64(step)*(0.65+float64(index)*0.2)
	start := float64(1700000000 + index)
	labels := domain.LabelSet{"model_name": model, "engine": "0"}
	return &domain.RawSample{
		EndpointName: endpoint.Name,
		EndpointURL:  endpoint.URL,
		MetricsURL:   endpoint.MetricsURL,
		At:           at,
		ProcessStart: &start,
		Recognized:   true,
		Series: []domain.SeriesPoint{
			gauge(domain.SemanticRequestsRunning, "vllm:num_requests_running", labels, running),
			gauge(domain.SemanticRequestsWaiting, "vllm:num_requests_waiting", labels, waiting),
			gauge(domain.SemanticKVCacheUsage, "vllm:kv_cache_usage_perc", labels, kv),
			counter(domain.SemanticPromptTokens, "vllm:prompt_tokens_total", labels, prompt),
			counter(domain.SemanticGenerationTokens, "vllm:generation_tokens_total", labels, gen),
			counter(domain.SemanticPrefixQueries, "vllm:prefix_cache_queries_total", labels, queries),
			counter(domain.SemanticPrefixHits, "vllm:prefix_cache_hits_total", labels, hits),
			counterWith(domain.SemanticCompletionOutcomes, "vllm:request_success_total", labels, "finished_reason", "stop", completed),
			counterWith(domain.SemanticCompletionOutcomes, "vllm:request_success_total", labels, "finished_reason", "length", length),
			counterWith(domain.SemanticCompletionOutcomes, "vllm:request_success_total", labels, "finished_reason", "abort", abort),
			counter(domain.SemanticPreemptions, "vllm:num_preemptions_total", labels, preemptions),
			counter(domain.SemanticCorruptedRequests, "vllm:corrupted_requests_total", labels, corrupted),
			counter(domain.SemanticAPICPU, "process_cpu_seconds_total", nil, cpu),
			gauge(domain.SemanticProcessStart, "process_start_time_seconds", nil, start),
			httpCounter("2xx", "POST", "/v1/chat/completions", completed+length),
			httpCounter("4xx", "GET", "none", float64(step/13+index)),
			httpCounter("5xx", "POST", "/v1/chat/completions", float64(step/23)),
		},
		Histograms: []domain.HistogramPoint{
			hist(domain.SemanticTTFT, "vllm:time_to_first_token_seconds", labels, uint64(completed), 0.25+float64(index)*0.08),
			hist(domain.SemanticInterTokenLatency, "vllm:inter_token_latency_seconds", labels, uint64(completed*8), 0.035+float64(index)*0.01),
			hist(domain.SemanticE2ELatency, "vllm:e2e_request_latency_seconds", labels, uint64(completed), 1.5+float64(index)*0.8),
			hist(domain.SemanticQueueTime, "vllm:request_queue_time_seconds", labels, uint64(completed), 0.04+waiting*0.02),
			hist(domain.SemanticInferenceTime, "vllm:request_inference_time_seconds", labels, uint64(completed), 1.0+float64(index)*0.5),
			hist(domain.SemanticPrefillTime, "vllm:request_prefill_time_seconds", labels, uint64(completed), 0.45+float64(index)*0.18),
			hist(domain.SemanticDecodeTime, "vllm:request_decode_time_seconds", labels, uint64(completed), 0.85+float64(index)*0.24),
		},
	}
}

func gauge(semantic domain.Semantic, name string, labels domain.LabelSet, value float64) domain.SeriesPoint {
	return domain.SeriesPoint{Name: name, Semantic: semantic, Kind: domain.KindGauge, Labels: labels.Clone(), Value: value}
}

func counter(semantic domain.Semantic, name string, labels domain.LabelSet, value float64) domain.SeriesPoint {
	return domain.SeriesPoint{Name: name, Semantic: semantic, Kind: domain.KindCounter, Labels: labels.Clone(), Value: value}
}

func counterWith(semantic domain.Semantic, name string, labels domain.LabelSet, key, value string, counterValue float64) domain.SeriesPoint {
	ls := labels.Clone()
	ls[key] = value
	return counter(semantic, name, ls, counterValue)
}

func httpCounter(status, method, handler string, value float64) domain.SeriesPoint {
	return counter(domain.SemanticHTTPOutcomes, "http_requests_total", domain.LabelSet{
		"status":  status,
		"method":  method,
		"handler": handler,
	}, value)
}

func hist(semantic domain.Semantic, name string, labels domain.LabelSet, count uint64, scale float64) domain.HistogramPoint {
	if count == 0 {
		count = 1
	}
	bounds := []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, math.Inf(1)}
	buckets := make([]domain.Bucket, 0, len(bounds))
	for i, bound := range bounds {
		frac := float64(i+1) / float64(len(bounds))
		if !math.IsInf(bound, 1) {
			frac = math.Min(0.98, math.Pow(math.Min(1, bound/(scale*3)), 0.8))
		} else {
			frac = 1
		}
		buckets = append(buckets, domain.Bucket{Upper: bound, Count: uint64(float64(count) * frac)})
	}
	buckets[len(buckets)-1].Count = count
	return domain.HistogramPoint{
		Name:     name,
		Semantic: semantic,
		Labels:   labels.Clone(),
		Buckets:  buckets,
		Count:    count,
		Sum:      float64(count) * scale,
	}
}
