package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

// assertSchemaEqual compares schemas by canonical JSON so map ordering and
// int-vs-float representation cannot cause false failures.
func assertSchemaEqual(t *testing.T, want, got map[string]any) {
	t.Helper()
	wantJSON, gotJSON := mustJSON(t, want), mustJSON(t, got)
	if wantJSON != gotJSON {
		t.Fatalf("schema mismatch:\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
}

func TestNewTypedDerivesSchema(t *testing.T) {
	type args struct {
		Pattern    string   `json:"pattern" desc:"glob pattern"`
		MaxMatches int      `json:"max_matches,omitempty" min:"1" max:"2000"`
		Ratio      float64  `json:"ratio,omitempty" min:"0.5"`
		Verbose    *bool    `json:"verbose"`
		Mode       string   `json:"mode,omitempty" enum:"fast,slow"`
		Names      []string `json:"names,omitempty"`
		hidden     string   //nolint:unused // exercises the unexported-field skip
		Skipped    string   `json:"-"`
	}

	tool, err := NewTyped(TypedSpec{Name: "demo", Description: "d"}, func(ctx context.Context, a args) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("NewTyped: %v", err)
	}

	assertSchemaEqual(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern":     map[string]any{"type": "string", "description": "glob pattern"},
			"max_matches": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
			"ratio":       map[string]any{"type": "number", "minimum": 0.5},
			"verbose":     map[string]any{"type": "boolean"},
			"mode":        map[string]any{"type": "string", "enum": []string{"fast", "slow"}},
			"names":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"pattern"},
	}, tool.Definition.Parameters)
}

func TestNewTypedNestedAndCompositeTypes(t *testing.T) {
	type inner struct {
		Depth int `json:"depth"`
	}
	type args struct {
		Nested  inner          `json:"nested"`
		Lookup  map[string]int `json:"lookup,omitempty"`
		Payload any            `json:"payload,omitempty"`
		Blob    []byte         `json:"blob,omitempty"`
	}

	tool, err := NewTyped(TypedSpec{Name: "demo"}, func(ctx context.Context, a args) (any, error) { return "", nil })
	if err != nil {
		t.Fatalf("NewTyped: %v", err)
	}

	assertSchemaEqual(t, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nested": map[string]any{
				"type":       "object",
				"properties": map[string]any{"depth": map[string]any{"type": "integer"}},
				"required":   []string{"depth"},
			},
			"lookup":  map[string]any{"type": "object"},
			"payload": map[string]any{},
			"blob":    map[string]any{"type": "string"},
		},
		"required": []string{"nested"},
	}, tool.Definition.Parameters)
}

func TestNewTypedSpecMetadataFlowsToDefinition(t *testing.T) {
	type args struct{}
	tool, err := NewTyped(TypedSpec{
		Name:         "meta",
		Description:  "desc",
		Action:       ActionWrite,
		Mutating:     true,
		ParallelSafe: true,
		Tags:         []string{"a", "b"},
		Tier:         TierDeferred,
	}, func(ctx context.Context, a args) (any, error) { return "", nil })
	if err != nil {
		t.Fatalf("NewTyped: %v", err)
	}
	def := tool.Definition
	if def.Name != "meta" || def.Description != "desc" || def.Action != ActionWrite ||
		!def.Mutating || !def.ParallelSafe || def.Tier != TierDeferred ||
		!reflect.DeepEqual(def.Tags, []string{"a", "b"}) {
		t.Fatalf("metadata not propagated: %+v", def)
	}
}

func TestNewTypedHandlerDecodesAndMarshals(t *testing.T) {
	type args struct {
		Name  string `json:"name"`
		Count int    `json:"count,omitempty"`
	}
	tool := MustTyped(TypedSpec{Name: "echo"}, func(ctx context.Context, a args) (any, error) {
		return map[string]any{"name": a.Name, "count": a.Count}, nil
	})

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"x","count":3}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if out != `{"count":3,"name":"x"}` {
		t.Fatalf("unexpected result: %s", out)
	}
}

func TestNewTypedHandlerToleratesEmptyAndNullArgs(t *testing.T) {
	type args struct {
		Porcelain *bool `json:"porcelain,omitempty"`
	}
	tool := MustTyped(TypedSpec{Name: "zero"}, func(ctx context.Context, a args) (any, error) {
		if a.Porcelain != nil {
			t.Fatalf("expected zero-value args, got %+v", a)
		}
		return "zero", nil
	})

	for _, raw := range []string{"", "null", "  "} {
		out, err := tool.Handler(context.Background(), json.RawMessage(raw))
		if err != nil {
			t.Fatalf("handler(%q): %v", raw, err)
		}
		if out != "zero" {
			t.Fatalf("handler(%q) = %q", raw, out)
		}
	}
}

func TestNewTypedHandlerStringAndRawPassthrough(t *testing.T) {
	type args struct{}
	str := MustTyped(TypedSpec{Name: "s"}, func(ctx context.Context, a args) (any, error) {
		return "plain text", nil
	})
	out, err := str.Handler(context.Background(), nil)
	if err != nil || out != "plain text" {
		t.Fatalf("string passthrough: %q, %v", out, err)
	}

	raw := MustTyped(TypedSpec{Name: "r"}, func(ctx context.Context, a args) (any, error) {
		return json.RawMessage(`{"already":"encoded"}`), nil
	})
	out, err = raw.Handler(context.Background(), nil)
	if err != nil || out != `{"already":"encoded"}` {
		t.Fatalf("raw passthrough: %q, %v", out, err)
	}
}

func TestNewTypedHandlerRejectsMalformedArgs(t *testing.T) {
	type args struct {
		Name string `json:"name"`
	}
	tool := MustTyped(TypedSpec{Name: "strict"}, func(ctx context.Context, a args) (any, error) {
		return "", nil
	})
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":42}`))
	if err == nil || !strings.Contains(err.Error(), "parse strict args") {
		t.Fatalf("expected parse error naming the tool, got %v", err)
	}
}

func TestNewTypedRejectsInvalidInputs(t *testing.T) {
	type args struct{}
	if _, err := NewTyped(TypedSpec{}, func(ctx context.Context, a args) (any, error) { return "", nil }); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := NewTyped[args](TypedSpec{Name: "x"}, nil); err == nil {
		t.Fatal("expected error for nil handler")
	}
	if _, err := NewTyped(TypedSpec{Name: "x"}, func(ctx context.Context, a string) (any, error) { return "", nil }); err == nil {
		t.Fatal("expected error for non-struct args")
	}
	type bad struct {
		Ch chan int `json:"ch"`
	}
	if _, err := NewTyped(TypedSpec{Name: "x"}, func(ctx context.Context, a bad) (any, error) { return "", nil }); err == nil {
		t.Fatal("expected error for unsupported field type")
	}
}
