package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// TypedSpec carries the tool metadata that cannot be derived from the args
// type: identity, action classification, and discovery tags. Parameters are
// intentionally absent — NewTyped derives the JSON schema from the args
// struct so the schema and the decode target can never drift apart.
type TypedSpec struct {
	Name         string
	Description  string
	Action       Action
	Mutating     bool
	ParallelSafe bool
	Tags         []string
	Tier         ToolTier
}

// NewTyped builds a Tool from a typed handler function, deriving the
// Parameters JSON schema from the exported fields of Args via reflection.
//
// Schema rules:
//   - Property names come from the `json` tag (fields tagged "-" or
//     unexported fields are skipped; untagged fields use the Go name).
//   - A field is optional when its json tag has ",omitempty" or its type is
//     a pointer; all other fields are listed in "required".
//   - Types map as: string→string, bool→boolean, ints/uints→integer,
//     floats→number, slices/arrays→array (with "items"), maps and nested
//     structs→object, interface{}→unconstrained.
//   - Optional tags refine a property: `desc:"..."` sets "description",
//     `min:"n"`/`max:"n"` set "minimum"/"maximum", and `enum:"a,b"` sets a
//     comma-separated string "enum".
//
// The wrapper decodes the raw arguments into Args before calling fn (empty
// or null input yields the zero value, matching hand-written handlers that
// tolerate absent arguments). fn's result is returned verbatim when it is a
// string or json.RawMessage, and JSON-marshalled otherwise.
func NewTyped[Args any](spec TypedSpec, fn func(ctx context.Context, args Args) (any, error)) (Tool, error) {
	if spec.Name == "" {
		return Tool{}, fmt.Errorf("typed tool requires a name")
	}
	if fn == nil {
		return Tool{}, fmt.Errorf("typed tool %q requires a handler function", spec.Name)
	}
	params, err := schemaForArgs(reflect.TypeOf((*Args)(nil)).Elem())
	if err != nil {
		return Tool{}, fmt.Errorf("typed tool %q: %w", spec.Name, err)
	}

	def := Definition{
		Name:         spec.Name,
		Description:  spec.Description,
		Parameters:   params,
		Action:       spec.Action,
		Mutating:     spec.Mutating,
		ParallelSafe: spec.ParallelSafe,
		Tags:         spec.Tags,
		Tier:         spec.Tier,
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args Args
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			if err := json.Unmarshal(trimmed, &args); err != nil {
				return "", fmt.Errorf("parse %s args: %w", spec.Name, err)
			}
		}
		out, err := fn(ctx, args)
		if err != nil {
			return "", err
		}
		switch v := out.(type) {
		case string:
			return v, nil
		case json.RawMessage:
			return string(v), nil
		default:
			return MarshalToolResult(out)
		}
	}

	return Tool{Definition: def, Handler: handler}, nil
}

// MustTyped is NewTyped for statically known tools: schema derivation can
// only fail on an invalid Args type or empty spec, both of which are
// programmer errors, so it panics instead of returning an error.
func MustTyped[Args any](spec TypedSpec, fn func(ctx context.Context, args Args) (any, error)) Tool {
	tool, err := NewTyped(spec, fn)
	if err != nil {
		panic(err)
	}
	return tool
}

func schemaForArgs(t reflect.Type) (map[string]any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("args type must be a struct, got %s", t.Kind())
	}

	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, omitempty := parseJSONFieldTag(field)
		if name == "-" {
			continue
		}
		prop, err := schemaForType(field.Type)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field.Name, err)
		}
		if desc := field.Tag.Get("desc"); desc != "" {
			prop["description"] = desc
		}
		if minTag := field.Tag.Get("min"); minTag != "" {
			v, err := parseSchemaNumber(minTag)
			if err != nil {
				return nil, fmt.Errorf("field %s: invalid min tag %q", field.Name, minTag)
			}
			prop["minimum"] = v
		}
		if maxTag := field.Tag.Get("max"); maxTag != "" {
			v, err := parseSchemaNumber(maxTag)
			if err != nil {
				return nil, fmt.Errorf("field %s: invalid max tag %q", field.Name, maxTag)
			}
			prop["maximum"] = v
		}
		if enum := field.Tag.Get("enum"); enum != "" {
			prop["enum"] = strings.Split(enum, ",")
		}
		props[name] = prop
		if !omitempty && field.Type.Kind() != reflect.Pointer {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, nil
}

func schemaForType(t reflect.Type) (map[string]any, error) {
	switch t.Kind() {
	case reflect.Pointer:
		return schemaForType(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			// []byte round-trips through JSON as a base64 string.
			return map[string]any{"type": "string"}, nil
		}
		items, err := schemaForType(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		return map[string]any{"type": "object"}, nil
	case reflect.Struct:
		return schemaForArgs(t)
	case reflect.Interface:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("unsupported schema type %s", t.Kind())
	}
}

func parseJSONFieldTag(field reflect.StructField) (name string, omitempty bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = field.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

// parseSchemaNumber keeps integer bounds as ints so generated schemas match
// hand-written literals like `"minimum": 1` byte-for-byte after marshalling.
func parseSchemaNumber(s string) (any, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return f, nil
}
