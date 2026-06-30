---
title: "Authoring Workflow Bundles (pkg/workflowsdk)"
sidebar_label: "Workflow SDK"
sidebar_position: 2
---

import { Callout, Steps, Step, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

The **Workflow SDK** (`go-agent-harness/pkg/workflowsdk`) is the only client-facing public package of the harness module intended for use in workflow bundles. It is a thin client library you link into a small standalone binary — called a **workflow bundle** — so that `harnessd` can discover, compile, and drive it at runtime.

A workflow bundle lets you express multi-step agent logic in plain Go: call sub-agents, emit progress phases, log messages, surface structured findings, ask questions, and invoke other registered workflows. The harness host handles process management, event streaming, and HTTP routing; your binary communicates with it over a JSONL protocol on stdin/stdout.

<Callout variant="warning" title="Bundles are out-of-process, not in-process">
The "library" pattern here is **not** embedding the harness engine in your own binary. You write a small `package main` that imports only `go-agent-harness/pkg/workflowsdk`, and `harnessd` compiles and spawns it as a subprocess. All of `internal/` — the engine, server, harness runner, and everything else — is inaccessible to external Go modules.
</Callout>

<Callout variant="warning" title="No published versioned releases">
There is no published module release on a public registry. The `go-agent-harness` module name carries no `v2+` version suffix and is not distributed via `go get`. Bundles are built by the harness using a `replace` directive that points at the harness source root on your local disk (see [Discovery and build](#discovery-and-build)).
</Callout>

---

## The client-facing public package

Every exported symbol lives in a single file: `pkg/workflowsdk/sdk.go`. The import path and typical alias are:

```go
import sdk "go-agent-harness/pkg/workflowsdk"
```

The sole entry point is `sdk.Main`:

```go
func Main(fn Run)
```

`Main` bootstraps the JSONL connection to the host, reads the initial `start` message (which carries `args`), constructs a `*Context`, calls `fn`, and writes either a `result` or `error` terminal message. If the bootstrap fails — for example, because the binary was launched without the expected stdin pipe — it writes a JSON error to stdout and calls `os.Exit(1)`.

The `Run` type is just a function signature:

```go
type Run func(ctx *Context) (any, error)
```

Return any JSON-serializable value as the workflow result, or return a non-nil error to fail the run.

---

## Bundle layout

A bundle is a directory that contains exactly two required files.

### `workflow.json`

The manifest tells the harness how to identify and build your bundle.

| Field | Required | Description |
|---|---|---|
| `name` | yes | Kebab-case identifier. Must match `^[a-z0-9]+(-[a-z0-9]+)*$`. |
| `description` | yes | Non-empty human-readable description. |
| `version` | yes | Must be `1`. |
| `language` | yes | Must be `"go"`. |
| `entrypoint` | yes | Filename of the Go source, typically `"main.go"`. |
| `when_to_use` | no | Usage hint shown to agents when selecting workflows. |
| `args_schema` | no | JSON Schema-like object describing accepted arguments. |
| `skill` | no | Associates the bundle with a named skill. |
| `timeout_seconds` | no | Per-run timeout. Default: 300 seconds (5 minutes). |

Example (`ux-feedback-check/workflow.json`):

```json
{
  "name": "ux-feedback-check",
  "description": "Workflow UX smoke that emits phase, log, finding feedback, and returns args.",
  "version": 1,
  "language": "go",
  "entrypoint": "main.go",
  "when_to_use": "Use to validate workflow discovery, run, feedback, SSE, and result handling.",
  "timeout_seconds": 30
}
```

### `main.go`

A `package main` file that calls `sdk.Main` with your workflow function. The example below is the real `ux-feedback-check` bundle from `.go-harness/workflows/ux-feedback-check/main.go`:

```go
package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		_ = ctx.Phase("Workflow UX")
		_ = ctx.Log("workflow ux path running")
		_ = ctx.Feedback("finding", "workflow feedback reached host", map[string]any{
			"path": "api-and-tmux",
		})
		return map[string]any{"ok": true, "args": ctx.Args}, nil
	})
}
```

`ctx.Args` carries whatever value was passed as `args` when the run was started via the HTTP API.

---

## Discovery and build

`harnessd` automatically discovers, builds, and registers bundles from several directories without requiring a restart.

### Discovery directories

<Card>
<CardHeader>
<CardTitle>Where harnessd looks for bundles</CardTitle>
</CardHeader>
<CardContent>

| Location | Default path | Controlled by |
|---|---|---|
| Global workflows | `~/.go-harness/workflows` | `HARNESS_GLOBAL_DIR` |
| Workspace workflows | `<workspace>/.go-harness/workflows` | — |
| Global skills | `~/.go-harness/skills` | `HARNESS_GLOBAL_DIR` |
| Workspace skills | `<workspace>/.go-harness/skills` | — |

Skill-scoped bundles live at `<skillRoot>/<skillName>/workflows/<workflowName>/`.

</CardContent>
</Card>

To place a bundle where `harnessd` will find it, drop the directory into any of these paths. The hot-reload watcher polls every 5 seconds by default (`HARNESS_WATCH_INTERVAL_SECONDS`).

### Build mechanics

When the harness builds a bundle it:

<Steps>
<Step title="Copies the bundle to a staging area">
  The bundle directory is copied to a temporary location to isolate the build.
</Step>
<Step title="Writes a synthetic go.mod with a replace directive">
  The generated module file sets `module workflow.local/<name>`, requires `go-agent-harness v0.0.0`, and adds a `replace go-agent-harness => <moduleRoot>` directive. This is what makes `go-agent-harness/pkg/workflowsdk` resolvable without a published release.
</Step>
<Step title="Runs go build">
  The harness runs `go build -o <binary> .` with `GOWORK=off` and a minimal environment (only `HOME` and `PATH`). This ensures reproducible builds independent of the developer's module workspace.
</Step>
<Step title="Caches the binary by content hash">
  Binaries are cached at `{cacheDir}/bin/{name}-{hash16}`, where `hash16` is the first 16 hex characters of a SHA-256 hash of the bundle directory. A cached binary is reused as long as the hash matches. The cache directory defaults to `<workspace>/.harness/workflow-cache` and can be overridden with `HARNESS_GO_WORKFLOW_CACHE_DIR`. **The cache dir must be an absolute path.** With the default relative path the binary is written relative to the harness process's working directory; if that differs from the directory at exec time, the run fails with `fork/exec .harness/workflow-cache/bin/...: no such file or directory`. Always set `HARNESS_GO_WORKFLOW_CACHE_DIR` to an absolute path (e.g. `/tmp/harness-workflow-cache`) when running bundles.
</Step>
</Steps>

### HARNESS_SOURCE_ROOT resolution

The build's `replace` directive must point at the on-disk harness module root. The harness resolves it in this priority order:

1. **`HARNESS_SOURCE_ROOT` environment variable** — if set, this path is used directly.
2. **CWD walk** — the harness walks up from the current working directory looking for a `go.mod` file containing `module go-agent-harness`.

In practice, when you run `harnessd` from inside the cloned repository, the CWD walk finds the root automatically. Set `HARNESS_SOURCE_ROOT` explicitly in CI or when starting `harnessd` from an unrelated directory.

---

## SDK API and limits

### `Context` methods

The `*sdk.Context` passed to your `Run` function exposes six methods:

<Tabs defaultValue="agent">
<TabsList>
  <TabsTrigger value="agent">Agent</TabsTrigger>
  <TabsTrigger value="phase">Phase</TabsTrigger>
  <TabsTrigger value="log">Log</TabsTrigger>
  <TabsTrigger value="feedback">Feedback</TabsTrigger>
  <TabsTrigger value="question">Question</TabsTrigger>
  <TabsTrigger value="workflow">Workflow</TabsTrigger>
</TabsList>

<TabsContent value="agent">

```go
func (c *Context) Agent(prompt string, opts *AgentOpts) (*AgentResult, error)
```

Spawns a sub-agent with the given `prompt`. Blocks until the agent completes and the host returns a result. Pass `nil` for `opts` to use all defaults.

`AgentOpts` controls how the sub-agent is configured:

```go
type AgentOpts struct {
    Label         string   `json:"label,omitempty"`
    Phase         string   `json:"phase,omitempty"`
    Schema        any      `json:"schema,omitempty"`
    Model         string   `json:"model,omitempty"`
    Provider      string   `json:"provider,omitempty"`
    Profile       string   `json:"profile,omitempty"`
    AllowedTools  []string `json:"allowed_tools,omitempty"`
    Isolation     string   `json:"isolation,omitempty"`
    CleanupPolicy string   `json:"cleanup_policy,omitempty"`
    AgentType     string   `json:"agent_type,omitempty"`
    MaxSteps      int      `json:"max_steps,omitempty"`
    MaxCostUSD    float64  `json:"max_cost_usd,omitempty"`
}
```

`AgentResult` carries the agent's output:

```go
type AgentResult struct {
    Output string `json:"output"`
    Schema any    `json:"schema,omitempty"`
    Error  string `json:"error,omitempty"`
}
```

</TabsContent>

<TabsContent value="phase">

```go
func (c *Context) Phase(title string) error
```

Emits a `workflow.phase.started` event on the host's event stream. Use phases to group related `Agent` calls and give operators a progress view. Errors are non-fatal — you can ignore them with `_ = ctx.Phase(...)`.

</TabsContent>

<TabsContent value="log">

```go
func (c *Context) Log(message string) error
```

Emits a `workflow.log` event. Use for human-readable progress messages that don't constitute structured findings. Errors can be safely ignored.

</TabsContent>

<TabsContent value="feedback">

```go
func (c *Context) Feedback(kind, message string, data map[string]any) error
```

Emits structured feedback. The `kind` string controls which event type is emitted:

| `kind` value | Emitted event |
|---|---|
| `"finding"` | `workflow.finding` |
| `"warning"` | `workflow.warning` |
| `""` or any other value | `workflow.feedback` |

`data` is an arbitrary map of additional fields attached to the payload.

</TabsContent>

<TabsContent value="question">

```go
func (c *Context) Question(prompt string, choices []QuestionOption) (any, error)
```

Emits a `workflow.question` event and blocks until the host's configured `QuestionResponder` answers. Returns an error if no responder is configured on the host side.

```go
type QuestionOption struct {
    Label       string `json:"label"`
    Description string `json:"description"`
}
```

</TabsContent>

<TabsContent value="workflow">

```go
func (c *Context) Workflow(name string, args any) (any, error)
```

Invokes another registered workflow by name and blocks until it completes. The nested workflow receives a cloned token budget (`Budget.Clone()`) so token accounting carries across nested calls; it runs with its own independent concurrency semaphore (a fresh `Context`), not a shared one. Returns the nested workflow's result, or an error if it fails.

</TabsContent>
</Tabs>

### What the SDK does NOT expose

<Callout variant="warning" title="Parallel, Pipeline, and Budget are host-only">
The `Parallel`, `Pipeline`, and `Budget` primitives — along with the full internal `Context` that powers them — are defined in `internal/workflow/` and are not accessible from a workflow bundle. If you need parallel fan-out in your bundle, implement it manually with goroutines and `sync.WaitGroup`, or decompose the work into separate named workflows that you invoke with `ctx.Workflow()`.
</Callout>

The following packages are in `internal/` and cannot be imported:

- `go-agent-harness/internal/workflow` — engine, `Script` type, `Parallel`, `Pipeline`, budget, event constants, `SourceManager`
- `go-agent-harness/internal/harness` — runner, tool registry, conversation store
- `go-agent-harness/internal/server` — HTTP server and routes

---

## The JSONL protocol (reference)

You will rarely need to think about this layer, but it is useful background for debugging.

When `harnessd` spawns your binary, communication happens over newline-delimited JSON on stdin/stdout:

| Direction | Message format | Purpose |
|---|---|---|
| host → child | `{"type":"start","result":<argsJSON>}` | Boots the workflow; delivers `ctx.Args` |
| child → host | `{"id":"req_N","type":"<op>","args":{...}}` | RPC call for `agent`, `phase`, `log`, `feedback`, `workflow`, or `question` |
| host → child | `{"id":"req_N","result":<json>}` | RPC reply |
| host → child | `{"id":"req_N","error":"..."}` | RPC error reply |
| child → host | `{"type":"result","result":<json>}` | Terminal success message |
| child → host | `{"type":"error","error":"..."}` | Terminal failure message |

Maximum protocol message size: 1 MB. Maximum stderr captured from the child process: 32 KB. Any message sent after a terminal `result` message causes the host to treat the run as an error.

---

## Putting it together: a complete bundle

Here is a worked example of a bundle that uses most SDK methods. Create a directory, add the two files, and place the directory in `~/.go-harness/workflows/` or `<workspace>/.go-harness/workflows/`.

<Tabs defaultValue="manifest">
<TabsList>
  <TabsTrigger value="manifest">workflow.json</TabsTrigger>
  <TabsTrigger value="main">main.go</TabsTrigger>
</TabsList>

<TabsContent value="manifest">

```json
{
  "name": "code-review-summary",
  "description": "Run a code review agent on a file, emit findings, and return a summary.",
  "version": 1,
  "language": "go",
  "entrypoint": "main.go",
  "when_to_use": "Use when you want an automated code review with structured finding events.",
  "timeout_seconds": 120
}
```

</TabsContent>

<TabsContent value="main">

```go
package main

import (
	"fmt"
	sdk "go-agent-harness/pkg/workflowsdk"
)

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		// Surface a phase for progress visibility.
		_ = ctx.Phase("Code Review")
		_ = ctx.Log("starting review")

		// Call a sub-agent with a structured output schema.
		result, err := ctx.Agent(
			"Review the diff in ctx.Args for correctness and style issues. "+
				"Return JSON with fields: summary (string), issues (array of strings).",
			&sdk.AgentOpts{
				Label: "reviewer",
				Schema: map[string]any{
					"type": "object",
					"required": []string{"summary", "issues"},
					"properties": map[string]any{
						"summary": map[string]any{"type": "string"},
						"issues":  map[string]any{"type": "array"},
					},
				},
			},
		)
		if err != nil {
			return nil, fmt.Errorf("agent failed: %w", err)
		}

		// Emit a structured finding for downstream consumers.
		_ = ctx.Feedback("finding", "review complete", map[string]any{
			"output": result.Output,
		})

		return map[string]any{"output": result.Output}, nil
	})
}
```

</TabsContent>
</Tabs>

Once the file is saved, `harnessd` picks it up within the next watch interval (default 5 seconds). You can then start a run via the HTTP API:

```bash
curl -s -X POST http://localhost:8080/v1/script-workflows/code-review-summary/runs \
  -H "Content-Type: application/json" \
  -d '{"args": {"file": "internal/server/http.go"}}' | jq .
```

And stream its events:

```bash
curl -s http://localhost:8080/v1/script-workflow-runs/<run_id>/events
```

---

## Relevant environment variables

| Variable | Default | Purpose |
|---|---|---|
| `HARNESS_SOURCE_ROOT` | CWD walk | Path to the harness module root used in the bundle's `replace` directive. |
| `HARNESS_GO_WORKFLOW_CACHE_DIR` | `<workspace>/.harness/workflow-cache` | Where compiled workflow binaries are cached. **Must be an absolute path** — with the default relative value, exec fails at runtime because the binary is written relative to the harness process's working directory. Set to an absolute path such as `/tmp/harness-workflow-cache`. |
| `HARNESS_GLOBAL_DIR` | `~/.go-harness` | Root of the global discovery directories. |
| `HARNESS_WATCH_INTERVAL_SECONDS` | `5` | How often the hot-reload watcher polls for new or changed bundles. |

---

## Next steps

- **HTTP API** — see the Script Workflow HTTP routes in the server reference for the full request/response shapes for `/v1/script-workflows` and `/v1/script-workflow-runs`.
- **Event model** — the events emitted during a workflow run (`workflow.phase.started`, `workflow.finding`, etc.) are described in [The Event Model](/docs/concepts/events).
- **Configuration** — `harnessd` startup, provider selection, and workspace layout are covered in [Configuration](/docs/concepts/configuration).
