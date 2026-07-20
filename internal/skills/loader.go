package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var kebabCaseRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// argumentNameRe defines the identifier shape for named arguments declared in
// the SKILL.md frontmatter `arguments` field.
var argumentNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// numericNameRe matches argument names made only of digits; those collide
// with positional placeholders ($0..$n) and are rejected.
var numericNameRe = regexp.MustCompile(`^\d+$`)

// reservedArgumentNames cannot be declared as named arguments because they
// are expansion variables provided by the runtime.
var reservedArgumentNames = map[string]bool{
	"ARGUMENTS": true,
	"WORKSPACE": true,
	"SKILL_DIR": true,
}

// validateArgumentNames validates the frontmatter `arguments` declaration.
// Every name must be an identifier, must not be numeric or reserved, and must
// not be declared twice.
func validateArgumentNames(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		switch {
		case numericNameRe.MatchString(name):
			return fmt.Errorf("argument name %q is numeric; positional placeholders reserve $<digits>", name)
		case !argumentNameRe.MatchString(name):
			return fmt.Errorf("argument name %q is not a valid identifier (must match [A-Za-z_][A-Za-z0-9_]*)", name)
		case reservedArgumentNames[name]:
			return fmt.Errorf("argument name %q is reserved", name)
		case seen[name]:
			return fmt.Errorf("argument name %q is declared twice", name)
		}
		seen[name] = true
	}
	return nil
}

// Loader discovers and parses SKILL.md files from configured directories.
type Loader struct {
	config LoaderConfig
}

// NewLoader creates a new Loader with the given configuration.
func NewLoader(config LoaderConfig) *Loader {
	return &Loader{config: config}
}

// Load scans GlobalDir and WorkspaceDir for skill definitions.
// It returns all found skills. Missing directories are silently skipped.
// Errors are returned only for parse/validation failures.
func (l *Loader) Load() ([]Skill, error) {
	var skills []Skill

	global, err := l.loadDir(l.config.GlobalDir, SourceGlobal)
	if err != nil {
		return nil, err
	}
	skills = append(skills, global...)

	local, err := l.loadDir(l.config.WorkspaceDir, SourceLocal)
	if err != nil {
		return nil, err
	}
	skills = append(skills, local...)
	for _, dir := range l.config.PluginDirs {
		pluginSkills, err := l.loadDir(dir, SourcePlugin)
		if err != nil {
			return nil, err
		}
		skills = append(skills, pluginSkills...)
	}

	return skills, nil
}

func (l *Loader) loadDir(dir string, source SkillSource) ([]Skill, error) {
	if dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skills directory %s: %w", dir, err)
	}

	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); os.IsNotExist(err) {
			continue // skip directories without SKILL.md
		}

		skill, err := parseSkillFile(skillFile, entry.Name(), source)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", skillFile, err)
		}
		skills = append(skills, skill)
	}

	return skills, nil
}

func parseSkillFile(path, dirName string, source SkillSource) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("reading file: %w", err)
	}

	content := string(data)
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return Skill{}, err
	}

	var meta frontmatter
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return Skill{}, fmt.Errorf("parsing frontmatter YAML: %w", err)
	}

	// Validate required fields
	if meta.Name == "" {
		return Skill{}, fmt.Errorf("name is required")
	}
	if meta.Description == "" {
		return Skill{}, fmt.Errorf("description is required")
	}
	if meta.Version != 1 {
		return Skill{}, fmt.Errorf("version must be 1, got %d", meta.Version)
	}
	if !kebabCaseRe.MatchString(meta.Name) {
		return Skill{}, fmt.Errorf("name %q must be kebab-case", meta.Name)
	}
	if meta.Name != dirName {
		return Skill{}, fmt.Errorf("name %q must match directory name %q", meta.Name, dirName)
	}

	autoInvoke := true
	if meta.AutoInvoke != nil {
		autoInvoke = *meta.AutoInvoke
	}

	// Validate and default context field
	skillContext := ContextConversation
	if meta.Context != "" {
		switch SkillContext(meta.Context) {
		case ContextConversation, ContextFork:
			skillContext = SkillContext(meta.Context)
		default:
			return Skill{}, fmt.Errorf("context must be %q or %q, got %q", ContextConversation, ContextFork, meta.Context)
		}
	}

	triggers := ExtractTriggers(meta.Description)

	if err := validateArgumentNames(meta.Arguments); err != nil {
		return Skill{}, fmt.Errorf("invalid arguments field: %w", err)
	}

	return Skill{
		Name:         meta.Name,
		Description:  meta.Description,
		Body:         body,
		FilePath:     path,
		Version:      meta.Version,
		AutoInvoke:   autoInvoke,
		AllowedTools: meta.AllowedTools,
		ArgumentHint: meta.ArgumentHint,
		Arguments:    meta.Arguments,
		Source:       source,
		Triggers:     triggers,
		Context:      skillContext,
		Agent:        meta.Agent,
		Verified:     meta.Verified,
		VerifiedAt:   meta.VerifiedAt,
		VerifiedBy:   meta.VerifiedBy,
	}, nil
}

// WriteVerification updates the verified, verified_at, and verified_by fields
// in the SKILL.md frontmatter at the given path, preserving the markdown body.
func WriteVerification(path, verifiedAt, verifiedBy string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	content := string(data)
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return fmt.Errorf("parsing frontmatter: %w", err)
	}

	var meta map[string]any
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return fmt.Errorf("parsing frontmatter YAML: %w", err)
	}
	if meta == nil {
		meta = make(map[string]any)
	}

	meta["verified"] = true
	meta["verified_at"] = verifiedAt
	meta["verified_by"] = verifiedBy

	updated, err := yaml.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshaling frontmatter YAML: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(updated)
	sb.WriteString("---\n")
	if body != "" {
		sb.WriteString(body)
		sb.WriteString("\n")
	}

	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

func splitFrontmatter(content string) (string, string, error) {
	const delimiter = "---"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, delimiter) {
		return "", "", fmt.Errorf("SKILL.md must start with --- frontmatter delimiter")
	}

	// Find the closing delimiter
	rest := trimmed[len(delimiter):]
	idx := strings.Index(rest, "\n"+delimiter)
	if idx < 0 {
		return "", "", fmt.Errorf("SKILL.md missing closing --- frontmatter delimiter")
	}

	fm := rest[:idx]
	body := strings.TrimSpace(rest[idx+len("\n"+delimiter):])

	return fm, body, nil
}
