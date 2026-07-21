package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds persistent CLI configuration.
type Config struct {
	StarredModels  []string          `json:"starred_models,omitempty"`
	Gateway        string            `json:"gateway,omitempty"` // "" = direct, "openrouter" = OpenRouter
	APIKeys        map[string]string `json:"api_keys,omitempty"`
	HistoryEntries []string          `json:"history_entries,omitempty"` // newest-first command history
	Theme          string            `json:"theme,omitempty"`           // selected color theme name (epic #810)
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "harnesscli", "config.json"), nil
}

// Load reads config from ~/.config/harnesscli/config.json.
// Returns empty Config if the file doesn't exist yet.
func Load() (*Config, error) {
	p, err := configPath()
	if err != nil {
		return &Config{}, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return &Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}, err
	}
	return &cfg, nil
}

// Save writes cfg to ~/.config/harnesscli/config.json.
func Save(cfg *Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
