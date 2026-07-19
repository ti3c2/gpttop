package prom

import (
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"

	"gpttop/internal/domain"
)

// Parse reads Prometheus text or OpenMetrics exposition and returns a normalized raw sample.
func Parse(r io.Reader, endpointName, endpointURL, metricsURL string, at time.Time) (*domain.RawSample, error) {
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(r)
	if err != nil {
		return nil, fmt.Errorf("parse prometheus metrics: %w", err)
	}

	sample := &domain.RawSample{
		EndpointName: endpointName,
		EndpointURL:  endpointURL,
		MetricsURL:   metricsURL,
		At:           at,
	}

	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		family := families[name]
		entry, ok := Lookup(family.GetName())
		if !ok {
			continue
		}
		sample.Recognized = true
		switch family.GetType() {
		case dto.MetricType_HISTOGRAM:
			appendHistograms(sample, family, entry)
		case dto.MetricType_COUNTER, dto.MetricType_GAUGE, dto.MetricType_UNTYPED:
			appendScalars(sample, family, entry)
		default:
			appendScalars(sample, family, entry)
		}
	}

	sort.Slice(sample.Series, func(i, j int) bool {
		return sample.Series[i].ID() < sample.Series[j].ID()
	})
	sort.Slice(sample.Histograms, func(i, j int) bool {
		return sample.Histograms[i].ID() < sample.Histograms[j].ID()
	})
	return sample, nil
}

func appendScalars(sample *domain.RawSample, family *dto.MetricFamily, entry Entry) {
	for _, metric := range family.Metric {
		value, ok := scalarValue(metric, family.GetType())
		if !ok || !domain.IsFinite(value) {
			continue
		}
		labels := labels(metric)
		point := domain.SeriesPoint{
			Name:     entry.Canonical,
			Semantic: entry.Semantic,
			Kind:     entry.Kind,
			Labels:   labels,
			Value:    value,
		}
		sample.Series = append(sample.Series, point)
		if entry.Semantic == domain.SemanticProcessStart {
			v := value
			sample.ProcessStart = &v
		}
	}
}

func scalarValue(metric *dto.Metric, typ dto.MetricType) (float64, bool) {
	switch typ {
	case dto.MetricType_COUNTER:
		if metric.Counter == nil {
			return 0, false
		}
		return metric.Counter.GetValue(), true
	case dto.MetricType_GAUGE:
		if metric.Gauge == nil {
			return 0, false
		}
		return metric.Gauge.GetValue(), true
	case dto.MetricType_UNTYPED:
		if metric.Untyped == nil {
			return 0, false
		}
		return metric.Untyped.GetValue(), true
	default:
		if metric.Gauge != nil {
			return metric.Gauge.GetValue(), true
		}
		if metric.Counter != nil {
			return metric.Counter.GetValue(), true
		}
		if metric.Untyped != nil {
			return metric.Untyped.GetValue(), true
		}
		return 0, false
	}
}

func appendHistograms(sample *domain.RawSample, family *dto.MetricFamily, entry Entry) {
	for _, metric := range family.Metric {
		if metric.Histogram == nil {
			continue
		}
		hist := metric.Histogram
		buckets := make([]domain.Bucket, 0, len(hist.Bucket))
		for _, bucket := range hist.Bucket {
			upper := bucket.GetUpperBound()
			if math.IsNaN(upper) {
				continue
			}
			buckets = append(buckets, domain.Bucket{
				Upper: upper,
				Count: bucket.GetCumulativeCount(),
			})
		}
		sort.Slice(buckets, func(i, j int) bool {
			return buckets[i].Upper < buckets[j].Upper
		})
		sample.Histograms = append(sample.Histograms, domain.HistogramPoint{
			Name:     entry.Canonical,
			Semantic: entry.Semantic,
			Labels:   labels(metric),
			Buckets:  buckets,
			Count:    hist.GetSampleCount(),
			Sum:      hist.GetSampleSum(),
		})
	}
}

func labels(metric *dto.Metric) domain.LabelSet {
	out := make(domain.LabelSet, len(metric.Label))
	for _, label := range metric.Label {
		name := label.GetName()
		if name == "" {
			continue
		}
		out[name] = label.GetValue()
	}
	return out
}
