package schema

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
)

type Issue struct {
	Path     []string `json:"path"`
	Code     string   `json:"code"`
	Expected string   `json:"expected,omitempty"`
	Received string   `json:"received,omitempty"`
	Message  string   `json:"message"`
}

type Result struct {
	Args   map[string]any `json:"args,omitempty"`
	Issues []Issue        `json:"issues,omitempty"`
}

func ValidateRaw(toolName string, raw json.RawMessage, parameters map[string]any) Result {
	var v any
	if len(strings.TrimSpace(string(raw))) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Result{Issues: []Issue{{
			Code:     "invalid_json",
			Expected: "valid JSON object",
			Received: "invalid_json",
			Message:  fmt.Sprintf("arguments must be valid JSON: %v", err),
		}}}
	}

	issues := ValidateValue(v, parameters)
	if obj, ok := v.(map[string]any); ok {
		issues = append(issues, SemanticIssues(toolName, obj)...)
		return Result{Args: obj, Issues: issues}
	}
	return Result{Issues: issues}
}

func ValidateValue(v any, parameters map[string]any) []Issue {
	return validateAt(v, parameters, nil)
}

func SemanticIssues(toolName string, args map[string]any) []Issue {
	var issues []Issue
	pathValue, hasPath := args["path"].(string)
	filePathValue, hasFilePath := args["file_path"].(string)
	if hasPath && hasFilePath && pathValue != filePathValue {
		issues = append(issues, Issue{
			Path:     []string{"file_path"},
			Code:     "path_alias_conflict",
			Expected: "same value as path",
			Received: "different value",
			Message:  "path and file_path must not disagree",
		})
	}
	for key, value := range args {
		text, ok := value.(string)
		if !ok || !strings.Contains(strings.ToLower(key), "path") {
			continue
		}
		if containsDegenerateMarkdownLink(text) {
			issues = append(issues, Issue{
				Path:     []string{key},
				Code:     "path_markdown_autolink",
				Expected: "plain filesystem path string",
				Received: "markdown_link",
				Message:  fmt.Sprintf("%s must be a plain path string, not a markdown link", key),
			})
		}
	}
	if toolName != "read" {
		return issues
	}
	_, hasOffset := args["offset"]
	_, hasLimit := args["limit"]
	if hasOffset == hasLimit {
		return issues
	}
	if hasLimit {
		return append(issues, Issue{
			Path:     []string{"offset"},
			Code:     "relational_required_with",
			Expected: "offset when limit is present",
			Received: "missing",
			Message:  "read.offset must be provided when read.limit is provided",
		})
	}
	return append(issues, Issue{
		Path:     []string{"limit"},
		Code:     "relational_required_with",
		Expected: "limit when offset is present",
		Received: "missing",
		Message:  "read.limit must be provided when read.offset is provided",
	})
}

func validateAt(v any, s map[string]any, path []string) []Issue {
	if s == nil {
		return nil
	}
	if !typeAllowed(v, s["type"]) {
		return []Issue{{
			Path:     append([]string(nil), path...),
			Code:     "invalid_type",
			Expected: typeString(s["type"]),
			Received: Shape(v),
			Message:  fmt.Sprintf("%s expected %s, got %s", dotPath(path), typeString(s["type"]), Shape(v)),
		}}
	}

	var issues []Issue
	for _, schema := range schemaList(s["allOf"]) {
		issues = append(issues, validateAt(v, schema, path)...)
	}
	if clauses := schemaList(s["anyOf"]); len(clauses) > 0 && !anySchemaMatches(v, clauses, path) {
		issues = append(issues, Issue{
			Path:     append([]string(nil), path...),
			Code:     "any_of",
			Expected: "one matching anyOf schema",
			Received: "no matching schema",
			Message:  fmt.Sprintf("%s must match at least one allowed schema", dotPath(path)),
		})
	}

	switch declaredSchemaKind(s) {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return issues
		}
		props, _ := s["properties"].(map[string]any)
		required := requiredSet(s["required"])
		requiredNames := make([]string, 0, len(required))
		for name := range required {
			requiredNames = append(requiredNames, name)
		}
		sort.Strings(requiredNames)
		for _, name := range requiredNames {
			if _, ok := obj[name]; !ok {
				issues = append(issues, Issue{
					Path:     appendPath(path, name),
					Code:     "required",
					Expected: "present",
					Received: "missing",
					Message:  fmt.Sprintf("%s is required", dotPath(appendPath(path, name))),
				})
			}
		}
		names := make([]string, 0, len(obj))
		for name := range obj {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			propSchema, ok := props[name].(map[string]any)
			if !ok {
				if ap, ok := s["additionalProperties"].(bool); ok && !ap {
					issues = append(issues, Issue{
						Path:     appendPath(path, name),
						Code:     "additional_property",
						Expected: "known property",
						Received: Shape(obj[name]),
						Message:  fmt.Sprintf("%s is not allowed by schema", dotPath(appendPath(path, name))),
					})
				}
				continue
			}
			issues = append(issues, validateAt(obj[name], propSchema, appendPath(path, name))...)
		}
		return issues
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return issues
		}
		itemSchema, _ := s["items"].(map[string]any)
		for i, item := range arr {
			issues = append(issues, validateAt(item, itemSchema, appendPath(path, fmt.Sprintf("%d", i)))...)
		}
		return issues
	}
	return issues
}

func anySchemaMatches(v any, schemas []map[string]any, path []string) bool {
	for _, schema := range schemas {
		if len(validateAt(v, schema, path)) == 0 {
			return true
		}
	}
	return false
}

func typeAllowed(v any, typeSpec any) bool {
	if typeSpec == nil {
		return true
	}
	for _, t := range allowedTypes(typeSpec) {
		if t == "null" && v == nil {
			return true
		}
		if t == Shape(v) {
			return true
		}
		if t == "number" && (Shape(v) == "integer" || Shape(v) == "number") {
			return true
		}
	}
	return false
}

func Shape(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case float64:
		if math.Trunc(x) == x {
			return "integer"
		}
		return "number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func ExpectedTypeAt(parameters map[string]any, path []string) string {
	s := schemaAt(parameters, path)
	if s == nil {
		return ""
	}
	return typeString(s["type"])
}

func IsRequiredAt(parameters map[string]any, path []string) bool {
	if len(path) == 0 {
		return true
	}
	parent := schemaAt(parameters, path[:len(path)-1])
	if parent == nil {
		return false
	}
	return requiredSet(parent["required"])[path[len(path)-1]]
}

func schemaAt(s map[string]any, path []string) map[string]any {
	current := s
	for _, part := range path {
		if declaredPrimaryType(current["type"]) == "array" {
			item, _ := current["items"].(map[string]any)
			current = item
			continue
		}
		props, _ := current["properties"].(map[string]any)
		next, _ := props[part].(map[string]any)
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

func allowedTypes(typeSpec any) []string {
	switch t := typeSpec.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return append([]string(nil), t...)
	default:
		return nil
	}
}

func declaredPrimaryType(typeSpec any) string {
	types := allowedTypes(typeSpec)
	if len(types) == 0 {
		return ""
	}
	return types[0]
}

func declaredSchemaKind(s map[string]any) string {
	if kind := declaredPrimaryType(s["type"]); kind != "" {
		return kind
	}
	if _, ok := s["properties"]; ok {
		return "object"
	}
	if _, ok := s["required"]; ok {
		return "object"
	}
	if _, ok := s["items"]; ok {
		return "array"
	}
	return ""
}

func schemaList(v any) []map[string]any {
	switch xs := v.(type) {
	case []any:
		out := make([]map[string]any, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(map[string]any); ok {
				out = append(out, s)
			}
		}
		return out
	case []map[string]any:
		return append([]map[string]any(nil), xs...)
	default:
		return nil
	}
}

func typeString(typeSpec any) string {
	types := allowedTypes(typeSpec)
	if len(types) == 0 {
		return "any"
	}
	return strings.Join(types, "|")
}

func requiredSet(v any) map[string]bool {
	out := map[string]bool{}
	switch xs := v.(type) {
	case []any:
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out[s] = true
			}
		}
	case []string:
		for _, s := range xs {
			out[s] = true
		}
	}
	return out
}

func appendPath(path []string, part string) []string {
	next := append([]string(nil), path...)
	next = append(next, part)
	return next
}

func dotPath(path []string) string {
	if len(path) == 0 {
		return "$"
	}
	return strings.Join(path, ".")
}

var markdownLinkRE = regexp.MustCompile(`\[([^\]]+)\]\(https?://([^)]+)\)`)

func containsDegenerateMarkdownLink(value string) bool {
	matches := markdownLinkRE.FindAllStringSubmatch(value, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		text := match[1]
		target := strings.TrimSpace(match[2])
		targetBase := path.Base(strings.TrimRight(target, "/"))
		if target == text || targetBase == text {
			return true
		}
	}
	return false
}
