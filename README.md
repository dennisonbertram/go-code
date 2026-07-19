<p align="center">
  <img src="docs/assets/go-code-watercolor-hero.png" alt="Watercolor illustration of a terminal coding agent workspace" width="100%">
</p>

# go-code

`go-code` is a local-first coding agent written in Go. One installed command gives you the same agent three ways: an interactive TUI for daily development, a single-shot CLI for scripting, and a streamed HTTP API for building on top of — all running in the repository where you launched it, against the model provider you choose.

There is no hosted service or separate control plane. The TUI and CLI are thin clients over one public HTTP/SSE API: `go-code` auto-starts the local `harnessd` daemon when needed, streams every model turn, tool call, and approval as events, and records runs so you can inspect, continue, replay, and search them later.

## Use the TUI

Install the latest `main` build with Homebrew and launch it from any repository:

```bash
brew install --HEAD dennisonbertram/go-code/go-code
export OPENAI_API_KEY="..."
go-code
```

The TUI runs the agent in the project directory where you launched it, streaming assistant output and tool activity as it works. A daemon is started automatically if one is not already running; a pre-existing server is left alone.

If you do not use Homebrew, install from source:

```bash
git clone https://github.com/dennisonbertram/go-code.git
cd go-code
./scripts/install.sh --add-to-path
```

Then open a new shell, or add the printed PATH line to your current one.

## Script it from the shell

For one-shot prompts and run management, the same command works headlessly:

```bash
go-code "summarize this repo"    # single-shot prompt, streamed to stdout
go-code runs                     # list known runs
go-code show <run-id>            # inspect one run
go-code continue <run-id> "..."  # continue and stream a completed run
go-code replay <run-id>          # replay a recorded run when rollout capture is enabled
go-code search <query>           # search run metadata
go-code improve --dry-run        # plan the self-improvement test loop
```

When persistence is enabled, completed runs keep a searchable workflow recap — goal, changed files, tests run, failure cause, fix pattern, useful commands, and a continuation prompt. `go-code search <query>` matches those recap fields.

## Build on the API

Everything the TUI does goes through the daemon's streamed run API, which you can use directly:

```bash
go-code --server                 # start harnessd in the background and print its URL
```

The most commonly used endpoints:

```text
GET  /healthz
GET  /v1/models
GET  /v1/providers
POST /v1/runs
GET  /v1/runs/{id}/events
POST /v1/runs/{id}/continue
POST /v1/runs/{id}/steer
POST /v1/runs/{id}/compact
POST /v1/runs/{id}/cancel
GET  /v1/conversations/
GET  /v1/skills
GET  /v1/subagents
POST /v1/subagents
```

Run requests support prompt, model, provider, workspace, sandbox, approval, tool, profile, reasoning, and budget fields. Canonical event names live in `internal/harness/events.go`.

## Pick your providers

Set keys for the providers you plan to use — only those:

```bash
export OPENAI_API_KEY="..."
export ANTHROPIC_API_KEY="..."
export GOOGLE_API_KEY="..."
export DEEPSEEK_API_KEY="..."
export ZAI_API_KEY="..."
```

The provider catalog covers OpenAI, Anthropic, Google, DeepSeek, Z.ai, and OpenRouter-style routes, with local catalog pricing for cost tracking. Keys can also be configured through the TUI and server APIs.

## Develop this repository

The public command is `go-code`; the internal Go module is still named `go-agent-harness` while the product surface settles.

For development or debugging, run the server and CLI directly:

```bash
go run ./cmd/harnessd
go run ./cmd/harnesscli -base-url http://127.0.0.1:8080 -prompt "Summarize the repository"
```

Long-running local servers should be started in tmux:

```bash
tmux new-session -d -s go-code-server 'cd /path/to/go-code && go run ./cmd/harnessd'
tmux attach-session -t go-code-server
```

### Layout

The core of the system:

- `cmd/harnesscli`: command-line client and terminal UI.
- `cmd/harnessd`: local HTTP daemon and runtime bootstrap.
- `internal/harness`: run loop, tools, event emission, and conversation behavior.
- `internal/server`: HTTP API handlers.
- `internal/provider`: provider clients, model catalogs, pricing, and routing.
- `internal/workspace`: local, container, VM, and worktree workspace implementations.
- `catalog/`: model and pricing catalogs used at runtime.
- `docs/`: runbooks, design notes, logs, Pages source, and project context.

Supporting directories: `prompts/` (bundled prompt assets), `apps/` (experimental integrations), `benchmarks/` and `harness_agent/` (Terminal Bench harnesses; Python), `skills/` (bundled skill fixtures), `demo/` (static demos), `build/` (packaging assets), `testdata/` (shared fixtures), `playground/` (separate-module experiments), and `scripts/` (install, development, Symphony, and regression helpers). The repo root is kept for product entrypoints and project metadata; scratch snippets belong under `playground/` or a dedicated test fixture.

### Testing

Focused checks for the install and TUI path:

```bash
bash -n scripts/install.sh scripts/go-code.sh
HOME=$(mktemp -d) GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... -count=1
```

Broader regression:

```bash
GOCACHE=/tmp/go-build ./scripts/test-regression.sh
```

Follow `docs/runbooks/testing.md` for strict TDD expectations, behavior tests, regression tests, and merge gates.

### Documentation

- Public page source: `docs/site/`
- Distribution runbook: `docs/runbooks/distribution.md`
- TUI visual testing: `docs/runbooks/tui-visual-testing.md`
- Symphony issue authoring: `docs/runbooks/symphony-issue-authoring.md`
- Worktree workflow: `docs/runbooks/worktree-flow.md`
- Full docs index: `docs/INDEX.md`

The public project page is:

```text
https://dennisonbertram.github.io/go-code/
```
