package sources

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rewind-io/rewind/internal/model"
)

// RunResult aggregates the merged output from all collectors.
type RunResult struct {
	Entities []model.Entity
	Events   []model.Event
	Signals  []model.Signal
	Reports  []model.SourceReport
	// RawSources maps source name → raw fixture bytes for bundle export.
	RawSources map[string][]byte
}

// RunAll executes all registered collectors concurrently and merges results.
// It never returns an error; failing collectors are recorded in Reports with
// status=failed. The perSourceTimeout is applied to each collector independently.
func RunAll(
	ctx context.Context,
	collectors []Collector,
	scope model.Scope,
	window model.TimeRange,
	perSourceTimeout time.Duration,
) RunResult {
	if perSourceTimeout <= 0 {
		perSourceTimeout = 15 * time.Second
	}

	type singleResult struct {
		name   string
		result CollectResult
		report model.SourceReport
	}

	results := make([]singleResult, len(collectors))
	var wg sync.WaitGroup

	for i, c := range collectors {
		wg.Add(1)
		go func(idx int, col Collector) {
			defer wg.Done()
			start := time.Now()
			name := col.Name()

			tctx, cancel := context.WithTimeout(ctx, perSourceTimeout)
			defer cancel()

			cr, err := col.Collect(tctx, scope, window)
			dur := time.Since(start)

			report := model.SourceReport{
				Name:        name,
				Duration:    fmt.Sprintf("%.2fs", dur.Seconds()),
				EventCount:  len(cr.Events),
				SignalCount: len(cr.Signals),
			}
			if err != nil {
				report.Status = model.SourceStatusFailed
				report.Error = err.Error()
			} else {
				report.Status = model.SourceStatusOK
			}

			results[idx] = singleResult{
				name:   name,
				result: cr,
				report: report,
			}
		}(i, c)
	}
	wg.Wait()

	out := RunResult{
		RawSources: make(map[string][]byte, len(collectors)),
	}
	for _, r := range results {
		out.Entities = append(out.Entities, r.result.Entities...)
		out.Events = append(out.Events, r.result.Events...)
		out.Signals = append(out.Signals, r.result.Signals...)
		out.Reports = append(out.Reports, r.report)
		if len(r.result.RawFixture) > 0 {
			out.RawSources[r.name] = r.result.RawFixture
		}
	}
	return out
}
