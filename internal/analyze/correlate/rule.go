// Package correlate implements the deterministic, rule-based causal ranking
// engine described in §10 of the project specification.
//
// Architecture:
//
//	Rule (interface)
//	  ↓  Each rule scans events + signals + graph
//	[]Edge  (trigger event → effect, with score)
//	  ↓  Engine assembles edges into causal chains
//	[]Hypothesis (ranked)
//	  ↓
//	Verdict
//
// Every rule has a stable ID (RW001..RW010), is independently testable, and
// emits the exact timestamps + magnitudes it used so the explanation template
// can cite them verbatim. No machine learning; same input ≡ same output.
package correlate

import (
	"fmt"
	"math"
	"time"

	"github.com/paultanay/rewind/internal/analyze/topology"
	"github.com/paultanay/rewind/internal/model"
)

// ─── Rule interface ───────────────────────────────────────────────────────────

// Rule is the interface every correlation rule must satisfy.
// Apply receives the full incident context and returns all edges it finds.
type Rule interface {
	// ID returns the stable identifier, e.g. "RW001".
	ID() string
	// Apply scans the incident for causal edges and returns them.
	Apply(ctx RuleContext) []Edge
}

// RuleContext bundles all inputs available to a rule.
type RuleContext struct {
	Inc   model.Incident
	Graph *topology.Graph
	// signalByEntity provides fast lookup of signals keyed by (entityID, metric).
	signalByEntity map[signalKey]*model.Signal
	// eventIndex provides fast lookup by ID.
	eventIndex map[string]*model.Event
}

type signalKey struct{ entityID, metric string }

// NewRuleContext builds a RuleContext from an incident and the topology graph.
func NewRuleContext(inc model.Incident, graph *topology.Graph) RuleContext {
	sbe := make(map[signalKey]*model.Signal, len(inc.Signals))
	for i := range inc.Signals {
		s := &inc.Signals[i]
		sbe[signalKey{s.EntityID, s.Metric}] = s
	}
	ei := make(map[string]*model.Event, len(inc.Events))
	for i := range inc.Events {
		e := &inc.Events[i]
		ei[e.ID] = e
	}
	return RuleContext{
		Inc:            inc,
		Graph:          graph,
		signalByEntity: sbe,
		eventIndex:     ei,
	}
}

// Signal returns the signal for (entityID, metric), or nil.
func (c *RuleContext) Signal(entityID, metric string) *model.Signal {
	return c.signalByEntity[signalKey{entityID, metric}]
}

// Event returns the event by ID, or nil.
func (c *RuleContext) Event(id string) *model.Event {
	return c.eventIndex[id]
}

// SignalsForEntity returns all signals whose EntityID matches.
func (c *RuleContext) SignalsForEntity(entityID string) []*model.Signal {
	var out []*model.Signal
	for i := range c.Inc.Signals {
		if c.Inc.Signals[i].EntityID == entityID {
			out = append(out, &c.Inc.Signals[i])
		}
	}
	return out
}

// SignalsWithMetric returns all signals with the given canonical metric name.
func (c *RuleContext) SignalsWithMetric(metric string) []*model.Signal {
	var out []*model.Signal
	for i := range c.Inc.Signals {
		if c.Inc.Signals[i].Metric == metric {
			out = append(out, &c.Inc.Signals[i])
		}
	}
	return out
}

// EventsByKind returns all events matching the given kind.
func (c *RuleContext) EventsByKind(kind model.EventKind) []model.Event {
	var out []model.Event
	for _, e := range c.Inc.Events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// ─── Edge: scored causal connection ──────────────────────────────────────────

// Edge represents a scored causal connection: a trigger event caused an effect.
// Multiple edges from the same trigger are assembled into a causal chain.
type Edge struct {
	// RuleID is the rule that produced this edge.
	RuleID string
	// TriggerEventID is the event that the rule believes initiated the chain.
	TriggerEventID string
	// EffectDesc is a human-readable description of the observed effect.
	EffectDesc string
	// Score is the edge's contribution to the hypothesis score (0..1).
	Score float64
	// Link is the model representation of this causal step.
	Link model.ChainLink
	// Chain holds the full ordered chain when a rule wants to pass more than
	// one link (e.g. RW001 passes [deployLink, cpLink1, cpLink2, ...]).
	// When non-nil, the assembler uses Chain instead of []Link.
	Chain []model.ChainLink
	// CorroborationOnly: if true, this edge adds evidence to an existing chain
	// but does not start a new hypothesis (RW010 pattern).
	CorroborationOnly bool
}

// ─── Temporal scoring helpers ─────────────────────────────────────────────────

// TemporalScore returns a score in (0,1] based on how quickly the effect
// followed the trigger. Spec §10: effects within 0–5m score ~1.0; at 2h ≈ 0.
//
//	score = exp(-λ·gap_minutes)  where λ = ln(20)/5 ≈ 0.6
//
// This gives: 0m→1.00, 1m→0.55, 5m→0.05, 10m→0.002.
// We floor at 0.01 so very late correlations remain non-zero and discoverable.
func TemporalScore(trigger, effect time.Time) float64 {
	if effect.Before(trigger) {
		return 0 // effect must follow trigger
	}
	gapMin := effect.Sub(trigger).Minutes()
	const λ = 0.6 // tuned so 5 min → ~0.05
	s := math.Exp(-λ * gapMin)
	if s < 0.01 {
		return 0.01
	}
	return s
}

// ProximityScore returns a boost factor based on topological distance between
// the trigger entity and the effect entity in the graph.
//
//	same entity → 1.0
//	distance 1  → 0.7
//	distance 2  → 0.5
//	distance 3  → 0.35
//	distance ≥4 → 0.2
func ProximityScore(graph *topology.Graph, triggerEntityID, effectEntityID string) float64 {
	if triggerEntityID == effectEntityID {
		return 1.0
	}
	d := graph.Distance(triggerEntityID, effectEntityID)
	switch {
	case d == 1:
		return 0.7
	case d == 2:
		return 0.5
	case d == 3:
		return 0.35
	default:
		return 0.2
	}
}

// ─── Change-point lookup helpers ──────────────────────────────────────────────

// FirstChangePointAfter returns the first change-point on sig that occurs at or
// after `after`, or nil if none.
func FirstChangePointAfter(sig *model.Signal, after time.Time) *model.ChangePoint {
	for i := range sig.ChangePoints {
		if !sig.ChangePoints[i].At.Before(after) {
			return &sig.ChangePoints[i]
		}
	}
	return nil
}

// AnyChangePointInWindow returns the highest-scoring change-point within the
// window [from, to], or nil if none.
func AnyChangePointInWindow(sig *model.Signal, from, to time.Time) *model.ChangePoint {
	var best *model.ChangePoint
	for i := range sig.ChangePoints {
		cp := &sig.ChangePoints[i]
		if !cp.At.Before(from) && !cp.At.After(to) {
			if best == nil || cp.Score > best.Score {
				best = cp
			}
		}
	}
	return best
}

// ─── Explanation template helper ──────────────────────────────────────────────

// FormatExplanation builds a citable explanation string from a template and
// named arguments. It always includes the rule IDs, timestamps, and magnitudes
// that generated the hypothesis. No free-form AI generation.
//
// Template verbs:
//
//	{trigger}    → trigger event title + timestamp
//	{effect}     → effect description
//	{magnitude}  → change-point magnitude, e.g. "3.4×"
//	{gap}        → gap between trigger and effect, e.g. "2m30s"
func FormatExplanation(ruleID, trigger, effect string, magnitude float64, gap time.Duration) string {
	magStr := ""
	if magnitude > 0 {
		magStr = fmt.Sprintf(" (%.1f×)", magnitude)
	}
	gapStr := ""
	if gap > 0 {
		gapStr = fmt.Sprintf(", %s after trigger", fmtDuration(gap))
	}
	return fmt.Sprintf("[%s] %s → %s%s%s", ruleID, trigger, effect, magStr, gapStr)
}

func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}
