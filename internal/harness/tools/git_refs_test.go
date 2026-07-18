package tools

import (
	"strings"
	"testing"
)

// Regression tests for issue #789: revision strings that git would parse as
// command-line options (e.g. --output=/abs/path) must be rejected before they
// reach a git argv position ahead of "--".
func TestValidateGitRef_RejectsOptionLikeRefs(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"--output=/tmp/x",
		"-O/tmp/x",
		"--upload-pack=touch /tmp/x",
	} {
		err := ValidateGitRef(ref)
		if err == nil {
			t.Errorf("ValidateGitRef(%q) = nil, want error", ref)
			continue
		}
		if !strings.Contains(err.Error(), "must not begin with '-'") {
			t.Errorf("ValidateGitRef(%q) error = %q, want it to contain %q", ref, err.Error(), "must not begin with '-'")
		}
	}
}

func TestValidateGitRef_AcceptsLegitRefs(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"HEAD",
		"HEAD~2",
		"main",
		"feature/x",
		"v1.0",
		"6e040e693a633eb08cfb0a83873af9328b657ae3",
		"abc123..def456",
		"main...feature",
	} {
		if err := ValidateGitRef(ref); err != nil {
			t.Errorf("ValidateGitRef(%q) = %v, want nil", ref, err)
		}
	}
}
