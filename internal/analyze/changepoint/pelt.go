package changepoint

import (
	"math"

	"github.com/paultanay/rewind/internal/model"
)

// PELT implements the Pruned Exact Linear Time change-point detection
// algorithm with a normal-mean (Gaussian) cost function, as described in:
//
//	Killick, R., Fearnhead, P., & Eckley, I. A. (2012). Optimal detection of
//	changepoints with a linear computational cost. JASA, 107(500), 1590-1598.
//
// The algorithm finds the globally optimal segmentation minimising:
//
//	sum_i [ cost(segment_i) ] + n_segments × penalty
//
// where cost(segment) = −2 × log-likelihood under a Gaussian model with
// unknown mean and known variance estimated from the whole series.
//
// Implementation is pure Go with zero external dependencies.
// Correctness is verified by tests against synthetic series (step, ramp,
// spike, noise-only) in pelt_test.go.
type PELT struct {
	// Penalty is the per-changepoint cost added to the objective.
	// If ≤0, a data-adaptive BIC penalty (2·ln(n)) is used.
	Penalty float64
	// MinSegLen is the minimum segment length in samples (default 3).
	// Prevents spurious micro-segments.
	MinSegLen int
}

const detectorIDPELT = "pelt"

// ID implements Detector.
func (p *PELT) ID() string { return detectorIDPELT }

// Detect implements Detector.
func (p *PELT) Detect(pts, _ []model.Point) []model.ChangePoint {
	// PELT operates only on the incident window; baseline is unused.
	vals := extractValues(pts)
	if len(vals) < 6 {
		// Too few points to reliably detect anything.
		return nil
	}

	minSeg := p.MinSegLen
	if minSeg <= 0 {
		minSeg = 3
	}

	n := len(vals)
	penalty := p.Penalty
	if penalty <= 0 {
		penalty = 2 * math.Log(float64(n)) // BIC
	}

	// Precompute prefix sums and prefix sum-of-squares for O(1) segment cost.
	// cost(s, e) = negative log-likelihood of segment vals[s:e] under Gaussian
	//            = (e-s) * log(variance(s,e)) + (e-s)   [up to constant]
	// We use the identity: variance = (sumSq - sum²/n) / n.
	prefixSum := make([]float64, n+1)
	prefixSumSq := make([]float64, n+1)
	for i, v := range vals {
		prefixSum[i+1] = prefixSum[i] + v
		prefixSumSq[i+1] = prefixSumSq[i] + v*v
	}

	// segCost returns the cost of segment [s, e) (exclusive end).
	segCost := func(s, e int) float64 {
		length := float64(e - s)
		if length < 1 {
			return 0
		}
		sum := prefixSum[e] - prefixSum[s]
		sumSq := prefixSumSq[e] - prefixSumSq[s]
		// Population variance of this segment.
		v := sumSq/length - (sum/length)*(sum/length)
		if v < 1e-10 {
			// Near-constant segment: cost is near zero.
			return 0
		}
		// Gaussian negative log-likelihood (constant terms omitted since they
		// cancel in the comparison): n/2 * ln(variance).
		return length * math.Log(v)
	}

	// F[t] = minimum cost of segmenting vals[0:t].
	// prev[t] = last change-point position before t in the optimal segmentation.
	F := make([]float64, n+1)
	prev := make([]int, n+1)
	for i := range F {
		F[i] = math.Inf(1)
		prev[i] = -1
	}
	F[0] = -penalty

	// Candidate set (the pruning step): set of t values still worth considering
	// as the start of the current segment.
	candidates := []int{0}

	for tStar := minSeg; tStar <= n; tStar++ {
		// Find the candidate that minimises F[t] + cost(t, tStar) + penalty.
		bestCost := math.Inf(1)
		bestT := -1
		for _, t := range candidates {
			if tStar-t < minSeg {
				continue
			}
			c := F[t] + segCost(t, tStar) + penalty
			if c < bestCost {
				bestCost = c
				bestT = t
			}
		}
		F[tStar] = bestCost
		prev[tStar] = bestT

		// PELT pruning: remove any candidate t where
		// F[t] + cost(t, tStar) + penalty > F[tStar] + penalty
		// Such candidates can never be optimal for any future tStar' > tStar.
		pruned := candidates[:0]
		for _, t := range candidates {
			if F[t]+segCost(t, tStar) <= F[tStar]+penalty {
				pruned = append(pruned, t)
			}
		}
		candidates = append(pruned, tStar)
	}

	// Backtrack to find change-point positions.
	var cpPositions []int
	at := n
	for {
		p2 := prev[at]
		if p2 <= 0 {
			break
		}
		if p2 > 0 {
			cpPositions = append(cpPositions, p2)
		}
		at = p2
	}

	if len(cpPositions) == 0 {
		return nil
	}

	// Convert positions to model.ChangePoint values.
	// A position p means a change between vals[p-1] and vals[p].
	var results []model.ChangePoint
	for _, pos := range cpPositions {
		if pos <= 0 || pos >= len(pts) {
			continue
		}

		beforeVals := extractValues(pts[:pos])
		afterVals := extractValues(pts[pos:])
		beforeMean := mean(beforeVals)
		afterMean := mean(afterVals)

		if math.IsNaN(beforeMean) || math.IsNaN(afterMean) {
			continue
		}

		// Score: based on normalised mean difference vs combined std-dev.
		combinedVar := (variance(beforeVals)*float64(len(beforeVals)) +
			variance(afterVals)*float64(len(afterVals))) /
			float64(len(beforeVals)+len(afterVals))
		combinedStd := math.Sqrt(combinedVar)
		var score float64
		if combinedStd > 1e-9 {
			diff := math.Abs(afterMean - beforeMean)
			score = math.Min(1.0, diff/(3*combinedStd))
		} else {
			score = 0.5
		}

		results = append(results, model.ChangePoint{
			At:         pts[pos].T,
			Direction:  direction(beforeMean, afterMean),
			Magnitude:  magnitude(beforeMean, afterMean),
			Score:      score,
			DetectorID: detectorIDPELT,
		})
	}
	return results
}

// extractValues extracts finite values from pts (without counter-reset
// filtering — PELT works on rate/gauge signals where resets are not expected;
// the caller should pre-process counter resets before calling).
func extractValues(pts []model.Point) []float64 {
	out := make([]float64, 0, len(pts))
	for _, p := range pts {
		if !math.IsNaN(p.V) && !math.IsInf(p.V, 0) {
			out = append(out, p.V)
		}
	}
	return out
}
