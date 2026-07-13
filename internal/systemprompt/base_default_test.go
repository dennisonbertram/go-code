package systemprompt

import (
	"strings"
	"testing"
)

// The default (blank) intent must compose the base prompt only — no intent
// overlay, and none of the headless/container framing that used to leak in via
// the old `general` default.
func TestResolve_BlankDefaultIntent_IsBaseOnly(t *testing.T) {
	eng, err := NewFileEngine("../../prompts")
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	r, err := eng.Resolve(ResolveRequest{Model: "gpt-4.1"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.ResolvedIntent != "" {
		t.Errorf("ResolvedIntent = %q, want empty (base only)", r.ResolvedIntent)
	}
	if strings.Contains(r.StaticPrompt, "[SECTION INTENT]") {
		t.Errorf("base-only prompt must not contain an INTENT section:\n%s", r.StaticPrompt)
	}
	for _, forbidden := range []string{"/app", "NO user", "Docker container running as root"} {
		if strings.Contains(r.StaticPrompt, forbidden) {
			t.Errorf("base-only prompt must not contain %q (headless/container framing)", forbidden)
		}
	}
	if !strings.Contains(r.StaticPrompt, "go-code") || !strings.Contains(r.StaticPrompt, "find_tool") {
		t.Errorf("base prompt should identify the harness and reference find_tool")
	}
}

// The autonomous overlay carries the headless/container framing, and `general`
// is a back-compat alias for it.
func TestResolve_AutonomousOverlay_AndGeneralAlias(t *testing.T) {
	eng, err := NewFileEngine("../../prompts")
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	auto, err := eng.Resolve(ResolveRequest{AgentIntent: "autonomous", Model: "gpt-4.1"})
	if err != nil {
		t.Fatalf("autonomous: %v", err)
	}
	if !strings.Contains(auto.StaticPrompt, "/app") {
		t.Errorf("autonomous overlay should carry the headless /app framing")
	}
	gen, err := eng.Resolve(ResolveRequest{AgentIntent: "general", Model: "gpt-4.1"})
	if err != nil {
		t.Fatalf("general alias: %v", err)
	}
	if gen.StaticPrompt != auto.StaticPrompt {
		t.Errorf("`general` must resolve to the same prompt as `autonomous` (alias)")
	}
}
