package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ValidateSchema validates data against a JSON Schema (draft-07 compatible subset).
// Returns nil if the data is valid, or a descriptive error if not.
//
// Supported keywords: type, required, properties, items, enum, additionalProperties.
func ValidateSchema(schema map[string]any, data any) error {
	return validateAgainstSchema(schema, data, "")
}

// ParseStructuredOutput parses a model output string and validates it against
// the given JSON Schema. It handles outputs that are:
//   - Pure JSON objects/arrays
//   - JSON embedded in markdown code blocks (```json ... ```)
//
// Returns the parsed and validated value on success.
func ParseStructuredOutput(output string, schema map[string]any) (any, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, fmt.Errorf("empty output")
	}

	var parsed any

	// Try direct JSON parsing first
	if err := json.Unmarshal([]byte(output), &parsed); err == nil {
		if err := ValidateSchema(schema, parsed); err != nil {
			return nil, err
		}
		return parsed, nil
	}

	// Try to extract JSON from markdown code blocks
	extracted := extractJSONFromMarkdown(output)
	if extracted != "" {
		if err := json.Unmarshal([]byte(extracted), &parsed); err == nil {
			if err := ValidateSchema(schema, parsed); err != nil {
				return nil, err
			}
			return parsed, nil
		}
	}

	return nil, fmt.Errorf("output is not valid JSON matching the schema: %s", truncate(output, 200))
}

func validateAgainstSchema(schema map[string]any, data any, path string) error {
	// Handle type checking
	if typeVal, ok := schema["type"]; ok {
		if err := checkType(typeVal, data, path); err != nil {
			return err
		}
	}

	// Handle enum
	if enumVal, ok := schema["enum"]; ok {
		if err := checkEnum(enumVal, data, path); err != nil {
			return err
		}
	}

	switch v := data.(type) {
	case map[string]any:
		return validateObject(schema, v, path)
	case []any:
		return validateArray(schema, v, path)
	}

	return nil
}

func checkType(expected any, data any, path string) error {
	typeStr, ok := expected.(string)
	if !ok {
		// Could be an array of types like ["string", "null"]
		types, ok := expected.([]any)
		if !ok {
			return nil
		}
		for _, t := range types {
			if ts, ok := t.(string); ok {
				if typeMatches(ts, data) {
					return nil
				}
			}
		}
		return fmt.Errorf("%s: expected one of types %v", path, types)
	}

	if !typeMatches(typeStr, data) {
		return fmt.Errorf("%s: expected type %s, got %T", path, typeStr, data)
	}
	return nil
}

func typeMatches(typeName string, data any) bool {
	switch typeName {
	case "string":
		_, ok := data.(string)
		return ok
	case "number":
		switch data.(type) {
		case float64, float32, int, int64, int32, json.Number:
			return true
		}
		return false
	case "integer":
		switch v := data.(type) {
		case float64:
			return v == float64(int64(v))
		case int, int64, int32:
			return true
		case json.Number:
			_, err := v.Int64()
			return err == nil
		}
		return false
	case "boolean":
		_, ok := data.(bool)
		return ok
	case "array":
		_, ok := data.([]any)
		return ok
	case "object":
		_, ok := data.(map[string]any)
		return ok
	case "null":
		return data == nil
	default:
		return true
	}
}

func checkEnum(enumVal any, data any, path string) error {
	enumList, ok := enumVal.([]any)
	if !ok {
		return nil
	}
	for _, allowed := range enumList {
		if deepEqual(data, allowed) {
			return nil
		}
	}
	return fmt.Errorf("%s: value %v is not in enum %v", path, data, enumList)
}

func validateObject(schema map[string]any, obj map[string]any, path string) error {
	// Check required fields
	if required, ok := schema["required"]; ok {
		if reqList, ok := required.([]any); ok {
			for _, r := range reqList {
				field, ok := r.(string)
				if !ok {
					continue
				}
				if _, exists := obj[field]; !exists {
					return fmt.Errorf("%s: missing required field %q", path, field)
				}
			}
		}
	}

	// Check properties
	additionalAllowed := true
	if ap, ok := schema["additionalProperties"]; ok {
		if apBool, ok := ap.(bool); ok {
			additionalAllowed = apBool
		}
	}

	if props, ok := schema["properties"]; ok {
		propMap, ok := props.(map[string]any)
		if !ok {
			return nil
		}

		for key, propSchema := range propMap {
			value, exists := obj[key]
			if !exists {
				continue
			}
			childPath := joinPath(path, key)
			if ps, ok := propSchema.(map[string]any); ok {
				if err := validateAgainstSchema(ps, value, childPath); err != nil {
					return err
				}
			}
		}

		// Check for unlisted properties
		if !additionalAllowed {
			for key := range obj {
				if _, listed := propMap[key]; !listed {
					return fmt.Errorf("%s: additional property %q not allowed", path, key)
				}
			}
		}
	}

	return nil
}

func validateArray(schema map[string]any, arr []any, path string) error {
	if items, ok := schema["items"]; ok {
		if itemSchema, ok := items.(map[string]any); ok {
			for i, item := range arr {
				childPath := fmt.Sprintf("%s[%d]", path, i)
				if err := validateAgainstSchema(itemSchema, item, childPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func deepEqual(a, b any) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

var jsonBlockRe = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")

func extractJSONFromMarkdown(text string) string {
	matches := jsonBlockRe.FindStringSubmatch(text)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
