# Plugin System

This document explains how to write a plugin for go-agent-harness. The audience is a Go developer adding a new plugin from scratch.

## What is a plugin

A plugin is a standalone Go package in the `plugins/` directory that attaches behaviour to the runner's step loop without modifying anything inside `internal/harness/`. Plugins are wired in at server startup by appending to the hook slices in `harness.RunnerConfig`.

This is in contrast to **internal forensics packages** (e.g. `internal/forensics/costanomaly/`), which implement self-contained logic but are always compiled into the runner and activated through `RunnerConfig` fields. A forensics package is not a "plugin" in this sense — it does not have a `Register(*harness.RunnerConfig)` function and it does not live in `plugins/`. The distinction matters for import discipline: forensics packages may import freely from within `internal/harness/`; plugins must not.

```
plugins/
    conclusion-watcher/     <-- example plugin (ships with the repo)
    my-plugin/              <-- your new plugin goes here

internal/
    harness/                <-- never modified by a plugin
    forensics/              <-- internal observability packages (not plugins)
```

## The hook model

The runner exposes four hook extension points, each backed by a slice in `RunnerConfig`. Multiple plugins can coexist by appending to the same slice — hooks run in order.

### PreMessageHook

Fires **before** the LLM is called, once per step.

```go
type PreMessageHook interface {
    Name() string
    BeforeMessage(ctx context.Context, in PreMessageHookInput) (PreMessageHookResult, error)
}

type PreMessageHookInput struct {
    RunID   string
    Step    int
    Request CompletionRequest   // the full prompt + tool definitions about to be sent
}

type PreMessageHookResult struct {
    Action         HookAction          // HookActionContinue or HookActionBlock
    Reason         string              // human-readable, used when blocking
    MutatedRequest *CompletionRequest  // non-nil to replace the outgoing request
}
```

Use this hook to inspect or rewrite the prompt before it reaches the LLM — for example, injecting extra system context, logging token counts, or enforcing prompt policies.

### PostMessageHook

Fires **after** the LLM responds (text and/or tool calls), before any tool executes.

```go
type PostMessageHook interface {
    Name() string
    AfterMessage(ctx context.Context, in PostMessageHookInput) (PostMessageHookResult, error)
}

type PostMessageHookInput struct {
    RunID     string
    Step      int
    Request   CompletionRequest   // what was sent
    Response  CompletionResult    // what the LLM returned
    ToolCalls []ToolCall          // parsed tool calls from the response
}

type PostMessageHookResult struct {
    Action          HookAction          // HookActionContinue or HookActionBlock
    Reason          string
    MutatedResponse *CompletionResult   // non-nil to replace the LLM response
}
```

Returning `HookActionBlock` aborts the step: no tool calls execute and the run stops with the reason as the error. This is the right place to detect unsafe or incorrect LLM reasoning before it has any side effects.

### PreToolUseHook

Fires **before each individual tool call** — called once per tool in the step.

```go
type PreToolUseHook interface {
    Name() string
    PreToolUse(ctx context.Context, ev PreToolUseEvent) (*PreToolUseResult, error)
}

type PreToolUseEvent struct {
    ToolName string
    CallID   string          // tool_call_id from the LLM response
    Args     json.RawMessage // raw JSON args from the LLM
    RunID    string
}

type PreToolUseResult struct {
    Decision     ToolHookDecision   // ToolHookAllow (0) or ToolHookDeny
    Reason       string             // shown to the LLM when denied
    ModifiedArgs json.RawMessage    // non-nil to replace args passed to the handler
}
```

Return `nil, nil` to allow the call with no modification — this is the zero-value behaviour. Returning `ToolHookDeny` feeds the `Reason` back to the LLM as a tool error result without executing the handler.

### PostToolUseHook

Fires **after each tool call completes**, even when the handler returned an error.

```go
type PostToolUseHook interface {
    Name() string
    PostToolUse(ctx context.Context, ev PostToolUseEvent) (*PostToolUseResult, error)
}

type PostToolUseEvent struct {
    ToolName string
    CallID   string
    Args     json.RawMessage // args after any PreToolUseHook modifications
    Result   string          // output from the tool handler; empty on error
    Duration time.Duration
    Error    error           // non-nil if the handler failed
    RunID    string
}

type PostToolUseResult struct {
    ModifiedResult string  // non-empty to replace the tool output shown to the LLM
}
```

Return `nil, nil` to pass through the original result unchanged.

### Hook failure mode

When a hook returns a non-nil error, the runner consults `RunnerConfig.HookFailureMode`:

- `HookFailureModeFailClosed` (default): the step is aborted as if the hook had blocked.
- `HookFailureModeFailOpen`: the error is logged and the step continues.

## Plugin structure

A plugin is an ordinary Go package. Recommended layout:

```
plugins/my-plugin/
    plugin.go   -- main struct, Register(*harness.RunnerConfig), hook implementations
    types.go    -- config types, event constants, public types
```

Package name convention: use a single identifier matching the directory name, without hyphens — e.g. `package myplugin` for `plugins/my-plugin/`.

The only required public API is:

```go
func (p *MyPlugin) Register(cfg *harness.RunnerConfig)
```

`Register` appends the plugin's hook implementations to whichever hook slices it needs. It must not replace existing slices — always use `append`. Calling `Register` twice on the same `cfg` will wire the hooks twice.

## Step-by-step: writing a plugin

Below is a minimal compilable plugin that logs every tool call to standard error.

**`plugins/tool-logger/plugin.go`**

```go
package toollogger

import (
    "context"
    "fmt"

    "go-agent-harness/internal/harness"
)

// ToolLogger is a plugin that logs tool calls to a writer.
type ToolLogger struct{}

// New creates a ToolLogger.
func New() *ToolLogger {
    return &ToolLogger{}
}

// Register wires the logger into the runner config.
// Call before constructing the runner.
func (tl *ToolLogger) Register(cfg *harness.RunnerConfig) {
    cfg.PreToolUseHooks = append(cfg.PreToolUseHooks, &preToolHook{})
}

// preToolHook implements harness.PreToolUseHook.
type preToolHook struct{}

func (h *preToolHook) Name() string { return "tool-logger-pre" }

func (h *preToolHook) PreToolUse(
    ctx context.Context,
    ev harness.PreToolUseEvent,
) (*harness.PreToolUseResult, error) {
    fmt.Printf("[tool-logger] run=%s step tool=%s args=%s\n",
        ev.RunID, ev.ToolName, string(ev.Args))
    // Return nil to allow with no modification.
    return nil, nil
}
```

That's the entire plugin. No changes to any file in `internal/harness/`.

To enable it in `cmd/harnessd/main.go`:

```go
import toollogger "go-agent-harness/plugins/tool-logger"

// ... inside runWithSignals, after runnerCfg is populated:
tl := toollogger.New()
tl.Register(&runnerCfg)
```

## Config integration

Add a config struct for your plugin to `internal/config/config.go` and embed it in `Config`. Follow the `ConclusionWatcherConfig` pattern exactly.

**Step 1 — add the config struct:**

```go
// MyPluginConfig holds configuration for the my-plugin plugin.
type MyPluginConfig struct {
    Enabled bool   `toml:"enabled"`
    // ... your fields
    Threshold float64 `toml:"threshold"`
}
```

**Step 2 — embed it in `Config`:**

```go
type Config struct {
    // ... existing fields ...
    MyPlugin MyPluginConfig `toml:"my_plugin"`
}
```

**Step 3 — add defaults in `Defaults()`:**

```go
func Defaults() Config {
    return Config{
        // ... existing defaults ...
        MyPlugin: MyPluginConfig{
            Enabled:   false,
            Threshold: 0.5,
        },
    }
}
```

**Step 4 — add a raw layer type and merge logic:**

```go
type rawMyPlugin struct {
    Enabled   *bool    `toml:"enabled"`
    Threshold *float64 `toml:"threshold"`
}
```

Add `MyPlugin *rawMyPlugin \`toml:"my_plugin"\`` to `rawLayer`, then extend `applyLayer`:

```go
if layer.MyPlugin != nil {
    mp := layer.MyPlugin
    if mp.Enabled != nil {
        cfg.MyPlugin.Enabled = *mp.Enabled
    }
    if mp.Threshold != nil {
        cfg.MyPlugin.Threshold = *mp.Threshold
    }
}
```

**Example TOML section (`~/.harness/config.toml` or `.harness/config.toml`):**

```toml
[my_plugin]
enabled = true
threshold = 0.8
```

## The conclusion-watcher as a reference

`plugins/conclusion-watcher/` is the canonical reference plugin. It detects when an LLM agent makes unjustified conclusions — assertions about code or state that are not supported by prior tool usage — and intervenes before those assertions cause side-effects.

Relevant files:

| File | Purpose |
|------|---------|
| `plugins/conclusion-watcher/types.go` | `WatcherConfig`, `DetectionResult`, `InterventionMode` constants, plugin-scoped event type strings |
| `plugins/conclusion-watcher/plugin.go` | `ConclusionWatcher` struct; `Register(*harness.RunnerConfig)`; three inner hook types (`postMessageHook`, `preToolUseHook`, `postToolUseHook`) |
| `plugins/conclusion-watcher/evaluator.go` | `Evaluator` interface and `OpenAIEvaluator` — a parallel LLM call that validates the main agent's reasoning |

Key design patterns to borrow:

- **Inner hook types**: define unexported `postMessageHook`, `preToolUseHook`, etc. as small structs that hold a pointer to the plugin root. Keep hook logic in `plugin.go`, not spread across files.
- **Parallel LLM evaluation**: `postMessageHook.AfterMessage` launches the evaluator in a goroutine (`go func() { ... ch <- ... }()`) and selects on the result channel and `ctx.Done()`. Use this pattern when your hook needs to make an LLM call without blocking the critical path unconditionally.
- **Observation ledger**: the plugin maintains its own state (which files the agent has read, which tools it has called) across multiple hook firings within a single run. Initialize per-run state in the plugin constructor; pass it to hooks via the root pointer.
- **Event emission via callback**: the plugin never imports the harness event bus. It emits events through the `EventEmitter func(eventType string, runID string, payload map[string]any)` callback in `WatcherConfig`. Event type strings are plain `string` constants defined in `types.go` — never of type `harness.EventType` — so `internal/harness/events.go` and `AllEventTypes()` are never touched.

```go
// Event types are plain strings, NOT harness.EventType.
const (
    EventConclusionDetected   = "conclusion.detected"
    EventConclusionIntervened = "conclusion.intervened"
)
```

## Wiring in harnessd

The full pattern for conditionally enabling a plugin based on config, taken directly from `cmd/harnessd/main.go`:

```go
// Conclusion watcher plugin
if harnessCfg.ConclusionWatcher.Enabled {
    wcfg := conclusionwatcher.WatcherConfig{
        Mode: conclusionwatcher.InterventionMode(harnessCfg.ConclusionWatcher.InterventionMode),
    }
    if harnessCfg.ConclusionWatcher.EvaluatorEnabled {
        cwAPIKey := harnessCfg.ConclusionWatcher.EvaluatorAPIKey
        if cwAPIKey == "" {
            cwAPIKey = os.Getenv("OPENAI_API_KEY")
        }
        eval := conclusionwatcher.NewOpenAIEvaluator(cwAPIKey)
        if harnessCfg.ConclusionWatcher.EvaluatorModel != "" {
            eval.Model = harnessCfg.ConclusionWatcher.EvaluatorModel
        }
        wcfg.Evaluator = eval
    }
    cw := conclusionwatcher.New(wcfg)
    cw.Register(&runnerCfg)
}

runner := harness.NewRunner(provider, tools, runnerCfg)
```

The pattern is always: build config struct from `harnessCfg`, construct the plugin, call `Register(&runnerCfg)`, then create the runner. The runner is created after all plugins are registered.

## Constraints

**Import discipline.** A plugin may import:

- `go-agent-harness/internal/harness` — for hook interfaces, `RunnerConfig`, and all types in `types.go` (`ToolCall`, `Message`, `CompletionRequest`, `CompletionResult`, `PreToolUseEvent`, etc.)
- The Go standard library and third-party packages.
- Other packages that do not themselves import `internal/harness/runner` or `internal/harness/tools`.

A plugin must **not** import:

- `go-agent-harness/internal/harness/tools` — this creates a circular dependency path through the tools registry.
- Any other `internal/harness/` sub-package beyond the top-level `harness` package.
- `go-agent-harness/internal/server` or `go-agent-harness/cmd/harnessd`.

**Event types.** Do not add entries to `internal/harness/events.go` or `AllEventTypes()` for plugin-specific events. Define plugin event type strings as plain `string` constants in your plugin's `types.go`. Emit them via an `EventEmitter` callback (see the conclusion-watcher pattern above), not by calling runner internals.

**No shared mutable state between runs.** A plugin instance may be shared across runs (harnessd creates one plugin instance at startup), so any per-run state must be protected by a mutex or atomic. The conclusion-watcher uses `sync/atomic` for its step counter and intervention count, and a `sync.Mutex` to guard the detections slice.

**Hook ordering.** Hooks run in slice order. A plugin that needs to run before another plugin must be registered first. Document ordering dependencies in the plugin's package comment.

**Nil returns from tool hooks.** `PreToolUseHook.PreToolUse` and `PostToolUseHook.PostToolUse` return `(*PreToolUseResult, error)` and `(*PostToolUseResult, error)` respectively. Returning `nil, nil` is explicitly documented as "allow with no modification" — use this as the no-op path.

## Config-driven hooks

Config-driven hooks let end users attach shell commands or HTTP calls to the
four lifecycle events — `pre_message`, `post_message`, `pre_tool_use`,
`post_tool_use` — by dropping JSON hook files into a discovery directory, with
zero Go code. They are an additive layer on top of the hook model above: each
hook file becomes an adapter that implements the existing hook interfaces and
is appended to the same `RunnerConfig` hook slices compiled-in plugins use.

### Hook files

Hook files are `*.json` files discovered from:

- **User-global**: `~/.harness/hooks/` — trusted implicitly (the user wrote
  them into their own config directory).
- **Project**: `<workspace>/.harness/hooks/` — requires explicit trust before
  loading (a cloned repository must not execute commands on your machine).

Extra discovery directories can be added with the `[hooks]` config section:

```toml
[hooks]
enabled = true                 # default true
dirs = ["/opt/team-hooks"]     # extra dirs; classify as project (trust required)
```

### Hook-file schema

```json
{
  "name": "deny-rm",                   // optional; defaults to the file base name
  "event": "pre_tool_use",             // required: pre_message | post_message | pre_tool_use | post_tool_use
  "kind": "command",                   // required: command | http
  "command": ["/path/to/script.sh"],   // required for kind=command (argv)
  "url": "https://example.com/hook",   // required for kind=http (http/https only)
  "matcher": "bash",                   // optional: exact or glob tool-name matcher (tool-use events only)
  "timeout_seconds": 5,                // optional: per-hook timeout (default 10s)
  "include_messages": false            // optional: include full messages in message-event payloads
}
```

Unknown fields are rejected. One invalid file never aborts loading — it is
recorded as a structured skip (file + reason) and surfaced in startup logs and
the `/hooks` listing.

### Wire protocol (command hooks)

A `command` hook executes its argv once per matching lifecycle event. The
event arrives as one JSON object on **stdin**; the decision is read as one
JSON object from **stdout**.

`pre_tool_use` stdin payload:

```json
{
  "event": "pre_tool_use",
  "run_id": "run_...",
  "hook_name": "deny-rm",
  "tool_name": "bash",
  "call_id": "call_...",
  "args": { "command": "rm -rf /" }
}
```

`post_tool_use` adds `"result"` (string), `"duration_ms"` (int), and
`"error"` (string, empty when the tool succeeded).

The hook replies on **stdout**:

- `pre_tool_use`: `{"decision":"allow"}` (default),
  `{"decision":"deny","reason":"..."}` blocks the tool and the reason is
  returned to the LLM as the tool result, or
  `{"decision":"allow","modified_args":{...}}` replaces the call arguments.
- `post_tool_use`: `{"modified_result":"..."}` replaces the tool output
  shown to the LLM.

Semantics:

- Exit 0 with **empty stdout** = allow / no modification.
- **Non-zero exit, timeout, or unparseable stdout = hook error**, never a
  deny. The runner's `HookFailureMode` decides: `fail_closed` (default)
  blocks the tool call, `fail_open` ignores the hook and continues.
- Every exec is bounded by the hook's timeout (`timeout_seconds`, default
  10s); on expiry the whole process tree is killed — a hung hook cannot hang
  a run.
- Hooks whose `matcher` does not match the event's tool name are not
  executed at all.
- Each execution emits runner events (`tool_hook.started/completed/failed`)
  carrying the hook name, decision, and `duration_ms`; failures additionally
  log `hook_name`, `event`, `tool_name`, `duration_ms`, `exit_code`, `error`.

Example deny script (`~/.harness/hooks/deny-rm.json` points at it):

```sh
#!/bin/sh
# deny destructive rm commands
payload=$(cat)
case "$payload" in
  *"rm -rf"*) echo '{"decision":"deny","reason":"rm -rf is not allowed"}' ;;
  *)          echo '{"decision":"allow"}' ;;
esac
```

The trust model and HTTP transport are documented in the sections below as
those layers land.

