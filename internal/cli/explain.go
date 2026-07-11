package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// ruleExplanations maps rule IDs to their human-readable documentation.
// This map is the authoritative source for `rewind explain <RULEID>`.
// Each entry mirrors a file in docs/rules/<ID>.md.
var ruleExplanations = map[string]string{
	"RW001": `RW001 — Deploy-triggered anomaly

  A deployment event (or config change) on an entity is followed by one
  or more metric change-points on the same entity or its direct dependents
  within a 10-minute window.

  Scoring:
    - Gap 0–2m  → high temporal score
    - Gap 2–5m  → medium
    - Gap 5–10m → low
    - Gap >10m  → rule does not fire

  Confidence upgrade triggers:
    - ≥3 corroborating signals on the same entity
    - error.rate change-point present

  See: docs/rules/RW001.md`,

	"RW002": `RW002 — ConfigChange-triggered anomaly

  Identical to RW001 but triggered by a ConfigChange event rather than a
  Deploy. Config changes (e.g. feature flags, environment variable updates)
  are treated separately to keep postmortem evidence distinct.

  See: docs/rules/RW002.md`,

	"RW003": `RW003 — OOMKill cascade

  Pattern: memory.usage change-point ↑ → OOMKill event → Restart event(s)
  → error.rate ↑ on dependent services.

  All four steps must be present to score High confidence.
  Missing the error.rate propagation scores Medium.

  See: docs/rules/RW003.md`,

	"RW004": `RW004 — CPU saturation → latency degradation

  Pattern: cpu.usage or cpu.throttle change-point ↑ on an entity, followed
  within 5 minutes by a latency.p99 change-point ↑ on the same entity.

  This is the classic "CPU-starved process starts queuing requests" pattern.

  See: docs/rules/RW004.md`,

	"RW005": `RW005 — Upstream cascade

  Pattern: error.rate ↑ on service B (upstream of A), followed within 5
  minutes by error.rate ↑ on service A.

  Requires the topology graph (K8s service-to-service edges from Tempo or
  K8s NetworkPolicies) to establish the B→A dependency.

  See: docs/rules/RW005.md`,

	"RW006": `RW006 — Node pressure → pod eviction cascade

  Pattern: NodePressure or Eviction event on a Node entity, followed by
  PodKilled or Restart events on pods scheduled on that node, followed by
  service-level anomalies.

  See: docs/rules/RW006.md`,

	"RW007": `RW007 — Queue lag → consumer latency

  Pattern: queue.lag change-point ↑ on a Queue entity, followed within
  10 minutes by latency.p99 ↑ on the consumer service.

  See: docs/rules/RW007.md`,

	"RW008": `RW008 — Scale event → saturation

  Pattern: replica count change-point ↓ (or HPA maxed out = replicas
  constant while cpu.usage ↑), followed by cpu.usage / latency.p99 ↑.

  See: docs/rules/RW008.md`,

	"RW009": `RW009 — Crash loop detection (event coalescing)

  When ≥3 Restart events occur on the same pod within a 10-minute window,
  they are coalesced into a single CrashLoop event. The CrashLoop event is
  then used as a trigger candidate in other rules.

  This rule fires at the coalescing stage (before correlation), not as a
  correlation rule itself.

  See: docs/rules/RW009.md`,

	"RW010": `RW010 — Alert-as-symptom corroboration

  AlertFired events NEVER score as a causal trigger. Alerts are symptoms
  produced by monitoring systems after a condition is already ongoing.
  Instead, an AlertFired event overlapping an active hypothesis chain
  adds +0.1 to that hypothesis's score (corroboration).

  This prevents the common false pattern: "the alert triggered, therefore
  the alert caused the incident."

  See: docs/rules/RW010.md`,
}

func newExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain <RULE_ID>",
		Short: "Explain a correlation rule (e.g. rewind explain RW001)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if doc, ok := ruleExplanations[id]; ok {
				fmt.Fprintln(os.Stdout, doc)
				return nil
			}
			fmt.Fprintf(os.Stderr, "unknown rule %q\n\nKnown rules:\n", id)
			for k := range ruleExplanations {
				fmt.Fprintf(os.Stderr, "  %s\n", k)
			}
			os.Exit(ExitUsageError)
			return nil
		},
	}
}
