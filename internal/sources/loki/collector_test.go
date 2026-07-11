package loki_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/sources/loki"
)

var base = time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)

// buildMatrixResponse returns a Loki matrix query_range response with a single
// series that starts at 0.1 errors/s, then spikes to 5.0 at t=10m.
func buildMatrixResponse(window model.TimeRange, step time.Duration) string {
	type val struct {
		ts int64
		v  float64
	}
	var vals []val
	for t := window.From; !t.After(window.To); t = t.Add(step) {
		v := 0.1
		if !t.Before(window.From.Add(10 * time.Minute)) {
			v = 5.0 // burst starts at 10m
		}
		vals = append(vals, val{t.UnixNano(), v})
	}

	var rows string
	for _, v := range vals {
		rows += fmt.Sprintf(`["%d", "%.1f"],`, v.ts, v.v)
	}
	if len(rows) > 0 {
		rows = rows[:len(rows)-1] // trim trailing comma
	}
	return fmt.Sprintf(`{"status":"success","data":{"resultType":"matrix","result":[{"stream":{"namespace":"shop","app":"checkout"},"values":[%s]}]}}`, rows)
}

// buildStreamResponse returns a Loki stream query_range response with 3 sample lines.
func buildStreamResponse() string {
	return `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"namespace":"shop","app":"checkout"},"values":[["1000000000","ERR connection refused"],["2000000000","FATAL out of memory"],["3000000000","ERROR timeout"]]}]}}`
}

func TestCollector_Collect_BurstDetection(t *testing.T) {
	window := model.TimeRange{
		From: base,
		To:   base.Add(30 * time.Minute),
	}
	step := time.Minute

	// httptest server that serves different responses based on the query parameter.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if q == "" {
			// labels endpoint (Check)
			w.WriteHeader(200)
			fmt.Fprint(w, `{"status":"success","data":[]}`)
			return
		}
		// Sample lines query (contains stream selector only, no rate())
		if !containsRate(q) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, buildStreamResponse())
			return
		}
		// Rate metric query
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, buildMatrixResponse(window, step))
	}))
	defer srv.Close()

	cfg := loki.Config{URL: srv.URL}
	c := loki.New(cfg, "test")

	scope := model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}}
	result, err := c.Collect(t.Context(), scope, window)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Entities) != 0 {
		t.Errorf("expected no entities from loki, got %d", len(result.Entities))
	}
	if len(result.Signals) == 0 {
		t.Error("expected at least one signal (log.error.rate)")
	}
	if len(result.Signals) > 0 && result.Signals[0].Metric != model.MetricLogErrorRate {
		t.Errorf("signal metric should be %q, got %q", model.MetricLogErrorRate, result.Signals[0].Metric)
	}
	if len(result.Events) == 0 {
		t.Error("expected at least one LogBurst event")
	}
	for _, ev := range result.Events {
		if ev.Kind != model.EventKindLogBurst {
			t.Errorf("expected LogBurst kind, got %q", ev.Kind)
		}
		if ev.Severity != model.SeverityNotable {
			t.Errorf("LogBurst should be Notable severity, got %q", ev.Severity)
		}
	}
}

func TestCollector_Check_NoURL(t *testing.T) {
	c := loki.New(loki.Config{}, "test")
	if err := c.Check(t.Context()); err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestCollector_Collect_NoURL(t *testing.T) {
	c := loki.New(loki.Config{}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	_, err := c.Collect(t.Context(), scope, model.TimeRange{From: base, To: base.Add(time.Hour)})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestCollector_NoScope(t *testing.T) {
	c := loki.New(loki.Config{URL: "http://localhost:3100"}, "test")
	_, err := c.Collect(t.Context(), model.Scope{}, model.TimeRange{From: base, To: base.Add(time.Hour)})
	if err == nil {
		t.Error("expected error when no namespaces in scope")
	}
}

func containsRate(q string) bool {
	return len(q) > 3 && (q[0] == 's' || q[0] == 'r') // rate() or sum(rate(...))
}
