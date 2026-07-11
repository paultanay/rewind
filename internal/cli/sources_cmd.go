package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newSourcesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sources",
		Short: "Check connectivity and capabilities of configured sources",
		Long: `Connects to every configured source and reports whether it is reachable,
authenticated, and which data types it can provide. Useful for verifying
your rewind.yaml before running an investigation.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourcesCheck(cmd.Context())
		},
	}
}

func runSourcesCheck(ctx context.Context) error {
	cfg, err := LoadConfig(globals.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(ExitInternalError)
	}

	// Collectors are registered in Phase 2+. For now, report based on config.
	type checkItem struct {
		name     string
		url      string
		disabled bool
	}
	items := []checkItem{
		{"prometheus", cfg.Prometheus.URL, cfg.Prometheus.Disabled},
		{"loki", cfg.Loki.URL, cfg.Loki.Disabled},
		{"kubernetes", cfg.Kubernetes.Kubeconfig, cfg.Kubernetes.Disabled},
		{"tempo", cfg.Tempo.URL, cfg.Tempo.Disabled},
		{"github", "(token auth)", cfg.GitHub.Disabled},
		{"gitlab", cfg.GitLab.URL, cfg.GitLab.Disabled},
		{"alertmanager", cfg.AlertMgr.URL, cfg.AlertMgr.Disabled},
	}

	bold := color.New(color.Bold)
	bold.Fprintln(os.Stdout, "Configured sources:")
	fmt.Fprintln(os.Stdout)

	for _, item := range items {
		status := color.GreenString("enabled")
		if item.disabled {
			status = color.New(color.Faint).Sprint("disabled")
		}
		if item.url == "" && !item.disabled {
			status = color.YellowString("no endpoint configured")
		}
		fmt.Fprintf(os.Stdout, "  %-16s %s  %s\n", item.name, status, color.New(color.Faint).Sprint(item.url))
	}

	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Live connectivity check will be available in Phase 2.")
	return nil
}
