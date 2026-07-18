package changepoint_test

import (
	"math"
	"testing"
	"time"

	"github.com/paultanay/rewind/internal/analyze/changepoint"
	"github.com/paultanay/rewind/internal/model"
)

// ─── Series builders ──────────────────────────────────────────────────────────

var base = time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)
var step = time.Minute

func makePts(values []float64) []model.Point {
	pts := make([]model.Point, len(values))
	for i, v := range values {
		pts[i] = model.Point{T: base.Add(time.Duration(i) * step), V: v}
	}
	return pts
}

// constantSeries returns n points all equal to v.
func constantSeries(v float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// stepSeries returns n1 points at v1 then n2 points at v2.
func stepSeries(v1 float64, n1 int, v2 float64, n2 int) []float64 {
	s := make([]float64, n1+n2)
	for i := 0; i < n1; i++ {
		s[i] = v1
	}
	for i := n1; i < n1+n2; i++ {
		s[i] = v2
	}
	return s
}

// noisySeries adds uniform noise in [-noise, +noise] to a base value.
// Uses a deterministic LCG (no crypto/rand) so tests are reproducible.
func noisySeries(base float64, n int, noise float64) []float64 {
	s := make([]float64, n)
	// LCG: x = (1664525*x + 1013904223) mod 2^32
	var seed uint32 = 12345
	for i := range s {
		seed = 1664525*seed + 1013904223
		f := float64(seed)/float64(math.MaxUint32)*2 - 1 // [-1, 1]
		s[i] = base + f*noise
	}
	return s
}

// ─── BaselineDeviation tests ──────────────────────────────────────────────────

func TestBaselineDeviation_StepUp(t *testing.T) {
	t.Parallel()
	// 20 points at ~40ms, then 20 points at ~200ms — clear step up.
	baseline := makePts(noisySeries(40, 30, 2))
	incident := makePts(stepSeries(40, 10, 200, 10))

	d := &changepoint.BaselineDeviation{}
	cps := d.Detect(incident, baseline)

	if len(cps) == 0 {
		t.Fatal("expected at least one change-point for step-up series, got none")
	}
	if cps[0].Direction != model.DirectionUp {
		t.Errorf("direction = %v, want Up", cps[0].Direction)
	}
	if cps[0].Magnitude < 2.0 {
		t.Errorf("magnitude = %.2f, want ≥ 2.0 (roughly 200/40)", cps[0].Magnitude)
	}
}

func TestBaselineDeviation_StepDown(t *testing.T) {
	t.Parallel()
	baseline := makePts(noisySeries(100, 30, 3))
	incident := makePts(stepSeries(100, 10, 10, 10))

	d := &changepoint.BaselineDeviation{}
	cps := d.Detect(incident, baseline)

	if len(cps) == 0 {
		t.Fatal("expected change-point for step-down series")
	}
	if cps[0].Direction != model.DirectionDown {
		t.Errorf("direction = %v, want Down", cps[0].Direction)
	}
}

func TestBaselineDeviation_ConstantSeries(t *testing.T) {
	t.Parallel()
	// Perfectly constant series — MAD is 0, nothing can deviate.
	baseline := makePts(constantSeries(50, 20))
	incident := makePts(constantSeries(50, 20))

	d := &changepoint.BaselineDeviation{}
	cps := d.Detect(incident, baseline)

	if len(cps) != 0 {
		t.Errorf("constant series should produce 0 change-points, got %d", len(cps))
	}
}

func TestBaselineDeviation_NoisyNoIncident(t *testing.T) {
	t.Parallel()
	// Noisy but stable — should not fire at k=5.
	baseline := makePts(noisySeries(50, 30, 5))
	incident := makePts(noisySeries(50, 20, 5))

	d := &changepoint.BaselineDeviation{K: 5}
	cps := d.Detect(incident, baseline)

	// May fire 0 or 1 — the series is in the same distribution as baseline.
	// We assert no more than 1 false positive with k=5.
	if len(cps) > 1 {
		t.Errorf("stable noisy series should not produce >1 change-point at k=5, got %d", len(cps))
	}
}

func TestBaselineDeviation_AllNaN(t *testing.T) {
	t.Parallel()
	pts := []model.Point{
		{T: base, V: math.NaN()},
		{T: base.Add(step), V: math.NaN()},
	}
	d := &changepoint.BaselineDeviation{}
	cps := d.Detect(pts, nil)
	if len(cps) != 0 {
		t.Errorf("all-NaN series should produce 0 change-points, got %d", len(cps))
	}
}

func TestBaselineDeviation_NoBaseline(t *testing.T) {
	t.Parallel()
	// No baseline provided — should use self-baseline (pts itself).
	// A step series should still be detected.
	incident := makePts(stepSeries(40, 10, 200, 10))
	d := &changepoint.BaselineDeviation{}
	cps := d.Detect(incident, nil)
	// With self-baseline the detector may or may not fire (depends on MAD of
	// the mixed series). We just ensure no panic.
	_ = cps
}

func TestBaselineDeviation_GapInSeries(t *testing.T) {
	t.Parallel()
	// NaN in the middle breaks a run.
	vals := []float64{40, 40, 40, math.NaN(), 200, 200, 200, 200, 200}
	pts := makePts(vals)
	baseline := makePts(noisySeries(40, 20, 2))

	d := &changepoint.BaselineDeviation{}
	cps := d.Detect(pts, baseline)
	// Expect a change-point in the 200-segment.
	if len(cps) == 0 {
		t.Fatal("expected change-point after gap, got none")
	}
}

func TestBaselineDeviation_Reproducible(t *testing.T) {
	t.Parallel()
	baseline := makePts(noisySeries(40, 30, 2))
	incident := makePts(stepSeries(40, 10, 200, 10))
	d := &changepoint.BaselineDeviation{}

	cps1 := d.Detect(incident, baseline)
	cps2 := d.Detect(incident, baseline)

	if len(cps1) != len(cps2) {
		t.Fatalf("non-reproducible: got %d then %d change-points", len(cps1), len(cps2))
	}
	for i := range cps1 {
		if !cps1[i].At.Equal(cps2[i].At) || cps1[i].Score != cps2[i].Score {
			t.Errorf("non-reproducible at index %d", i)
		}
	}
}

// ─── PELT tests ───────────────────────────────────────────────────────────────

func TestPELT_StepUp(t *testing.T) {
	t.Parallel()
	pts := makePts(stepSeries(10, 20, 80, 20))
	d := &changepoint.PELT{}
	cps := d.Detect(pts, nil)

	if len(cps) == 0 {
		t.Fatal("PELT: expected change-point for step-up series, got none")
	}
	if cps[0].Direction != model.DirectionUp {
		t.Errorf("PELT direction = %v, want Up", cps[0].Direction)
	}
	// Change-point should be near the step at index 20.
	stepTime := base.Add(20 * step)
	diff := cps[0].At.Sub(stepTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > 3*step {
		t.Errorf("PELT detected step at %v, expected near %v (diff %v)", cps[0].At, stepTime, diff)
	}
}

func TestPELT_StepDown(t *testing.T) {
	t.Parallel()
	pts := makePts(stepSeries(80, 20, 10, 20))
	d := &changepoint.PELT{}
	cps := d.Detect(pts, nil)

	if len(cps) == 0 {
		t.Fatal("PELT: expected change-point for step-down series")
	}
	if cps[0].Direction != model.DirectionDown {
		t.Errorf("PELT direction = %v, want Down", cps[0].Direction)
	}
}

func TestPELT_NoiseOnly(t *testing.T) {
	t.Parallel()
	// Pure noise — PELT should not find a meaningful change-point.
	pts := makePts(noisySeries(50, 40, 3))
	d := &changepoint.PELT{Penalty: 5 * math.Log(float64(40))}
	cps := d.Detect(pts, nil)
	// At a generous penalty, noise-only series should produce ≤1 false positive.
	if len(cps) > 1 {
		t.Errorf("noise-only series produced %d change-points, expected ≤1", len(cps))
	}
}

func TestPELT_TooShort(t *testing.T) {
	t.Parallel()
	pts := makePts([]float64{1, 2, 3})
	d := &changepoint.PELT{}
	cps := d.Detect(pts, nil)
	if len(cps) != 0 {
		t.Errorf("too-short series should produce 0 change-points, got %d", len(cps))
	}
}

func TestPELT_Spike(t *testing.T) {
	t.Parallel()
	// A spike: 20 low, 2 high, 20 low — single spike, not a sustained shift.
	vals := make([]float64, 42)
	for i := range vals {
		vals[i] = 10
	}
	vals[20] = 500
	vals[21] = 480
	pts := makePts(vals)

	d := &changepoint.PELT{}
	cps := d.Detect(pts, nil)
	// Two change-points expected (up at 20, down at 22), or possibly 0 if
	// the spike is too short vs minSegLen. Either is acceptable — we just
	// verify no panic.
	_ = cps
}

func TestPELT_Reproducible(t *testing.T) {
	t.Parallel()
	pts := makePts(stepSeries(10, 20, 80, 20))
	d := &changepoint.PELT{}
	cps1 := d.Detect(pts, nil)
	cps2 := d.Detect(pts, nil)

	if len(cps1) != len(cps2) {
		t.Fatalf("PELT non-reproducible: %d vs %d", len(cps1), len(cps2))
	}
	for i := range cps1 {
		if !cps1[i].At.Equal(cps2[i].At) {
			t.Errorf("PELT non-reproducible at index %d", i)
		}
	}
}

// ─── Combine and MergeAndCap tests ───────────────────────────────────────────

func TestCombine_Cap5(t *testing.T) {
	t.Parallel()
	// A series with many steps — combined output should be capped at 5.
	vals := make([]float64, 80)
	levels := []float64{10, 100, 10, 100, 10, 100, 10, 100}
	for i, v := range vals {
		_ = v
		vals[i] = levels[(i/10)%len(levels)]
	}
	pts := makePts(vals)

	detectors := []changepoint.Detector{
		&changepoint.BaselineDeviation{},
		&changepoint.PELT{},
	}
	stepNs := int64(step)
	cps := changepoint.Combine(detectors, pts, nil, stepNs)
	if len(cps) > 5 {
		t.Errorf("Combine should cap at 5 change-points, got %d", len(cps))
	}
}

func TestCombine_Sorted(t *testing.T) {
	t.Parallel()
	pts := makePts(stepSeries(10, 20, 80, 20))
	detectors := []changepoint.Detector{
		&changepoint.BaselineDeviation{},
		&changepoint.PELT{},
	}
	cps := changepoint.Combine(detectors, pts, nil, int64(step))
	for i := 1; i < len(cps); i++ {
		if cps[i].At.Before(cps[i-1].At) {
			t.Errorf("combined change-points not sorted by time at index %d", i)
		}
	}
}

func TestMergeAndCap_Collapse(t *testing.T) {
	t.Parallel()
	// Two change-points 30s apart with step=60s → merge window = 120s.
	// They should collapse to the higher-score one.
	t1 := base
	t2 := base.Add(30 * time.Second)
	cps := []model.ChangePoint{
		{At: t1, Score: 0.6, Direction: model.DirectionUp, DetectorID: "a"},
		{At: t2, Score: 0.9, Direction: model.DirectionUp, DetectorID: "b"},
	}
	merged := changepoint.MergeAndCap(cps, int64(120*time.Second), 5)
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged change-point, got %d", len(merged))
	}
	if merged[0].Score != 0.9 {
		t.Errorf("merged should keep higher score 0.9, got %.2f", merged[0].Score)
	}
}

func TestMergeAndCap_NoDuplicateElision(t *testing.T) {
	t.Parallel()
	// Two change-points far apart should NOT be collapsed.
	t1 := base
	t2 := base.Add(10 * time.Minute)
	cps := []model.ChangePoint{
		{At: t1, Score: 0.8, Direction: model.DirectionUp},
		{At: t2, Score: 0.7, Direction: model.DirectionUp},
	}
	merged := changepoint.MergeAndCap(cps, int64(2*time.Minute), 5)
	if len(merged) != 2 {
		t.Fatalf("expected 2 separate change-points, got %d", len(merged))
	}
}
