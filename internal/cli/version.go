package cli

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stdout, "rewind %s\n", rewindVersion)
			fmt.Fprintf(os.Stdout, "  go:     %s\n", runtime.Version())
			fmt.Fprintf(os.Stdout, "  os:     %s/%s\n", runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
