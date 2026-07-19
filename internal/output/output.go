package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"gpttop/internal/config"
	"gpttop/internal/domain"
)

func WriteTable(w io.Writer, snapshot domain.AppSnapshot) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ENDPOINT\tMODEL\tSTATE\tRPS(req/s)\tRUN(req)\tWAIT(req)\tPROMPT(tok/s)\tGEN(tok/s)\tKV(%)\tHIT(%)\tTTFT95(s)\tE2E95(s)\tERR(count)\tAGE"); err != nil {
		return err
	}
	for _, row := range snapshot.Rows {
		if _, err := fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.EndpointName,
			row.Model,
			row.State,
			formatValue(row.Overview.RPS),
			formatValue(row.Overview.Running),
			formatValue(row.Overview.Waiting),
			formatValue(row.Overview.PromptTokens),
			formatValue(row.Overview.GenerationTokens),
			formatValue(row.Overview.KVCache),
			formatValue(row.Overview.PrefixHitRate),
			formatValue(row.Overview.TTFTP95),
			formatValue(row.Overview.E2EP95),
			formatValue(row.Overview.Errors),
			formatAge(row.SampleAge),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func Table(snapshot domain.AppSnapshot) (string, error) {
	var b strings.Builder
	err := WriteTable(&b, snapshot)
	return b.String(), err
}

func WriteJSON(w io.Writer, snapshot domain.AppSnapshot) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(toJSON(snapshot))
}

func JSON(snapshot domain.AppSnapshot) ([]byte, error) {
	var b bytes.Buffer
	if err := WriteJSON(&b, snapshot); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

type snapshotJSON struct {
	At      string    `json:"at"`
	NoColor bool      `json:"no_color"`
	Rows    []rowJSON `json:"rows"`
}

type rowJSON struct {
	Endpoint       endpointJSON       `json:"endpoint"`
	Model          string             `json:"model"`
	State          domain.State       `json:"state"`
	Scrape         scrapeJSON         `json:"scrape"`
	Overview       overviewJSON       `json:"overview"`
	Metrics        []metricJSON       `json:"metrics"`
	EngineOutcomes []engineWindowJSON `json:"engine_outcomes"`
	HTTPOutcomes   []httpWindowJSON   `json:"http_outcomes"`
	Warnings       []string           `json:"warnings,omitempty"`
}

type endpointJSON struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	MetricsURL   string `json:"metrics_url"`
	SanitizedURL string `json:"sanitized_url"`
}

type scrapeJSON struct {
	LastSuccess         *string `json:"last_success"`
	SampleAgeSeconds    float64 `json:"sample_age_seconds"`
	DurationSeconds     float64 `json:"duration_seconds"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	LastError           string  `json:"last_error,omitempty"`
	Restarted           bool    `json:"restarted"`
	Unsupported         bool    `json:"unsupported"`
}

type overviewJSON struct {
	RPS              *valueJSON `json:"rps"`
	Running          *valueJSON `json:"running"`
	Waiting          *valueJSON `json:"waiting"`
	PromptTokens     *valueJSON `json:"prompt_tokens"`
	GenerationTokens *valueJSON `json:"generation_tokens"`
	PrefixHitRate    *valueJSON `json:"prefix_hit_rate"`
	KVCache          *valueJSON `json:"kv_cache"`
	APICPU           *valueJSON `json:"api_cpu"`
	TTFTP95          *valueJSON `json:"ttft_p95"`
	E2EP95           *valueJSON `json:"e2e_p95"`
	Errors           *valueJSON `json:"errors"`
}

type metricJSON struct {
	Key     domain.Semantic   `json:"key"`
	Label   string            `json:"label"`
	Kind    domain.MetricKind `json:"kind"`
	Unit    string            `json:"unit"`
	Now     *valueJSON        `json:"now"`
	OneMin  *valueJSON        `json:"one_min"`
	FiveMin *valueJSON        `json:"five_min"`
	Fifteen *valueJSON        `json:"fifteen_min"`
	Min15   *valueJSON        `json:"min_fifteen_min"`
	Max15   *valueJSON        `json:"max_fifteen_min"`
}

type valueJSON struct {
	Value           float64 `json:"value"`
	Unit            string  `json:"unit,omitempty"`
	Partial         bool    `json:"partial"`
	CoverageSeconds float64 `json:"coverage_seconds"`
	WindowSeconds   float64 `json:"window_seconds"`
	Note            string  `json:"note,omitempty"`
	MoreThan        bool    `json:"more_than,omitempty"`
}

type engineWindowJSON struct {
	WindowSeconds float64             `json:"window_seconds"`
	Window        string              `json:"window"`
	Outcomes      []engineOutcomeJSON `json:"outcomes"`
}

type engineOutcomeJSON struct {
	Reason string     `json:"reason"`
	Count  *valueJSON `json:"count"`
}

type httpWindowJSON struct {
	WindowSeconds float64           `json:"window_seconds"`
	Window        string            `json:"window"`
	Outcomes      []httpOutcomeJSON `json:"outcomes"`
}

type httpOutcomeJSON struct {
	Status  string     `json:"status"`
	Method  string     `json:"method,omitempty"`
	Handler string     `json:"handler,omitempty"`
	Count   *valueJSON `json:"count"`
}

func toJSON(snapshot domain.AppSnapshot) snapshotJSON {
	out := snapshotJSON{
		At:      formatTime(snapshot.At),
		NoColor: snapshot.NoColor,
		Rows:    make([]rowJSON, 0, len(snapshot.Rows)),
	}
	for _, row := range snapshot.Rows {
		out.Rows = append(out.Rows, rowToJSON(row))
	}
	return out
}

func rowToJSON(row domain.ModelSnapshot) rowJSON {
	sanitizedMetricsURL := sanitizeMaybe(row.SanitizedURL)
	if sanitizedMetricsURL == "" {
		sanitizedMetricsURL = sanitizeMaybe(row.MetricsURL)
	}
	if sanitizedMetricsURL == "" {
		sanitizedMetricsURL = sanitizeMaybe(row.EndpointURL)
	}
	return rowJSON{
		Endpoint: endpointJSON{
			Name:         row.EndpointName,
			URL:          sanitizeMaybe(row.EndpointURL),
			MetricsURL:   sanitizeMaybe(row.MetricsURL),
			SanitizedURL: sanitizedMetricsURL,
		},
		Model: row.Model,
		State: row.State,
		Scrape: scrapeJSON{
			LastSuccess:         optionalTime(row.LastSuccess),
			SampleAgeSeconds:    seconds(row.SampleAge),
			DurationSeconds:     seconds(row.ScrapeDuration),
			ConsecutiveFailures: row.ConsecutiveFailures,
			LastError:           row.LastError,
			Restarted:           row.Restarted,
			Unsupported:         row.Unsupported,
		},
		Overview:       overviewToJSON(row.Overview),
		Metrics:        metricsToJSON(row.Metrics),
		EngineOutcomes: engineOutcomesToJSON(row.EngineOutcomes),
		HTTPOutcomes:   httpOutcomesToJSON(row.HTTPOutcomes),
		Warnings:       append([]string(nil), row.Warnings...),
	}
}

func sanitizeMaybe(raw string) string {
	if raw == "" {
		return ""
	}
	return config.SanitizeURL(raw)
}

func overviewToJSON(v domain.OverviewValues) overviewJSON {
	return overviewJSON{
		RPS:              valueToJSON(v.RPS),
		Running:          valueToJSON(v.Running),
		Waiting:          valueToJSON(v.Waiting),
		PromptTokens:     valueToJSON(v.PromptTokens),
		GenerationTokens: valueToJSON(v.GenerationTokens),
		PrefixHitRate:    valueToJSON(v.PrefixHitRate),
		KVCache:          valueToJSON(v.KVCache),
		APICPU:           valueToJSON(v.APICPU),
		TTFTP95:          valueToJSON(v.TTFTP95),
		E2EP95:           valueToJSON(v.E2EP95),
		Errors:           valueToJSON(v.Errors),
	}
}

func metricsToJSON(rows []domain.MetricRow) []metricJSON {
	out := make([]metricJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, metricJSON{
			Key:     row.Key,
			Label:   row.Label,
			Kind:    row.Kind,
			Unit:    row.Unit,
			Now:     valueToJSON(row.Now),
			OneMin:  valueToJSON(row.OneMin),
			FiveMin: valueToJSON(row.Five),
			Fifteen: valueToJSON(row.Fifteen),
			Min15:   valueToJSON(row.MinFifteen),
			Max15:   valueToJSON(row.MaxFifteen),
		})
	}
	return out
}

func engineOutcomesToJSON(outcomes map[time.Duration][]domain.EngineOutcome) []engineWindowJSON {
	windows := sortedWindows(outcomes)
	out := make([]engineWindowJSON, 0, len(windows))
	for _, window := range windows {
		items := append([]domain.EngineOutcome(nil), outcomes[window]...)
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].Reason < items[j].Reason
		})
		windowJSON := engineWindowJSON{WindowSeconds: seconds(window), Window: domain.DurationLabel(window)}
		for _, item := range items {
			windowJSON.Outcomes = append(windowJSON.Outcomes, engineOutcomeJSON{
				Reason: item.Reason,
				Count:  valueToJSON(item.Count),
			})
		}
		out = append(out, windowJSON)
	}
	return out
}

func httpOutcomesToJSON(outcomes map[time.Duration][]domain.HTTPOutcome) []httpWindowJSON {
	windows := sortedWindows(outcomes)
	out := make([]httpWindowJSON, 0, len(windows))
	for _, window := range windows {
		items := append([]domain.HTTPOutcome(nil), outcomes[window]...)
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Status != items[j].Status {
				return items[i].Status < items[j].Status
			}
			if items[i].Method != items[j].Method {
				return items[i].Method < items[j].Method
			}
			return items[i].Handler < items[j].Handler
		})
		windowJSON := httpWindowJSON{WindowSeconds: seconds(window), Window: domain.DurationLabel(window)}
		for _, item := range items {
			windowJSON.Outcomes = append(windowJSON.Outcomes, httpOutcomeJSON{
				Status:  item.Status,
				Method:  item.Method,
				Handler: item.Handler,
				Count:   valueToJSON(item.Count),
			})
		}
		out = append(out, windowJSON)
	}
	return out
}

func valueToJSON(value domain.WindowValue) *valueJSON {
	if !value.Available {
		return nil
	}
	return &valueJSON{
		Value:           value.Value,
		Unit:            value.Unit,
		Partial:         value.Partial,
		CoverageSeconds: seconds(value.Coverage),
		WindowSeconds:   seconds(value.Window),
		Note:            value.Note,
		MoreThan:        value.MoreThan,
	}
}

func sortedWindows[T any](m map[time.Duration][]T) []time.Duration {
	windows := make([]time.Duration, 0, len(m))
	for window := range m {
		windows = append(windows, window)
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i] == domain.OutcomeAllTime {
			return false
		}
		if windows[j] == domain.OutcomeAllTime {
			return true
		}
		return windows[i] < windows[j]
	})
	return windows
}

func optionalTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	formatted := formatTime(t)
	return &formatted
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func seconds(d time.Duration) float64 {
	return float64(d) / float64(time.Second)
}

func formatValue(value domain.WindowValue) string {
	if !value.Available {
		return "-"
	}
	prefix := ""
	if value.MoreThan {
		prefix = ">"
	}
	suffix := ""
	switch value.Unit {
	case "%":
		return fmt.Sprintf("%s%.1f%s", prefix, value.Value, suffix)
	case "s":
		return fmt.Sprintf("%s%.2f%s", prefix, value.Value, suffix)
	case "/s", "t/s", "req/s", "requests/s", "tokens/s":
		return fmt.Sprintf("%s%.1f%s", prefix, value.Value, suffix)
	default:
		if value.Value == float64(int64(value.Value)) {
			return fmt.Sprintf("%s%.0f%s", prefix, value.Value, suffix)
		}
		return fmt.Sprintf("%s%.2f%s", prefix, value.Value, suffix)
	}
}

func formatAge(age time.Duration) string {
	if age <= 0 {
		return "-"
	}
	if age < time.Second {
		return fmt.Sprintf("%dms", age.Milliseconds())
	}
	if age < time.Minute {
		return fmt.Sprintf("%ds", int(age/time.Second))
	}
	return fmt.Sprintf("%dm", int(age/time.Minute))
}
