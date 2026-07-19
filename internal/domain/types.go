package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	DefaultInterval      = 2 * time.Second
	DefaultTimeout       = 3 * time.Second
	DefaultCurrentWindow = 10 * time.Second
	DefaultHistory       = 20 * time.Minute
	DefaultBodyLimit     = 32 << 20
	DefaultSeriesGrace   = 30 * time.Second
	OutcomeAllTime       = time.Duration(0)
)

var DefaultWindows = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

type MetricKind string

const (
	KindGauge     MetricKind = "gauge"
	KindCounter   MetricKind = "counter"
	KindHistogram MetricKind = "histogram"
	KindRatio     MetricKind = "ratio"
)

type Semantic string

const (
	SemanticRequestsRunning      Semantic = "requests_running"
	SemanticRequestsWaiting      Semantic = "requests_waiting"
	SemanticPromptTokens         Semantic = "prompt_tokens"
	SemanticGenerationTokens     Semantic = "generation_tokens"
	SemanticPrefixQueries        Semantic = "prefix_queries"
	SemanticPrefixHits           Semantic = "prefix_hits"
	SemanticLegacyPrefixHitRate  Semantic = "legacy_prefix_hit_rate"
	SemanticKVCacheUsage         Semantic = "kv_cache_usage"
	SemanticCompletionOutcomes   Semantic = "completion_outcomes"
	SemanticHTTPOutcomes         Semantic = "http_outcomes"
	SemanticAPICPU               Semantic = "api_cpu"
	SemanticProcessStart         Semantic = "process_start"
	SemanticTTFT                 Semantic = "ttft"
	SemanticInterTokenLatency    Semantic = "inter_token_latency"
	SemanticE2ELatency           Semantic = "e2e_latency"
	SemanticQueueTime            Semantic = "queue_time"
	SemanticInferenceTime        Semantic = "inference_time"
	SemanticPrefillTime          Semantic = "prefill_time"
	SemanticDecodeTime           Semantic = "decode_time"
	SemanticPreemptions          Semantic = "preemptions"
	SemanticCorruptedRequests    Semantic = "corrupted_requests"
	SemanticCompletedRequestRate Semantic = "completed_request_rate"
	SemanticPrefixHitRatio       Semantic = "prefix_hit_ratio"
	SemanticTTFTP50              Semantic = "ttft_p50"
	SemanticTTFTP95              Semantic = "ttft_p95"
	SemanticITLP50               Semantic = "itl_p50"
	SemanticITLP95               Semantic = "itl_p95"
	SemanticE2EP50               Semantic = "e2e_p50"
	SemanticE2EP95               Semantic = "e2e_p95"
	SemanticQueueP95             Semantic = "queue_p95"
	SemanticInferenceP95         Semantic = "inference_p95"
	SemanticPrefillP95           Semantic = "prefill_p95"
	SemanticDecodeP95            Semantic = "decode_p95"
	SemanticPreemptionRate       Semantic = "preemption_rate"
	SemanticCorruptedRequestRate Semantic = "corrupted_request_rate"
)

type EndpointConfig struct {
	Name         string
	URL          string
	MetricsPath  string
	MetricsURL   string
	SanitizedURL string
	Model        string
	Headers      map[string]string
}

type RuntimeConfig struct {
	RefreshInterval time.Duration
	ScrapeTimeout   time.Duration
	CurrentWindow   time.Duration
	History         time.Duration
	Windows         []time.Duration
	Endpoints       []EndpointConfig
	NoColor         bool
	Demo            bool
}

type Label struct {
	Name  string
	Value string
}

type LabelSet map[string]string

func (l LabelSet) Clone() LabelSet {
	out := make(LabelSet, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

func (l LabelSet) Key() string {
	if len(l) == 0 {
		return ""
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(l[k])
		b.WriteByte('\xff')
	}
	return b.String()
}

func (l LabelSet) Without(names ...string) LabelSet {
	remove := make(map[string]struct{}, len(names))
	for _, name := range names {
		remove[name] = struct{}{}
	}
	out := make(LabelSet, len(l))
	for k, v := range l {
		if _, ok := remove[k]; !ok {
			out[k] = v
		}
	}
	return out
}

type SeriesPoint struct {
	Name     string
	Semantic Semantic
	Kind     MetricKind
	Labels   LabelSet
	Value    float64
}

func (s SeriesPoint) ID() string {
	return SeriesID(s.Name, s.Labels)
}

type Bucket struct {
	Upper float64
	Count uint64
}

type HistogramPoint struct {
	Name     string
	Semantic Semantic
	Labels   LabelSet
	Buckets  []Bucket
	Count    uint64
	Sum      float64
}

func (h HistogramPoint) ID() string {
	return SeriesID(h.Name, h.Labels)
}

func SeriesID(name string, labels LabelSet) string {
	return name + "{" + labels.Key() + "}"
}

type RawSample struct {
	EndpointName string
	EndpointURL  string
	MetricsURL   string
	At           time.Time
	Series       []SeriesPoint
	Histograms   []HistogramPoint
	ProcessStart *float64
	Recognized   bool
}

// OutcomeCounterTotal is a reset-safe counter increase observed since gpttop started.
type OutcomeCounterTotal struct {
	Series   SeriesPoint
	Delta    float64
	Coverage time.Duration
}

type ScrapeStatus struct {
	EndpointName       string
	MetricsURL         string
	SanitizedURL       string
	At                 time.Time
	Success            bool
	Duration           time.Duration
	LastSuccess        time.Time
	ConsecutiveFailure int
	Error              string
	Recognized         bool
}

type EndpointUpdate struct {
	Target EndpointConfig
	Sample *RawSample
	Status ScrapeStatus
}

type State string

const (
	StateUP          State = "UP"
	StateWarming     State = "WARMING"
	StateStale       State = "STALE"
	StateDown        State = "DOWN"
	StateUnsupported State = "UNSUPPORTED"
)

type WindowValue struct {
	Value     float64       `json:"value,omitempty"`
	Unit      string        `json:"unit,omitempty"`
	Available bool          `json:"available"`
	Partial   bool          `json:"partial,omitempty"`
	Coverage  time.Duration `json:"coverage,omitempty"`
	Window    time.Duration `json:"window,omitempty"`
	Note      string        `json:"note,omitempty"`
	MoreThan  bool          `json:"more_than,omitempty"`
}

func Missing(window time.Duration, note string) WindowValue {
	return WindowValue{Available: false, Window: window, Note: note}
}

func Value(v float64, unit string, window, coverage time.Duration) WindowValue {
	partial := window > 0 && coverage+time.Millisecond < window
	return WindowValue{Value: v, Unit: unit, Available: true, Partial: partial, Coverage: coverage, Window: window}
}

func GreaterThan(v float64, unit string, window, coverage time.Duration) WindowValue {
	w := Value(v, unit, window, coverage)
	w.MoreThan = true
	return w
}

type MetricRow struct {
	Key        Semantic
	Label      string
	Kind       MetricKind
	Unit       string
	Now        WindowValue
	OneMin     WindowValue
	Five       WindowValue
	Fifteen    WindowValue
	MinFifteen WindowValue
	MaxFifteen WindowValue
}

type OverviewValues struct {
	RPS              WindowValue
	Running          WindowValue
	Waiting          WindowValue
	PromptTokens     WindowValue
	GenerationTokens WindowValue
	PrefixHitRate    WindowValue
	KVCache          WindowValue
	APICPU           WindowValue
	TTFTP95          WindowValue
	E2EP95           WindowValue
	Errors           WindowValue
}

type EngineOutcome struct {
	Reason string
	Count  WindowValue
}

type HTTPOutcome struct {
	Status  string
	Method  string
	Handler string
	Count   WindowValue
}

type ModelSnapshot struct {
	EndpointName        string
	EndpointURL         string
	MetricsURL          string
	SanitizedURL        string
	Model               string
	State               State
	LastSuccess         time.Time
	SampleAge           time.Duration
	ObservationDuration time.Duration
	ScrapeDuration      time.Duration
	ConsecutiveFailures int
	LastError           string
	Restarted           bool
	Unsupported         bool
	Overview            OverviewValues
	Metrics             []MetricRow
	EngineOutcomes      map[time.Duration][]EngineOutcome
	HTTPOutcomes        map[time.Duration][]HTTPOutcome
	Warnings            []string
}

type AppSnapshot struct {
	At      time.Time
	Rows    []ModelSnapshot
	NoColor bool
}

type SnapshotProvider interface {
	Snapshot(now time.Time) AppSnapshot
}

func DurationLabel(d time.Duration) string {
	switch d {
	case 0:
		return "all"
	case time.Minute:
		return "1m"
	case 5 * time.Minute:
		return "5m"
	case 15 * time.Minute:
		return "15m"
	default:
		if d%time.Minute == 0 {
			return fmt.Sprintf("%dm", int(d/time.Minute))
		}
		if d%time.Second == 0 {
			return fmt.Sprintf("%ds", int(d/time.Second))
		}
		return d.String()
	}
}

func ModelName(labels LabelSet, configured string) string {
	if configured != "" {
		return configured
	}
	if labels != nil && labels["model_name"] != "" {
		return labels["model_name"]
	}
	return "<unknown>"
}

func IsFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
