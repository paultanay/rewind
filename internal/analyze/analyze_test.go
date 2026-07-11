package analyze_test

import (
	"math"
	"testing"
	"time"

	"github.com/rewind-io/rewind/internal/analyze"
	"github.com/rewind-io/rewind/internal/model"
)

var (
	testBase = time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	testStep = time.Minute
)

func makePts(values []float64, start time.Time, step time.Duration) []model.Point {
	pts := make([]model.Point, len(values))
	for i, v := range values {
		pts[i] = model.Point{T: start.Add(time.Duration(i) * step), V: v}
	}
	return pts
}

// stepValues returns n1 copies of v1 followed by n2 copies of v2.
func stepValues(v1 float64, n1 int, v2 float64, n2 int) []float64 {
	vals := make([]float64, n1+n2)
	for i := 0; i < n1; i++ {
		vals[i] = v1
	}
	for i := n1; i < n1+n2; i++ {
		vals[i] = v2
	}
	return vals
}

// noiseValues returns n points near base with small uniform noise (deterministic).
func noiseValues(base float64, n int, noise float64) []float64 {
	vals := make([]float64, n)
	var seed uint32 = 42
	for i := range vals {
		seed = 1664525*seed + 1013904223
		f := float64(seed)/math.MaxUint32*2 - 1
		vals[i] = base + f*noise
	}
	return vals
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestRun_ChangePointsDetected(t *testing.T) {
	t.Parallel()
	// Build an incident with a latency signal that has a clear step-up.
	baseline := makePts(noiseValues(40, 30, 2), testBase.Add(-30*testStep), testStep)
	incident := makePts(stepValues(40, 10, 200, 15), testBase, testStep)

	inc := model.Incident{
		ID:     "test-001",
		Window: model.TimeRange{From: testBase, To: testBase.Add(25 * testStep)},
		Signals: []model.Signal{
			{
				ID:       "sig-1",
				EntityID: "svc/shop/checkout",
				Metric:   model.MetricLatencyP99,
				Unit:     "ms",
				Points:   incident,
				Baseline: baseline,
			},
		},
	}

	result := analyze.Run(inc)

	if len(result.Signals) == 0 {
		t.Fatal("expected signals after Run, got none")
	}
	sig := result.Signals[0]
	if len(sig.ChangePoints) == 0 {
		t.Fatal("expected at least one change-point on latency step-up signal, got none")
	}
	cp := sig.ChangePoints[0]
	if cp.Direction != model.DirectionUp {
		t.Errorf("change-point direction = %v, want Up", cp.Direction)
	}
	if cp.Magnitude < 2.0 {
		t.Errorf("change-point magnitude = %.2f, want ≥ 2.0", cp.Magnitude)
	}
	if cp.Score <= 0 || cp.Score > 1 {
		t.Errorf("change-point score = %.3f, want (0,1]", cp.Score)
	}
}

func TestRun_NoisySignal_NoFalsePositives(t *testing.T) {
	t.Parallel()
	// Stable noisy signal — should produce no high-confidence change-points.
	baseline := makePts(noiseValues(50, 30, 4), testBase.Add(-30*testStep), testStep)
	incident := makePts(noiseValues(50, 20, 4), testBase, testStep)

	inc := model.Incident{
		ID:     "test-002",
		Window: model.TimeRange{From: testBase, To: testBase.Add(20 * testStep)},
		Signals: []model.Signal{
			{
				ID:       "sig-1",
				EntityID: "svc/shop/frontend",
				Metric:   model.MetricLatencyP99,
				Points:   incident,
				Baseline: baseline,
			},
		},
	}

	result := analyze.Run(inc)
	if len(result.Signals) == 0 {
		t.Fatal("signals missing after Run")
	}
	// At k=5 MAD threshold, stable noise should not produce change-points.
	for _, cp := range result.Signals[0].ChangePoints {
		if cp.Score > 0.8 {
			t.Errorf("high-confidence change-point (score %.2f) on stable noise — false positive", cp.Score)
		}
	}
}

func TestRun_Cap5ChangePoints(t *testing.T) {
	t.Parallel()
	// 8 alternating steps → many potential change-points; should be capped at 5.
	vals := make([]float64, 80)
	levels := []float64{10, 100, 10, 100, 10, 100, 10, 100}
	for i := range vals {
		vals[i] = levels[(i/10)%len(levels)]
	}
	pts := makePts(vals, testBase, testStep)

	inc := model.Incident{
		ID:     "test-003",
		Window: model.TimeRange{From: testBase, To: testBase.Add(80 * testStep)},
		Signals: []model.Signal{
			{ID: "sig-1", EntityID: "svc/x", Metric: model.MetricCPUUsage, Points: pts},
		},
	}

	result := analyze.Run(inc)
	if len(result.Signals[0].ChangePoints) > 5 {
		t.Errorf("cap violated: got %d change-points, want ≤5", len(result.Signals[0].ChangePoints))
	}
}

func TestRun_EmptySignals(t *testing.T) {
	t.Parallel()
	// Incident with no signals — Run must not panic.
	inc := model.Incident{
		ID:     "test-004",
		Window: model.TimeRange{From: testBase, To: testBase.Add(time.Hour)},
	}
	result := analyze.Run(inc)
	if len(result.Signals) != 0 {
		t.Errorf("expected 0 signals, got %d", len(result.Signals))
	}
}

func TestRun_MultipleSignals(t *testing.T) {
	t.Parallel()
	baseline := makePts(noiseValues(40, 20, 2), testBase.Add(-20*testStep), testStep)

	// latency: step up at index 10
	latency := makePts(stepValues(40, 10, 200, 15), testBase, testStep)
	// error rate: step up at index 12
	errorRate := makePts(stepValues(0.01, 12, 0.15, 13), testBase, testStep)
	// CPU: stable — use very small noise (0.5% of base) so neither detector fires
	cpu := makePts(noiseValues(30, 25, 0.1), testBase, testStep)

	inc := model.Incident{
		ID:     "test-005",
		Window: model.TimeRange{From: testBase, To: testBase.Add(25 * testStep)},
		Signals: []model.Signal{
			{ID: "sig-1", EntityID: "svc/shop/checkout", Metric: model.MetricLatencyP99, Points: latency, Baseline: baseline},
			{ID: "sig-2", EntityID: "svc/shop/checkout", Metric: model.MetricErrorRate, Points: errorRate, Baseline: baseline},
			{ID: "sig-3", EntityID: "svc/shop/checkout", Metric: model.MetricCPUUsage, Points: cpu, Baseline: baseline},
		},
	}

	result := analyze.Run(inc)
	if len(result.Signals) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(result.Signals))
	}

	// latency and errorRate should have change-points; cpu should not.
	latencyCPs := result.Signals[0].ChangePoints
	errorCPs := result.Signals[1].ChangePoints
	cpuCPs := result.Signals[2].ChangePoints

	if len(latencyCPs) == 0 {
		t.Error("latency signal: expected change-point, got none")
	}
	if len(errorCPs) == 0 {
		t.Error("error.rate signal: expected change-point, got none")
	}
	if len(cpuCPs) > 1 {
		t.Errorf("cpu signal: expected ≤1 change-point on stable noise, got %d", len(cpuCPs))
	}
}

func TestRun_Reproducible(t *testing.T) {
	t.Parallel()
	baseline := makePts(noiseValues(40, 30, 2), testBase.Add(-30*testStep), testStep)
	incident := makePts(stepValues(40, 10, 200, 15), testBase, testStep)
	inc := model.Incident{
		ID:     "test-rep",
		Window: model.TimeRange{From: testBase, To: testBase.Add(25 * testStep)},
		Signals: []model.Signal{
			{ID: "sig-1", EntityID: "svc/shop/checkout", Metric: model.MetricLatencyP99, Points: incident, Baseline: baseline},
		},
	}
	r1 := analyze.Run(inc)
	r2 := analyze.Run(inc)

	cps1 := r1.Signals[0].ChangePoints
	cps2 := r2.Signals[0].ChangePoints
	if len(cps1) != len(cps2) {
		t.Fatalf("non-reproducible: %d vs %d change-points", len(cps1), len(cps2))
	}
	for i := range cps1 {
		if !cps1[i].At.Equal(cps2[i].At) || cps1[i].Score != cps2[i].Score {
			t.Errorf("non-reproducible at index %d", i)
		}
	}
}
