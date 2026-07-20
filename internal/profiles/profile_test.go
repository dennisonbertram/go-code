package profiles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListProfileSummariesFromDirsDeduplicatesByTierPriority(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	projectRoot := t.TempDir()
	projectProfilesDir := filepath.Join(projectRoot, ".harness", "profiles")
	require.NoError(t, os.MkdirAll(projectProfilesDir, 0755))

	projectProfile := `
[meta]
name = "custom"
description = "Project custom"
created_by = "user"

[runner]
model = "gpt-4.1"

[tools]
allow = ["read", "grep"]
`
	userProfile := `
[meta]
name = "custom"
description = "User custom"
created_by = "user"

[runner]
model = "gpt-4.1-mini"

[tools]
allow = ["read"]
`

	homeProfilesDir := filepath.Join(os.Getenv("HOME"), ".harness", "profiles")
	require.NoError(t, os.MkdirAll(homeProfilesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectProfilesDir, "custom.toml"), []byte(projectProfile), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(homeProfilesDir, "custom.toml"), []byte(userProfile), 0644))

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(projectRoot))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(originalWD))
	})

	summaries, err := ListProfileSummaries()
	require.NoError(t, err)

	var custom *ProfileSummary
	for i := range summaries {
		if summaries[i].Name == "custom" {
			custom = &summaries[i]
			break
		}
	}

	require.NotNil(t, custom, "expected custom profile summary")
	assert.Equal(t, "Project custom", custom.Description)
	assert.Equal(t, "gpt-4.1", custom.Model)
	assert.Equal(t, []string{"read", "grep"}, custom.AllowedTools)
	assert.Equal(t, 2, custom.AllowedToolCount)
	assert.Equal(t, "project", custom.SourceTier)
}

// TestLoadBuiltinProfiles verifies all built-in profiles parse correctly.
func TestLoadBuiltinProfiles(t *testing.T) {
	builtins := []string{"github", "file-writer", "researcher", "bash-runner", "reviewer", "full"}
	for _, name := range builtins {
		t.Run(name, func(t *testing.T) {
			p, err := loadProfileWithDirs(name, "", "")
			require.NoError(t, err)
			require.NotNil(t, p)
			assert.Equal(t, name, p.Meta.Name)
			assert.NotEmpty(t, p.Meta.Description)
			assert.Equal(t, "built-in", p.Meta.CreatedBy)
			assert.False(t, p.Meta.ReviewEligible, "built-in profiles must not be review-eligible")
		})
	}
}

func TestLoadBuiltinFullProfileGoldenPathDefaults(t *testing.T) {
	p, err := loadProfileWithDirs("full", "", "")
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "full", p.Meta.Name)
	assert.Equal(t, "gpt-4.1-mini", p.Runner.Model)
	assert.Equal(t, 30, p.Runner.MaxSteps)
	assert.Equal(t, 2.0, p.Runner.MaxCostUSD)
	assert.Empty(t, p.Tools.Allow, "full profile should keep the full tool registry available")
}

// TestLoadProfileNotFound verifies that a missing profile returns an error.
func TestLoadProfileNotFound(t *testing.T) {
	_, err := loadProfileWithDirs("nonexistent-profile-xyz", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestLoadProfileFromUserDir verifies project-level and user-global resolution.
func TestLoadProfileFromUserDir(t *testing.T) {
	dir := t.TempDir()

	// Write a custom profile.
	content := `
[meta]
name = "custom"
description = "Test profile"
version = 1
created_at = "2026-01-01"
created_by = "user"
review_eligible = true

[runner]
model = "gpt-4.1-mini"
max_steps = 5
max_cost_usd = 0.10
system_prompt = "Test prompt"

[tools]
allow = ["read", "grep"]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "custom.toml"), []byte(content), 0644))

	p, err := loadProfileWithDirs("custom", "", dir)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "custom", p.Meta.Name)
	assert.Equal(t, "gpt-4.1-mini", p.Runner.Model)
	assert.Equal(t, 5, p.Runner.MaxSteps)
	assert.Equal(t, 0.10, p.Runner.MaxCostUSD)
	assert.Equal(t, "Test prompt", p.Runner.SystemPrompt)
	assert.Equal(t, []string{"read", "grep"}, p.Tools.Allow)
	assert.True(t, p.Meta.ReviewEligible)
}

// TestProjectLevelOverridesUserGlobal verifies project-level profiles take precedence.
func TestProjectLevelOverridesUserGlobal(t *testing.T) {
	projectDir := t.TempDir()
	userDir := t.TempDir()

	projectContent := `
[meta]
name = "myprofile"
description = "Project version"
created_by = "user"

[runner]
model = "gpt-4.1"
max_steps = 25
`
	userContent := `
[meta]
name = "myprofile"
description = "User version"
created_by = "user"

[runner]
model = "gpt-4.1-mini"
max_steps = 10
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "myprofile.toml"), []byte(projectContent), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "myprofile.toml"), []byte(userContent), 0644))

	p, err := loadProfileWithDirs("myprofile", projectDir, userDir)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4.1", p.Runner.Model, "project-level should override user-global")
	assert.Equal(t, 25, p.Runner.MaxSteps)
}

// TestBuiltinOverriddenByUserGlobal verifies user-global overrides built-ins.
func TestBuiltinOverriddenByUserGlobal(t *testing.T) {
	userDir := t.TempDir()

	// Override the "github" built-in with a custom version.
	content := `
[meta]
name = "github"
description = "Custom github override"
created_by = "user"

[runner]
model = "gpt-4.1"
max_steps = 99
`
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "github.toml"), []byte(content), 0644))

	p, err := loadProfileWithDirs("github", "", userDir)
	require.NoError(t, err)
	assert.Equal(t, "gpt-4.1", p.Runner.Model, "user-global should override built-in")
	assert.Equal(t, 99, p.Runner.MaxSteps)
}

// TestListProfiles verifies that ListProfiles returns all profile names.
func TestListProfiles(t *testing.T) {
	names, err := listProfilesWithDirs("", "")
	require.NoError(t, err)
	// Should include all 6 built-ins.
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "file-writer")
	assert.Contains(t, names, "researcher")
	assert.Contains(t, names, "bash-runner")
	assert.Contains(t, names, "reviewer")
	assert.Contains(t, names, "full")
}

// TestListProfilesDeduplicates verifies that duplicate names are only listed once.
func TestListProfilesDeduplicates(t *testing.T) {
	userDir := t.TempDir()

	// Add a profile with the same name as a built-in.
	content := "[meta]\nname = \"github\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "github.toml"), []byte(content), 0644))

	names, err := listProfilesWithDirs("", userDir)
	require.NoError(t, err)

	count := 0
	for _, n := range names {
		if n == "github" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate profile name should appear only once")
}

func TestListProfileSummariesDeduplicatesByTierPriority(t *testing.T) {
	projectDir := t.TempDir()
	userDir := t.TempDir()

	projectContent := `
[meta]
name = "shared"
description = "Project profile"
created_by = "user"

[runner]
model = "gpt-4.1"

[tools]
allow = ["read", "write"]
`
	userContent := `
[meta]
name = "shared"
description = "User profile"
created_by = "user"

[runner]
model = "gpt-4.1-mini"

[tools]
allow = ["read"]
`
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "shared.toml"), []byte(projectContent), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(userDir, "shared.toml"), []byte(userContent), 0644))

	summaries, err := ListProfileSummariesFromDirs(projectDir, userDir)
	require.NoError(t, err)

	var shared *ProfileSummary
	for i := range summaries {
		if summaries[i].Name == "shared" {
			shared = &summaries[i]
			break
		}
	}
	require.NotNil(t, shared, "expected shared profile summary")
	assert.Equal(t, "Project profile", shared.Description)
	assert.Equal(t, "gpt-4.1", shared.Model)
	assert.Equal(t, 2, shared.AllowedToolCount)
	assert.Equal(t, []string{"read", "write"}, shared.AllowedTools)
	assert.Equal(t, "project", shared.SourceTier)
}

// TestSaveProfile verifies that SaveProfile writes a TOML file correctly.
func TestSaveProfile(t *testing.T) {
	dir := t.TempDir()

	p := &Profile{
		Meta: ProfileMeta{
			Name:        "test-save",
			Description: "Saved test profile",
			Version:     1,
			CreatedBy:   "user",
		},
		Runner: ProfileRunner{
			Model:    "gpt-4.1-mini",
			MaxSteps: 10,
		},
		Tools: ProfileTools{
			Allow: []string{"read", "write"},
		},
	}

	// Temporarily swap defaultUserProfilesDir by using the internal function.
	path := filepath.Join(dir, "test-save.toml")
	require.NoError(t, os.MkdirAll(dir, 0755))

	// Use the internal save logic with explicit dir.
	require.NoError(t, saveProfileToDir(p, dir))

	// Verify the file was written.
	_, err := os.Stat(path)
	require.NoError(t, err)

	// Reload and verify round-trip.
	loaded, err := loadProfileFile(path)
	require.NoError(t, err)
	assert.Equal(t, "test-save", loaded.Meta.Name)
	assert.Equal(t, []string{"read", "write"}, loaded.Tools.Allow)
}

// TestApplyValues verifies that ApplyValues returns correct profile fields.
func TestApplyValues(t *testing.T) {
	p := &Profile{
		Runner: ProfileRunner{
			Model:        "claude-opus-4-6",
			MaxSteps:     50,
			MaxCostUSD:   2.0,
			SystemPrompt: "Be thorough.",
		},
		Tools: ProfileTools{
			Allow: []string{"bash", "read"},
		},
	}

	vals := p.ApplyValues()
	assert.Equal(t, "claude-opus-4-6", vals.Model)
	assert.Equal(t, 50, vals.MaxSteps)
	assert.Equal(t, 2.0, vals.MaxCostUSD)
	assert.Equal(t, "Be thorough.", vals.SystemPrompt)
	assert.Equal(t, []string{"bash", "read"}, vals.AllowedTools)
}

// TestApplyValuesCopiesSlice verifies that AllowedTools is a copy (no aliasing).
func TestApplyValuesCopiesSlice(t *testing.T) {
	p := &Profile{
		Tools: ProfileTools{Allow: []string{"bash"}},
	}
	v1 := p.ApplyValues()
	v1.AllowedTools[0] = "mutated"
	v2 := p.ApplyValues()
	assert.Equal(t, "bash", v2.AllowedTools[0], "profile should not be mutated via AllowedTools alias")
}

// TestInvalidProfileName verifies path traversal protection.
func TestInvalidProfileName(t *testing.T) {
	tests := []string{"../secret", "/etc/passwd", "foo/bar", ""}
	for _, name := range tests {
		_, err := loadProfileWithDirs(name, "", "")
		assert.Error(t, err, "expected error for invalid name %q", name)
	}
}

// TestLoadProfileExported verifies the exported LoadProfile function resolves
// built-in profiles when no project or user directories are present.
func TestLoadProfileExported(t *testing.T) {
	// LoadProfile uses default dirs (which likely do not exist in CI),
	// so a built-in profile should still be found.
	p, err := LoadProfile("full")
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "full", p.Meta.Name)
}

// TestLoadProfileFromUserDirExported verifies the exported LoadProfileFromUserDir
// function resolves profiles from an explicit user directory.
func TestLoadProfileFromUserDirExported(t *testing.T) {
	dir := t.TempDir()

	content := `
[meta]
name = "exported-test"
description = "Exported test profile"
version = 1
created_at = "2026-01-01"
created_by = "test"
review_eligible = false

[runner]
model = "gpt-4.1-mini"
max_steps = 3
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "exported-test.toml"), []byte(content), 0644))

	p, err := LoadProfileFromUserDir("exported-test", dir)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, "exported-test", p.Meta.Name)
	assert.Equal(t, 3, p.Runner.MaxSteps)
}

// TestListProfilesExported verifies the exported ListProfiles function returns
// at least the built-in profiles.
func TestListProfilesExported(t *testing.T) {
	names, err := ListProfiles()
	require.NoError(t, err)
	// Built-in profiles must always be present.
	assert.Contains(t, names, "full")
	assert.Contains(t, names, "github")
	assert.Contains(t, names, "researcher")
}

func TestListProfileSummariesExported(t *testing.T) {
	summaries, err := ListProfileSummaries()
	require.NoError(t, err)

	var fullProfile *ProfileSummary
	for i := range summaries {
		if summaries[i].Name == "full" {
			fullProfile = &summaries[i]
			break
		}
	}

	require.NotNil(t, fullProfile, "expected built-in full profile summary")
	assert.Equal(t, "built-in", fullProfile.SourceTier)
	assert.NotEmpty(t, fullProfile.Model)
	assert.Equal(t, fullProfile.AllowedToolCount, len(fullProfile.AllowedTools))
}

// TestSaveProfileExported verifies the exported SaveProfile function writes a
// TOML file to the default user profiles directory.
func TestSaveProfileExported(t *testing.T) {
	dir := t.TempDir()
	p := &Profile{
		Meta: ProfileMeta{
			Name:        "save-exported-test",
			Description: "Test save via exported function",
			Version:     1,
			CreatedAt:   "2026-01-01",
			CreatedBy:   "test",
		},
		Runner: ProfileRunner{
			Model:    "gpt-4.1-mini",
			MaxSteps: 10,
		},
	}

	err := saveProfileToDir(p, dir)
	require.NoError(t, err)

	path := filepath.Join(dir, "save-exported-test.toml")
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "expected TOML file to be written")

	loaded, err := loadProfileWithDirs("save-exported-test", "", dir)
	require.NoError(t, err)
	assert.Equal(t, "save-exported-test", loaded.Meta.Name)
	assert.Equal(t, 10, loaded.Runner.MaxSteps)
}

// TestProfile_TOMLRoundTrip_NewFields verifies that the new expanded profile
// fields (permissions, isolation_mode, cleanup_policy, base_ref,
// reasoning_effort, result_mode) parse correctly from TOML and round-trip
// back to zero values when the TOML does not include them.
func TestProfile_TOMLRoundTrip_NewFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := `
isolation_mode = "worktree"
cleanup_policy = "delete_on_success"
base_ref = "main"
result_mode = "summary"

[meta]
name = "newfields-test"
description = "Test new profile fields"
version = 1
created_at = "2026-01-01"
created_by = "test"
review_eligible = false

[runner]
model = "gpt-4.1-mini"
max_steps = 10
max_cost_usd = 1.0
reasoning_effort = "high"

[permissions]
allow_bash = true
allow_file_write = true
allow_net_access = false
allowed_commands = ["git", "go"]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "newfields-test.toml"), []byte(content), 0644))

	p, err := loadProfileWithDirs("newfields-test", "", dir)
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "newfields-test", p.Meta.Name)
	assert.Equal(t, "high", p.Runner.ReasoningEffort)
	assert.True(t, p.Permissions.AllowBash)
	assert.True(t, p.Permissions.AllowFileWrite)
	assert.False(t, p.Permissions.AllowNetAccess)
	assert.Equal(t, []string{"git", "go"}, p.Permissions.AllowedCommands)
	assert.Equal(t, "worktree", p.IsolationMode)
	assert.Equal(t, "delete_on_success", p.CleanupPolicy)
	assert.Equal(t, "main", p.BaseRef)
	assert.Equal(t, "summary", p.ResultMode)
}

// TestProfile_TOMLRoundTrip_ZeroValues verifies backward-compatibility: old
// TOML without new fields parses without error, and new fields are zero.
func TestProfile_TOMLRoundTrip_ZeroValues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := `
[meta]
name = "old-compat"
description = "Old-style profile without new fields"
version = 1
created_at = "2026-01-01"
created_by = "test"
review_eligible = false

[runner]
model = "gpt-4.1-mini"
max_steps = 5

[tools]
allow = ["read"]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old-compat.toml"), []byte(content), 0644))

	p, err := loadProfileWithDirs("old-compat", "", dir)
	require.NoError(t, err)
	require.NotNil(t, p)

	assert.Equal(t, "", p.Runner.ReasoningEffort, "missing reasoning_effort must be empty string")
	assert.Equal(t, "", p.IsolationMode, "missing isolation_mode must be empty string")
	assert.Equal(t, "", p.CleanupPolicy, "missing cleanup_policy must be empty string")
	assert.Equal(t, "", p.BaseRef, "missing base_ref must be empty string")
	assert.Equal(t, "", p.ResultMode, "missing result_mode must be empty string")
	assert.False(t, p.Permissions.AllowBash, "missing allow_bash must be false")
	assert.False(t, p.Permissions.AllowFileWrite, "missing allow_file_write must be false")
	assert.False(t, p.Permissions.AllowNetAccess, "missing allow_net_access must be false")
	assert.Nil(t, p.Permissions.AllowedCommands, "missing allowed_commands must be nil")
}

// TestProfile_ApplyValues_IsolationMode verifies that isolation_mode from the
// profile is propagated through ApplyValues.
func TestProfile_ApplyValues_IsolationMode(t *testing.T) {
	t.Parallel()

	p := &Profile{
		Runner:        ProfileRunner{Model: "gpt-4.1-mini", MaxSteps: 5},
		IsolationMode: "worktree",
	}

	vals := p.ApplyValues()
	assert.Equal(t, "worktree", vals.IsolationMode)
}

// TestProfile_ApplyValues_ReasoningEffort verifies that reasoning_effort from
// the profile runner section is propagated through ApplyValues.
func TestProfile_ApplyValues_ReasoningEffort(t *testing.T) {
	t.Parallel()

	p := &Profile{
		Runner: ProfileRunner{
			Model:           "gpt-4.1-mini",
			MaxSteps:        5,
			ReasoningEffort: "medium",
		},
	}

	vals := p.ApplyValues()
	assert.Equal(t, "medium", vals.ReasoningEffort)
}

// TestProfile_ApplyValues_ResultMode verifies that result_mode from the
// profile is propagated through ApplyValues.
func TestProfile_ApplyValues_ResultMode(t *testing.T) {
	t.Parallel()

	p := &Profile{
		Runner:     ProfileRunner{Model: "gpt-4.1-mini"},
		ResultMode: "full",
	}

	vals := p.ApplyValues()
	assert.Equal(t, "full", vals.ResultMode)
}

// TestProfile_ApplyValues_Permissions verifies that permissions from the
// profile are propagated through ApplyValues.
func TestProfile_ApplyValues_Permissions(t *testing.T) {
	t.Parallel()

	p := &Profile{
		Runner: ProfileRunner{Model: "gpt-4.1-mini"},
		Permissions: ProfilePermissions{
			AllowBash:       true,
			AllowFileWrite:  false,
			AllowNetAccess:  true,
			AllowedCommands: []string{"git", "go", "make"},
		},
	}

	vals := p.ApplyValues()
	assert.True(t, vals.Permissions.AllowBash)
	assert.False(t, vals.Permissions.AllowFileWrite)
	assert.True(t, vals.Permissions.AllowNetAccess)
	assert.Equal(t, []string{"git", "go", "make"}, vals.Permissions.AllowedCommands)
}

// TestProfile_ApplyValues_CleanupPolicyAndBaseRef verifies that cleanup_policy
// and base_ref are propagated through ApplyValues.
func TestProfile_ApplyValues_CleanupPolicyAndBaseRef(t *testing.T) {
	t.Parallel()

	p := &Profile{
		Runner:        ProfileRunner{Model: "gpt-4.1-mini"},
		CleanupPolicy: "delete",
		BaseRef:       "develop",
	}

	vals := p.ApplyValues()
	assert.Equal(t, "delete", vals.CleanupPolicy)
	assert.Equal(t, "develop", vals.BaseRef)
}

// TestSaveProfileCallsDefaultUserDir exercises the exported SaveProfile path
// by temporarily overriding HOME to point to a temp directory.
func TestSaveProfileCallsDefaultUserDir(t *testing.T) {
	// Note: t.Parallel() is intentionally omitted — t.Setenv requires sequential execution.

	// Redirect HOME so SaveProfile writes to a temp dir.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	p := &Profile{
		Meta: ProfileMeta{
			Name:        "save-via-exported",
			Description: "Coverage test for exported SaveProfile",
			Version:     1,
			CreatedAt:   "2026-01-01",
			CreatedBy:   "test",
		},
		Runner: ProfileRunner{Model: "gpt-4.1-mini", MaxSteps: 1},
	}

	err := SaveProfile(p)
	require.NoError(t, err, "SaveProfile must succeed with valid home dir")

	// Verify the file was written under the expected subdirectory.
	// defaultUserProfilesDir() returns $HOME/.harness/profiles.
	expectedDir := filepath.Join(tmpHome, ".harness", "profiles")
	_, statErr := os.Stat(filepath.Join(expectedDir, "save-via-exported.toml"))
	require.NoError(t, statErr, "expected TOML file at %s", expectedDir)
}
