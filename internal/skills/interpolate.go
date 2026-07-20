package skills

import (
	"regexp"
	"strings"
)

// positionalVarPattern matches positional placeholders $0..$n. The digit run
// is matched maximally, so $10 is a single placeholder and never collides
// with $1.
var positionalVarPattern = regexp.MustCompile(`\$\d+`)

// Interpolate replaces variables in a skill body with provided values.
//
// Supported variables:
//   - $ARGUMENTS - full argument string
//   - $0 through $n - positional arguments (0-based, split with SplitArgs)
//   - $WORKSPACE - workspace path
//   - $SKILL_DIR - directory containing the SKILL.md
//
// Undefined variables are replaced with empty string.
func Interpolate(body string, vars map[string]string) string {
	result := body

	// Replace named variables first (longer names before shorter to avoid partial matches)
	namedVars := []string{"$ARGUMENTS", "$WORKSPACE", "$SKILL_DIR"}
	for _, v := range namedVars {
		val := vars[v]
		result = strings.ReplaceAll(result, v, val)
	}

	// Replace positional variables $0..$n. Each match is the full digit run,
	// so $10 expands from vars["$10"] rather than being clobbered by $1.
	// Unset positions are absent from vars and expand to empty string.
	result = positionalVarPattern.ReplaceAllStringFunc(result, func(match string) string {
		return vars[match]
	})

	return result
}

// hasArgPlaceholder reports whether a skill body references any argument
// placeholder: $ARGUMENTS or a positional $N. Slice 3 extends this to also
// recognize named arguments declared in frontmatter.
func hasArgPlaceholder(body string) bool {
	if strings.Contains(body, "$ARGUMENTS") {
		return true
	}
	return positionalVarPattern.MatchString(body)
}
