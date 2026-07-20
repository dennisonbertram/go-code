package harness

import (
	"reflect"
	"strings"
)

// deepClonePayload returns a fully isolated deep copy of a map[string]any.
// It clones nested maps and slices while preserving nil-valued keys.
func deepClonePayload(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCloneValue(v)
	}
	return out
}

// deepCloneValue recursively deep-clones any value containing mutable
// reference types (maps and slices). Scalars and nil are returned as-is.
func deepCloneValue(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		out := reflect.MakeMap(rv.Type())
		for _, key := range rv.MapKeys() {
			cloned := deepCloneValue(rv.MapIndex(key).Interface())
			cv := reflect.ValueOf(cloned)
			if cv.IsValid() {
				out.SetMapIndex(key, cv)
			} else {
				out.SetMapIndex(key, reflect.Zero(rv.Type().Elem()))
			}
		}
		return out.Interface()
	case reflect.Slice:
		if rv.IsNil() {
			return v
		}
		out := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			cloned := deepCloneValue(rv.Index(i).Interface())
			if cv := reflect.ValueOf(cloned); cv.IsValid() {
				out.Index(i).Set(cv)
			}
		}
		return out.Interface()
	default:
		return v
	}
}

func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// copyMessages returns a deep copy of msgs where each Message has an
// independent ToolCalls slice, preventing callers from mutating runner state.
func copyMessages(msgs []Message) []Message {
	if msgs == nil {
		return nil
	}
	result := make([]Message, len(msgs))
	for i := range msgs {
		result[i] = msgs[i].Clone()
	}
	return result
}

// buildTurnMessages assembles the per-step message list with cache-friendly
// ordering. Stable content (the system prompt) goes first, then the growing
// conversation history — so the provider-visible prefix (tools + system +
// history) only ever grows by appending and stays a valid cached prefix across
// steps. All volatile per-step content (working memory, observational memory,
// dynamic rules, plan-mode guidance, and the runtime context, which carries the
// step number, token and cost totals, and a timestamp) is placed at the tail,
// after the history, where it can change every step without invalidating the
// cached prefix. Empty snippets are skipped.
func (r *Runner) buildTurnMessages(systemPrompt string, messages []Message, workingMemory, observationalMemory, ruleContent, planModeGuidance, runtimeContext string) []Message {
	tm := make([]Message, 0, len(messages)+6)
	if systemPrompt != "" {
		tm = append(tm, Message{Role: "system", Content: systemPrompt})
	}
	tm = append(tm, copyMessages(messages)...)
	for _, tail := range []string{workingMemory, observationalMemory, ruleContent, planModeGuidance, runtimeContext} {
		if strings.TrimSpace(tail) != "" {
			tm = append(tm, Message{Role: "system", Content: tail})
		}
	}
	return tm
}
