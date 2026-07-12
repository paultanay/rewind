package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rewind-io/rewind/internal/bundle"
	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/server"
)

func newUICmd() *cobra.Command {
	var port int

	cmd := &cobra.Command{
		Use:   "ui [bundle.rewind]",
		Short: "Open the incident timeline in a local web browser",
		Long: `Start a local HTTP server (bound to 127.0.0.1) and serve the web UI
for a .rewind bundle. The server prints the URL and blocks until Ctrl+C.

Examples:
  rewind ui incident.rewind         # open a saved bundle
  rewind ui incident.rewind --port 8080
  rewind investigate ... -o inc.rewind && rewind ui inc.rewind`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUI(cmd.Context(), args, port)
		},
	}
	cmd.Flags().IntVar(&port, "port", 7750, "local port to listen on (0 = random available port)")
	return cmd
}

func runUI(ctx context.Context, args []string, port int) error {
	var inc model.Incident

	if len(args) > 0 {
		// Load from bundle file.
		bundlePath := args[0]
		b, err := bundle.Import(bundlePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: loading bundle %q: %v\n", bundlePath, err)
			os.Exit(ExitInternalError)
		}
		inc = b.Incident
		fmt.Fprintf(os.Stderr, "loaded bundle: %s (window %s → %s, %d events)\n",
			bundlePath,
			inc.Window.From.Format("15:04:05"),
			inc.Window.To.Format("15:04:05"),
			len(inc.Events),
		)
	} else {
		// No bundle: create a placeholder incident with a helpful message.
		now := time.Now()
		inc = model.Incident{
			ID:     model.NewIncidentID(now),
			Window: model.TimeRange{From: now.Add(-1 * time.Hour), To: now},
			Meta: model.Meta{
				RewindVersion: rewindVersion,
				SchemaVersion: bundle.CurrentSchemaVersion,
				CreatedAt:     now.UTC(),
			},
		}
		fmt.Fprintln(os.Stderr, "no bundle provided — showing empty UI.")
		fmt.Fprintln(os.Stderr, "Tip: rewind investigate ... -o incident.rewind && rewind ui incident.rewind")
	}

	// Start the HTTP server.
	srv, err := server.New(inc, port)
	if err != nil {
		return fmt.Errorf("ui server: %w", err)
	}

	addr := srv.Addr()
	url := "http://" + addr

	fmt.Fprintf(os.Stderr, "\n  ⏪  rewind UI ready\n")
	fmt.Fprintf(os.Stderr, "     → %s\n\n", url)
	fmt.Fprintf(os.Stderr, "  Press Ctrl+C to stop.\n\n")

	// Try to open the browser (best effort — don't fail if it doesn't work).
	openBrowser(url)

	// Block until Ctrl+C or context cancellation.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return srv.Serve(ctx)
}
