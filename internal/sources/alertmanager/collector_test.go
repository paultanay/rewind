package alertmanager_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/sources/alertmanager"
)

var base = time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)

type amAlert struct {
	Fingerprint string `json:"fingerprint"`
	Status      struct {
		State string `json:"state"`
	} `json:"status"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       *string           `json:"endsAt"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	GeneratorURL string            `json:"generatorURL"`
	Receivers    []struct {
		Name string `json:"name"`
	} `json:"receivers"`
}

func buildAlerts(alerts []amAlert) string {
	b, _ := json.Marshal(alerts)
	return string(b)
}

func TestCollector_Name(t *testing.T) {
	c := alertmanager.New(alertmanager.Config{URL: "http://localhost:9093"}, "test")
	if c.Name() != "alertmanager" {
		t.Errorf("expected 'alertmanager', got %q", c.Name())
	}
}

func TestCollector_Check_NoURL(t *testing.T) {
	c := alertmanager.New(alertmanager.Config{}, "test")
	if err := c.Check(t.Context()); err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestCollector_Check_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/status" {
			w.WriteHeader(200)
			fmt.Fprint(w, `{"cluster":{"status":"ready"}}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := alertmanager.New(alertmanager.Config{URL: srv.URL}, "test")
	if err := c.Check(t.Context()); err != nil {
		t.Errorf("unexpected check error: %v", err)
	}
}

func TestCollector_Collect_AlertFired(t *testing.T) {
	startsAt := base.Add(5 * time.Minute).Format(time.RFC3339Nano)
	alerts := []amAlert{
		{
			Fingerprint: "fp-001",
			Status: struct {
				State string `json:"state"`
			}{"active"},
			StartsAt: startsAt,
			Labels: map[string]string{
				"alertname": "HighErrorRate",
				"namespace": "shop",
				"app":       "checkout",
				"severity":  "critical",
			},
			Annotations: map[string]string{
				"summary": "checkout error rate above 10%",
			},
			GeneratorURL: "http://prometheus:9090/alerts",
			Receivers: []struct {
				Name string `json:"name"`
			}{{Name: "pagerduty"}},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, buildAlerts(alerts))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := alertmanager.New(alertmanager.Config{URL: srv.URL}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}}
	window := model.TimeRange{From: base, To: base.Add(30 * time.Minute)}

	result, err := c.Collect(t.Context(), scope, window)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Events) == 0 {
		t.Fatal("expected at least one event")
	}
	ev := result.Events[0]
	if ev.Kind != model.EventKindAlertFired {
		t.Errorf("expected AlertFired, got %q", ev.Kind)
	}
	if ev.Severity != model.SeverityCritical {
		t.Errorf("expected Critical severity for 'critical' label, got %q", ev.Severity)
	}
	if ev.Title != "HighErrorRate" {
		t.Errorf("expected title 'HighErrorRate', got %q", ev.Title)
	}
	// Entity should be svc/shop/checkout (app label)
	if ev.EntityID != "svc/shop/checkout" {
		t.Errorf("expected entity 'svc/shop/checkout', got %q", ev.EntityID)
	}
	// SourceRef should carry the generatorURL
	if ev.SourceRef.URL != "http://prometheus:9090/alerts" {
		t.Errorf("unexpected sourceRef URL: %q", ev.SourceRef.URL)
	}
	// No signals from alertmanager
	if len(result.Signals) != 0 {
		t.Errorf("alertmanager should produce no signals, got %d", len(result.Signals))
	}
}

func TestCollector_Collect_AlertResolved(t *testing.T) {
	startsAt := base.Add(2 * time.Minute).Format(time.RFC3339Nano)
	endsAt := base.Add(15 * time.Minute).Format(time.RFC3339Nano)
	alerts := []amAlert{
		{
			Fingerprint: "fp-002",
			Status: struct {
				State string `json:"state"`
			}{"resolved"},
			StartsAt: startsAt,
			EndsAt:   &endsAt,
			Labels: map[string]string{
				"alertname": "PodCrashLoop",
				"namespace": "shop",
				"pod":       "checkout-7d9f-abc",
				"severity":  "warning",
			},
			Annotations: map[string]string{},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, buildAlerts(alerts))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := alertmanager.New(alertmanager.Config{URL: srv.URL}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	window := model.TimeRange{From: base, To: base.Add(30 * time.Minute)}

	result, err := c.Collect(t.Context(), scope, window)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Events) == 0 {
		t.Fatal("expected at least one event")
	}
	// Should produce both AlertFired and AlertResolved
	kinds := make(map[model.EventKind]bool)
	for _, ev := range result.Events {
		kinds[ev.Kind] = true
		// Entity should be pod-based
		if ev.EntityID != "pod/shop/checkout-7d9f-abc" {
			t.Errorf("expected pod entity, got %q", ev.EntityID)
		}
	}
	if !kinds[model.EventKindAlertFired] {
		t.Error("expected AlertFired event")
	}
	if !kinds[model.EventKindAlertResolved] {
		t.Error("expected AlertResolved event")
	}
}

func TestCollector_Collect_OutsideWindow(t *testing.T) {
	// Alert that started and ended before the window
	startsAt := base.Add(-30 * time.Minute).Format(time.RFC3339Nano)
	endsAt := base.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	alerts := []amAlert{
		{
			Fingerprint: "fp-stale",
			Status: struct {
				State string `json:"state"`
			}{"resolved"},
			StartsAt: startsAt,
			EndsAt:   &endsAt,
			Labels:   map[string]string{"alertname": "Stale", "namespace": "shop"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, buildAlerts(alerts))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := alertmanager.New(alertmanager.Config{URL: srv.URL}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	window := model.TimeRange{From: base, To: base.Add(30 * time.Minute)}

	result, err := c.Collect(t.Context(), scope, window)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Events) != 0 {
		t.Errorf("expected no events for alerts outside window, got %d", len(result.Events))
	}
}

func TestCollector_Collect_Deduplication(t *testing.T) {
	startsAt := base.Add(5 * time.Minute).Format(time.RFC3339Nano)
	// Same fingerprint twice
	alerts := []amAlert{
		{Fingerprint: "fp-dup", StartsAt: startsAt,
			Status: struct {
				State string `json:"state"`
			}{"active"},
			Labels: map[string]string{"alertname": "DupAlert", "namespace": "shop"}},
		{Fingerprint: "fp-dup", StartsAt: startsAt,
			Status: struct {
				State string `json:"state"`
			}{"active"},
			Labels: map[string]string{"alertname": "DupAlert", "namespace": "shop"}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, buildAlerts(alerts))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := alertmanager.New(alertmanager.Config{URL: srv.URL}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	window := model.TimeRange{From: base, To: base.Add(30 * time.Minute)}

	result, _ := c.Collect(t.Context(), scope, window)
	if len(result.Events) != 1 {
		t.Errorf("expected 1 event (deduplicated), got %d", len(result.Events))
	}
}

func TestCollector_Collect_NoURL(t *testing.T) {
	c := alertmanager.New(alertmanager.Config{}, "test")
	scope := model.Scope{Namespaces: []string{"shop"}}
	_, err := c.Collect(t.Context(), scope, model.TimeRange{From: base, To: base.Add(time.Hour)})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}
