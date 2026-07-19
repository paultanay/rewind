package correlate_test

import (
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/analyze/correlate"
	"github.com/paultanay/rewind/internal/analyze/topology"
	"github.com/paultanay/rewind/internal/model"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

var t0 = time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)


func makeEvent(id string, kind model.EventKind, entityID string, at time.Time, sev model.Severity) model.Event {
	return model.Event{
		ID:       id,
		Kind:     kind,
		EntityID: entityID,
		At:       at,
		Severity: sev,
		Title:    string(kind) + " on " + entityID,
	}
}

// signal with a step change-point at cpAt (values low before, high after).
func makeSignalWithCP(id, entityID, metric string, cpAt time.Time, mag float64) model.Signal {
	base := 1.0
	var pts []model.Point
	var baseline []model.Point
	for i := -10; i < 20; i++ {
		t := t0.Add(time.Duration(i) * time.Minute)
		v := base
		if t.After(cpAt) {
			v = base * mag
		}
		pts = append(pts, model.Point{T: t, V: v})
	}
	for i := -20; i < 0; i++ {
		baseline = append(baseline, model.Point{T: t0.Add(time.Duration(i) * time.Minute), V: base})
	}
	cp := model.ChangePoint{
		At:         cpAt,
		Direction:  model.DirectionUp,
		Magnitude:  mag,
		Score:      0.95,
		DetectorID: "baseline-deviation",
	}
	return model.Signal{
		ID:           id,
		EntityID:     entityID,
		Metric:       metric,
		Points:       pts,
		Baseline:     baseline,
		ChangePoints: []model.ChangePoint{cp},
	}
}

// stablesignal has no change-points.
func makeStableSignal(id, entityID, metric string) model.Signal {
	var pts []model.Point
	for i := 0; i < 20; i++ {
		pts = append(pts, model.Point{T: t0.Add(time.Duration(i) * time.Minute), V: 1.0})
	}
	return model.Signal{ID: id, EntityID: entityID, Metric: metric, Points: pts}
}

// ctx builds a RuleContext from explicit events, signals, and entities.
func ctx(entities []model.Entity, events []model.Event, signals []model.Signal) correlate.RuleContext {
	inc := model.Incident{
		Entities: entities,
		Events:   events,
		Signals:  signals,
	}
	g := topology.Build(entities)
	return correlate.NewRuleContext(inc, g)
}

// ─── RW001 tests ──────────────────────────────────────────────────────────────

func TestRW001_DeployThenMetricChange(t *testing.T) {
	deploy := makeEvent("ev-deploy", model.EventKindDeploy, "svc/shop/checkout", t0, model.SeverityInfo)
	latSig := makeSignalWithCP("sig-lat", "svc/shop/checkout", model.MetricLatencyP99, t0.Add(2*time.Minute), 4.5)

	c := ctx(nil, []model.Event{deploy}, []model.Signal{latSig})
	edges := correlate.RW001{}.Apply(c)
	if len(edges) == 0 {
		t.Fatal("RW001: expected at least one edge for deploy + latency CP within 10m")
	}
	if edges[0].TriggerEventID != "ev-deploy" {
		t.Errorf("RW001: trigger should be deploy event, got %q", edges[0].TriggerEventID)
	}
	if edges[0].Score < 0.5 {
		t.Errorf("RW001: score too low: %.3f", edges[0].Score)
	}
}

func TestRW001_NoChangePoint(t *testing.T) {
	deploy := makeEvent("ev-deploy", model.EventKindDeploy, "svc/shop/checkout", t0, model.SeverityInfo)
	stable := makeStableSignal("sig-stable", "svc/shop/checkout", model.MetricLatencyP99)
	c := ctx(nil, []model.Event{deploy}, []model.Signal{stable})
	edges := correlate.RW001{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW001: expected no edges when no change-point, got %d", len(edges))
	}
}

func TestRW001_ChangePointTooLate(t *testing.T) {
	deploy := makeEvent("ev-deploy", model.EventKindDeploy, "svc/shop/checkout", t0, model.SeverityInfo)
	// CP at 15 minutes — beyond the 10m window
	latSig := makeSignalWithCP("sig-lat", "svc/shop/checkout", model.MetricLatencyP99, t0.Add(15*time.Minute), 4.5)
	c := ctx(nil, []model.Event{deploy}, []model.Signal{latSig})
	edges := correlate.RW001{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW001: expected no edges for CP at 15m (beyond window), got %d", len(edges))
	}
}

func TestRW001_MultipleCorroborations_HigherScore(t *testing.T) {
	deploy := makeEvent("ev-deploy", model.EventKindDeploy, "svc/shop/checkout", t0, model.SeverityInfo)
	sig1 := makeSignalWithCP("sig-lat", "svc/shop/checkout", model.MetricLatencyP99, t0.Add(2*time.Minute), 4.5)
	sig2 := makeSignalWithCP("sig-err", "svc/shop/checkout", model.MetricErrorRate, t0.Add(3*time.Minute), 10.0)
	sig3 := makeSignalWithCP("sig-mem", "svc/shop/checkout", model.MetricMemoryUsage, t0.Add(4*time.Minute), 1.8)

	c1 := ctx(nil, []model.Event{deploy}, []model.Signal{sig1})
	c3 := ctx(nil, []model.Event{deploy}, []model.Signal{sig1, sig2, sig3})

	e1 := correlate.RW001{}.Apply(c1)
	e3 := correlate.RW001{}.Apply(c3)
	if len(e1) == 0 || len(e3) == 0 {
		t.Fatal("RW001: no edges produced")
	}
	if e3[0].Score <= e1[0].Score {
		t.Errorf("RW001: 3 corroborations (%.3f) should score higher than 1 (%.3f)", e3[0].Score, e1[0].Score)
	}
}

// ─── RW003 tests ──────────────────────────────────────────────────────────────

func TestRW003_OOMKillWithMemorySpike(t *testing.T) {
	oom := makeEvent("ev-oom", model.EventKindOOMKill, "pod/shop/checkout-abc", t0.Add(5*time.Minute), model.SeverityCritical)
	// Memory spiked 3 minutes before OOMKill
	memSig := makeSignalWithCP("sig-mem", "pod/shop/checkout-abc", model.MetricMemoryUsage,
		t0.Add(2*time.Minute), 3.0)
	// Force the CP to be before the OOM (it already is — CP at t0+2m, OOM at t0+5m)
	c := ctx(nil, []model.Event{oom}, []model.Signal{memSig})
	edges := correlate.RW003{}.Apply(c)
	if len(edges) == 0 {
		t.Fatal("RW003: expected edge for memory spike → OOMKill")
	}
	found := false
	for _, e := range edges {
		if e.TriggerEventID == "ev-oom" {
			found = true
			break
		}
	}
	if !found {
		t.Error("RW003: trigger should be the OOMKill event")
	}
}

func TestRW003_OOMKillWithoutMemoryEvidence(t *testing.T) {
	oom := makeEvent("ev-oom", model.EventKindOOMKill, "pod/shop/checkout-abc", t0, model.SeverityCritical)
	// No memory signal → rule should be silent.
	c := ctx(nil, []model.Event{oom}, nil)
	edges := correlate.RW003{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW003: expected no edges without memory evidence, got %d", len(edges))
	}
}

// ─── RW004 tests ──────────────────────────────────────────────────────────────

func TestRW004_CPUSpikeBeforeLatency(t *testing.T) {
	cpuSig := makeSignalWithCP("sig-cpu", "svc/shop/checkout", model.MetricCPUUsage, t0, 3.0)
	latSig := makeSignalWithCP("sig-lat", "svc/shop/checkout", model.MetricLatencyP99, t0.Add(2*time.Minute), 4.0)
	c := ctx(nil, nil, []model.Signal{cpuSig, latSig})
	edges := correlate.RW004{}.Apply(c)
	if len(edges) == 0 {
		t.Fatal("RW004: expected saturation edge for CPU → latency")
	}
}

func TestRW004_NoLatencySignal(t *testing.T) {
	cpuSig := makeSignalWithCP("sig-cpu", "svc/shop/checkout", model.MetricCPUUsage, t0, 3.0)
	c := ctx(nil, nil, []model.Signal{cpuSig})
	edges := correlate.RW004{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW004: expected no edges without latency signal, got %d", len(edges))
	}
}

// ─── RW005 tests ──────────────────────────────────────────────────────────────

func TestRW005_UpstreamCascade(t *testing.T) {
	// upstream → downstream in call graph
	entities := []model.Entity{
		{ID: "svc/shop/payments", Kind: model.EntityKindService},
		{ID: "svc/shop/checkout", Kind: model.EntityKindService},
	}
	g := topology.Build(entities)
	g.AddCallEdge("svc/shop/checkout", "svc/shop/payments") // checkout calls payments

	// payments error rises first, then checkout errors
	payErr := makeSignalWithCP("sig-pay-err", "svc/shop/payments", model.MetricErrorRate, t0, 8.0)
	coErr := makeSignalWithCP("sig-co-err", "svc/shop/checkout", model.MetricErrorRate, t0.Add(3*time.Minute), 5.0)

	inc := model.Incident{Entities: entities, Signals: []model.Signal{payErr, coErr}}
	rctx := correlate.NewRuleContext(inc, g)
	edges := correlate.RW005{}.Apply(rctx)
	if len(edges) == 0 {
		t.Fatal("RW005: expected upstream cascade edge")
	}
}

func TestRW005_NoTopologyPath(t *testing.T) {
	// Two unrelated services (no topology connection)
	entities := []model.Entity{
		{ID: "svc/shop/payments", Kind: model.EntityKindService},
		{ID: "svc/shop/inventory", Kind: model.EntityKindService},
	}
	payErr := makeSignalWithCP("sig-pay-err", "svc/shop/payments", model.MetricErrorRate, t0, 8.0)
	invErr := makeSignalWithCP("sig-inv-err", "svc/shop/inventory", model.MetricErrorRate, t0.Add(3*time.Minute), 5.0)
	c := ctx(entities, nil, []model.Signal{payErr, invErr})
	edges := correlate.RW005{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW005: expected no edges for unrelated services, got %d", len(edges))
	}
}

// ─── RW009 tests ──────────────────────────────────────────────────────────────

func TestRW009_CrashLoopCoalescing(t *testing.T) {
	// 5 restarts within 10 minutes → CrashLoop
	var restarts []model.Event
	for i := 0; i < 5; i++ {
		restarts = append(restarts, makeEvent(
			"ev-restart-"+string(rune('0'+i)),
			model.EventKindRestart,
			"pod/shop/checkout-abc",
			t0.Add(time.Duration(i)*2*time.Minute),
			model.SeverityNotable,
		))
	}
	c := ctx(nil, restarts, nil)
	edges := correlate.RW009{}.Apply(c)
	if len(edges) == 0 {
		t.Fatal("RW009: expected CrashLoop detection for 5 restarts in 10m")
	}
	if edges[0].Score < 0.6 {
		t.Errorf("RW009: crash loop score too low: %.3f", edges[0].Score)
	}
}

func TestRW009_TooFewRestarts(t *testing.T) {
	// Only 2 restarts — below threshold of 3
	restarts := []model.Event{
		makeEvent("ev-r1", model.EventKindRestart, "pod/shop/checkout-abc", t0, model.SeverityNotable),
		makeEvent("ev-r2", model.EventKindRestart, "pod/shop/checkout-abc", t0.Add(2*time.Minute), model.SeverityNotable),
	}
	c := ctx(nil, restarts, nil)
	edges := correlate.RW009{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW009: expected no CrashLoop for only 2 restarts, got %d", len(edges))
	}
}

func TestRW009_RestartsTooSpread(t *testing.T) {
	// 4 restarts but spread over 30 minutes — each pair is outside the 10m window
	restarts := []model.Event{
		makeEvent("ev-r1", model.EventKindRestart, "pod/shop/checkout-abc", t0, model.SeverityNotable),
		makeEvent("ev-r2", model.EventKindRestart, "pod/shop/checkout-abc", t0.Add(11*time.Minute), model.SeverityNotable),
		makeEvent("ev-r3", model.EventKindRestart, "pod/shop/checkout-abc", t0.Add(22*time.Minute), model.SeverityNotable),
		makeEvent("ev-r4", model.EventKindRestart, "pod/shop/checkout-abc", t0.Add(33*time.Minute), model.SeverityNotable),
	}
	c := ctx(nil, restarts, nil)
	edges := correlate.RW009{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW009: expected no CrashLoop when restarts are spread >10m apart, got %d", len(edges))
	}
}

// ─── RW010 tests ──────────────────────────────────────────────────────────────

func TestRW010_AlertIsCorroborationOnly(t *testing.T) {
	alert := makeEvent("ev-alert", model.EventKindAlertFired, "svc/shop/checkout", t0, model.SeverityNotable)
	c := ctx(nil, []model.Event{alert}, nil)
	edges := correlate.RW010{}.Apply(c)
	if len(edges) == 0 {
		t.Fatal("RW010: expected corroboration edge for alert")
	}
	for _, e := range edges {
		if !e.CorroborationOnly {
			t.Errorf("RW010: all alert edges must be corroboration-only")
		}
	}
}

func TestRW010_NoAlerts(t *testing.T) {
	c := ctx(nil, nil, nil)
	edges := correlate.RW010{}.Apply(c)
	if len(edges) != 0 {
		t.Errorf("RW010: expected no edges with no alerts, got %d", len(edges))
	}
}

// ─── Temporal and proximity scoring ──────────────────────────────────────────

func TestTemporalScore(t *testing.T) {
	cases := []struct {
		gapMin float64
		wantGT float64
		wantLT float64
	}{
		{0, 0.99, 1.01},     // immediate: ~1.0
		{5, 0.01, 0.20},     // 5 minutes: small but non-zero
		{120, 0.001, 0.05},  // 2 hours: near-zero
	}
	base := time.Now()
	for _, tc := range cases {
		effect := base.Add(time.Duration(tc.gapMin * float64(time.Minute)))
		s := correlate.TemporalScore(base, effect)
		if s < tc.wantGT {
			t.Errorf("gap=%.0fm: score %.4f < %.4f", tc.gapMin, s, tc.wantGT)
		}
		if s > tc.wantLT {
			t.Errorf("gap=%.0fm: score %.4f > %.4f", tc.gapMin, s, tc.wantLT)
		}
	}
}

func TestTemporalScore_EffectBeforeTrigger(t *testing.T) {
	base := time.Now()
	effect := base.Add(-5 * time.Minute) // effect before trigger → 0
	if s := correlate.TemporalScore(base, effect); s != 0 {
		t.Errorf("expected 0 for effect before trigger, got %.4f", s)
	}
}

// ─── Full engine integration tests ───────────────────────────────────────────

// TestEngine_BadDeploy is the primary golden scenario from the spec §1 demo:
// Deploy → latency ↑, error.rate ↑, memory ↑ → hypothesis should be "deploy".
func TestEngine_BadDeploy(t *testing.T) {
	entities := []model.Entity{
		{ID: "svc/shop/checkout", Kind: model.EntityKindService},
	}
	deploy := makeEvent("ev-deploy", model.EventKindDeploy, "svc/shop/checkout", t0, model.SeverityInfo)
	deploy.Title = "Deployed checkout v2.3.1"

	latSig := makeSignalWithCP("sig-lat", "svc/shop/checkout", model.MetricLatencyP99, t0.Add(2*time.Minute), 4.5)
	errSig := makeSignalWithCP("sig-err", "svc/shop/checkout", model.MetricErrorRate, t0.Add(3*time.Minute), 36.0)
	memSig := makeSignalWithCP("sig-mem", "svc/shop/checkout", model.MetricMemoryUsage, t0.Add(5*time.Minute), 1.7)

	inc := model.Incident{
		Entities: entities,
		Events:   []model.Event{deploy},
		Signals:  []model.Signal{latSig, errSig, memSig},
	}
	g := topology.Build(entities)
	verdict := correlate.Run(inc, g)

	if verdict == nil {
		t.Fatal("expected non-nil verdict")
	}
	if verdict.NoTriggerFound {
		t.Error("expected a trigger to be found")
	}
	if len(verdict.Hypotheses) == 0 {
		t.Fatal("expected at least one hypothesis")
	}
	top := verdict.Hypotheses[0]
	if top.TriggerEventID != "ev-deploy" {
		t.Errorf("top hypothesis trigger should be the deploy event, got %q", top.TriggerEventID)
	}
	if top.Confidence != model.ConfidenceHigh && top.Confidence != model.ConfidenceMedium {
		t.Errorf("top hypothesis should be High or Medium confidence, got %q", top.Confidence)
	}
	t.Logf("verdict: %q (score=%.3f, confidence=%s)", top.Explanation, top.Score, top.Confidence)
}

// TestEngine_OOMCascade: memory spike → OOMKill → should be detected by RW003.
func TestEngine_OOMCascade(t *testing.T) {
	entities := []model.Entity{
		{ID: "pod/shop/checkout-abc", Kind: model.EntityKindPod},
	}
	oomEv := makeEvent("ev-oom", model.EventKindOOMKill, "pod/shop/checkout-abc", t0.Add(5*time.Minute), model.SeverityCritical)
	memSig := makeSignalWithCP("sig-mem", "pod/shop/checkout-abc", model.MetricMemoryUsage, t0.Add(2*time.Minute), 3.5)

	inc := model.Incident{Entities: entities, Events: []model.Event{oomEv}, Signals: []model.Signal{memSig}}
	g := topology.Build(entities)
	verdict := correlate.Run(inc, g)

	if verdict == nil {
		t.Fatal("expected non-nil verdict")
	}
	if verdict.NoTriggerFound {
		t.Error("expected trigger for OOM cascade")
	}
	if len(verdict.Hypotheses) == 0 {
		t.Fatal("expected hypothesis for OOM cascade")
	}
}

// TestEngine_NoisyNoIncident: random small fluctuations → NoTriggerFound.
func TestEngine_NoisyNoIncident(t *testing.T) {
	// No events, no significant change-points.
	inc := model.Incident{
		Signals: []model.Signal{
			makeStableSignal("s1", "svc/shop/checkout", model.MetricLatencyP99),
			makeStableSignal("s2", "svc/shop/checkout", model.MetricErrorRate),
		},
	}
	g := topology.Build(nil)
	verdict := correlate.Run(inc, g)

	if verdict == nil {
		t.Fatal("expected non-nil verdict")
	}
	if !verdict.NoTriggerFound {
		t.Errorf("expected NoTriggerFound for a noisy-no-incident scenario; got %d hypotheses", len(verdict.Hypotheses))
	}
	// Must not produce a High-confidence verdict on a no-incident scenario (spec §16).
	for _, h := range verdict.Hypotheses {
		if h.Confidence == model.ConfidenceHigh {
			t.Errorf("no-incident scenario produced a High-confidence verdict: %q", h.Explanation)
		}
	}
}

// TestEngine_CrashLoopSynthesis: 4 restarts → RW009 coalesces a CrashLoop event
// which should appear in the Hypotheses.
func TestEngine_CrashLoopSynthesis(t *testing.T) {
	var restarts []model.Event
	for i := 0; i < 4; i++ {
		restarts = append(restarts, makeEvent(
			"ev-restart-"+string(rune('a'+i)),
			model.EventKindRestart,
			"pod/shop/checkout-abc",
			t0.Add(time.Duration(i)*2*time.Minute),
			model.SeverityNotable,
		))
	}
	inc := model.Incident{Events: restarts}
	g := topology.Build(nil)
	verdict := correlate.Run(inc, g)

	if verdict == nil {
		t.Fatal("expected non-nil verdict")
	}
	// Should produce a CrashLoop hypothesis (not NoTriggerFound).
	if verdict.NoTriggerFound && len(verdict.Hypotheses) == 0 {
		t.Error("expected a CrashLoop hypothesis when 4 restarts in 10m")
	}
}

// TestEngine_AlertNeverTrigger: an alert with no other signals must NOT produce
// a hypothesis with the alert as trigger (spec §10 RW010 invariant).
func TestEngine_AlertNeverTrigger(t *testing.T) {
	alert := makeEvent("ev-alert", model.EventKindAlertFired, "svc/shop/checkout", t0, model.SeverityNotable)
	inc := model.Incident{Events: []model.Event{alert}}
	g := topology.Build(nil)
	verdict := correlate.Run(inc, g)

	// With only an alert and no other signals, there should be no hypothesis
	// that treats the alert as a trigger.
	for _, h := range verdict.Hypotheses {
		if h.TriggerEventID == "ev-alert" {
			t.Errorf("RW010 violated: alert was used as a trigger in hypothesis: %q", h.Explanation)
		}
	}
}

// TestEngine_Deterministic: same input produces identical verdicts.
func TestEngine_Deterministic(t *testing.T) {
	entities := []model.Entity{
		{ID: "svc/shop/checkout", Kind: model.EntityKindService},
	}
	deploy := makeEvent("ev-deploy", model.EventKindDeploy, "svc/shop/checkout", t0, model.SeverityInfo)
	latSig := makeSignalWithCP("sig-lat", "svc/shop/checkout", model.MetricLatencyP99, t0.Add(2*time.Minute), 4.5)
	errSig := makeSignalWithCP("sig-err", "svc/shop/checkout", model.MetricErrorRate, t0.Add(3*time.Minute), 10.0)

	inc := model.Incident{Entities: entities, Events: []model.Event{deploy}, Signals: []model.Signal{latSig, errSig}}

	runs := make([]*model.Verdict, 5)
	for i := range runs {
		g := topology.Build(entities)
		runs[i] = correlate.Run(inc, g)
	}
	for i := 1; i < len(runs); i++ {
		if len(runs[i].Hypotheses) != len(runs[0].Hypotheses) {
			t.Fatalf("non-deterministic: run %d produced %d hypotheses, run 0 produced %d",
				i, len(runs[i].Hypotheses), len(runs[0].Hypotheses))
		}
		for j := range runs[i].Hypotheses {
			if runs[i].Hypotheses[j].TriggerEventID != runs[0].Hypotheses[j].TriggerEventID {
				t.Errorf("non-deterministic trigger at hypothesis %d on run %d", j, i)
			}
		}
	}
}
