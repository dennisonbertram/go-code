package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// PermissionEffect is the result of a matching fine-grained permission rule.
type PermissionEffect string

const (
	PermissionEffectAllow PermissionEffect = "allow"
	PermissionEffectAsk   PermissionEffect = "ask"
	PermissionEffectDeny  PermissionEffect = "deny"
)

// PermissionRule matches either every invocation of a tool ("bash") or the
// tool's primary argument ("bash(git diff:*)" and "read(./src/**)").
type PermissionRule struct {
	Pattern string           `json:"pattern"`
	Effect  PermissionEffect `json:"effect"`
}

// PermissionRuleSet stores a permission rule slice behind a pointer so that
// PermissionConfig remains comparable for legacy callers. It encodes on the
// wire as a JSON array.
type PermissionRuleSet struct {
	Items []PermissionRule `json:"-"`
}

// NewPermissionRuleSet returns an owned copy of rules.
func NewPermissionRuleSet(rules []PermissionRule) *PermissionRuleSet {
	if rules == nil {
		return nil
	}
	return &PermissionRuleSet{Items: copyPermissionRules(rules)}
}

func (s PermissionRuleSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Items)
}

func (s *PermissionRuleSet) UnmarshalJSON(data []byte) error {
	var rules []PermissionRule
	if err := json.Unmarshal(data, &rules); err != nil {
		return err
	}
	s.Items = rules
	return nil
}

func permissionRulesFromSet(set *PermissionRuleSet) []PermissionRule {
	if set == nil {
		return nil
	}
	return set.Items
}

// ParsePermissionRule parses a tool rule pattern and validates its effect.
// The accepted pattern grammar is Tool or Tool(argpattern), with a non-empty
// tool name and argument pattern. Tool names are matched case-insensitively.
func ParsePermissionRule(pattern string, effect PermissionEffect) (PermissionRule, error) {
	rule := PermissionRule{Pattern: strings.TrimSpace(pattern), Effect: effect}
	if rule.Pattern == "" {
		return PermissionRule{}, fmt.Errorf("permission rule pattern is required")
	}
	if !validPermissionEffect(effect) {
		return PermissionRule{}, fmt.Errorf("invalid permission rule effect %q", effect)
	}
	parsed, err := parsePermissionRule(rule)
	if err != nil {
		return PermissionRule{}, err
	}
	if parsed.argPattern != "" && isPathPermissionTool(parsed.tool) {
		if err := validatePathRulePattern(parsed.argPattern); err != nil {
			return PermissionRule{}, err
		}
	}
	return rule, nil
}

// ValidatePermissionRules validates every rule in rules without evaluating it.
func ValidatePermissionRules(rules []PermissionRule) error {
	for i, rule := range rules {
		if _, err := ParsePermissionRule(rule.Pattern, rule.Effect); err != nil {
			return fmt.Errorf("permission rule %d: %w", i, err)
		}
	}
	return nil
}

// EvaluatePermissionRules evaluates a tool call against rules. If no rule
// matches, PermissionEffectAllow is returned. The precedence contract is:
// the most-specific matching rule wins; specificity is determined by the
// matched argument pattern's literal content, with a bare tool rule less
// specific than every argument rule. When matching rules have equal
// specificity, deny beats ask and allow, and ask beats allow. A more-specific
// allow or ask may therefore override a broader deny. This is a policy and
// ergonomics layer, not a security boundary: command matching normalizes
// whitespace, shell quotes, and the leading executable path, but it does not
// parse shell syntax or detect commands embedded after operators, expansions,
// aliases, or interpreters. Path matching canonicalizes workspace paths,
// rejects lexical .. traversal, and rejects paths whose symlinks resolve
// outside the workspace. OS-level sandboxing remains the security boundary.
func EvaluatePermissionRules(rules []PermissionRule, toolName string, args json.RawMessage, workspaceRoot string) (PermissionEffect, error) {
	if len(rules) == 0 {
		return PermissionEffectAllow, nil
	}

	tool := strings.ToLower(strings.TrimSpace(toolName))
	if tool == "" {
		return PermissionEffectAllow, nil
	}
	best := parsedPermissionRule{specificity: -1}
	for i, rule := range rules {
		parsed, err := parsePermissionRule(rule)
		if err != nil {
			return PermissionEffectAllow, fmt.Errorf("permission rule %d: %w", i, err)
		}
		if parsed.tool != tool {
			continue
		}
		matched, err := parsed.matches(args, workspaceRoot)
		if err != nil {
			return PermissionEffectAllow, err
		}
		if !matched {
			continue
		}
		if permissionRuleWins(parsed, best) {
			best = parsed
		}
	}
	if best.specificity < 0 {
		return PermissionEffectAllow, nil
	}
	return best.effect, nil
}

func (r *Runner) permissionRuleDecision(runID, toolName string, args json.RawMessage) (PermissionEffect, error) {
	r.mu.RLock()
	state, ok := r.runs[runID]
	if !ok {
		r.mu.RUnlock()
		return PermissionEffectAllow, nil
	}
	rules := copyPermissionRules(permissionRulesFromSet(state.permissions.Rules))
	workspaceRoot := state.permissionWorkspaceRoot
	r.mu.RUnlock()
	return EvaluatePermissionRules(rules, toolName, args, workspaceRoot)
}

func (r *Runner) defaultPermissionWorkspaceRoot() string {
	return r.snapshotConfig().WorkspaceBaseOptions.RepoPath
}

type parsedPermissionRule struct {
	tool        string
	argPattern  string
	hasArg      bool
	effect      PermissionEffect
	specificity int
}

func parsePermissionRule(rule PermissionRule) (parsedPermissionRule, error) {
	pattern := strings.TrimSpace(rule.Pattern)
	if pattern == "" {
		return parsedPermissionRule{}, fmt.Errorf("permission rule pattern is required")
	}
	if !validPermissionEffect(rule.Effect) {
		return parsedPermissionRule{}, fmt.Errorf("invalid permission rule effect %q", rule.Effect)
	}

	tool := pattern
	argPattern := ""
	hasArg := false
	if open := strings.IndexByte(pattern, '('); open >= 0 {
		if !strings.HasSuffix(pattern, ")") || open == 0 {
			return parsedPermissionRule{}, fmt.Errorf("invalid permission rule pattern %q", rule.Pattern)
		}
		tool = strings.TrimSpace(pattern[:open])
		argPattern = strings.TrimSpace(pattern[open+1 : len(pattern)-1])
		hasArg = true
		if argPattern == "" {
			return parsedPermissionRule{}, fmt.Errorf("permission rule %q has an empty argument pattern", rule.Pattern)
		}
		if strings.Contains(argPattern, "(") || strings.Contains(argPattern, ")") {
			return parsedPermissionRule{}, fmt.Errorf("invalid permission rule pattern %q", rule.Pattern)
		}
		if isPathPermissionTool(strings.ToLower(strings.TrimSpace(tool))) {
			if err := validatePathRulePattern(argPattern); err != nil {
				return parsedPermissionRule{}, err
			}
		}
	}
	if tool == "" {
		return parsedPermissionRule{}, fmt.Errorf("permission rule %q has an empty tool name", rule.Pattern)
	}
	for _, r := range tool {
		if unicode.IsSpace(r) {
			return parsedPermissionRule{}, fmt.Errorf("permission rule %q has an invalid tool name", rule.Pattern)
		}
	}
	tool = strings.ToLower(tool)

	specificity := 0
	if hasArg {
		specificity = 1 + permissionPatternLiteralCount(argPattern)
		if !permissionPatternHasGlob(argPattern) {
			specificity += 1_000_000
		}
	}
	return parsedPermissionRule{
		tool:        tool,
		argPattern:  argPattern,
		hasArg:      hasArg,
		effect:      rule.Effect,
		specificity: specificity,
	}, nil
}

func (r parsedPermissionRule) matches(args json.RawMessage, workspaceRoot string) (bool, error) {
	if !r.hasArg {
		return true, nil
	}
	value, ok := primaryPermissionArgument(r.tool, args)
	if !ok {
		return false, nil
	}
	if r.tool == "bash" {
		command, ok := normalizeShellCommand(value)
		if !ok {
			return false, nil
		}
		pattern := normalizeShellPattern(r.argPattern)
		return permissionGlobMatch(command, pattern, false), nil
	}
	if isPathPermissionTool(r.tool) {
		candidate, ok := canonicalWorkspacePath(workspaceRoot, value)
		if !ok {
			return false, nil
		}
		pattern := normalizePathPattern(r.argPattern)
		return permissionGlobMatch(candidate, pattern, true), nil
	}
	return permissionGlobMatch(strings.TrimSpace(value), strings.TrimSpace(r.argPattern), false), nil
}

func permissionRuleWins(candidate, current parsedPermissionRule) bool {
	if candidate.specificity != current.specificity {
		return candidate.specificity > current.specificity
	}
	return permissionEffectPriority(candidate.effect) > permissionEffectPriority(current.effect)
}

func permissionEffectPriority(effect PermissionEffect) int {
	switch effect {
	case PermissionEffectDeny:
		return 3
	case PermissionEffectAsk:
		return 2
	case PermissionEffectAllow:
		return 1
	default:
		return 0
	}
}

func validPermissionEffect(effect PermissionEffect) bool {
	return effect == PermissionEffectAllow || effect == PermissionEffectAsk || effect == PermissionEffectDeny
}

func copyPermissionRules(rules []PermissionRule) []PermissionRule {
	if rules == nil {
		return nil
	}
	return append([]PermissionRule(nil), rules...)
}

func copyPermissionRuleSet(set *PermissionRuleSet) *PermissionRuleSet {
	if set == nil {
		return nil
	}
	return NewPermissionRuleSet(set.Items)
}

func permissionConfigsEqual(a, b PermissionConfig) bool {
	aRules := permissionRulesFromSet(a.Rules)
	bRules := permissionRulesFromSet(b.Rules)
	if a.Sandbox != b.Sandbox || a.Approval != b.Approval || len(aRules) != len(bRules) {
		return false
	}
	for i := range aRules {
		if aRules[i] != bRules[i] {
			return false
		}
	}
	return true
}

func permissionPatternHasGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func permissionPatternLiteralCount(pattern string) int {
	count := 0
	for _, r := range pattern {
		if r != '*' && r != '?' && r != '[' && r != ']' {
			count++
		}
	}
	return count
}

func primaryPermissionArgument(tool string, args json.RawMessage) (string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", false
	}
	keys := []string{"command", "path", "file_path"}
	if tool != "bash" {
		keys = []string{"path", "file_path", "command", "query", "url"}
	}
	for _, key := range keys {
		var value string
		if err := json.Unmarshal(fields[key], &value); err == nil && value != "" {
			return value, true
		}
	}
	return "", false
}

func normalizeShellPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if strings.Contains(pattern, ":*") {
		pattern = strings.ReplaceAll(pattern, ":*", " *")
	}
	if normalized, ok := normalizeShellCommand(pattern); ok {
		return normalized
	}
	return strings.Join(strings.Fields(pattern), " ")
}

func normalizeShellCommand(command string) (string, bool) {
	words, ok := shellWords(command)
	if !ok || len(words) == 0 {
		return "", false
	}
	if strings.Contains(words[0], "/") {
		words[0] = filepath.Base(words[0])
	}
	return strings.Join(words, " "), true
}

func shellWords(input string) ([]string, bool) {
	var words []string
	var word strings.Builder
	quote := byte(0)
	inWord := false
	escaped := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if escaped {
			word.WriteByte(c)
			inWord = true
			escaped = false
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' {
				escaped = true
			} else {
				word.WriteByte(c)
			}
			inWord = true
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			inWord = true
		case '\\':
			escaped = true
			inWord = true
		case ' ', '\t', '\r', '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 || escaped {
		return nil, false
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, true
}

func isPathPermissionTool(tool string) bool {
	switch tool {
	case "read", "write", "edit", "apply_patch":
		return true
	default:
		return false
	}
}

func validatePathRulePattern(pattern string) error {
	if filepath.IsAbs(pattern) {
		return fmt.Errorf("absolute path patterns are not allowed: %q", pattern)
	}
	for _, component := range strings.Split(filepath.ToSlash(pattern), "/") {
		if component == ".." {
			return fmt.Errorf("path rule pattern %q escapes the workspace", pattern)
		}
	}
	return nil
}

func normalizePathPattern(pattern string) string {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	for strings.HasPrefix(pattern, "./") {
		pattern = strings.TrimPrefix(pattern, "./")
	}
	return pattern
}

func canonicalWorkspacePath(workspaceRoot, input string) (string, bool) {
	if input == "" || hasDotDotComponent(input) {
		return "", false
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", false
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootReal = filepath.Clean(root)
	}
	candidate := input
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if !pathWithin(root, candidate) {
		return "", false
	}
	realCandidate, err := evalExistingParent(candidate)
	if err != nil || !pathWithin(rootReal, realCandidate) {
		return "", false
	}
	relative, err := filepath.Rel(rootReal, realCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	if relative == "." {
		return ".", true
	}
	return filepath.ToSlash(relative), true
}

func hasDotDotComponent(input string) bool {
	for _, component := range strings.Split(filepath.ToSlash(input), "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func evalExistingParent(candidate string) (string, error) {
	current := candidate
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func permissionGlobMatch(value, pattern string, pathPattern bool) bool {
	expression := permissionGlobRegexp(pattern, pathPattern)
	if expression == "" {
		return false
	}
	matched, err := regexp.MatchString(expression, value)
	return err == nil && matched
}

func permissionGlobRegexp(pattern string, pathPattern bool) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if pathPattern && i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else if pathPattern {
				b.WriteString("[^/]*")
			} else {
				b.WriteString(".*")
			}
		case '?':
			if pathPattern {
				b.WriteString("[^/]")
			} else {
				b.WriteByte('.')
			}
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return ""
			}
			end += i + 1
			class := pattern[i+1 : end]
			if strings.ContainsAny(class, "\\(){}+*?.|") {
				b.WriteString(regexp.QuoteMeta("[" + class + "]"))
			} else {
				b.WriteByte('[')
				b.WriteString(class)
				b.WriteByte(']')
			}
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	return b.String()
}
