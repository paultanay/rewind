// Package analyze is the top-level analysis pipeline. It wires together the
// sub-packages (changepoint, topology, correlate, verdict) and exposes a
// single Run function that the CLI calls.
//
// Phase 2 implements: change-point detection over all signals.
// Phase 3 adds: topology graph construction.
// Phase 4 adds: correlation rules and verdict generation.
package analyze

import (
	"math"

	"github.com/rewind-io/rewind/internal/analyze/changepoint"
	"github.com/rewind-io/rewind/internal/model"
)

// Run executes the full analysis pipeline on the incident and returns an
// updated copy with Signals populated with ChangePoints.
// In Phase 2 only change-point detection is implemented; Verdict remains nil.
func Run(inc model.Incident) model.Incident {
	inc.Signals = detectChangePoints(inc.Signals)
	return inc
}

// detectChangePoints runs both detectors on each signal and attaches the
// merged, capped change-point list.
//
// Detector tuning rationale:
//   - BaselineDeviation always runs at k=5 MAD threshold (spec §9).
//   - PELT penalty is adaptive: standard BIC (2·ln n) when no baseline is
//     available; 3× BIC when a good baseline exists. The higher penalty means
//     PELT only fires on large structural shifts, letting the baseline-deviation
//     detector handle noise-relative detection. This prevents double-counting
//     false positives on stable signals.
func detectChangePoints(signals []model.Signal) []model.Signal {
	out := make([]model.Signal, len(signals))
	for i, sig := range signals {
		step := changepoint.EstimateStep(sig.Points)

		n := float64(len(sig.Points))
		if n < 2 {
			out[i] = sig
			continue
		}

		peltPenalty := 0.0 // 0 = use default BIC inside PELT
		if len(sig.Baseline) >= 10 {
			// 3× BIC when baseline is present (see rationale above).
			peltPenalty = 3 * 2 * math.Log(n)
		}

		detectors := []changepoint.Detector{
			&changepoint.BaselineDeviation{K: 5, MinRun: 3},
			&changepoint.PELT{Penalty: peltPenalty},
		}

		sig.ChangePoints = changepoint.Combine(detectors, sig.Points, sig.Baseline, step)
		out[i] = sig
	}
	return out
}
