package bundle_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/bundle"
	"github.com/paultanay/rewind/internal/model"
)

// makeTestIncident returns a minimal but realistic Incident for testing.
func makeTestIncident() model.Incident {
	at := time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	return model.Incident{
		ID: model.NewIncidentID(at),
		Window: model.TimeRange{
			From: at,
			To:   at.Add(45 * time.Minute),
		},
		Scope: model.Scope{Namespaces: []string{"shop"}},
		Entities: []model.Entity{
			{
				ID:          "svc/shop/checkout",
				Kind:        model.EntityKindService,
				DisplayName: "checkout",
				Labels:      map[string]string{"app": "checkout"},
			},
		},
		Events: []model.Event{
			{
				ID:       "evt-001",
				At:       at,
				Kind:     model.EventKindDeploy,
				EntityID: "svc/shop/checkout",
				Severity: model.SeverityNotable,
				Title:    "Deployed checkout v2.3.1",
				Detail:   "Image: checkout:v2.3.1 (was v2.3.0)\nAuthor: alice",
				SourceRef: model.SourceRef{
					SourceName: "github",
					URL:        "https://github.com/example/checkout/deployments/42",
				},
			},
			{
				ID:       "evt-002",
				At:       at.Add(40 * time.Second),
				Kind:     model.EventKindOOMKill,
				EntityID: "svc/shop/checkout",
				Severity: model.SeverityCritical,
				Title:    "OOMKilled: checkout-7d9f-abc12",
			},
		},
		Signals: []model.Signal{
			{
				ID:       "sig-001",
				EntityID: "svc/shop/checkout",
				Metric:   model.MetricLatencyP99,
				Unit:     "ms",
				Points: []model.Point{
					{T: at, V: 42.0},
					{T: at.Add(time.Minute), V: 180.0},
				},
				ChangePoints: []model.ChangePoint{
					{
						At:         at.Add(40 * time.Second),
						Direction:  model.DirectionUp,
						Magnitude:  4.2,
						Score:      0.91,
						DetectorID: "baseline-deviation",
					},
				},
			},
		},
		Verdict: &model.Verdict{
			Hypotheses: []model.Hypothesis{
				{
					TriggerEventID: "evt-001",
					Confidence:     model.ConfidenceHigh,
					Score:          0.87,
					RuleIDs:        []string{"RW001"},
					Explanation:    "Deployment of checkout v2.3.1 preceded latency spike (rule RW001).",
					Chain: []model.ChainLink{
						{EventID: "evt-001", Description: "Deployment trigger", RuleID: "RW001"},
						{SignalID: "sig-001", ChangePointIndex: 0, Description: "latency.p99 ↑4.2×", RuleID: "RW001"},
					},
				},
			},
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "1.1s", EventCount: 0, SignalCount: 1},
			{Name: "github", Status: model.SourceStatusOK, Duration: "0.3s", EventCount: 1, SignalCount: 0},
		},
		Meta: model.Meta{
			RewindVersion: "0.1.0-dev",
			SchemaVersion: bundle.CurrentSchemaVersion,
			CreatedAt:     at,
		},
	}
}

// TestRoundTrip verifies that export → import produces an identical Incident.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	original := makeTestIncident()

	rawSources := map[string][]byte{
		"prometheus": []byte(`{"mock":"fixture"}`),
	}

	var buf bytes.Buffer
	if err := bundle.ExportTo(original, rawSources, &buf); err != nil {
		t.Fatalf("ExportTo: %v", err)
	}

	loaded, err := bundle.Read(&buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Core structural checks.
	if loaded.Incident.ID != original.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.Incident.ID, original.ID)
	}
	if !loaded.Incident.Window.From.Equal(original.Window.From) {
		t.Errorf("Window.From mismatch")
	}
	if len(loaded.Incident.Events) != len(original.Events) {
		t.Errorf("Events length: got %d, want %d", len(loaded.Incident.Events), len(original.Events))
	}
	if len(loaded.Incident.Signals) != len(original.Signals) {
		t.Errorf("Signals length: got %d, want %d", len(loaded.Incident.Signals), len(original.Signals))
	}
	if loaded.Incident.Verdict == nil {
		t.Fatal("Verdict is nil after round-trip")
	}
	if len(loaded.Incident.Verdict.Hypotheses) != 1 {
		t.Errorf("Hypotheses: got %d, want 1", len(loaded.Incident.Verdict.Hypotheses))
	}
	if loaded.Incident.Verdict.Hypotheses[0].Confidence != model.ConfidenceHigh {
		t.Errorf("Confidence: got %q", loaded.Incident.Verdict.Hypotheses[0].Confidence)
	}

	// Source fixture preserved.
	if string(loaded.RawSources["prometheus"]) != `{"mock":"fixture"}` {
		t.Errorf("raw source not preserved: %q", loaded.RawSources["prometheus"])
	}

	// Schema version stamped.
	if loaded.Incident.Meta.SchemaVersion != bundle.CurrentSchemaVersion {
		t.Errorf("SchemaVersion: got %d, want %d", loaded.Incident.Meta.SchemaVersion, bundle.CurrentSchemaVersion)
	}
}

// TestDoubleExportIdentical verifies that two exports of the same incident
// produce byte-identical output (reproducibility requirement).
func TestDoubleExportIdentical(t *testing.T) {
	t.Parallel()
	inc := makeTestIncident()
	// Fix CreatedAt so the two exports are truly identical.
	inc.Meta.CreatedAt = time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)

	var buf1, buf2 bytes.Buffer
	if err := bundle.ExportTo(inc, nil, &buf1); err != nil {
		t.Fatalf("first export: %v", err)
	}
	if err := bundle.ExportTo(inc, nil, &buf2); err != nil {
		t.Fatalf("second export: %v", err)
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Errorf("two exports of identical incident produced different bytes (%d vs %d bytes)",
			buf1.Len(), buf2.Len())
	}
}

// TestEmptyBundleRejected checks that a zero-byte or garbage reader returns an error.
func TestEmptyBundleRejected(t *testing.T) {
	t.Parallel()
	_, err := bundle.Read(bytes.NewReader(nil))
	if err == nil {
		t.Error("expected error reading empty bundle, got nil")
	}
}

// TestNewerSchemaRejected verifies forward-compatibility guard.
func TestNewerSchemaRejected(t *testing.T) {
	t.Parallel()
	inc := makeTestIncident()
	inc.Meta.SchemaVersion = bundle.CurrentSchemaVersion + 99 // simulate future version

	var buf bytes.Buffer
	if err := bundle.ExportTo(inc, nil, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	_, err := bundle.Read(&buf)
	if err == nil {
		t.Error("expected error for schema version newer than tool supports")
	}
}
