package cli

import (
	"os"

	"github.com/spf13/cobra"
)

// rewindVersion is set at link time via -ldflags.
// Default is "dev" so `go run` always prints something useful.
var rewindVersion = "dev"

// SetVersion is called from main.go to inject the version string produced by
// goreleaser's ldflags injection.
func SetVersion(v string) {
	if v != "" {
		rewindVersion = v
	}
}

// Version returns the current binary version.
func Version() string {
	return rewindVersion
}

// globalFlags holds flag values shared across commands.
type globalFlags struct {
	configPath string
}

var globals globalFlags

// NewRootCommand builds and returns the root cobra.Command. All sub-commands
// are registered here. main.go calls Execute() on the result.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "rewind",
		Short: "Production incident replay engine",
		Long: `rewind reconstructs a production incident as a single, scrubbable timeline —
deployments, metric anomalies, log error bursts, Kubernetes events, and traces,
all correlated and causally ranked.

Run 'rewind demo' to see it in action in under five minutes.
Run 'rewind investigate --help' for investigation options.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&globals.configPath,
		"config", "", "path to rewind.yaml (default: ./rewind.yaml or ~/.config/rewind/rewind.yaml)")

	root.AddCommand(
		newInvestigateCmd(),
		newUICmd(),
		newExportCmd(),
		newImportCmd(),
		newSourcesCmd(),
		newDemoCmd(),
		newExplainCmd(),
		newVersionCmd(),
	)

	return root
}

// Execute runs the root command and exits with the appropriate exit code.
// This is the single entry point called from main.go.
func Execute() {
	root := NewRootCommand()
	if err := root.Execute(); err != nil {
		// cobra has already printed the error for usage errors.
		// For other errors we print here.
		os.Exit(ExitInternalError)
	}
}
