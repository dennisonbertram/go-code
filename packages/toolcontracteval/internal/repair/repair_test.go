package repair

import (
	"encoding/json"
	"testing"
)

func arraySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"memo":  map[string]any{"type": "string"},
		},
		"required": []any{"paths"},
	}
}

func TestStringifiedArrayParseRunsBeforeBareStringWrap(t *testing.T) {
	raw := json.RawMessage(`{"paths":"[\"a\",\"b\"]"}`)

	parsed := Simulate(Candidate{Name: "stringified_array_parse", Safety: "safe"}, "shape_probe", raw, arraySchema())
	if !parsed.Applied || !parsed.AfterValid {
		t.Fatalf("stringified array repair = %+v, want applied and valid", parsed)
	}
	if parsed.RepairedArguments != `{"paths":["a","b"]}` {
		t.Fatalf("repaired args = %s", parsed.RepairedArguments)
	}

	wrapped := Simulate(Candidate{Name: "bare_string_to_array", Safety: "safe"}, "shape_probe", raw, arraySchema())
	if wrapped.Applied {
		t.Fatalf("bare string repair should not wrap JSON-looking arrays: %+v", wrapped)
	}
}

func TestNullOptionalOmitDoesNotTouchValidInputs(t *testing.T) {
	raw := json.RawMessage(`{"paths":["a"],"memo":"keep"}`)
	sim := Simulate(Candidate{Name: "null_optional_omit", Safety: "safe"}, "shape_probe", raw, arraySchema())
	if !sim.BeforeValid || sim.Applied {
		t.Fatalf("simulation = %+v, want valid input untouched", sim)
	}
}

func TestMarkdownPathUnwrapOnlyDegenerateLinks(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
		},
		"required": []any{"file_path", "content"},
	}
	raw := json.RawMessage(`{"file_path":"/tmp/[notes.md](http://notes.md)","content":"[click](https://x.com)"}`)
	sim := Simulate(Candidate{Name: "markdown_path_unwrap", Safety: "safe"}, "write", raw, params)
	if !sim.Applied || !sim.AfterValid {
		t.Fatalf("simulation = %+v, want applied and valid", sim)
	}
	if sim.RepairedArguments != `{"content":"[click](https://x.com)","file_path":"/tmp/notes.md"}` {
		t.Fatalf("repaired args = %s", sim.RepairedArguments)
	}
}

func TestReadWindowDefaultRepairsRelationalIssue(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string"},
			"offset": map[string]any{"type": "integer"},
			"limit":  map[string]any{"type": "integer"},
		},
		"required": []any{"path"},
	}
	raw := json.RawMessage(`{"path":"main.go","limit":30}`)
	sim := Simulate(Candidate{Name: "read_window_default", Safety: "semantic"}, "read", raw, params)
	if !sim.Applied || !sim.AfterValid || !sim.SemanticNoteRequired {
		t.Fatalf("simulation = %+v, want semantic repair", sim)
	}
}

func TestFilePathAliasRepairsReadPath(t *testing.T) {
	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
	raw := json.RawMessage(`{"file_path":"math.go"}`)
	sim := Simulate(Candidate{Name: "file_path_to_path_alias", Safety: "safe"}, "read", raw, params)
	if !sim.Applied || !sim.AfterValid {
		t.Fatalf("simulation = %+v, want applied and valid", sim)
	}
	if sim.RepairedArguments != `{"path":"math.go"}` {
		t.Fatalf("repaired args = %s", sim.RepairedArguments)
	}
}
