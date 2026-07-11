//go:build ignore

// make_fixture.go generates testdata/incidents/bad-deploy.rewind — the
// canonical Phase 2 golden bundle used to validate the replay path.
// Run: go run testdata/make_fixture.go
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

func main() {
	base := time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
	step := time.Minute

	pts := func(vals []float64) []model.Point {
		p := make([]model.Point, len(vals))
		for i, v := range vals {
			p[i] = model.Point{T: base.Add(time.Duration(i) * step), V: v}
		}
		return p
	}

	noise := func(base float64, n int, sigma float64) []float64 {
		v := make([]float64, n)
		var seed uint32 = 99
		for i := range v {
			seed = 1664525*seed + 1013904223
			f := float64(seed)/math.MaxUint32*2 - 1
			v[i] = base + f*sigma
		}
		return v
	}

	stepVals := func(v1 float64, n1 int, v2 float64, n2 int) []float64 {
		v := make([]float64, n1+n2)
		for i := 0; i < n1; i++ {
			v[i] = v1
		}
		for i := n1; i < n1+n2; i++ {
			v[i] = v2
		}
		return v
	}

	inc := model.Incident{
		ID:     model.NewIncidentID(base),
		Window: model.TimeRange{From: base, To: base.Add(45 * time.Minute)},
		Scope:  model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		Entities: []model.Entity{
			{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
		},
		Events: []model.Event{
			{
				ID: "evt-deploy", At: base, Kind: model.EventKindDeploy,
				EntityID: "svc/shop/checkout", Severity: model.SeverityNotable,
				Title:  "Deployed checkout v2.3.1",
				Detail: "Image: checkout:v2.3.1 (was v2.3.0)\nAuthor: alice\nCommit: a1b2c3d",
				SourceRef: model.SourceRef{SourceName: "github",
					URL: "https://github.com/example/checkout/deployments/42"},
			},
			{
				ID: "evt-oom", At: base.Add(80 * time.Second), Kind: model.EventKindOOMKill,
				EntityID: "svc/shop/checkout", Severity: model.SeverityCritical,
				Title: "OOMKilled: checkout-7d9f-abc12",
			},
		},
		Signals: []model.Signal{
			{
				ID: "sig-latency", EntityID: "svc/shop/checkout",
				Metric:   model.MetricLatencyP99,
				Unit:     "ms",
				Points:   pts(stepVals(40, 10, 180, 35)),
				Baseline: pts(noise(40, 45, 2)),
			},
			{
				ID: "sig-errors", EntityID: "svc/shop/checkout",
				Metric:   model.MetricErrorRate,
				Unit:     "ratio",
				Points:   pts(stepVals(0.005, 12, 0.18, 33)),
				Baseline: pts(noise(0.005, 45, 0.001)),
			},
			{
				ID: "sig-memory", EntityID: "svc/shop/checkout",
				Metric:   model.MetricMemoryUsage,
				Unit:     "%",
				Points:   pts(stepVals(55, 8, 92, 37)),
				Baseline: pts(noise(55, 45, 2)),
			},
		},
		Sources: []model.SourceReport{
			{Name: "prometheus", Status: model.SourceStatusOK, Duration: "1.1s", EventCount: 0, SignalCount: 3},
			{Name: "github", Status: model.SourceStatusOK, Duration: "0.3s", EventCount: 1, SignalCount: 0},
		},
		Meta: model.Meta{
			RewindVersion: "0.1.0",
			SchemaVersion: bundle.CurrentSchemaVersion,
			CreatedAt:     base,
		},
	}

	inc = analyze.Run(inc)

	path := "testdata/incidents/bad-deploy.rewind"
	if err := bundle.Export(inc, nil, path); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", path)
	for _, sig := range inc.Signals {
		fmt.Printf("  signal %-20s  %d change-points\n", sig.Metric, len(sig.ChangePoints))
	}
}
