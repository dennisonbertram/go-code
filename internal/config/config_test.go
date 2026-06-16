package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go-agent-harness/internal/config"
)

// TestDefaultValues verifies the built-in defaults are applied when no other
// config layers are present.
func TestDefaultValues(t *testing.T) {
	cfg := config.Defaults()

	if cfg.Model != "gpt-4.1-mini" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4.1-mini")
	}
	if cfg.MaxSteps != 0 {
		t.Errorf("MaxSteps: got %d, want 0", cfg.MaxSteps)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr: got %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.Cost.MaxPerRunUSD != 0.0 {
		t.Errorf("Cost.MaxPerRunUSD: got %f, want 0.0", cfg.Cost.MaxPerRunUSD)
	}
	if !cfg.Memory.Enabled {
		t.Error("Memory.Enabled: got false, want true")
	}
}

// TestLoadMissingFilesSkipped verifies that missing config files are
// skipped gracefully rather than returning an error.
func TestLoadMissingFilesSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	opts := config.LoadOptions{
		UserConfigPath:    filepath.Join(tmpDir, "nonexistent", "config.toml"),
		ProjectConfigPath: filepath.Join(tmpDir, "nonexistent2", "config.toml"),
		ProfilesDir:       filepath.Join(tmpDir, "nonexistent3"),
		ProfileName:       "",
		Getenv:            func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() returned error for missing files: %v", err)
	}
	// Should fall back to defaults
	if cfg.Model != "gpt-4.1-mini" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4.1-mini")
	}
}

// TestUserConfigOverridesDefaults verifies that values set in the user global
// config override the built-in defaults.
func TestUserConfigOverridesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	userConfig := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(userConfig, []byte(`model = "gpt-4o"`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath:    userConfig,
		ProjectConfigPath: filepath.Join(tmpDir, "nonexistent.toml"),
		ProfilesDir:       tmpDir,
		ProfileName:       "",
		Getenv:            func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4o")
	}
	// addr should still be the default
	if cfg.Addr != ":8080" {
		t.Errorf("Addr: got %q, want %q", cfg.Addr, ":8080")
	}
}

// TestProjectConfigOverridesUserConfig verifies that the project config
// overrides the user global config.
func TestProjectConfigOverridesUserConfig(t *testing.T) {
	tmpDir := t.TempDir()
	userConfig := filepath.Join(tmpDir, "user.toml")
	projectConfig := filepath.Join(tmpDir, "project.toml")

	if err := os.WriteFile(userConfig, []byte(`
model = "gpt-4o"
addr = ":9000"
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte(`
model = "gpt-4.1"
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath:    userConfig,
		ProjectConfigPath: projectConfig,
		ProfilesDir:       tmpDir,
		ProfileName:       "",
		Getenv:            func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// Project overrides user for model
	if cfg.Model != "gpt-4.1" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4.1")
	}
	// User-set addr survives project merge (project didn't set it)
	if cfg.Addr != ":9000" {
		t.Errorf("Addr: got %q, want %q", cfg.Addr, ":9000")
	}
}

// TestProfileOverridesProjectConfig verifies that a named profile overrides
// the project config.
func TestProfileOverridesProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := filepath.Join(tmpDir, "project.toml")
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(projectConfig, []byte(`model = "gpt-4.1"`), 0600); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profilesDir, "fast.toml")
	if err := os.WriteFile(profilePath, []byte(`model = "gpt-4.1-mini"`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath:    filepath.Join(tmpDir, "nonexistent.toml"),
		ProjectConfigPath: projectConfig,
		ProfilesDir:       profilesDir,
		ProfileName:       "fast",
		Getenv:            func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Model != "gpt-4.1-mini" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4.1-mini")
	}
}

// TestEnvVarOverridesProfile verifies that HARNESS_* env vars (layer 5)
// override profile config (layer 4).
func TestEnvVarOverridesProfile(t *testing.T) {
	tmpDir := t.TempDir()
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profilesDir, "myprofile.toml")
	if err := os.WriteFile(profilePath, []byte(`model = "gpt-4.1"`), 0600); err != nil {
		t.Fatal(err)
	}

	envMap := map[string]string{
		"HARNESS_MODEL": "o1-mini",
	}
	opts := config.LoadOptions{
		UserConfigPath:    filepath.Join(tmpDir, "nonexistent.toml"),
		ProjectConfigPath: filepath.Join(tmpDir, "nonexistent2.toml"),
		ProfilesDir:       profilesDir,
		ProfileName:       "myprofile",
		Getenv: func(key string) string {
			return envMap[key]
		},
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Model != "o1-mini" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "o1-mini")
	}
}

// TestEnvVarMaxSteps verifies HARNESS_MAX_STEPS is parsed correctly.
func TestEnvVarMaxSteps(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_MAX_STEPS": "20",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.MaxSteps != 20 {
		t.Errorf("MaxSteps: got %d, want 20", cfg.MaxSteps)
	}
}

// TestEnvVarAddr verifies HARNESS_ADDR is applied.
func TestEnvVarAddr(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_ADDR": ":9090",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Addr != ":9090" {
		t.Errorf("Addr: got %q, want %q", cfg.Addr, ":9090")
	}
}

// TestEnvVarMaxCostPerRun verifies HARNESS_MAX_COST_PER_RUN_USD is parsed.
func TestEnvVarMaxCostPerRun(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_MAX_COST_PER_RUN_USD": "0.50",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cost.MaxPerRunUSD != 0.50 {
		t.Errorf("Cost.MaxPerRunUSD: got %f, want 0.50", cfg.Cost.MaxPerRunUSD)
	}
}

// TestMalformedTOMLReturnsError verifies malformed TOML causes an error.
func TestMalformedTOMLReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	badConfig := filepath.Join(tmpDir, "bad.toml")
	if err := os.WriteFile(badConfig, []byte(`model = `), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: badConfig,
		Getenv:         func(string) string { return "" },
	}
	_, err := config.Load(opts)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

// TestMissingProfileReturnsError verifies that specifying a profile name
// that does not exist returns an error.
func TestMissingProfileReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0700); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		ProfilesDir: profilesDir,
		ProfileName: "nonexistent",
		Getenv:      func(string) string { return "" },
	}
	_, err := config.Load(opts)
	if err == nil {
		t.Fatal("expected error for missing profile, got nil")
	}
}

// TestEmptyProfileNameIsSkipped verifies that an empty profile name
// doesn't try to load a profile file.
func TestEmptyProfileNameIsSkipped(t *testing.T) {
	opts := config.LoadOptions{
		ProfileName: "",
		Getenv:      func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() with empty profile name returned error: %v", err)
	}
	// Should use defaults
	if cfg.Model != "gpt-4.1-mini" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4.1-mini")
	}
}

// TestAllLayersMergeInCorrectOrder verifies that all 5 layers are merged
// correctly with highest priority winning.
// Layer order: defaults (1) < user (2) < project (3) < profile (4) < env (5)
func TestAllLayersMergeInCorrectOrder(t *testing.T) {
	tmpDir := t.TempDir()
	userConfig := filepath.Join(tmpDir, "user.toml")
	projectConfig := filepath.Join(tmpDir, "project.toml")
	profilesDir := filepath.Join(tmpDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0700); err != nil {
		t.Fatal(err)
	}

	// User sets model and addr
	if err := os.WriteFile(userConfig, []byte(`
model = "user-model"
addr = ":2000"
max_steps = 5
`), 0600); err != nil {
		t.Fatal(err)
	}

	// Project overrides model only
	if err := os.WriteFile(projectConfig, []byte(`
model = "project-model"
`), 0600); err != nil {
		t.Fatal(err)
	}

	// Profile overrides model and max_steps
	profilePath := filepath.Join(profilesDir, "staging.toml")
	if err := os.WriteFile(profilePath, []byte(`
model = "profile-model"
max_steps = 10
`), 0600); err != nil {
		t.Fatal(err)
	}

	// Env overrides model only
	envMap := map[string]string{
		"HARNESS_MODEL": "env-model",
	}
	opts := config.LoadOptions{
		UserConfigPath:    userConfig,
		ProjectConfigPath: projectConfig,
		ProfilesDir:       profilesDir,
		ProfileName:       "staging",
		Getenv:            func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Env wins for model
	if cfg.Model != "env-model" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "env-model")
	}
	// addr: user set it, project and profile didn't override, env didn't set it
	if cfg.Addr != ":2000" {
		t.Errorf("Addr: got %q, want %q", cfg.Addr, ":2000")
	}
	// max_steps: profile set 10, env didn't override
	if cfg.MaxSteps != 10 {
		t.Errorf("MaxSteps: got %d, want 10", cfg.MaxSteps)
	}
}

// TestCostSectionTOML verifies parsing of the [cost] section.
func TestCostSectionTOML(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfg, []byte(`
[cost]
max_per_run_usd = 1.25
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: cfg,
		Getenv:         func(string) string { return "" },
	}
	result, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if result.Cost.MaxPerRunUSD != 1.25 {
		t.Errorf("Cost.MaxPerRunUSD: got %f, want 1.25", result.Cost.MaxPerRunUSD)
	}
}

// TestMemorySectionTOML verifies parsing of the [memory] section.
func TestMemorySectionTOML(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfg, []byte(`
[memory]
enabled = false
mode = "auto"
db_driver = "postgres"
db_dsn = "postgres://memory"
sqlite_path = "data/memory.db"
default_enabled = true
observe_min_tokens = 1400
snippet_max_tokens = 950
reflect_threshold_tokens = 4200
llm_mode = "provider"
llm_provider = "openrouter"
llm_model = "moonshotai/kimi-k2.5"
llm_base_url = "https://example.test/v1"
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: cfg,
		Getenv:         func(string) string { return "" },
	}
	result, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if result.Memory.Enabled {
		t.Error("Memory.Enabled: got true, want false")
	}
	if result.Memory.Mode != "auto" {
		t.Errorf("Memory.Mode: got %q, want %q", result.Memory.Mode, "auto")
	}
	if result.Memory.DBDriver != "postgres" {
		t.Errorf("Memory.DBDriver: got %q, want %q", result.Memory.DBDriver, "postgres")
	}
	if result.Memory.DBDSN != "postgres://memory" {
		t.Errorf("Memory.DBDSN: got %q, want %q", result.Memory.DBDSN, "postgres://memory")
	}
	if result.Memory.SQLitePath != "data/memory.db" {
		t.Errorf("Memory.SQLitePath: got %q, want %q", result.Memory.SQLitePath, "data/memory.db")
	}
	if !result.Memory.DefaultEnabled {
		t.Error("Memory.DefaultEnabled: got false, want true")
	}
	if result.Memory.ObserveMinTokens != 1400 {
		t.Errorf("Memory.ObserveMinTokens: got %d, want 1400", result.Memory.ObserveMinTokens)
	}
	if result.Memory.SnippetMaxTokens != 950 {
		t.Errorf("Memory.SnippetMaxTokens: got %d, want 950", result.Memory.SnippetMaxTokens)
	}
	if result.Memory.ReflectThresholdTokens != 4200 {
		t.Errorf("Memory.ReflectThresholdTokens: got %d, want 4200", result.Memory.ReflectThresholdTokens)
	}
	if result.Memory.LLMMode != "provider" {
		t.Errorf("Memory.LLMMode: got %q, want %q", result.Memory.LLMMode, "provider")
	}
	if result.Memory.LLMProvider != "openrouter" {
		t.Errorf("Memory.LLMProvider: got %q, want %q", result.Memory.LLMProvider, "openrouter")
	}
	if result.Memory.LLMModel != "moonshotai/kimi-k2.5" {
		t.Errorf("Memory.LLMModel: got %q, want %q", result.Memory.LLMModel, "moonshotai/kimi-k2.5")
	}
	if result.Memory.LLMBaseURL != "https://example.test/v1" {
		t.Errorf("Memory.LLMBaseURL: got %q, want %q", result.Memory.LLMBaseURL, "https://example.test/v1")
	}
}

func TestMemoryEnvOverrides(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_MEMORY_MODE":                     "off",
		"HARNESS_MEMORY_DB_DRIVER":                "postgres",
		"HARNESS_MEMORY_DB_DSN":                   "postgres://override",
		"HARNESS_MEMORY_SQLITE_PATH":              "override.db",
		"HARNESS_MEMORY_DEFAULT_ENABLED":          "true",
		"HARNESS_MEMORY_OBSERVE_MIN_TOKENS":       "1600",
		"HARNESS_MEMORY_SNIPPET_MAX_TOKENS":       "980",
		"HARNESS_MEMORY_REFLECT_THRESHOLD_TOKENS": "4300",
		"HARNESS_MEMORY_LLM_MODE":                 "provider",
		"HARNESS_MEMORY_LLM_PROVIDER":             "anthropic",
		"HARNESS_MEMORY_LLM_MODEL":                "claude-sonnet-4-6",
		"HARNESS_MEMORY_LLM_BASE_URL":             "https://provider.example/v1",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Memory.Mode != "off" {
		t.Errorf("Memory.Mode: got %q, want %q", cfg.Memory.Mode, "off")
	}
	if cfg.Memory.DBDriver != "postgres" {
		t.Errorf("Memory.DBDriver: got %q, want %q", cfg.Memory.DBDriver, "postgres")
	}
	if cfg.Memory.DBDSN != "postgres://override" {
		t.Errorf("Memory.DBDSN: got %q, want %q", cfg.Memory.DBDSN, "postgres://override")
	}
	if cfg.Memory.SQLitePath != "override.db" {
		t.Errorf("Memory.SQLitePath: got %q, want %q", cfg.Memory.SQLitePath, "override.db")
	}
	if !cfg.Memory.DefaultEnabled {
		t.Error("Memory.DefaultEnabled: got false, want true")
	}
	if cfg.Memory.ObserveMinTokens != 1600 {
		t.Errorf("Memory.ObserveMinTokens: got %d, want 1600", cfg.Memory.ObserveMinTokens)
	}
	if cfg.Memory.SnippetMaxTokens != 980 {
		t.Errorf("Memory.SnippetMaxTokens: got %d, want 980", cfg.Memory.SnippetMaxTokens)
	}
	if cfg.Memory.ReflectThresholdTokens != 4300 {
		t.Errorf("Memory.ReflectThresholdTokens: got %d, want 4300", cfg.Memory.ReflectThresholdTokens)
	}
	if cfg.Memory.LLMMode != "provider" {
		t.Errorf("Memory.LLMMode: got %q, want %q", cfg.Memory.LLMMode, "provider")
	}
	if cfg.Memory.LLMProvider != "anthropic" {
		t.Errorf("Memory.LLMProvider: got %q, want %q", cfg.Memory.LLMProvider, "anthropic")
	}
	if cfg.Memory.LLMModel != "claude-sonnet-4-6" {
		t.Errorf("Memory.LLMModel: got %q, want %q", cfg.Memory.LLMModel, "claude-sonnet-4-6")
	}
	if cfg.Memory.LLMBaseURL != "https://provider.example/v1" {
		t.Errorf("Memory.LLMBaseURL: got %q, want %q", cfg.Memory.LLMBaseURL, "https://provider.example/v1")
	}
}

// TestInvalidMaxStepsEnvVar verifies that an invalid HARNESS_MAX_STEPS env
// var value is gracefully skipped (falls back to previous layer value).
func TestInvalidMaxStepsEnvVar(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_MAX_STEPS": "not-a-number",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// Should fall back to default (0)
	if cfg.MaxSteps != 0 {
		t.Errorf("MaxSteps: got %d, want 0", cfg.MaxSteps)
	}
}

// TestResolveReturnsMergedConfig verifies that Config.Resolve() returns
// the same config it holds (it's the final resolved value).
func TestResolveReturnsMergedConfig(t *testing.T) {
	opts := config.LoadOptions{
		Getenv: func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	resolved := cfg.Resolve()
	if resolved.Model != cfg.Model {
		t.Errorf("Resolve() model mismatch: got %q, want %q", resolved.Model, cfg.Model)
	}
}

// TestProfilePathTraversal verifies that profile names with path separators
// are rejected to prevent path traversal attacks.
func TestProfilePathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name string
	}{
		{"../evil"},
		{"../../etc/passwd"},
		{"/absolute/path"},
		{"sub/dir/profile"},
	}

	for _, tt := range tests {
		opts := config.LoadOptions{
			ProfilesDir: tmpDir,
			ProfileName: tt.name,
			Getenv:      func(string) string { return "" },
		}
		_, err := config.Load(opts)
		if err == nil {
			t.Errorf("profile %q: expected error for path traversal, got nil", tt.name)
		}
	}
}

// TestMCPServersDefaultEmpty verifies that MCPServers is nil/empty by default.
func TestMCPServersDefaultEmpty(t *testing.T) {
	cfg := config.Defaults()
	if len(cfg.MCPServers) != 0 {
		t.Errorf("MCPServers: got %d entries, want 0", len(cfg.MCPServers))
	}
}

// TestMCPServersLoadedFromTOML verifies that [mcp_servers.*] sections in
// a TOML file are decoded into the MCPServers map with the correct values.
func TestMCPServersLoadedFromTOML(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[mcp_servers.my-tool]
transport = "stdio"
command = "/usr/local/bin/my-mcp-server"
args = ["--verbose", "--port=8765"]

[mcp_servers.remote]
transport = "http"
url = "http://localhost:3001/mcp"
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: cfgPath,
		Getenv:         func(string) string { return "" },
	}
	result, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(result.MCPServers) != 2 {
		t.Fatalf("MCPServers: got %d entries, want 2", len(result.MCPServers))
	}

	myTool, ok := result.MCPServers["my-tool"]
	if !ok {
		t.Fatal("MCPServers: missing key \"my-tool\"")
	}
	if myTool.Transport != "stdio" {
		t.Errorf("my-tool.Transport: got %q, want \"stdio\"", myTool.Transport)
	}
	if myTool.Command != "/usr/local/bin/my-mcp-server" {
		t.Errorf("my-tool.Command: got %q, want \"/usr/local/bin/my-mcp-server\"", myTool.Command)
	}
	if len(myTool.Args) != 2 || myTool.Args[0] != "--verbose" || myTool.Args[1] != "--port=8765" {
		t.Errorf("my-tool.Args: got %v, want [\"--verbose\" \"--port=8765\"]", myTool.Args)
	}

	remote, ok := result.MCPServers["remote"]
	if !ok {
		t.Fatal("MCPServers: missing key \"remote\"")
	}
	if remote.Transport != "http" {
		t.Errorf("remote.Transport: got %q, want \"http\"", remote.Transport)
	}
	if remote.URL != "http://localhost:3001/mcp" {
		t.Errorf("remote.URL: got %q, want \"http://localhost:3001/mcp\"", remote.URL)
	}
}

// TestMCPServersLayerMerge verifies that MCPServer entries from higher-priority
// config layers are merged additively with lower-priority entries.
// Specifically:
//   - user layer adds "server-a"
//   - project layer adds "server-b" and overrides "server-a"
//   - result should have both servers, with server-a using project values
func TestMCPServersLayerMerge(t *testing.T) {
	tmpDir := t.TempDir()
	userConfig := filepath.Join(tmpDir, "user.toml")
	projectConfig := filepath.Join(tmpDir, "project.toml")

	if err := os.WriteFile(userConfig, []byte(`
[mcp_servers.server-a]
transport = "stdio"
command = "/usr/bin/tool-a-user"
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte(`
[mcp_servers.server-a]
transport = "stdio"
command = "/usr/bin/tool-a-project"

[mcp_servers.server-b]
transport = "http"
url = "http://localhost:4000/mcp"
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath:    userConfig,
		ProjectConfigPath: projectConfig,
		Getenv:            func(string) string { return "" },
	}
	result, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(result.MCPServers) != 2 {
		t.Fatalf("MCPServers: got %d entries, want 2", len(result.MCPServers))
	}

	// server-a should be the project version (higher priority)
	srvA, ok := result.MCPServers["server-a"]
	if !ok {
		t.Fatal("MCPServers: missing \"server-a\"")
	}
	if srvA.Command != "/usr/bin/tool-a-project" {
		t.Errorf("server-a.Command: got %q, want \"/usr/bin/tool-a-project\"", srvA.Command)
	}

	// server-b should be present from the project layer
	srvB, ok := result.MCPServers["server-b"]
	if !ok {
		t.Fatal("MCPServers: missing \"server-b\"")
	}
	if srvB.Transport != "http" {
		t.Errorf("server-b.Transport: got %q, want \"http\"", srvB.Transport)
	}
	if srvB.URL != "http://localhost:4000/mcp" {
		t.Errorf("server-b.URL: got %q, want \"http://localhost:4000/mcp\"", srvB.URL)
	}
}

// TestConcurrentLoad verifies that Load() is safe for concurrent use.
func TestConcurrentLoad(t *testing.T) {
	tmpDir := t.TempDir()
	userConfig := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(userConfig, []byte(`model = "gpt-4o"`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: userConfig,
		Getenv:         func(string) string { return "" },
	}

	const goroutines = 20
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			cfg, err := config.Load(opts)
			if err != nil {
				errCh <- err
				return
			}
			if cfg.Model != "gpt-4o" {
				errCh <- fmt.Errorf("Load() returned Model=%q, want \"gpt-4o\"", cfg.Model)
				return
			}
			errCh <- nil
		}()
	}

	for i := 0; i < goroutines; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent Load() error: %v", err)
		}
	}
}

// TestConclusionWatcherConfig_Defaults verifies that ConclusionWatcher defaults
// are applied correctly when no config layers override them.
func TestConclusionWatcherConfig_Defaults(t *testing.T) {
	cfg := config.Defaults()

	if cfg.ConclusionWatcher.Enabled {
		t.Error("ConclusionWatcher.Enabled: got true, want false")
	}
	if cfg.ConclusionWatcher.InterventionMode != "inject_validation_prompt" {
		t.Errorf("ConclusionWatcher.InterventionMode: got %q, want \"inject_validation_prompt\"", cfg.ConclusionWatcher.InterventionMode)
	}
	if cfg.ConclusionWatcher.EvaluatorEnabled {
		t.Error("ConclusionWatcher.EvaluatorEnabled: got true, want false")
	}
	if cfg.ConclusionWatcher.EvaluatorModel != "gpt-4o-mini" {
		t.Errorf("ConclusionWatcher.EvaluatorModel: got %q, want \"gpt-4o-mini\"", cfg.ConclusionWatcher.EvaluatorModel)
	}
}

// TestConclusionWatcherConfig_EnvVarOverride verifies that HARNESS_CONCLUSION_WATCHER_*
// environment variables override defaults.
func TestConclusionWatcherConfig_EnvVarOverride(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_CONCLUSION_WATCHER_ENABLED":         "true",
		"HARNESS_CONCLUSION_WATCHER_EVALUATOR_MODEL": "gpt-4o",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.ConclusionWatcher.Enabled {
		t.Error("ConclusionWatcher.Enabled: got false, want true")
	}
	if cfg.ConclusionWatcher.EvaluatorModel != "gpt-4o" {
		t.Errorf("ConclusionWatcher.EvaluatorModel: got %q, want \"gpt-4o\"", cfg.ConclusionWatcher.EvaluatorModel)
	}
}

// TestConclusionWatcherConfig_TOMLOverride verifies that the [conclusion_watcher]
// TOML section is parsed and applied correctly.
func TestConclusionWatcherConfig_TOMLOverride(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[conclusion_watcher]
enabled = true
intervention_mode = "pause_for_user"
evaluator_enabled = true
evaluator_model = "gpt-4o"
evaluator_api_key = "sk-test-key"
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: cfgPath,
		Getenv:         func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.ConclusionWatcher.Enabled {
		t.Error("ConclusionWatcher.Enabled: got false, want true")
	}
	if cfg.ConclusionWatcher.InterventionMode != "pause_for_user" {
		t.Errorf("ConclusionWatcher.InterventionMode: got %q, want \"pause_for_user\"", cfg.ConclusionWatcher.InterventionMode)
	}
	if !cfg.ConclusionWatcher.EvaluatorEnabled {
		t.Error("ConclusionWatcher.EvaluatorEnabled: got false, want true")
	}
	if cfg.ConclusionWatcher.EvaluatorModel != "gpt-4o" {
		t.Errorf("ConclusionWatcher.EvaluatorModel: got %q, want \"gpt-4o\"", cfg.ConclusionWatcher.EvaluatorModel)
	}
	if cfg.ConclusionWatcher.EvaluatorAPIKey != "sk-test-key" {
		t.Errorf("ConclusionWatcher.EvaluatorAPIKey: got %q, want \"sk-test-key\"", cfg.ConclusionWatcher.EvaluatorAPIKey)
	}
}

// TestCronConfig_Defaults verifies cron defaults.
func TestCronConfig_Defaults(t *testing.T) {
	cfg := config.Defaults()

	if !cfg.Cron.JitterEnabled {
		t.Error("Cron.JitterEnabled: got false, want true")
	}
	if cfg.Cron.JitterMinSec != 60 {
		t.Errorf("Cron.JitterMinSec: got %d, want 60", cfg.Cron.JitterMinSec)
	}
	if cfg.Cron.JitterMaxSec != 300 {
		t.Errorf("Cron.JitterMaxSec: got %d, want 300", cfg.Cron.JitterMaxSec)
	}
	if len(cfg.Cron.AvoidMinuteMarks) != 2 ||
		cfg.Cron.AvoidMinuteMarks[0] != 0 ||
		cfg.Cron.AvoidMinuteMarks[1] != 30 {
		t.Errorf("Cron.AvoidMinuteMarks: got %v, want [0, 30]", cfg.Cron.AvoidMinuteMarks)
	}
	if !cfg.Cron.LogJitteredTimes {
		t.Error("Cron.LogJitteredTimes: got false, want true")
	}
}

// TestCronConfig_TOMLOverride verifies that the [cron] TOML section is parsed.
func TestCronConfig_TOMLOverride(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[cron]
jitter_enabled = false
jitter_min_sec = 30
jitter_max_sec = 120
avoid_minute_marks = [0, 15, 30, 45]
log_jittered_times = false
`), 0600); err != nil {
		t.Fatal(err)
	}

	opts := config.LoadOptions{
		UserConfigPath: cfgPath,
		Getenv:         func(string) string { return "" },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Cron.JitterEnabled {
		t.Error("Cron.JitterEnabled: got true, want false")
	}
	if cfg.Cron.JitterMinSec != 30 {
		t.Errorf("Cron.JitterMinSec: got %d, want 30", cfg.Cron.JitterMinSec)
	}
	if cfg.Cron.JitterMaxSec != 120 {
		t.Errorf("Cron.JitterMaxSec: got %d, want 120", cfg.Cron.JitterMaxSec)
	}
	if len(cfg.Cron.AvoidMinuteMarks) != 4 {
		t.Errorf("Cron.AvoidMinuteMarks: got %v, want [0, 15, 30, 45]", cfg.Cron.AvoidMinuteMarks)
	}
	if cfg.Cron.LogJitteredTimes {
		t.Error("Cron.LogJitteredTimes: got true, want false")
	}
}

// TestCronConfig_EnvVarOverrides verifies env vars override cron defaults.
func TestCronConfig_EnvVarOverrides(t *testing.T) {
	envMap := map[string]string{
		"HARNESS_CRON_JITTER_ENABLED": "false",
		"HARNESS_CRON_JITTER_MIN_SEC": "30",
		"HARNESS_CRON_JITTER_MAX_SEC": "120",
	}
	opts := config.LoadOptions{
		Getenv: func(key string) string { return envMap[key] },
	}
	cfg, err := config.Load(opts)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Cron.JitterEnabled {
		t.Error("Cron.JitterEnabled: got true, want false")
	}
	if cfg.Cron.JitterMinSec != 30 {
		t.Errorf("Cron.JitterMinSec: got %d, want 30", cfg.Cron.JitterMinSec)
	}
	if cfg.Cron.JitterMaxSec != 120 {
		t.Errorf("Cron.JitterMaxSec: got %d, want 120", cfg.Cron.JitterMaxSec)
	}
}
