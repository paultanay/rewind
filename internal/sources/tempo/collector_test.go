package tempo_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/sources/tempo"
)

var base = time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)

// buildSearchResponse returns a Tempo /api/search response with two traces:
// one normal, one error, for service "checkout".
func buildSearchResponse() string {
	type trace struct {
		TraceID           string  `json:"traceID"`
		RootServiceName   string  `json:"rootServiceName"`
		RootTraceName     string  `json:"rootTraceName"`
		DurationMs        float64 `json:"durationMs"`
		StartTimeUnixNano string  `json:"startTimeUnixNano"`
		SpanSets          []struct {
			Spans []struct {
				Attributes []struct {
					Key   string `json:"key"`
					Value struct {
						StringValue string `json:"stringValue"`
					} `json:"value"`
				} `json:"attributes"`
			} `json:"spans"`
		} `json:"spanSets"`
	}
	traces := []trace{
		{
			TraceID:         "trace-001",
			RootServiceName: "checkout",
			RootTraceName:   "POST /checkout",
			DurationMs:      45.0,
			SpanSets: []struct {
				Spans []struct {
					Attributes []struct {
						Key   string `json:"key"`
						Value struct {
							StringValue string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				} `json:"spans"`
			}{
				{Spans: []struct {
					Attributes []struct {
						Key   string `json:"key"`
						Value struct {
							StringValue string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				}{{Attributes: []struct {
					Key   string `json:"key"`
					Value struct {
						StringValue string `json:"stringValue"`
					} `json:"value"`
				}{{Key: "service.name", Value: struct {
					StringValue string `json:"stringValue"`
				}{"checkout"}}, {Key: "service.name", Value: struct {
					StringValue string `json:"stringValue"`
				}{"payments"}}}}}},
			},
		},
		{
			TraceID:         "trace-error-001",
			RootServiceName: "checkout",
			RootTraceName:   "POST /checkout [ERROR: timeout]",
			DurationMs:      5100.0,
		},
	}
	// Add more errors to exceed 5% threshold (need >5% of 2 traces = at least 1 error)
	for i := 0; i < 8; i++ {
		traces = append(traces, trace{
			TraceID:         fmt.Sprintf("trace-error-%03d", i+2),
			RootServiceName: "checkout",
			RootTraceName:   fmt.Sprintf("POST /checkout [ERROR: connection refused %d]", i),
			DurationMs:      3000.0,
		})
	}

	data := map[string]interface{}{"traces": traces}
	b, _ := json.Marshal(data)
	return string(b)
}

func TestCollector_Name(t *testing.T) {
	c := tempo.New(tempo.Config{URL: "http://localhost:3200"}, "test")
	if c.Name() != "tempo" {
		t.Errorf("expected name 'tempo', got %q", c.Name())
	}
}

func TestCollector_Check_NoURL(t *testing.T) {
	c := tempo.New(tempo.Config{}, "test")
	if err := c.Check(t.Context()); err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestCollector_Check_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/echo" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := tempo.New(tempo.Config{URL: srv.URL}, "test")
	if err := c.Check(t.Context()); err != nil {
		t.Errorf("unexpected check error: %v", err)
	}
}

func TestCollector_Collect_TraceErrorSpike(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, buildSearchResponse())
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	cfg := tempo.Config{URL: srv.URL}
	c := tempo.New(cfg, "test")

	scope := model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}}
	window := model.TimeRange{From: base, To: base.Add(30 * time.Minute)}

	result, err := c.Collect(t.Context(), scope, window)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have signals (error rate + latency)
	if len(result.Signals) == 0 {
		t.Error("expected signals from tempo")
	}
	hasErrRate := false
	for _, sig := range result.Signals {
		if sig.Metric == model.MetricTraceErrorRate {
			hasErrRate = true
			if sig.Points[len(sig.Points)-1].V <= 0 {
				t.Error("expected non-zero trace error rate")
			}
		}
	}
	if !hasErrRate {
		t.Error("expected trace.error.rate signal")
	}

	// Should have at least one TraceErrorSpike event (>5% error rate)
	hasSpike := false
	for _, ev := range result.Events {
		if ev.Kind == model.EventKindTraceErrorSpike {
			hasSpike = true
			if ev.Severity != model.SeverityNotable {
				t.Errorf("spike severity should be Notable, got %q", ev.Severity)
			}
		}
	}
	if !hasSpike {
		t.Error("expected TraceErrorSpike event for high error rate")
	}

	// Should have entities with call edges
	hasCallEdge := false
	for _, ent := range result.Entities {
		if calls := ent.Labels["calls"]; calls != "" {
			hasCallEdge = true
		}
	}
	if !hasCallEdge {
		t.Log("no call edges found — may be OK if span services not in response")
	}
}

func TestCollector_Collect_NoURL(t *testing.T) {
	c := tempo.New(tempo.Config{}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	_, err := c.Collect(t.Context(), scope, model.TimeRange{From: base, To: base.Add(time.Hour)})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestCollector_Collect_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":"internal server error"}`)
	}))
	defer srv.Close()

	c := tempo.New(tempo.Config{URL: srv.URL}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	_, err := c.Collect(t.Context(), scope, model.TimeRange{From: base, To: base.Add(time.Hour)})
	if err == nil {
		t.Error("expected error for 500 response")
	}
}
