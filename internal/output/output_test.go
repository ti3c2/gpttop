package output

import (
	"strings"
	"testing"
	"time"

	"gpttop/internal/domain"
)

func TestWriteTable(t *testing.T) {
	snapshot := testSnapshot()
	table, err := Table(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ENDPOINT", "RPS(req/s)", "TTFT95(s)", "prod", "gemma", "UP", "3.2", "-", "0.12"} {
		if !strings.Contains(table, want) {
			t.Fatalf("table missing %q:\n%s", want, table)
		}
	}
	if strings.Contains(table, "0.12~") {
		t.Fatalf("table value contains per-cell partial marker:\n%s", table)
	}
	if strings.Contains(table, "0.12s") {
		t.Fatalf("table should keep units in headers, not cells:\n%s", table)
	}
}

func TestJSONStableAndMissingValuesAreNull(t *testing.T) {
	snapshot := testSnapshot()
	first, err := JSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := JSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("JSON output is unstable:\n%s\n---\n%s", first, second)
	}
	out := string(first)
	for _, want := range []string{
		`"sanitized_url": "https://example.test/metrics"`,
		`"running": null`,
		`"window": "1m"`,
		`"window": "all"`,
		`"reason": "stop"`,
		`"status": "5xx"`,
		`"partial": true`,
		`"min_fifteen_min": null`,
		`"max_fifteen_min": {`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"plots"`) {
		t.Fatalf("JSON should not contain plots:\n%s", out)
	}
}

func TestJSONRedactsEndpointURLs(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Rows[0].EndpointURL = "https://user:password@example.test/base?api_key=query-secret&debug=1"
	snapshot.Rows[0].MetricsURL = "https://user:password@example.test/metrics?api_key=query-secret&debug=1"
	snapshot.Rows[0].SanitizedURL = ""
	out, err := JSON(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, leaked := range []string{"password", "query-secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("JSON leaked %q:\n%s", leaked, text)
		}
	}
	for _, want := range []string{
		`"url": "https://%3Credacted%3E@example.test/base?api_key=%3Credacted%3E&debug=1"`,
		`"metrics_url": "https://%3Credacted%3E@example.test/metrics?api_key=%3Credacted%3E&debug=1"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("JSON missing %q:\n%s", want, text)
		}
	}
}

func testSnapshot() domain.AppSnapshot {
	at := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)
	return domain.AppSnapshot{
		At: at,
		Rows: []domain.ModelSnapshot{{
			EndpointName:        "prod",
			EndpointURL:         "https://example.test",
			MetricsURL:          "https://example.test/metrics",
			SanitizedURL:        "https://example.test/metrics",
			Model:               "gemma",
			State:               domain.StateUP,
			LastSuccess:         at.Add(-2 * time.Second),
			SampleAge:           2 * time.Second,
			ScrapeDuration:      120 * time.Millisecond,
			ConsecutiveFailures: 0,
			Overview: domain.OverviewValues{
				RPS:     domain.Value(3.2, "/s", 10*time.Second, 10*time.Second),
				Running: domain.Missing(0, "absent"),
				Waiting: domain.Value(2, "", 0, 0),
				TTFTP95: domain.Value(0.12, "s", 10*time.Second, 6*time.Second),
			},
			Metrics: []domain.MetricRow{{
				Key:        domain.SemanticCompletedRequestRate,
				Label:      "RPS",
				Kind:       domain.KindCounter,
				Unit:       "/s",
				Now:        domain.Value(3.2, "/s", 10*time.Second, 10*time.Second),
				OneMin:     domain.Missing(time.Minute, "warming"),
				Five:       domain.Missing(5*time.Minute, "warming"),
				Fifteen:    domain.Missing(15*time.Minute, "warming"),
				MinFifteen: domain.Missing(15*time.Minute, "warming"),
				MaxFifteen: domain.Value(4.8, "/s", 15*time.Minute, 10*time.Minute),
			}},
			EngineOutcomes: map[time.Duration][]domain.EngineOutcome{
				domain.OutcomeAllTime: {{Reason: "stop", Count: domain.Value(84, "", domain.OutcomeAllTime, 2*time.Minute)}},
				time.Minute:           {{Reason: "stop", Count: domain.Value(42, "", time.Minute, time.Minute)}},
			},
			HTTPOutcomes: map[time.Duration][]domain.HTTPOutcome{
				domain.OutcomeAllTime: {{Status: "5xx", Method: "POST", Handler: "/v1/chat/completions", Count: domain.Value(2, "", domain.OutcomeAllTime, 2*time.Minute)}},
				time.Minute:           {{Status: "5xx", Method: "POST", Handler: "/v1/chat/completions", Count: domain.Value(1, "", time.Minute, time.Minute)}},
			},
		}},
	}
}
