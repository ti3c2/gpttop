package config

import (
	"strings"
	"testing"
	"time"

	"gpttop/internal/domain"
)

func TestBuildConfigPrecedenceEnvNormalizeAndRedact(t *testing.T) {
	interval := 7 * time.Second
	noColor := true
	lookup := map[string]string{
		"ENDPOINT_NAME": "prod",
		"URL_PASSWORD":  "url-secret",
		"API_KEY":       "query-secret",
		"MODEL_NAME":    "gemma",
		"AUTH_TOKEN":    "header-secret",
	}
	cfg, err := BuildWithEnv("../testdata/config/full.yaml", Overrides{
		RefreshInterval: &interval,
		NoColor:         &noColor,
		Endpoints: []EndpointFlag{{
			Name: "cli",
			URL:  "localhost:8000",
		}},
	}, func(key string) (string, bool) {
		value, ok := lookup[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RefreshInterval != interval {
		t.Fatalf("refresh interval = %v, want %v", cfg.RefreshInterval, interval)
	}
	if !cfg.NoColor {
		t.Fatal("NoColor override was not applied")
	}
	if len(cfg.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(cfg.Endpoints))
	}

	fileEP := cfg.Endpoints[0]
	if fileEP.Name != "prod" || fileEP.Model != "gemma" {
		t.Fatalf("decoded endpoint = %#v", fileEP)
	}
	if fileEP.MetricsURL != "https://user:url-secret@example.test:8443/metrics?api_key=query-secret&debug=1" {
		t.Fatalf("metrics URL = %q", fileEP.MetricsURL)
	}
	if strings.Contains(fileEP.SanitizedURL, "url-secret") || strings.Contains(fileEP.SanitizedURL, "query-secret") {
		t.Fatalf("sanitized URL leaked secret: %s", fileEP.SanitizedURL)
	}
	if !strings.Contains(fileEP.SanitizedURL, "debug=1") {
		t.Fatalf("sanitized URL should preserve non-secret query: %s", fileEP.SanitizedURL)
	}
	redactedHeaders := RedactHeaders(fileEP.Headers)
	if redactedHeaders["Authorization"] != redacted || redactedHeaders["X-Trace-Id"] != "trace-123" {
		t.Fatalf("redacted headers = %#v", redactedHeaders)
	}

	cliEP := cfg.Endpoints[1]
	if cliEP.MetricsURL != "http://localhost:8000/metrics" {
		t.Fatalf("CLI endpoint metrics URL = %q", cliEP.MetricsURL)
	}
}

func TestMissingEnvReportsField(t *testing.T) {
	_, err := BuildWithEnv("../testdata/config/full.yaml", Overrides{}, func(key string) (string, bool) {
		return "", false
	})
	if err == nil {
		t.Fatal("expected missing env error")
	}
	if !strings.Contains(err.Error(), "endpoints[0].name") || !strings.Contains(err.Error(), "ENDPOINT_NAME") {
		t.Fatalf("error = %v", err)
	}
}

func TestDuplicateNamesRejectedAfterMerge(t *testing.T) {
	_, err := BuildWithEnv("", Overrides{Endpoints: []EndpointFlag{
		{Name: "same", URL: "http://one.test"},
		{Name: "same", URL: "http://two.test"},
	}}, nil)
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
	if !strings.Contains(err.Error(), "not unique") {
		t.Fatalf("error = %v", err)
	}
}

func TestEndpointFlagsParseNameAndURL(t *testing.T) {
	var flags EndpointFlags
	if err := flags.Set("local=http://127.0.0.1:8000"); err != nil {
		t.Fatal(err)
	}
	if err := flags.Set("http://example.test/metrics?x=1"); err != nil {
		t.Fatal(err)
	}
	if len(flags) != 2 || flags[0].Name != "local" || flags[1].Name != "" {
		t.Fatalf("flags = %#v", flags)
	}
}

func TestValidateWindows(t *testing.T) {
	cfg := domain.RuntimeConfig{
		RefreshInterval: time.Second,
		ScrapeTimeout:   time.Second,
		CurrentWindow:   time.Minute,
		History:         time.Second,
	}
	if err := NormalizeAndValidate(&cfg); err == nil {
		t.Fatal("expected invalid current_window/history")
	}

	cfg = domain.RuntimeConfig{
		RefreshInterval: 5 * time.Second,
		ScrapeTimeout:   time.Second,
		CurrentWindow:   2 * time.Second,
		History:         15 * time.Minute,
	}
	if err := NormalizeAndValidate(&cfg); err == nil || !strings.Contains(err.Error(), "current_window") {
		t.Fatalf("expected current_window/interval validation, got %v", err)
	}
}

func TestMetricsPathMustBePathOnly(t *testing.T) {
	_, err := NormalizeEndpoint(domain.EndpointConfig{
		Name:        "bad",
		URL:         "https://example.test",
		MetricsPath: "/metrics?token=secret",
	})
	if err == nil || !strings.Contains(err.Error(), "metrics_path") {
		t.Fatalf("expected metrics_path validation error, got %v", err)
	}
}

func TestMetricsPathResolutionStoresResolvedPath(t *testing.T) {
	ep, err := NormalizeEndpoint(domain.EndpointConfig{
		Name:        "ok",
		URL:         "https://example.test/base?api_key=secret",
		MetricsPath: "nested/../metrics",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.MetricsPath != "/metrics" {
		t.Fatalf("metrics path = %q, want /metrics", ep.MetricsPath)
	}
	if ep.MetricsURL != "https://example.test/metrics?api_key=secret" {
		t.Fatalf("metrics URL = %q", ep.MetricsURL)
	}
	if strings.Contains(ep.SanitizedURL, "secret") {
		t.Fatalf("sanitized URL leaked query secret: %s", ep.SanitizedURL)
	}
}
