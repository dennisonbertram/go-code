package symphd

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds the runtime configuration for symphd.
type Config struct {
	Addr                string `yaml:"addr"`
	WorkspaceType       string `yaml:"workspace_type"`
	MaxConcurrentAgents int    `yaml:"max_concurrent_agents"`
	PollIntervalMs      int    `yaml:"poll_interval_ms"`
	// HarnessURL is the HTTP endpoint of the harnessd instance.
	// Only used for "local" and "worktree" workspace types where the URL is
	// static. Ignored for "container" and "vm" types — the URL is derived
	// from the provisioned workspace.
	HarnessURL string `yaml:"harness_url"`
	BaseDir    string `yaml:"base_dir"`

	// PoolSize is the number of pre-provisioned workspaces maintained in pool
	// mode. Only used when WorkspaceType is "pool". Default: 3.
	PoolSize int `yaml:"pool_size"`

	// PoolWorkspaceType is the inner workspace type used for pool entries.
	// Only used when WorkspaceType is "pool". Default: "container".
	PoolWorkspaceType string `yaml:"pool_workspace_type"`

	// RepoURL is an optional git repository URL passed to workspace.Options.RepoURL.
	RepoURL string `yaml:"repo_url"`

	// GitHub issue tracker fields.
	GitHubOwner string `yaml:"github_owner"`
	GitHubRepo  string `yaml:"github_repo"`
	TrackLabel  string `yaml:"track_label"`  // default: "symphd"
	GitHubToken string `yaml:"github_token"` // falls back to GITHUB_TOKEN env var

	// Retry / backoff configuration.
	RetryMaxAttempts int `yaml:"retry_max_attempts"`  // default: 5
	RetryBaseDelayMs int `yaml:"retry_base_delay_ms"` // default: 10000
	RetryMaxDelayMs  int `yaml:"retry_max_delay_ms"`  // default: 300000
}

// Load reads and parses a YAML config file, applying defaults to unset fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("symphd: read config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("symphd: parse config: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

// DefaultConfig returns a Config with all defaults applied.
func DefaultConfig() *Config {
	cfg := &Config{}
	cfg.applyDefaults()
	return cfg
}

func (c *Config) applyDefaults() {
	if c.Addr == "" {
		c.Addr = ":8888"
	}
	if c.WorkspaceType == "" {
		c.WorkspaceType = "local"
	}
	if c.MaxConcurrentAgents == 0 {
		c.MaxConcurrentAgents = 10
	}
	if c.PollIntervalMs == 0 {
		c.PollIntervalMs = 5000
	}
	if c.HarnessURL == "" {
		c.HarnessURL = "http://localhost:8080"
	}
	if c.BaseDir == "" {
		c.BaseDir = filepath.Join(os.TempDir(), "symphd")
	}
	if c.TrackLabel == "" {
		c.TrackLabel = "symphd"
	}
	if c.GitHubToken == "" {
		c.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}
	if c.RetryMaxAttempts == 0 {
		c.RetryMaxAttempts = 5
	}
	if c.RetryBaseDelayMs == 0 {
		c.RetryBaseDelayMs = 10000
	}
	if c.RetryMaxDelayMs == 0 {
		c.RetryMaxDelayMs = 300000
	}
	if c.PoolSize == 0 {
		c.PoolSize = 3
	}
	if c.PoolWorkspaceType == "" {
		c.PoolWorkspaceType = "container"
	}
}
