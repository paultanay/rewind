//go:build ignore

// make_fixture.go generates the golden .rewind bundles in testdata/incidents/.
// Run: go run testdata/make_fixture.go
//
// Scenarios produced:
//
//	bad-deploy.rewind        — Deploy → latency/error/memory spikes (Phase 4: RW001+RW003)
//	oom-cascade.rewind       — Memory spike → OOMKill → restart → error.rate cascade (RW003)
//	node-failure.rewind      — NodePressure → pod evictions → service errors (RW006)
//	noisy-no-incident.rewind — Stable noisy signals + no events (Phase 4: NoTriggerFound)
package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/rewind-io/rewind/internal/analyze"
	"github.com/rewind-io/rewind/internal/bundle"
	"github.com/rewind-io/rewind/internal/model"
)

var base = time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
var step = time.Minute

func pts(vals []float64, origin time.Time) []model.Point {
	p := make([]model.Point, len(vals))
	for i, v := range vals {
		p[i] = model.Point{T: origin.Add(time.Duration(i) * step), V: v}
	}
	return p
}

func noise(base float64, n int, sigma float64) []float64 {
	v := make([]float64, n)
	var seed uint32 = 99
	for i := range v {
		seed = 1664525*seed + 1013904223
		f := float64(seed)/math.MaxUint32*2 - 1
		v[i] = base + f*sigma
	}
	return v
}

func stepVals(v1 float64, n1 int, v2 float64, n2 int) []float64 {
	v := make([]float64, n1+n2)
	for i := 0; i < n1; i++ {
		v[i] = v1
	}
	for i := n1; i < n1+n2; i++ {
		v[i] = v2
	}
	return v
}

// ─── Scenario builders ────────────────────────────────────────────────────────

func badDeploy() model.Incident {
	return model.Incident{
		ID:     "golden-bad-deploy",
		Window: model.TimeRange{From: base, To: base.Add(45 * time.Minute)},
		Scope:  model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		Entities: []model.Entity{
			{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
			{ID: "deploy/shop/checkout", Kind: model.EntityKindDeployment, Owner: "svc/shop/checkout"},
		},
		Events: []model.Event{
			{
				ID: "evt-deploy", At: base, Kind: model.EventKindDeploy,
				EntityID: "deploy/shop/checkout", Severity: model.SeverityNotable,
				Title:  "Deployed checkout v2.3.1",
				Detail: "Image: checkout:v2.3.1 (was v2.3.0)\nAuthor: alice\nCommit: a1b2c3d",
				SourceRef: model.SourceRef{SourceName: "github",
					URL: "https://github.com/example/checkout/deployments/42"},
			},
			{
				ID: "evt-oom", At: base.Add(80 * time.Second), Kind: model.EventKindOOMKill,
				EntityID: "deploy/shop/checkout", Severity: model.SeverityCritical,
				Title: "OOMKilled: checkout-7d9f-abc12",
			},
		},
		Signals: []model.Signal{
			{ID: "sig-lat", EntityID: "svc/shop/checkout", Metric: model.MetricLatencyP99, Unit: "ms",
				Points: pts(stepVals(40, 10, 180, 35), base), Baseline: pts(noise(40, 45, 2), base.Add(-45*time.Minute))},
			{ID: "sig-err", EntityID: "svc/shop/checkout", Metric: model.MetricErrorRate, Unit: "ratio",
				Points: pts(stepVals(0.005, 12, 0.18, 33), base), Baseline: pts(noise(0.005, 45, 0.001), base.Add(-45*time.Minute))},
			{ID: "sig-mem", EntityID: "svc/shop/checkout", Metric: model.MetricMemoryUsage, Unit: "%",
				Points: pts(stepVals(55, 8, 92, 37), base), Baseline: pts(noise(55, 45, 2), base.Add(-45*time.Minute))},
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "1.1s", SignalCount: 3},
			{Name: "github", Status: model.SourceStatusOK, Duration: "0.3s", EventCount: 1},
			{Name: "kubernetes", Status: model.SourceStatusOK, Duration: "0.4s", EventCount: 1},
		},
		Meta: model.Meta{RewindVersion: "0.1.0", SchemaVersion: bundle.CurrentSchemaVersion, CreatedAt: base},
	}
}

func oomCascade() model.Incident {
	// Scenario: memory climbs steadily → OOMKill → crash loop → error.rate spikes
	// No deploy event — pure resource exhaustion
	return model.Incident{
		ID:     "golden-oom-cascade",
		Window: model.TimeRange{From: base, To: base.Add(45 * time.Minute)},
		Scope:  model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		Entities: []model.Entity{
			{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
			{ID: "pod/shop/checkout-7d9f", Kind: model.EntityKindPod, Owner: "svc/shop/checkout"},
		},
		Events: []model.Event{
			{
				ID: "evt-oom1", At: base.Add(8 * time.Minute), Kind: model.EventKindOOMKill,
				EntityID: "pod/shop/checkout-7d9f", Severity: model.SeverityCritical,
				Title: "OOMKilled: checkout-7d9f (container checkout)",
			},
			{
				ID: "evt-restart1", At: base.Add(8*time.Minute + 5*time.Second), Kind: model.EventKindRestart,
				EntityID: "pod/shop/checkout-7d9f", Severity: model.SeverityNotable,
				Title: "Restarting: checkout-7d9f (restart #1)",
			},
			{
				ID: "evt-restart2", At: base.Add(8*time.Minute + 40*time.Second), Kind: model.EventKindRestart,
				EntityID: "pod/shop/checkout-7d9f", Severity: model.SeverityNotable,
				Title: "Restarting: checkout-7d9f (restart #2)",
			},
			{
				ID: "evt-crash", At: base.Add(10 * time.Minute), Kind: model.EventKindCrashLoop,
				EntityID: "pod/shop/checkout-7d9f", Severity: model.SeverityCritical,
				Title: "CrashLoopBackOff: checkout-7d9f (3 restarts in 2m)",
			},
		},
		Signals: func() []model.Signal {
			// Memory ramps up gradually then falls after OOMKill
			memVals := make([]float64, 45)
			for i := range memVals {
				if i < 8 {
					memVals[i] = 55 + float64(i)*4.5 // 55% → 91%
				} else if i < 12 {
					memVals[i] = 40 + float64(i-8)*10 // recovery/re-leak cycle
				} else {
					memVals[i] = 70 + float64(i-12)*1.5 // steady climb again
				}
			}
			errVals := stepVals(0.005, 10, 0.22, 35)
			return []model.Signal{
				{ID: "sig-mem", EntityID: "svc/shop/checkout", Metric: model.MetricMemoryUsage, Unit: "%",
					Points: pts(memVals, base), Baseline: pts(noise(55, 45, 2), base.Add(-45*time.Minute))},
				{ID: "sig-err", EntityID: "svc/shop/checkout", Metric: model.MetricErrorRate, Unit: "ratio",
					Points: pts(errVals, base), Baseline: pts(noise(0.005, 45, 0.001), base.Add(-45*time.Minute))},
				{ID: "sig-rst", EntityID: "svc/shop/checkout", Metric: model.MetricRestarts, Unit: "count",
					Points: pts(stepVals(0, 8, 3, 37), base), Baseline: pts(noise(0, 45, 0), base.Add(-45*time.Minute))},
			}
		}(),
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "1.3s", SignalCount: 3},
			{Name: "kubernetes", Status: model.SourceStatusOK, Duration: "0.5s", EventCount: 4},
		},
		Meta: model.Meta{RewindVersion: "0.1.0", SchemaVersion: bundle.CurrentSchemaVersion, CreatedAt: base},
	}
}

func nodeFailure() model.Incident {
	// Scenario: Node memory pressure → pod eviction on two services → error rates spike
	return model.Incident{
		ID:     "golden-node-failure",
		Window: model.TimeRange{From: base, To: base.Add(45 * time.Minute)},
		Scope:  model.Scope{Namespaces: []string{"shop"}},
		Entities: []model.Entity{
			{ID: "node/worker-2", Kind: model.EntityKindNode},
			{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
			{ID: "svc/shop/frontend", Kind: model.EntityKindService, DisplayName: "frontend"},
			{ID: "pod/shop/checkout-node2", Kind: model.EntityKindPod, Owner: "svc/shop/checkout"},
			{ID: "pod/shop/frontend-node2", Kind: model.EntityKindPod, Owner: "svc/shop/frontend"},
		},
		Events: []model.Event{
			{
				ID: "evt-pressure", At: base.Add(3 * time.Minute), Kind: model.EventKindNodePressure,
				EntityID: "node/worker-2", Severity: model.SeverityCritical,
				Title:  "NodeMemoryPressure: worker-2",
				Detail: "Node is under memory pressure: 97% used",
			},
			{
				ID: "evt-evict1", At: base.Add(4 * time.Minute), Kind: model.EventKindPodKilled,
				EntityID: "pod/shop/checkout-node2", Severity: model.SeverityCritical,
				Title:  "Evicted: checkout-node2 from worker-2",
				Detail: "The node was low on resource: memory.",
			},
			{
				ID: "evt-evict2", At: base.Add(4*time.Minute + 15*time.Second), Kind: model.EventKindPodKilled,
				EntityID: "pod/shop/frontend-node2", Severity: model.SeverityCritical,
				Title: "Evicted: frontend-node2 from worker-2",
			},
		},
		Signals: []model.Signal{
			{ID: "sig-co-err", EntityID: "svc/shop/checkout", Metric: model.MetricErrorRate, Unit: "ratio",
				Points: pts(stepVals(0.01, 5, 0.35, 40), base), Baseline: pts(noise(0.01, 45, 0.002), base.Add(-45*time.Minute))},
			{ID: "sig-fe-err", EntityID: "svc/shop/frontend", Metric: model.MetricErrorRate, Unit: "ratio",
				Points: pts(stepVals(0.01, 5, 0.28, 40), base), Baseline: pts(noise(0.01, 45, 0.002), base.Add(-45*time.Minute))},
			{ID: "sig-co-lat", EntityID: "svc/shop/checkout", Metric: model.MetricLatencyP99, Unit: "ms",
				Points: pts(stepVals(45, 4, 280, 41), base), Baseline: pts(noise(45, 45, 3), base.Add(-45*time.Minute))},
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "1.4s", SignalCount: 3},
			{Name: "kubernetes", Status: model.SourceStatusOK, Duration: "0.6s", EventCount: 3},
		},
		Meta: model.Meta{RewindVersion: "0.1.0", SchemaVersion: bundle.CurrentSchemaVersion, CreatedAt: base},
	}
}

func noisyNoIncident() model.Incident {
	// Scenario: business-hours traffic ramp — no real incident.
	// Phase 4 must produce NoTriggerFound with zero High-confidence hypotheses.
	return model.Incident{
		ID:     "golden-noisy-no-incident",
		Window: model.TimeRange{From: base, To: base.Add(45 * time.Minute)},
		Scope:  model.Scope{Namespaces: []string{"shop"}},
		Entities: []model.Entity{
			{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
		},
		Events: []model.Event{}, // no events
		Signals: func() []model.Signal {
			// Gradual traffic ramp — not a step change, just morning traffic
			rampVals := make([]float64, 45)
			var seed uint32 = 777
			for i := range rampVals {
				seed = 1664525*seed + 1013904223
				noise := float64(seed)/math.MaxUint32*2 - 1
				rampVals[i] = 40 + float64(i)*0.5 + noise*3 // 40ms→62ms gentle ramp
			}
			errVals := make([]float64, 45)
			seed = 888
			for i := range errVals {
				seed = 1664525*seed + 1013904223
				noise := float64(seed)/math.MaxUint32*2 - 1
				errVals[i] = 0.01 + noise*0.002 // stable ~1% error rate
			}
			return []model.Signal{
				{ID: "sig-lat", EntityID: "svc/shop/checkout", Metric: model.MetricLatencyP99, Unit: "ms",
					Points:   pts(rampVals, base),
					Baseline: pts(noise(40, 45, 3), base.Add(-45*time.Minute))},
				{ID: "sig-err", EntityID: "svc/shop/checkout", Metric: model.MetricErrorRate, Unit: "ratio",
					Points:   pts(errVals, base),
					Baseline: pts(noise(0.01, 45, 0.002), base.Add(-45*time.Minute))},
			}
		}(),
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "0.9s", SignalCount: 2},
		},
		Meta: model.Meta{RewindVersion: "0.1.0", SchemaVersion: bundle.CurrentSchemaVersion, CreatedAt: base},
	}
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	scenarios := []struct {
		name string
		inc  model.Incident
	}{
		{"bad-deploy", badDeploy()},
		{"oom-cascade", oomCascade()},
		{"node-failure", nodeFailure()},
		{"noisy-no-incident", noisyNoIncident()},
	}

	ok := true
	for _, s := range scenarios {
		inc := analyze.Run(s.inc)
		path := "testdata/incidents/" + s.name + ".rewind"
		if err := bundle.Export(inc, nil, path); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
			ok = false
			continue
		}
		cpTotal := 0
		for _, sig := range inc.Signals {
			cpTotal += len(sig.ChangePoints)
		}
		fmt.Printf("%-30s  %devt  %dsig  %dcp\n",
			s.name, len(inc.Events), len(inc.Signals), cpTotal)
	}
	if !ok {
		os.Exit(1)
	}
}
