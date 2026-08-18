package correlate

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/paultanay/rewind/internal/model"
)

// ─── RW001: Deploy → metric change-point ─────────────────────────────────────

// RW001 scores a Deploy event as the trigger when any metric change-point on
// the same service appears within 10 minutes of the deploy.
//
// Rationale: the most common incident pattern in modern services is a bad
// deploy causing immediate metric degradation. The 10-minute window is
// deliberately narrow — it balances false-negative risk against promiscuous
// correlation with unrelated drift.
//
// Scoring: temporal score × proximity boost × change-point score.
// Each corroborating signal (additional change-points on the same entity
// after the deploy) adds to the base score with a 0.3× weight.
type RW001 struct{}

func (r RW001) ID() string { return "RW001" }

func (r RW001) Apply(ctx RuleContext) []Edge {
	const window = 10 * minD
	var edges []Edge

	for _, ev := range ctx.EventsByKind(evDeploy) {
		ev := ev // capture
		var corroborations []model.ChainLink

		for _, sig := range ctx.SignalsForEntity(ev.EntityID) {
			cp := FirstChangePointAfter(sig, ev.At)
			if cp == nil {
				continue
			}
			gap := cp.At.Sub(ev.At)
			if gap > window {
				continue
			}
			link := model.ChainLink{
				SignalID:    sig.ID,
				Description: fmt.Sprintf("%.1f× %s change-point %s after deploy", cp.Magnitude, sig.Metric, fmtDuration(gap)),
				RuleID:      r.ID(),
			}
			corroborations = append(corroborations, link)
		}

		if len(corroborations) == 0 {
			continue
		}

		// Base score: temporal is 1.0 (deploy is the anchor, not a downstream
		// effect), × (1 + 0.3·extras) so more corroborating signals = higher score.
		score := math.Min(1.0, 0.5+float64(len(corroborations))*0.15)

		chain := []model.ChainLink{{
			EventID:     ev.ID,
			Description: fmt.Sprintf("Deploy event: %s", ev.Title),
			RuleID:      r.ID(),
		}}
		chain = append(chain, corroborations...)

		edges = append(edges, Edge{
			RuleID:         r.ID(),
			TriggerEventID: ev.ID,
			EffectDesc:     fmt.Sprintf("%d metric change-points within %s of deploy", len(corroborations), fmtDuration(window)),
			Score:          score,
			Link:           chain[0],
			Chain:          chain, // full chain for confidence calibration
		})
		_ = chain // already set in Chain above
	}

	return edges
}

// ─── RW002: ConfigChange → metric change-point ───────────────────────────────

// RW002 is structurally identical to RW001 but fires on ConfigChange events.
// It is a separate rule so that each appears independently in the hypothesis
// explanation and rule list, making it auditable.
type RW002 struct{}

func (r RW002) ID() string { return "RW002" }

func (r RW002) Apply(ctx RuleContext) []Edge {
	const window = 10 * minD
	var edges []Edge

	for _, ev := range ctx.EventsByKind(evConfigChange) {
		ev := ev
		count := 0
		for _, sig := range ctx.SignalsForEntity(ev.EntityID) {
			cp := FirstChangePointAfter(sig, ev.At)
			if cp == nil || cp.At.Sub(ev.At) > window {
				continue
			}
			count++
		}
		if count == 0 {
			continue
		}
		score := math.Min(1.0, 0.5+float64(count)*0.15)
		edges = append(edges, Edge{
			RuleID:         r.ID(),
			TriggerEventID: ev.ID,
			EffectDesc:     fmt.Sprintf("%d metric change-points within %s of config change", count, fmtDuration(window)),
			Score:          score,
			Link: model.ChainLink{
				EventID:     ev.ID,
				Description: fmt.Sprintf("ConfigChange event: %s", ev.Title),
				RuleID:      r.ID(),
			},
		})
	}
	return edges
}

// ─── RW003: OOMKill chain ─────────────────────────────────────────────────────

// RW003 detects the classic memory saturation chain:
//
//	memory.usage ↑ → OOMKill event → Restart event → error.rate ↑ on dependents
//
// Each step is required in causal order. Missing steps reduce the score but do
// not disqualify the chain — partial evidence still surfaces the memory pattern.
type RW003 struct{}

func (r RW003) ID() string { return "RW003" }

func (r RW003) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	const oomWindow = 5 * minD      // memory spike should precede OOMKill by ≤5m
	const cascadeWindow = 10 * minD // downstream error.rate within 10m

	for _, oomEv := range ctx.EventsByKind(evOOMKill) {
		oomEv := oomEv
		// Step 1: was there a memory.usage spike before the OOMKill?
		memSig := ctx.Signal(oomEv.EntityID, model.MetricMemoryUsage)
		memScore := 0.0
		var memLink model.ChainLink
		if memSig != nil {
			// Look for a memory CP in the window BEFORE the OOMKill
			cp := AnyChangePointInWindow(memSig, oomEv.At.Add(-oomWindow), oomEv.At)
			if cp != nil && cp.Direction == model.DirectionUp {
				memScore = 0.4 * cp.Score
				memLink = model.ChainLink{
					SignalID:    memSig.ID,
					Description: fmt.Sprintf("memory.usage rose %.1f× before OOMKill", cp.Magnitude),
					RuleID:      r.ID(),
				}
			}
		}

		if memScore == 0 {
			// Without memory evidence, this is speculative at best — skip.
			continue
		}

		baseScore := 0.5 + memScore

		edges = append(edges, Edge{
			RuleID:         r.ID(),
			TriggerEventID: oomEv.ID,
			EffectDesc:     fmt.Sprintf("OOMKill on %s (memory saturation chain)", oomEv.EntityID),
			Score:          math.Min(1.0, baseScore),
			Link:           memLink,
		})

		// Step 2: look for downstream error.rate on entities close to the OOMKilled pod.
		for _, errSig := range ctx.SignalsWithMetric(model.MetricErrorRate) {
			if errSig.EntityID == oomEv.EntityID {
				continue
			}
			cp := FirstChangePointAfter(errSig, oomEv.At)
			if cp == nil || cp.At.Sub(oomEv.At) > cascadeWindow {
				continue
			}
			proximity := ProximityScore(ctx.Graph, oomEv.EntityID, errSig.EntityID)
			cascadeScore := math.Min(1.0, baseScore+0.2*proximity*cp.Score)
			edges = append(edges, Edge{
				RuleID:         r.ID(),
				TriggerEventID: oomEv.ID,
				EffectDesc:     fmt.Sprintf("error.rate ↑ on %s after OOMKill cascade", errSig.EntityID),
				Score:          cascadeScore,
				Link: model.ChainLink{
					SignalID:    errSig.ID,
					Description: fmt.Sprintf("error.rate ↑ %.1f× on downstream entity %s", cp.Magnitude, errSig.EntityID),
					RuleID:      r.ID(),
				},
			})
		}
	}
	return edges
}

// ─── RW004: Saturation — cpu.usage ↑ → latency.p99 ↑ ───────────────────────

// RW004 detects CPU saturation causing latency degradation on the same entity.
// Also fires on cpu.throttle as an alternative saturation indicator.
type RW004 struct{}

func (r RW004) ID() string { return "RW004" }

func (r RW004) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	const satWindow = 5 * minD

	for _, cpuSig := range ctx.SignalsWithMetric(model.MetricCPUUsage) {
		cpuSig := cpuSig
		for i := range cpuSig.ChangePoints {
			cpuCP := &cpuSig.ChangePoints[i]
			if cpuCP.Direction != model.DirectionUp {
				continue
			}
			// Look for latency CP on same entity within satWindow after cpu spike
			latSig := ctx.Signal(cpuSig.EntityID, model.MetricLatencyP99)
			if latSig == nil {
				// Also check p95
				latSig = ctx.Signal(cpuSig.EntityID, model.MetricLatencyP95)
			}
			if latSig == nil {
				continue
			}
			latCP := FirstChangePointAfter(latSig, cpuCP.At)
			if latCP == nil || latCP.At.Sub(cpuCP.At) > satWindow {
				continue
			}
			score := 0.5 * cpuCP.Score * TemporalScore(cpuCP.At, latCP.At)
			// Saturation chains don't have an "event trigger" — we attach to the
			// CPU change-point. The TriggerEventID is left empty; the verdict
			// assembler will handle this pattern as a "signal-initiated" chain.
			edges = append(edges, Edge{
				RuleID:         r.ID(),
				TriggerEventID: "", // signal-initiated chain
				EffectDesc: fmt.Sprintf("cpu.usage ↑%.1f× then latency.p99 ↑%.1f× within %s",
					cpuCP.Magnitude, latCP.Magnitude, fmtDuration(latCP.At.Sub(cpuCP.At))),
				Score: math.Min(0.9, score+0.3), // saturation is a strong signal
				Link: model.ChainLink{
					SignalID:    latSig.ID,
					Description: fmt.Sprintf("latency.p99 rose %.1f× after cpu.usage ↑", latCP.Magnitude),
					RuleID:      r.ID(),
				},
			})
		}
	}

	// Also check cpu.throttle as saturation indicator
	for _, throtSig := range ctx.SignalsWithMetric(model.MetricCPUThrottle) {
		throtSig := throtSig
		for i := range throtSig.ChangePoints {
			cp := &throtSig.ChangePoints[i]
			if cp.Direction != model.DirectionUp {
				continue
			}
			latSig := ctx.Signal(throtSig.EntityID, model.MetricLatencyP99)
			if latSig == nil {
				continue
			}
			latCP := FirstChangePointAfter(latSig, cp.At)
			if latCP == nil || latCP.At.Sub(cp.At) > satWindow {
				continue
			}
			score := 0.4 * cp.Score * TemporalScore(cp.At, latCP.At)
			edges = append(edges, Edge{
				RuleID:         r.ID(),
				TriggerEventID: "",
				EffectDesc:     fmt.Sprintf("cpu.throttle ↑ then latency.p99 ↑%.1f×", latCP.Magnitude),
				Score:          math.Min(0.85, score+0.25),
				Link: model.ChainLink{
					SignalID:    latSig.ID,
					Description: fmt.Sprintf("latency.p99 rose %.1f× after cpu throttling", latCP.Magnitude),
					RuleID:      r.ID(),
				},
			})
		}
	}
	return edges
}

// ─── RW005: Upstream cascade ──────────────────────────────────────────────────

// RW005 identifies upstream dependency failures: service B's error.rate rises
// before service A's, and B is reachable from A in the topology graph.
// This supports the "upstream is the cause" pattern.
type RW005 struct{}

func (r RW005) ID() string { return "RW005" }

func (r RW005) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	errSigs := ctx.SignalsWithMetric(model.MetricErrorRate)

	for i, sigA := range errSigs {
		cpA := firstUpChangePoint(sigA)
		if cpA == nil {
			continue
		}
		for j, sigB := range errSigs {
			if i == j {
				continue
			}
			cpB := firstUpChangePoint(sigB)
			if cpB == nil {
				continue
			}
			// B must precede A
			if !cpB.At.Before(cpA.At) {
				continue
			}
			gap := cpA.At.Sub(cpB.At)
			if gap > 15*minD {
				continue // too long to be causal
			}
			// B is upstream of A if A depends on B: A can reach B via call edges
			// (A→B means A calls B, so B failing first explains A's degradation).
			if !ctx.Graph.Reachable(sigA.EntityID, sigB.EntityID) {
				continue
			}
			score := TemporalScore(cpB.At, cpA.At) * cpB.Score * 0.7
			edges = append(edges, Edge{
				RuleID:         r.ID(),
				TriggerEventID: "", // signal-initiated
				EffectDesc: fmt.Sprintf("error.rate ↑ on %s (upstream) propagated to %s %s later",
					sigB.EntityID, sigA.EntityID, fmtDuration(gap)),
				Score: math.Min(0.85, score+0.2),
				Link: model.ChainLink{
					SignalID:    sigA.ID,
					Description: fmt.Sprintf("error.rate ↑ on %s, %s after upstream %s", sigA.EntityID, fmtDuration(gap), sigB.EntityID),
					RuleID:      r.ID(),
				},
			})
		}
	}
	return edges
}

func firstUpChangePoint(sig *model.Signal) *model.ChangePoint {
	for i := range sig.ChangePoints {
		if sig.ChangePoints[i].Direction == model.DirectionUp {
			return &sig.ChangePoints[i]
		}
	}
	return nil
}

// ─── RW006: Node pressure → pod effects ──────────────────────────────────────

// RW006 correlates NodePressure or Eviction events with metric degradation on
// pods that the topology graph places on that node.
type RW006 struct{}

func (r RW006) ID() string { return "RW006" }

func (r RW006) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	const pressureWindow = 10 * minD

	for _, ev := range ctx.EventsByKind(evNodePressure) {
		ev := ev
		// Find all pod entities adjacent to this node in the graph.
		adj := ctx.Graph.Adjacent(ev.EntityID)
		for _, podID := range adj {
			for _, sig := range ctx.SignalsForEntity(podID) {
				cp := FirstChangePointAfter(sig, ev.At)
				if cp == nil || cp.At.Sub(ev.At) > pressureWindow {
					continue
				}
				score := TemporalScore(ev.At, cp.At) * cp.Score * ProximityScore(ctx.Graph, ev.EntityID, podID)
				edges = append(edges, Edge{
					RuleID:         r.ID(),
					TriggerEventID: ev.ID,
					EffectDesc:     fmt.Sprintf("NodePressure on %s caused %s degradation on pod %s", ev.EntityID, sig.Metric, podID),
					Score:          math.Min(0.9, score+0.3),
					Link: model.ChainLink{
						EventID:     ev.ID,
						Description: fmt.Sprintf("NodePressure event on node %s", ev.EntityID),
						RuleID:      r.ID(),
					},
				})
			}
		}
	}
	return edges
}

// ─── RW007: Queue lag → consumer latency ─────────────────────────────────────

// RW007 finds queue.lag increases that precede latency degradation in consumer
// services adjacent to the queue entity in the topology graph.
type RW007 struct{}

func (r RW007) ID() string { return "RW007" }

func (r RW007) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	const lagWindow = 10 * minD

	for _, lagSig := range ctx.SignalsWithMetric(model.MetricQueueLag) {
		lagSig := lagSig
		cp := firstUpChangePoint(lagSig)
		if cp == nil {
			continue
		}
		// Find adjacent consumers
		for _, consumerID := range ctx.Graph.Adjacent(lagSig.EntityID) {
			latSig := ctx.Signal(consumerID, model.MetricLatencyP99)
			if latSig == nil {
				latSig = ctx.Signal(consumerID, model.MetricLatencyP95)
			}
			if latSig == nil {
				continue
			}
			latCP := FirstChangePointAfter(latSig, cp.At)
			if latCP == nil || latCP.At.Sub(cp.At) > lagWindow {
				continue
			}
			score := TemporalScore(cp.At, latCP.At) * cp.Score * 0.7
			edges = append(edges, Edge{
				RuleID:         r.ID(),
				TriggerEventID: "",
				EffectDesc:     fmt.Sprintf("queue.lag ↑%.1f× → consumer %s latency ↑%.1f×", cp.Magnitude, consumerID, latCP.Magnitude),
				Score:          math.Min(0.85, score+0.25),
				Link: model.ChainLink{
					SignalID:    latSig.ID,
					Description: fmt.Sprintf("consumer latency ↑ after queue.lag rose %.1f×", cp.Magnitude),
					RuleID:      r.ID(),
				},
			})
		}
	}
	return edges
}

// ─── RW008: Scale event → saturation ─────────────────────────────────────────

// RW008 detects replica count decreases (or HPA at maximum) that precede CPU
// or latency saturation. A scale-down reduces capacity; if traffic is constant,
// remaining pods saturate.
type RW008 struct{}

func (r RW008) ID() string { return "RW008" }

func (r RW008) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	const scaleWindow = 5 * minD

	for _, ev := range ctx.EventsByKind(evScaleChange) {
		ev := ev
		// Only care about scale-down (captured as "down" in Detail or implied
		// by replicas.Direction==Down)
		repSig := ctx.Signal(ev.EntityID, model.MetricReplicas)
		isScaleDown := false
		if repSig != nil {
			cp := AnyChangePointInWindow(repSig, ev.At.Add(-1*minD), ev.At.Add(1*minD))
			if cp != nil && cp.Direction == model.DirectionDown {
				isScaleDown = true
			}
		}
		if !isScaleDown {
			continue
		}
		// Look for saturation signals on the same entity after the scale event
		for _, metric := range []string{model.MetricCPUUsage, model.MetricLatencyP99} {
			sig := ctx.Signal(ev.EntityID, metric)
			if sig == nil {
				continue
			}
			cp := FirstChangePointAfter(sig, ev.At)
			if cp == nil || cp.At.Sub(ev.At) > scaleWindow || cp.Direction != model.DirectionUp {
				continue
			}
			score := TemporalScore(ev.At, cp.At) * cp.Score
			edges = append(edges, Edge{
				RuleID:         r.ID(),
				TriggerEventID: ev.ID,
				EffectDesc:     fmt.Sprintf("ScaleDown on %s → %s ↑%.1f× saturation", ev.EntityID, metric, cp.Magnitude),
				Score:          math.Min(0.8, score+0.3),
				Link: model.ChainLink{
					EventID:     ev.ID,
					Description: fmt.Sprintf("ScaleChange (down) on %s", ev.EntityID),
					RuleID:      r.ID(),
				},
			})
		}
	}
	return edges
}

// ─── RW009: Crash loop coalescing ────────────────────────────────────────────

// RW009 synthesizes a CrashLoop event when ≥3 Restart events occur on the same
// pod within 10 minutes. This is event coalescing, not just correlation: it
// creates a new high-severity event in the Incident that downstream rules can
// reference.
//
// CrashLoop is one of the most actionable patterns in Kubernetes — an engineer
// must investigate the pod logs immediately.
type RW009 struct{}

func (r RW009) ID() string { return "RW009" }

func (r RW009) Apply(ctx RuleContext) []Edge {
	const window = 10 * minD
	const minRestarts = 3
	var edges []Edge

	restarts := ctx.EventsByKind(evRestart)

	// Group by entity
	byEntity := make(map[string][]model.Event)
	for _, e := range restarts {
		byEntity[e.EntityID] = append(byEntity[e.EntityID], e)
	}

	entityIDs := make([]string, 0, len(byEntity))
	for entityID := range byEntity {
		entityIDs = append(entityIDs, entityID)
	}
	sort.Strings(entityIDs)
	for _, entityID := range entityIDs {
		evs := byEntity[entityID]
		// Sliding window: find the earliest group of ≥3 restarts within 10m
		for i := 0; i < len(evs); i++ {
			j := i
			for j < len(evs) && evs[j].At.Sub(evs[i].At) <= window {
				j++
			}
			if j-i >= minRestarts {
				// Coalesce into a CrashLoop edge. The caller (Run) will insert a
				// synthetic CrashLoop event into the Incident.
				edges = append(edges, Edge{
					RuleID:         r.ID(),
					TriggerEventID: evs[i].ID, // first restart is the anchor
					EffectDesc:     fmt.Sprintf("%d restarts in %s on %s → CrashLoop", j-i, fmtDuration(evs[j-1].At.Sub(evs[i].At)), entityID),
					Score:          0.7, // crash loops are always notable
					Link: model.ChainLink{
						EventID:     evs[i].ID,
						Description: fmt.Sprintf("%d Restart events in %s (CrashLoop)", j-i, fmtDuration(evs[j-1].At.Sub(evs[i].At))),
						RuleID:      r.ID(),
					},
					CorroborationOnly: false, // this creates a new event
				})
				break // one detection per entity is sufficient
			}
		}
	}
	return edges
}

// ─── RW010: Alert-as-symptom ──────────────────────────────────────────────────

// RW010 ensures AlertFired events add corroborating confidence to hypotheses
// they overlap with, but are NEVER treated as causal triggers themselves.
//
// Spec §10: "AlertFired never scores as a trigger, only as corroboration
// (+confidence to chains it overlaps)."
//
// Implementation: RW010 returns CorroborationOnly edges. The verdict assembler
// adds their scores to existing hypotheses but does not create new ones.
type RW010 struct{}

func (r RW010) ID() string { return "RW010" }

func (r RW010) Apply(ctx RuleContext) []Edge {
	var edges []Edge
	alerts := ctx.EventsByKind(evAlertFired)
	if len(alerts) == 0 {
		return nil
	}
	// For each alert, emit a corroboration edge. The assembler will match it
	// to any hypothesis whose chain includes a change-point that overlaps the
	// alert's time.
	for _, a := range alerts {
		a := a
		edges = append(edges, Edge{
			RuleID:            r.ID(),
			TriggerEventID:    a.ID,
			EffectDesc:        fmt.Sprintf("Alert '%s' fired (corroboration only)", a.Title),
			Score:             0.15, // modest boost; alerts are symptoms, not causes
			CorroborationOnly: true,
			Link: model.ChainLink{
				EventID:     a.ID,
				Description: fmt.Sprintf("AlertFired: %s", a.Title),
				RuleID:      r.ID(),
			},
		})
	}
	return edges
}

// ─── EventKind shorthands (package-private) ───────────────────────────────────
// These constants avoid importing model in every rule function body.
const (
	evDeploy       = model.EventKindDeploy
	evConfigChange = model.EventKindConfigChange
	evOOMKill      = model.EventKindOOMKill
	evRestart      = model.EventKindRestart
	evScaleChange  = model.EventKindScaleChange
	evNodePressure = model.EventKindNodePressure
	evAlertFired   = model.EventKindAlertFired
	minD           = time.Minute
)

// Ensure all rules implement the Rule interface.
var _ Rule = RW001{}
var _ Rule = RW002{}
var _ Rule = RW003{}
var _ Rule = RW004{}
var _ Rule = RW005{}
var _ Rule = RW006{}
var _ Rule = RW007{}
var _ Rule = RW008{}
var _ Rule = RW009{}
var _ Rule = RW010{}
