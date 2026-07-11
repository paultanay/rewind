package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/rewind-io/rewind/internal/bundle"
	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/render/terminal"
	mdrender "github.com/rewind-io/rewind/internal/render/markdown"
	"github.com/rewind-io/rewind/internal/sources"
)

type investigateFlags struct {
	from       string
	to         string
	namespaces []string
	services   []string
	format     string
	output     string
	replay     string
}

func newInvestigateCmd() *cobra.Command {
	var flags investigateFlags

	cmd := &cobra.Command{
		Use:   "investigate",
		Short: "Investigate a production incident",
		Long: `Collect data from configured sources for the given time window, correlate
events and anomalies, generate a causal verdict, render the timeline, and
optionally write a .rewind bundle.

Examples:
  rewind investigate --from 14:00 --to 14:45 --namespace shop
  rewind investigate --from -2h --to -0m --service checkout -o incident.rewind
  rewind investigate --replay incident.rewind --format md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInvestigate(cmd.Context(), flags)
		},
	}

	cmd.Flags().StringVar(&flags.from, "from", "-1h", "start of investigation window (RFC3339, HH:MM, or -45m)")
	cmd.Flags().StringVar(&flags.to, "to", "now", "end of investigation window")
	cmd.Flags().StringSliceVarP(&flags.namespaces, "namespace", "n", nil, "Kubernetes namespace(s) to scope")
	cmd.Flags().StringSliceVarP(&flags.services, "service", "s", nil, "service name(s) to scope")
	cmd.Flags().StringVar(&flags.format, "format", "term", "output format: term|md|json")
	cmd.Flags().StringVarP(&flags.output, "output", "o", "", "write bundle to this path (e.g. incident.rewind)")
	cmd.Flags().StringVar(&flags.replay, "replay", "", "re-run analysis on a previously exported bundle")

	return cmd
}

func runInvestigate(ctx context.Context, flags investigateFlags) error {
	now := time.Now()

	// ── Parse time window ────────────────────────────────────────────────────
	toStr := flags.to
	if toStr == "now" || toStr == "" {
		toStr = "-0m"
	}
	fromTime, err := ParseTime(flags.from, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --from: %v\n", err)
		os.Exit(ExitUsageError)
	}
	toTime, err := ParseTime(toStr, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --to: %v\n", err)
		os.Exit(ExitUsageError)
	}
	if !toTime.After(fromTime) {
		fmt.Fprintf(os.Stderr, "error: --to must be after --from\n")
		os.Exit(ExitUsageError)
	}

	window := model.TimeRange{From: fromTime, To: toTime}
	scope := model.Scope{
		Namespaces: flags.namespaces,
		Services:   flags.services,
	}

	// ── Load config ──────────────────────────────────────────────────────────
	cfg, err := LoadConfig(globals.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(ExitInternalError)
	}
	_ = cfg // Used when collectors are wired in Phase 2+

	var inc model.Incident

	if flags.replay != "" {
		// ── Replay mode: load bundle, re-run analysis ────────────────────────
		b, err := bundle.Import(flags.replay)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: loading bundle: %v\n", err)
			os.Exit(ExitInternalError)
		}
		inc = b.Incident
		fmt.Fprintf(os.Stderr, "replaying bundle %s\n", flags.replay)
	} else {
		// ── Live collection ──────────────────────────────────────────────────
		// In Phase 1 the collector registry is empty; we build the skeleton
		// incident so all downstream rendering/bundle code is exercised.
		// Phase 2 wires in real collectors.
		reg := &sources.Registry{}
		_ = reg // collectors registered in Phase 2

		runResult := sources.RunAll(ctx, reg.All(), scope, window, cfg.SourceTimeout)

		allSourcesFailed := true
		for _, r := range runResult.Reports {
			if r.Status != model.SourceStatusFailed {
				allSourcesFailed = false
				break
			}
		}
		// Only error on all-sources-failed if we had at least one source configured.
		if allSourcesFailed && len(runResult.Reports) > 0 {
			fmt.Fprintln(os.Stderr, "error: all configured sources failed")
			os.Exit(ExitAllSourcesFailed)
		}

		inc = model.Incident{
			ID:       model.NewIncidentID(now),
			Window:   window,
			Scope:    scope,
			Entities: runResult.Entities,
			Events:   runResult.Events,
			Signals:  runResult.Signals,
			Sources:  runResult.Reports,
			Meta: model.Meta{
				RewindVersion: rewindVersion,
				SchemaVersion: bundle.CurrentSchemaVersion,
				CreatedAt:     now.UTC(),
			},
		}

		// Analysis engine (Phase 4) will fill Verdict here.
		// For Phase 1 we leave it nil — the renderer handles that gracefully.

		// ── Bundle export ────────────────────────────────────────────────────
		if flags.output != "" {
			if err := bundle.Export(inc, runResult.RawSources, flags.output); err != nil {
				fmt.Fprintf(os.Stderr, "warning: bundle export failed: %v\n", err)
			}
		}
	}

	// ── Render ───────────────────────────────────────────────────────────────
	switch flags.format {
	case "term", "":
		if err := terminal.Render(os.Stdout, inc, terminal.Options{Width: 120}); err != nil {
			return fmt.Errorf("render: %w", err)
		}
	case "md":
		if err := mdrender.Render(os.Stdout, inc); err != nil {
			return fmt.Errorf("render: %w", err)
		}
	case "json":
		// TODO(Phase 1+): add JSON renderer
		fmt.Fprintln(os.Stderr, "json format not yet implemented")
		os.Exit(ExitUsageError)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown format %q (term|md|json)\n", flags.format)
		os.Exit(ExitUsageError)
	}

	// ── Exit code based on findings ──────────────────────────────────────────
	for _, e := range inc.Events {
		if e.Severity == model.SeverityCritical {
			os.Exit(ExitCriticalFindings)
		}
	}
	if inc.Verdict != nil && len(inc.Verdict.Hypotheses) > 0 {
		if inc.Verdict.Hypotheses[0].Confidence == model.ConfidenceHigh {
			os.Exit(ExitCriticalFindings)
		}
	}

	return nil
}
