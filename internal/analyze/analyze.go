// Package analyze is the top-level analysis pipeline. It wires together the
// sub-packages (changepoint, topology, correlate, verdict) and exposes a
// single Run function that the CLI calls.
//
// Phase 2: change-point detection on all signals.
// Phase 3: topology graph construction from collected entities.
// Phase 4: correlation rules RW001–RW010, verdict generation.
package analyze

import (
	"math"

	"github.com/rewind-io/rewind/internal/analyze/changepoint"
	"github.com/rewind-io/rewind/internal/analyze/topology"
	"github.com/rewind-io/rewind/internal/model"
)

// RunResult extends the base Incident with the topology graph produced during
// analysis. The graph is passed to the correlation engine in Phase 4.
type RunResult struct {
	Incident model.Incident
	// Graph is the entity topology built from Incident.Entities. Callers that
	// only care about the updated Incident can ignore this field.
	Graph *topology.Graph
}

// Run executes the full analysis pipeline and returns an updated Incident.
// It is a convenience wrapper around RunFull that discards the RunResult.
func Run(inc model.Incident) model.Incident {
	return RunFull(inc).Incident
}

// RunFull executes the full analysis pipeline and returns both the updated
// Incident and the topology graph. Used by Phase 4 correlation engine.
func RunFull(inc model.Incident) RunResult {
	// ── Phase 2: change-point detection ──────────────────────────────────────
	inc.Signals = detectChangePoints(inc.Signals)

	// ── Phase 3: topology graph ───────────────────────────────────────────────
	// Build once; the correlation engine (Phase 4) receives it via RunResult.
	graph := topology.Build(inc.Entities)

	// ── Phase 4 placeholder: verdict engine ───────────────────────────────────
	// inc.Verdict = correlate.Run(inc, graph)
	_ = graph // used in Phase 4

	return RunResult{Incident: inc, Graph: graph}
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
