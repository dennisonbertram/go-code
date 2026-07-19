package profiles

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"go-agent-harness/internal/config"
)

//go:embed builtins/*.toml
var builtinFS embed.FS

// LoadProfile loads a named profile using the three-tier resolution:
//  1. Project-level:  <projectProfilesDir>/<name>.toml
//  2. User-global:    <userProfilesDir>/<name>.toml
//  3. Built-in:       embedded in binary
//
// Pass empty strings for dirs you want to skip.
// Returns ErrProfileNotFound if the profile cannot be resolved from any tier.
func LoadProfile(name string) (*Profile, error) {
	return loadProfileWithDirs(name, defaultProjectProfilesDir(), defaultUserProfilesDir())
}

// LoadProfileFromUserDir loads a profile using an explicit user profiles directory.
// Falls back to built-ins if not found in userDir.
func LoadProfileFromUserDir(name, userDir string) (*Profile, error) {
	return loadProfileWithDirs(name, "", userDir)
}

// LoadProfileWithDirs loads a profile using explicit project and user profile
// directories, then falls back to embedded built-ins.
func LoadProfileWithDirs(name, projectDir, userDir string) (*Profile, error) {
	return loadProfileWithDirs(name, projectDir, userDir)
}

// LoadProfileWithExtraDirs adds trusted external profile directories after
// project and user directories and before built-ins.
func LoadProfileWithExtraDirs(name, projectDir, userDir string, extraDirs []string) (*Profile, error) {
	if err := config.ValidateProfileName(name); err != nil {
		return nil, err
	}
	for _, dir := range append([]string{projectDir, userDir}, extraDirs...) {
		if dir == "" {
			continue
		}
		p, err := loadProfileFile(filepath.Join(dir, name+".toml"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if p != nil {
			return p, nil
		}
	}
	return loadBuiltinProfile(name)
}

// loadProfileWithDirs is the internal implementation that accepts explicit dirs for testing.
func loadProfileWithDirs(name, projectDir, userDir string) (*Profile, error) {
	if err := config.ValidateProfileName(name); err != nil {
		return nil, err
	}

	return loadProfileWithDirsRecursive(name, projectDir, userDir, make(map[string]struct{}), nil)
}

// loadProfileWithDirsRecursive resolves a profile name with recursive inheritance.
// The `resolving` map tracks active profile names in the current resolution
// chain and errors fast on cycles.
func loadProfileWithDirsRecursive(
	name, projectDir, userDir string,
	resolving map[string]struct{},
	stack []string,
) (*Profile, error) {
	if _, ok := resolving[name]; ok {
		cycle := append(append([]string{}, stack...), name)
		return nil, fmt.Errorf("cycle detected in profile inheritance: %s", strings.Join(cycle, " -> "))
	}

	resolving[name] = struct{}{}
	stack = append(stack, name)
	defer delete(resolving, name)

	p, err := loadProfileFromTiers(name, projectDir, userDir)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("profile %q not found", name)
	}

	if p.Extends == "" {
		return p, nil
	}

	if err := config.ValidateProfileName(p.Extends); err != nil {
		return nil, fmt.Errorf("profile %q extends invalid base %q: %w", name, p.Extends, err)
	}

	base, err := loadProfileWithDirsRecursive(p.Extends, projectDir, userDir, resolving, stack)
	if err != nil {
		return nil, fmt.Errorf("profile %q extends missing base profile %q: %w", name, p.Extends, err)
	}

	return mergeProfiles(base, p), nil
}

// loadProfileFromTiers resolves a profile by tier (project > user > built-in)
// without applying inheritance.
func loadProfileFromTiers(name, projectDir, userDir string) (*Profile, error) {
	// Tier 1: project-level.
	if projectDir != "" {
		p, err := loadProfileFile(filepath.Join(projectDir, name+".toml"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("project profile %q: %w", name, err)
		}
		if p != nil {
			return p, nil
		}
	}

	// Tier 2: user-global.
	if userDir != "" {
		p, err := loadProfileFile(filepath.Join(userDir, name+".toml"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("user profile %q: %w", name, err)
		}
		if p != nil {
			return p, nil
		}
	}

	// Tier 3: built-in embedded profiles.
	p, err := loadBuiltinProfile(name)
	if err != nil {
		return nil, err
	}
	if p != nil {
		return p, nil
	}

	return nil, nil
}

// mergeProfiles merges two profiles where `child` is the higher-priority profile
// and `base` provides inherited defaults. Child fields override base fields.
func mergeProfiles(base, child *Profile) *Profile {
	merged := *child
	merged.Extends = ""

	// Merge meta fields: keep child first, fall back to base for unset values.
	merged.Meta = mergeProfileMeta(base.Meta, child.Meta)

	// Merge runner fields deterministically; child overrides non-zero values.
	if child.Runner.Model != "" {
		merged.Runner.Model = child.Runner.Model
	} else {
		merged.Runner.Model = base.Runner.Model
	}
	if child.Runner.MaxSteps != 0 {
		merged.Runner.MaxSteps = child.Runner.MaxSteps
	} else {
		merged.Runner.MaxSteps = base.Runner.MaxSteps
	}
	if child.Runner.MaxCostUSD != 0 {
		merged.Runner.MaxCostUSD = child.Runner.MaxCostUSD
	} else {
		merged.Runner.MaxCostUSD = base.Runner.MaxCostUSD
	}
	if child.Runner.SystemPrompt != "" {
		merged.Runner.SystemPrompt = child.Runner.SystemPrompt
	} else {
		merged.Runner.SystemPrompt = base.Runner.SystemPrompt
	}
	if child.Runner.ReasoningEffort != "" {
		merged.Runner.ReasoningEffort = child.Runner.ReasoningEffort
	} else {
		merged.Runner.ReasoningEffort = base.Runner.ReasoningEffort
	}

	// Tool allowlist is replace-by-child semantics.
	if child.Tools.Allow == nil {
		merged.Tools.Allow = append([]string(nil), base.Tools.Allow...)
	} else {
		merged.Tools.Allow = append([]string(nil), child.Tools.Allow...)
	}

	// Merge permissions with explicit child overrides for non-zero booleans/defined list.
	merged.Permissions = ProfilePermissions{
		AllowBash:       mergeBoolWithPresence(base.Permissions.AllowBash, child.Permissions.AllowBash, child.Permissions.allowBashSet),
		AllowFileWrite:  mergeBoolWithPresence(base.Permissions.AllowFileWrite, child.Permissions.AllowFileWrite, child.Permissions.allowFileWriteSet),
		AllowNetAccess:  mergeBoolWithPresence(base.Permissions.AllowNetAccess, child.Permissions.AllowNetAccess, child.Permissions.allowNetAccessSet),
		AllowedCommands: mergedAllowedCommands(base.Permissions.AllowedCommands, child.Permissions.AllowedCommands),
	}
	merged.Permissions.allowBashSet = base.Permissions.allowBashSet || child.Permissions.allowBashSet
	merged.Permissions.allowFileWriteSet = base.Permissions.allowFileWriteSet || child.Permissions.allowFileWriteSet
	merged.Permissions.allowNetAccessSet = base.Permissions.allowNetAccessSet || child.Permissions.allowNetAccessSet

	// Merge runtime/safety topology fields.
	merged.IsolationMode = mergeStringWithFallback(child.IsolationMode, base.IsolationMode)
	merged.CleanupPolicy = mergeStringWithFallback(child.CleanupPolicy, base.CleanupPolicy)
	merged.BaseRef = mergeStringWithFallback(child.BaseRef, base.BaseRef)
	merged.ResultMode = mergeStringWithFallback(child.ResultMode, base.ResultMode)

	// Merge MCP servers, with child entries replacing inherited entries.
	merged.MCPServers = mergedMCPServers(base.MCPServers, child.MCPServers)

	return &merged
}

func mergeProfileMeta(base, child ProfileMeta) ProfileMeta {
	if child.Name == "" {
		child.Name = base.Name
	}
	if child.Description == "" {
		child.Description = base.Description
	}
	if child.CreatedAt == "" {
		child.CreatedAt = base.CreatedAt
	}
	if child.CreatedBy == "" {
		child.CreatedBy = base.CreatedBy
	}
	if child.Version == 0 {
		child.Version = base.Version
	}
	if child.EfficiencyScore == 0 {
		child.EfficiencyScore = base.EfficiencyScore
	}
	if child.ReviewCount == 0 {
		child.ReviewCount = base.ReviewCount
	}
	if !child.reviewEligibleSet {
		child.ReviewEligible = base.ReviewEligible
		child.reviewEligibleSet = base.reviewEligibleSet
	}
	return child
}

func mergeBoolWithPresence(baseValue, childValue, childSet bool) bool {
	if childSet {
		return childValue
	}
	return baseValue
}

func mergedAllowedCommands(base, child []string) []string {
	if child != nil {
		return append([]string(nil), child...)
	}
	return append([]string(nil), base...)
}

func mergeStringWithFallback(childValue, baseValue string) string {
	if childValue != "" {
		return childValue
	}
	return baseValue
}

func mergedMCPServers(base, child map[string]config.MCPServerConfig) map[string]config.MCPServerConfig {
	if len(base) == 0 && len(child) == 0 {
		return nil
	}
	merged := make(map[string]config.MCPServerConfig, len(base)+len(child))
	for name, cfg := range base {
		merged[name] = cfg
	}
	for name, cfg := range child {
		merged[name] = cfg
	}
	return merged
}

// ListProfiles returns the names of all available profiles across all three tiers.
// Duplicates (same name in multiple tiers) are deduplicated; project-level wins.
func ListProfiles() ([]string, error) {
	return listProfilesWithDirs(defaultProjectProfilesDir(), defaultUserProfilesDir())
}

// ProfileSummary holds read-only metadata about a profile for discovery APIs.
type ProfileSummary struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Model            string   `json:"model"`
	AllowedToolCount int      `json:"allowed_tool_count"`
	AllowedTools     []string `json:"allowed_tools,omitempty"`
	SourceTier       string   `json:"source_tier"` // "project" | "user" | "built-in"
}

// ListProfileSummaries returns rich metadata for all available profiles across all tiers.
// Resolution priority: project > user > built-in. Duplicate names return only the
// highest-priority entry.
func ListProfileSummaries() ([]ProfileSummary, error) {
	return listProfileSummariesWithDirs(defaultProjectProfilesDir(), defaultUserProfilesDir())
}

// ListProfileSummariesFromDirs lists profile summaries using explicit dirs.
// Falls back to built-ins for profiles not found in the given dirs.
func ListProfileSummariesFromDirs(projectDir, userDir string) ([]ProfileSummary, error) {
	return listProfileSummariesWithDirs(projectDir, userDir)
}

// listProfileSummariesWithDirs is the internal implementation for testing.
func listProfileSummariesWithDirs(projectDir, userDir string) ([]ProfileSummary, error) {
	seen := make(map[string]bool)
	var summaries []ProfileSummary

	addEntry := func(name, sourceTier string, loadFn func() (*Profile, error)) error {
		if seen[name] {
			return nil
		}
		p, err := loadFn()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		seen[name] = true
		s := ProfileSummary{
			Name:             name,
			Description:      p.Meta.Description,
			Model:            p.Runner.Model,
			AllowedToolCount: len(p.Tools.Allow),
			AllowedTools:     append([]string(nil), p.Tools.Allow...),
			SourceTier:       sourceTier,
		}
		summaries = append(summaries, s)
		return nil
	}

	// Tier 1: project-level.
	if projectDir != "" {
		entries, err := os.ReadDir(projectDir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".toml")
			if err := addEntry(name, "project", func() (*Profile, error) { return loadProfileWithDirs(name, projectDir, userDir) }); err != nil {
				return nil, err
			}
		}
	}

	// Tier 2: user-global.
	if userDir != "" {
		entries, err := os.ReadDir(userDir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".toml")
			if err := addEntry(name, "user", func() (*Profile, error) { return loadProfileWithDirs(name, projectDir, userDir) }); err != nil {
				return nil, err
			}
		}
	}

	// Tier 3: built-ins.
	builtinNames, err := listBuiltinNames()
	if err != nil {
		return nil, err
	}
	for _, name := range builtinNames {
		n := name // capture
		if err := addEntry(n, "built-in", func() (*Profile, error) { return loadBuiltinProfile(n) }); err != nil {
			return nil, err
		}
	}

	return summaries, nil
}

// listProfilesWithDirs is the internal implementation for testing.
func listProfilesWithDirs(projectDir, userDir string) ([]string, error) {
	seen := make(map[string]bool)
	var names []string

	addDir := func(dir string) error {
		if dir == "" {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			n := strings.TrimSuffix(e.Name(), ".toml")
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		return nil
	}

	if err := addDir(projectDir); err != nil {
		return nil, err
	}
	if err := addDir(userDir); err != nil {
		return nil, err
	}

	// Add built-ins not already seen.
	builtins, err := listBuiltinNames()
	if err != nil {
		return nil, err
	}
	for _, n := range builtins {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}

	return names, nil
}

// SaveProfile writes a profile to the user-global profiles directory.
// Creates the directory if it does not exist.
func SaveProfile(p *Profile) error {
	dir := defaultUserProfilesDir()
	if dir == "" {
		return fmt.Errorf("cannot determine user home directory")
	}
	return saveProfileToDir(p, dir)
}

// saveProfileToDir writes a profile TOML to the given directory.
// Creates the directory if it does not exist.
func saveProfileToDir(p *Profile, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	if err := config.ValidateProfileName(p.Meta.Name); err != nil {
		return err
	}
	path := filepath.Join(dir, p.Meta.Name+".toml")
	// Write atomically: write to temp file, then rename.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp profile file: %w", err)
	}
	if err := toml.NewEncoder(f).Encode(p); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encode profile: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// loadProfileFile reads and parses a single TOML profile file.
// Returns (nil, nil) when the file does not exist.
func loadProfileFile(path string) (*Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var p Profile
	if _, err := toml.NewDecoder(f).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UnmarshalTOML tracks whether booleans were explicitly present so inheritance
// can distinguish "unset" from an intentional false override.
func (m *ProfileMeta) UnmarshalTOML(data any) error {
	type rawProfileMeta ProfileMeta
	var raw rawProfileMeta
	if _, err := toml.Decode(tomlValueToString(data), &raw); err != nil {
		return err
	}
	*m = ProfileMeta(raw)
	if fields, ok := data.(map[string]any); ok {
		_, m.reviewEligibleSet = fields["review_eligible"]
	}
	return nil
}

// UnmarshalTOML tracks boolean presence for permission inheritance semantics.
func (p *ProfilePermissions) UnmarshalTOML(data any) error {
	type rawProfilePermissions ProfilePermissions
	var raw rawProfilePermissions
	if _, err := toml.Decode(tomlValueToString(data), &raw); err != nil {
		return err
	}
	*p = ProfilePermissions(raw)
	if fields, ok := data.(map[string]any); ok {
		_, p.allowBashSet = fields["allow_bash"]
		_, p.allowFileWriteSet = fields["allow_file_write"]
		_, p.allowNetAccessSet = fields["allow_net_access"]
	}
	return nil
}

func tomlValueToString(data any) string {
	switch fields := data.(type) {
	case map[string]any:
		var b strings.Builder
		for key, value := range fields {
			switch typed := value.(type) {
			case string:
				fmt.Fprintf(&b, "%s = %q\n", key, typed)
			case bool:
				fmt.Fprintf(&b, "%s = %t\n", key, typed)
			case int64:
				fmt.Fprintf(&b, "%s = %d\n", key, typed)
			case float64:
				fmt.Fprintf(&b, "%s = %v\n", key, typed)
			case []any:
				var rendered []string
				for _, item := range typed {
					rendered = append(rendered, fmt.Sprintf("%q", item))
				}
				fmt.Fprintf(&b, "%s = [%s]\n", key, strings.Join(rendered, ", "))
			}
		}
		return b.String()
	default:
		return ""
	}
}

// loadBuiltinProfile loads a named built-in profile from the embedded FS.
func loadBuiltinProfile(name string) (*Profile, error) {
	data, err := builtinFS.ReadFile("builtins/" + name + ".toml")
	if err != nil {
		if isNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read built-in profile %q: %w", name, err)
	}
	var p Profile
	if _, err := toml.Decode(string(data), &p); err != nil {
		return nil, fmt.Errorf("parse built-in profile %q: %w", name, err)
	}
	return &p, nil
}

// listBuiltinNames returns the names of all embedded built-in profiles.
func listBuiltinNames() ([]string, error) {
	entries, err := fs.ReadDir(builtinFS, "builtins")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
		}
	}
	return names, nil
}

// isNotExist checks if an embed.FS error is "file not found".
func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "file does not exist") ||
		strings.Contains(err.Error(), "open builtins/") ||
		os.IsNotExist(err)
}

// defaultUserProfilesDir returns ~/.harness/profiles/.
func defaultUserProfilesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".harness", "profiles")
}

// defaultProjectProfilesDir returns .harness/profiles/ relative to cwd.
func defaultProjectProfilesDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Join(cwd, ".harness", "profiles")
}
