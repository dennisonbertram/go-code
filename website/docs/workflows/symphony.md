---
title: "Symphony: Issue-Driven Orchestration"
sidebar_label: "Symphony"
sidebar_position: 3
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

`symphd` is a long-running daemon that turns labeled GitHub Issues into agent runs. It polls a repository for issues carrying a configurable label, provisions an isolated workspace for each one, dispatches a run to `harnessd` over HTTP, and retries failures with exponential back-off — all without a database. When an issue is resolved the workspace is destroyed; if it exhausts its retry budget, it moves to a dead-letter queue you can inspect via the HTTP API.

This page covers how to configure and start `symphd`, how the claim state machine works, how retries and dead letters behave, and the four workspace types you can pair with it.

<Callout variant="info">
`symphd` is distinct from both the **workflow engine** (`internal/workflow/`) and **Go Relay**. The workflow engine composes Go functions into multi-agent pipelines via `Engine.Register`; Go Relay is a multi-location control plane that routes runs across `harnessd` nodes. `symphd` is a single-node scheduler whose only job is to watch GitHub Issues and dispatch runs.
</Callout>

---

## What symphd is

`symphd` operates a simple loop:

1. Poll GitHub for open issues carrying `track_label` (default `"symphd"`).
2. Claim each unclaimed issue and advance it through the state machine.
3. Provision a fresh workspace per issue (local directory, git worktree, Docker container, or Hetzner VM).
4. Start a run against the `harnessd` HTTP endpoint that lives in that workspace.
5. Poll the run until it completes, fails, or stalls.
6. On failure, back off and retry; after `retry_max_attempts` failures, send the issue to the dead-letter queue.
7. Destroy the workspace whether the run succeeded or failed.

The dispatcher prepends a hardcoded **COORDINATOR SYNTHESIS DOCTRINE** block to every agent prompt. The doctrine requires the agent to cite exact file paths and line numbers in every delegation and finding. This is not configurable.

---

## Config and run

### Config file

`symphd` reads a YAML config file. All fields have defaults; you only need to specify what you want to change.

```yaml
# symphd config.yaml — all fields shown with their defaults
addr: ":8888"
workspace_type: "local"      # local | worktree | container | vm | pool
max_concurrent_agents: 10
poll_interval_ms: 5000        # 5 seconds
harness_url: "http://localhost:8080"  # used for local and worktree only
base_dir: ""                  # defaults to $TMPDIR/symphd
track_label: "symphd"

# GitHub repo to watch
github_owner: "your-org"
github_repo:  "your-repo"
# github_token falls back to $GITHUB_TOKEN if omitted

# Retry / backoff
retry_max_attempts: 5
retry_base_delay_ms: 10000    # 10 s
retry_max_delay_ms: 300000    # 5 min

# Pool mode (only when workspace_type: "pool")
pool_size: 3
pool_workspace_type: "container"
```

<Callout variant="warning">
`harness_url` is only used for `workspace_type: "local"` and `workspace_type: "worktree"`. For `"container"` and `"vm"`, the URL is derived from the provisioned workspace automatically; the config value is ignored.
</Callout>

### Environment variables

| Variable | Purpose |
|---|---|
| `GITHUB_TOKEN` | GitHub Personal Access Token. Fallback when `github_token` is absent from the config file. |
| `OPENAI_API_KEY` | Injected into each subagent workspace at dispatch time. |
| `ANTHROPIC_API_KEY` | Injected into each subagent workspace at dispatch time. |
| `HARNESS_MODEL` | Injected into each subagent workspace at dispatch time. |
| `HETZNER_API_KEY` | Required when `workspace_type: "vm"`. |

API keys are captured from the parent process environment at startup and forwarded to `workspace.Options.Env`. They are **never written to disk** — not to `harness.toml`, not anywhere else.

### Starting symphd

```bash
# Build the binary
go build -o symphd ./cmd/symphd

# Run with a config file
./symphd -config ./config.yaml

# Override the listen address on the command line
./symphd -config ./config.yaml -addr :9000
```

`symphd` also needs a running `harnessd` reachable at `harness_url` (for `local`/`worktree` types) or a Docker daemon (for `container`) or a Hetzner API key (for `vm`).

---

## Issue state machine and retries

### State machine

Every GitHub issue `symphd` tracks passes through exactly five states:

```
Unclaimed → Claimed → Running → Done
                              ↓
                           Failed → retry → Unclaimed
                                  → (max attempts) → DeadLetterQueue
```

| State | Constant | Transition |
|---|---|---|
| `unclaimed` | `ClaimStateUnclaimed` | Initial state; also set by `Reset` after a failed retry |
| `claimed` | `ClaimStateClaimed` | Set by `Claim` (poll tick) |
| `running` | `ClaimStateRunning` | Set by `Start` (dispatch goroutine) |
| `done` | `ClaimStateDone` | Set by `Complete` when `harnessd` reports `"completed"` |
| `failed` | `ClaimStateFailed` | Set by `Fail` when `harnessd` reports `"failed"` or stall fires |

`GitHubTracker.Poll` fetches `GET /repos/{owner}/{repo}/issues?state=open&labels={label}&per_page=100` from `https://api.github.com`. New issues are inserted as `Unclaimed`; already-tracked issues keep their current state.

### Retry policy

The `RetryPolicy` struct controls when and how long `symphd` waits before re-trying:

```go
type RetryPolicy struct {
    MaxAttempts int // default: 5
    BaseDelayMs int // default: 10000 (10 s)
    MaxDelayMs  int // default: 300000 (5 min)
}
```

Backoff formula (exponential, capped):

```
delay = min(BaseDelayMs × 2^(attempt-1), MaxDelayMs)
```

Example `BackoffDelay(attempt)` formula output (not actually slept — see note below):

| Attempt | Delay |
|---|---|
| 1 | 10 s |
| 2 | 20 s |
| 3 | 40 s |
| 4 | 80 s |
| 5 | 160 s |

<Callout variant="warning">
**Backoff is computed but not slept.** `RetryPolicy.BackoffDelay` returns the correct duration, but the orchestrator's poll loop does not call it. After `tracker.Reset` moves the issue back to `Unclaimed`, it will be re-claimed on the very next poll tick (default every 5 seconds), regardless of the computed delay. This may change in a future release.
</Callout>

<Callout variant="warning">
**The `Attempts` counter increments twice per failed cycle.** `Claim` increments `Attempts` and `Reset` also increments `Attempts`. After one failed cycle the issue has `Attempts = 2`. With the default `retry_max_attempts: 5`, this means a maximum of two full retry cycles before the issue lands in the dead-letter queue.
</Callout>

### Dead-letter queue

When `ShouldRetry(attempts)` returns `false` (i.e. `attempts >= MaxAttempts`), the issue is added to the `DeadLetterQueue` instead of being reset. Each entry has:

| Field | Description |
|---|---|
| `IssueNumber` | GitHub issue number |
| `Title` | Issue title at time of failure |
| `Attempts` | Total attempt count |
| `LastError` | Error message from the final failure |
| `ExhaustedAt` | Timestamp when the issue was dead-lettered |

Inspect the queue at any time via `GET /api/v1/dead-letters` (see [HTTP surface](#http-surface) below).

---

## Workspace types

`symphd` supports five values for `workspace_type`. Choose based on your isolation and provisioning needs.

<Tabs>
<TabsList>
  <TabsTrigger value="local">local</TabsTrigger>
  <TabsTrigger value="worktree">worktree</TabsTrigger>
  <TabsTrigger value="container">container</TabsTrigger>
  <TabsTrigger value="vm">vm</TabsTrigger>
  <TabsTrigger value="pool">pool</TabsTrigger>
</TabsList>

<TabsContent value="local">

<Card>
<CardHeader>
<CardTitle>local</CardTitle>
</CardHeader>
<CardContent>

Creates a directory `<base_dir>/issue-{N}` for each issue and points the run at an **already-running** `harnessd` at `harness_url`. Does not start or stop any process.

Good for development and single-host deployments where one `harnessd` instance handles all issues.

**Config:** Only `base_dir` and `harness_url` are relevant.

</CardContent>
</Card>

</TabsContent>
<TabsContent value="worktree">

<Card>
<CardHeader>
<CardTitle>worktree</CardTitle>
</CardHeader>
<CardContent>

Runs `git worktree add` to give each issue its own branch and directory within an existing repo. Concurrent worktree operations are serialized per repo path to avoid git conflicts.

Good for multi-agent parallelism on the same repository.

**Config:** Set `base_dir` to the path of the git repo. Each worktree lands in a sibling directory named `<repo>-subagents/`.

</CardContent>
</Card>

</TabsContent>
<TabsContent value="container">

<Card>
<CardHeader>
<CardTitle>container</CardTitle>
</CardHeader>
<CardContent>

Starts a Docker container per issue using the image `go-agent-harness:latest` (override via `HARNESS_IMAGE` env var). The harness URL is derived from the dynamically allocated host port; `harness_url` in the config is ignored.

Requires Docker daemon accessible on the host.

Build the image first:

```bash
docker build -f build/Dockerfile.harnessd -t go-agent-harness:latest .
```

</CardContent>
</Card>

</TabsContent>
<TabsContent value="vm">

<Card>
<CardHeader>
<CardTitle>vm</CardTitle>
</CardHeader>
<CardContent>

Provisions a Hetzner Cloud VM per issue (default: `cax11`, ARM 2c/4G, `ubuntu-24.04`, region `nbg1`). The VM runs a cloud-init script that installs `harnessd` and starts it as a systemd service at `:8080`.

Requires `HETZNER_API_KEY` in the environment.

<Callout variant="warning">
VM workspace tool routing is incomplete. File and shell tools (`write`, `edit`, `bash`) execute on the **host**, not inside the guest VM. Each affected run emits a `prompt.warning` event with code `"vm_workspace_tool_routing"`. This is tracked in issue #564.
</Callout>

</CardContent>
</Card>

</TabsContent>
<TabsContent value="pool">

<Card>
<CardHeader>
<CardTitle>pool</CardTitle>
</CardHeader>
<CardContent>

Pre-provisions `pool_size` workspaces of type `pool_workspace_type` and hands them out on demand. When an issue finishes, its workspace slot is returned to the pool and reprovisioned for the next issue.

Reduces per-issue startup latency at the cost of always keeping `pool_size` workspaces alive.

```yaml
workspace_type: "pool"
pool_size: 3
pool_workspace_type: "container"  # or "local" | "vm"
```

</CardContent>
</Card>

</TabsContent>
</Tabs>

---

## HTTP surface

`symphd` listens on `addr` (default `:8888`) and exposes four JSON endpoints. All responses include `"status": "ok"` on success.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/v1/state` | Orchestrator state snapshot: version, running since, agent count, workspace type |
| `GET` | `/api/v1/issues` | All tracked issues with their current `ClaimState` |
| `POST` | `/api/v1/refresh` | Triggers an immediate GitHub poll (same as the next tick) |
| `GET` | `/api/v1/dead-letters` | Issues that exhausted `retry_max_attempts` |

### Example: inspect state

```bash
curl http://localhost:8888/api/v1/state
```

```json
{
  "status": "ok",
  "state": {
    "version": "0.1.0",
    "running_since": "2025-06-28T10:00:00Z",
    "agent_count": 0,
    "config": {
      "workspace_type": "local",
      "max_concurrent_agents": 10
    }
  }
}
```

### Example: read the dead-letter queue

```bash
curl http://localhost:8888/api/v1/dead-letters
```

```json
{
  "status": "ok",
  "dead_letters": [
    {
      "IssueNumber": 42,
      "Title": "Fix login bug",
      "Attempts": 5,
      "LastError": "dispatcher: stall timeout (5m0s) exceeded for run <run-id>",
      "ExhaustedAt": "2025-06-28T11:30:00Z"
    }
  ]
}
```

### Example: force a poll

```bash
curl -X POST http://localhost:8888/api/v1/refresh
```

```json
{ "status": "ok", "message": "refresh queued" }
```

---

## WORKFLOW.md (future wiring)

`symphd` includes a `LoadWorkflow` function that reads a Markdown file with optional YAML front matter as a per-repository dispatch template:

```markdown
---
max_concurrent_agents: 5
max_turns: 20
workspace_type: worktree
track_label: symphd
---
Fix issue #{{ .issue_number }}: {{ .issue_title }}

{{ .issue_body }}
```

Front matter fields override the global config defaults for that workflow. The body is a Go `text/template` rendered with `missingkey=error` — any referenced variable absent from `vars` causes a hard error.

<Callout variant="warning">
`LoadWorkflow` and `RenderPrompt` are implemented and tested, but the orchestrator's poll loop does **not** yet load or use a `WORKFLOW.md` file. Dispatch currently uses `buildPrompt(issue)` — the hardcoded synthesis doctrine prefix plus the issue number, title, and body. WORKFLOW.md wiring is not present in the current release.
</Callout>

---

## Operational notes

**Auto-wiring requirement.** The orchestrator only starts dispatching if both `github_owner`/`github_repo` and a valid `workspace_type` are present in the config. If either is missing, `Start()` is a no-op and no issues are dispatched.

**Workspaces are always destroyed.** `ws.Destroy` is deferred in the dispatch goroutine and runs on both success and failure. Unlike the OpenAI Symphony spec which preserves successful workspaces, `symphd` always destroys the workspace after the run exits.

**Stall detection.** The stall timer (default 5 minutes) resets on any status transition. A run that transitions between `"queued"` and `"running"` repeatedly will delay stall detection, since each transition resets the clock.

**Concurrency.** The semaphore is sized to `max_concurrent_agents` (minimum 1). Only the `Dispatch` call acquires a slot; results are buffered in a channel of size 64.

---

## Next steps

- See [Workspaces and Isolation](/docs/concepts/workspaces) for a deeper dive into the `local`, `worktree`, `container`, and `vm` backends.
- See [Environment Variables](/docs/reference/environment-variables) and [CLI Flags](/docs/reference/cli-flags) for the full `harnessd` config reference — `symphd` forwards `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `HARNESS_MODEL` into each subagent workspace.
- See [Server API Reference](/docs/server/http-api-guide) for the `harnessd` run endpoints that `symphd` calls (`POST /v1/runs`, `GET /v1/runs/{id}`).
