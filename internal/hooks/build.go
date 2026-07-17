package hooks

import (
	"go-agent-harness/internal/harness"
)

// Adapters groups built hook adapters by lifecycle event, ready to append to
// the matching RunnerConfig slices. One def produces exactly one adapter in
// exactly one slice.
type Adapters struct {
	PreMessage  []harness.PreMessageHook
	PostMessage []harness.PostMessageHook
	PreToolUse  []harness.PreToolUseHook
	PostToolUse []harness.PostToolUseHook
}

// Build constructs one adapter per def and routes it to the slice for its
// event. The logger (nil-safe) receives structured failure lines from the
// adapters at run time. Build performs no loading or trust checks — feed it
// defs from LoadWithOptions.
func Build(defs []HookDef, logger harness.Logger) Adapters {
	var a Adapters
	for _, def := range defs {
		switch def.Kind {
		case KindCommand:
			h := NewCommandHook(def)
			h.Logger = logger
			a.add(def.Event, h, h, h, h)
		case KindHTTP:
			h := NewHTTPHook(def)
			h.Logger = logger
			a.add(def.Event, h, h, h, h)
		}
	}
	return a
}

// add routes one adapter to the slice matching event. Both adapter types
// implement all four interfaces, so a single value is passed for whichever
// slice applies; the other three parameters are ignored for that event.
func (a *Adapters) add(event string, pre harness.PreMessageHook, post harness.PostMessageHook, preTool harness.PreToolUseHook, postTool harness.PostToolUseHook) {
	switch event {
	case EventPreMessage:
		a.PreMessage = append(a.PreMessage, pre)
	case EventPostMessage:
		a.PostMessage = append(a.PostMessage, post)
	case EventPreToolUse:
		a.PreToolUse = append(a.PreToolUse, preTool)
	case EventPostToolUse:
		a.PostToolUse = append(a.PostToolUse, postTool)
	}
}

// LoadedHook is the public listing view of one loaded hook — what startup
// logs, GET /v1/hooks, and the TUI /hooks command render. It intentionally
// omits the command argv and URL: the listing answers "which hooks loaded,
// from where, for which events" without echoing executable detail into
// logs and API responses.
type LoadedHook struct {
	Name    string `json:"name"`
	Event   string `json:"event"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Matcher string `json:"matcher,omitempty"`
	File    string `json:"file"`
}

// Summary is the startup-computed loaded/skipped hook listing. It is
// computed once at startup — never re-derived per request — so the listing
// always matches what the runner actually registered.
type Summary struct {
	Hooks   []LoadedHook `json:"hooks"`
	Skipped []SkipRecord `json:"skipped"`
}

// NewSummary builds the listing from loaded defs and skip records. Empty
// inputs yield non-nil empty slices so the JSON shape is [] rather than
// null.
func NewSummary(defs []HookDef, skips []SkipRecord) Summary {
	s := Summary{
		Hooks:   make([]LoadedHook, 0, len(defs)),
		Skipped: make([]SkipRecord, 0, len(skips)),
	}
	for _, def := range defs {
		s.Hooks = append(s.Hooks, LoadedHook{
			Name:    def.Name,
			Event:   def.Event,
			Kind:    def.Kind,
			Source:  string(def.Source),
			Matcher: def.Matcher,
			File:    def.FilePath,
		})
	}
	s.Skipped = append(s.Skipped, skips...)
	return s
}
