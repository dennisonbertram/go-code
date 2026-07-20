package config

import (
	"maps"
	"slices"
)

// ReloadClass classifies how a Config field may change on a live reload.
type ReloadClass int

const (
	// ReloadHotSwappable fields take effect for subsequent runs when a
	// reload is triggered; no daemon restart is required.
	ReloadHotSwappable ReloadClass = iota
	// ReloadRestartOnly fields are wired once at startup (listen sockets,
	// persistence handles, MCP server processes). A reload never applies
	// them; it reports them as requiring a restart instead.
	ReloadRestartOnly
)

// String returns a human-readable name for the reload class.
func (c ReloadClass) String() string {
	switch c {
	case ReloadHotSwappable:
		return "hot-swappable"
	case ReloadRestartOnly:
		return "restart-only"
	default:
		return "unknown"
	}
}

// FieldClassification pairs a dotted TOML field path (e.g.
// "memory.db_driver") with its reload class.
type FieldClassification struct {
	Path  string
	Class ReloadClass
}

// reloadField is one row of the classification table: the dotted TOML path,
// the reload class, and an equality probe used by ReloadDiff.
type reloadField struct {
	path  string
	class ReloadClass
	equal func(a, b Config) bool
}

// reloadFields is the single authoritative classification of every Config
// field. The order is stable and defines the order of entries in a
// ReloadReport. Keep it in sync with the Config struct — the exhaustiveness
// test in reload_test.go fails if any field is missing or duplicated.
//
// Restart-only rationale:
//   - addr: the HTTP listen socket is bound once at startup.
//   - memory.db_driver / memory.db_dsn / memory.sqlite_path: persistence
//     handles are opened once at startup.
//   - mcp_servers: MCP server processes and the tool registry built from
//     them are wired once at startup.
//
// Everything else is a per-run or runtime policy knob (model, step and cost
// ceilings, auto-compaction, memory toggles and thresholds, forensics flags,
// conclusion watcher, hooks, cron timing) and is hot-swappable.
var reloadFields = []reloadField{
	{"model", ReloadHotSwappable, func(a, b Config) bool { return a.Model == b.Model }},
	{"max_steps", ReloadHotSwappable, func(a, b Config) bool { return a.MaxSteps == b.MaxSteps }},
	{"addr", ReloadRestartOnly, func(a, b Config) bool { return a.Addr == b.Addr }},

	{"cost.max_per_run_usd", ReloadHotSwappable, func(a, b Config) bool { return a.Cost.MaxPerRunUSD == b.Cost.MaxPerRunUSD }},

	{"memory.enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.Enabled == b.Memory.Enabled }},
	{"memory.mode", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.Mode == b.Memory.Mode }},
	{"memory.db_driver", ReloadRestartOnly, func(a, b Config) bool { return a.Memory.DBDriver == b.Memory.DBDriver }},
	{"memory.db_dsn", ReloadRestartOnly, func(a, b Config) bool { return a.Memory.DBDSN == b.Memory.DBDSN }},
	{"memory.sqlite_path", ReloadRestartOnly, func(a, b Config) bool { return a.Memory.SQLitePath == b.Memory.SQLitePath }},
	{"memory.default_enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.DefaultEnabled == b.Memory.DefaultEnabled }},
	{"memory.observe_min_tokens", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.ObserveMinTokens == b.Memory.ObserveMinTokens }},
	{"memory.snippet_max_tokens", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.SnippetMaxTokens == b.Memory.SnippetMaxTokens }},
	{"memory.reflect_threshold_tokens", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.ReflectThresholdTokens == b.Memory.ReflectThresholdTokens }},
	{"memory.llm_mode", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.LLMMode == b.Memory.LLMMode }},
	{"memory.llm_provider", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.LLMProvider == b.Memory.LLMProvider }},
	{"memory.llm_model", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.LLMModel == b.Memory.LLMModel }},
	{"memory.llm_base_url", ReloadHotSwappable, func(a, b Config) bool { return a.Memory.LLMBaseURL == b.Memory.LLMBaseURL }},

	{"auto_compact.enabled", ReloadHotSwappable, func(a, b Config) bool { return a.AutoCompact.Enabled == b.AutoCompact.Enabled }},
	{"auto_compact.mode", ReloadHotSwappable, func(a, b Config) bool { return a.AutoCompact.Mode == b.AutoCompact.Mode }},
	{"auto_compact.threshold", ReloadHotSwappable, func(a, b Config) bool { return a.AutoCompact.Threshold == b.AutoCompact.Threshold }},
	{"auto_compact.keep_last", ReloadHotSwappable, func(a, b Config) bool { return a.AutoCompact.KeepLast == b.AutoCompact.KeepLast }},
	{"auto_compact.model_context_window", ReloadHotSwappable, func(a, b Config) bool { return a.AutoCompact.ModelContextWindow == b.AutoCompact.ModelContextWindow }},

	{"forensics.trace_tool_decisions", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.TraceToolDecisions == b.Forensics.TraceToolDecisions }},
	{"forensics.detect_anti_patterns", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.DetectAntiPatterns == b.Forensics.DetectAntiPatterns }},
	{"forensics.trace_hook_mutations", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.TraceHookMutations == b.Forensics.TraceHookMutations }},
	{"forensics.capture_request_envelope", ReloadHotSwappable, func(a, b Config) bool {
		return a.Forensics.CaptureRequestEnvelope == b.Forensics.CaptureRequestEnvelope
	}},
	{"forensics.snapshot_memory_snippet", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.SnapshotMemorySnippet == b.Forensics.SnapshotMemorySnippet }},
	{"forensics.error_chain_enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.ErrorChainEnabled == b.Forensics.ErrorChainEnabled }},
	{"forensics.error_context_depth", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.ErrorContextDepth == b.Forensics.ErrorContextDepth }},
	{"forensics.capture_reasoning", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.CaptureReasoning == b.Forensics.CaptureReasoning }},
	{"forensics.cost_anomaly_detection_enabled", ReloadHotSwappable, func(a, b Config) bool {
		return a.Forensics.CostAnomalyDetectionEnabled == b.Forensics.CostAnomalyDetectionEnabled
	}},
	{"forensics.cost_anomaly_step_multiplier", ReloadHotSwappable, func(a, b Config) bool {
		return a.Forensics.CostAnomalyStepMultiplier == b.Forensics.CostAnomalyStepMultiplier
	}},
	{"forensics.audit_trail_enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.AuditTrailEnabled == b.Forensics.AuditTrailEnabled }},
	{"forensics.context_window_snapshot_enabled", ReloadHotSwappable, func(a, b Config) bool {
		return a.Forensics.ContextWindowSnapshotEnabled == b.Forensics.ContextWindowSnapshotEnabled
	}},
	{"forensics.context_window_warning_threshold", ReloadHotSwappable, func(a, b Config) bool {
		return a.Forensics.ContextWindowWarningThreshold == b.Forensics.ContextWindowWarningThreshold
	}},
	{"forensics.causal_graph_enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.CausalGraphEnabled == b.Forensics.CausalGraphEnabled }},
	{"forensics.rollout_dir", ReloadHotSwappable, func(a, b Config) bool { return a.Forensics.RolloutDir == b.Forensics.RolloutDir }},

	{"conclusion_watcher.enabled", ReloadHotSwappable, func(a, b Config) bool { return a.ConclusionWatcher.Enabled == b.ConclusionWatcher.Enabled }},
	{"conclusion_watcher.intervention_mode", ReloadHotSwappable, func(a, b Config) bool {
		return a.ConclusionWatcher.InterventionMode == b.ConclusionWatcher.InterventionMode
	}},
	{"conclusion_watcher.evaluator_enabled", ReloadHotSwappable, func(a, b Config) bool {
		return a.ConclusionWatcher.EvaluatorEnabled == b.ConclusionWatcher.EvaluatorEnabled
	}},
	{"conclusion_watcher.evaluator_model", ReloadHotSwappable, func(a, b Config) bool {
		return a.ConclusionWatcher.EvaluatorModel == b.ConclusionWatcher.EvaluatorModel
	}},
	{"conclusion_watcher.evaluator_api_key", ReloadHotSwappable, func(a, b Config) bool {
		return a.ConclusionWatcher.EvaluatorAPIKey == b.ConclusionWatcher.EvaluatorAPIKey
	}},

	{"hooks.enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Hooks.Enabled == b.Hooks.Enabled }},
	{"hooks.dirs", ReloadHotSwappable, func(a, b Config) bool { return slices.Equal(a.Hooks.Dirs, b.Hooks.Dirs) }},

	{"cron.jitter_enabled", ReloadHotSwappable, func(a, b Config) bool { return a.Cron.JitterEnabled == b.Cron.JitterEnabled }},
	{"cron.jitter_min_sec", ReloadHotSwappable, func(a, b Config) bool { return a.Cron.JitterMinSec == b.Cron.JitterMinSec }},
	{"cron.jitter_max_sec", ReloadHotSwappable, func(a, b Config) bool { return a.Cron.JitterMaxSec == b.Cron.JitterMaxSec }},
	{"cron.avoid_minute_marks", ReloadHotSwappable, func(a, b Config) bool { return slices.Equal(a.Cron.AvoidMinuteMarks, b.Cron.AvoidMinuteMarks) }},
	{"cron.log_jittered_times", ReloadHotSwappable, func(a, b Config) bool { return a.Cron.LogJitteredTimes == b.Cron.LogJitteredTimes }},

	{"mcp_servers", ReloadRestartOnly, func(a, b Config) bool { return maps.EqualFunc(a.MCPServers, b.MCPServers, mcpServerConfigEqual) }},
}

// mcpServerConfigEqual compares two MCP server configs field by field;
// maps.Equal cannot be used because Args makes MCPServerConfig non-comparable.
func mcpServerConfigEqual(a, b MCPServerConfig) bool {
	return a.Transport == b.Transport &&
		a.Command == b.Command &&
		a.URL == b.URL &&
		slices.Equal(a.Args, b.Args)
}

// ReloadReport describes the difference between two loaded configs, split by
// reload class. Applied lists hot-swappable fields that changed and may take
// effect for subsequent runs; RestartRequired lists restart-only fields that
// changed but can only take effect after a daemon restart. Both lists follow
// the stable order of the classification table.
type ReloadReport struct {
	Applied         []string
	RestartRequired []string
}

// Changed reports whether the diff found any field change at all.
func (r ReloadReport) Changed() bool {
	return len(r.Applied) > 0 || len(r.RestartRequired) > 0
}

// NeedsRestart reports whether the diff found restart-only changes.
func (r ReloadReport) NeedsRestart() bool {
	return len(r.RestartRequired) > 0
}

// ReloadDiff compares old and new field by field per the classification
// table and returns the resulting report. It is a pure function: it never
// mutates either config and performs no I/O.
func ReloadDiff(old, new Config) ReloadReport {
	var report ReloadReport
	for _, f := range reloadFields {
		if f.equal(old, new) {
			continue
		}
		if f.class == ReloadRestartOnly {
			report.RestartRequired = append(report.RestartRequired, f.path)
		} else {
			report.Applied = append(report.Applied, f.path)
		}
	}
	return report
}

// ReloadClassification returns a copy of the authoritative classification
// table in stable order, for documentation, validation, and tests.
func ReloadClassification() []FieldClassification {
	out := make([]FieldClassification, len(reloadFields))
	for i, f := range reloadFields {
		out[i] = FieldClassification{Path: f.path, Class: f.class}
	}
	return out
}
