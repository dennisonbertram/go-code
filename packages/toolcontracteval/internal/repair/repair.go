package repair

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"go-agent-harness/packages/toolcontracteval/internal/schema"
)

type Candidate struct {
	Name   string `json:"name"`
	Safety string `json:"safety"`
}

type Simulation struct {
	Repair               string         `json:"repair"`
	Safety               string         `json:"safety"`
	BeforeValid          bool           `json:"before_valid"`
	Applied              bool           `json:"applied"`
	AfterValid           bool           `json:"after_valid"`
	SemanticNoteRequired bool           `json:"semantic_note_required,omitempty"`
	RepairedArguments    string         `json:"repaired_arguments,omitempty"`
	IssuesAfter          []schema.Issue `json:"issues_after,omitempty"`
}

var DefaultCandidates = []Candidate{
	{Name: "null_optional_omit", Safety: "safe"},
	{Name: "stringified_array_parse", Safety: "safe"},
	{Name: "bare_string_to_array", Safety: "safe"},
	{Name: "empty_object_placeholder_to_array", Safety: "safe"},
	{Name: "markdown_path_unwrap", Safety: "safe"},
	{Name: "file_path_to_path_alias", Safety: "safe"},
	{Name: "read_window_default", Safety: "semantic"},
}

func SimulateAll(toolName string, raw json.RawMessage, parameters map[string]any) []Simulation {
	out := make([]Simulation, 0, len(DefaultCandidates))
	for _, candidate := range DefaultCandidates {
		out = append(out, Simulate(candidate, toolName, raw, parameters))
	}
	return out
}

func Simulate(candidate Candidate, toolName string, raw json.RawMessage, parameters map[string]any) Simulation {
	before := schema.ValidateRaw(toolName, raw, parameters)
	sim := Simulation{
		Repair:      candidate.Name,
		Safety:      candidate.Safety,
		BeforeValid: len(before.Issues) == 0,
	}
	if sim.BeforeValid {
		sim.AfterValid = true
		return sim
	}

	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		sim.IssuesAfter = before.Issues
		return sim
	}

	var applied bool
	switch candidate.Name {
	case "null_optional_omit":
		applied = omitOptionalNulls(args, parameters, nil)
	case "stringified_array_parse":
		applied = repairStringifiedArrays(args, parameters, nil)
	case "bare_string_to_array":
		applied = repairBareStrings(args, parameters, nil)
	case "empty_object_placeholder_to_array":
		applied = repairEmptyObjects(args, parameters, nil)
	case "markdown_path_unwrap":
		applied = unwrapMarkdownPaths(args)
	case "file_path_to_path_alias":
		applied = repairFilePathAlias(toolName, args)
	case "read_window_default":
		applied = repairReadWindow(toolName, args)
		sim.SemanticNoteRequired = applied
	}

	sim.Applied = applied
	if !applied {
		sim.IssuesAfter = before.Issues
		return sim
	}
	repaired, err := json.Marshal(args)
	if err != nil {
		sim.IssuesAfter = before.Issues
		return sim
	}
	after := schema.ValidateRaw(toolName, repaired, parameters)
	sim.AfterValid = len(after.Issues) == 0
	sim.RepairedArguments = string(repaired)
	sim.IssuesAfter = after.Issues
	return sim
}

func omitOptionalNulls(obj map[string]any, s map[string]any, path []string) bool {
	props, _ := s["properties"].(map[string]any)
	applied := false
	for name, v := range obj {
		propSchema, _ := props[name].(map[string]any)
		if propSchema == nil {
			continue
		}
		if v == nil && !schema.IsRequiredAt(s, []string{name}) {
			delete(obj, name)
			applied = true
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			applied = omitOptionalNulls(nested, propSchema, append(path, name)) || applied
		}
	}
	return applied
}

func repairStringifiedArrays(obj map[string]any, s map[string]any, path []string) bool {
	return repairArrayShape(obj, s, func(v string) (any, bool) {
		var arr []any
		if err := json.Unmarshal([]byte(v), &arr); err != nil {
			return nil, false
		}
		return arr, true
	})
}

func repairBareStrings(obj map[string]any, s map[string]any, path []string) bool {
	return repairArrayShape(obj, s, func(v string) (any, bool) {
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			return nil, false
		}
		return []any{v}, true
	})
}

func repairEmptyObjects(obj map[string]any, s map[string]any, path []string) bool {
	applied := false
	props, _ := s["properties"].(map[string]any)
	for name, v := range obj {
		propSchema, _ := props[name].(map[string]any)
		if propSchema == nil {
			continue
		}
		if schema.ExpectedTypeAt(s, []string{name}) == "array" {
			if nested, ok := v.(map[string]any); ok && len(nested) == 0 {
				obj[name] = []any{}
				applied = true
			}
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			applied = repairEmptyObjects(nested, propSchema, append(path, name)) || applied
		}
	}
	return applied
}

func repairArrayShape(obj map[string]any, s map[string]any, convert func(string) (any, bool)) bool {
	applied := false
	props, _ := s["properties"].(map[string]any)
	for name, v := range obj {
		propSchema, _ := props[name].(map[string]any)
		if propSchema == nil {
			continue
		}
		if schema.ExpectedTypeAt(s, []string{name}) == "array" {
			text, ok := v.(string)
			if !ok {
				continue
			}
			if converted, ok := convert(text); ok {
				obj[name] = converted
				applied = true
			}
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			applied = repairArrayShape(nested, propSchema, convert) || applied
		}
	}
	return applied
}

var markdownLinkRE = regexp.MustCompile(`\[([^\]]+)\]\(https?://([^)]+)\)`)

func unwrapMarkdownPaths(obj map[string]any) bool {
	applied := false
	for key, v := range obj {
		switch x := v.(type) {
		case string:
			if !strings.Contains(strings.ToLower(key), "path") {
				continue
			}
			next, ok := unwrapDegenerateMarkdownLink(x)
			if ok {
				obj[key] = next
				applied = true
			}
		case map[string]any:
			applied = unwrapMarkdownPaths(x) || applied
		}
	}
	return applied
}

func unwrapDegenerateMarkdownLink(value string) (string, bool) {
	matches := markdownLinkRE.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, false
	}
	out := value
	applied := false
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		text := value[m[2]:m[3]]
		urlHostOrPath := value[m[4]:m[5]]
		urlHostOrPath = strings.TrimSpace(urlHostOrPath)
		candidateBase := path.Base(strings.TrimRight(urlHostOrPath, "/"))
		if candidateBase != text && urlHostOrPath != text {
			continue
		}
		out = out[:m[0]] + text + out[m[1]:]
		applied = true
	}
	return out, applied
}

func repairReadWindow(toolName string, obj map[string]any) bool {
	if toolName != "read" {
		return false
	}
	_, hasOffset := obj["offset"]
	_, hasLimit := obj["limit"]
	if hasOffset == hasLimit {
		return false
	}
	if hasLimit {
		obj["offset"] = float64(0)
		return true
	}
	obj["limit"] = float64(2000)
	return true
}

func repairFilePathAlias(toolName string, obj map[string]any) bool {
	if toolName != "read" {
		return false
	}
	if _, hasPath := obj["path"]; hasPath {
		return false
	}
	filePath, ok := obj["file_path"]
	if !ok {
		return false
	}
	if _, ok := filePath.(string); !ok {
		return false
	}
	obj["path"] = filePath
	delete(obj, "file_path")
	return true
}
