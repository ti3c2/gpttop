package scrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"gpttop/internal/config"
	"gpttop/internal/domain"
	"gpttop/internal/prom"
)

type Parser interface {
	Parse(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error)
}

type ParserFunc func(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error)

func (f ParserFunc) Parse(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error) {
	return f(endpoint, body, at)
}

type PromParser struct{}

func (PromParser) Parse(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error) {
	return prom.Parse(bytes.NewReader(body), endpoint.Name, endpoint.URL, endpoint.MetricsURL, at)
}

type Option func(*Scraper)

type Scraper struct {
	client    *http.Client
	parser    Parser
	timeout   time.Duration
	bodyLimit int64

	mu       sync.Mutex
	statuses map[string]domain.ScrapeStatus
	inFlight map[string]context.CancelFunc
	closed   bool
}

func New(parser Parser, opts ...Option) *Scraper {
	s := &Scraper{
		client:    &http.Client{},
		parser:    parser,
		timeout:   domain.DefaultTimeout,
		bodyLimit: domain.DefaultBodyLimit,
		statuses:  make(map[string]domain.ScrapeStatus),
		inFlight:  make(map[string]context.CancelFunc),
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.parser == nil {
		s.parser = PromParser{}
	}
	return s
}

func WithClient(client *http.Client) Option {
	return func(s *Scraper) {
		if client != nil {
			s.client = client
		}
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(s *Scraper) {
		if timeout > 0 {
			s.timeout = timeout
		}
	}
}

func WithBodyLimit(limit int64) Option {
	return func(s *Scraper) {
		if limit > 0 {
			s.bodyLimit = limit
		}
	}
}

func (s *Scraper) ScrapeAll(ctx context.Context, endpoints []domain.EndpointConfig, force bool) []domain.EndpointUpdate {
	updates := make([]domain.EndpointUpdate, len(endpoints))
	var wg sync.WaitGroup
	for i := range endpoints {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			updates[i] = s.ScrapeEndpoint(ctx, endpoints[i], force)
		}()
	}
	wg.Wait()
	return updates
}

func (s *Scraper) ScrapeEndpoint(ctx context.Context, endpoint domain.EndpointConfig, force bool) domain.EndpointUpdate {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	endpoint = ensureSanitized(endpoint)
	key := endpointKey(endpoint)
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	if err := s.begin(key, cancel); err != nil {
		cancel()
		status := s.failureStatus(endpoint, start, 0, err.Error())
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	defer s.end(key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.MetricsURL, nil)
	if err != nil {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), "invalid metrics url")
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	for key, value := range endpoint.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "text/plain; version=0.0.4, application/openmetrics-text, */*")

	resp, err := s.client.Do(req)
	if err != nil {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), sanitizeError(err.Error()))
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		cancel()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		status := s.failureStatus(endpoint, start, time.Since(start), fmt.Sprintf("unexpected HTTP status %d", resp.StatusCode))
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	if !isSupportedContentType(resp.Header.Get("Content-Type")) {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), fmt.Sprintf("unsupported content type %q", sanitizeError(resp.Header.Get("Content-Type"))))
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}

	body, err := readLimited(resp.Body, s.bodyLimit)
	if err != nil {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), sanitizeError(err.Error()))
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	at := time.Now()
	if s.parser == nil {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), "metrics parser is not configured")
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	sample, err := s.parser.Parse(endpoint, body, at)
	if err != nil {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), sanitizeError(err.Error()))
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	if sample == nil {
		cancel()
		status := s.failureStatus(endpoint, start, time.Since(start), "metrics parser returned no sample")
		return domain.EndpointUpdate{Target: endpoint, Status: status}
	}
	cancel()
	sample.EndpointName = endpoint.Name
	sample.EndpointURL = endpoint.URL
	sample.MetricsURL = endpoint.MetricsURL
	sample.At = at
	status := s.successStatus(endpoint, at, time.Since(start), sample.Recognized)
	return domain.EndpointUpdate{Target: endpoint, Sample: sample, Status: status}
}

func (s *Scraper) Shutdown() {
	s.mu.Lock()
	s.closed = true
	cancels := make([]context.CancelFunc, 0, len(s.inFlight))
	for _, cancel := range s.inFlight {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Scraper) begin(key string, cancel context.CancelFunc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrShutdown
	}
	if _, ok := s.inFlight[key]; ok {
		return errors.New("scrape already in progress")
	}
	s.inFlight[key] = cancel
	return nil
}

func (s *Scraper) end(key string) {
	s.mu.Lock()
	delete(s.inFlight, key)
	s.mu.Unlock()
}

func (s *Scraper) successStatus(endpoint domain.EndpointConfig, at time.Time, duration time.Duration, recognized bool) domain.ScrapeStatus {
	status := domain.ScrapeStatus{
		EndpointName: endpoint.Name,
		MetricsURL:   endpoint.MetricsURL,
		SanitizedURL: endpoint.SanitizedURL,
		At:           at,
		Success:      true,
		Duration:     duration,
		LastSuccess:  at,
		Recognized:   recognized,
	}
	s.mu.Lock()
	s.statuses[endpointKey(endpoint)] = status
	s.mu.Unlock()
	return status
}

func (s *Scraper) failureStatus(endpoint domain.EndpointConfig, at time.Time, duration time.Duration, message string) domain.ScrapeStatus {
	key := endpointKey(endpoint)
	s.mu.Lock()
	prev := s.statuses[key]
	failures := prev.ConsecutiveFailure + 1
	lastSuccess := prev.LastSuccess
	s.mu.Unlock()
	status := domain.ScrapeStatus{
		EndpointName:       endpoint.Name,
		MetricsURL:         endpoint.MetricsURL,
		SanitizedURL:       endpoint.SanitizedURL,
		At:                 at,
		Success:            false,
		Duration:           duration,
		LastSuccess:        lastSuccess,
		ConsecutiveFailure: failures,
		Error:              concise(message),
		Recognized:         prev.Recognized,
	}
	s.mu.Lock()
	s.statuses[key] = status
	s.mu.Unlock()
	return status
}

func ensureSanitized(endpoint domain.EndpointConfig) domain.EndpointConfig {
	if endpoint.MetricsURL == "" {
		normalized, err := config.NormalizeEndpoint(endpoint)
		if err == nil {
			return normalized
		}
		endpoint.MetricsURL = endpoint.URL
	}
	if endpoint.SanitizedURL == "" {
		endpoint.SanitizedURL = config.SanitizeURL(endpoint.MetricsURL)
	}
	return endpoint
}

func endpointKey(endpoint domain.EndpointConfig) string {
	if endpoint.Name != "" {
		return endpoint.Name
	}
	if endpoint.MetricsURL != "" {
		return endpoint.MetricsURL
	}
	return endpoint.URL
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("metrics body exceeds %d bytes", limit)
	}
	return body, nil
}

func isSupportedContentType(contentType string) bool {
	if contentType == "" {
		return true
	}
	media := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch media {
	case "text/plain", "application/openmetrics-text":
		return true
	default:
		return false
	}
}

func sanitizeError(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "request failed"
	}
	fields := strings.Fields(message)
	for i, field := range fields {
		trimmed := trimURLToken(field)
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			fields[i] = strings.Replace(field, trimmed, config.SanitizeURL(trimmed), 1)
		}
	}
	return concise(strings.Join(fields, " "))
}

func trimURLToken(value string) string {
	trimmed := value
	for {
		before := trimmed
		trimmed = strings.Trim(trimmed, "\"'()[]{}<>,")
		trimmed = strings.TrimSuffix(trimmed, ":")
		if trimmed == before {
			return trimmed
		}
	}
}

func concise(message string) string {
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.ReplaceAll(message, "\r", " ")
	fields := strings.Fields(message)
	message = strings.Join(fields, " ")
	if len(message) > 240 {
		return message[:237] + "..."
	}
	return message
}

var ErrShutdown = errors.New("scraper is shut down")
