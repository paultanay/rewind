package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/sources"
)

// Collector implements sources.Collector for Prometheus (and API-compatible
// backends: Thanos, Mimir, VictoriaMetrics). It is read-only by construction.
type Collector struct {
	// URL is the base URL of the Prometheus API.
	URL string
	// Headers contains optional extra request headers (e.g. Authorization).
	Headers map[string]string
	// Version is the rewind binary version (used in User-Agent).
	Version string
	// ExtraQueries are user-defined additional query templates from config.
	ExtraQueries []QueryTemplate

	client *client
	once   sync.Once
}

const sourceName = "prometheus"

// Name implements sources.Collector.
func (c *Collector) Name() string { return sourceName }

// Check implements sources.Collector.
func (c *Collector) Check(ctx context.Context) error {
	return c.getClient().Check(ctx)
}

func (c *Collector) getClient() *client {
	c.once.Do(func() {
		c.client = newClient(c.URL, c.Headers, 30*time.Second, c.Version)
	})
	return c.client
}

// Collect implements sources.Collector.
//
// For each service in scope:
//  1. Execute all default + extra queries over the incident window.
//  2. Execute the same queries over the baseline window(s).
//  3. Attach baseline points to each Signal for the change-point detectors.
//  4. Downsample to ≤500 points per signal.
//  5. Build synthetic Entity records from the results.
func (c *Collector) Collect(ctx context.Context, scope model.Scope, window model.TimeRange) (sources.CollectResult, error) {
	cl := c.getClient()
	step := chooseStep(window)

	// Determine target services. If none specified, use namespace-wide queries
	// by substituting namespace only.
	services := scope.Services
	if len(services) == 0 && len(scope.Namespaces) > 0 {
		// Use the namespace itself as the "service" wildcard.
		// Individual queries use {service=~".+"} pattern — handled by template.
		services = []string{""}
	}
	if len(services) == 0 {
		services = []string{""}
	}

	// Baseline windows:
	//   B1: same duration immediately before the incident window.
	//   B2: same duration 24h earlier (if different enough from B1).
	b1 := model.TimeRange{
		From: window.From.Add(-window.Duration()),
		To:   window.From,
	}
	b2 := model.TimeRange{
		From: window.From.Add(-24 * time.Hour).Add(-window.Duration()),
		To:   window.From.Add(-24 * time.Hour),
	}

	queries := append(defaultQueries, c.ExtraQueries...) //nolint:gocritic

	var (
		mu       sync.Mutex
		signals  []model.Signal
		entities []model.Entity
		wg       sync.WaitGroup
		errs     []string
	)

	// Fan out: one goroutine per (service × query).
	// Bounded: services × queries is typically small (5 services × 8 queries = 40).
	for _, svc := range services {
		for nsIdx := range scope.Namespaces {
			ns := scope.Namespaces[nsIdx]
			for _, qt := range queries {
				wg.Add(1)
				go func(ns, svc string, qt QueryTemplate) {
					defer wg.Done()

					sig, err := c.collectSignal(ctx, cl, qt, ns, svc, window, b1, b2, step)
					if err != nil {
						mu.Lock()
						errs = append(errs, fmt.Sprintf("%s/%s %s: %v", ns, svc, qt.Metric, err))
						mu.Unlock()
						return
					}
					if sig == nil {
						return // no data for this query
					}

					mu.Lock()
					signals = append(signals, *sig)
					// Create entity if not already present.
					entityID := entityIDForService(ns, svc)
					found := false
					for _, e := range entities {
						if e.ID == entityID {
							found = true
							break
						}
					}
					if !found {
						entities = append(entities, model.Entity{
							ID:          entityID,
							Kind:        model.EntityKindService,
							DisplayName: svc,
							Labels: map[string]string{
								"namespace": ns,
								"service":   svc,
							},
						})
					}
					mu.Unlock()
				}(ns, svc, qt)
			}
		}
	}
	wg.Wait()

	// Serialise raw fixture for bundle replay.
	raw, _ := json.Marshal(map[string]any{
		"url":      c.URL,
		"step":     step.String(),
		"window":   window,
		"signals":  len(signals),
		"errors":   errs,
	})

	var collectErr error
	if len(errs) > 0 && len(signals) == 0 {
		collectErr = fmt.Errorf("all queries failed: %s", strings.Join(errs[:min(3, len(errs))], "; "))
	}

	return sources.CollectResult{
		Entities:   entities,
		Signals:    signals,
		RawFixture: raw,
	}, collectErr
}

// collectSignal runs one query template for one service and returns the
// populated Signal (incident + baseline points), or nil if no data was returned.
func (c *Collector) collectSignal(
	ctx context.Context,
	cl *client,
	qt QueryTemplate,
	ns, svc string,
	window, b1, b2 model.TimeRange,
	step time.Duration,
) (*model.Signal, error) {
	q := substituteLabels(qt.PromQL, ns, svc)

	entries, err := cl.QueryRange(ctx, q, window.From, window.To, step)
	if err != nil {
		return nil, err
	}
	pts := downsample(matrixToPoints(entries))
	if len(pts) == 0 {
		return nil, nil
	}

	// Fetch baseline B1.
	b1Entries, _ := cl.QueryRange(ctx, q, b1.From, b1.To, step)
	b1pts := downsample(matrixToPoints(b1Entries))

	// Fetch baseline B2 (24h prior). Ignore errors — B2 is best-effort.
	b2Entries, _ := cl.QueryRange(ctx, q, b2.From, b2.To, step)
	b2pts := downsample(matrixToPoints(b2Entries))

	// Merge baseline: B1 + B2, capped to 500 points.
	baseline := append(b1pts, b2pts...) //nolint:gocritic
	baseline = downsample(baseline)

	entityID := entityIDForService(ns, svc)
	sig := &model.Signal{
		ID:       model.NewSignalID(),
		EntityID: entityID,
		Metric:   qt.Metric,
		Unit:     qt.Unit,
		Points:   pts,
		Baseline: baseline,
	}
	return sig, nil
}

// substituteLabels replaces {namespace} and {service} placeholders in a
// PromQL template with the actual values.
func substituteLabels(promQL, ns, svc string) string {
	q := strings.ReplaceAll(promQL, "{namespace}", ns)
	if svc == "" {
		// Wildcard: match all services in the namespace.
		q = strings.ReplaceAll(q, `service="{service}"`, `namespace=~".+"`)
		q = strings.ReplaceAll(q, `container="{service}"`, `namespace=~".+"`)
		q = strings.ReplaceAll(q, `node="{service}"`, `node=~".+"`)
	} else {
		q = strings.ReplaceAll(q, "{service}", svc)
	}
	return q
}

func entityIDForService(ns, svc string) string {
	if svc == "" {
		return model.NewEntityID(model.EntityKindService, ns, ns)
	}
	return model.NewEntityID(model.EntityKindService, ns, svc)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
