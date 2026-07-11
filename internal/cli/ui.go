package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newUICmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "ui [bundle]",
		Short: "Open the incident timeline in a local web browser",
		Long: `Start a local HTTP server (bound to 127.0.0.1) and open the web UI.
If a .rewind bundle path is provided, the UI loads that incident.
Otherwise the UI waits for an incident to be piped in via investigate.

The web UI is implemented in Phase 6. This command is a placeholder stub.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stderr, "web UI is coming in Phase 6 — stay tuned!")
			fmt.Fprintln(os.Stderr, "In the meantime, use: rewind investigate --format md")
			os.Exit(ExitInternalError)
			return nil
		},
	}

	cmd.Flags().IntVar(&port, "port", 7750, "local port to listen on (0 = random)")
	return cmd
}
