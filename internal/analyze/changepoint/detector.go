// Package changepoint implements two change-point detectors that together
// identify statistically significant inflections in time-series Signals:
//
//  1. BaselineDeviation — median + MAD over a reference baseline window;
//     flags sustained excursions beyond k·MAD lasting ≥3 consecutive points.
//
//  2. PELT — Pruned Exact Linear Time algorithm with a normal-mean cost
//     function; detects abrupt mean shifts within the incident window itself.
//
// Both detectors implement the Detector interface. The Combine function runs
// both and merges results: nearby change-points collapse to the strongest,
// capped at 5 per signal.
//
// Reproducibility guarantee: given identical inputs both detectors always
// produce identical outputs (no randomness, no time.Now() calls).
package changepoint

import (
	"math"
	"sort"

	"github.com/rewind-io/rewind/internal/model"
)

// Detector is the common interface for all change-point algorithms.
// Implementations must be stateless and safe for concurrent use.
type Detector interface {
	// ID returns the stable identifier used in ChangePoint.DetectorID.
	ID() string
	// Detect analyses pts (the incident window) optionally against baseline
	// (the reference window before the incident). It returns zero or more
	// ChangePoints, sorted by time.
	//
	// pts and baseline must both be sorted ascending by time.
	// Neither slice is modified.
	Detect(pts, baseline []model.Point) []model.ChangePoint
}

// ─── Shared statistics helpers ────────────────────────────────────────────────

// validValues extracts finite (non-NaN, non-Inf) values from pts.
// Monotone-reset detection: if we see a counter that jumps down by more than
// 10% of its previous value, we treat the reset as a NaN (gap) rather than
// a real down-spike — otherwise a Prometheus counter reset would look like a
// huge negative change-point.
func validValues(pts []model.Point) []float64 {
	out := make([]float64, 0, len(pts))
	prev := math.NaN()
	for _, p := range pts {
		v := p.V
		if math.IsNaN(v) || math.IsInf(v, 0) {
			prev = math.NaN()
			continue
		}
		// Counter reset heuristic: value dropped >10% vs previous non-NaN.
		if !math.IsNaN(prev) && v < prev*0.90 && prev > 0 {
			// Treat as a gap — skip this point but keep prev for next comparison.
			prev = math.NaN()
			continue
		}
		out = append(out, v)
		prev = v
	}
	return out
}

// median returns the median of a sorted slice. Panics if empty.
func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return math.NaN()
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// mad returns the Median Absolute Deviation of vals (unsorted input).
// MAD = median(|xi − median(x)|).
func mad(vals []float64) (med, madVal float64) {
	if len(vals) == 0 {
		return math.NaN(), math.NaN()
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	med = median(sorted)

	devs := make([]float64, len(vals))
	for i, v := range vals {
		devs[i] = math.Abs(v - med)
	}
	sort.Float64s(devs)
	madVal = median(devs)
	return med, madVal
}

// mean returns the arithmetic mean of vals.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return math.NaN()
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// variance returns the population variance of vals.
func variance(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	m := mean(vals)
	sum := 0.0
	for _, v := range vals {
		d := v - m
		sum += d * d
	}
	return sum / float64(len(vals))
}

// magnitude computes the ratio of after-mean to before-mean.
// Returns 1.0 when before is zero to avoid division by zero.
func magnitude(before, after float64) float64 {
	if math.IsNaN(before) || math.IsNaN(after) {
		return 1.0
	}
	if math.Abs(before) < 1e-9 {
		if math.Abs(after) < 1e-9 {
			return 1.0
		}
		// Before ≈ 0, after ≠ 0 — use a large but finite sentinel.
		return 10.0
	}
	return math.Abs(after / before)
}

// direction determines whether after > before.
func direction(before, after float64) model.Direction {
	if after > before {
		return model.DirectionUp
	}
	if after < before {
		return model.DirectionDown
	}
	return model.DirectionUnknown
}

// ─── Merge / dedup ────────────────────────────────────────────────────────────

// MergeAndCap merges change-points from multiple detectors, collapses those
// within mergeWindowNs of each other (keeping the highest score), and returns
// at most maxKeep, sorted by time.
//
// mergeWindowNs should be set to 2× the signal step in nanoseconds.
func MergeAndCap(cps []model.ChangePoint, mergeWindowNs int64, maxKeep int) []model.ChangePoint {
	if len(cps) == 0 {
		return nil
	}

	// Sort by time ascending.
	sort.Slice(cps, func(i, j int) bool {
		return cps[i].At.Before(cps[j].At)
	})

	// Collapse nearby change-points: sweep through keeping a "current cluster"
	// and always promoting the highest-score member.
	merged := make([]model.ChangePoint, 0, len(cps))
	current := cps[0]
	for i := 1; i < len(cps); i++ {
		gap := cps[i].At.UnixNano() - current.At.UnixNano()
		if gap < 0 {
			gap = -gap
		}
		if gap <= mergeWindowNs {
			// Same cluster: keep the stronger one.
			if cps[i].Score > current.Score {
				current = cps[i]
			}
		} else {
			merged = append(merged, current)
			current = cps[i]
		}
	}
	merged = append(merged, current)

	// Sort by score descending, keep top maxKeep, then re-sort by time.
	if len(merged) > maxKeep {
		sort.Slice(merged, func(i, j int) bool {
			return merged[i].Score > merged[j].Score
		})
		merged = merged[:maxKeep]
		sort.Slice(merged, func(i, j int) bool {
			return merged[i].At.Before(merged[j].At)
		})
	}

	return merged
}

// Combine runs all provided detectors on a signal's points, merges the
// results, and returns at most 5 change-points (per spec §9).
//
// step is the approximate interval between pts samples (used to set the
// merge window). If step is 0, a default of 60 seconds is used.
func Combine(detectors []Detector, pts, baseline []model.Point, stepNs int64) []model.ChangePoint {
	if stepNs <= 0 {
		stepNs = int64(60e9) // 60 seconds default
	}
	mergeWindow := 2 * stepNs

	var all []model.ChangePoint
	for _, d := range detectors {
		all = append(all, d.Detect(pts, baseline)...)
	}
	return MergeAndCap(all, mergeWindow, 5)
}

// EstimateStep returns the median inter-sample interval in nanoseconds.
// Falls back to 60s if pts has fewer than 2 points.
func EstimateStep(pts []model.Point) int64 {
	if len(pts) < 2 {
		return int64(60e9)
	}
	diffs := make([]float64, 0, len(pts)-1)
	for i := 1; i < len(pts); i++ {
		d := float64(pts[i].T.UnixNano() - pts[i-1].T.UnixNano())
		if d > 0 {
			diffs = append(diffs, d)
		}
	}
	if len(diffs) == 0 {
		return int64(60e9)
	}
	sort.Float64s(diffs)
	return int64(median(diffs))
}
