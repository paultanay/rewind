package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newSourcesCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Check connectivity and capabilities of configured sources",
		Long: `Connects to every configured source and reports whether it is reachable,
authenticated, and which data types it can provide. Useful for verifying
your rewind.yaml before running an investigation.

Exit code 0 = all enabled sources reachable.
Exit code 3 = one or more enabled sources unreachable.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourcesCheck(cmd.Context(), timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second,
		"per-source connection timeout")
	return cmd
}

func runSourcesCheck(ctx context.Context, timeout time.Duration) error {
	cfg, err := LoadConfig(globals.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(ExitInternalError)
	}

	reg := buildRegistry(cfg)
	collectors := reg.All()

	bold := color.New(color.Bold)
	bold.Fprintln(os.Stdout, "Configured sources:")
	fmt.Fprintln(os.Stdout)

	if len(collectors) == 0 {
		fmt.Fprintln(os.Stdout, "  no sources configured — edit rewind.yaml")
		fmt.Fprintln(os.Stdout)
		printDisabledSources(cfg)
		return nil
	}

	anyFailed := false
	for _, c := range collectors {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		err := c.Check(checkCtx)
		cancel()

		var statusStr string
		if err != nil {
			statusStr = color.RedString("✗ unreachable")
			fmt.Fprintf(os.Stdout, "  %-20s %s  (%v)\n", c.Name(), statusStr, err)
			anyFailed = true
		} else {
			statusStr = color.GreenString("✓ ok        ")
			fmt.Fprintf(os.Stdout, "  %-20s %s\n", c.Name(), statusStr)
		}
	}

	fmt.Fprintln(os.Stdout)
	printDisabledSources(cfg)

	if anyFailed {
		os.Exit(ExitAllSourcesFailed)
	}
	return nil
}

// printDisabledSources lists sources that are explicitly disabled or have no
// credentials configured, so the user knows what they're missing.
func printDisabledSources(cfg *Config) {
	type item struct {
		name     string
		reason   string
		disabled bool
	}

	items := []item{
		{"prometheus", cfg.Prometheus.URL, cfg.Prometheus.Disabled},
		{"kubernetes", cfg.Kubernetes.Kubeconfig, cfg.Kubernetes.Disabled},
		{"github", "(token auth)", cfg.GitHub.Disabled || cfg.GitHub.Token == ""},
		{"gitlab", cfg.GitLab.URL, cfg.GitLab.Disabled || cfg.GitLab.Token == ""},
		{"loki", cfg.Loki.URL, cfg.Loki.Disabled || cfg.Loki.URL == ""},
		{"tempo", cfg.Tempo.URL, cfg.Tempo.Disabled || cfg.Tempo.URL == ""},
		{"alertmanager", cfg.AlertMgr.URL, cfg.AlertMgr.Disabled || cfg.AlertMgr.URL == ""},
	}

	// Build registry to see which are actually active.
	active := map[string]bool{}
	reg := buildRegistry(cfg)
	for _, c := range reg.All() {
		active[c.Name()] = true
	}

	var skipped []item
	for _, it := range items {
		if !active[it.name] {
			skipped = append(skipped, it)
		}
	}
	if len(skipped) == 0 {
		return
	}

	color.New(color.Faint).Fprintln(os.Stdout, "Not configured / disabled:")
	for _, it := range skipped {
		reason := "disabled"
		if !it.disabled && it.reason == "" {
			reason = "no credentials/endpoint in config"
		}
		color.New(color.Faint).Fprintf(os.Stdout, "  %-20s %s\n", it.name, reason)
	}
	fmt.Fprintln(os.Stdout)
}
