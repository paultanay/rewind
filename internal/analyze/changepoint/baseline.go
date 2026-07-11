package changepoint

import (
	"math"

	"github.com/rewind-io/rewind/internal/model"
)

// BaselineDeviation implements the median+MAD detector described in spec §9.
//
// Algorithm:
//  1. Compute median and MAD of the baseline window values.
//  2. Sweep the incident window; a point is "flagged" when |v − median| > k·MAD.
//  3. A change-point fires when ≥ minRun consecutive flagged points are seen.
//  4. The change-point timestamp is the start of the run.
//  5. Score = min(1.0, max_deviation / (k·MAD)) normalised.
//
// Edge cases handled:
//   - Empty or all-NaN baseline: threshold computed from incident window itself
//     (self-baseline). This handles new series that begin mid-window.
//   - Constant series (MAD=0): no change-points (nothing to deviate from).
//   - NaN / Inf in incident window: treated as gaps, don't contribute to runs.
//   - Counter resets: handled by validValues in detector.go.
type BaselineDeviation struct {
	// K is the MAD multiplier (default 5.0). Higher = less sensitive.
	K float64
	// MinRun is the minimum consecutive flagged points to fire (default 3).
	MinRun int
}

const detectorIDBaseline = "baseline-deviation"

// ID implements Detector.
func (b *BaselineDeviation) ID() string { return detectorIDBaseline }

// defaults fills zero-value fields with the spec-recommended defaults.
func (b *BaselineDeviation) defaults() (k float64, minRun int) {
	k = b.K
	if k <= 0 {
		k = 5.0
	}
	minRun = b.MinRun
	if minRun <= 0 {
		minRun = 3
	}
	return k, minRun
}

// Detect implements Detector.
func (b *BaselineDeviation) Detect(pts, baseline []model.Point) []model.ChangePoint {
	if len(pts) == 0 {
		return nil
	}
	k, minRun := b.defaults()

	// Build the reference distribution from baseline; fall back to pts itself
	// (self-baseline) when baseline is absent or too short.
	refVals := validValues(baseline)
	if len(refVals) < 5 {
		refVals = validValues(pts)
	}
	if len(refVals) == 0 {
		return nil
	}

	med, madVal := mad(refVals)
	if math.IsNaN(med) || math.IsNaN(madVal) {
		return nil
	}

	// Constant series: MAD == 0 means nothing can deviate from it in a
	// meaningful way. Return no change-points rather than infinite scores.
	if madVal < 1e-12 {
		return nil
	}

	threshold := k * madVal

	type runState struct {
		start   int // index into pts where the run began
		maxDev  float64
		maxIdx  int
		isAbove bool // true = values above median, false = below
	}

	var results []model.ChangePoint
	var run *runState

	for i, p := range pts {
		v := p.V
		if math.IsNaN(v) || math.IsInf(v, 0) {
			// Gap breaks any active run.
			if run != nil && i-run.start >= minRun {
				results = append(results, b.makeCP(pts, run.start, run.maxDev, threshold, madVal, run.isAbove))
			}
			run = nil
			continue
		}

		dev := v - med
		absDev := math.Abs(dev)
		above := dev > 0

		if absDev > threshold {
			if run == nil {
				run = &runState{start: i, isAbove: above}
			} else if above != run.isAbove {
				// Direction flip mid-run: close current run and start a new one.
				if i-run.start >= minRun {
					results = append(results, b.makeCP(pts, run.start, run.maxDev, threshold, madVal, run.isAbove))
				}
				run = &runState{start: i, isAbove: above}
			}
			if absDev > run.maxDev {
				run.maxDev = absDev
				run.maxIdx = i
			}
		} else {
			// Back inside threshold: close run if long enough.
			if run != nil && i-run.start >= minRun {
				results = append(results, b.makeCP(pts, run.start, run.maxDev, threshold, madVal, run.isAbove))
			}
			run = nil
		}
	}
	// Handle run that extends to end of series.
	if run != nil && len(pts)-run.start >= minRun {
		results = append(results, b.makeCP(pts, run.start, run.maxDev, threshold, madVal, run.isAbove))
	}

	return results
}

func (b *BaselineDeviation) makeCP(
	pts []model.Point,
	startIdx int,
	maxDev, threshold, madVal float64,
	above bool,
) model.ChangePoint {
	// Estimate before/after means for magnitude.
	beforeVals := validValues(pts[:startIdx])
	afterVals := validValues(pts[startIdx:])
	beforeMean := mean(beforeVals)
	afterMean := mean(afterVals)
	if math.IsNaN(beforeMean) {
		beforeMean = 0
	}

	dir := model.DirectionUp
	if !above {
		dir = model.DirectionDown
	}

	// Score: how many MADs above threshold are we? Clamped to [0,1].
	score := math.Min(1.0, maxDev/threshold)

	return model.ChangePoint{
		At:         pts[startIdx].T,
		Direction:  dir,
		Magnitude:  magnitude(beforeMean, afterMean),
		Score:      score,
		DetectorID: detectorIDBaseline,
	}
}
