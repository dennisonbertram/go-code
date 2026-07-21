package skills

import (
	"strings"
	"testing"
)

// ExpandTemplate is the shared argument-expansion engine for skill invocation
// and bundle markdown commands (epic #821 slice 4). These tests pin the
// exported contract; the existing hook/resolver tests pin the delegating
// wrappers.

func TestExpandTemplate_PositionalAndBuiltinVars(t *testing.T) {
	body := "First: $0. Second: $1. Missing: $5. All: $ARGUMENTS. Workspace: $WORKSPACE. Dir: $SKILL_DIR. Shell var: $HOME."
	got := ExpandTemplate(body, nil, `one "two words"`, "/ws", "/bundles/demo/commands")
	want := `First: one. Second: two words. Missing: . All: one "two words". Workspace: /ws. Dir: /bundles/demo/commands. Shell var: $HOME.`
	if got != want {
		t.Fatalf("ExpandTemplate() =\n%q\nwant\n%q", got, want)
	}
}

func TestExpandTemplate_NamedArgumentsBindInDeclarationOrder(t *testing.T) {
	got := ExpandTemplate("Hello $who, you are $mood.", []string{"who", "mood"}, "world", "/ws", "/dir")
	want := "Hello world, you are ."
	if got != want {
		t.Fatalf("ExpandTemplate() = %q, want %q", got, want)
	}
}

func TestExpandTemplate_ArgumentsFallback(t *testing.T) {
	t.Run("appends raw args when no placeholder is referenced", func(t *testing.T) {
		got := ExpandTemplate("Summarize the notes.", nil, "these specific notes", "/ws", "/dir")
		want := "Summarize the notes.\nARGUMENTS: these specific notes"
		if got != want {
			t.Fatalf("ExpandTemplate() = %q, want %q", got, want)
		}
	})

	t.Run("no fallback line without args", func(t *testing.T) {
		if got := ExpandTemplate("Summarize.", nil, "", "/ws", "/dir"); got != "Summarize." {
			t.Fatalf("ExpandTemplate() = %q", got)
		}
	})

	t.Run("no fallback when a placeholder is referenced", func(t *testing.T) {
		got := ExpandTemplate("Summarize $ARGUMENTS.", nil, "x", "/ws", "/dir")
		if got != "Summarize x." {
			t.Fatalf("ExpandTemplate() = %q", got)
		}
	})

	t.Run("no fallback when a declared named argument is referenced", func(t *testing.T) {
		got := ExpandTemplate("Summarize $topic.", []string{"topic"}, "x", "/ws", "/dir")
		if got != "Summarize x." {
			t.Fatalf("ExpandTemplate() = %q", got)
		}
	})
}

func TestHasArgPlaceholder(t *testing.T) {
	cases := []struct {
		body      string
		namedArgs []string
		want      bool
	}{
		{"$ARGUMENTS here", nil, true},
		{"positional $2", nil, true},
		{"plain text", nil, false},
		{"uses $topic", []string{"topic"}, true},
		{"uses $other", []string{"topic"}, false},
		{"shell $HOME", nil, false},
	}
	for _, tc := range cases {
		if got := HasArgPlaceholder(tc.body, tc.namedArgs); got != tc.want {
			t.Errorf("HasArgPlaceholder(%q, %v) = %t, want %t", tc.body, tc.namedArgs, got, tc.want)
		}
	}
}

func TestBuildTemplateVars(t *testing.T) {
	vars := BuildTemplateVars([]string{"who"}, `world "bright new"`, "/ws", "/dir")
	for key, want := range map[string]string{
		"$ARGUMENTS": `world "bright new"`,
		"$0":         "world",
		"$1":         "bright new",
		"$who":       "world",
		"$WORKSPACE": "/ws",
		"$SKILL_DIR": "/dir",
	} {
		if vars[key] != want {
			t.Errorf("vars[%q] = %q, want %q", key, vars[key], want)
		}
	}
	if _, ok := vars["$2"]; ok {
		t.Errorf("unexpected $2 binding: %q", vars["$2"])
	}
	if !strings.HasPrefix(vars["$ARGUMENTS"], "world") {
		t.Errorf("$ARGUMENTS = %q", vars["$ARGUMENTS"])
	}
}
