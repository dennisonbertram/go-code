package harness

import (
	"reflect"
	"testing"
)

func TestPatchFilesExtractsMutatedPaths(t *testing.T) {
	patch := `*** Begin Patch
*** Add File: cmd/harnesscli/improve.go
+package main
*** Update File: internal/harness/workflow_recap.go
@@
-old
+new
*** Delete File: docs/context/stale-note.md
*** End Patch`

	got := patchFiles(patch)
	want := []string{
		"cmd/harnesscli/improve.go",
		"internal/harness/workflow_recap.go",
		"docs/context/stale-note.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("patchFiles() = %#v, want %#v", got, want)
	}
}
