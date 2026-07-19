package skills

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SkillActivation represents the result of skill matching.
type SkillActivation struct {
	Name    string       // matched skill name
	Content string       // interpolated body
	Context SkillContext // "conversation" or "fork"
	Agent   string       // agent type hint
	Skill   *Skill       // reference to full skill for metadata access
}

// AutoInvokeHook returns a function that detects skill invocations in user messages.
// It handles two patterns:
//  1. Explicit: "/skill-name args" as the user message
//  2. Auto-invoke: trigger phrase matching (only for skills with AutoInvoke=true)
//
// The returned function takes a user message string and returns a *SkillActivation
// if a skill matches, or nil if no skill matches.
//
// For explicit invocation, the skill name is extracted from the slash prefix and looked
// up in the registry. For auto-invocation, exactly one AutoInvoke-enabled skill must
// match to avoid ambiguity; multiple matches return nil.
func AutoInvokeHook(registry *Registry) func(lastUserMessage string) *SkillActivation {
	return func(lastUserMessage string) *SkillActivation {
		msg := strings.TrimSpace(lastUserMessage)
		if msg == "" {
			return nil
		}

		// 1. Explicit invocation: /skill-name [args]
		if strings.HasPrefix(msg, "/") {
			parts := strings.SplitN(msg[1:], " ", 2)
			name := strings.TrimSpace(parts[0])
			args := ""
			if len(parts) > 1 {
				args = strings.TrimSpace(parts[1])
			}
			skill, ok := registry.Get(name)
			if ok {
				vars := buildVars(skill, args, "")
				content := Interpolate(skill.Body, vars)
				return &SkillActivation{
					Name:    skill.Name,
					Content: content,
					Context: skill.Context,
					Agent:   skill.Agent,
					Skill:   skill,
				}
			}
			// Not a known skill -- fall through to trigger matching
		}

		// 2. Auto-invocation via trigger matching
		matched := registry.MatchTriggers(msg)
		// Only auto-invoke skills with AutoInvoke=true
		var autoInvokeMatches []*Skill
		for _, s := range matched {
			if s.AutoInvoke {
				autoInvokeMatches = append(autoInvokeMatches, s)
			}
		}
		// Exactly one match required to avoid ambiguity
		if len(autoInvokeMatches) == 1 {
			skill := autoInvokeMatches[0]
			vars := buildVars(skill, msg, "")
			content := Interpolate(skill.Body, vars)
			return &SkillActivation{
				Name:    skill.Name,
				Content: content,
				Context: skill.Context,
				Agent:   skill.Agent,
				Skill:   skill,
			}
		}

		return nil
	}
}

// buildVars creates the variable map for skill body interpolation.
func buildVars(skill *Skill, args, workspace string) map[string]string {
	vars := map[string]string{
		"$ARGUMENTS": args,
		"$WORKSPACE": workspace,
		"$SKILL_DIR": filepath.Dir(skill.FilePath),
	}
	fields, err := SplitArgs(args)
	if err != nil {
		// SplitArgs never returns an error today (unterminated quotes are
		// not an error); if a future error path appears, degrade to no
		// positional vars rather than mangling tokens.
		fields = nil
	}
	for i := 1; i <= 9; i++ {
		key := fmt.Sprintf("$%d", i)
		if i-1 < len(fields) {
			vars[key] = fields[i-1]
		}
	}
	return vars
}
