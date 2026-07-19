package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseHooksCommand verifies "/hooks" parses like other slash commands.
func TestParseHooksCommand(t *testing.T) {
	t.Parallel()
	cmd, ok := ParseCommand("/hooks")
	if !ok {
		t.Fatal("ParseCommand(\"/hooks\") returned ok=false")
	}
	if cmd.Name != "hooks" {
		t.Errorf("Name: got %q, want %q", cmd.Name, "hooks")
	}
}

// TestHooksCommandRegistered verifies /hooks dispatches (not CmdUnknown) and
// appears alongside existing commands without shadowing them.
func TestHooksCommandRegistered(t *testing.T) {
	t.Parallel()
	reg := NewCommandRegistry()
	cmd, ok := ParseCommand("/hooks")
	if !ok {
		t.Fatal("parse failed")
	}
	result := reg.Dispatch(cmd)
	if result.Status == CmdUnknown {
		t.Fatalf("/hooks is not registered: %+v", result)
	}

	// Regression: neighboring commands must still dispatch.
	for _, name := range []string{"stats", "help", "subagents", "config"} {
		c, _ := ParseCommand("/" + name)
		if r := reg.Dispatch(c); r.Status == CmdUnknown {
			t.Errorf("/%s became unknown after adding /hooks", name)
		}
	}
}

// TestLoadHooksCmdDecodes verifies the API client fetches and decodes the
// /v1/hooks payload.
func TestLoadHooksCmdDecodes(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/hooks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hooks": []map[string]any{{
				"name": "deny-rm", "event": "pre_tool_use", "kind": "command",
				"source": "project", "matcher": "bash", "file": "/w/.harness/hooks/deny.json",
			}},
			"skipped": []map[string]any{{
				"file": "/w/.harness/hooks/evil.json", "reason": "untrusted",
			}},
		})
	}))
	defer ts.Close()

	msg := loadHooksCmd(ts.URL, "")()
	loaded, ok := msg.(HooksLoadedMsg)
	if !ok {
		t.Fatalf("expected HooksLoadedMsg, got %T (%+v)", msg, msg)
	}
	if len(loaded.Hooks) != 1 || loaded.Hooks[0].Name != "deny-rm" || loaded.Hooks[0].Matcher != "bash" {
		t.Fatalf("hooks payload: %+v", loaded.Hooks)
	}
	if len(loaded.Skipped) != 1 || loaded.Skipped[0].Reason != "untrusted" {
		t.Fatalf("skipped payload: %+v", loaded.Skipped)
	}
}

// TestLoadHooksCmdServerError verifies a non-200 surfaces as a failure message.
func TestLoadHooksCmdServerError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	msg := loadHooksCmd(ts.URL, "")()
	if _, ok := msg.(HooksLoadFailedMsg); !ok {
		t.Fatalf("expected HooksLoadFailedMsg, got %T", msg)
	}
}

// TestFormatHooksLines covers the rendered table: loaded hooks, the skipped
// section with reasons, and the empty state.
func TestFormatHooksLines(t *testing.T) {
	t.Parallel()

	t.Run("populated", func(t *testing.T) {
		t.Parallel()
		lines := formatHooksLines(HooksLoadedMsg{
			Hooks: []hookEntry{
				{Name: "deny-rm", Event: "pre_tool_use", Kind: "command", Source: "project", Matcher: "bash"},
				{Name: "audit", Event: "post_tool_use", Kind: "http", Source: "user"},
			},
			Skipped: []hookSkipEntry{
				{File: "/w/.harness/hooks/evil.json", Reason: "untrusted"},
			},
		})
		joined := strings.Join(lines, "\n")
		for _, want := range []string{"deny-rm", "pre_tool_use", "command", "project", "bash", "audit", "user", "evil.json", "untrusted"} {
			if !strings.Contains(joined, want) {
				t.Errorf("rendered output missing %q:\n%s", want, joined)
			}
		}
	})

	t.Run("empty state", func(t *testing.T) {
		t.Parallel()
		lines := formatHooksLines(HooksLoadedMsg{})
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "no hooks") && !strings.Contains(joined, "No hooks") {
			t.Errorf("empty state should say no hooks loaded:\n%s", joined)
		}
	})
}
