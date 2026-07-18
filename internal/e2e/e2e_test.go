// Package e2e contains end-to-end tests that exercise the full pipeline:
// Prometheus fixture server → collector → analysis → renderer → bundle.
// These tests run without any external infrastructure.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"

	"github.com/paultanay/rewind/internal/analyze"
	"github.com/paultanay/rewind/internal/bundle"
	"github.com/paultanay/rewind/internal/model"
	"github.com/paultanay/rewind/internal/render/terminal"
	"github.com/paultanay/rewind/internal/sources"
	prom "github.com/paultanay/rewind/internal/sources/prometheus"
)

func init() {
	color.NoColor = true
}

// ─── Fixture server ───────────────────────────────────────────────────────────

// makeIncidentResponse generates a Prometheus matrix response with a step
// increase at stepIdx, simulating a bad deployment.
func makeIncidentResponse(baseTS float64, n, stepIdx int, before, after float64) []byte {
	type sample [2]any
	type result struct {
		Metric map[string]string `json:"metric"`
		Values []sample          `json:"values"`
	}
	values := make([]sample, n)
	for i := range values {
		v := before
		if i >= stepIdx {
			v = after
		}
		values[i] = sample{baseTS + float64(i*60), fmt.Sprintf("%.6f", v)}
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
			Result:     []result{{Metric: map[string]string{}, Values: values}},
		},
	})
	return b
}

// scenarioServer returns a test server that serves different metric shapes
// for different query types, simulating the checkout bad-deploy scenario.
func scenarioServer(t *testing.T, baseTS float64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")

		var body []byte
		switch {
		case strings.Contains(q, "p99") || strings.Contains(q, "duration"):
			// latency.p99: 40ms → 180ms step at index 10
			body = makeIncidentResponse(baseTS, 45, 10, 0.040, 0.180)
		case strings.Contains(q, "5..") || strings.Contains(q, "error"):
			// error.rate: 0.01 → 0.25 step at index 12
			body = makeIncidentResponse(baseTS, 45, 12, 0.01, 0.25)
		case strings.Contains(q, "cpu") || strings.Contains(q, "throttl"):
			// cpu: stable at 30%
			body = makeIncidentResponse(baseTS, 45, 45, 0.30, 0.30)
		case strings.Contains(q, "memory") || strings.Contains(q, "working_set"):
			// memory: spike at index 8, indicating pre-OOM buildup
			body = makeIncidentResponse(baseTS, 45, 8, 0.50, 0.95)
		case strings.Contains(q, "restart"):
			// restarts: all zero baseline, then 3 at index 15
			body = makeIncidentResponse(baseTS, 45, 15, 0, 3)
		default:
			// request.rate: stable (not affected by the incident)
			body = makeIncidentResponse(baseTS, 45, 45, 100, 100)
		}
		w.Write(body) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestE2E_BadDeployScenario(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	baseTS := float64(base.Unix())
	srv := scenarioServer(t, baseTS)

	window := model.TimeRange{
		From: base,
		To:   base.Add(45 * time.Minute),
	}
	scope := model.Scope{
		Namespaces: []string{"shop"},
		Services:   []string{"checkout"},
	}

	// ── Collect ──────────────────────────────────────────────────────────────
	collector := &prom.Collector{
		URL:     srv.URL,
		Version: "test",
	}
	runResult := sources.RunAll(
		context.Background(),
		[]sources.Collector{collector},
		scope, window,
		30*time.Second,
	)

	if len(runResult.Signals) == 0 {
		t.Fatal("expected signals from collector, got none")
	}
	t.Logf("collected %d signals, %d events", len(runResult.Signals), len(runResult.Events))

	// ── Build incident ────────────────────────────────────────────────────────
	inc := model.Incident{
		ID:       "e2e-bad-deploy",
		Window:   window,
		Scope:    scope,
		Entities: runResult.Entities,
		Events:   runResult.Events,
		Signals:  runResult.Signals,
		Sources:  runResult.Reports,
		Meta: model.Meta{
			RewindVersion: "test",
			SchemaVersion: bundle.CurrentSchemaVersion,
			CreatedAt:     base,
		},
	}

	// ── Analyse ───────────────────────────────────────────────────────────────
	inc = analyze.Run(inc)

	// Verify change-points were detected on incident signals.
	hasAnomalies := false
	for _, sig := range inc.Signals {
		if len(sig.ChangePoints) > 0 {
			hasAnomalies = true
			t.Logf("signal %s (%s): %d change-point(s), first score=%.2f dir=%s",
				sig.Metric, sig.EntityID,
				len(sig.ChangePoints),
				sig.ChangePoints[0].Score,
				sig.ChangePoints[0].Direction,
			)
		}
	}
	if !hasAnomalies {
		t.Error("expected at least one signal with change-points after analysis, got none")
	}

	// ── Render (terminal) ─────────────────────────────────────────────────────
	var buf bytes.Buffer
	if err := terminal.Render(&buf, inc, terminal.Options{Width: 120, NoColor: true}); err != nil {
		t.Fatalf("terminal render failed: %v", err)
	}
	out := buf.String()
	if len(out) == 0 {
		t.Error("terminal renderer produced empty output")
	}
	// Must show the ANOMALY rows.
	if !strings.Contains(out, "ANOMALY") {
		t.Error("terminal output missing ANOMALY rows")
	}
	t.Logf("terminal output (%d bytes):\n%s", len(out), out)

	// ── Bundle export/import round-trip ───────────────────────────────────────
	tmpFile := t.TempDir() + "/e2e.rewind"
	if err := bundle.Export(inc, runResult.RawSources, tmpFile); err != nil {
		t.Fatalf("bundle export failed: %v", err)
	}
	if fi, err := os.Stat(tmpFile); err != nil || fi.Size() == 0 {
		t.Fatalf("bundle file not written or empty: %v", err)
	}

	loaded, err := bundle.Import(tmpFile)
	if err != nil {
		t.Fatalf("bundle import failed: %v", err)
	}
	if loaded.Incident.ID != inc.ID {
		t.Errorf("round-trip ID mismatch: got %q want %q", loaded.Incident.ID, inc.ID)
	}
	if len(loaded.Incident.Signals) != len(inc.Signals) {
		t.Errorf("round-trip signals: got %d want %d", len(loaded.Incident.Signals), len(inc.Signals))
	}

	// Change-points must survive the round-trip.
	for i, sig := range loaded.Incident.Signals {
		orig := inc.Signals[i]
		if len(sig.ChangePoints) != len(orig.ChangePoints) {
			t.Errorf("signal %s: round-trip change-points %d → %d",
				sig.Metric, len(orig.ChangePoints), len(sig.ChangePoints))
		}
	}
	t.Logf("bundle size: %d bytes", func() int64 { fi, _ := os.Stat(tmpFile); return fi.Size() }())
}

func TestE2E_Replay(t *testing.T) {
	t.Parallel()

	// Create a bundle, then replay it — analysis should produce identical results.
	base := time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	baseTS := float64(base.Unix())
	srv := scenarioServer(t, baseTS)

	window := model.TimeRange{From: base, To: base.Add(45 * time.Minute)}
	scope := model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}}

	collector := &prom.Collector{URL: srv.URL, Version: "test"}
	runResult := sources.RunAll(context.Background(), []sources.Collector{collector}, scope, window, 30*time.Second)

	inc := model.Incident{
		ID: "e2e-replay", Window: window, Scope: scope,
		Entities: runResult.Entities, Events: runResult.Events,
		Signals: runResult.Signals, Sources: runResult.Reports,
		Meta: model.Meta{RewindVersion: "test", SchemaVersion: bundle.CurrentSchemaVersion, CreatedAt: base},
	}
	inc = analyze.Run(inc)

	tmpFile := t.TempDir() + "/replay.rewind"
	if err := bundle.Export(inc, runResult.RawSources, tmpFile); err != nil {
		t.Fatalf("export: %v", err)
	}

	// Import and re-run analysis.
	loaded, err := bundle.Import(tmpFile)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	replayed := analyze.Run(loaded.Incident)

	// Change-point counts must be identical.
	if len(replayed.Signals) != len(inc.Signals) {
		t.Fatalf("replay signal count mismatch: %d vs %d", len(replayed.Signals), len(inc.Signals))
	}
	for i := range replayed.Signals {
		got := len(replayed.Signals[i].ChangePoints)
		want := len(inc.Signals[i].ChangePoints)
		if got != want {
			t.Errorf("replay signal %s: %d change-points, want %d",
				replayed.Signals[i].Metric, got, want)
		}
	}
}

func TestE2E_SourceFailed_GracefulDegradation(t *testing.T) {
	t.Parallel()
	// Server returns 500 — RunAll should complete, reports error, returns empty signals.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	collector := &prom.Collector{URL: srv.URL, Version: "test"}
	window := model.TimeRange{From: time.Now().Add(-time.Hour), To: time.Now()}
	runResult := sources.RunAll(
		context.Background(),
		[]sources.Collector{collector},
		model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		window,
		5*time.Second,
	)

	if len(runResult.Reports) == 0 {
		t.Fatal("expected a SourceReport even on failure")
	}
	if runResult.Reports[0].Status != model.SourceStatusFailed {
		t.Errorf("expected failed status, got %s", runResult.Reports[0].Status)
	}
	// Analysis must not panic on empty signals.
	inc := model.Incident{
		ID: "e2e-degraded", Window: window,
		Signals: runResult.Signals,
		Sources: runResult.Reports,
	}
	result := analyze.Run(inc)
	if len(result.Signals) != 0 {
		t.Error("expected 0 signals after source failure")
	}
}
