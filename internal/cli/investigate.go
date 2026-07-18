package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/paultanay/rewind/internal/analyze"
	"github.com/paultanay/rewind/internal/bundle"
	"github.com/paultanay/rewind/internal/model"
	mdrender "github.com/paultanay/rewind/internal/render/markdown"
	"github.com/paultanay/rewind/internal/render/terminal"
	"github.com/paultanay/rewind/internal/sources"
	"github.com/paultanay/rewind/internal/sources/alertmanager"
	"github.com/paultanay/rewind/internal/sources/cicd"
	k8s "github.com/paultanay/rewind/internal/sources/kubernetes"
	"github.com/paultanay/rewind/internal/sources/loki"
	prom "github.com/paultanay/rewind/internal/sources/prometheus"
	"github.com/paultanay/rewind/internal/sources/tempo"
)

type investigateFlags struct {
	from       string
	to         string
	namespaces []string
	services   []string
	format     string
	output     string
	replay     string
	width      int
	noColor    bool
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

	cmd.Flags().StringVar(&flags.from, "from", "-1h",
		"start of investigation window (RFC3339, HH:MM, HH:MM:SS, or -45m)")
	cmd.Flags().StringVar(&flags.to, "to", "now",
		"end of investigation window (same formats as --from, or 'now')")
	cmd.Flags().StringSliceVarP(&flags.namespaces, "namespace", "n", nil,
		"Kubernetes namespace(s) to scope")
	cmd.Flags().StringSliceVarP(&flags.services, "service", "s", nil,
		"service name(s) to scope (empty = all services in namespace)")
	cmd.Flags().StringVar(&flags.format, "format", "term",
		"output format: term|md|json")
	cmd.Flags().StringVarP(&flags.output, "output", "o", "",
		"write .rewind bundle to this path")
	cmd.Flags().StringVar(&flags.replay, "replay", "",
		"re-run analysis on a previously exported bundle (offline)")
	cmd.Flags().IntVar(&flags.width, "width", 120,
		"terminal column width for term output")
	cmd.Flags().BoolVar(&flags.noColor, "no-color", false,
		"disable ANSI colour output")

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

	var inc model.Incident

	if flags.replay != "" {
		// ── Replay mode: load bundle, re-run analysis on bundled raw data ────
		b, loadErr := bundle.Import(flags.replay)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "error: loading bundle %q: %v\n", flags.replay, loadErr)
			os.Exit(ExitInternalError)
		}
		inc = b.Incident
		fmt.Fprintf(os.Stderr, "replaying %s (window %s → %s)\n",
			flags.replay,
			inc.Window.From.Format("15:04:05"),
			inc.Window.To.Format("15:04:05"),
		)
		// Re-run analysis with the current version of the engine.
		inc = analyze.Run(inc)
	} else {
		// ── Live collection ──────────────────────────────────────────────────
		reg := buildRegistry(cfg)

		fmt.Fprintf(os.Stderr, "collecting from %d source(s)…\n", reg.Len())
		runResult := sources.RunAll(ctx, reg.All(), scope, window, cfg.SourceTimeout)

		// Print per-source status to stderr so stdout stays clean for piping.
		for _, r := range runResult.Reports {
			switch r.Status {
			case model.SourceStatusOK:
				fmt.Fprintf(os.Stderr, "  ✓ %-16s %devt  %dsig  %s\n",
					r.Name, r.EventCount, r.SignalCount, r.Duration)
			case model.SourceStatusFailed:
				fmt.Fprintf(os.Stderr, "  ✗ %-16s %s\n", r.Name, r.Error)
			default:
				fmt.Fprintf(os.Stderr, "  - %-16s skipped\n", r.Name)
			}
		}

		// All-sources-failed is a hard error only when sources were configured.
		if reg.Len() > 0 {
			allFailed := true
			for _, r := range runResult.Reports {
				if r.Status == model.SourceStatusOK || r.Status == model.SourceStatusPartial {
					allFailed = false
					break
				}
			}
			if allFailed {
				fmt.Fprintln(os.Stderr, "error: all configured sources failed")
				os.Exit(ExitAllSourcesFailed)
			}
		}

		// Build the initial incident model from collected data.
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

		// Run the full analysis pipeline: change-point detection, topology
		// graph construction, correlation rules, and verdict generation.
		inc = analyze.Run(inc)

		// ── Bundle export ────────────────────────────────────────────────────
		if flags.output != "" {
			if exportErr := bundle.Export(inc, runResult.RawSources, flags.output); exportErr != nil {
				fmt.Fprintf(os.Stderr, "warning: bundle export failed: %v\n", exportErr)
			} else {
				fmt.Fprintf(os.Stderr, "bundle written → %s\n", flags.output)
			}
		}
	}

	// ── Render ───────────────────────────────────────────────────────────────
	switch flags.format {
	case "term", "":
		renderErr := terminal.Render(os.Stdout, inc, terminal.Options{
			Width:   flags.width,
			NoColor: flags.noColor,
		})
		if renderErr != nil {
			return fmt.Errorf("render: %w", renderErr)
		}
	case "md":
		if renderErr := mdrender.Render(os.Stdout, inc); renderErr != nil {
			return fmt.Errorf("render: %w", renderErr)
		}
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if renderErr := enc.Encode(inc); renderErr != nil {
			return fmt.Errorf("render json: %w", renderErr)
		}
	default:
		fmt.Fprintf(os.Stderr, "error: unknown format %q (term|md|json)\n", flags.format)
		os.Exit(ExitUsageError)
	}

	// ── Exit code based on findings (contractual per §13) ────────────────────
	// Exit 1 when Critical findings exist so CI pipelines can gate on it.
	for _, e := range inc.Events {
		if e.Severity == model.SeverityCritical {
			os.Exit(ExitCriticalFindings)
		}
	}
	for _, sig := range inc.Signals {
		for _, cp := range sig.ChangePoints {
			if cp.Score >= 0.8 {
				os.Exit(ExitCriticalFindings)
			}
		}
	}
	if inc.Verdict != nil && len(inc.Verdict.Hypotheses) > 0 &&
		inc.Verdict.Hypotheses[0].Confidence == model.ConfidenceHigh {
		os.Exit(ExitCriticalFindings)
	}

	return nil
}

// buildRegistry constructs and returns a registry populated with all
// enabled collectors from the loaded config. Disabled sources are skipped
// without error — the user explicitly opted out.
func buildRegistry(cfg *Config) *sources.Registry {
	reg := &sources.Registry{}

	// ── Prometheus ───────────────────────────────────────────────────────────
	if !cfg.Prometheus.Disabled && cfg.Prometheus.URL != "" {
		reg.Register(&prom.Collector{
			URL:     cfg.Prometheus.URL,
			Headers: cfg.Prometheus.Headers,
			Version: rewindVersion,
		})
	}

	// ── Kubernetes ───────────────────────────────────────────────────────────
	if !cfg.Kubernetes.Disabled {
		reg.Register(&k8s.Collector{
			KubeconfigPath: cfg.Kubernetes.Kubeconfig,
			ContextName:    cfg.Kubernetes.Context,
			Version:        rewindVersion,
		})
	}

	// ── CI/CD (GitHub + GitLab) ──────────────────────────────────────────────
	// Register even if only one provider is configured; the collector skips
	// whichever provider has no credentials/repos.
	hasGitHub := !cfg.GitHub.Disabled && cfg.GitHub.Token != "" && len(cfg.GitHub.Repos) > 0
	hasGitLab := !cfg.GitLab.Disabled && cfg.GitLab.Token != "" && len(cfg.GitLab.Projects) > 0
	if hasGitHub || hasGitLab {
		reg.Register(&cicd.Collector{
			GitHub: cicd.GitHubConfig{
				Token:    cfg.GitHub.Token,
				Repos:    cfg.GitHub.Repos,
				Disabled: cfg.GitHub.Disabled,
			},
			GitLab: cicd.GitLabConfig{
				BaseURL:  cfg.GitLab.URL,
				Token:    cfg.GitLab.Token,
				Projects: cfg.GitLab.Projects,
				Disabled: cfg.GitLab.Disabled,
			},
			Version: rewindVersion,
		})
	}

	// ── Loki ───────────────────────────────────────────────────────────────────────
	if !cfg.Loki.Disabled && cfg.Loki.URL != "" {
		reg.Register(loki.New(loki.Config{
			URL:            cfg.Loki.URL,
			TenantID:       cfg.Loki.TenantID,
			Username:       cfg.Loki.Username,
			Password:       cfg.Loki.Password,
			GrafanaBaseURL: cfg.Loki.GrafanaBaseURL,
			MaxSampleLines: cfg.Loki.MaxSampleLines,
		}, rewindVersion))
	}

	// ── Tempo ──────────────────────────────────────────────────────────────────────
	if !cfg.Tempo.Disabled && cfg.Tempo.URL != "" {
		reg.Register(tempo.New(tempo.Config{
			URL:            cfg.Tempo.URL,
			TenantID:       cfg.Tempo.TenantID,
			Username:       cfg.Tempo.Username,
			Password:       cfg.Tempo.Password,
			GrafanaBaseURL: cfg.Tempo.GrafanaBaseURL,
		}, rewindVersion))
	}

	// ── Alertmanager ─────────────────────────────────────────────────────────────
	if !cfg.AlertMgr.Disabled && cfg.AlertMgr.URL != "" {
		reg.Register(alertmanager.New(alertmanager.Config{
			URL:      cfg.AlertMgr.URL,
			Username: cfg.AlertMgr.Username,
			Password: cfg.AlertMgr.Password,
		}, rewindVersion))
	}

	return reg
}
