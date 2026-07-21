package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bundle markdown command files (epic #821 slice 4): a <name>.md file with
// YAML frontmatter (name, description) and a prompt-template body.

func TestParseMarkdownCommand_Valid(t *testing.T) {
	content := "---\nname: greet\ndescription: Greet someone\n---\nSay hello to $ARGUMENTS.\n"
	def, err := ParseMarkdownCommand([]byte(content), "/x/commands/greet.md")
	if err != nil {
		t.Fatalf("ParseMarkdownCommand() error = %v", err)
	}
	if def.Name != "greet" {
		t.Fatalf("Name = %q", def.Name)
	}
	if def.Description != "Greet someone" {
		t.Fatalf("Description = %q", def.Description)
	}
	if def.Body != "Say hello to $ARGUMENTS." {
		t.Fatalf("Body = %q", def.Body)
	}
	if def.FilePath != "/x/commands/greet.md" {
		t.Fatalf("FilePath = %q", def.FilePath)
	}
}

func TestParseMarkdownCommand_ValidationErrors(t *testing.T) {
	cases := map[string]struct {
		content string
		wantErr string
	}{
		"missing frontmatter": {
			content: "Say hello\n",
			wantErr: "frontmatter",
		},
		"missing closing delimiter": {
			content: "---\nname: greet\ndescription: x\n",
			wantErr: "frontmatter",
		},
		"missing name": {
			content: "---\ndescription: Greet someone\n---\nbody\n",
			wantErr: "name",
		},
		"invalid name": {
			content: "---\nname: Greet_Me!\ndescription: x\n---\nbody\n",
			wantErr: "name",
		},
		"missing description": {
			content: "---\nname: greet\n---\nbody\n",
			wantErr: "description",
		},
		"empty body": {
			content: "---\nname: greet\ndescription: x\n---\n",
			wantErr: "body",
		},
		"unknown frontmatter field": {
			content: "---\nname: greet\ndescription: x\nbogus: 1\n---\nbody\n",
			wantErr: "bogus",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMarkdownCommand([]byte(tc.content), "/x/commands/x.md")
			if err == nil {
				t.Fatalf("ParseMarkdownCommand() succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestLoadMarkdownCommands(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("greet.md", "---\nname: greet\ndescription: Greet someone\n---\nSay hello to $ARGUMENTS.\n")
	write("review.md", "---\nname: review\ndescription: Review code\n---\nReview $0.\n")
	write("broken.md", "---\nname: broken\n---\nmissing description\n")
	// JSON PluginDef files and other files coexist and are ignored by the
	// markdown loader (the JSON path loads them separately).
	write("legacy.json", `{"name":"legacy","description":"x","handler":"prompt","prompt_template":"y"}`)
	write("notes.txt", "not a command")

	defs, errs := LoadMarkdownCommands(dir)
	if len(defs) != 2 {
		t.Fatalf("LoadMarkdownCommands() defs = %d, want 2 (%v); errs = %v", len(defs), defs, errs)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "broken.md") {
		t.Fatalf("LoadMarkdownCommands() errs = %v, want one naming broken.md", errs)
	}

	// A missing directory is not an error, mirroring LoadPlugins.
	defs, errs = LoadMarkdownCommands(filepath.Join(t.TempDir(), "does-not-exist"))
	if defs != nil || errs != nil {
		t.Fatalf("missing dir: defs = %v, errs = %v, want both nil", defs, errs)
	}
}
