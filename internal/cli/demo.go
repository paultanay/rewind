package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rewind-io/rewind/internal/analyze"
	"github.com/rewind-io/rewind/internal/bundle"
	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/render/terminal"
	"github.com/rewind-io/rewind/internal/server"
)

func newDemoCmd() *cobra.Command {
	var (
		scenario string
		uiMode   bool
		port     int
		savePath string
	)

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Run an offline incident replay demo (no cluster required)",
		Long: `rewind demo replays a built-in golden incident scenario so you can see
the full analysis pipeline — event timeline, correlation engine, and
causal verdict — without any external infrastructure.

Available scenarios:
  bad-deploy     A bad deployment causes latency spike and error rate surge (default)
  oom-cascade    OOMKill triggers crash loop and downstream cascade
  node-pressure  Node memory pressure evicts pods across two services
  cpu-throttle   CPU throttling causes p99 latency degradation
  false-positive Noisy alerts with no real causal trigger (0 High hypotheses)

Examples:
  rewind demo                                    # bad-deploy, terminal
  rewind demo --scenario oom-cascade             # different scenario
  rewind demo --ui                               # open in web browser
  rewind demo --save demo.rewind                 # export bundle
  rewind demo --save demo.rewind --ui            # export then open UI
  rewind ui demo.rewind                          # replay saved bundle`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDemo(cmd.Context(), scenario, uiMode, port, savePath)
		},
	}

	cmd.Flags().StringVar(&scenario, "scenario", "bad-deploy",
		"scenario to replay: bad-deploy|oom-cascade|node-pressure|cpu-throttle|false-positive")
	cmd.Flags().BoolVar(&uiMode, "ui", false, "open the web UI instead of terminal output")
	cmd.Flags().IntVar(&port, "port", 7750, "port for --ui mode (0 = random)")
	cmd.Flags().StringVar(&savePath, "save", "",
		"export the demo incident as a .rewind bundle to this path")
	return cmd
}

func runDemo(ctx context.Context, scenario string, uiMode bool, port int, savePath string) error {
	inc, err := buildDemoIncident(scenario)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(ExitUsageError)
	}

	// Re-run the full analysis engine on the demo data.
	inc = analyze.Run(inc)

	fmt.Fprintf(os.Stderr, "\n  ⏪  rewind demo — scenario: %s\n\n", scenario)

	// Export bundle if --save was specified.
	if savePath != "" {
		if exportErr := bundle.Export(inc, nil, savePath); exportErr != nil {
			fmt.Fprintf(os.Stderr, "  warning: bundle export failed: %v\n", exportErr)
		} else {
			fmt.Fprintf(os.Stderr, "  bundle saved → %s\n", savePath)
			fmt.Fprintf(os.Stderr, "  replay with: rewind ui %s\n\n", savePath)
		}
	}

	if uiMode {
		srv, err := server.New(inc, port)
		if err != nil {
			return fmt.Errorf("demo ui: %w", err)
		}
		url := "http://" + srv.Addr()
		fmt.Fprintf(os.Stderr, "  UI → %s\n", url)
		fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop.\n\n")
		openBrowser(url)
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		return srv.Serve(ctx)
	}

	// Terminal mode.
	return terminal.Render(os.Stdout, inc, terminal.Options{Width: 120})
}

// buildDemoIncident constructs a synthetic incident for the requested scenario.
// All data is hard-coded — no network, no cluster, no files required.
func buildDemoIncident(scenario string) (model.Incident, error) {
	base := time.Date(2026, 7, 9, 14, 0, 0, 0, time.UTC)
	window := model.TimeRange{From: base, To: base.Add(45 * time.Minute)}
	now := time.Now().UTC()

	commonMeta := model.Meta{
		RewindVersion: rewindVersion,
		SchemaVersion: bundle.CurrentSchemaVersion,
		CreatedAt:     now,
	}

	switch scenario {
	case "bad-deploy":
		return badDeployScenario(base, window, commonMeta), nil
	case "oom-cascade":
		return oomCascadeScenario(base, window, commonMeta), nil
	case "node-pressure":
		return nodePressureScenario(base, window, commonMeta), nil
	case "cpu-throttle":
		return cpuThrottleScenario(base, window, commonMeta), nil
	case "false-positive":
		return falsePositiveScenario(base, window, commonMeta), nil
	default:
		return model.Incident{}, fmt.Errorf("unknown scenario %q — valid: bad-deploy, oom-cascade, node-pressure, cpu-throttle, false-positive", scenario)
	}
}

// ─── Scenario: bad-deploy ─────────────────────────────────────────────────────

func badDeployScenario(base time.Time, window model.TimeRange, meta model.Meta) model.Incident {
	svc := "svc/shop/checkout"
	return model.Incident{
		ID: "demo-bad-deploy-001", Window: window, Meta: meta,
		Scope: model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		Entities: []model.Entity{
			{ID: svc, Kind: model.EntityKindService, Labels: map[string]string{"app": "checkout", "namespace": "shop"}},
		},
		Events: []model.Event{
			{ID: "ev-1", At: base.Add(5 * time.Minute), Kind: model.EventKindDeploy,
				EntityID: svc, Severity: model.SeverityInfo,
				Title:  "Deployed checkout v2.3.1",
				Detail: "Image: checkout:v2.3.1 (sha256:f3a...)\nTriggered by: CI pipeline #8821\nRollout: 3/3 pods updated",
				SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-2", At: base.Add(13 * time.Minute), Kind: model.EventKindAlertFired,
				EntityID: svc, Severity: model.SeverityNotable,
				Title:    "HighLatency: checkout p99 > 2s",
				SourceRef: model.SourceRef{SourceName: "alertmanager"}},
			{ID: "ev-3", At: base.Add(14 * time.Minute), Kind: model.EventKindAlertFired,
				EntityID: svc, Severity: model.SeverityCritical,
				Title:    "HighErrorRate: checkout error rate > 5%",
				SourceRef: model.SourceRef{SourceName: "alertmanager"}},
			{ID: "ev-4", At: base.Add(16 * time.Minute), Kind: "LOG-BURST",
				EntityID: svc, Severity: model.SeverityNotable,
				Title:  "Log error burst on checkout (18.3 errors/s peak)",
				Detail: "ERR connection pool exhausted after 30s\nERROR database timeout waiting for connection\nFATAL unable to acquire DB connection after 3 retries\nERR HTTP 503 from upstream\nERROR request failed: context deadline exceeded",
				SourceRef: model.SourceRef{SourceName: "loki"}},
		},
		Signals: []model.Signal{
			demoSignal("prom-lat-"+svc, svc, model.MetricLatencyP99, "ms",
				base, 45, func(i int) float64 {
					if i < 10 { return 180 + float64(i)*3 }
					if i < 15 { return 220 + float64(i-10)*280 } // spike after deploy at 5m
					return 1650 - float64(i-15)*15
				}),
			demoSignal("prom-err-"+svc, svc, model.MetricErrorRate, "ratio",
				base, 45, func(i int) float64 {
					if i < 11 { return 0.002 }
					if i < 16 { return 0.002 + float64(i-11)*0.025 }
					return 0.12 - float64(i-16)*0.003
				}),
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, EventCount: 0, SignalCount: 2, Duration: "1.2s"},
			{Name: "kubernetes", Status: model.SourceStatusOK, EventCount: 1, SignalCount: 0, Duration: "0.8s"},
			{Name: "alertmanager", Status: model.SourceStatusOK, EventCount: 2, SignalCount: 0, Duration: "0.3s"},
			{Name: "loki", Status: model.SourceStatusOK, EventCount: 1, SignalCount: 1, Duration: "0.9s"},
		},
	}
}

// ─── Scenario: oom-cascade ────────────────────────────────────────────────────

func oomCascadeScenario(base time.Time, window model.TimeRange, meta model.Meta) model.Incident {
	svc := "svc/shop/checkout"
	pod := "pod/shop/checkout-7d9f-xk2p9"
	return model.Incident{
		ID: "demo-oom-cascade-001", Window: window, Meta: meta,
		Scope: model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		Entities: []model.Entity{
			{ID: svc, Kind: model.EntityKindService, Labels: map[string]string{"app": "checkout"}},
			{ID: pod, Kind: model.EntityKindPod, Labels: map[string]string{"app": "checkout", "pod": "checkout-7d9f-xk2p9"}},
		},
		Events: []model.Event{
			{ID: "ev-oom-1", At: base.Add(8 * time.Minute), Kind: model.EventKindOOMKill,
				EntityID: pod, Severity: model.SeverityCritical,
				Title:  "OOMKilled: checkout-7d9f-xk2p9",
				Detail: "Container: checkout\nLimit: 512Mi\nUsage at kill: 511Mi (99.8%)\nReason: memory.limit_in_bytes exceeded",
				SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-oom-2", At: base.Add(9 * time.Minute), Kind: model.EventKindRestart,
				EntityID: pod, Severity: model.SeverityNotable,
				Title: "Pod restarted (restart #1)", SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-oom-3", At: base.Add(11 * time.Minute), Kind: model.EventKindRestart,
				EntityID: pod, Severity: model.SeverityNotable,
				Title: "Pod restarted (restart #2)", SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-oom-4", At: base.Add(13 * time.Minute), Kind: model.EventKindRestart,
				EntityID: pod, Severity: model.SeverityNotable,
				Title: "Pod restarted (restart #3)", SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-oom-5", At: base.Add(14 * time.Minute), Kind: model.EventKindAlertFired,
				EntityID: svc, Severity: model.SeverityCritical,
				Title:    "HighErrorRate: checkout error rate > 5%",
				SourceRef: model.SourceRef{SourceName: "alertmanager"}},
		},
		Signals: []model.Signal{
			// Memory signal on the pod: perfectly flat baseline then extreme step-change.
			// 0.10 → 0.95 at t=3 ensures both CUSUM and PELT fire with score ≈0.95.
			demoSignal("mem-"+pod, pod, model.MetricMemoryUsage, "ratio",
				base, 45, func(i int) float64 {
					switch {
					case i < 3:
						return 0.10 // flat, very low baseline
					case i < 8:
						return 0.10 + float64(i-3)*0.17 // rapid rise: 0.10→0.95
					case i < 10:
						return 0.22 // drops post-restart (counter reset)
					default:
						return 0.25 + float64(i%4)*0.04 // stable post-crash oscillation
					}
				}),
			// Error-rate on pod spikes sharply right after OOMKill (t=8 →0.40).
			demoSignal("err-"+pod, pod, model.MetricErrorRate, "ratio",
				base, 45, func(i int) float64 {
					switch {
					case i < 8:
						return 0.001 // flat baseline
					case i < 13:
						return 0.001 + float64(i-8)*0.08 // sharp rise: 0.001→0.401
					default:
						return 0.38 - float64(i-13)*0.009
					}
				}),
			// Restarts counter on svc for RW009 crash-loop detection.
			demoSignal("rst-"+svc, svc, model.MetricRestarts, "count",
				base, 45, func(i int) float64 {
					if i < 9 { return 0 }
					return float64(i - 8)
				}),
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, EventCount: 0, SignalCount: 3, Duration: "1.1s"},
			{Name: "kubernetes", Status: model.SourceStatusOK, EventCount: 4, SignalCount: 0, Duration: "0.7s"},
			{Name: "alertmanager", Status: model.SourceStatusOK, EventCount: 1, SignalCount: 0, Duration: "0.3s"},
		},
	}
}

// ─── Scenario: node-pressure ──────────────────────────────────────────────────

func nodePressureScenario(base time.Time, window model.TimeRange, meta model.Meta) model.Incident {
	node := "node/worker-2"
	svc := "svc/shop/checkout"
	pod := "pod/shop/checkout-7d9f-abc"
	return model.Incident{
		ID: "demo-node-pressure-001", Window: window, Meta: meta,
		Scope: model.Scope{Namespaces: []string{"shop"}},
		Entities: []model.Entity{
			{ID: node, Kind: model.EntityKindNode, Labels: map[string]string{"node": "worker-2"}},
			{ID: svc, Kind: model.EntityKindService, Labels: map[string]string{"app": "checkout", "namespace": "shop"}},
			// Owner must be the node ID so topology.Build() wires node → pod edge,
			// which RW006 traverses via ctx.Graph.Adjacent(nodeEvent.EntityID).
			{ID: pod, Kind: model.EntityKindPod, Owner: node,
				Labels: map[string]string{"app": "checkout", "node": "worker-2"}},
		},
		Events: []model.Event{
			{ID: "ev-np-1", At: base.Add(3 * time.Minute), Kind: model.EventKindNodePressure,
				EntityID: node, Severity: model.SeverityCritical,
				Title:  "NodeMemoryPressure: worker-2",
				Detail: "Available memory: 142Mi / 8Gi (1.7%)\nCondition: MemoryPressure=True\nEviction threshold: 100Mi",
				SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-np-2", At: base.Add(4 * time.Minute), Kind: model.EventKindPodKilled,
				EntityID: pod, Severity: model.SeverityNotable,
				Title:     "Pod evicted: checkout-7d9f-abc (node memory pressure)",
				Detail:    "Node: worker-2\nReason: Evicted (MemoryPressure)\nMessage: The node was low on resource: memory",
				SourceRef: model.SourceRef{SourceName: "kubernetes"}},
			{ID: "ev-np-3", At: base.Add(15 * time.Minute), Kind: model.EventKindAlertFired,
				EntityID: svc, Severity: model.SeverityCritical,
				Title:    "HighErrorRate: checkout > 10% for 5m",
				SourceRef: model.SourceRef{SourceName: "alertmanager"}},
		},
		Signals: []model.Signal{
			// Error-rate on the pod entity: flat ~0 then abrupt jump at t=4.
			// 0.001 → 0.45 in 5 steps — magnitude >100x — guaranteed CP.
			demoSignal("err-"+pod, pod, model.MetricErrorRate, "ratio",
				base, 45, func(i int) float64 {
					switch {
					case i < 4:
						return 0.001 // flat baseline
					case i < 9:
						return 0.001 + float64(i-4)*0.09 // 0.001→0.451
					default:
						return 0.42 - float64(i-9)*0.008
					}
				}),
			// Latency on svc also spikes post-eviction. Gives RW006 two confirming signals.
			demoSignal("lat-"+svc, svc, model.MetricLatencyP99, "ms",
				base, 45, func(i int) float64 {
					switch {
					case i < 4:
						return 90.0 // flat baseline
					case i < 10:
						return 90 + float64(i-4)*200 // 90→1290ms
					default:
						return 1100 - float64(i-10)*20
					}
				}),
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, EventCount: 0, SignalCount: 2, Duration: "0.9s"},
			{Name: "kubernetes", Status: model.SourceStatusOK, EventCount: 2, SignalCount: 0, Duration: "0.6s"},
			{Name: "alertmanager", Status: model.SourceStatusOK, EventCount: 1, SignalCount: 0, Duration: "0.3s"},
		},
	}
}

// ─── Scenario: cpu-throttle ───────────────────────────────────────────────────

func cpuThrottleScenario(base time.Time, window model.TimeRange, meta model.Meta) model.Incident {
	svc := "svc/shop/recommendation"
	return model.Incident{
		ID: "demo-cpu-throttle-001", Window: window, Meta: meta,
		Scope: model.Scope{Namespaces: []string{"shop"}, Services: []string{"recommendation"}},
		Entities: []model.Entity{
			{ID: svc, Kind: model.EntityKindService, Labels: map[string]string{"app": "recommendation"}},
		},
		Events: []model.Event{},
		Signals: []model.Signal{
			demoSignal("cpu-"+svc, svc, model.MetricCPUThrottle, "ratio",
				base, 45, func(i int) float64 {
					if i < 10 { return 0.02 }
					if i < 14 { return 0.02 + float64(i-10)*0.18 }
					return 0.72
				}),
			demoSignal("lat-"+svc, svc, model.MetricLatencyP99, "ms",
				base, 45, func(i int) float64 {
					if i < 12 { return 95 + float64(i)*2 }
					if i < 18 { return 110 + float64(i-12)*85 }
					return 600
				}),
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, EventCount: 0, SignalCount: 2, Duration: "1.0s"},
		},
	}
}

// ─── Scenario: false-positive ─────────────────────────────────────────────────

func falsePositiveScenario(base time.Time, window model.TimeRange, meta model.Meta) model.Incident {
	svc := "svc/shop/frontend"
	return model.Incident{
		ID: "demo-false-positive-001", Window: window, Meta: meta,
		Scope: model.Scope{Namespaces: []string{"shop"}, Services: []string{"frontend"}},
		Entities: []model.Entity{
			{ID: svc, Kind: model.EntityKindService, Labels: map[string]string{"app": "frontend"}},
		},
		Events: []model.Event{
			{ID: "ev-fp-1", At: base.Add(10 * time.Minute), Kind: model.EventKindAlertFired,
				EntityID: svc, Severity: model.SeverityNotable,
				Title:    "HighLatency: frontend p99 > 1s (brief spike)",
				Detail:   "Duration: 42 seconds. Auto-resolved.",
				SourceRef: model.SourceRef{SourceName: "alertmanager"}},
		},
		Signals: []model.Signal{
			demoSignal("lat-"+svc, svc, model.MetricLatencyP99, "ms",
				base, 45, func(i int) float64 {
					// Brief spike, no change-point.
					if i == 10 || i == 11 { return 1100 }
					return 200 + float64(i%3)*10
				}),
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, EventCount: 0, SignalCount: 1, Duration: "0.8s"},
			{Name: "alertmanager", Status: model.SourceStatusOK, EventCount: 1, SignalCount: 0, Duration: "0.3s"},
		},
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// demoSignal generates a Signal with one Point per minute over n minutes,
// using fn(minute_index) to compute each value.
func demoSignal(id, entityID, metric, unit string, base time.Time, n int, fn func(int) float64) model.Signal {
	pts := make([]model.Point, n)
	for i := 0; i < n; i++ {
		pts[i] = model.Point{T: base.Add(time.Duration(i) * time.Minute), V: fn(i)}
	}
	return model.Signal{ID: id, EntityID: entityID, Metric: metric, Unit: unit, Points: pts}
}
