package terminal_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"

	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/render/terminal"
)

func init() {
	// Always disable colour in tests so snapshots are deterministic.
	color.NoColor = true
}

// makeFixtureIncident returns the canonical Phase-1 test incident used for
// snapshot tests. Changing this fixture intentionally means updating the
// snapshot; accidental changes will be caught.
func makeFixtureIncident() model.Incident {
	base := time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	return model.Incident{
		ID: "inc-20260709-142000-abcd1234",
		Window: model.TimeRange{
			From: base,
			To:   base.Add(45 * time.Minute),
		},
		Scope: model.Scope{Namespaces: []string{"shop"}},
		Entities: []model.Entity{
			{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
		},
		Events: []model.Event{
			{
				ID:       "evt-001",
				At:       base,
				Kind:     model.EventKindDeploy,
				EntityID: "svc/shop/checkout",
				Severity: model.SeverityNotable,
				Title:    "Deployed checkout v2.3.1",
			},
			{
				ID:       "evt-002",
				At:       base.Add(2 * time.Minute),
				Kind:     model.EventKindOOMKill,
				EntityID: "svc/shop/checkout",
				Severity: model.SeverityCritical,
				Title:    "OOMKilled: checkout-7d9f",
			},
		},
		Signals: []model.Signal{
			{
				ID:       "sig-001",
				EntityID: "svc/shop/checkout",
				Metric:   model.MetricLatencyP99,
				Unit:     "ms",
				Points: func() []model.Point {
					pts := make([]model.Point, 10)
					for i := range pts {
						v := 40.0
						if i >= 5 {
							v = 180.0
						}
						pts[i] = model.Point{T: base.Add(time.Duration(i) * time.Minute), V: v}
					}
					return pts
				}(),
				ChangePoints: []model.ChangePoint{
					{
						At:         base.Add(5 * time.Minute),
						Direction:  model.DirectionUp,
						Magnitude:  4.5,
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
						{EventID: "evt-001", Description: "Deploy trigger", RuleID: "RW001"},
						{SignalID: "sig-001", ChangePointIndex: 0, Description: "latency.p99 ↑4.5×", RuleID: "RW001"},
					},
				},
			},
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "1.1s", SignalCount: 1},
			{Name: "github", Status: model.SourceStatusOK, Duration: "0.3s", EventCount: 1},
		},
		Meta: model.Meta{
			RewindVersion: "0.1.0-dev",
			SchemaVersion: 1,
			CreatedAt:     time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC),
		},
	}
}

func TestRenderNoError(t *testing.T) {
	t.Parallel()
	inc := makeFixtureIncident()
	var buf bytes.Buffer
	if err := terminal.Render(&buf, inc, terminal.Options{Width: 120}); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("Render produced empty output")
	}
}

func TestRenderContainsKeyElements(t *testing.T) {
	t.Parallel()
	inc := makeFixtureIncident()
	var buf bytes.Buffer
	_ = terminal.Render(&buf, inc, terminal.Options{Width: 120})
	out := buf.String()

	checks := []string{
		inc.ID,
		"shop",               // namespace
		"DEPLOY",             // event kind label
		"OOMKILL",            // critical event
		"ANOMALY",            // change-point row
		"latency.p99",        // metric name in CP row
		"VERDICT",            // verdict section
		"HIGH",               // confidence
		"RW001",              // rule ID
		"checkout v2.3.1",    // trigger title
		"0.1.0-dev",          // version in footer
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

// TestRenderDeterministic verifies that identical incidents always produce
// identical output bytes — required for snapshot tests and bundle replay.
func TestRenderDeterministic(t *testing.T) {
	t.Parallel()
	inc := makeFixtureIncident()

	var buf1, buf2 bytes.Buffer
	_ = terminal.Render(&buf1, inc, terminal.Options{Width: 120})
	_ = terminal.Render(&buf2, inc, terminal.Options{Width: 120})

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Error("two renders of identical incident produced different output")
	}
}

func TestRenderEmptyIncident(t *testing.T) {
	t.Parallel()
	// An incident with no events/signals/verdict must render without panic.
	inc := model.Incident{
		ID:     "inc-empty",
		Window: model.TimeRange{From: time.Now(), To: time.Now().Add(time.Hour)},
		Meta:   model.Meta{RewindVersion: "test", CreatedAt: time.Now()},
	}
	var buf bytes.Buffer
	if err := terminal.Render(&buf, inc, terminal.Options{Width: 80}); err != nil {
		t.Fatalf("unexpected error on empty incident: %v", err)
	}
}

func TestRenderNoTriggerFound(t *testing.T) {
	t.Parallel()
	inc := makeFixtureIncident()
	inc.Verdict = &model.Verdict{
		NoTriggerFound:   true,
		NotableAnomalies: []string{"latency.p99 ↑4.5× at 14:25:00"},
	}
	var buf bytes.Buffer
	_ = terminal.Render(&buf, inc, terminal.Options{Width: 120})
	out := buf.String()

	if !strings.Contains(out, "No clear trigger identified") {
		t.Error("expected 'No clear trigger identified' in output")
	}
	if !strings.Contains(out, "latency.p99 ↑4.5×") {
		t.Error("expected notable anomaly listed in output")
	}
}

func TestRenderNarrowWidth(t *testing.T) {
	t.Parallel()
	inc := makeFixtureIncident()
	var buf bytes.Buffer
	// Should not panic at narrow width.
	if err := terminal.Render(&buf, inc, terminal.Options{Width: 60}); err != nil {
		t.Fatalf("narrow width render failed: %v", err)
	}
}
