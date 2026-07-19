// Package config provides a 6-layer configuration cascade for the agent harness.
//
// Layer priority (lowest to highest):
//  1. Built-in defaults (hardcoded)
//  2. User global config: ~/.harness/config.toml
//  3. Project config: .harness/config.toml in workspace root
//  4. Named profile: ~/.harness/profiles/<name>.toml
//  5. CLI/env overrides: HARNESS_* environment variables
//  6. Cloud/team constraints (future stub — not yet applied)
//
// Reload classification: every Config field is classified as hot-swappable
// (takes effect on a live reload for subsequent runs) or restart-only
// (wired once at startup; a reload reports it but never applies it). The
// authoritative table lives in reload.go (ReloadClassification) and
// ReloadDiff reports field changes split by class.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// CostConfig holds per-run cost ceiling configuration.
type CostConfig struct {
	// MaxPerRunUSD is the maximum spend per run in USD. 0 means unlimited.
	MaxPerRunUSD float64 `toml:"max_per_run_usd"`
}

// MemoryConfig holds memory feature configuration.
type MemoryConfig struct {
	// Enabled controls whether observational memory is active by default.
	Enabled bool `toml:"enabled"`
	// Mode controls how observational memory is managed at runtime.
	Mode string `toml:"mode"`
	// DBDriver selects the persistence backend (sqlite or postgres).
	DBDriver string `toml:"db_driver"`
	// DBDSN is the DSN for postgres mode.
	DBDSN string `toml:"db_dsn"`
	// SQLitePath is the on-disk sqlite path for local mode.
	SQLitePath string `toml:"sqlite_path"`
	// DefaultEnabled controls whether new scopes start with memory enabled.
	DefaultEnabled bool `toml:"default_enabled"`
	// ObserveMinTokens is the minimum token threshold before observe runs.
	ObserveMinTokens int `toml:"observe_min_tokens"`
	// SnippetMaxTokens caps the injected snippet size.
	SnippetMaxTokens int `toml:"snippet_max_tokens"`
	// ReflectThresholdTokens is the threshold for reflection compaction.
	ReflectThresholdTokens int `toml:"reflect_threshold_tokens"`
	// LLMMode controls how the memory LLM is chosen: inherit, openai, or provider.
	LLMMode string `toml:"llm_mode"`
	// LLMProvider is the provider key used when llm_mode=provider.
	LLMProvider string `toml:"llm_provider"`
	// LLMModel is the model override for dedicated memory generation.
	LLMModel string `toml:"llm_model"`
	// LLMBaseURL overrides the base URL for the legacy openai-compatible mode.
	LLMBaseURL string `toml:"llm_base_url"`
}

// AutoCompactConfig holds auto-compaction feature configuration.
type AutoCompactConfig struct {
	// Enabled controls whether proactive context auto-compaction is active.
	Enabled bool `toml:"enabled"`
	// Mode is the compaction strategy: "strip", "summarize", or "hybrid".
	Mode string `toml:"mode"`
	// Threshold is the fraction of ModelContextWindow that triggers compaction.
	// For example, 0.80 means 80%. Default is 0.80.
	Threshold float64 `toml:"threshold"`
	// KeepLast is the number of recent turns to preserve during compaction.
	// Default is 8.
	KeepLast int `toml:"keep_last"`
	// ModelContextWindow is the model's context window size in tokens.
	// Default is 128000.
	ModelContextWindow int `toml:"model_context_window"`
}

// ConclusionWatcherConfig controls the conclusion-jumping detector plugin.
type ConclusionWatcherConfig struct {
	Enabled bool `toml:"enabled"`
	// InterventionMode: "inject_validation_prompt" | "pause_for_user" | "request_critique"
	InterventionMode string `toml:"intervention_mode"`
	// EvaluatorEnabled enables the gpt-4o-mini LLM evaluator alongside phrase matching.
	// When true, LLM result wins on conflict with phrase detectors.
	EvaluatorEnabled bool `toml:"evaluator_enabled"`
	// EvaluatorModel is the model to use for LLM evaluation. Default: "gpt-4o-mini".
	EvaluatorModel string `toml:"evaluator_model"`
	// EvaluatorAPIKey is the OpenAI API key. Defaults to OPENAI_API_KEY env var if empty.
	EvaluatorAPIKey string `toml:"evaluator_api_key"`
}

// ForensicsConfig holds forensic feature flag configuration.
// These flags control detailed observability and audit logging.
type ForensicsConfig struct {
	// TraceToolDecisions enables forensic tool-decision tracing.
	TraceToolDecisions bool `toml:"trace_tool_decisions"`
	// DetectAntiPatterns enables detection of repetitive tool call patterns.
	DetectAntiPatterns bool `toml:"detect_anti_patterns"`
	// TraceHookMutations enables before/after snapshots for pre-tool-use hooks.
	TraceHookMutations bool `toml:"trace_hook_mutations"`
	// CaptureRequestEnvelope enables forensic capture of the LLM request envelope.
	CaptureRequestEnvelope bool `toml:"capture_request_envelope"`
	// SnapshotMemorySnippet controls whether memory snippet text is included
	// verbatim in llm.request.snapshot events.
	SnapshotMemorySnippet bool `toml:"snapshot_memory_snippet"`
	// ErrorChainEnabled enables error context snapshots and chain tracing.
	ErrorChainEnabled bool `toml:"error_chain_enabled"`
	// ErrorContextDepth controls the rolling window size for error context snapshots.
	ErrorContextDepth int `toml:"error_context_depth"`
	// CaptureReasoning controls whether LLM reasoning/thinking text is captured.
	CaptureReasoning bool `toml:"capture_reasoning"`
	// CostAnomalyDetectionEnabled enables cost anomaly detection.
	CostAnomalyDetectionEnabled bool `toml:"cost_anomaly_detection_enabled"`
	// CostAnomalyStepMultiplier is the cost anomaly detection multiplier per step.
	CostAnomalyStepMultiplier float64 `toml:"cost_anomaly_step_multiplier"`
	// AuditTrailEnabled enables the append-only compliance audit log.
	AuditTrailEnabled bool `toml:"audit_trail_enabled"`
	// ContextWindowSnapshotEnabled enables context window snapshot events.
	ContextWindowSnapshotEnabled bool `toml:"context_window_snapshot_enabled"`
	// ContextWindowWarningThreshold is the fraction at which a warning is emitted.
	ContextWindowWarningThreshold float64 `toml:"context_window_warning_threshold"`
	// CausalGraphEnabled enables causal event graph construction.
	CausalGraphEnabled bool `toml:"causal_graph_enabled"`
	// RolloutDir is the root directory for JSONL rollout files.
	RolloutDir string `toml:"rollout_dir"`
}

// HooksConfig holds config-driven lifecycle hook settings.
// Hook files themselves are JSON definitions discovered from
// ~/.harness/hooks/ and <workspace>/.harness/hooks/ — see internal/hooks.
type HooksConfig struct {
	// Enabled controls whether config-driven hooks are discovered and
	// registered at startup. Default true.
	Enabled bool `toml:"enabled"`
	// Dirs lists extra hook discovery directories beyond the two defaults.
	// Extra dirs classify as project-level: files in them require explicit
	// trust before they load.
	Dirs []string `toml:"dirs"`
}

// MCPServerConfig holds the configuration for a single external MCP server.
// The transport field controls how the harness connects to the server.
//
// Example TOML (stdio):
//
//	[mcp_servers.my-tool]
//	transport = "stdio"
//	command = "/usr/local/bin/my-mcp-server"
//	args = ["--verbose"]
//
// Example TOML (http):
//
//	[mcp_servers.remote-tool]
//	transport = "http"
//	url = "http://localhost:3001/mcp"
//
// Example TOML (http with static headers, e.g. bearer auth):
//
//	[mcp_servers.authed-tool]
//	transport = "http"
//	url = "https://mcp.example.com/mcp"
//	[mcp_servers.authed-tool.headers]
//	Authorization = "Bearer <token>"
type MCPServerConfig struct {
	// Transport must be "stdio" or "http".
	Transport string `toml:"transport"`

	// Stdio transport: path or name of the subprocess to launch.
	Command string `toml:"command"`

	// Stdio transport: additional arguments for the subprocess.
	Args []string `toml:"args"`

	// HTTP transport: the MCP server endpoint URL.
	URL string `toml:"url"`

	// HTTP transport: static headers sent with every request, e.g.
	// "Authorization" = "Bearer <token>". Ignored on the stdio transport.
	Headers map[string]string `toml:"headers"`
}

// CronConfig holds cron scheduler configuration.
type CronConfig struct {
	// JitterEnabled controls whether jitter is applied to scheduled task execution times.
	JitterEnabled bool `toml:"jitter_enabled"`
	// JitterMinSec is the minimum jitter offset in seconds.
	JitterMinSec int `toml:"jitter_min_sec"`
	// JitterMaxSec is the maximum jitter offset in seconds.
	JitterMaxSec int `toml:"jitter_max_sec"`
	// AvoidMinuteMarks lists the minute marks (0-59) to avoid landing on.
	AvoidMinuteMarks []int `toml:"avoid_minute_marks"`
	// LogJitteredTimes controls whether jittered execution times are logged.
	LogJitteredTimes bool `toml:"log_jittered_times"`
}

// Config is the merged, resolved configuration for a harness instance.
// It represents the final result after all config layers have been applied.
type Config struct {
	// Model is the LLM model identifier (e.g. "gpt-4.1-mini").
	Model string `toml:"model"`

	// MaxSteps is the maximum number of tool-calling steps per run. 0 = unlimited.
	MaxSteps int `toml:"max_steps"`

	// Addr is the HTTP listen address (e.g. ":8080").
	Addr string `toml:"addr"`

	// Cost holds per-run cost ceiling settings.
	Cost CostConfig `toml:"cost"`

	// Memory holds memory feature settings.
	Memory MemoryConfig `toml:"memory"`

	// AutoCompact holds auto-compaction feature settings.
	AutoCompact AutoCompactConfig `toml:"auto_compact"`

	// Forensics holds forensic feature flag settings.
	Forensics ForensicsConfig `toml:"forensics"`

	// ConclusionWatcher holds conclusion-jumping detector plugin settings.
	ConclusionWatcher ConclusionWatcherConfig `toml:"conclusion_watcher"`

	// Hooks holds config-driven lifecycle hook settings.
	Hooks HooksConfig `toml:"hooks"`

	// Cron holds cron scheduler configuration.
	Cron CronConfig `toml:"cron"`

	// MCPServers is the map of named external MCP server configurations.
	// Keys are the logical server names (used as prefixes for tool names).
	// This field is populated from the [mcp_servers.*] sections in TOML files.
	MCPServers map[string]MCPServerConfig `toml:"mcp_servers"`
}

// Resolve returns the config itself. It exists to satisfy the issue requirement
// that Config.Resolve() returns the merged config, and to serve as an extension
// point for future cloud/team constraint application (layer 6).
func (c Config) Resolve() Config {
	return c
}

// Defaults returns the built-in default configuration (layer 1).
func Defaults() Config {
	return Config{
		Model:    "gpt-4.1-mini",
		MaxSteps: 0,
		Addr:     ":8080",
		Cost: CostConfig{
			MaxPerRunUSD: 0.0,
		},
		Memory: MemoryConfig{
			Enabled:                true,
			Mode:                   "auto",
			DBDriver:               "sqlite",
			SQLitePath:             ".harness/state.db",
			DefaultEnabled:         false,
			ObserveMinTokens:       1200,
			SnippetMaxTokens:       900,
			ReflectThresholdTokens: 4000,
		},
		ConclusionWatcher: ConclusionWatcherConfig{
			Enabled:          false,
			InterventionMode: "inject_validation_prompt",
			EvaluatorEnabled: false,
			EvaluatorModel:   "gpt-4o-mini",
		},
		Hooks: HooksConfig{
			Enabled: true,
		},
		Cron: CronConfig{
			JitterEnabled:    true,
			JitterMinSec:     60,
			JitterMaxSec:     300,
			AvoidMinuteMarks: []int{0, 30},
			LogJitteredTimes: true,
		},
	}
}

// LoadOptions controls how Load() sources configuration layers.
// Fields that are empty string are treated as "not configured" and
// the corresponding layer is skipped.
type LoadOptions struct {
	// UserConfigPath is the path to the user global config file
	// (typically ~/.harness/config.toml). If empty, the layer is skipped.
	UserConfigPath string

	// ProjectConfigPath is the path to the project config file
	// (typically .harness/config.toml in workspace root). If empty, skipped.
	ProjectConfigPath string

	// ProfilesDir is the directory that contains named profile files
	// (typically ~/.harness/profiles/). Required when ProfileName is non-empty.
	ProfilesDir string

	// ProfileName is the name of the profile to load (without ".toml" suffix).
	// If empty, the profile layer is skipped. Names with path separators or
	// absolute path components are rejected to prevent path traversal.
	ProfileName string

	// Getenv is the function used to read environment variables. If nil,
	// os.Getenv is used. Inject a custom function in tests.
	Getenv func(string) string
}

// rawLayer is the TOML-decoded partial configuration from a single config file.
// Pointer fields distinguish "not set" from "set to zero value", enabling
// correct layered merging where only non-zero fields override lower layers.
// MCPServers uses a plain map because absent keys are naturally nil/missing.
type rawLayer struct {
	Model             *string                    `toml:"model"`
	MaxSteps          *int                       `toml:"max_steps"`
	Addr              *string                    `toml:"addr"`
	Cost              *rawCost                   `toml:"cost"`
	Memory            *rawMemory                 `toml:"memory"`
	AutoCompact       *rawAutoCompact            `toml:"auto_compact"`
	Forensics         *rawForensics              `toml:"forensics"`
	ConclusionWatcher *rawConclusionWatcher      `toml:"conclusion_watcher"`
	Hooks             *rawHooks                  `toml:"hooks"`
	Cron              *rawCron                   `toml:"cron"`
	MCPServers        map[string]MCPServerConfig `toml:"mcp_servers"`
}

type rawCost struct {
	MaxPerRunUSD *float64 `toml:"max_per_run_usd"`
}

type rawMemory struct {
	Enabled                *bool   `toml:"enabled"`
	Mode                   *string `toml:"mode"`
	DBDriver               *string `toml:"db_driver"`
	DBDSN                  *string `toml:"db_dsn"`
	SQLitePath             *string `toml:"sqlite_path"`
	DefaultEnabled         *bool   `toml:"default_enabled"`
	ObserveMinTokens       *int    `toml:"observe_min_tokens"`
	SnippetMaxTokens       *int    `toml:"snippet_max_tokens"`
	ReflectThresholdTokens *int    `toml:"reflect_threshold_tokens"`
	LLMMode                *string `toml:"llm_mode"`
	LLMProvider            *string `toml:"llm_provider"`
	LLMModel               *string `toml:"llm_model"`
	LLMBaseURL             *string `toml:"llm_base_url"`
}

type rawAutoCompact struct {
	Enabled            *bool    `toml:"enabled"`
	Mode               *string  `toml:"mode"`
	Threshold          *float64 `toml:"threshold"`
	KeepLast           *int     `toml:"keep_last"`
	ModelContextWindow *int     `toml:"model_context_window"`
}

type rawForensics struct {
	TraceToolDecisions            *bool    `toml:"trace_tool_decisions"`
	DetectAntiPatterns            *bool    `toml:"detect_anti_patterns"`
	TraceHookMutations            *bool    `toml:"trace_hook_mutations"`
	CaptureRequestEnvelope        *bool    `toml:"capture_request_envelope"`
	SnapshotMemorySnippet         *bool    `toml:"snapshot_memory_snippet"`
	ErrorChainEnabled             *bool    `toml:"error_chain_enabled"`
	ErrorContextDepth             *int     `toml:"error_context_depth"`
	CaptureReasoning              *bool    `toml:"capture_reasoning"`
	CostAnomalyDetectionEnabled   *bool    `toml:"cost_anomaly_detection_enabled"`
	CostAnomalyStepMultiplier     *float64 `toml:"cost_anomaly_step_multiplier"`
	AuditTrailEnabled             *bool    `toml:"audit_trail_enabled"`
	ContextWindowSnapshotEnabled  *bool    `toml:"context_window_snapshot_enabled"`
	ContextWindowWarningThreshold *float64 `toml:"context_window_warning_threshold"`
	CausalGraphEnabled            *bool    `toml:"causal_graph_enabled"`
	RolloutDir                    *string  `toml:"rollout_dir"`
}

type rawConclusionWatcher struct {
	Enabled          *bool   `toml:"enabled"`
	InterventionMode *string `toml:"intervention_mode"`
	EvaluatorEnabled *bool   `toml:"evaluator_enabled"`
	EvaluatorModel   *string `toml:"evaluator_model"`
	EvaluatorAPIKey  *string `toml:"evaluator_api_key"`
}

type rawHooks struct {
	Enabled *bool    `toml:"enabled"`
	Dirs    []string `toml:"dirs"`
}

type rawCron struct {
	JitterEnabled    *bool `toml:"jitter_enabled"`
	JitterMinSec     *int  `toml:"jitter_min_sec"`
	JitterMaxSec     *int  `toml:"jitter_max_sec"`
	AvoidMinuteMarks []int `toml:"avoid_minute_marks"`
	LogJitteredTimes *bool `toml:"log_jittered_times"`
}

// Load builds the merged Config by walking through all layers in priority
// order. Each successive layer overrides only the fields it explicitly sets.
func Load(opts LoadOptions) (Config, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	// Start from built-in defaults (layer 1).
	cfg := Defaults()

	// Layer 2: user global config.
	if opts.UserConfigPath != "" {
		layer, err := loadTOMLFile(opts.UserConfigPath)
		if err != nil {
			return Config{}, fmt.Errorf("user config %s: %w", opts.UserConfigPath, err)
		}
		applyLayer(&cfg, layer)
	}

	// Layer 3: project config.
	if opts.ProjectConfigPath != "" {
		layer, err := loadTOMLFile(opts.ProjectConfigPath)
		if err != nil {
			return Config{}, fmt.Errorf("project config %s: %w", opts.ProjectConfigPath, err)
		}
		applyLayer(&cfg, layer)
	}

	// Layer 4: named profile.
	if opts.ProfileName != "" {
		if err := validateProfileName(opts.ProfileName); err != nil {
			return Config{}, err
		}
		profilePath := filepath.Join(opts.ProfilesDir, opts.ProfileName+".toml")
		// A named profile that doesn't exist is always an error — the user
		// explicitly requested it, so a missing file is not silently skipped.
		if _, statErr := os.Stat(profilePath); os.IsNotExist(statErr) {
			return Config{}, fmt.Errorf("profile %q not found at %s", opts.ProfileName, profilePath)
		}
		layer, err := loadTOMLFile(profilePath)
		if err != nil {
			return Config{}, fmt.Errorf("profile %s: %w", profilePath, err)
		}
		applyLayer(&cfg, layer)
	}

	// Layer 5: HARNESS_* environment variables.
	applyEnvLayer(&cfg, getenv)

	// Layer 6 (cloud/team constraints): stub — not yet implemented.

	return cfg, nil
}

// loadTOMLFile loads a single TOML config file into a rawLayer.
// Returns the zero-value rawLayer (no overrides) if the file does not exist.
// Returns an error for any other I/O or parse failure.
func loadTOMLFile(path string) (rawLayer, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return rawLayer{}, nil // layer is simply absent — not an error
		}
		return rawLayer{}, err
	}
	defer f.Close()

	var layer rawLayer
	if _, err := toml.NewDecoder(f).Decode(&layer); err != nil {
		return rawLayer{}, err
	}
	return layer, nil
}

// applyLayer merges non-nil fields from layer into cfg.
// MCPServers are merged additively: entries in layer are added to (or
// override) any existing entries in cfg. Servers absent from layer are
// preserved unchanged from lower layers.
func applyLayer(cfg *Config, layer rawLayer) {
	if layer.Model != nil {
		cfg.Model = *layer.Model
	}
	if layer.MaxSteps != nil {
		cfg.MaxSteps = *layer.MaxSteps
	}
	if layer.Addr != nil {
		cfg.Addr = *layer.Addr
	}
	if layer.Cost != nil {
		if layer.Cost.MaxPerRunUSD != nil {
			cfg.Cost.MaxPerRunUSD = *layer.Cost.MaxPerRunUSD
		}
	}
	if layer.Memory != nil {
		m := layer.Memory
		if m.Enabled != nil {
			cfg.Memory.Enabled = *m.Enabled
		}
		if m.Mode != nil {
			cfg.Memory.Mode = *m.Mode
		}
		if m.DBDriver != nil {
			cfg.Memory.DBDriver = *m.DBDriver
		}
		if m.DBDSN != nil {
			cfg.Memory.DBDSN = *m.DBDSN
		}
		if m.SQLitePath != nil {
			cfg.Memory.SQLitePath = *m.SQLitePath
		}
		if m.DefaultEnabled != nil {
			cfg.Memory.DefaultEnabled = *m.DefaultEnabled
		}
		if m.ObserveMinTokens != nil {
			cfg.Memory.ObserveMinTokens = *m.ObserveMinTokens
		}
		if m.SnippetMaxTokens != nil {
			cfg.Memory.SnippetMaxTokens = *m.SnippetMaxTokens
		}
		if m.ReflectThresholdTokens != nil {
			cfg.Memory.ReflectThresholdTokens = *m.ReflectThresholdTokens
		}
		if m.LLMMode != nil {
			cfg.Memory.LLMMode = *m.LLMMode
		}
		if m.LLMProvider != nil {
			cfg.Memory.LLMProvider = *m.LLMProvider
		}
		if m.LLMModel != nil {
			cfg.Memory.LLMModel = *m.LLMModel
		}
		if m.LLMBaseURL != nil {
			cfg.Memory.LLMBaseURL = *m.LLMBaseURL
		}
	}
	if layer.AutoCompact != nil {
		ac := layer.AutoCompact
		if ac.Enabled != nil {
			cfg.AutoCompact.Enabled = *ac.Enabled
		}
		if ac.Mode != nil {
			cfg.AutoCompact.Mode = *ac.Mode
		}
		if ac.Threshold != nil {
			cfg.AutoCompact.Threshold = *ac.Threshold
		}
		if ac.KeepLast != nil {
			cfg.AutoCompact.KeepLast = *ac.KeepLast
		}
		if ac.ModelContextWindow != nil {
			cfg.AutoCompact.ModelContextWindow = *ac.ModelContextWindow
		}
	}
	if layer.Forensics != nil {
		f := layer.Forensics
		if f.TraceToolDecisions != nil {
			cfg.Forensics.TraceToolDecisions = *f.TraceToolDecisions
		}
		if f.DetectAntiPatterns != nil {
			cfg.Forensics.DetectAntiPatterns = *f.DetectAntiPatterns
		}
		if f.TraceHookMutations != nil {
			cfg.Forensics.TraceHookMutations = *f.TraceHookMutations
		}
		if f.CaptureRequestEnvelope != nil {
			cfg.Forensics.CaptureRequestEnvelope = *f.CaptureRequestEnvelope
		}
		if f.SnapshotMemorySnippet != nil {
			cfg.Forensics.SnapshotMemorySnippet = *f.SnapshotMemorySnippet
		}
		if f.ErrorChainEnabled != nil {
			cfg.Forensics.ErrorChainEnabled = *f.ErrorChainEnabled
		}
		if f.ErrorContextDepth != nil {
			cfg.Forensics.ErrorContextDepth = *f.ErrorContextDepth
		}
		if f.CaptureReasoning != nil {
			cfg.Forensics.CaptureReasoning = *f.CaptureReasoning
		}
		if f.CostAnomalyDetectionEnabled != nil {
			cfg.Forensics.CostAnomalyDetectionEnabled = *f.CostAnomalyDetectionEnabled
		}
		if f.CostAnomalyStepMultiplier != nil {
			cfg.Forensics.CostAnomalyStepMultiplier = *f.CostAnomalyStepMultiplier
		}
		if f.AuditTrailEnabled != nil {
			cfg.Forensics.AuditTrailEnabled = *f.AuditTrailEnabled
		}
		if f.ContextWindowSnapshotEnabled != nil {
			cfg.Forensics.ContextWindowSnapshotEnabled = *f.ContextWindowSnapshotEnabled
		}
		if f.ContextWindowWarningThreshold != nil {
			cfg.Forensics.ContextWindowWarningThreshold = *f.ContextWindowWarningThreshold
		}
		if f.CausalGraphEnabled != nil {
			cfg.Forensics.CausalGraphEnabled = *f.CausalGraphEnabled
		}
		if f.RolloutDir != nil {
			cfg.Forensics.RolloutDir = *f.RolloutDir
		}
	}
	if layer.ConclusionWatcher != nil {
		cw := layer.ConclusionWatcher
		if cw.Enabled != nil {
			cfg.ConclusionWatcher.Enabled = *cw.Enabled
		}
		if cw.InterventionMode != nil {
			cfg.ConclusionWatcher.InterventionMode = *cw.InterventionMode
		}
		if cw.EvaluatorEnabled != nil {
			cfg.ConclusionWatcher.EvaluatorEnabled = *cw.EvaluatorEnabled
		}
		if cw.EvaluatorModel != nil {
			cfg.ConclusionWatcher.EvaluatorModel = *cw.EvaluatorModel
		}
		if cw.EvaluatorAPIKey != nil {
			cfg.ConclusionWatcher.EvaluatorAPIKey = *cw.EvaluatorAPIKey
		}
	}
	if layer.Hooks != nil {
		h := layer.Hooks
		if h.Enabled != nil {
			cfg.Hooks.Enabled = *h.Enabled
		}
		if len(h.Dirs) > 0 {
			cfg.Hooks.Dirs = append([]string(nil), h.Dirs...)
		}
	}
	if layer.Cron != nil {
		c := layer.Cron
		if c.JitterEnabled != nil {
			cfg.Cron.JitterEnabled = *c.JitterEnabled
		}
		if c.JitterMinSec != nil {
			cfg.Cron.JitterMinSec = *c.JitterMinSec
		}
		if c.JitterMaxSec != nil {
			cfg.Cron.JitterMaxSec = *c.JitterMaxSec
		}
		if len(c.AvoidMinuteMarks) > 0 {
			cfg.Cron.AvoidMinuteMarks = append([]int(nil), c.AvoidMinuteMarks...)
		}
		if c.LogJitteredTimes != nil {
			cfg.Cron.LogJitteredTimes = *c.LogJitteredTimes
		}
	}

	if len(layer.MCPServers) > 0 {
		if cfg.MCPServers == nil {
			cfg.MCPServers = make(map[string]MCPServerConfig)
		}
		for name, srv := range layer.MCPServers {
			cfg.MCPServers[name] = srv
		}
	}
}

// applyEnvLayer applies HARNESS_* environment variables as layer 5 overrides.
// Invalid values (e.g. non-numeric HARNESS_MAX_STEPS) are silently ignored,
// preserving the previous layer's value.
func applyEnvLayer(cfg *Config, getenv func(string) string) {
	if v := strings.TrimSpace(getenv("HARNESS_MODEL")); v != "" {
		cfg.Model = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_ADDR")); v != "" {
		cfg.Addr = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MAX_STEPS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxSteps = n
		}
		// invalid value: silently skip, preserve previous layer's value
	}
	if v := strings.TrimSpace(getenv("HARNESS_MAX_COST_PER_RUN_USD")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Cost.MaxPerRunUSD = f
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_MODE")); v != "" {
		cfg.Memory.Mode = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_DB_DRIVER")); v != "" {
		cfg.Memory.DBDriver = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_DB_DSN")); v != "" {
		cfg.Memory.DBDSN = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_SQLITE_PATH")); v != "" {
		cfg.Memory.SQLitePath = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_DEFAULT_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Memory.DefaultEnabled = b
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_OBSERVE_MIN_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Memory.ObserveMinTokens = n
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_SNIPPET_MAX_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Memory.SnippetMaxTokens = n
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_REFLECT_THRESHOLD_TOKENS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Memory.ReflectThresholdTokens = n
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_LLM_MODE")); v != "" {
		cfg.Memory.LLMMode = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_LLM_PROVIDER")); v != "" {
		cfg.Memory.LLMProvider = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_LLM_MODEL")); v != "" {
		cfg.Memory.LLMModel = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_MEMORY_LLM_BASE_URL")); v != "" {
		cfg.Memory.LLMBaseURL = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_CONCLUSION_WATCHER_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.ConclusionWatcher.Enabled = b
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_CONCLUSION_WATCHER_INTERVENTION_MODE")); v != "" {
		cfg.ConclusionWatcher.InterventionMode = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_CONCLUSION_WATCHER_EVALUATOR_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.ConclusionWatcher.EvaluatorEnabled = b
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_CONCLUSION_WATCHER_EVALUATOR_MODEL")); v != "" {
		cfg.ConclusionWatcher.EvaluatorModel = v
	}
	if v := strings.TrimSpace(getenv("HARNESS_CRON_JITTER_ENABLED")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Cron.JitterEnabled = b
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_CRON_JITTER_MIN_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Cron.JitterMinSec = n
		}
	}
	if v := strings.TrimSpace(getenv("HARNESS_CRON_JITTER_MAX_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Cron.JitterMaxSec = n
		}
	}
}

// ValidateProfileName ensures the profile name contains no path separators or
// absolute path components that could cause path traversal attacks.
// It is exported so that external packages (e.g. harness) can validate profile
// names before constructing file paths.
func ValidateProfileName(name string) error {
	return validateProfileName(name)
}

// validateProfileName is the unexported implementation of ValidateProfileName.
func validateProfileName(name string) error {
	// Reject anything with a path separator.
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid profile name %q: must not contain path separators", name)
	}
	// Reject absolute paths that start with / (already caught above but be explicit).
	if filepath.IsAbs(name) {
		return fmt.Errorf("invalid profile name %q: must not be an absolute path", name)
	}
	// Reject names that contain "..".
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid profile name %q: must not contain '..'", name)
	}
	if name == "" {
		return fmt.Errorf("profile name must not be empty")
	}
	return nil
}
