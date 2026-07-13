package systemprompt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (e *FileEngine) Resolve(req ResolveRequest) (ResolvedPrompt, error) {
	intent := strings.TrimSpace(req.AgentIntent)
	if intent == "" {
		intent = strings.TrimSpace(req.DefaultAgentIntent)
	}
	if intent == "" {
		intent = strings.TrimSpace(e.defaults.intent)
	}
	// A blank intent means "base prompt only" — no intent overlay is applied.
	var intentPrompt string
	if intent != "" {
		p, ok := e.intents[intent]
		if !ok {
			return ResolvedPrompt{}, invalid("agent_intent", intent, "intent not found")
		}
		intentPrompt = p
	}

	profileName, modelFallback, err := e.resolveModelProfile(req.Model, req.PromptProfile)
	if err != nil {
		return ResolvedPrompt{}, err
	}
	profile := e.profileByName[profileName]

	behaviors, err := resolveExtensions(req.Extensions.Behaviors, e.behaviorByID, "prompt_extensions.behaviors")
	if err != nil {
		return ResolvedPrompt{}, err
	}
	talents, err := resolveExtensions(req.Extensions.Talents, e.talentByID, "prompt_extensions.talents")
	if err != nil {
		return ResolvedPrompt{}, err
	}

	warnings := make([]Warning, 0, 1)
	var skillSections []resolvedExtension
	if len(req.Extensions.Skills) > 0 {
		if e.skillResolver == nil {
			warnings = append(warnings, Warning{
				Code:    "skills_no_resolver",
				Message: "skills requested but no skill resolver is configured",
			})
		} else {
			for _, skillName := range req.Extensions.Skills {
				name := strings.TrimSpace(skillName)
				if name == "" {
					continue
				}
				content, err := e.skillResolver.ResolveSkill(context.Background(), name, "", "")
				if err != nil {
					warnings = append(warnings, Warning{
						Code:    "skill_resolve_failed",
						Message: fmt.Sprintf("failed to resolve skill %q: %v", name, err),
					})
					continue
				}
				skillSections = append(skillSections, resolvedExtension{id: name, content: content})
			}
		}
	}

	// Load AGENTS.md from the workspace if a path was provided.
	agentsMdContent, agentsMdErr := readAgentsMd(req.WorkspacePath)
	if agentsMdErr != nil {
		warnings = append(warnings, Warning{
			Code:    "agents_md_read_failed",
			Message: fmt.Sprintf("failed to read AGENTS.md from workspace %q: %v", req.WorkspacePath, agentsMdErr),
		})
	}

	sections := make([]promptSection, 0, 9)
	sections = append(sections,
		promptSection{Name: "BASE", Content: e.basePrompt},
		promptSection{Name: "INTENT", Content: intentPrompt},
		promptSection{Name: "MODEL_PROFILE", Content: profile.Content},
	)
	if agentsMdContent != "" {
		sections = append(sections, promptSection{Name: "AGENTS_MD", Content: agentsMdContent})
	}
	if taskContext := strings.TrimSpace(req.TaskContext); taskContext != "" {
		sections = append(sections, promptSection{Name: "TASK_CONTEXT", Content: taskContext})
	}
	for _, behavior := range behaviors {
		sections = append(sections, promptSection{Name: "BEHAVIOR", Content: behavior.content, Meta: behavior.id})
	}
	for _, talent := range talents {
		sections = append(sections, promptSection{Name: "TALENT", Content: talent.content, Meta: talent.id})
	}
	for _, skill := range skillSections {
		sections = append(sections, promptSection{Name: "SKILL", Content: skill.content, Meta: skill.id})
	}
	if custom := strings.TrimSpace(req.Extensions.Custom); custom != "" {
		sections = append(sections, promptSection{Name: "CUSTOM", Content: custom})
	}

	return ResolvedPrompt{
		StaticPrompt:         composeStaticPrompt(sections),
		ResolvedIntent:       intent,
		ResolvedModelProfile: profileName,
		ModelFallback:        modelFallback,
		Behaviors:            extensionIDs(behaviors),
		Talents:              extensionIDs(talents),
		Skills:               extensionIDs(skillSections),
		Warnings:             warnings,
		AgentsMdLoaded:       agentsMdContent != "",
	}, nil
}

type resolvedExtension struct {
	id      string
	content string
}

type promptSection struct {
	Name    string
	Meta    string
	Content string
}

func resolveExtensions(ids []string, catalog map[string]string, field string) ([]resolvedExtension, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	result := make([]resolvedExtension, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		content, ok := catalog[id]
		if !ok {
			return nil, invalid(field, id, "unknown extension id")
		}
		seen[id] = struct{}{}
		result = append(result, resolvedExtension{id: id, content: content})
	}
	return result, nil
}

func extensionIDs(items []resolvedExtension) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.id)
	}
	return ids
}

// readAgentsMd reads the AGENTS.md file from the given workspace root directory.
// It returns the file content on success, empty string if the file does not
// exist (soft fail), or an error if the file cannot be read for other reasons.
// Path traversal is prevented by verifying the candidate path is exactly
// "<absRoot>/AGENTS.md" after cleaning.
func readAgentsMd(workspaceRoot string) (string, error) {
	if workspaceRoot == "" {
		return "", nil
	}
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("invalid workspace path: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	candidate := filepath.Join(absRoot, "AGENTS.md")
	rel, err := filepath.Rel(absRoot, candidate)
	if err != nil || rel != "AGENTS.md" {
		return "", fmt.Errorf("AGENTS.md path escape detected")
	}
	content, err := os.ReadFile(candidate)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func composeStaticPrompt(sections []promptSection) string {
	var b strings.Builder
	for _, section := range sections {
		content := strings.TrimSpace(section.Content)
		if content == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		header := section.Name
		if meta := strings.TrimSpace(section.Meta); meta != "" {
			header = fmt.Sprintf("%s:%s", section.Name, meta)
		}
		b.WriteString("[SECTION ")
		b.WriteString(header)
		b.WriteString("]\n")
		b.WriteString(content)
		b.WriteString("\n[END SECTION]")
	}
	return b.String()
}
