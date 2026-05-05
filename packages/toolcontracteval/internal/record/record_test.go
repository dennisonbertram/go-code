package record

import (
	"path/filepath"
	"testing"
)

func TestAppendJSONLWritesReadableRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-calls.jsonl")
	if err := AppendJSONL(path, ToolCall{RunID: "r1", Scenario: "s1", Tool: "read", Valid: true}); err != nil {
		t.Fatal(err)
	}
	if err := AppendJSONL(path, ToolCall{RunID: "r1", Scenario: "s1", Tool: "write", Valid: false}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJSONL[ToolCall](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("records len = %d, want 2", len(got))
	}
	if got[1].Tool != "write" || got[1].Valid {
		t.Fatalf("second record = %+v", got[1])
	}
}
