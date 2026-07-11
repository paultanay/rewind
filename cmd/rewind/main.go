// Command rewind is the single binary entry point for the Rewind incident
// replay engine. It is intentionally thin — all logic lives in internal/.
// Build-time version injection via goreleaser ldflags:
//
//	-X github.com/rewind-io/rewind/internal/cli.rewindVersion={{.Version}}
package main

import (
	"github.com/rewind-io/rewind/internal/cli"
)

func main() {
	cli.Execute()
}
