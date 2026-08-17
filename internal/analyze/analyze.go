// Package analyze is the top-level analysis pipeline. It wires together
// change-point detection, entity topology construction, and the correlation
// engine, exposing a single Run entry-point for the CLI.
package analyze

import (
	"math"

	"github.com/paultanay/rewind/internal/analyze/changepoint"
	"github.com/paultanay/rewind/internal/analyze/correlate"
	"github.com/paultanay/rewind/internal/analyze/topology"
	"github.com/paultanay/rewind/internal/model"
)

// RunResult extends the base Incident with the topology graph produced during
// analysis. Callers that need the graph for further inspection (e.g. tests)
// can use RunFull; ordinary callers should use Run.
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
// Incident and the entity topology graph.
func RunFull(inc model.Incident) RunResult {
	inc = model.NormalizeIncident(inc)

	// Detect statistical change-points in each signal using both the
	// baseline-deviation (median+MAD) and PELT detectors.
	inc.Signals = detectChangePoints(inc.Signals)

	// Build the entity ownership graph once; the correlation engine
	// uses it to score causal proximity between entities.
	graph := topology.Build(inc.Entities)

	// Apply all 10 correlation rules (RW001–RW010), assemble causal chains,
	// calibrate confidence, and attach the ranked verdict.
	inc.Verdict = correlate.Run(inc, graph)

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
