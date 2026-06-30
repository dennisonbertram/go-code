---
title: "Tutorial: Your First Go Workflow"
sidebar_label: "First Go Workflow"
sidebar_position: 1
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

A **Go workflow bundle** is a small `package main` Go program that `harnessd` discovers, compiles, and runs on demand. You write a function, drop it in a directory, and the harness does the rest: it builds a standalone binary from your source, wires up a JSON protocol over stdin/stdout, and exposes the whole thing through the `/v1/script-workflows` HTTP API.

This tutorial walks you through creating that bundle from scratch, starting it via the HTTP API, and streaming its events back.

**What you will build:** a workflow that emits a named phase, logs a message, and returns its input arguments.

> **Note on `ctx.Agent` and API keys.** The phase, log, and return steps in this tutorial are fully key-free using the fake provider. However, `ctx.Agent(...)` dispatches through the harness provider registry, which requires a real API key (e.g. `OPENAI_API_KEY`). The core workflow and event streaming exercise in this tutorial omits the agent call to stay key-free. The section [Calling a sub-agent](#calling-a-sub-agent) shows the full agent example and is clearly labeled as requiring a key.

**What you will learn:**
- How to write a `workflow.json` manifest and a `main.go` entrypoint
- How the harness discovers and builds bundles automatically
- How to start a run with `POST /v1/script-workflows/{name}/runs`
- How to read `workflow.phase.started`, `workflow.log`, and `workflow.completed` events from the SSE stream
- Why `ctx.Agent(...)` requires a real API key even when the fake provider is configured

---

## Prerequisites

- `harnessd` built and on your `PATH` (run `make build` from the repo root, or `bash scripts/install.sh`)
- `curl` and `jq` available in your shell
- The `go-agent-harness` repo checked out — the harness needs its own source on disk to compile workflow bundles

---

## Set up a key-free harnessd

The fake provider (`HARNESS_PROVIDER=fake`) stands in for a real LLM. It returns scripted responses from a JSON turns file and requires no API key or network access.

<Steps>
<Step title="Write a fake turns file">

`HARNESS_FAKE_TURNS` is required when `HARNESS_PROVIDER=fake`. The file is consumed by the top-level harness runner; it is not used by `ctx.Agent(...)` calls inside workflow bundles (those require a real API key — see the note above).

```bash
cat > /tmp/workflow-turns.json << 'EOF'
[
  {
    "content": "analysis complete",
    "usage": {"prompt": 50, "completion": 20},
    "cost_usd": 0.0001,
    "cost_status": "available"
  }
]
EOF
```

</Step>
<Step title="Create a workflow discovery directory">

The harness looks for bundles in `~/.go-harness/workflows/` by default. Create a directory for your new bundle:

```bash
mkdir -p ~/.go-harness/workflows/hello-workflow
```

</Step>
<Step title="Start harnessd">

Run this from the **repo root** (the directory containing `go.mod` that declares `module go-agent-harness`). The server loads `prompts/catalog.yaml` from the working directory at startup.

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/workflow-turns.json \
HARNESS_AUTH_DISABLED=true \
harnessd
```

The server listens on `:8080` by default. Leave it running in this terminal.

</Step>
<Step title="Confirm the health check">

In a second terminal:

```bash
curl -s http://localhost:8080/healthz
```

Expected response:

```json
{"status":"ok"}
```

</Step>
</Steps>

<Callout variant="warning">
`HARNESS_FAKE_TURNS` is required when `HARNESS_PROVIDER=fake`. If the path is missing or the file does not exist, `harnessd` fails at startup with a fatal error. Note: `ctx.Agent(...)` calls inside workflow bundles do **not** use this turns file — agent calls go through the provider registry, which requires a real API key regardless of `HARNESS_PROVIDER`. See [Calling a sub-agent](#calling-a-sub-agent).
</Callout>

<Callout variant="warning">
The harness compiles workflow bundles by generating a temporary `go.mod` that uses a `replace` directive pointing `go-agent-harness` at the harness source root on disk. It locates that root by checking `HARNESS_SOURCE_ROOT` first, then walking up from the current working directory looking for a `go.mod` file that declares `module go-agent-harness`. If the build fails with a module-not-found error, set `HARNESS_SOURCE_ROOT` to the absolute path of the repo checkout:

```bash
export HARNESS_SOURCE_ROOT=/path/to/go-agent-harness
```
</Callout>

---

## Write the bundle

A bundle is a directory containing exactly two files: `workflow.json` (the manifest) and a Go entrypoint (conventionally `main.go`).

<Steps>
<Step title="Write workflow.json">

```bash
cat > ~/.go-harness/workflows/hello-workflow/workflow.json << 'EOF'
{
  "name": "hello-workflow",
  "description": "Introductory workflow that emits a phase, logs a message, calls an agent, and returns its args.",
  "version": 1,
  "language": "go",
  "entrypoint": "main.go",
  "when_to_use": "Use to verify end-to-end workflow discovery, build, run, and SSE event streaming.",
  "timeout_seconds": 60
}
EOF
```

Every field matters:

| Field | Required | Notes |
|---|---|---|
| `name` | yes | kebab-case identifier; must match `^[a-z0-9]+(-[a-z0-9]+)*$` |
| `description` | yes | non-empty string |
| `version` | yes | must be `1` |
| `language` | yes | must be `"go"` |
| `entrypoint` | yes | filename of the Go source file in this directory |
| `when_to_use` | no | usage hint shown to orchestrating agents |
| `timeout_seconds` | no | default is 300 (5 minutes) |

</Step>
<Step title="Write main.go">

This version is fully key-free — it emits a phase, logs a message, and returns its args without calling a real LLM. The [Calling a sub-agent](#calling-a-sub-agent) section below shows how to add `ctx.Agent(...)` once you have an API key.

```bash
cat > ~/.go-harness/workflows/hello-workflow/main.go << 'EOF'
package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		// Announce the phase — emits workflow.phase.started
		_ = ctx.Phase("Hello")

		// Log a message — emits workflow.log
		_ = ctx.Log("hello-workflow is running")

		// Return the input args
		return map[string]any{
			"args": ctx.Args,
		}, nil
	})
}
EOF
```

</Step>
</Steps>

**What the SDK surface looks like here:**

- `sdk.Main(fn)` — connects the binary to the host JSON protocol and calls your function. It reads a `start` message from stdin containing the run arguments, then writes the terminal `result` or `error` message when your function returns.
- `ctx.Phase(title string) error` — emits a `workflow.phase.started` event, which groups subsequent log and agent events under a named phase.
- `ctx.Log(message string) error` — emits a `workflow.log` event with the given message string.
- `ctx.Agent(prompt string, opts *sdk.AgentOpts) (*sdk.AgentResult, error)` — spawns a sub-agent. **Requires a real LLM API key** — see [Calling a sub-agent](#calling-a-sub-agent) below. Blocks until the agent result arrives from the host. Returns `*sdk.AgentResult` with an `Output` string field.
- `ctx.Args` — the workflow arguments decoded from the start message. In this tutorial it carries whatever JSON you pass in the `args` field of the POST body.

<Callout variant="info">
`pkg/workflowsdk` is the only Go-importable public surface of `go-agent-harness`. Everything else — the engine, the server, the harness runner — lives under `internal/` and is not importable by external modules. Your workflow binary links only against `go-agent-harness/pkg/workflowsdk`.
</Callout>

---

## Run it

The harness discovers bundles when it starts and whenever its file watcher triggers (default poll interval: 5 seconds). If `harnessd` was already running when you created the directory, wait a few seconds or restart it.

<Steps>
<Step title="List registered workflows">

```bash
curl -s http://localhost:8080/v1/script-workflows | jq .
```

You should see `hello-workflow` in the list:

```json
{
  "workflows": [
    {
      "name": "hello-workflow",
      "description": "Introductory workflow that emits a phase, logs a message, calls an agent, and returns its args.",
      "when_to_use": "Use to verify end-to-end workflow discovery, build, run, and SSE event streaming."
    }
  ]
}
```

If the list is empty, the harness has not yet built the bundle. Check the `harnessd` terminal output for build errors. The most common cause is a missing `HARNESS_SOURCE_ROOT` — see the callout above.

</Step>
<Step title="Start a workflow run">

```bash
RUN=$(curl -s -X POST http://localhost:8080/v1/script-workflows/hello-workflow/runs \
  -H "Content-Type: application/json" \
  -d '{"args": {"topic": "go workflows"}}' | jq -r .run_id)

echo "run_id: $RUN"
```

The server responds with HTTP 202:

```json
{
  "run_id": "wf_...",
  "status": "running",
  "workflow_name": "hello-workflow"
}
```

</Step>
<Step title="Stream events over SSE">

Open a stream to watch events as they arrive:

```bash
curl -s "http://localhost:8080/v1/script-workflow-runs/$RUN/events"
```

You will see lines like this (whitespace added for readability):

```
id: 1
event: workflow.started
data: {"workflow":"hello-workflow"}

id: 2
event: workflow.phase.started
data: {"phase":"Hello"}

id: 3
event: workflow.log
data: {"message":"hello-workflow is running"}

id: 4
event: workflow.completed
data: {"workflow":"hello-workflow"}
```

The stream closes automatically when a `workflow.completed` or `workflow.failed` event is emitted.

</Step>
<Step title="Read the result">

```bash
curl -s "http://localhost:8080/v1/script-workflow-runs/$RUN" | jq .
```

```json
{
  "id": "wf_...",
  "workflow_name": "hello-workflow",
  "status": "completed",
  "result_json": "{\"args\":{\"topic\":\"go workflows\"}}",
  "error": "",
  "created_at": "...",
  "updated_at": "..."
}
```

`result_json` is the JSON-encoded return value from your workflow function. To parse it as an object in a single `jq` command:

```bash
curl -s "http://localhost:8080/v1/script-workflow-runs/$RUN" | jq '.result_json | fromjson'
```

</Step>
</Steps>

**Event types you will encounter in a workflow run:**

<Card>
<CardHeader>
<CardTitle>workflow.* event types</CardTitle>
</CardHeader>
<CardContent>

| Event | When it fires |
|---|---|
| `workflow.started` | Run begins executing |
| `workflow.phase.started` | `ctx.Phase(title)` called |
| `workflow.log` | `ctx.Log(message)` called |
| `workflow.agent.started` | `ctx.Agent(...)` invoked |
| `workflow.agent.completed` | Agent returned successfully |
| `workflow.agent.failed` | Agent returned an error |
| `workflow.finding` | `ctx.Feedback("finding", ...)` called |
| `workflow.warning` | `ctx.Feedback("warning", ...)` called |
| `workflow.feedback` | `ctx.Feedback` with any other kind |
| `workflow.completed` | Workflow function returned without error |
| `workflow.failed` | Workflow function returned an error or panicked |

</CardContent>
</Card>

---

## Calling a sub-agent

<Callout variant="warning">
**Requires a real LLM API key.** `ctx.Agent(...)` dispatches through the harness provider registry. Even when `HARNESS_PROVIDER=fake` is set, agent calls resolve the default model (`gpt-4.1-mini`) via the OpenAI provider catalog and will fail with `API key env "OPENAI_API_KEY" is not set` unless a real key is provided. Set `OPENAI_API_KEY` (or the appropriate key for your provider) and remove `HARNESS_PROVIDER=fake` before using `ctx.Agent`.
</Callout>

Once you have an API key, you can extend `main.go` to call a sub-agent:

```bash
cat > ~/.go-harness/workflows/hello-workflow/main.go << 'EOF'
package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		_ = ctx.Phase("Hello")
		_ = ctx.Log("hello-workflow is running")

		// Call a sub-agent — requires a real LLM API key.
		// Emits workflow.agent.started / workflow.agent.completed.
		result, err := ctx.Agent("Summarize the workflow in one sentence.", &sdk.AgentOpts{
			Label: "summarize",
		})
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"summary": result.Output,
			"args":    ctx.Args,
		}, nil
	})
}
EOF
```

With a real provider configured, the SSE stream will include additional agent events:

```
id: 4
event: workflow.agent.started
data: {"isolation":"","label":"summarize","phase":"Hello","prompt":"Summarize the workflow in one sentence."}

id: 5
event: workflow.agent.completed
data: {"hasSchema":false,"label":"summarize","phase":"Hello"}
```

And the result will contain a `summary` key from the agent's output.

---

## Iterate

The harness caches compiled binaries by a SHA-256 content hash of all files in the bundle directory (first 16 hex chars). When you edit `main.go` or `workflow.json`, the hash changes and the harness rebuilds automatically on the next run — no restart needed.

<Steps>
<Step title="Edit and re-run">

Make a change to `main.go` — for example, add a second log call:

```go
_ = ctx.Log("second log line")
```

Save the file, then start a new run:

```bash
curl -s -X POST http://localhost:8080/v1/script-workflows/hello-workflow/runs \
  -H "Content-Type: application/json" \
  -d '{"args": {"topic": "iteration"}}' | jq .
```

The harness detects the changed hash, rebuilds the binary, and executes the new version. The `harnessd` terminal log will show a `building bundle` line for the new hash.

</Step>
<Step title="Resume a failed run">

If a run ends with `status: "failed"`, you can resume it rather than starting from scratch. This is useful when a transient error interrupted an otherwise correct workflow.

```bash
curl -s -X POST "http://localhost:8080/v1/script-workflow-runs/$RUN/resume" \
  -H "Content-Type: application/json" \
  -d '{"args": {"topic": "retry"}}' | jq .
```

Resume only works when the run's status is `"failed"`. Attempting to resume a `"completed"` run returns an error.

</Step>
<Step title="Set HARNESS_SOURCE_ROOT if the build cannot find the module root">

The build synthesizes a `go.mod` with a `replace` directive:

```
replace go-agent-harness => <moduleRoot>
```

The harness locates `moduleRoot` by checking `HARNESS_SOURCE_ROOT` first, then walking up from the working directory looking for a `go.mod` that declares `module go-agent-harness`. If neither path succeeds the build fails.

To fix it permanently, add the export to your shell profile:

```bash
export HARNESS_SOURCE_ROOT=/path/to/go-agent-harness
```

Or pass it inline when starting `harnessd`:

```bash
HARNESS_SOURCE_ROOT=/path/to/go-agent-harness \
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/workflow-turns.json \
HARNESS_AUTH_DISABLED=true \
harnessd
```

</Step>
<Step title="Set HARNESS_GO_WORKFLOW_CACHE_DIR to an absolute path">

The harness writes compiled bundle binaries to `<workspace>/.harness/workflow-cache/` by default. Because this path is relative, the binary is written relative to the harness process's working directory — which may not be what you expect. If the working directory at exec time differs from the directory at build time, exec fails with:

```
fork/exec .harness/workflow-cache/bin/hello-workflow-<hash>: no such file or directory
```

Always set `HARNESS_GO_WORKFLOW_CACHE_DIR` to an absolute path:

```bash
export HARNESS_GO_WORKFLOW_CACHE_DIR=/tmp/harness-workflow-cache
```

Or pass it inline with `HARNESS_SOURCE_ROOT`:

```bash
HARNESS_SOURCE_ROOT=/path/to/go-agent-harness \
HARNESS_GO_WORKFLOW_CACHE_DIR=/tmp/harness-workflow-cache \
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/workflow-turns.json \
HARNESS_AUTH_DISABLED=true \
harnessd
```

</Step>
</Steps>

<Callout variant="warning">
`ctx.Agent(...)` requires a real API key and will fail when `HARNESS_PROVIDER=fake` is set, even though the top-level harness uses the fake provider. This is because agent calls resolve the model via the provider registry, which requires a configured provider key. To use `ctx.Agent`, set `OPENAI_API_KEY` (or the appropriate key for your provider) and remove `HARNESS_PROVIDER=fake` from the startup environment. The workflow code itself does not change.
</Callout>

---

## Next steps

- **Workflow SDK reference** — the full `pkg/workflowsdk` API including `ctx.Feedback`, `ctx.Question`, and `ctx.Workflow` for nested workflows: [/docs/workflows/workflow-sdk](/docs/workflows/workflow-sdk)
- **Script Workflows HTTP API** — all six routes, request/response shapes, SSE format, and resume semantics: [/docs/server/script-workflows-api](/docs/server/script-workflows-api)
- **Workflow engine concepts** — how the engine handles concurrency, budget tracking, and the `Parallel`/`Pipeline` primitives available inside the harness: [/docs/workflows/workflow-engine](/docs/workflows/workflow-engine)
- **Events reference** — the complete `workflow.*` event type list and the `Event` struct fields: [/docs/concepts/events](/docs/concepts/events)
