package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newDemoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "demo",
		Short: "Spin up a local demo cluster, break it, and investigate",
		Long: `rewind demo spins up a local kind cluster with a demo application,
Prometheus, and Loki, triggers a bad deployment to break it, then runs
'rewind investigate' automatically so you can see the full timeline.

Requires: Docker, kind, kubectl.

The demo is implemented in Phase 6. This command prints a getting-started
guide in the meantime.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(os.Stdout, `rewind demo — coming in Phase 6!

What it will do:
  1. spin up a kind cluster (kind create cluster --name rewind-demo)
  2. deploy the demo shop application (checkout + frontend + postgres)
  3. install Prometheus + Loki via helm
  4. trigger a bad deployment of checkout v2.3.1 (image with a memory leak)
  5. wait 90 seconds for the incident to develop
  6. run: rewind investigate --from -10m --to now --namespace shop
  7. open the web UI

In the meantime, you can build your own bundle:
  rewind investigate --from 14:00 --to 14:45 --namespace shop -o incident.rewind
  rewind import incident.rewind`)
			return nil
		},
	}
}
