package aggregate

import (
	"sort"
	"strings"
	"time"

	"gpttop/internal/derive"
	"gpttop/internal/domain"
	"gpttop/internal/history"
)

const (
	unitCount  = "count"
	unitCores  = "cores"
	unitPct    = "%"
	unitReq    = "requests"
	unitReqPS  = "requests/s"
	unitSec    = "s"
	unitTokens = "tokens/s"
)

// BuildSnapshot derives all endpoint/model rows from bounded scrape history.
func BuildSnapshot(store *history.Store, config domain.RuntimeConfig, now time.Time) domain.AppSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	if config.CurrentWindow <= 0 {
		config.CurrentWindow = domain.DefaultCurrentWindow
	}
	if len(config.Windows) == 0 {
		config.Windows = domain.DefaultWindows
	}
	samplesByEndpoint := map[string][]domain.RawSample{}
	statuses := map[string]domain.ScrapeStatus{}
	outcomeTotalsByEndpoint := map[string][]domain.OutcomeCounterTotal{}
	if store != nil {
		samplesByEndpoint = store.AllSamples()
		statuses = store.Statuses()
		outcomeTotalsByEndpoint = store.AllOutcomeTotals()
	}

	endpoints := configuredEndpoints(config, samplesByEndpoint, statuses)
	rows := make([]domain.ModelSnapshot, 0, len(endpoints))
	for _, target := range endpoints {
		samples := samplesByEndpoint[target.Name]
		models := modelsForEndpoint(samples, target.Model, now)
		if len(models) == 0 {
			models = []string{domain.ModelName(nil, target.Model)}
		}
		for _, model := range models {
			rows = append(rows, buildModelSnapshot(samples, outcomeTotalsByEndpoint[target.Name], statuses[target.Name], target, model, len(models), config, now))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].EndpointName == rows[j].EndpointName {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].EndpointName < rows[j].EndpointName
	})
	return domain.AppSnapshot{At: now, Rows: rows, NoColor: config.NoColor}
}

// Provider implements domain.SnapshotProvider over a history store.
type Provider struct {
	Store  *history.Store
	Config domain.RuntimeConfig
}

// Snapshot builds a fresh application snapshot.
func (p Provider) Snapshot(now time.Time) domain.AppSnapshot {
	return BuildSnapshot(p.Store, p.Config, now)
}

func configuredEndpoints(config domain.RuntimeConfig, samples map[string][]domain.RawSample, statuses map[string]domain.ScrapeStatus) []domain.EndpointConfig {
	seen := map[string]bool{}
	var endpoints []domain.EndpointConfig
	for _, endpoint := range config.Endpoints {
		if endpoint.Name == "" {
			endpoint.Name = endpoint.MetricsURL
			if endpoint.Name == "" {
				endpoint.Name = endpoint.URL
			}
		}
		if endpoint.Name == "" || seen[endpoint.Name] {
			continue
		}
		seen[endpoint.Name] = true
		endpoints = append(endpoints, endpoint)
	}
	for name, endpointSamples := range samples {
		if seen[name] {
			continue
		}
		seen[name] = true
		endpoint := domain.EndpointConfig{Name: name}
		if len(endpointSamples) > 0 {
			last := endpointSamples[len(endpointSamples)-1]
			endpoint.URL = last.EndpointURL
			endpoint.MetricsURL = last.MetricsURL
		}
		endpoints = append(endpoints, endpoint)
	}
	for name, status := range statuses {
		if seen[name] {
			continue
		}
		seen[name] = true
		endpoints = append(endpoints, domain.EndpointConfig{
			Name:         name,
			MetricsURL:   status.MetricsURL,
			SanitizedURL: status.SanitizedURL,
		})
	}
	return endpoints
}

func buildModelSnapshot(samples []domain.RawSample, outcomeTotals []domain.OutcomeCounterTotal, status domain.ScrapeStatus, target domain.EndpointConfig, model string, modelCount int, config domain.RuntimeConfig, now time.Time) domain.ModelSnapshot {
	sort.SliceStable(samples, func(i, j int) bool {
		return samples[i].At.Before(samples[j].At)
	})
	row := domain.ModelSnapshot{
		EndpointName:        target.Name,
		EndpointURL:         target.URL,
		MetricsURL:          target.MetricsURL,
		SanitizedURL:        target.SanitizedURL,
		Model:               model,
		State:               stateFor(status, samples, config, now),
		LastSuccess:         status.LastSuccess,
		ScrapeDuration:      status.Duration,
		ConsecutiveFailures: status.ConsecutiveFailure,
		LastError:           status.Error,
		Unsupported:         status.At.IsZero() == false && !status.Recognized,
		EngineOutcomes:      map[time.Duration][]domain.EngineOutcome{},
		HTTPOutcomes:        map[time.Duration][]domain.HTTPOutcome{},
	}
	if target.MetricsURL == "" && len(samples) > 0 {
		row.MetricsURL = samples[len(samples)-1].MetricsURL
	}
	if target.URL == "" && len(samples) > 0 {
		row.EndpointURL = samples[len(samples)-1].EndpointURL
	}
	if target.SanitizedURL == "" {
		row.SanitizedURL = status.SanitizedURL
	}
	if !status.LastSuccess.IsZero() {
		row.SampleAge = now.Sub(status.LastSuccess)
	} else if len(samples) > 0 {
		row.SampleAge = now.Sub(samples[len(samples)-1].At)
		row.LastSuccess = samples[len(samples)-1].At
	}
	if len(samples) > 1 {
		row.ObservationDuration = samples[len(samples)-1].At.Sub(samples[0].At)
	}
	row.Restarted = restarted(samples)

	windows := displayWindows(config)
	row.Metrics = []domain.MetricRow{
		counterRateRow(samples, model, target.Model, domain.SemanticCompletionOutcomes, domain.SemanticCompletedRequestRate, "RPS", unitReqPS, config.CurrentWindow, windows),
		gaugeRow(samples, model, target.Model, domain.SemanticRequestsRunning, "Requests running", unitReq, gaugeSum, windows, maxGaugeGap(config)),
		gaugeRow(samples, model, target.Model, domain.SemanticRequestsWaiting, "Requests waiting", unitReq, gaugeSum, windows, maxGaugeGap(config)),
		counterRateRow(samples, model, target.Model, domain.SemanticPromptTokens, domain.SemanticPromptTokens, "Prompt throughput", unitTokens, config.CurrentWindow, windows),
		counterRateRow(samples, model, target.Model, domain.SemanticGenerationTokens, domain.SemanticGenerationTokens, "Generation throughput", unitTokens, config.CurrentWindow, windows),
		prefixRatioRow(samples, model, target.Model, "Prefix-cache hit", config.CurrentWindow, windows),
		kvRow(samples, model, target.Model, windows, maxGaugeGap(config)),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticTTFT, domain.SemanticTTFTP50, "TTFT p50", 0.50, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticTTFT, domain.SemanticTTFTP95, "TTFT p95", 0.95, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticInterTokenLatency, domain.SemanticITLP50, "ITL p50", 0.50, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticInterTokenLatency, domain.SemanticITLP95, "ITL p95", 0.95, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticE2ELatency, domain.SemanticE2EP50, "E2E p50", 0.50, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticE2ELatency, domain.SemanticE2EP95, "E2E p95", 0.95, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticQueueTime, domain.SemanticQueueP95, "Queue p95", 0.95, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticInferenceTime, domain.SemanticInferenceP95, "Inference p95", 0.95, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticPrefillTime, domain.SemanticPrefillP95, "Prefill p95", 0.95, config.CurrentWindow, windows),
		histogramQuantileRow(samples, model, target.Model, domain.SemanticDecodeTime, domain.SemanticDecodeP95, "Decode p95", 0.95, config.CurrentWindow, windows),
		counterRateRow(samples, model, target.Model, domain.SemanticPreemptions, domain.SemanticPreemptionRate, "Preemptions", unitReqPS, config.CurrentWindow, windows),
		counterRateRow(samples, model, target.Model, domain.SemanticCorruptedRequests, domain.SemanticCorruptedRequestRate, "Corrupted requests", unitReqPS, config.CurrentWindow, windows),
	}
	if modelCount == 1 {
		row.Metrics = append(row.Metrics, counterRateRow(samples, model, target.Model, domain.SemanticAPICPU, domain.SemanticAPICPU, "API CPU (cores)", unitCores, config.CurrentWindow, windows))
	}
	if usesLegacyPrefixHitRate(samples, model, target.Model) {
		row.Warnings = append(row.Warnings, "prefix-cache hit uses a legacy gauge; it is not counter-window semantics")
	}
	row.Overview = overviewFromRows(row.Metrics, samples, model, target.Model, modelCount, config.CurrentWindow)
	row.EngineOutcomes = engineOutcomes(samples, outcomeTotals, model, target.Model)
	if modelCount == 1 {
		row.HTTPOutcomes = httpOutcomes(samples, outcomeTotals)
	}
	return row
}

func displayWindows(config domain.RuntimeConfig) []time.Duration {
	out := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
	for i := range out {
		if i < len(config.Windows) && config.Windows[i] > 0 {
			out[i] = config.Windows[i]
		}
	}
	return out
}

func stateFor(status domain.ScrapeStatus, samples []domain.RawSample, config domain.RuntimeConfig, now time.Time) domain.State {
	if !status.At.IsZero() && !status.Recognized {
		return domain.StateUnsupported
	}
	if len(samples) == 0 {
		if status.At.IsZero() {
			return domain.StateWarming
		}
		return domain.StateDown
	}
	last := samples[len(samples)-1].At
	if status.Success {
		if config.RefreshInterval <= 0 {
			config.RefreshInterval = domain.DefaultInterval
		}
		if now.Sub(last) > config.RefreshInterval+domain.DefaultSeriesGrace {
			return domain.StateStale
		}
		return domain.StateUP
	}
	if now.Sub(last) > domain.DefaultSeriesGrace {
		return domain.StateDown
	}
	return domain.StateStale
}

func modelsForEndpoint(samples []domain.RawSample, configured string, now time.Time) []string {
	if configured != "" {
		return []string{configured}
	}
	latest := map[string]time.Time{}
	for _, sample := range samples {
		for _, series := range sample.Series {
			if series.Labels["model_name"] != "" {
				latest[series.Labels["model_name"]] = sample.At
			}
		}
		for _, hist := range sample.Histograms {
			if hist.Labels["model_name"] != "" {
				latest[hist.Labels["model_name"]] = sample.At
			}
		}
	}
	if len(latest) == 0 && len(samples) > 0 {
		latest[domain.ModelName(nil, "")] = samples[len(samples)-1].At
	}
	out := make([]string, 0, len(latest))
	for model, at := range latest {
		if now.IsZero() || now.Sub(at) <= domain.DefaultSeriesGrace || at.Equal(lastSampleAt(samples)) {
			out = append(out, model)
		}
	}
	sort.Strings(out)
	return out
}

func modelMatches(labels domain.LabelSet, model, configured string) bool {
	if configured != "" {
		return true
	}
	if labels["model_name"] == "" && model == domain.ModelName(nil, "") {
		return true
	}
	return domain.ModelName(labels, "") == model
}

type gaugeMode int

const (
	gaugeSum gaugeMode = iota
	gaugeMax
)

func gaugeRow(samples []domain.RawSample, model, configured string, semantic domain.Semantic, label, unit string, mode gaugeMode, windows []time.Duration, maxGap time.Duration) domain.MetricRow {
	points := gaugePoints(samples, model, configured, semantic, mode, 1)
	return makeGaugeRow(semantic, label, unit, points, windows, maxGap)
}

func kvRow(samples []domain.RawSample, model, configured string, windows []time.Duration, maxGap time.Duration) domain.MetricRow {
	points := gaugePoints(samples, model, configured, domain.SemanticKVCacheUsage, gaugeMax, 100)
	return makeGaugeRow(domain.SemanticKVCacheUsage, "KV-cache usage", unitPct, points, windows, maxGap)
}

func makeGaugeRow(key domain.Semantic, label, unit string, points []derive.TimedValue, windows []time.Duration, maxGap time.Duration) domain.MetricRow {
	now := time.Time{}
	if len(points) > 0 {
		now = points[len(points)-1].At
	}
	row := domain.MetricRow{Key: key, Label: label, Kind: domain.KindGauge, Unit: unit}
	row.Now = gaugeWindowValue(derive.GaugeLatest(points, now), 0, unit)
	row.OneMin = gaugeWindowValue(derive.GaugeMeanBounded(points, now, windows[0], maxGap), windows[0], unit)
	row.Five = gaugeWindowValue(derive.GaugeMeanBounded(points, now, windows[1], maxGap), windows[1], unit)
	row.Fifteen = gaugeWindowValue(derive.GaugeMeanBounded(points, now, windows[2], maxGap), windows[2], unit)
	row.MinFifteen, row.MaxFifteen = gaugeExtrema(points, now, windows[2], unit)
	return row
}

func gaugePoints(samples []domain.RawSample, model, configured string, semantic domain.Semantic, mode gaugeMode, scale float64) []derive.TimedValue {
	var points []derive.TimedValue
	for _, sample := range samples {
		var found bool
		var value float64
		for _, series := range sample.Series {
			if series.Semantic != semantic || !modelMatches(series.Labels, model, configured) {
				continue
			}
			if !found {
				value = series.Value
				found = true
				continue
			}
			if mode == gaugeMax {
				if series.Value > value {
					value = series.Value
				}
			} else {
				value += series.Value
			}
		}
		if found {
			points = append(points, derive.TimedValue{At: sample.At, Value: value * scale, Restart: sampleRestart(samples, sample.At)})
		}
	}
	return points
}

func counterRateRow(samples []domain.RawSample, model, configured string, semantic, key domain.Semantic, label, unit string, currentWindow time.Duration, windows []time.Duration) domain.MetricRow {
	series := counterSeries(samples, model, configured, semantic, semantic != domain.SemanticAPICPU)
	row := domain.MetricRow{Key: key, Label: label, Kind: domain.KindCounter, Unit: unit}
	end := lastSampleAt(samples)
	row.Now = rateWindowValue(derive.SumCounterWindows(series, end, currentWindow), currentWindow, unit)
	row.OneMin = rateWindowValue(derive.SumCounterWindows(series, end, windows[0]), windows[0], unit)
	row.Five = rateWindowValue(derive.SumCounterWindows(series, end, windows[1]), windows[1], unit)
	row.Fifteen = rateWindowValue(derive.SumCounterWindows(series, end, windows[2]), windows[2], unit)
	row.MinFifteen, row.MaxFifteen = counterRateExtrema(samples, series, end, currentWindow, windows[2], unit)
	return row
}

func prefixRatioRow(samples []domain.RawSample, model, configured, label string, currentWindow time.Duration, windows []time.Duration) domain.MetricRow {
	hits := counterSeries(samples, model, configured, domain.SemanticPrefixHits, true)
	queries := counterSeries(samples, model, configured, domain.SemanticPrefixQueries, true)
	legacy := gaugePoints(samples, model, configured, domain.SemanticLegacyPrefixHitRate, gaugeMax, 100)
	end := lastSampleAt(samples)
	row := domain.MetricRow{Key: domain.SemanticPrefixHitRatio, Label: label, Kind: domain.KindRatio, Unit: unitPct}
	row.Now = ratioWindowValue(hits, queries, legacy, end, currentWindow)
	row.OneMin = ratioWindowValue(hits, queries, legacy, end, windows[0])
	row.Five = ratioWindowValue(hits, queries, legacy, end, windows[1])
	row.Fifteen = ratioWindowValue(hits, queries, legacy, end, windows[2])
	row.MinFifteen, row.MaxFifteen = ratioExtrema(samples, hits, queries, legacy, end, currentWindow, windows[2])
	return row
}

func ratioWindowValue(hits, queries map[string][]derive.TimedValue, legacy []derive.TimedValue, end time.Time, window time.Duration) domain.WindowValue {
	h := derive.SumCounterWindows(hits, end, window)
	q := derive.SumCounterWindows(queries, end, window)
	if h.Available && q.Available && q.Delta > 0 {
		return domain.Value(h.Delta/q.Delta*100, unitPct, window, minPositiveDuration(h.Coverage, q.Coverage))
	}
	g := derive.GaugeMean(legacy, end, window)
	if g.Available {
		value := domain.Value(g.Value, unitPct, window, g.Coverage)
		value.Note = "legacy gauge; not counter-window semantics"
		return value
	}
	return domain.Missing(window, "unavailable")
}

func histogramQuantileRow(samples []domain.RawSample, model, configured string, semantic, key domain.Semantic, label string, q float64, currentWindow time.Duration, windows []time.Duration) domain.MetricRow {
	series := histogramSeries(samples, model, configured, semantic)
	end := lastSampleAt(samples)
	row := domain.MetricRow{Key: key, Label: label, Kind: domain.KindHistogram, Unit: unitSec}
	row.Now = histogramWindowValue(series, end, currentWindow, q)
	row.OneMin = histogramWindowValue(series, end, windows[0], q)
	row.Five = histogramWindowValue(series, end, windows[1], q)
	row.Fifteen = histogramWindowValue(series, end, windows[2], q)
	row.MinFifteen, row.MaxFifteen = histogramExtrema(samples, series, end, currentWindow, windows[2], q)
	return row
}

func histogramWindowValue(series map[string][]derive.TimedHistogram, end time.Time, window time.Duration, q float64) domain.WindowValue {
	result := derive.SumHistogramWindows(series, end, window)
	if !result.Available {
		return domain.Missing(window, "unavailable")
	}
	qv := derive.QuantileWithBound(q, result.Buckets)
	if !qv.Available {
		return domain.Missing(window, "unavailable")
	}
	if qv.MoreThan {
		return domain.GreaterThan(qv.Value, unitSec, window, result.Coverage)
	}
	return domain.Value(qv.Value, unitSec, window, result.Coverage)
}

func counterSeries(samples []domain.RawSample, model, configured string, semantic domain.Semantic, filterModel bool) map[string][]derive.TimedValue {
	out := map[string][]derive.TimedValue{}
	restarts := restartMap(samples)
	for _, sample := range samples {
		for _, series := range sample.Series {
			if series.Semantic != semantic {
				continue
			}
			if filterModel && !modelMatches(series.Labels, model, configured) {
				continue
			}
			out[series.ID()] = append(out[series.ID()], derive.TimedValue{
				At:      sample.At,
				Value:   series.Value,
				Restart: restarts[sample.At],
			})
		}
	}
	return out
}

func histogramSeries(samples []domain.RawSample, model, configured string, semantic domain.Semantic) map[string][]derive.TimedHistogram {
	out := map[string][]derive.TimedHistogram{}
	restarts := restartMap(samples)
	for _, sample := range samples {
		for _, hist := range sample.Histograms {
			if hist.Semantic != semantic || !modelMatches(hist.Labels, model, configured) {
				continue
			}
			out[hist.ID()] = append(out[hist.ID()], derive.TimedHistogram{
				At:      sample.At,
				Buckets: append([]domain.Bucket(nil), hist.Buckets...),
				Count:   hist.Count,
				Sum:     hist.Sum,
				Restart: restarts[sample.At],
			})
		}
	}
	return out
}

func engineOutcomes(samples []domain.RawSample, totals []domain.OutcomeCounterTotal, model, configured string) map[time.Duration][]domain.EngineOutcome {
	durations := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
	out := map[time.Duration][]domain.EngineOutcome{}
	restarts := restartMap(samples)
	for _, window := range durations {
		groups := map[string]map[string][]derive.TimedValue{}
		for _, sample := range samples {
			for _, series := range sample.Series {
				if series.Semantic != domain.SemanticCompletionOutcomes || !modelMatches(series.Labels, model, configured) {
					continue
				}
				reason := series.Labels["finished_reason"]
				if reason == "" {
					reason = "reason unavailable"
				}
				if groups[reason] == nil {
					groups[reason] = map[string][]derive.TimedValue{}
				}
				groups[reason][series.ID()] = append(groups[reason][series.ID()], derive.TimedValue{
					At:      sample.At,
					Value:   series.Value,
					Restart: restarts[sample.At],
				})
			}
		}
		var values []domain.EngineOutcome
		for reason, series := range groups {
			result := derive.SumCounterWindows(series, lastSampleAt(samples), window)
			if result.Available {
				values = append(values, domain.EngineOutcome{Reason: reason, Count: domain.Value(result.Delta, unitCount, window, result.Coverage)})
			}
		}
		sort.Slice(values, func(i, j int) bool {
			if values[i].Count.Value == values[j].Count.Value {
				return values[i].Reason < values[j].Reason
			}
			return values[i].Count.Value > values[j].Count.Value
		})
		out[window] = values
	}
	out[domain.OutcomeAllTime] = engineOutcomeTotals(totals, model, configured)
	return out
}

func httpOutcomes(samples []domain.RawSample, totals []domain.OutcomeCounterTotal) map[time.Duration][]domain.HTTPOutcome {
	durations := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
	out := map[time.Duration][]domain.HTTPOutcome{}
	restarts := restartMap(samples)
	for _, window := range durations {
		groups := map[string]map[string][]derive.TimedValue{}
		labels := map[string]domain.LabelSet{}
		for _, sample := range samples {
			for _, series := range sample.Series {
				if series.Semantic != domain.SemanticHTTPOutcomes {
					continue
				}
				if isMetricsHandler(series.Labels["handler"]) {
					continue
				}
				key := series.Labels["status"] + "\xff" + series.Labels["method"] + "\xff" + series.Labels["handler"]
				if groups[key] == nil {
					groups[key] = map[string][]derive.TimedValue{}
					labels[key] = series.Labels.Clone()
				}
				groups[key][series.ID()] = append(groups[key][series.ID()], derive.TimedValue{
					At:      sample.At,
					Value:   series.Value,
					Restart: restarts[sample.At],
				})
			}
		}
		var values []domain.HTTPOutcome
		for key, series := range groups {
			result := derive.SumCounterWindows(series, lastSampleAt(samples), window)
			if !result.Available {
				continue
			}
			ls := labels[key]
			values = append(values, domain.HTTPOutcome{
				Status:  ls["status"],
				Method:  ls["method"],
				Handler: ls["handler"],
				Count:   domain.Value(result.Delta, unitCount, window, result.Coverage),
			})
		}
		sort.Slice(values, func(i, j int) bool {
			if values[i].Count.Value == values[j].Count.Value {
				return values[i].Status+values[i].Method+values[i].Handler < values[j].Status+values[j].Method+values[j].Handler
			}
			return values[i].Count.Value > values[j].Count.Value
		})
		out[window] = values
	}
	out[domain.OutcomeAllTime] = httpOutcomeTotals(totals)
	return out
}

func engineOutcomeTotals(totals []domain.OutcomeCounterTotal, model, configured string) []domain.EngineOutcome {
	type total struct {
		delta    float64
		coverage time.Duration
	}
	groups := map[string]total{}
	for _, counter := range totals {
		series := counter.Series
		if series.Semantic != domain.SemanticCompletionOutcomes || !modelMatches(series.Labels, model, configured) {
			continue
		}
		reason := series.Labels["finished_reason"]
		if reason == "" {
			reason = "reason unavailable"
		}
		group := groups[reason]
		group.delta += counter.Delta
		if counter.Coverage > group.coverage {
			group.coverage = counter.Coverage
		}
		groups[reason] = group
	}
	values := make([]domain.EngineOutcome, 0, len(groups))
	for reason, group := range groups {
		values = append(values, domain.EngineOutcome{
			Reason: reason,
			Count:  domain.Value(group.delta, unitCount, domain.OutcomeAllTime, group.coverage),
		})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Count.Value == values[j].Count.Value {
			return values[i].Reason < values[j].Reason
		}
		return values[i].Count.Value > values[j].Count.Value
	})
	return values
}

func httpOutcomeTotals(totals []domain.OutcomeCounterTotal) []domain.HTTPOutcome {
	type total struct {
		labels   domain.LabelSet
		delta    float64
		coverage time.Duration
	}
	groups := map[string]total{}
	for _, counter := range totals {
		series := counter.Series
		if series.Semantic != domain.SemanticHTTPOutcomes || isMetricsHandler(series.Labels["handler"]) {
			continue
		}
		key := series.Labels["status"] + "\xff" + series.Labels["method"] + "\xff" + series.Labels["handler"]
		group := groups[key]
		if group.labels == nil {
			group.labels = series.Labels.Clone()
		}
		group.delta += counter.Delta
		if counter.Coverage > group.coverage {
			group.coverage = counter.Coverage
		}
		groups[key] = group
	}
	values := make([]domain.HTTPOutcome, 0, len(groups))
	for _, group := range groups {
		values = append(values, domain.HTTPOutcome{
			Status:  group.labels["status"],
			Method:  group.labels["method"],
			Handler: group.labels["handler"],
			Count:   domain.Value(group.delta, unitCount, domain.OutcomeAllTime, group.coverage),
		})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Count.Value == values[j].Count.Value {
			return values[i].Status+values[i].Method+values[i].Handler < values[j].Status+values[j].Method+values[j].Handler
		}
		return values[i].Count.Value > values[j].Count.Value
	})
	return values
}

func overviewFromRows(rows []domain.MetricRow, samples []domain.RawSample, model, configured string, modelCount int, currentWindow time.Duration) domain.OverviewValues {
	var overview domain.OverviewValues
	for _, row := range rows {
		switch row.Key {
		case domain.SemanticCompletedRequestRate:
			overview.RPS = row.Now
		case domain.SemanticRequestsRunning:
			overview.Running = row.Now
		case domain.SemanticRequestsWaiting:
			overview.Waiting = row.Now
		case domain.SemanticPromptTokens:
			overview.PromptTokens = row.Now
		case domain.SemanticGenerationTokens:
			overview.GenerationTokens = row.Now
		case domain.SemanticPrefixHitRatio:
			overview.PrefixHitRate = row.Now
		case domain.SemanticKVCacheUsage:
			overview.KVCache = row.Now
		case domain.SemanticAPICPU:
			overview.APICPU = row.Now
		case domain.SemanticTTFTP95:
			overview.TTFTP95 = row.Now
		case domain.SemanticE2EP95:
			overview.E2EP95 = row.Now
		}
	}
	overview.Errors = errorCount(samples, model, configured, modelCount, currentWindow)
	return overview
}

func errorCount(samples []domain.RawSample, model, configured string, modelCount int, window time.Duration) domain.WindowValue {
	end := lastSampleAt(samples)
	var delta float64
	var coverage time.Duration
	add := func(result derive.CounterResult) {
		if !result.Available {
			return
		}
		delta += result.Delta
		if result.Coverage > coverage {
			coverage = result.Coverage
		}
	}
	add(derive.SumCounterWindows(counterSeries(samples, model, configured, domain.SemanticCorruptedRequests, true), end, window))

	engineSeries := counterSeries(samples, model, configured, domain.SemanticCompletionOutcomes, true)
	notOK := map[string][]derive.TimedValue{}
	for id, points := range engineSeries {
		reason := reasonFromSeriesID(id)
		if reason == "" || reason == "stop" || reason == "length" || reason == "eos" {
			continue
		}
		notOK[id] = points
	}
	add(derive.SumCounterWindows(notOK, end, window))

	if modelCount == 1 {
		httpSeries := counterSeries(samples, model, configured, domain.SemanticHTTPOutcomes, false)
		badHTTP := map[string][]derive.TimedValue{}
		for id, points := range httpSeries {
			if (strings.Contains(id, "status=4") || strings.Contains(id, "status=5")) && !strings.Contains(id, "handler=/metrics") {
				badHTTP[id] = points
			}
		}
		add(derive.SumCounterWindows(badHTTP, end, window))
	}
	if coverage <= 0 {
		return domain.Missing(window, "unavailable")
	}
	return domain.Value(delta, unitCount, window, coverage)
}

type timedWindowValue struct {
	at    time.Time
	value domain.WindowValue
}

func gaugeExtrema(points []derive.TimedValue, end time.Time, horizon time.Duration, unit string) (domain.WindowValue, domain.WindowValue) {
	candidates := make([]timedWindowValue, 0, len(points))
	for _, point := range points {
		candidates = append(candidates, timedWindowValue{
			at: point.At,
			value: domain.WindowValue{
				Value:     point.Value,
				Unit:      unit,
				Available: true,
			},
		})
	}
	return extremaFromCandidates(candidates, end, horizon, unit)
}

func counterRateExtrema(samples []domain.RawSample, series map[string][]derive.TimedValue, end time.Time, currentWindow, horizon time.Duration, unit string) (domain.WindowValue, domain.WindowValue) {
	candidates := make([]timedWindowValue, 0, len(samples))
	for _, sample := range samplesInHorizon(samples, end, horizon) {
		candidates = append(candidates, timedWindowValue{
			at:    sample.At,
			value: rateWindowValue(derive.SumCounterWindows(series, sample.At, currentWindow), currentWindow, unit),
		})
	}
	return extremaFromCandidates(candidates, end, horizon, unit)
}

func ratioExtrema(samples []domain.RawSample, hits, queries map[string][]derive.TimedValue, legacy []derive.TimedValue, end time.Time, currentWindow, horizon time.Duration) (domain.WindowValue, domain.WindowValue) {
	candidates := make([]timedWindowValue, 0, len(samples))
	for _, sample := range samplesInHorizon(samples, end, horizon) {
		candidates = append(candidates, timedWindowValue{
			at:    sample.At,
			value: ratioWindowValue(hits, queries, legacy, sample.At, currentWindow),
		})
	}
	return extremaFromCandidates(candidates, end, horizon, unitPct)
}

func histogramExtrema(samples []domain.RawSample, series map[string][]derive.TimedHistogram, end time.Time, currentWindow, horizon time.Duration, q float64) (domain.WindowValue, domain.WindowValue) {
	candidates := make([]timedWindowValue, 0, len(samples))
	for _, sample := range samplesInHorizon(samples, end, horizon) {
		candidates = append(candidates, timedWindowValue{
			at:    sample.At,
			value: histogramWindowValue(series, sample.At, currentWindow, q),
		})
	}
	return extremaFromCandidates(candidates, end, horizon, unitSec)
}

func extremaFromCandidates(candidates []timedWindowValue, end time.Time, horizon time.Duration, unit string) (domain.WindowValue, domain.WindowValue) {
	if horizon <= 0 {
		horizon = 15 * time.Minute
	}
	if end.IsZero() {
		for _, candidate := range candidates {
			if candidate.at.After(end) {
				end = candidate.at
			}
		}
	}
	start := end.Add(-horizon)
	var minCandidate timedWindowValue
	var maxCandidate timedWindowValue
	var first, last time.Time
	found := false
	for _, candidate := range candidates {
		if candidate.at.Before(start) || candidate.at.After(end) || !candidate.value.Available || !domain.IsFinite(candidate.value.Value) {
			continue
		}
		if !found {
			minCandidate = candidate
			maxCandidate = candidate
			first = candidate.at
			last = candidate.at
			found = true
			continue
		}
		if candidate.value.Value < minCandidate.value.Value {
			minCandidate = candidate
		}
		if candidate.value.Value > maxCandidate.value.Value {
			maxCandidate = candidate
		}
		if candidate.at.Before(first) {
			first = candidate.at
		}
		if candidate.at.After(last) {
			last = candidate.at
		}
	}
	if !found {
		return domain.Missing(horizon, "unavailable"), domain.Missing(horizon, "unavailable")
	}
	coverageStart := first
	if coverageStart.Before(start) {
		coverageStart = start
	}
	coverageEnd := last
	if coverageEnd.After(end) {
		coverageEnd = end
	}
	coverage := coverageEnd.Sub(coverageStart)
	if coverage < 0 {
		coverage = 0
	}
	minValue := extremaWindowValue(minCandidate.value, unit, horizon, coverage)
	maxValue := extremaWindowValue(maxCandidate.value, unit, horizon, coverage)
	return minValue, maxValue
}

func extremaWindowValue(source domain.WindowValue, unit string, horizon, coverage time.Duration) domain.WindowValue {
	if source.Unit != "" {
		unit = source.Unit
	}
	value := domain.Value(source.Value, unit, horizon, coverage)
	value.Note = source.Note
	value.MoreThan = source.MoreThan
	return value
}

func samplesInHorizon(samples []domain.RawSample, end time.Time, horizon time.Duration) []domain.RawSample {
	if end.IsZero() {
		end = lastSampleAt(samples)
	}
	if horizon <= 0 {
		horizon = 15 * time.Minute
	}
	start := end.Add(-horizon)
	out := make([]domain.RawSample, 0, len(samples))
	for _, sample := range samples {
		if sample.At.Before(start) || sample.At.After(end) {
			continue
		}
		out = append(out, sample)
	}
	return out
}

func reasonFromSeriesID(id string) string {
	const prefix = "finished_reason="
	start := strings.Index(id, prefix)
	if start < 0 {
		return ""
	}
	start += len(prefix)
	end := strings.IndexByte(id[start:], '\xff')
	if end < 0 {
		return id[start:]
	}
	return id[start : start+end]
}

func gaugeWindowValue(result derive.GaugeResult, window time.Duration, unit string) domain.WindowValue {
	if !result.Available {
		return domain.Missing(window, "unavailable")
	}
	return domain.Value(result.Value, unit, window, result.Coverage)
}

func rateWindowValue(result derive.CounterResult, window time.Duration, unit string) domain.WindowValue {
	if !result.Available {
		return domain.Missing(window, "unavailable")
	}
	return domain.Value(result.Rate, unit, window, result.Coverage)
}

func minPositiveDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

func lastSampleAt(samples []domain.RawSample) time.Time {
	if len(samples) == 0 {
		return time.Time{}
	}
	return samples[len(samples)-1].At
}

func restartMap(samples []domain.RawSample) map[time.Time]bool {
	out := map[time.Time]bool{}
	var previous *float64
	for _, sample := range samples {
		if sample.ProcessStart != nil {
			if previous != nil && *previous != *sample.ProcessStart {
				out[sample.At] = true
			}
			v := *sample.ProcessStart
			previous = &v
		}
	}
	return out
}

func sampleRestart(samples []domain.RawSample, at time.Time) bool {
	return restartMap(samples)[at]
}

func restarted(samples []domain.RawSample) bool {
	for _, restart := range restartMap(samples) {
		if restart {
			return true
		}
	}
	return false
}

func usesLegacyPrefixHitRate(samples []domain.RawSample, model, configured string) bool {
	for _, sample := range samples {
		for _, series := range sample.Series {
			if series.Semantic == domain.SemanticLegacyPrefixHitRate && modelMatches(series.Labels, model, configured) {
				return true
			}
		}
	}
	return false
}

func maxGaugeGap(config domain.RuntimeConfig) time.Duration {
	interval := config.RefreshInterval
	if interval <= 0 {
		interval = domain.DefaultInterval
	}
	return interval * 5 / 2
}

func isMetricsHandler(handler string) bool {
	return handler == "/metrics" || strings.HasSuffix(handler, " /metrics")
}
