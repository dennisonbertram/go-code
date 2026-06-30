---
title: "What is go-code?"
sidebar_label: "What is go-code?"
sidebar_position: 1
---

import { Hero, FeatureGrid, FeatureCard, Callout, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';
import { Terminal, Server, Workflow, Plug, Globe } from 'lucide-react';

<Hero
  eyebrow="What is go-code?"
  title="A coding-agent runtime built for parallel work"
  description="go-code runs AI coding agents over your real repositories — in parallel, from a single Go binary. Drive it from the terminal, an interactive TUI, or a streamed HTTP API. Local by default; scale across git worktrees, containers, and machines when you need to."
/>

go-code is a **local-first coding-agent runtime, written in Go**. Its defining traits are speed and parallelism. It ships as one self-contained binary — no Node, no Python, no virtualenv — and it is built to run many agents at once: fanned out by a multi-agent **workflow engine**, isolated in per-agent **git worktrees**, and scaled across **containers, VMs, and machines**. On top of that core it gives you an installable command (`go-code`), a full-screen TUI, a local HTTP daemon (`harnessd`), a streamed Server-Sent Events API, and provider-aware model routing.

<Callout variant="info" title="Module name vs. product name">
  The Go module is named <code>go-agent-harness</code> (that is the name in <code>go.mod</code> and import paths), while the user-facing binary and public product name is <code>go-code</code>. You will see both names in the source tree; they refer to the same project.
</Callout>

---

## go-code in one sentence

go-code runs an AI coding agent against a real repository on your machine. You give it a prompt; it loops through LLM turns and tool calls, streams every event back to you in real time, and stops when the task is done or you interrupt it.

Key properties:

- **Written in Go.** One static binary, no language runtime to install, and goroutine-based concurrency underneath the orchestration. It cross-compiles and drops into a tiny container image.
- **Built for parallelism.** A workflow engine fans agents out with `ctx.Parallel()` and `ctx.Pipeline()` (bounded by a concurrency semaphore); isolated git worktrees let many agents work one repository at once with no checkout conflicts; warm workspace pools, containers, and VMs extend that across machines, with a relay control plane for multi-location routing in progress.
- **Local-first.** `harnessd` runs on your laptop, pointed at your working directory. No repository upload, no remote execution required. Cloud and relay features exist but are optional additions on top of this local core.
- **Provider-aware routing.** A JSON catalog (`catalog/models.json`) describes 10 providers — `openai`, `anthropic`, `gemini`, `deepseek`, `groq`, `xai`, `kimi`, `qwen`, `together`, `openrouter` — with per-provider API keys, pricing, and capability flags. go-code routes to whichever provider and model you configure, with an optional per-run cost ceiling (`HARNESS_MAX_COST_PER_RUN_USD`).
- **Streamed everything.** Every event — LLM token deltas, tool calls, cost accounting, workspace provisioning — is emitted on an SSE stream at `GET /v1/runs/{id}/events`. The TUI and CLI consume the same stream that your own scripts can consume.
- **Key-free smoke path.** Set `HARNESS_PROVIDER=fake` to run the full stack without any API key. This is how CI tests and new contributor smoke checks work.

---

## The surfaces you will use

<FeatureGrid columns={3}>
  <FeatureCard
    icon={Terminal}
    title="go-code CLI"
    description="The primary user-facing launcher. Auto-starts harnessd, detects the project root, then either opens the TUI or streams a single prompt and exits. One command for everyday use."
  />
  <FeatureCard
    icon={Terminal}
    title="BubbleTea TUI"
    description="A full-screen interactive chat interface (harnesscli --tui). Multi-turn conversations, live tool-call cards, slash commands, model picker, session history, and two-stage interrupt."
  />
  <FeatureCard
    icon={Server}
    title="harnessd"
    description="The local HTTP daemon. Boots the complete agent runtime — provider, tools, memory, cron, MCP, skills — and serves it over REST + SSE at localhost:8080."
  />
  <FeatureCard
    icon={Workflow}
    title="Workflow engine"
    description="Go script-based multi-agent pipeline orchestrator (internal/workflow). Register Script functions, fan out with ctx.Parallel(), compose stages with ctx.Pipeline(), track budgets, and resume failed runs."
  />
  <FeatureCard
    icon={Plug}
    title="MCP & integrations"
    description="harnessd exposes an MCP server at /mcp on the same port as the REST API. Webhooks for GitHub, Slack, and Linear are built in; an embedded cron scheduler handles time-based triggers."
  />
  <FeatureCard
    icon={Globe}
    title="Go Relay & Symphony"
    description="Optional remote pieces: Go Relay (internal/relay) is a multi-location control plane for worker registration and run dispatch. Symphony (symphd) orchestrates agents across workspace pools with a GitHub issue work queue."
  />
</FeatureGrid>

### The three binaries

| Binary | Entry point | What it does |
|---|---|---|
| `go-code` | `scripts/go-code.sh` (installed as `go-code`) | Shell launcher that auto-starts `harnessd` and delegates to `harnesscli` |
| `harnesscli` | `cmd/harnesscli` | Terminal client: streaming prompt mode and BubbleTea TUI |
| `harnessd` | `cmd/harnessd` | Local HTTP daemon and runtime bootstrap |

When you type `go-code`, the shell wrapper checks whether a healthy server is already running on the configured port. If not, it starts `harnessd` in the background, then launches `harnesscli`. If you already have a server running, `go-code` leaves it alone and connects to it.

---

## What it enables

### Drive an agent from wherever you are

```bash
# Interactive TUI (default when no prompt is given)
go-code

# Stream a single prompt and exit
go-code "Refactor the authentication layer to use middleware"

# Use the CLI directly against a running daemon
harnesscli -base-url http://localhost:8080 -prompt "Write tests for pkg/auth"
```

All three paths ultimately call `POST /v1/runs` on `harnessd` and stream `GET /v1/runs/{id}/events` until a terminal event (`run.completed`, `run.failed`, or `run.cancelled`) arrives.

<Callout variant="warning" title="Default tool posture: full auto, no sandbox">
  Out of the box, <code>harnessd</code> runs with <code>HARNESS_TOOL_APPROVAL_MODE=fullauto</code> — the agent's file and shell tools execute directly against your working directory with no approval prompts and no sandbox isolation. Point go-code at a repository you trust, and switch <code>HARNESS_TOOL_APPROVAL_MODE</code> to <code>permissions</code> if you want an approval gate.
</Callout>

### Key-free smoke with the fake provider

You can run the entire stack — daemon, agent loop, tool calls, SSE streaming — without any API key:

```bash
# Write a minimal turns file, then start harnessd with both env vars set
printf '%s' '[{"content":"hello from fake"}]' > /tmp/turns.json
HARNESS_PROVIDER=fake HARNESS_FAKE_TURNS=/tmp/turns.json go run ./cmd/harnessd &
harnesscli -base-url http://localhost:8080 -prompt "hello"
```

`HARNESS_FAKE_TURNS` must point to a JSON turns file — harnessd fails to boot without it. The canonical shell smoke that handles this automatically is `scripts/run-bench-smoke.sh`. The same fake provider also underpins the key-free unit smoke `go test ./internal/server/... -run TestRunSmoke` (which constructs it in-process with inline turns).

### Route across multiple providers

Set the model once; go-code resolves the provider:

```bash
# Use an environment variable
HARNESS_MODEL=claude-sonnet-4-6 go-code "Review this PR"

# Or pick interactively in the TUI with /model
go-code
# then type: /model
```

The model catalog at `catalog/models.json` lists which API key each model needs (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`, etc.). If `OPENAI_API_KEY` is set and no catalog model is configured, `harnessd` falls back to OpenAI automatically.

### Replay and forensically diff runs

Every run can be recorded as a JSONL rollout file (set `HARNESS_ROLLOUT_DIR`). You can then:

```bash
# Replay a recorded run in simulate mode
go-code replay <run-id-or-rollout-path>

# Or from harnesscli directly
harnesscli replay --mode simulate <run-id>
```

The replay subsystem re-runs the same prompt through the same tool sequence, making it possible to detect drift when prompts or tools change.

### Compose multi-agent workflows in Go

The workflow engine lets you write typed Go pipelines that fan out over sub-agents, track a shared token budget, and stream structured events alongside regular run events:

```go
engine.Register("code-review-pipeline", func(ctx *workflow.Context) (any, error) {
    results, err := ctx.Parallel([]func() (any, error){
        func() (any, error) { return ctx.Agent("Review pkg/auth for security issues", nil) },
        func() (any, error) { return ctx.Agent("Check test coverage in pkg/auth", nil) },
    })
    return results, err
})
```

Registered workflows are exposed at `POST /v1/script-workflows/{name}/runs` and stream events at `GET /v1/script-workflow-runs/{id}/events`.

---

## Configuration in 30 seconds

go-code uses a 6-layer configuration cascade (lowest to highest priority):

1. Built-in defaults (`harnessd` default model: `gpt-4.1-mini`, listen address: `:8080`)
2. User global config: `~/.harness/config.toml`
3. Project config: `.harness/config.toml` in the workspace root
4. Named profile: `~/.harness/profiles/<name>.toml` (via `harnessd --profile <name>`)
5. `HARNESS_*` environment variables
6. Cloud/team constraints (a future stub — not yet applied)

The most useful environment variables to know up front:

| Variable | Default | Purpose |
|---|---|---|
| `HARNESS_ADDR` | `:8080` | Server listen address |
| `HARNESS_MODEL` | `gpt-4.1-mini` | Default LLM model |
| `HARNESS_MAX_STEPS` | `8` | Max tool-call steps per run |
| `HARNESS_MAX_COST_PER_RUN_USD` | `0` (unlimited) | Per-run cost ceiling in USD |
| `HARNESS_PROVIDER` | (catalog) | Set to `fake` for key-free smoke testing |
| `HARNESS_WORKSPACE` | `.` | Workspace root for the agent |

---

## Local-first, optional remote

<Callout variant="warning" title="Cloud and relay features are optional extensions">
  go-code is designed local-first. The Go Relay control plane (worker registration, multi-location dispatch) and the Symphony orchestrator (workspace pools, GitHub issue queues) are implemented components, but they are optional additions that require additional setup. For most use cases you only need <code>harnessd</code> running locally. Do not expect remote execution to work out of the box.
</Callout>

---

## Where to go next

<div style={{display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))', gap: '1rem', marginTop: '1rem'}}>
  <Card>
    <CardHeader>
      <CardTitle>Quickstart</CardTitle>
    </CardHeader>
    <CardContent>
      Install go-code, start harnessd with the fake provider, then run your first real prompt. <a href="/docs/getting-started/quickstart">Get started →</a>
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>Core Concepts</CardTitle>
    </CardHeader>
    <CardContent>
      The run lifecycle, SSE event model, provider routing, and workspace isolation explained. <a href="/docs/concepts">Read the concepts →</a>
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>CLI Reference</CardTitle>
    </CardHeader>
    <CardContent>
      Every flag for <code>go-code</code>, <code>harnesscli</code>, and <code>harnessd</code>. <a href="/docs/cli">Browse the CLI docs →</a>
    </CardContent>
  </Card>
</div>
