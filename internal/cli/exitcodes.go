// Package cli contains all Cobra command definitions, configuration loading,
// and exit-code semantics. The thin main.go in cmd/rewind delegates entirely
// to this package.
package cli

// Exit codes are contractual — tested and documented in the CLI spec (§13).
// Scripts and CI pipelines depend on these values; never change them without
// a major version bump.
const (
	// ExitOK is the success exit code.
	ExitOK = 0
	// ExitCriticalFindings means the investigation completed and found at
	// least one Critical-severity event or High-confidence hypothesis.
	// Useful for CI-gating after a deploy.
	ExitCriticalFindings = 1
	// ExitUsageError means the user provided bad flags or arguments.
	ExitUsageError = 2
	// ExitAllSourcesFailed means every configured source failed to respond.
	ExitAllSourcesFailed = 3
	// ExitInternalError is an unexpected internal failure.
	ExitInternalError = 4
)
