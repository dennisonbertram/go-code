package skills

import (
	"regexp"
	"strings"
)

// positionalVarPattern matches positional placeholders $0..$n. The digit run
// is matched maximally, so $10 is a single placeholder and never collides
// with $1.
var positionalVarPattern = regexp.MustCompile(`\$\d+`)

// namedVarPattern matches named placeholders like $target. The identifier run
// is matched maximally, so $targets is a single placeholder and is never
// clobbered by a declared $target.
var namedVarPattern = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)

// Interpolate replaces variables in a skill body with provided values.
//
// Supported variables:
//   - $ARGUMENTS - full argument string
//   - $0 through $n - positional arguments (0-based, split with SplitArgs)
//   - $<name> - named arguments declared in the frontmatter arguments field
//   - $WORKSPACE - workspace path
//   - $SKILL_DIR - directory containing the SKILL.md
//
// Undefined variables are replaced with empty string, except dollar-prefixed
// identifiers that are not known variables (e.g. a literal shell $HOME in the
// body): those are left untouched.
func Interpolate(body string, vars map[string]string) string {
	result := body

	// Replace named variables first (longer names before shorter to avoid partial matches)
	namedVars := []string{"$ARGUMENTS", "$WORKSPACE", "$SKILL_DIR"}
	for _, v := range namedVars {
		val := vars[v]
		result = strings.ReplaceAll(result, v, val)
	}

	// Replace declared named arguments ($<name>). Each match is the full
	// identifier run; identifiers absent from vars are not known variables
	// and stay literal so shell snippets in bodies keep working.
	result = namedVarPattern.ReplaceAllStringFunc(result, func(match string) string {
		if val, ok := vars[match]; ok {
			return val
		}
		return match
	})

	// Replace positional variables $0..$n. Each match is the full digit run,
	// so $10 expands from vars["$10"] rather than being clobbered by $1.
	// Unset positions are absent from vars and expand to empty string.
	result = positionalVarPattern.ReplaceAllStringFunc(result, func(match string) string {
		return vars[match]
	})

	return result
}

// hasArgPlaceholder reports whether a skill body references any argument
// placeholder: $ARGUMENTS, a positional $N, or a named argument declared in
// the skill's frontmatter arguments field.
func hasArgPlaceholder(skill *Skill) bool {
	body := skill.Body
	if strings.Contains(body, "$ARGUMENTS") || positionalVarPattern.MatchString(body) {
		return true
	}
	if len(skill.Arguments) == 0 {
		return false
	}
	declared := make(map[string]bool, len(skill.Arguments))
	for _, name := range skill.Arguments {
		declared["$"+name] = true
	}
	for _, ref := range namedVarPattern.FindAllString(body, -1) {
		if declared[ref] {
			return true
		}
	}
	return false
}
