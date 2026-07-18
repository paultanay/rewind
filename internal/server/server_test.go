package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/server"
)

// newTestIncident builds a minimal incident for server tests.
func newTestIncident() model.Incident {
	now := time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)
	return model.Incident{
		ID:     "test-inc-001",
		Window: model.TimeRange{From: now, To: now.Add(30 * time.Minute)},
		Meta: model.Meta{
			RewindVersion: "test",
			SchemaVersion: 1,
			CreatedAt:     now,
		},
		Events: []model.Event{
			{
				ID:       "ev-1",
				At:       now.Add(5 * time.Minute),
				Kind:     model.EventKindDeploy,
				EntityID: "svc/shop/checkout",
				Severity: model.SeverityInfo,
				Title:    "Deployed checkout v2.3.1",
			},
		},
		Verdict: &model.Verdict{
			Hypotheses: []model.Hypothesis{
				{
					TriggerEventID: "ev-1",
					RuleIDs:        []string{"RW001"},
					Score:          0.82,
					Confidence:     model.ConfidenceHigh,
					Explanation:    "Deploy at 14:05 followed by 2 metric change-points within 10m",
				},
			},
		},
	}
}

// startTestServer creates a server, starts it, and returns it with a cancel function.
func startTestServer(t *testing.T) (*server.Server, context.CancelFunc) {
	t.Helper()
	srv, err := server.New(newTestIncident(), 0) // port 0 = random
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx) //nolint:errcheck
	time.Sleep(25 * time.Millisecond) // allow goroutine to bind
	return srv, cancel
}

func TestServer_HealthEndpoint(t *testing.T) {
	t.Parallel()
	srv, cancel := startTestServer(t)
	defer cancel()

	resp, err := http.Get("http://" + srv.Addr() + "/api/health") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestServer_IncidentEndpoint_ReturnsJSON(t *testing.T) {
	t.Parallel()
	srv, cancel := startTestServer(t)
	defer cancel()

	resp, err := http.Get("http://" + srv.Addr() + "/api/incident") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/incident: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var got model.Incident
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ID != "test-inc-001" {
		t.Errorf("incident ID = %q, want test-inc-001", got.ID)
	}
	if len(got.Events) != 1 {
		t.Errorf("events count = %d, want 1", len(got.Events))
	}
	if got.Verdict == nil || len(got.Verdict.Hypotheses) != 1 {
		t.Errorf("verdict hypotheses not round-tripped correctly")
	}
}

func TestServer_IncidentEndpoint_HypothesisFields(t *testing.T) {
	t.Parallel()
	srv, cancel := startTestServer(t)
	defer cancel()

	resp, err := http.Get("http://" + srv.Addr() + "/api/incident") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/incident: %v", err)
	}
	defer resp.Body.Close()

	var got model.Incident
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	h := got.Verdict.Hypotheses[0]
	if h.TriggerEventID != "ev-1" {
		t.Errorf("TriggerEventID = %q, want ev-1", h.TriggerEventID)
	}
	if h.Confidence != model.ConfidenceHigh {
		t.Errorf("Confidence = %q, want HIGH", h.Confidence)
	}
	if h.Score < 0.8 {
		t.Errorf("Score = %.3f, want >= 0.80", h.Score)
	}
}

func TestServer_IncidentEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	srv, cancel := startTestServer(t)
	defer cancel()

	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/api/incident", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/incident: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestServer_SPAFallback_ServesIndexHTML(t *testing.T) {
	t.Parallel()
	srv, cancel := startTestServer(t)
	defer cancel()

	// Any unknown path should fall back to index.html (SPA client-side routing).
	resp, err := http.Get("http://" + srv.Addr() + "/some/client/side/route") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /some/client/side/route: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("SPA fallback status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

func TestServer_RootPath_ServesIndexHTML(t *testing.T) {
	t.Parallel()
	srv, cancel := startTestServer(t)
	defer cancel()

	resp, err := http.Get("http://" + srv.Addr() + "/") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("root status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	t.Parallel()
	srv, err := server.New(newTestIncident(), 0)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	time.Sleep(25 * time.Millisecond)
	cancel() // trigger graceful shutdown

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned non-nil error on clean shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("server did not shut down within 3 seconds")
	}
}

func TestServer_Port_IsNonZero(t *testing.T) {
	t.Parallel()
	srv, err := server.New(newTestIncident(), 0)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	if srv.Port() == 0 {
		t.Error("Port() returned 0 for a random-port server")
	}
	if srv.Addr() == "" {
		t.Error("Addr() returned empty string")
	}
}
