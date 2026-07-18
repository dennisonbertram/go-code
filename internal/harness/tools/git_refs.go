package tools

import (
	"fmt"
	"strings"
)

// ValidateGitRef rejects revision strings git would parse as command-line
// options. Legitimate refs (branches, tags, SHAs, HEAD~2, a..b ranges)
// never begin with '-'; git refnames cannot either.
func ValidateGitRef(ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("invalid git revision %q: must not begin with '-'", ref)
	}
	return nil
}
