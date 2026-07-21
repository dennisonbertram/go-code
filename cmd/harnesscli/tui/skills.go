package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"go-agent-harness/cmd/harnesscli/tui/components/slashcomplete"
	"go-agent-harness/internal/plugins"
	"go-agent-harness/internal/skills"
)

// skillNamespace is the slash-command prefix that always names a skill, e.g.
// /skill:deploy. A bare /<name> resolves to a skill only when the name is
// unclaimed by builtin and plugin commands.
const skillNamespace = "skill:"

// loadTUISkills loads the skill registry for the TUI from the same sources
// harnessd uses: the global skills directory, the workspace skills directory,
// and enabled plugin bundle skill directories. It never fails hard — a broken
// or missing source yields whatever loaded successfully, and a fully failed
// load returns an empty registry so the TUI keeps working without skills.
func loadTUISkills(workspace string) (*skills.Registry, *skills.Resolver) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	globalDir := os.Getenv("HARNESS_GLOBAL_DIR")
	if globalDir == "" {
		globalDir = filepath.Join(home, ".go-harness")
	}

	var workspaceDir string
	if workspace != "" {
		workspaceDir = filepath.Join(workspace, ".go-harness", "skills")
	}

	var pluginSkillDirs []string
	pluginRoot := filepath.Join(globalDir, "plugins")
	bundles, err := plugins.EnabledBundles(pluginRoot, plugins.NewStateStore(filepath.Join(pluginRoot, "state.json")))
	if err == nil {
		for _, bundle := range bundles {
			if bundle.SkillsDir != "" {
				pluginSkillDirs = append(pluginSkillDirs, bundle.SkillsDir)
			}
		}
	}

	registry := skills.NewRegistry()
	loader := skills.NewLoader(skills.LoaderConfig{
		GlobalDir:    filepath.Join(globalDir, "skills"),
		WorkspaceDir: workspaceDir,
		PluginDirs:   pluginSkillDirs,
	})
	if err := registry.Load(loader); err != nil {
		registry = skills.NewRegistry() // degrade to empty rather than no skills surface
	}
	return registry, skills.NewResolver(registry)
}

// skillSlashSuggestions returns completion suggestions for loaded skills:
// "skill:<name>" for every skill, plus the bare "<name>" shorthand when the
// name is unclaimed by builtin and plugin commands.
func skillSlashSuggestions(reg *CommandRegistry, skillReg *skills.Registry) []slashcomplete.Suggestion {
	if skillReg == nil {
		return nil
	}
	var out []slashcomplete.Suggestion
	for _, s := range skillReg.List() {
		out = append(out, slashcomplete.Suggestion{
			Name:        skillNamespace + s.Name,
			Description: s.Description,
		})
		if !reg.IsRegistered(s.Name) {
			out = append(out, slashcomplete.Suggestion{
				Name:        s.Name,
				Description: s.Description,
			})
		}
	}
	return out
}

// skillInvocationTarget resolves a parsed slash-command name to a skill name
// following the claim precedence builtin > plugin > shorthand skill:
// "skill:<name>" always names the skill; a bare "<name>" resolves only when
// unclaimed. It reports the skill name and whether the invocation targets a
// skill at all.
func skillInvocationTarget(reg *CommandRegistry, skillReg *skills.Registry, name string) (string, bool) {
	if skillReg == nil {
		return "", false
	}
	skillName := ""
	if rest, found := strings.CutPrefix(name, skillNamespace); found {
		skillName = rest // the skill: namespace always wins over any claim
	} else if !reg.IsRegistered(name) {
		skillName = name
	}
	if skillName == "" {
		return "", false
	}
	if _, ok := skillReg.Get(skillName); !ok {
		return "", false
	}
	return skillName, true
}

// slashArgsText returns the raw argument text following the command name in a
// raw slash-command line ("/deploy a b" -> "a b"), preserving quoting for
// downstream tokenizers.
func slashArgsText(raw string) string {
	rest := strings.TrimPrefix(raw, "/")
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		return strings.TrimSpace(rest[i+1:])
	}
	return ""
}

// expandSkillInvocation expands a slash input to skill content when it names
// a skill, following the claim precedence in skillInvocationTarget. The
// remainder of the line is passed verbatim as the skill's args string through
// the shared skills.Resolver contract (quote-aware $0..$n, named arguments,
// ARGUMENTS fallback). It returns ok=false when the input is not a skill
// invocation or the resolver is unavailable.
func expandSkillInvocation(reg *CommandRegistry, skillReg *skills.Registry, resolver *skills.Resolver, workspace, input string) (string, bool) {
	if resolver == nil {
		return "", false
	}
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", false
	}
	name := strings.ToLower(strings.TrimPrefix(trimmed, "/"))
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", false
	}
	skillName, ok := skillInvocationTarget(reg, skillReg, name)
	if !ok {
		return "", false
	}
	content, err := resolver.ResolveSkill(context.Background(), skillName, slashArgsText(trimmed), workspace)
	if err != nil {
		return "", false
	}
	return content, true
}
