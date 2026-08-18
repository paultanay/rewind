package cli

import (
	"os/exec"
	"runtime"
)

// openBrowser attempts to open the given URL in the system default browser.
// It is best-effort: errors are silently ignored because a browser failure
// must never prevent the server from serving the UI.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // executable is fixed; URL is an argument.
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // executable is fixed; URL is an argument.
	default: // linux, bsd, etc.
		cmd = exec.Command("xdg-open", url) //nolint:gosec // executable is fixed; URL is an argument.
	}
	_ = cmd.Start() // intentionally ignore error
}
