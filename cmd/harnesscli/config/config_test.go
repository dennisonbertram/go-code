package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"go-agent-harness/cmd/harnesscli/config"
)

// TestConfig_LoadMissingFileReturnsEmpty verifies Load returns empty Config when file doesn't exist.
func TestConfig_LoadMissingFileReturnsEmpty(t *testing.T) {
	// Use a temp dir that definitely has no config file.
	t.Setenv("HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned error for missing file: %v", err)
	}
	if len(cfg.StarredModels) != 0 {
		t.Errorf("Load() on missing file: StarredModels = %v, want empty", cfg.StarredModels)
	}
}

// TestConfig_SaveLoadRoundTrip verifies Save+Load preserves StarredModels.
func TestConfig_SaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	original := &config.Config{
		StarredModels: []string{"gpt-4.1", "claude-sonnet-4-6", "deepseek-reasoner"},
	}

	if err := config.Save(original); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}

	if len(loaded.StarredModels) != len(original.StarredModels) {
		t.Fatalf("StarredModels length: got %d, want %d", len(loaded.StarredModels), len(original.StarredModels))
	}
	for i, id := range original.StarredModels {
		if loaded.StarredModels[i] != id {
			t.Errorf("StarredModels[%d] = %q, want %q", i, loaded.StarredModels[i], id)
		}
	}
}

// TestConfig_SaveCreatesDirectoryIfNotExists verifies Save creates intermediate dirs.
func TestConfig_SaveCreatesDirectoryIfNotExists(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Ensure the directory does not exist beforehand.
	configDir := filepath.Join(tmpHome, ".config", "harnesscli")
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Skip("config dir already exists, skipping")
	}

	cfg := &config.Config{StarredModels: []string{"gpt-4.1"}}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Directory should now exist.
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		t.Error("Save() did not create the config directory")
	}
}

// TestConfig_SaveFileMode0600 verifies the saved config file has mode 0600.
func TestConfig_SaveFileMode0600(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg := &config.Config{StarredModels: []string{"gpt-4.1"}}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	configFile := filepath.Join(tmpHome, ".config", "harnesscli", "config.json")
	info, err := os.Stat(configFile)
	if err != nil {
		t.Fatalf("Stat config file: %v", err)
	}

	// Mode should be 0600.
	got := info.Mode().Perm()
	if got != 0o600 {
		t.Errorf("config file mode = %o, want %o", got, 0o600)
	}
}

// TestGatewayRoundTrip verifies the Gateway field round-trips through Save/Load.
func TestGatewayRoundTrip(t *testing.T) {
	t.Run("openrouter", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		original := &config.Config{Gateway: "openrouter"}
		if err := config.Save(original); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}

		loaded, err := config.Load()
		if err != nil {
			t.Fatalf("Load() after Save() failed: %v", err)
		}
		if loaded.Gateway != "openrouter" {
			t.Errorf("Gateway = %q, want %q", loaded.Gateway, "openrouter")
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		original := &config.Config{Gateway: ""}
		if err := config.Save(original); err != nil {
			t.Fatalf("Save() failed: %v", err)
		}

		loaded, err := config.Load()
		if err != nil {
			t.Fatalf("Load() after Save() failed: %v", err)
		}
		if loaded.Gateway != "" {
			t.Errorf("Gateway = %q, want %q", loaded.Gateway, "")
		}
	})
}

// TestAPIKeysRoundTrip verifies the APIKeys field round-trips through Save/Load.
func TestAPIKeysRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	original := &config.Config{
		APIKeys: map[string]string{
			"openai": "sk-test-123",
			"groq":   "gsk-test-456",
		},
	}
	if err := config.Save(original); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}

	if len(loaded.APIKeys) != len(original.APIKeys) {
		t.Fatalf("APIKeys length: got %d, want %d", len(loaded.APIKeys), len(original.APIKeys))
	}
	for k, v := range original.APIKeys {
		if loaded.APIKeys[k] != v {
			t.Errorf("APIKeys[%q] = %q, want %q", k, loaded.APIKeys[k], v)
		}
	}
}

// TestConfig_EmptyStarredModelsRoundTrip verifies empty starred models round-trips correctly.
func TestConfig_EmptyStarredModelsRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	original := &config.Config{}
	if err := config.Save(original); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}

	if len(loaded.StarredModels) != 0 {
		t.Errorf("StarredModels = %v, want empty slice", loaded.StarredModels)
	}
}

// TestConfig_ThemeRoundTrip verifies the Theme field persists through
// Save+Load and that an omitted theme loads back empty (epic #810 slice 4).
func TestConfig_ThemeRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := config.Save(&config.Config{Theme: "ocean"}); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}
	if loaded.Theme != "ocean" {
		t.Errorf("Theme = %q, want %q", loaded.Theme, "ocean")
	}

	// A config written without a theme must load with Theme == "".
	if err := config.Save(&config.Config{StarredModels: []string{"gpt-5"}}); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	loaded, err = config.Load()
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}
	if loaded.Theme != "" {
		t.Errorf("Theme = %q, want empty when omitted", loaded.Theme)
	}
}
