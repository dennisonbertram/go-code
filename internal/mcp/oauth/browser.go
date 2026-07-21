package oauth

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's default browser using the platform
// launcher. It returns quickly: the browser completes the flow asynchronously
// through the loopback listener.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("oauth: launch browser (%s): %w", runtime.GOOS, err)
	}
	return nil
}
