package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// ruleDoc is the structured documentation entry for one correlation rule.
type ruleDoc struct {
	ID          string
	Name        string
	Hypothesis  string
	Description string
	Signals     []string
	Events      []string
	Window      string
	ScoreRange  string
	Example     string
	Reference   string
}

// ruleExplanations is the authoritative per-rule reference for `rewind explain`.
// Each entry mirrors docs/rules/<ID>.md.
var ruleExplanations = map[string]ruleDoc{
	"RW001": {
		ID:         "RW001",
		Name:       "Deploy → Metric Change-Point",
		Hypothesis: "A deployment or config change is the root cause of observed metric degradation.",
		Description: `Scores a Deploy event as the trigger when one or more metric signals
show a statistically significant change-point within 10 minutes of the
deployment completing. Each corroborating metric (latency, error rate,
memory, CPU) increases the score multiplicatively.

Scoring gaps:
  0–2 min  → temporal score 1.0   (high proximity)
  2–5 min  → temporal score 0.7   (medium)
  5–10 min → temporal score 0.4   (low)
  > 10 min → rule does not fire`,
		Signals:    []string{"latency.p99", "error.rate", "memory.usage", "cpu.usage"},
		Events:     []string{"DEPLOY"},
		Window:     "10 minutes after deploy",
		ScoreRange: "0.40–0.95 (higher with more corroborating signals)",
		Example: `checkout v2.3.1 deploys at 14:20. latency.p99 shows change-point at
14:28 (4.5× rise) and error.rate at 14:30 (36× rise). Two corroborating
signals → RW001 fires with HIGH confidence.`,
		Reference: "Google SRE Book §22: Change Management; spec §10 RW001",
	},
	"RW002": {
		ID:         "RW002",
		Name:       "Config Change → Metric Change-Point",
		Hypothesis: "A configuration change caused the observed degradation.",
		Description: `Identical logic to RW001 but triggered by EventKindConfigChange.
Config changes (feature flags, env var updates, quota changes) are
tracked separately to keep postmortem evidence distinct from code
deploys. The window is extended to 15 minutes because config
effects often propagate more slowly.`,
		Signals:    []string{"latency.p99", "error.rate"},
		Events:     []string{"CONFIG"},
		Window:     "15 minutes after config change",
		ScoreRange: "0.35–0.85",
		Example: `Feature flag 'new-payment-provider' enabled at 09:00. Error rate on
payments climbs 8× by 09:12. RW002 names the config change.`,
		Reference: "Spec §10 RW002; LaunchDarkly feature flag best practices",
	},
	"RW003": {
		ID:         "RW003",
		Name:       "OOMKill Evidence",
		Hypothesis: "A container ran out of memory, was OOMKilled, and caused service disruption.",
		Description: `Scores an OOMKill event as the trigger when the entity's memory usage
signal shows a rising trend in the 10 minutes before the kill. Restart
events following the OOMKill are counted as corroboration. Effective
for detecting slow memory leaks that build up over hours.

Full chain required for HIGH confidence:
  memory.usage ↑ → OOMKill → Restart → error.rate ↑ (downstream)`,
		Signals:    []string{"memory.usage"},
		Events:     []string{"OOMKILL", "RESTART"},
		Window:     "10 minutes before OOMKill",
		ScoreRange: "0.50–0.90",
		Example: `Pod checkout-7d9f: memory rises 55%→91% over 8 min, then OOMKilled.
Downstream error.rate spikes. RW003 fires (score 0.85).`,
		Reference: "Linux OOM Killer; cgroup memory.limit_in_bytes; Kubernetes eviction",
	},
	"RW004": {
		ID:         "RW004",
		Name:       "CPU Saturation → Latency Increase",
		Hypothesis: "CPU saturation or throttling caused latency to increase.",
		Description: `CPU change-point must strictly precede the latency change-point on
the same entity. Score is discounted when the CPU magnitude increase
is small (< 50%) because sub-saturation CPU changes rarely cause
detectable latency degradation.`,
		Signals:    []string{"cpu.usage", "cpu.throttle", "latency.p99", "latency.p95"},
		Events:     []string{},
		Window:     "CPU CP must precede latency CP by ≤ 5 minutes",
		ScoreRange: "0.30–0.70",
		Example: `Recommendation service latency doubles. cpu.throttle shows 3× increase
2 minutes earlier. RW004 names CPU saturation as hypothesis.`,
		Reference: "Linux CFS scheduler; cgroup cpu.cfs_quota_us throttle; BPF CPU profiling",
	},
	"RW005": {
		ID:         "RW005",
		Name:       "Upstream Service Cascade",
		Hypothesis: "An upstream service failure propagated downstream via synchronous calls.",
		Description: `Requires a call-graph topology (from Tempo traces or static config).
Upstream service's error.rate change-point must precede the downstream
entity's CP by ≤ 15 minutes AND a call path downstream→upstream must
exist in the topology graph.

Direction note: "upstream" means the service that is called, not the
caller. If checkout calls payments, payments is upstream of checkout.`,
		Signals:    []string{"error.rate", "latency.p99"},
		Events:     []string{},
		Window:     "Upstream CP must precede downstream CP by ≤ 15 minutes",
		ScoreRange: "0.30–0.65",
		Example: `payments error.rate spikes at 14:05. checkout error.rate spikes at
14:07. Topology: checkout→payments. RW005 names payments failure.`,
		Reference: "Netflix Hystrix; Google SRE Book §23: Managing Critical State",
	},
	"RW006": {
		ID:         "RW006",
		Name:       "Node Pressure → Pod Eviction",
		Hypothesis: "Node memory or disk pressure caused pod eviction, disrupting the service.",
		Description: `Scores a NodePressure event as the trigger when it is followed within
5 minutes by PodKilled (eviction) events on pods owned by that node.
Topology adjacency bonus is applied when evicted pods belong to
services that then show metric degradation.`,
		Signals:    []string{"memory.usage", "disk.io"},
		Events:     []string{"NODE-PRESSURE", "KILLED"},
		Window:     "5 minutes after NodePressure event",
		ScoreRange: "0.50–0.85",
		Example: `worker-2 reports NodeMemoryPressure at 14:23. Two checkout pods
evicted at 14:24. Both service error rates spike. RW006 fires.`,
		Reference: "Kubernetes node conditions: MemoryPressure, DiskPressure, PIDPressure",
	},
	"RW007": {
		ID:         "RW007",
		Name:       "Probe Failure → Restart Loop",
		Hypothesis: "A failing liveness or readiness probe caused repeated pod restarts.",
		Description: `Links ProbeFailed events (liveness/readiness probe timeouts) to
subsequent Restart events on the same pod. Useful for diagnosing
probe misconfiguration after code changes that increase startup time
or introduce blocking in health-check endpoints.`,
		Signals:    []string{"restarts"},
		Events:     []string{"PROBE-FAIL", "RESTART"},
		Window:     "Probe failures must precede restarts by ≤ 3 minutes",
		ScoreRange: "0.45–0.80",
		Example: `New API version increases cold-start to 25s. Liveness probe (10s
timeout) fails → pod restarted repeatedly. RW007 fires.`,
		Reference: "Kubernetes probes: initialDelaySeconds, failureThreshold, periodSeconds",
	},
	"RW008": {
		ID:         "RW008",
		Name:       "Log Burst → Error Spike Correlation",
		Hypothesis: "A burst of error-level log messages coincided with metric degradation.",
		Description: `Links a Loki LogBurst event to a metric error.rate change-point on
the same entity within 5 minutes. Acts as a corroborating bridge —
adds evidence to an existing hypothesis (RW001, RW005, etc.) rather
than creating a standalone trigger. The rule is corroboration-only
(CorroborationOnly=true).`,
		Signals:    []string{"error.rate", "log.error.rate"},
		Events:     []string{"LOG-BURST"},
		Window:     "5 minutes (LogBurst ↔ error.rate CP)",
		ScoreRange: "0.20–0.50 (corroborating only; additive)",
		Example: `A deploy (RW001 trigger) is corroborated by a Loki burst showing
'connection pool exhausted' errors at the same timestamp.`,
		Reference: "Spec §8 Loki design constraints; §10 RW008 corroboration-only",
	},
	"RW009": {
		ID:         "RW009",
		Name:       "CrashLoop Detection",
		Hypothesis: "A pod is in CrashLoopBackOff, causing repeated restarts and unavailability.",
		Description: `When ≥3 pod Restart events occur on the same entity within a 10-minute
sliding window, they are coalesced into a single synthetic CrashLoop
event injected into the timeline. This synthetic event is then scored
as a trigger candidate. Prevents individual restart events from
fragmenting the causal chain into noise.`,
		Signals:    []string{"restarts"},
		Events:     []string{"RESTART"},
		Window:     "10-minute sliding window; threshold ≥ 3 restarts",
		ScoreRange: "0.60–0.85",
		Example: `checkout-7d9f restarts 4× in 8 min. RW009 synthesises CRASH-LOOP at
first restart timestamp and scores it as trigger for the error spike.`,
		Reference: "Kubernetes CrashLoopBackOff; exponential back-off restart policy",
	},
	"RW010": {
		ID:         "RW010",
		Name:       "Alert Corroboration (Never Trigger)",
		Hypothesis: "An Alertmanager alert is evidence of impact — never a root cause.",
		Description: `AlertFired events NEVER score as causal triggers. Alerts are symptoms
produced by monitoring after a condition is already ongoing. RW010
adds +0.10 to an existing hypothesis's score when an alert fires
within 2 minutes of any link in that hypothesis's causal chain.

This prevents the tautological verdict: "the alert fired because
the alert fired." Invariant enforced at the assembler level.`,
		Signals:    []string{},
		Events:     []string{"ALERT"},
		Window:     "2 minutes overlap with any chain link",
		ScoreRange: "0.00–0.20 (additive corroboration only)",
		Example: `"HighErrorRate" alert fires at 14:32. RW001 already found the deploy
as trigger at 14:20. RW010 adds +0.10 to RW001's confidence score.`,
		Reference: "Spec §10 invariant: 'Alerts are symptoms, not triggers'",
	},
}

func newExplainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "explain [RULE_ID]",
		Short: "Explain a correlation rule in detail",
		Long: `Print the full documentation for a correlation rule (e.g. RW001–RW010).
Without an argument, lists all rules with one-line summaries.

Examples:
  rewind explain             # list all rules
  rewind explain RW001       # deploy → metric change-point
  rewind explain RW009       # crash loop detection
  rewind explain RW010       # alert corroboration (never trigger)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				printRuleList()
				return nil
			}
			return printRuleDetail(strings.ToUpper(args[0]))
		},
	}
	return cmd
}

// printRuleList prints the one-line summary table for all rules.
func printRuleList() {
	fmt.Printf("%-8s  %-38s  %s\n", "RULE", "NAME", "HYPOTHESIS (BRIEF)")
	fmt.Println(strings.Repeat("─", 92))
	ids := []string{"RW001", "RW002", "RW003", "RW004", "RW005", "RW006", "RW007", "RW008", "RW009", "RW010"}
	for _, id := range ids {
		doc, ok := ruleExplanations[id]
		if !ok {
			continue
		}
		brief := doc.Hypothesis
		if len(brief) > 55 {
			brief = brief[:54] + "…"
		}
		fmt.Printf("%-8s  %-38s  %s\n", doc.ID, doc.Name, brief)
	}
	fmt.Println()
	fmt.Println("Run `rewind explain <RULE_ID>` for full documentation.")
}

// printRuleDetail prints the full structured documentation for one rule.
func printRuleDetail(id string) error {
	doc, ok := ruleExplanations[id]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown rule %q\n\nKnown rules: RW001–RW010\n", id)
		fmt.Fprintln(os.Stderr, "Run `rewind explain` to list all rules.")
		os.Exit(ExitUsageError)
		return nil
	}

	sep := strings.Repeat("─", 72)
	fmt.Println(sep)
	fmt.Printf("  %s  %s\n", doc.ID, doc.Name)
	fmt.Println(sep)
	fmt.Printf("\n  Hypothesis\n  %s\n\n", doc.Hypothesis)
	fmt.Println("  Description")
	for _, line := range strings.Split(doc.Description, "\n") {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	if len(doc.Signals) > 0 {
		fmt.Printf("  Signals required  : %s\n", strings.Join(doc.Signals, ", "))
	}
	if len(doc.Events) > 0 {
		fmt.Printf("  Events required   : %s\n", strings.Join(doc.Events, ", "))
	}
	fmt.Printf("  Temporal window   : %s\n", doc.Window)
	fmt.Printf("  Score range       : %s\n", doc.ScoreRange)
	fmt.Printf("\n  Example\n")
	for _, line := range strings.Split(doc.Example, "\n") {
		fmt.Printf("  %s\n", line)
	}
	fmt.Printf("\n  Reference: %s\n", doc.Reference)
	fmt.Println(sep)
	return nil
}
