package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunHelpAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--endpoint") {
		t.Fatalf("help missing endpoint flag:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "gpttop") || !strings.Contains(stdout.String(), "commit=") {
		t.Fatalf("version output = %q", stdout.String())
	}
}

func TestRunRequiresEndpointOrDemo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--once"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected nonzero without endpoint")
	}
	if !strings.Contains(stderr.String(), "no endpoints") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDemoNonTTYJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--demo", "--output", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("demo code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, want := range []string{`"rows"`, "demo-healthy", "demo-pressure", `"state": "UP"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("demo JSON missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunOnceScrapesEndpointTable(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("path = %q, want /metrics", r.URL.Path)
		}
		call := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, onceMetrics(call))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--once", "--sample-duration", "1ms", "--endpoint", "local=" + server.URL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("once code = %d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("scrape calls = %d, want 2", calls)
	}
	for _, want := range []string{"ENDPOINT", "local", "llama", "UP"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("table missing %q:\n%s", want, stdout.String())
		}
	}
}

func onceMetrics(call int32) string {
	completed := 10 + call*4
	prompt := 100 + call*20
	gen := 50 + call*10
	return fmt.Sprintf(`# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="llama"} %d
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="llama"} 0
# TYPE vllm:request_success_total counter
vllm:request_success_total{model_name="llama",finished_reason="stop"} %d
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="llama"} %d
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="llama"} %d
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{model_name="llama"} 0.5
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1700000000
`, call, completed, prompt, gen)
}
