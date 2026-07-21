package plugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MarkdownCommand is a slash command declared by a bundle markdown file: a
// YAML frontmatter block (name, description) and a prompt-template body that
// is expanded at invocation with internal/skills argument semantics
// ($ARGUMENTS, positional $0..$n, $WORKSPACE, $SKILL_DIR, ARGUMENTS fallback).
type MarkdownCommand struct {
	Name        string
	Description string
	Body        string
	FilePath    string
}

// markdownFrontmatter is the YAML frontmatter of a bundle markdown command
// file. Unknown fields are rejected so typos fail loading instead of being
// silently ignored (matching the plugin.json manifest contract).
type markdownFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// LoadMarkdownCommands reads all *.md files from dir, parses and validates
// each as a MarkdownCommand, and returns valid commands plus per-file errors
// that name the offending file. A missing directory is not an error (both
// return values are nil), mirroring LoadPlugins.
func LoadMarkdownCommands(dir string) ([]MarkdownCommand, []error) {
	_, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("stat %s: %w", dir, err)}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{fmt.Errorf("read dir %s: %w", dir, err)}
	}

	var commands []MarkdownCommand
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		fullPath := filepath.Join(dir, name)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: read error: %w", name, err))
			continue
		}
		def, err := ParseMarkdownCommand(data, fullPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		commands = append(commands, def)
	}
	return commands, errs
}

// ParseMarkdownCommand parses one markdown command file's content.
func ParseMarkdownCommand(data []byte, path string) (MarkdownCommand, error) {
	fm, body, err := splitCommandFrontmatter(string(data))
	if err != nil {
		return MarkdownCommand{}, err
	}
	var meta markdownFrontmatter
	dec := yaml.NewDecoder(strings.NewReader(fm))
	dec.KnownFields(true)
	if err := dec.Decode(&meta); err != nil {
		return MarkdownCommand{}, fmt.Errorf("frontmatter: %w", err)
	}
	if meta.Name == "" {
		return MarkdownCommand{}, fmt.Errorf("frontmatter name is required")
	}
	if !validName.MatchString(meta.Name) {
		return MarkdownCommand{}, fmt.Errorf("frontmatter name %q is invalid: must match ^[a-z][a-z0-9-]*$", meta.Name)
	}
	if meta.Description == "" {
		return MarkdownCommand{}, fmt.Errorf("frontmatter description is required")
	}
	if body == "" {
		return MarkdownCommand{}, fmt.Errorf("command body is empty")
	}
	return MarkdownCommand{
		Name:        meta.Name,
		Description: meta.Description,
		Body:        body,
		FilePath:    path,
	}, nil
}

// splitCommandFrontmatter splits a markdown command file into YAML
// frontmatter and body, requiring the --- delimiters like SKILL.md files.
func splitCommandFrontmatter(content string) (string, string, error) {
	const delimiter = "---"
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, delimiter) {
		return "", "", fmt.Errorf("must start with --- frontmatter delimiter")
	}
	rest := trimmed[len(delimiter):]
	idx := strings.Index(rest, "\n"+delimiter)
	if idx < 0 {
		return "", "", fmt.Errorf("missing closing --- frontmatter delimiter")
	}
	fm := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n"+delimiter):])
	return fm, body, nil
}
