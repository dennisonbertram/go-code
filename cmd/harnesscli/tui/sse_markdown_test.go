package tui_test

// sse_markdown_test.go — regression coverage for the LIVE assistant streaming
// path. The production stream delivers assistant text as SSEEventMsg events of
// type "assistant.message.delta". Previously that path appended raw text via
// viewport.AppendChunk, bypassing the glamour markdown renderer entirely (the
// glamour path was only reachable through AssistantDeltaMsg, which is never
// produced outside of tests). These tests drive the real SSE event so the
// renderer wiring is exercised the same way users experience it.

import (
	"strings"
	"testing"

	tui "go-agent-harness/cmd/harnesscli/tui"
)

// TestSSEAssistantDelta_RendersMarkdownViaGlamour verifies that markdown in a
// streamed assistant response is rendered (not shown as raw source). Bullet
// lists are converted to "•" by glamour; the raw "- " prefix must not survive.
func TestSSEAssistantDelta_RendersMarkdownViaGlamour(t *testing.T) {
	m := initModel(t, 100, 30)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-md-1"})
	model := m2.(tui.Model)

	// "# " and backticks make looksLikeMarkdown() true so the glamour branch runs.
	m3, _ := model.Update(tui.SSEEventMsg{
		EventType: "assistant.message.delta",
		Raw:       []byte(`{"content":"## Heading\n\n- alpha\n- beta\n\ninline ` + "`code`" + ` here\n"}`),
	})
	model = m3.(tui.Model)

	view := model.View()
	if !strings.Contains(view, "Heading") {
		t.Fatalf("expected heading text in view; view=%q", view)
	}
	if !strings.Contains(view, "•") {
		t.Errorf("assistant markdown must be glamour-rendered (bullet list -> '•'); raw '- ' was not converted.\nview=%q", view)
	}
	if strings.Contains(view, "`code`") {
		t.Errorf("inline code backticks must be consumed by glamour, not shown raw.\nview=%q", view)
	}
}

// TestSSEAssistantDelta_MultiLineNotCorruptedAcrossDeltas verifies that content
// streamed across multiple deltas (chunk boundaries falling mid-content) keeps
// each source line intact instead of merging adjacent lines — the failure mode
// observed as "func maifmt.Println(...)" with the old AppendChunk path.
func TestSSEAssistantDelta_MultiLineNotCorruptedAcrossDeltas(t *testing.T) {
	m := initModel(t, 100, 30)
	m = m.WithCancelRun(func() {})
	m2, _ := m.Update(tui.RunStartedMsg{RunID: "run-md-2"})
	model := m2.(tui.Model)

	// A fenced code block split so a chunk boundary lands inside a line.
	deltas := []string{
		"Here is code:\n\n```go\nfunc main() {\n\tfmt.Pri",
		"ntln(\"hi\")\n}\n```\n",
	}
	for _, d := range deltas {
		raw := []byte(`{"content":` + jsonQuote(d) + `}`)
		mn, _ := model.Update(tui.SSEEventMsg{EventType: "assistant.message.delta", Raw: raw})
		model = mn.(tui.Model)
	}

	view := model.View()
	if strings.Contains(view, "func mainfmt") || strings.Contains(view, "func maifmt") {
		t.Errorf("adjacent code lines must not be merged across delta boundaries.\nview=%q", view)
	}
	if !strings.Contains(view, "fmt.Println") {
		t.Errorf("expected intact 'fmt.Println' line in rendered code block.\nview=%q", view)
	}
}

// jsonQuote returns a minimal JSON-quoted string for embedding in test payloads.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
