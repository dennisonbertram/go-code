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
				return &SkillActivation{
					Name:    skill.Name,
					Content: expandBody(skill, args, ""),
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
			return &SkillActivation{
				Name:    skill.Name,
				Content: expandBody(skill, msg, ""),
				Context: skill.Context,
				Agent:   skill.Agent,
				Skill:   skill,
			}
		}

		return nil
	}
}

// BuildTemplateVars creates the variable map for prompt-template expansion,
// shared by skill invocation and bundle markdown commands. Positional
// variables are 0-based: $0 is the first argument token (SplitArgs
// tokenization). Named arguments are bound to tokens in declaration order;
// names with no corresponding token expand empty.
func BuildTemplateVars(namedArgs []string, args, workspace, dir string) map[string]string {
	vars := map[string]string{
		"$ARGUMENTS": args,
		"$WORKSPACE": workspace,
		"$SKILL_DIR": dir,
	}
	fields, err := SplitArgs(args)
	if err != nil {
		// SplitArgs never returns an error today (unterminated quotes are
		// not an error); if a future error path appears, degrade to no
		// positional vars rather than mangling tokens.
		fields = nil
	}
	for i, field := range fields {
		vars[fmt.Sprintf("$%d", i)] = field
	}
	// Bind named arguments to tokens in declaration order.
	for i, name := range namedArgs {
		if i < len(fields) {
			vars["$"+name] = fields[i]
		} else {
			vars["$"+name] = ""
		}
	}
	return vars
}

// ExpandTemplate interpolates a prompt-template body with the shared
// expansion contract used by every invocation path (AutoInvokeHook,
// Resolver.ResolveSkill, and bundle markdown commands): when the body
// references no argument placeholder ($ARGUMENTS, $N, or a declared named
// argument) and the raw args are non-empty, the raw args are appended
// verbatim as a trailing "ARGUMENTS: <args>" line instead of being dropped.
func ExpandTemplate(body string, namedArgs []string, args, workspace, dir string) string {
	content := Interpolate(body, BuildTemplateVars(namedArgs, args, workspace, dir))
	if args != "" && !HasArgPlaceholder(body, namedArgs) {
		content += "\nARGUMENTS: " + args
	}
	return content
}

// buildVars creates the variable map for skill body interpolation.
// Positional variables are 0-based: $0 is the first argument token.
func buildVars(skill *Skill, args, workspace string) map[string]string {
	return BuildTemplateVars(skill.Arguments, args, workspace, filepath.Dir(skill.FilePath))
}

// expandBody interpolates the skill body with the given arguments and
// workspace, applying the shared expansion contract used by every invocation
// path (AutoInvokeHook and Resolver.ResolveSkill): when the body references
// no argument placeholder ($ARGUMENTS, $N, or a declared named argument) and
// the raw args are non-empty, the raw args are appended verbatim as a
// trailing "ARGUMENTS: <args>" line instead of being dropped.
func expandBody(skill *Skill, args, workspace string) string {
	return ExpandTemplate(skill.Body, skill.Arguments, args, workspace, filepath.Dir(skill.FilePath))
}
