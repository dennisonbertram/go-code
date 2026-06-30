---
title: "Tutorial: Using go-code as a Library"
sidebar_label: "Use as a Library"
sidebar_position: 5
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

Go developers often want to embed an agent harness directly in their own project rather than running a separate service. This tutorial walks through the two legitimate ways to do that with go-code, explains what each pattern is good for, and is honest about the boundary you cannot cross.

**What "library" means here.** go-code (`go-agent-harness`) is not a traditional importable library where you `go get` it and wire up the engine in your own binary. The orchestration engine, HTTP server, and harness internals all live under `internal/`, which Go's module system makes impossible to import from outside the module. One package — `pkg/workflowsdk` — is the exception. It is the only public Go API surface.

**The two supported patterns:**

- **Pattern A — Workflow bundles:** You author small `package main` binaries that import `go-agent-harness/pkg/workflowsdk`. A running `harnessd` process discovers, builds, and runs them on demand. This is the "Go-native" path.
- **Pattern B — HTTP from another service:** Your service calls `harnessd`'s REST/SSE API directly, just like any HTTP client. The `apps/socialagent` example in the repo shows this pattern end-to-end.

---

## What "library" means here

<Callout variant="warning">
`internal/workflow`, `internal/harness`, and `internal/server` are **not importable** by external Go code. Go's module visibility rules enforce this at compile time — there is no workaround. Do not attempt to add them to your `go.mod`.
</Callout>

The only importable package is:

```
go-agent-harness/pkg/workflowsdk
```

This package is a thin client that a workflow binary uses to communicate back to the `harnessd` host process over a JSONL stdin/stdout protocol. It exposes six operations: `Agent`, `Phase`, `Log`, `Feedback`, `Question`, and `Workflow`.

<Callout variant="warning">
There is no stable published module version of `go-agent-harness` on any public registry. Workflow bundles are built using a `replace` directive that points at a local copy of the harness source. There is no `v1.0.0` to pin to.
</Callout>

---

## Pattern A: workflow bundles (`pkg/workflowsdk`)

A **workflow bundle** is a directory that contains two files:

- `workflow.json` — a manifest declaring the workflow's name, description, and entrypoint
- `main.go` (or the filename named in `entrypoint`) — a `package main` that calls `sdk.Main`

You drop this directory into a discovery location on disk. A running `harnessd` instance finds it, compiles it using `go build` with a generated `go.mod` that replaces `go-agent-harness` with the local harness source, caches the resulting binary, and runs it when triggered.

<Callout variant="info">
The build uses `HARNESS_SOURCE_ROOT` (if set) or auto-detects the module root by walking up the directory tree looking for a `go.mod` containing `module go-agent-harness`. If you are running `harnessd` from the repo root, the auto-detection works with no extra configuration.
</Callout>

### Bundle structure

```
my-workflow/
├── workflow.json
└── main.go
```

**`workflow.json`** (all required fields unless noted):

```json
{
  "name": "my-workflow",
  "description": "What this workflow does.",
  "version": 1,
  "language": "go",
  "entrypoint": "main.go",
  "when_to_use": "Optional hint shown to agents.",
  "timeout_seconds": 60
}
```

Field notes:
- `name` must match the pattern `^[a-z0-9]+(-[a-z0-9]+)*$` (kebab-case)
- `version` must be exactly `1`
- `language` must be exactly `"go"`
- `timeout_seconds` defaults to 300 (5 minutes) if omitted

**`main.go`** — the workflow entrypoint:

```go
package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
    sdk.Main(func(ctx *sdk.Context) (any, error) {
        // ctx.Args holds whatever args were passed when the workflow was triggered.

        _ = ctx.Phase("Analysis")
        _ = ctx.Log("starting my workflow")

        result, err := ctx.Agent("Summarize the current project README.", &sdk.AgentOpts{
            Label:      "summarizer",
            MaxCostUSD: 0.05,
        })
        if err != nil {
            return nil, err
        }

        _ = ctx.Feedback("finding", result.Output, map[string]any{"source": "README"})
        return map[string]any{"summary": result.Output}, nil
    })
}
```

### What `sdk.Main` does

`sdk.Main` handles all the protocol machinery so your function does not have to:

1. Reads a `start` message from stdin carrying the workflow's `args`.
2. Constructs a `Context` and calls your `Run` function.
3. Writes a terminal `result` or `error` message to stdout when your function returns.
4. Calls `os.Exit(1)` if the boot handshake fails.

Your function only sees `ctx.Args` (the decoded arguments) and the six context methods.

### The SDK context methods

| Method | Signature | What it does |
|--------|-----------|--------------|
| `Agent` | `func (c *Context) Agent(prompt string, opts *AgentOpts) (*AgentResult, error)` | Spawns a sub-agent; blocks until it completes |
| `Phase` | `func (c *Context) Phase(title string) error` | Emits a phase-start event visible in the SSE stream |
| `Log` | `func (c *Context) Log(message string) error` | Emits a log message |
| `Feedback` | `func (c *Context) Feedback(kind, message string, data map[string]any) error` | Emits structured feedback (`"finding"`, `"warning"`, or progress) |
| `Question` | `func (c *Context) Question(prompt string, choices []QuestionOption) (any, error)` | Asks the user or parent workflow a question and waits for an answer |
| `Workflow` | `func (c *Context) Workflow(name string, args any) (any, error)` | Invokes another registered workflow by name |

<Callout variant="warning">
`Parallel`, `Pipeline`, and `Budget` are **not available** in workflow bundles. Those primitives live in `internal/workflow/context.go` and are only accessible inside the harness process itself. If you need fan-out, call `ctx.Workflow()` to dispatch sub-workflows, or implement concurrency inside your own `main.go` using standard Go goroutines that call `ctx.Agent()` sequentially.
</Callout>

### `AgentOpts` fields

```go
type AgentOpts struct {
    Label         string   // human label for progress display
    Phase         string   // overrides current phase grouping
    Schema        any      // JSON Schema for structured output validation
    Model         string   // model override (e.g. "gpt-4o")
    Provider      string   // provider override (e.g. "openai", "anthropic")
    Profile       string   // named sub-agent profile
    AllowedTools  []string // tool allowlist
    Isolation     string   // "" (inline) or "worktree"
    CleanupPolicy string   // cleanup policy for isolated worktrees
    AgentType     string   // custom sub-agent type
    MaxSteps      int      // child run step limit
    MaxCostUSD    float64  // child run cost ceiling in USD
}
```

### Where to put bundles

`harnessd` discovers workflow bundles from these locations (checked at startup):

| Location | How to configure |
|----------|-----------------|
| `~/.go-harness/workflows/` | Global; set `HARNESS_GLOBAL_DIR` to change the base |
| `<workspace>/.go-harness/workflows/` | Per-workspace; no config needed |

Built binaries are cached at `<workspace>/.harness/workflow-cache/` by default. Override with `HARNESS_GO_WORKFLOW_CACHE_DIR`.

<Callout variant="warning">
Set `HARNESS_GO_WORKFLOW_CACHE_DIR` to an **absolute path** (e.g. `/tmp/harness-workflow-cache`). With the default relative path the compiled binary is written relative to the harness process's working directory, which often differs from the directory you expect, causing exec failures at runtime (`fork/exec .harness/workflow-cache/bin/...: no such file or directory`).
</Callout>

### Running a key-free bundle

A bundle that does not call `ctx.Agent()` runs fully key-free: `ctx.Phase()`, `ctx.Log()`, `ctx.Feedback()`, and returning a value all communicate over the stdin/stdout JSONL protocol and never touch a provider. Use this shape to verify bundle discovery, build, and event streaming without any API key.

<Callout variant="warning">
`ctx.Agent()` requires a real LLM API key. When a workflow bundle calls `ctx.Agent()`, the harness resolves the default model (`gpt-4.1-mini`) through the provider registry, which requires a configured provider key (e.g. `OPENAI_API_KEY`). `HARNESS_PROVIDER=fake` configures the top-level harness runner but does not affect this lookup — the workflow→`SubagentRequest` conversion does not set `allow_fallback`, so the registry lookup fails with `API key env "OPENAI_API_KEY" is not set`. There is no `AllowFallback` field in `sdk.AgentOpts` — that field does not exist. To use `ctx.Agent()`, set a real provider key and remove `HARNESS_PROVIDER=fake`. See [first-go-workflow.md — Calling a sub-agent](/docs/tutorials/first-go-workflow#calling-a-sub-agent).
</Callout>

The example in this walkthrough uses a key-free bundle that only calls `ctx.Phase()`, `ctx.Log()`, and returns — matching what is actually possible without a key.

<Callout variant="warning" title="Use an absolute HARNESS_GO_WORKFLOW_CACHE_DIR">
With the default relative cache path (`<workspace>/.harness/workflow-cache`), the compiled bundle binary is written relative to wherever the harness process resolves its working directory — often not where you expect. This causes runtime exec failures:

```
fork/exec .harness/workflow-cache/bin/my-workflow-<hash>: no such file or directory
```

Always set `HARNESS_GO_WORKFLOW_CACHE_DIR` to an absolute path when running bundles:

```bash
export HARNESS_GO_WORKFLOW_CACHE_DIR=/tmp/harness-workflow-cache
```

Also set `HARNESS_SOURCE_ROOT` to the absolute repo path so the `replace` directive in the generated `go.mod` resolves correctly:

```bash
export HARNESS_SOURCE_ROOT=/path/to/go-agent-harness
```
</Callout>

<Steps>

<Step title="Create the key-free bundle">

Place the following two files in `~/.go-harness/workflows/my-workflow/`:

**`workflow.json`:**

```json
{
  "name": "my-workflow",
  "description": "Key-free demo workflow: emits a phase, logs a message, and returns args.",
  "version": 1,
  "language": "go",
  "entrypoint": "main.go",
  "timeout_seconds": 60
}
```

**`main.go`** — note: no `ctx.Agent()` call, so no API key is needed:

```go
package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
    sdk.Main(func(ctx *sdk.Context) (any, error) {
        _ = ctx.Phase("Analysis")
        _ = ctx.Log("starting my workflow")
        _ = ctx.Feedback("finding", "workflow ran key-free", map[string]any{"args": ctx.Args})
        return map[string]any{"ok": true, "args": ctx.Args}, nil
    })
}
```

</Step>

<Step title="Start harnessd with the fake provider and absolute cache dir">

From the repo root (so module auto-detection works), with an absolute cache dir:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/my-turns.json \
HARNESS_AUTH_DISABLED=true \
HARNESS_SOURCE_ROOT=/path/to/go-agent-harness \
HARNESS_GO_WORKFLOW_CACHE_DIR=/tmp/harness-workflow-cache \
go run ./cmd/harnessd
```

The turns file is required by `HARNESS_PROVIDER=fake`. Create `/tmp/my-turns.json` if it does not exist:

```json
[{"content":"ok","usage":{"prompt":0,"completion":0},"cost_usd":0,"cost_status":"available"}]
```

</Step>

<Step title="Trigger the workflow over HTTP">

```bash
# Start a workflow run
curl -s -X POST http://localhost:8080/v1/script-workflows/my-workflow/runs \
  -H "Content-Type: application/json" \
  -d '{"args": {"target": "README.md"}}' | jq .
```

Response (HTTP 202):

```json
{"run_id": "wf_abc123", "status": "running", "workflow_name": "my-workflow"}
```

</Step>

<Step title="Stream the events">

```bash
curl -s -N http://localhost:8080/v1/script-workflow-runs/wf_abc123/events
```

You will see events like:

```
id: 1
event: workflow.started
data: {"workflow":"my-workflow"}

id: 2
event: workflow.phase.started
data: {"phase":"Analysis"}

id: 3
event: workflow.log
data: {"message":"starting my workflow"}

id: 4
event: workflow.finding
data: {"message":"workflow ran key-free","data":{"args":{"target":"README.md"}}}

id: 5
event: workflow.completed
data: {"workflow":"my-workflow"}
```

Note: `workflow.phase.started` uses the key `"phase"`, not `"title"`. The `workflow.completed` payload carries only the workflow name — the run's final result is in the `result_json` field returned by `GET /v1/script-workflow-runs/{id}`, not in the SSE payload.

The stream closes automatically when `workflow.completed` or `workflow.failed` is emitted.

</Step>

</Steps>

---

## Pattern B: HTTP from another service

If you want full access to all harness features — persistent conversations, tool approval, multi-tenant isolation, cron triggers, and the complete run event stream — the right integration boundary is the HTTP API.

Your service `POST`s to `/v1/runs` to start an agent run, then reads the SSE stream at `GET /v1/runs/{id}/events` to receive real-time events. This works from any language. The `apps/socialagent` directory in the repo is a complete Go example of this pattern: it is a Telegram bot that delegates every AI task to a running `harnessd` instance without importing a single `internal/` package.

<Callout variant="info">
The package comment in `apps/socialagent/main.go` says it explicitly: "It never imports internal/ packages; all harness interaction is done via the public harnessd REST API." This is the model to follow.
</Callout>

### The minimal client

The `apps/socialagent/harness/` package shows a well-structured client. Here is the essential shape, usable as a starting point:

```go
package main

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"
)

// RunRequest is the payload for POST /v1/runs.
// Only Prompt is required. See the HTTP API reference for all fields.
type RunRequest struct {
    Prompt         string `json:"prompt"`
    ConversationID string `json:"conversation_id,omitempty"`
    AllowFallback  bool   `json:"allow_fallback,omitempty"`
}

type RunResponse struct {
    RunID  string `json:"run_id"`
    Status string `json:"status"`
}

// startRun posts a run and returns the run ID.
func startRun(ctx context.Context, baseURL string, req RunRequest) (string, error) {
    body, _ := json.Marshal(req)
    r, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/runs", bytes.NewReader(body))
    if err != nil {
        return "", err
    }
    r.Header.Set("Content-Type", "application/json")

    resp, err := http.DefaultClient.Do(r)
    if err != nil {
        return "", fmt.Errorf("POST /v1/runs: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusAccepted {
        return "", fmt.Errorf("POST /v1/runs: status %d", resp.StatusCode)
    }
    var out RunResponse
    json.NewDecoder(resp.Body).Decode(&out)
    return out.RunID, nil
}

// streamUntilDone reads the SSE event stream and returns the final output.
// Terminal events are: run.completed, run.failed, run.cancelled.
func streamUntilDone(ctx context.Context, baseURL, runID string) (string, error) {
    url := fmt.Sprintf("%s/v1/runs/%s/events", baseURL, runID)
    r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return "", err
    }
    r.Header.Set("Accept", "text/event-stream")

    resp, err := (&http.Client{}).Do(r) // no global timeout — caller's ctx controls it
    if err != nil {
        return "", fmt.Errorf("GET events: %w", err)
    }
    defer resp.Body.Close()

    scanner := bufio.NewScanner(resp.Body)
    var eventType, data string

    for scanner.Scan() {
        line := scanner.Text()
        switch {
        case strings.HasPrefix(line, "event:"):
            eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
        case strings.HasPrefix(line, "data:"):
            data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
        case line == "":
            switch eventType {
            case "run.completed":
                var env struct {
                    Payload struct{ Output string `json:"output"` } `json:"payload"`
                }
                json.Unmarshal([]byte(data), &env)
                return env.Payload.Output, nil
            case "run.failed":
                var env struct {
                    Payload struct{ Error string `json:"error"` } `json:"payload"`
                }
                json.Unmarshal([]byte(data), &env)
                return "", fmt.Errorf("run failed: %s", env.Payload.Error)
            case "run.cancelled":
                return "", fmt.Errorf("run cancelled")
            }
            eventType, data = "", ""
        }
    }
    return "", fmt.Errorf("stream ended without terminal event")
}

func main() {
    const baseURL = "http://localhost:8080"
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    // AllowFallback is required when running with HARNESS_PROVIDER=fake:
    // harnessd's provider registry resolves models to the real provider first
    // (e.g. openai for gpt-4.1-mini) and returns an error if no API key is set.
    // AllowFallback tells the runner to fall back to the configured default
    // provider (the fake one) when the primary lookup fails.
    runID, err := startRun(ctx, baseURL, RunRequest{
        Prompt:        "What is 2 + 2? Reply with just the number.",
        AllowFallback: true,
    })
    if err != nil {
        panic(err)
    }
    fmt.Println("run_id:", runID)

    output, err := streamUntilDone(ctx, baseURL, runID)
    if err != nil {
        panic(err)
    }
    fmt.Println("output:", output)
}
```

### Running the HTTP client example against a key-free harnessd

<Steps>

<Step title="Write a fake turns file">

```bash
cat > /tmp/http-client-turns.json << 'EOF'
[
  {
    "content": "4",
    "usage": {"prompt": 20, "completion": 5},
    "cost_usd": 0.0001,
    "cost_status": "available"
  }
]
EOF
```

</Step>

<Step title="Start harnessd">

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/http-client-turns.json \
HARNESS_AUTH_DISABLED=true \
go run ./cmd/harnessd &
```

Wait for the server to be ready:

```bash
until curl -sf http://localhost:8080/healthz > /dev/null; do sleep 0.5; done
echo "harnessd ready"
```

</Step>

<Step title="Run the Go client">

Save the client code above as `client/main.go` in any directory. The client is a self-contained standard-library program — no external imports — so the module name does not matter:

```bash
mkdir -p mytest/client
# (save client code as mytest/client/main.go)
cd mytest
go mod init mytest
go run ./client/
```

Expected output:

```
run_id: run_abc123
output: 4
```

</Step>

</Steps>

### Key HTTP endpoints for the HTTP client pattern

| Method | Path | Notes |
|--------|------|-------|
| `GET` | `/healthz` | Liveness check; returns `{"status":"ok"}`. No auth required. |
| `POST` | `/v1/runs` | Start a run. Returns HTTP 202 with `{"run_id":"…","status":"…"}`. |
| `GET` | `/v1/runs/{id}/events` | SSE event stream. Use `Accept: text/event-stream`. |
| `GET` | `/v1/runs/{id}` | Poll run status and final output. |
| `POST` | `/v1/runs/{id}/cancel` | Cancel an active run. |
| `GET` | `/v1/runs/{id}/summary` | Post-run token, cost, and step telemetry. |

Terminal SSE events that close the stream: `run.completed`, `run.failed`, `run.cancelled`.

The SSE stream supports `Last-Event-ID` for reconnection: set the header to replay events you missed after a dropped connection.

### `POST /v1/runs` — selected request fields

The full `RunRequest` has many optional fields. Here are the ones most useful for an external service:

| JSON field | Type | Notes |
|------------|------|-------|
| `prompt` | `string` | **Required.** The task or question for the agent. |
| `conversation_id` | `string` | Pin to an existing conversation for multi-turn continuity. |
| `system_prompt` | `string` | Overrides the runner's default system prompt. |
| `allowed_tools` | `[]string` | Allowlist of tool names; empty means all tools. |
| `mcp_servers` | `[]MCPServerConfig` | Per-run MCP server definitions. |
| `allow_fallback` | `bool` | Fall back to the runner's default provider — both when the primary provider cannot be resolved (the key-free `HARNESS_PROVIDER=fake` case used throughout this tutorial) and when the primary returns a transient (429/5xx) error. |
| `model` | `string` | Model ID override (e.g. `"gpt-4o"`). |
| `max_cost_usd` | `float64` | Spending ceiling in USD; `0` means unlimited. |

---

## Honest boundaries

<Callout variant="warning">
**What you cannot do from an external module:**

- Import `go-agent-harness/internal/workflow`, `go-agent-harness/internal/harness`, or `go-agent-harness/internal/server`. Go enforces this at compile time.
- Use `Parallel`, `Pipeline`, or `Budget` from your own binary. These are defined in `internal/workflow/context.go` and `internal/workflow/types.go` and are not reachable through `pkg/workflowsdk`.
- Pin to a stable published release. The module has no published versioned releases; bundles are built against a local source root.
- Run the orchestration engine in-process in your own binary without forking and vendoring the entire harness.
</Callout>

### When to choose which pattern

<Tabs defaultValue="bundles">
<TabsList>
  <TabsTrigger value="bundles">Choose workflow bundles when…</TabsTrigger>
  <TabsTrigger value="http">Choose HTTP integration when…</TabsTrigger>
</TabsList>
<TabsContent value="bundles">

- You want to write orchestration logic in Go with type safety.
- Your workflow fits a single `func(ctx *sdk.Context) (any, error)` entry point.
- You are already running `harnessd` and want to add custom multi-step agent logic discoverable by name.
- You are okay with the bundle being compiled by `harnessd` at runtime (a few seconds on first run, then cached).

</TabsContent>
<TabsContent value="http">

- You have an existing service in any language that needs to trigger agent runs.
- You need full access to harness features: conversations, cost controls, multi-tenant auth, cron, webhooks.
- You want the integration boundary to be a stable, documented API rather than a Go package.
- You want the harness to be independently deployable and upgradeable without touching your service's code.

</TabsContent>
</Tabs>

---

## Next steps

- For the complete `RunRequest` field reference, see the [HTTP API reference](/docs/reference/http-routes).
- To understand what events come back on the SSE stream, see [Events reference](/docs/concepts/events).
- To see a full production example of the HTTP pattern, read through `apps/socialagent/` in the repo — particularly `harness/client.go` and `harness/sse.go`.
- To learn about workflow bundle discovery, caching, and the JSONL protocol, see [Workflow bundles](/docs/workflows/workflow-sdk).
