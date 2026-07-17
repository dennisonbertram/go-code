package hooks

import (
	"testing"

	"go-agent-harness/internal/harness"
)

// TestBuild_AdaptersLandInEventSlices is the table-driven mapping check:
// a def for each of the four events must produce an adapter in exactly the
// matching RunnerConfig slice, for both kinds.
func TestBuild_AdaptersLandInEventSlices(t *testing.T) {
	t.Parallel()

	kinds := []string{KindCommand, KindHTTP}
	for _, kind := range kinds {
		defs := []HookDef{
			{Name: "pre-msg", Event: EventPreMessage, Kind: kind, Command: []string{"/bin/true"}, URL: "http://x"},
			{Name: "post-msg", Event: EventPostMessage, Kind: kind, Command: []string{"/bin/true"}, URL: "http://x"},
			{Name: "pre-tool", Event: EventPreToolUse, Kind: kind, Command: []string{"/bin/true"}, URL: "http://x"},
			{Name: "post-tool", Event: EventPostToolUse, Kind: kind, Command: []string{"/bin/true"}, URL: "http://x"},
		}
		if kind == KindCommand {
			for i := range defs {
				defs[i].URL = ""
			}
		} else {
			for i := range defs {
				defs[i].Command = nil
			}
		}

		a := Build(defs, nil)
		if len(a.PreMessage) != 1 || a.PreMessage[0].Name() != "pre-msg" {
			t.Errorf("%s: PreMessage = %v", kind, namesOfPre(a.PreMessage))
		}
		if len(a.PostMessage) != 1 || a.PostMessage[0].Name() != "post-msg" {
			t.Errorf("%s: PostMessage wrong", kind)
		}
		if len(a.PreToolUse) != 1 || a.PreToolUse[0].Name() != "pre-tool" {
			t.Errorf("%s: PreToolUse wrong", kind)
		}
		if len(a.PostToolUse) != 1 || a.PostToolUse[0].Name() != "post-tool" {
			t.Errorf("%s: PostToolUse wrong", kind)
		}
	}
}

func namesOfPre(hooks []harness.PreMessageHook) []string {
	out := []string{}
	for _, h := range hooks {
		out = append(out, h.Name())
	}
	return out
}

// TestBuild_AdaptersSatisfyHarnessInterfaces proves built adapters can be
// assigned directly to RunnerConfig slices (compile-time + runtime check).
func TestBuild_AdaptersSatisfyHarnessInterfaces(t *testing.T) {
	t.Parallel()
	defs := []HookDef{
		{Name: "cmd", Event: EventPreToolUse, Kind: KindCommand, Command: []string{"/bin/true"}},
		{Name: "http", Event: EventPostToolUse, Kind: KindHTTP, URL: "http://127.0.0.1:1"},
	}
	a := Build(defs, nil)
	cfg := harness.RunnerConfig{
		PreToolUseHooks:  a.PreToolUse,
		PostToolUseHooks: a.PostToolUse,
	}
	if len(cfg.PreToolUseHooks) != 1 || len(cfg.PostToolUseHooks) != 1 {
		t.Fatalf("RunnerConfig slices: %+v", cfg)
	}
}

// TestNewSummary verifies the listing shape: loaded hooks carry the public
// fields, skips pass through, and empty lists are non-nil (JSON [] not null).
func TestNewSummary(t *testing.T) {
	t.Parallel()

	t.Run("populated", func(t *testing.T) {
		t.Parallel()
		defs := []HookDef{{
			Name: "deny-rm", Event: EventPreToolUse, Kind: KindCommand,
			Matcher: "bash", Source: SourceProject, FilePath: "/w/.harness/hooks/deny.json",
		}}
		skips := []SkipRecord{{File: "/w/.harness/hooks/bad.json", Reason: SkipReasonUntrusted}}
		s := NewSummary(defs, skips)

		if len(s.Hooks) != 1 {
			t.Fatalf("Hooks: %+v", s.Hooks)
		}
		h := s.Hooks[0]
		if h.Name != "deny-rm" || h.Event != EventPreToolUse || h.Kind != KindCommand ||
			h.Source != string(SourceProject) || h.Matcher != "bash" || h.File != "/w/.harness/hooks/deny.json" {
			t.Errorf("LoadedHook fields: %+v", h)
		}
		if len(s.Skipped) != 1 || s.Skipped[0].Reason != SkipReasonUntrusted {
			t.Errorf("Skipped: %+v", s.Skipped)
		}
	})

	t.Run("empty yields empty slices not nil", func(t *testing.T) {
		t.Parallel()
		s := NewSummary(nil, nil)
		if s.Hooks == nil || s.Skipped == nil {
			t.Fatal("empty summary must have non-nil slices so JSON marshals [] not null")
		}
		if len(s.Hooks) != 0 || len(s.Skipped) != 0 {
			t.Fatalf("empty summary: %+v", s)
		}
	})
}
