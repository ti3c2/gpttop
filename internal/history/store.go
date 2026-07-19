package history

import (
	"sort"
	"sync"
	"time"

	"gpttop/internal/domain"
)

// Store keeps bounded in-memory scrape samples and scrape health status.
type Store struct {
	mu        sync.RWMutex
	retention time.Duration
	samples   map[string][]domain.RawSample
	statuses  map[string]domain.ScrapeStatus
}

// NewStore creates a store with the configured retention duration.
func NewStore(retention time.Duration) *Store {
	if retention <= 0 {
		retention = domain.DefaultHistory
	}
	return &Store{
		retention: retention,
		samples:   make(map[string][]domain.RawSample),
		statuses:  make(map[string]domain.ScrapeStatus),
	}
}

// Retention returns the configured in-memory retention window.
func (s *Store) Retention() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retention
}

// AddSample appends a successful raw sample and evicts old samples for that endpoint.
func (s *Store) AddSample(sample *domain.RawSample) {
	if sample == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.samples == nil {
		s.samples = make(map[string][]domain.RawSample)
	}
	key := sample.EndpointName
	cp := cloneSample(*sample)
	s.samples[key] = append(s.samples[key], cp)
	sort.SliceStable(s.samples[key], func(i, j int) bool {
		return s.samples[key][i].At.Before(s.samples[key][j].At)
	})
	s.evictLocked(key, cp.At)
}

// AddStatus records the latest scrape status for an endpoint.
func (s *Store) AddStatus(status domain.ScrapeStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statuses == nil {
		s.statuses = make(map[string]domain.ScrapeStatus)
	}
	s.statuses[status.EndpointName] = status
}

// Samples returns retained samples for an endpoint ordered by scrape time.
func (s *Store) Samples(endpointName string) []domain.RawSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSamples(s.samples[endpointName])
}

// AllSamples returns retained samples grouped by endpoint.
func (s *Store) AllSamples() map[string][]domain.RawSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]domain.RawSample, len(s.samples))
	for key, samples := range s.samples {
		out[key] = cloneSamples(samples)
	}
	return out
}

// Status returns the latest scrape status for an endpoint.
func (s *Store) Status(endpointName string) (domain.ScrapeStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status, ok := s.statuses[endpointName]
	return status, ok
}

// Statuses returns the latest scrape statuses grouped by endpoint.
func (s *Store) Statuses() map[string]domain.ScrapeStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]domain.ScrapeStatus, len(s.statuses))
	for key, status := range s.statuses {
		out[key] = status
	}
	return out
}

func (s *Store) evictLocked(endpointName string, now time.Time) {
	if s.retention <= 0 {
		return
	}
	cutoff := now.Add(-s.retention)
	samples := s.samples[endpointName]
	keep := 0
	for keep < len(samples) && samples[keep].At.Before(cutoff) {
		keep++
	}
	if keep > 0 {
		s.samples[endpointName] = append([]domain.RawSample(nil), samples[keep:]...)
	}
}

func cloneSamples(samples []domain.RawSample) []domain.RawSample {
	out := make([]domain.RawSample, len(samples))
	for i := range samples {
		out[i] = cloneSample(samples[i])
	}
	return out
}

func cloneSample(sample domain.RawSample) domain.RawSample {
	sample.Series = append([]domain.SeriesPoint(nil), sample.Series...)
	for i := range sample.Series {
		sample.Series[i].Labels = sample.Series[i].Labels.Clone()
	}
	sample.Histograms = append([]domain.HistogramPoint(nil), sample.Histograms...)
	for i := range sample.Histograms {
		sample.Histograms[i].Labels = sample.Histograms[i].Labels.Clone()
		sample.Histograms[i].Buckets = append([]domain.Bucket(nil), sample.Histograms[i].Buckets...)
	}
	if sample.ProcessStart != nil {
		v := *sample.ProcessStart
		sample.ProcessStart = &v
	}
	return sample
}
