package correlate

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/paultanay/rewind/internal/analyze/topology"
	"github.com/paultanay/rewind/internal/model"
)

// Run applies all correlation rules and assembles a ranked Verdict.
// It is called by analyze.RunFull.
func Run(inc model.Incident, graph *topology.Graph) *model.Verdict {
	ctx := NewRuleContext(inc, graph)

	// Apply all rules in a fixed, deterministic order.
	allRules := []Rule{
		RW001{}, RW002{}, RW003{}, RW004{},
		RW005{}, RW006{}, RW007{}, RW008{},
		RW009{}, RW010{},
	}

	// Step 1: collect CrashLoop coalescing events from RW009.
	// These must be injected into the context before other rules see them.
	var rw009Edges []Edge
	for _, rule := range allRules {
		if rule.ID() == "RW009" {
			rw009Edges = rule.Apply(ctx)
		}
	}
	crashLoopEvents := coalesceCrashLoops(inc, rw009Edges)
	if len(crashLoopEvents) > 0 {
		inc.Events = append(inc.Events, crashLoopEvents...)
		// Rebuild context with the new events.
		ctx = NewRuleContext(inc, graph)
	}

	// Step 2: apply all rules (including RW009 again — it's idempotent).
	var allEdges []Edge
	for _, rule := range allRules {
		edges := rule.Apply(ctx)
		allEdges = append(allEdges, edges...)
	}

	// Step 3: assemble edges into hypotheses.
	hypotheses := assembleHypotheses(ctx, allEdges)

	// Step 4: calibrate confidence per spec §10.
	calibrateConfidence(hypotheses)

	// Step 5: sort best-first, cap at 3.
	sort.Slice(hypotheses, func(i, j int) bool {
		return hypotheses[i].Score > hypotheses[j].Score
	})
	if len(hypotheses) > 3 {
		hypotheses = hypotheses[:3]
	}

	verdict := &model.Verdict{
		Hypotheses: hypotheses,
	}

	// Spec §10: if no chain scores above floor, set NoTriggerFound and list
	// notable anomalies.
	const scoreFloor = 0.2
	if len(hypotheses) == 0 || hypotheses[0].Score < scoreFloor {
		verdict.NoTriggerFound = true
		verdict.NotableAnomalies = collectNotableAnomalies(inc)
		if len(hypotheses) > 0 && hypotheses[0].Score < scoreFloor {
			verdict.Hypotheses = nil
		}
	}

	return verdict
}

// ─── CrashLoop coalescing (RW009) ─────────────────────────────────────────────

func coalesceCrashLoops(inc model.Incident, edges []Edge) []model.Event {
	seen := make(map[string]bool)
	var out []model.Event
	for _, e := range edges {
		if seen[e.TriggerEventID] {
			continue
		}
		seen[e.TriggerEventID] = true
		// Find the trigger event to get entity/time
		var anchor *model.Event
		for i := range inc.Events {
			if inc.Events[i].ID == e.TriggerEventID {
				anchor = &inc.Events[i]
				break
			}
		}
		if anchor == nil {
			continue
		}
		out = append(out, model.Event{
			ID:       "synth-crashloop-" + anchor.EntityID,
			At:       anchor.At,
			Kind:     model.EventKindCrashLoop,
			EntityID: anchor.EntityID,
			Severity: model.SeverityCritical,
			Title:    fmt.Sprintf("CrashLoop: %s (%s)", anchor.EntityID, e.EffectDesc),
			Detail:   e.EffectDesc,
		})
	}
	return out
}

// ─── Hypothesis assembly ──────────────────────────────────────────────────────

type hypothesisKey struct {
	triggerEventID string
	ruleID         string
}

// assembleHypotheses groups edges by trigger event, builds chains, and
// merges corroboration-only edges onto existing hypotheses.
func assembleHypotheses(ctx RuleContext, edges []Edge) []model.Hypothesis {
	// Partition corroboration-only edges.
	var corrobEdges []Edge
	var triggerEdges []Edge
	for _, e := range edges {
		if e.CorroborationOnly {
			corrobEdges = append(corrobEdges, e)
		} else {
			triggerEdges = append(triggerEdges, e)
		}
	}

	// Group trigger edges by (triggerEventID, ruleID) → best score wins.
	type hypoKey = hypothesisKey
	byKey := make(map[hypoKey][]Edge)
	for _, e := range triggerEdges {
		k := hypoKey{e.TriggerEventID, e.RuleID}
		byKey[k] = append(byKey[k], e)
	}

	var hypotheses []model.Hypothesis
	for key, group := range byKey {
		h := buildHypothesis(ctx, key.triggerEventID, key.ruleID, group)
		hypotheses = append(hypotheses, h)
	}

	// Apply corroboration: each alert edge boosts hypotheses that overlap.
	for _, ce := range corrobEdges {
		for i := range hypotheses {
			if chainOverlapsAlert(ctx, hypotheses[i].Chain, ce) {
				hypotheses[i].Score += ce.Score
				hypotheses[i].RuleIDs = appendUnique(hypotheses[i].RuleIDs, ce.RuleID)
				hypotheses[i].Chain = append(hypotheses[i].Chain, ce.Link)
			}
		}
	}

	return hypotheses
}

func buildHypothesis(ctx RuleContext, triggerEventID, ruleID string, edges []Edge) model.Hypothesis {
	// Sum scores across all edges for this (trigger, rule) pair.
	totalScore := 0.0
	var links []model.ChainLink
	var effectDescs []string
	for _, e := range edges {
		totalScore += e.Score
		if len(e.Chain) > 0 {
			// Rule passed a full chain (e.g. RW001 with corroborations).
			links = append(links, e.Chain...)
		} else {
			links = append(links, e.Link)
		}
		effectDescs = append(effectDescs, e.EffectDesc)
	}
	links = deduplicateLinks(links)
	// Diminishing returns on corroboration: √(n) scaling.
	if len(edges) > 1 {
		totalScore = edges[0].Score + (totalScore-edges[0].Score)*0.5
	}

	// Build explanation from template.
	triggerTitle := triggerEventID
	if ev := ctx.Event(triggerEventID); ev != nil {
		triggerTitle = fmt.Sprintf("%s at %s", ev.Title, ev.At.UTC().Format("15:04:05Z"))
	}
	explanation := fmt.Sprintf("[%s] %s → %s", ruleID, triggerTitle, strings.Join(effectDescs, "; "))

	return model.Hypothesis{
		TriggerEventID: triggerEventID,
		Score:          math.Min(1.0, totalScore),
		Chain:          links,
		Explanation:    explanation,
		RuleIDs:        []string{ruleID},
	}
}

// chainOverlapsAlert returns true if any link in the chain is within 2 minutes
// of the alert event, making it plausibly corroborating.
func chainOverlapsAlert(ctx RuleContext, chain []model.ChainLink, alertEdge Edge) bool {
	alertEv := ctx.Event(alertEdge.TriggerEventID)
	if alertEv == nil {
		return false
	}
	for _, link := range chain {
		if link.EventID != "" {
			ev := ctx.Event(link.EventID)
			if ev != nil && absDur(ev.At.Sub(alertEv.At)) < 2*time.Minute {
				return true
			}
		}
	}
	return false
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func appendUnique(ss []string, s string) []string {
	for _, existing := range ss {
		if existing == s {
			return ss
		}
	}
	return append(ss, s)
}

// deduplicateLinks removes duplicate ChainLinks (by EventID or SignalID).
func deduplicateLinks(links []model.ChainLink) []model.ChainLink {
	seen := map[string]bool{}
	var out []model.ChainLink
	for _, l := range links {
		key := l.EventID + "|" + l.SignalID
		if !seen[key] {
			seen[key] = true
			out = append(out, l)
		}
	}
	return out
}

// ─── Confidence calibration (spec §10) ───────────────────────────────────────

// calibrateConfidence assigns Confidence labels per spec §10:
//   High   = trigger + ≥3 corroborating signals AND no competitor within 20%
//   Medium = 2+ corroborations OR close competitor
//   else   = Speculative
func calibrateConfidence(hypotheses []model.Hypothesis) {
	if len(hypotheses) == 0 {
		return
	}
	sort.Slice(hypotheses, func(i, j int) bool {
		return hypotheses[i].Score > hypotheses[j].Score
	})
	best := hypotheses[0].Score

	for i := range hypotheses {
		h := &hypotheses[i]
		chainLen := len(h.Chain)
		hasCompetitor := len(hypotheses) > 1 && hypotheses[1].Score >= best*0.8

		switch {
		case chainLen >= 3 && !hasCompetitor:
			h.Confidence = model.ConfidenceHigh
		case chainLen >= 2 || hasCompetitor:
			h.Confidence = model.ConfidenceMedium
		default:
			h.Confidence = model.ConfidenceSpeculative
		}
	}
}

// ─── Notable anomalies (no-trigger fallback) ──────────────────────────────────

// collectNotableAnomalies returns a list of the top change-point descriptions
// when no causal trigger is found. Gives the engineer a starting point.
func collectNotableAnomalies(inc model.Incident) []string {
	type scored struct {
		desc  string
		score float64
	}
	var all []scored
	for _, sig := range inc.Signals {
		for _, cp := range sig.ChangePoints {
			desc := fmt.Sprintf("%s on entity %s: %.1f× %s at %s (score %.2f)",
				sig.Metric, sig.EntityID, cp.Magnitude, cp.Direction,
				cp.At.UTC().Format("15:04:05Z"), cp.Score)
			all = append(all, scored{desc, cp.Score * cp.Magnitude})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	var out []string
	for i, s := range all {
		if i >= 5 {
			break
		}
		out = append(out, s.desc)
	}
	return out
}
