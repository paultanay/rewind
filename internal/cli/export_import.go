package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rewind-io/rewind/internal/bundle"
	"github.com/rewind-io/rewind/internal/render/terminal"
)

func newExportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export <source.rewind> <dest.rewind>",
		Short: "Re-export a bundle (normalises and upgrades schema if needed)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := bundle.Import(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(ExitInternalError)
			}
			if err := bundle.Export(b.Incident, b.RawSources, args[1]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(ExitInternalError)
			}
			fmt.Fprintf(os.Stderr, "exported %s → %s\n", args[0], args[1])
			return nil
		},
	}
}

func newImportCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "import <bundle.rewind>",
		Short: "Import and render a .rewind bundle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := bundle.Import(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(ExitInternalError)
			}

			switch format {
			case "term", "":
				return terminal.Render(os.Stdout, b.Incident, terminal.Options{Width: 120})
			default:
				fmt.Fprintf(os.Stderr, "error: unknown format %q\n", format)
				os.Exit(ExitUsageError)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "term", "output format: term|md|json")
	return cmd
}
