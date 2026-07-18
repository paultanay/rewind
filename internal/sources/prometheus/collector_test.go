package prometheus_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
	prom "github.com/paultanay/rewind/internal/sources/prometheus"
)

// ─── Fixture helpers ──────────────────────────────────────────────────────────

// makeRangeResponse builds a minimal Prometheus query_range JSON response
// for the given (timestamp, value) pairs.
func makeRangeResponse(pairs [][2]float64) []byte {
	type sample [2]any
	type result struct {
		Metric map[string]string `json:"metric"`
		Values []sample          `json:"values"`
	}
	values := make([]sample, len(pairs))
	for i, p := range pairs {
		// Prometheus returns value as a string.
		values[i] = sample{p[0], fmt.Sprintf("%.6f", p[1])}
	}
	type data struct {
		ResultType string   `json:"resultType"`
		Result     []result `json:"result"`
	}
	type resp struct {
		Status string `json:"status"`
		Data   data   `json:"data"`
	}
	b, _ := json.Marshal(resp{
		Status: "success",
		Data: data{
			ResultType: "matrix",
			Result: []result{
				{Metric: map[string]string{}, Values: values},
			},
		},
	})
	return b
}

// serveFixture starts an httptest.Server that serves a fixed query_range
// response for all requests (simulating a real Prometheus).
// It returns the server and a cleanup function.
func serveFixture(t *testing.T, pairs [][2]float64) *httptest.Server {
	t.Helper()
	body := makeRangeResponse(pairs)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestCollector_BasicCollection(t *testing.T) {
	t.Parallel()

	// Build 20 sample pairs: timestamps at 60s intervals, values = 40ms latency.
	base := float64(time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC).Unix())
	pairs := make([][2]float64, 20)
	for i := range pairs {
		pairs[i] = [2]float64{base + float64(i*60), 0.040} // 40ms
	}

	srv := serveFixture(t, pairs)

	c := &prom.Collector{
		URL:     srv.URL,
		Version: "test",
	}

	window := model.TimeRange{
		From: time.Unix(int64(base), 0).UTC(),
		To:   time.Unix(int64(base)+1200, 0).UTC(),
	}
	scope := model.Scope{
		Namespaces: []string{"shop"},
		Services:   []string{"checkout"},
	}

	result, err := c.Collect(context.Background(), scope, window)
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	if len(result.Signals) == 0 {
		t.Error("expected at least one signal, got none")
	}
	if len(result.Entities) == 0 {
		t.Error("expected at least one entity, got none")
	}

	// Verify entity ID format.
	if result.Entities[0].ID != "svc/shop/checkout" {
		t.Errorf("entity ID = %q, want svc/shop/checkout", result.Entities[0].ID)
	}

	// Every signal should reference the entity.
	for _, sig := range result.Signals {
		if sig.EntityID != "svc/shop/checkout" {
			t.Errorf("signal entity ID = %q, want svc/shop/checkout", sig.EntityID)
		}
		if sig.Metric == "" {
			t.Error("signal metric name is empty")
		}
		if len(sig.Points) == 0 {
			t.Errorf("signal %s has no points", sig.Metric)
		}
	}

	// RawFixture should be populated.
	if len(result.RawFixture) == 0 {
		t.Error("RawFixture is empty")
	}
}

func TestCollector_EmptyResponse(t *testing.T) {
	t.Parallel()
	// Prometheus returns no data — Collect should return no signals, no error.
	body := []byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer srv.Close()

	c := &prom.Collector{URL: srv.URL, Version: "test"}
	window := model.TimeRange{
		From: time.Now().Add(-time.Hour),
		To:   time.Now(),
	}
	result, err := c.Collect(context.Background(), model.Scope{
		Namespaces: []string{"shop"},
		Services:   []string{"checkout"},
	}, window)

	// No signals but also no hard error (empty is valid).
	if err != nil {
		t.Logf("Collect err (acceptable if all queries empty): %v", err)
	}
	if len(result.Signals) != 0 {
		t.Errorf("expected 0 signals for empty response, got %d", len(result.Signals))
	}
}

func TestCollector_ServerError(t *testing.T) {
	t.Parallel()
	// Server returns 500 — Collect should return error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &prom.Collector{URL: srv.URL, Version: "test"}
	window := model.TimeRange{
		From: time.Now().Add(-time.Hour),
		To:   time.Now(),
	}
	result, err := c.Collect(context.Background(), model.Scope{
		Namespaces: []string{"shop"},
		Services:   []string{"checkout"},
	}, window)

	// Should return an error because all queries failed.
	if err == nil {
		t.Error("expected error for server 500, got nil")
	}
	if len(result.Signals) != 0 {
		t.Errorf("expected 0 signals on server error, got %d", len(result.Signals))
	}
}

func TestCollector_ContextCancelled(t *testing.T) {
	t.Parallel()
	// Slow server — context cancelled before response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &prom.Collector{URL: srv.URL, Version: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	window := model.TimeRange{From: time.Now().Add(-time.Hour), To: time.Now()}
	_, err := c.Collect(ctx, model.Scope{
		Namespaces: []string{"shop"},
		Services:   []string{"checkout"},
	}, window)
	// Should return an error due to context cancellation.
	if err == nil {
		t.Error("expected error on context cancellation, got nil")
	}
}

func TestCollector_Name(t *testing.T) {
	t.Parallel()
	c := &prom.Collector{URL: "http://localhost:9090", Version: "test"}
	if c.Name() != "prometheus" {
		t.Errorf("Name() = %q, want prometheus", c.Name())
	}
}

// TestDownsample_MaxPoints verifies that a large series is capped at 500 pts.
func TestDownsample_MaxPoints(t *testing.T) {
	t.Parallel()
	// 1000 data points at 1-second intervals.
	baseTS := float64(time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC).Unix())
	pairs := make([][2]float64, 1000)
	for i := range pairs {
		pairs[i] = [2]float64{baseTS + float64(i), float64(i % 100)}
	}

	srv := serveFixture(t, pairs)
	c := &prom.Collector{URL: srv.URL, Version: "test"}

	window := model.TimeRange{
		From: time.Unix(int64(baseTS), 0).UTC(),
		To:   time.Unix(int64(baseTS)+999, 0).UTC(),
	}
	result, _ := c.Collect(context.Background(), model.Scope{
		Namespaces: []string{"ns"},
		Services:   []string{"svc"},
	}, window)

	for _, sig := range result.Signals {
		if len(sig.Points) > 500 {
			t.Errorf("signal %s has %d points, expected ≤500", sig.Metric, len(sig.Points))
		}
	}
}
