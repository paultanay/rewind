// Package sources defines the Collector interface that every source adapter
// must implement, and the registry that the CLI uses to instantiate them.
//
// Design constraints (from the spec §8):
//   - All collectors run in parallel, each with an independent timeout.
//   - A failing collector produces a SourceReport entry with status=failed and
//     NEVER causes the overall investigation to fail.
//   - Every collector is read-only by construction: the interface has no write
//     methods and returns only model types.
//   - Collectors must be testable against recorded HTTP fixtures (httptest).
package sources

import (
	"context"

	"github.com/rewind-io/rewind/internal/model"
)

// CollectResult is the output of a single collector run.
type CollectResult struct {
	Entities []model.Entity
	Events   []model.Event
	Signals  []model.Signal
	// RawFixture is the raw response bytes (for bundle sources/*.json). May be nil.
	RawFixture []byte
}

// Collector is the single interface every source adapter implements.
// Implementations must:
//   - Be safe to call concurrently.
//   - Respect ctx cancellation/deadline immediately.
//   - Never mutate their arguments.
//   - Return a non-nil CollectResult even on partial failure.
type Collector interface {
	// Name returns the stable source identifier used in SourceReport and bundle
	// entries (e.g. "prometheus", "loki", "kubernetes").
	Name() string

	// Collect queries the source for the given scope and time window.
	// On error the caller records a SourceReport and continues; partial results
	// are always preferred over a complete failure.
	Collect(ctx context.Context, scope model.Scope, window model.TimeRange) (CollectResult, error)

	// Check verifies connectivity and reports capabilities without running a
	// full collection. Used by `rewind sources`.
	Check(ctx context.Context) error
}

// Registry holds the set of configured collectors. Collectors are registered
// at program startup; the CLI calls RunAll to execute them in parallel.
type Registry struct {
	collectors []Collector
}

// Register adds a collector to the registry. Not safe for concurrent use;
// call only during program initialisation.
func (r *Registry) Register(c Collector) {
	r.collectors = append(r.collectors, c)
}

// All returns the registered collectors in registration order.
func (r *Registry) All() []Collector {
	out := make([]Collector, len(r.collectors))
	copy(out, r.collectors)
	return out
}

// Len returns the number of registered collectors.
func (r *Registry) Len() int {
	return len(r.collectors)
}
