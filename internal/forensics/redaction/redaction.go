// Package redaction provides a configurable PII/secret redaction pipeline for
// forensic event payloads. It filters sensitive data (API keys, JWTs, passwords,
// connection strings, etc.) before events are written to JSONL rollouts or audit logs.
package redaction

import (
	"crypto/sha256"
	"fmt"
	"regexp"
)

// ---------------------------------------------------------------------------
// StorageMode
// ---------------------------------------------------------------------------

// StorageMode controls how an event payload is stored.
type StorageMode string

const (
	// StorageModeRedacted applies redaction patterns to string values (default).
	StorageModeRedacted StorageMode = "redacted"
	// StorageModeFull stores the payload without any modification.
	StorageModeFull StorageMode = "full"
	// StorageModeHashed replaces each string value with its SHA-256 hex digest.
	StorageModeHashed StorageMode = "hashed"
	// StorageModeNone drops the event entirely (Apply returns keep=false).
	StorageModeNone StorageMode = "none"
)

// ---------------------------------------------------------------------------
// EventClassConfig
// ---------------------------------------------------------------------------

// EventClassConfig maps event type strings to their StorageMode.
// Event types not present in the map default to StorageModeRedacted.
type EventClassConfig map[string]StorageMode

// ---------------------------------------------------------------------------
// Built-in regex patterns
// ---------------------------------------------------------------------------

// pattern pairs a compiled regex with the redaction label to insert.
type pattern struct {
	re    *regexp.Regexp
	label string
}

// builtinPatterns is the ordered list of built-in secret patterns.
// Each pattern matches a distinct secret type. The patterns are applied in
// order; the first match wins for a given substring.
var builtinPatterns = []pattern{
	// JWTs — three base64url segments separated by dots.
	{
		re:    regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
		label: "jwt",
	},
	// Private keys (PEM block header, possibly with key type name).
	// Note on performance: Go uses RE2 (linear-time DFA), so `[\s\S]*?` does
	// NOT cause catastrophic backtracking. Total scanning work is bounded by
	// maxRedactStringBytes (64 KiB) × maxRedactElements (100k) × len(patterns).
	{
		re:    regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		label: "private_key",
	},
	// AWS access key IDs — always AKIA... (20 uppercase alphanumeric chars).
	{
		re:    regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		label: "aws_key",
	},
	// AWS secret access key — key=value forms where the value looks like an AWS secret.
	{
		re:    regexp.MustCompile(`(?i)aws_secret_access_key\s*[=:]\s*[A-Za-z0-9/+]{40}`),
		label: "aws_secret",
	},
	// Database / broker connection strings.
	{
		re:    regexp.MustCompile(`(?i)(postgres|postgresql|mysql|redis|mongodb|amqp|amqps)://[^\s"']+`),
		label: "connection_string",
	},
	// sk- prefixed API keys (OpenAI, Anthropic, Stripe, etc.)
	{
		re:    regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
		label: "api_key",
	},
	// Generic high-entropy API key value patterns in key=value or key: value forms.
	// Matches: api_key=<32+ hex/alphanum chars>, apikey: <value>, etc.
	{
		re:    regexp.MustCompile(`(?i)(?:api[_-]?key|secret[_-]?key|access[_-]?token|auth[_-]?token)\s*[=:]\s*["']?[A-Za-z0-9_/+.-]{32,}["']?`),
		label: "api_key",
	},
	// Bearer / Authorization header tokens.
	{
		re:    regexp.MustCompile(`(?i)(?:Bearer|Authorization:\s*Bearer)\s+[A-Za-z0-9_\-./+=]{20,}`),
		label: "bearer_token",
	},
}

// ---------------------------------------------------------------------------
// Redactor
// ---------------------------------------------------------------------------

// Redactor applies redaction patterns to text strings. It is safe for
// concurrent use; all state is immutable after construction.
type Redactor struct {
	patterns []pattern
}

// NewRedactor creates a Redactor with the built-in patterns plus any additional
// custom patterns. custom patterns use the label "custom".
func NewRedactor(custom []*regexp.Regexp) *Redactor {
	pats := make([]pattern, len(builtinPatterns), len(builtinPatterns)+len(custom))
	copy(pats, builtinPatterns)
	for _, re := range custom {
		pats = append(pats, pattern{re: re, label: "custom"})
	}
	return &Redactor{patterns: pats}
}

// Redact applies all patterns to text and replaces matches with
// [REDACTED:<label>] markers. It is safe for concurrent use.
func (r *Redactor) Redact(text string) string {
	if text == "" {
		return text
	}
	result := text
	for _, p := range r.patterns {
		result = p.re.ReplaceAllString(result, fmt.Sprintf("[REDACTED:%s]", p.label))
	}
	return result
}

// ---------------------------------------------------------------------------
// Pipeline
// ---------------------------------------------------------------------------

// Pipeline combines a Redactor with per-event-type StorageMode configuration.
// It is safe for concurrent use.
type Pipeline struct {
	redactor *Redactor
	cfg      EventClassConfig
}

// NewPipeline creates a Pipeline using the given Redactor and EventClassConfig.
// A nil Redactor is replaced with a default Redactor using no custom patterns.
func NewPipeline(r *Redactor, cfg EventClassConfig) *Pipeline {
	if r == nil {
		r = NewRedactor(nil)
	}
	if cfg == nil {
		cfg = EventClassConfig{}
	}
	return &Pipeline{redactor: r, cfg: cfg}
}

// Apply processes an event payload according to the configured StorageMode for
// eventType. It returns the (possibly modified) payload and a boolean indicating
// whether the event should be kept (false means drop the event).
//
// The input payload is never mutated; a deep copy is made before modification.
func (p *Pipeline) Apply(eventType string, payload map[string]any) (map[string]any, bool) {
	mode, ok := p.cfg[eventType]
	if !ok {
		mode = StorageModeRedacted
	}

	if mode == StorageModeNone {
		return nil, false
	}

	if payload == nil {
		return map[string]any{}, true
	}

	switch mode {
	case StorageModeFull:
		// HIGH-7 fix: use deep copy to prevent post-Apply aliasing. A shallow
		// copy preserves references to nested maps/slices; callers (or concurrent
		// goroutines in async pipelines) mutating nested structures after Apply
		// returns would silently corrupt the logged payload (TOCTOU integrity).
		return deepTransformStrings(payload, func(s string) string { return s }), true
	case StorageModeHashed:
		return deepTransformStrings(payload, hashString), true
	default: // StorageModeRedacted
		return deepTransformStrings(payload, p.redactor.Redact), true
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// maxRedactDepth limits recursive descent in deepTransformValue to prevent
// goroutine stack overflow from deeply-nested payloads.
//
// HIGH-3 fix (round 29): deepTransformStrings/deepTransformValue had no depth
// limit — a payload with nesting depth 10,000 causes mutual recursion that
// exhausts Go's goroutine stack. Consistent with replay.deepCapWithBudget.
const maxRedactDepth = 20

// maxRedactElements limits the total number of map/slice elements traversed
// across the entire payload to prevent CPU/memory amplification from
// fan-out structures.
const maxRedactElements = 100_000

// deepTransformStrings recursively walks a map, applying fn to every string
// value it encounters. It always returns a new map and never mutates the input.
// HIGH-5 fix: extended to recurse into []any slices so that secrets stored in
// arrays (e.g., messages: [{content:"..."}, ...], header lists, tool arg arrays)
// are redacted/hashed correctly. Without this, any array-valued payload field
// passes through unredacted regardless of its contents.
func deepTransformStrings(m map[string]any, fn func(string) string) map[string]any {
	budget := maxRedactElements
	return deepTransformStringsDepth(m, fn, 0, &budget)
}

func deepTransformStringsDepth(m map[string]any, fn func(string) string, depth int, budget *int) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepTransformValueDepth(v, fn, depth, budget)
	}
	return out
}

// maxRedactStringBytes caps the size of a string before applying redaction
// regex patterns. Without a cap, a single huge string (tens/hundreds of MB)
// causes repeated regex scanning and copying proportional to its size.
//
// HIGH-5 fix: unbounded strings in deepTransformValue can cause O(string_size)
// work per pattern × number of patterns. Cap before applying fn to bound cost.
//
// HIGH-5 fix (round 30): reduced from 1 MiB to 64 KiB to match the 64 KiB cap
// used throughout the forensics packages. 1 MiB × 8 built-in patterns ×
// 100k elements = up to 800 GB of regex work per payload, which defeats the
// maxRedactElements budget.
const maxRedactStringBytes = 64 * 1024 // 64 KiB

// deepTransformValue applies fn to any string it finds while recursing through
// maps and slices. Non-string, non-collection values are returned unchanged.
//
// HIGH-8 fix: extended to handle typed Go containers that encoding/json does
// not produce (map[string]string, []string) but that callers may inject
// directly. Without this, secrets in []string or map[string]string fields
// pass through unredacted regardless of StorageMode.
func deepTransformValue(v any, fn func(string) string) any {
	budget := maxRedactElements
	return deepTransformValueDepth(v, fn, 0, &budget)
}

func deepTransformValueDepth(v any, fn func(string) string, depth int, budget *int) any {
	// HIGH-4 fix (round 32): check budget BEFORE decrementing so the element
	// at the exact boundary (budget transitions 1→0) is still processed.
	// HIGH-3 fix (round 33): decrement after the guard to avoid the off-by-one
	// where budget=1 → decrement → budget=0 → guard fires → element returned
	// unredacted. The correct sequence: check first, then consume the token.
	if depth > maxRedactDepth || *budget <= 0 {
		return v
	}
	*budget--
	switch val := v.(type) {
	case string:
		// HIGH-5 fix: cap string before regex processing to bound CPU/memory.
		if len(val) > maxRedactStringBytes {
			val = val[:maxRedactStringBytes]
		}
		return fn(val)
	case map[string]any:
		return deepTransformStringsDepth(val, fn, depth+1, budget)
	case map[string]string:
		// HIGH-8 fix: typed string map — transform values only (not keys).
		// HIGH-6 fix (round 29): applying fn to keys causes key collision when
		// two distinct keys both match a redaction pattern — the second assignment
		// silently drops the first entry from the forensic record (data loss).
		// HIGH-4 fix (round 33): apply per-child budget check so typed containers
		// don't bypass the element budget entirely.
		out := make(map[string]string, len(val))
		for k, s := range val {
			if *budget <= 0 {
				out[k] = s // budget exhausted — leave unprocessed
				continue
			}
			*budget--
			out[k] = fn(s)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = deepTransformValueDepth(elem, fn, depth+1, budget)
		}
		return out
	case []string:
		// HIGH-8 fix: typed string slice — transform each element.
		// HIGH-4 fix (round 33): apply per-child budget check.
		out := make([]string, len(val))
		for i, s := range val {
			if *budget <= 0 {
				out[i] = s // budget exhausted — leave unprocessed
				continue
			}
			*budget--
			out[i] = fn(s)
		}
		return out
	default:
		return v
	}
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum)
}

// ---------------------------------------------------------------------------
// Sentinel helpers used by the runner integration.
// ---------------------------------------------------------------------------

// RedactPayload is a convenience wrapper: it applies the Pipeline and returns
// the processed payload plus whether the event should be kept.
// It is equivalent to calling p.Apply directly.
func RedactPayload(p *Pipeline, eventType string, payload map[string]any) (map[string]any, bool) {
	if p == nil {
		return payload, true
	}
	return p.Apply(eventType, payload)
}
