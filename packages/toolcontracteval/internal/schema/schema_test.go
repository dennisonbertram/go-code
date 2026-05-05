package schema

import (
	"encoding/json"
	"testing"

	tcatalog "go-agent-harness/packages/toolcontracteval/internal/catalog"
)

func TestValidateRawNormalizesSchemaIssues(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"memo": map[string]any{"type": "string"},
		},
		"required": []any{"path", "tags"},
	}

	res := ValidateRaw("shape_probe", json.RawMessage(`{"tags":"[\"a\",\"b\"]","memo":null}`), params)
	if len(res.Issues) != 3 {
		t.Fatalf("issues len = %d, want 3: %+v", len(res.Issues), res.Issues)
	}
	if res.Issues[0].Code != "required" || res.Issues[0].Path[0] != "path" {
		t.Fatalf("first issue = %+v, want missing path", res.Issues[0])
	}
	tagsIssue := findIssue(res.Issues, "tags")
	if tagsIssue.Expected != "array" || tagsIssue.Received != "string" {
		t.Fatalf("tags issue = %+v, want array/string", tagsIssue)
	}
	memoIssue := findIssue(res.Issues, "memo")
	if memoIssue.Expected != "string" || memoIssue.Received != "null" {
		t.Fatalf("memo issue = %+v, want string/null", memoIssue)
	}
}

func TestValidateRawAddsReadWindowRelationalIssue(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string"},
			"offset": map[string]any{"type": "integer"},
			"limit":  map[string]any{"type": "integer"},
		},
		"required": []any{"path"},
	}

	res := ValidateRaw("read", json.RawMessage(`{"path":"main.go","limit":30}`), params)
	if len(res.Issues) != 1 {
		t.Fatalf("issues len = %d, want 1: %+v", len(res.Issues), res.Issues)
	}
	if res.Issues[0].Code != "relational_required_with" || res.Issues[0].Path[0] != "offset" {
		t.Fatalf("issue = %+v, want missing offset relational issue", res.Issues[0])
	}
}

func TestValidateRawFlagsDegenerateMarkdownPath(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
			"note":      map[string]any{"type": "string"},
		},
		"required": []any{"file_path", "note"},
	}

	res := ValidateRaw("path_probe", json.RawMessage(`{"file_path":"/tmp/[notes.md](http://notes.md)","note":"[click](https://x.ai)"}`), params)
	if len(res.Issues) != 1 {
		t.Fatalf("issues len = %d, want 1: %+v", len(res.Issues), res.Issues)
	}
	if res.Issues[0].Code != "path_markdown_autolink" {
		t.Fatalf("issue = %+v, want path markdown issue", res.Issues[0])
	}
}

func TestValidateRawSupportsAnyOfPathAliases(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":      map[string]any{"type": "string"},
			"file_path": map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
		},
		"anyOf": []any{
			map[string]any{"required": []any{"path"}},
			map[string]any{"required": []any{"file_path"}},
		},
		"allOf": []any{
			map[string]any{
				"anyOf": []any{
					map[string]any{"required": []any{"content"}},
					map[string]any{"required": []any{"new_text"}},
					map[string]any{"required": []any{"new_string"}},
					map[string]any{"required": []any{"text"}},
				},
			},
		},
	}

	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"path":"notes.txt","content":"hello"}`),
		json.RawMessage(`{"file_path":"notes.txt","content":"hello"}`),
	} {
		res := ValidateRaw("write", raw, params)
		if len(res.Issues) != 0 {
			t.Fatalf("ValidateRaw(%s) issues = %+v, want none", raw, res.Issues)
		}
	}

	res := ValidateRaw("write", json.RawMessage(`{"content":"hello"}`), params)
	if len(res.Issues) != 1 || res.Issues[0].Code != "any_of" {
		t.Fatalf("missing path/file_path issues = %+v, want one any_of issue", res.Issues)
	}

	res = ValidateRaw("write", json.RawMessage(`{"path":"notes.txt"}`), params)
	if len(res.Issues) != 1 || res.Issues[0].Code != "any_of" {
		t.Fatalf("missing content alias issues = %+v, want one any_of issue", res.Issues)
	}

	res = ValidateRaw("write", json.RawMessage(`{"path":"a.txt","file_path":"b.txt","content":"hello"}`), params)
	if len(res.Issues) != 1 || res.Issues[0].Code != "path_alias_conflict" {
		t.Fatalf("conflicting path/file_path issues = %+v, want path_alias_conflict", res.Issues)
	}
}

func TestProductionFilePathAliasesValidateWithEvalSchema(t *testing.T) {
	defs := tcatalog.ProductionDefinitions(t.TempDir())
	defByName := map[string]map[string]any{}
	for _, def := range defs {
		defByName[def.Name] = def.Parameters
	}

	cases := []struct {
		tool string
		raw  json.RawMessage
	}{
		{tool: "read", raw: json.RawMessage(`{"file_path":"notes.txt"}`)},
		{tool: "write", raw: json.RawMessage(`{"file_path":"notes.txt","content":"hello"}`)},
		{tool: "edit", raw: json.RawMessage(`{"file_path":"notes.txt","old_text":"hello","new_text":"hi"}`)},
		{tool: "apply_patch", raw: json.RawMessage(`{"file_path":"notes.txt","find":"hello","replace":"hi"}`)},
	}

	for _, tc := range cases {
		params := defByName[tc.tool]
		if params == nil {
			t.Fatalf("production tool %q not found", tc.tool)
		}
		res := ValidateRaw(tc.tool, tc.raw, params)
		if len(res.Issues) != 0 {
			t.Fatalf("%s file_path alias issues = %+v, want none", tc.tool, res.Issues)
		}
	}

	res := ValidateRaw("write", json.RawMessage(`{"path":"notes.txt"}`), defByName["write"])
	if len(res.Issues) != 1 || res.Issues[0].Code != "any_of" {
		t.Fatalf("write without content alias issues = %+v, want one any_of issue", res.Issues)
	}
}

func findIssue(issues []Issue, path string) Issue {
	for _, issue := range issues {
		if len(issue.Path) == 1 && issue.Path[0] == path {
			return issue
		}
	}
	return Issue{}
}
