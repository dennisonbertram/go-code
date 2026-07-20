package codex

import (
	"os/exec"
	"testing"
)

// TestCredentialPathsHaveNoTokenLogging is a grep-based guard against adding
// credential-bearing log/print calls to this provider's auth paths.
func TestCredentialPathsHaveNoTokenLogging(t *testing.T) {
	cmd := exec.Command("rg", "--pcre2", "(?i)(?:log\\.|fmt\\.(?:print|printf|sprint)).{0,120}(?:access[_ -]?token|refresh[_ -]?token|id[_ -]?token)", ".")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("credential-related logging call found:\n%s", output)
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run credential logging grep: %v\n%s", err, output)
	}
}
