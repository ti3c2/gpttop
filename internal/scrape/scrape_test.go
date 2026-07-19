package scrape

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpttop/internal/config"
	"gpttop/internal/domain"
)

func TestScrapeSuccessAndAuthHeader(t *testing.T) {
	var sawAuth int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got == "Bearer secret-token" {
			atomic.StoreInt32(&sawAuth, 1)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintln(w, "ok")
	}))
	defer server.Close()

	endpoint := normalizedEndpoint(t, "good", server.URL, map[string]string{"Authorization": "Bearer secret-token"})
	parser := ParserFunc(func(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error) {
		if string(body) != "ok\n" {
			t.Fatalf("body = %q", body)
		}
		return &domain.RawSample{Recognized: true}, nil
	})
	update := New(parser).ScrapeEndpoint(context.Background(), endpoint, false)
	if !update.Status.Success || update.Sample == nil {
		t.Fatalf("update = %#v", update)
	}
	if atomic.LoadInt32(&sawAuth) != 1 {
		t.Fatal("server did not receive Authorization header")
	}
	if strings.Contains(update.Status.Error, "secret-token") || strings.Contains(update.Status.SanitizedURL, "secret-token") {
		t.Fatalf("secret leaked in status: %#v", update.Status)
	}
}

func TestScrapeTimeoutCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			fmt.Fprintln(w, "late")
		}
	}))
	defer server.Close()

	update := New(okParser(), WithTimeout(20*time.Millisecond)).ScrapeEndpoint(context.Background(), normalizedEndpoint(t, "slow", server.URL, nil), false)
	if update.Status.Success || update.Sample != nil {
		t.Fatalf("timeout should fail without sample: %#v", update)
	}
	if !strings.Contains(update.Status.Error, "context deadline") {
		t.Fatalf("error = %q", update.Status.Error)
	}
}

func TestScrapeNon2xxNoSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	update := New(okParser()).ScrapeEndpoint(context.Background(), normalizedEndpoint(t, "bad", server.URL, nil), false)
	if update.Status.Success || update.Sample != nil {
		t.Fatalf("non-2xx should fail without sample: %#v", update)
	}
	if !strings.Contains(update.Status.Error, "503") {
		t.Fatalf("error = %q", update.Status.Error)
	}
}

func TestScrapeOversizedNoSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0")
		fmt.Fprint(w, strings.Repeat("x", 12))
	}))
	defer server.Close()

	update := New(okParser(), WithBodyLimit(8)).ScrapeEndpoint(context.Background(), normalizedEndpoint(t, "large", server.URL, nil), false)
	if update.Status.Success || update.Sample != nil {
		t.Fatalf("oversized should fail without sample: %#v", update)
	}
	if !strings.Contains(update.Status.Error, "exceeds") {
		t.Fatalf("error = %q", update.Status.Error)
	}
}

func TestScrapeMalformedNoSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "bad")
	}))
	defer server.Close()

	parser := ParserFunc(func(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error) {
		return nil, errors.New("parse failed near token")
	})
	update := New(parser).ScrapeEndpoint(context.Background(), normalizedEndpoint(t, "malformed", server.URL, nil), false)
	if update.Status.Success || update.Sample != nil {
		t.Fatalf("malformed should fail without sample: %#v", update)
	}
	if update.Status.Error != "parse failed near token" {
		t.Fatalf("error = %q", update.Status.Error)
	}
}

func TestScrapeAllConcurrentHealthyAndUnhealthy(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer healthy.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer unhealthy.Close()

	updates := New(okParser()).ScrapeAll(context.Background(), []domain.EndpointConfig{
		normalizedEndpoint(t, "healthy", healthy.URL, nil),
		normalizedEndpoint(t, "unhealthy", unhealthy.URL, nil),
	}, false)
	if len(updates) != 2 {
		t.Fatalf("updates = %d", len(updates))
	}
	if !updates[0].Status.Success || updates[0].Sample == nil {
		t.Fatalf("healthy update = %#v", updates[0])
	}
	if updates[1].Status.Success || updates[1].Sample != nil {
		t.Fatalf("unhealthy update = %#v", updates[1])
	}
}

func TestForcedRefreshDoesNotOverlapEndpoint(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var current int32
	var maxConcurrent int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		closeOnce(started)
		now := atomic.AddInt32(&current, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if now <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, now) {
				break
			}
		}
		<-release
		atomic.AddInt32(&current, -1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	s := New(okParser(), WithTimeout(time.Second))
	endpoint := normalizedEndpoint(t, "one", server.URL, nil)
	done := make(chan domain.EndpointUpdate, 1)
	go func() {
		done <- s.ScrapeEndpoint(context.Background(), endpoint, true)
	}()
	<-started
	second := s.ScrapeEndpoint(context.Background(), endpoint, true)
	close(release)
	first := <-done
	if !first.Status.Success {
		t.Fatalf("first update = %#v", first)
	}
	if second.Status.Success || second.Sample != nil || !strings.Contains(second.Status.Error, "already in progress") {
		t.Fatalf("second update = %#v", second)
	}
	if atomic.LoadInt32(&maxConcurrent) != 1 {
		t.Fatalf("max concurrent = %d, want 1", maxConcurrent)
	}
}

func TestSameMetricsURLDifferentEndpointNamesCanScrapeConcurrently(t *testing.T) {
	release := make(chan struct{})
	var current int32
	var maxConcurrent int32
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		now := atomic.AddInt32(&current, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if now <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, now) {
				break
			}
		}
		if atomic.LoadInt32(&requests) == 2 {
			closeOnce(release)
		}
		select {
		case <-release:
		case <-time.After(time.Second):
		}
		atomic.AddInt32(&current, -1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	s := New(okParser(), WithTimeout(time.Second))
	updates := s.ScrapeAll(context.Background(), []domain.EndpointConfig{
		normalizedEndpoint(t, "one", server.URL, nil),
		normalizedEndpoint(t, "two", server.URL, nil),
	}, false)
	for _, update := range updates {
		if !update.Status.Success {
			t.Fatalf("update = %#v", update)
		}
	}
	if atomic.LoadInt32(&maxConcurrent) != 2 {
		t.Fatalf("max concurrent = %d, want 2", maxConcurrent)
	}
}

func TestShutdownCancelsInFlightAndRejectsNew(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	s := New(okParser(), WithTimeout(time.Second))
	endpoint := normalizedEndpoint(t, "shutdown", server.URL, nil)
	done := make(chan domain.EndpointUpdate, 1)
	go func() {
		done <- s.ScrapeEndpoint(context.Background(), endpoint, false)
	}()
	time.Sleep(20 * time.Millisecond)
	s.Shutdown()
	update := <-done
	if update.Status.Success || update.Sample != nil {
		t.Fatalf("in-flight after shutdown = %#v", update)
	}
	next := s.ScrapeEndpoint(context.Background(), endpoint, false)
	if next.Status.Success || !strings.Contains(next.Status.Error, ErrShutdown.Error()) {
		t.Fatalf("new after shutdown = %#v", next)
	}
}

func TestUnsupportedContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "{}")
	}))
	defer server.Close()

	update := New(okParser()).ScrapeEndpoint(context.Background(), normalizedEndpoint(t, "json", server.URL, nil), false)
	if update.Status.Success || !strings.Contains(update.Status.Error, "unsupported content type") {
		t.Fatalf("update = %#v", update)
	}
}

func okParser() Parser {
	return ParserFunc(func(endpoint domain.EndpointConfig, body []byte, at time.Time) (*domain.RawSample, error) {
		return &domain.RawSample{Recognized: true}, nil
	})
}

func normalizedEndpoint(t *testing.T, name, rawURL string, headers map[string]string) domain.EndpointConfig {
	t.Helper()
	ep, err := config.NormalizeEndpoint(domain.EndpointConfig{Name: name, URL: rawURL, Headers: headers})
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

var closeMu sync.Mutex
var closedChans = map[chan struct{}]struct{}{}

func closeOnce(ch chan struct{}) {
	closeMu.Lock()
	defer closeMu.Unlock()
	if _, ok := closedChans[ch]; ok {
		return
	}
	close(ch)
	closedChans[ch] = struct{}{}
}
