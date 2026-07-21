---
title: "CLI Flag Reference"
sidebar_label: "CLI Flags"
sidebar_position: 1
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

This page is the exhaustive flag and subcommand reference for every binary in the go-code suite: `harnessd`, `harnesscli`, `go-code`, `cronctl`, `cronsd`, `symphd`, `trainerd`, `forensics`, and `install.sh`.

Use it to look up the exact flag name, its type, its default, and what it controls. When you need the "why" behind a flag or a worked example, the linked concept pages go deeper.

<Callout type="info">
Go's `flag` package accepts both single-dash and double-dash forms for every flag — `-flag value` and `--flag value` are equivalent. The fact sheets and source code use a single dash in most examples; both forms work at runtime.
</Callout>

---

## `harnessd` — the HTTP daemon

`harnessd` (`cmd/harnessd`) is the long-running HTTP daemon that boots the full agent runtime (LLM provider, tools, cron, MCP, workflows) and exposes it over a REST + SSE API. It has only three CLI flags; everything else is controlled by environment variables or TOML config files.

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--profile` | string | `""` | Named profile to load from `~/.harness/profiles/<name>.toml` (or `.harness/profiles/<name>.toml` in the project dir). Applies on top of the user and project TOML layers, before env vars. |
| `--mcp` | bool | `false` | Start as an MCP stdio server instead of an HTTP server. Reads stdin and writes stdout; does not bind a TCP port. |
| `--mcp-workspace` | string | `""` (resolves to `$PWD`) | Workspace root used when `--mcp` is active. Falls back to `HARNESS_WORKSPACE`, then `.`. |

Source: `cmd/harnessd/main.go:170-178`

### Key environment variables

`harnessd` is primarily configured through environment variables and TOML files. The most commonly needed env vars are listed here; for the full cascade see the [Configuration reference](/docs/concepts/configuration).

| Variable | Default | Description |
|---|---|---|
| `HARNESS_ADDR` | `:8080` | HTTP listen address (e.g. `:9000` or `127.0.0.1:8080`). |
| `HARNESS_MODEL` | `"gpt-4.1-mini"` | Default LLM model identifier. |
| `HARNESS_MAX_STEPS` | `8` | Maximum tool-call steps per run. Set to `0` in config for unlimited; the daemon resets a `0` config default back to `8` for backward compatibility. |
| `HARNESS_WORKSPACE` | `.` | Workspace root path. |
| `HARNESS_PROVIDER` | (catalog) | Set to `"fake"` for key-free deterministic smoke testing. |
| `HARNESS_FAKE_TURNS` | `""` | Path to the JSON turns file when `HARNESS_PROVIDER=fake`. Required when the fake provider is active. |
| `HARNESS_AUTH_DISABLED` | `""` | Set to `"true"` to disable Bearer token auth. Auth is also implicitly off when `HARNESS_RUN_DB` is not set (no key store to validate against). |
| `OPENAI_API_KEY` | `""` | OpenAI API key. The primary provider path; required unless a catalog provider or `HARNESS_PROVIDER=fake` is configured. |

Source: `cmd/harnessd/main.go:319-432`, `internal/server/auth.go:228-230`, `internal/server/auth.go:77-81`, `cmd/harnessd/bootstrap_helpers.go:259-273`

### MCP stdio mode

```bash
./harnessd --mcp --mcp-workspace /path/to/project
```

When `--mcp` is set, harnessd communicates over stdin/stdout using the MCP protocol instead of binding a port. The REST API and the `/mcp` HTTP endpoint are both unavailable in this mode.

---

## `harnesscli` — the CLI client

`harnesscli` (`cmd/harnesscli`) is the terminal client for `harnessd`. It dispatches to one of several subcommands based on the first argument. When no recognized subcommand is given it falls through to default run mode.

### Default / bare-run mode

When no subcommand is present, `harnesscli` creates a run via `POST /v1/runs` and streams SSE events until a terminal event (`run.completed`, `run.failed`, or `run.cancelled`) arrives.

Source: `cmd/harnesscli/main.go:123`

| Flag | Type | Default | Description |
|---|---|---|---|
| `-base-url` | string | `http://localhost:8080` | Harness API base URL. |
| `-prompt` | string | `""` | Prompt text to send. Required unless `-tui` or `-list-profiles` is set. |
| `-model` | string | `""` | Model override for this run. |
| `-system-prompt` | string | `""` | System prompt override for this run. |
| `-agent-intent` | string | `""` | Startup intent for prompt routing (e.g. `code_review`, `general`). |
| `-task-context` | string | `""` | Task context injected into the startup prompt. |
| `-prompt-profile` | string | `""` | Prompt profile override for model routing. |
| `-prompt-custom` | string | `""` | Custom prompt extension text appended to the prompt. |
| `-workspace` | string | `""` (resolves to cwd) | Workspace directory for this run. Resolved via `os.Getwd()` when empty. |
| `-tui` | bool | `false` | Launch the interactive BubbleTea TUI. Requires a real terminal — fails with an error if stdout is a pipe. |
| `-list-profiles` | bool | `false` | Fetch and print available profiles, then exit. |
| `-prompt-behavior` | csvListFlag | (empty) | Behavior extension IDs. Accepts comma-separated values or repeated flags. |
| `-prompt-talent` | csvListFlag | (empty) | Talent extension IDs. Accepts comma-separated values or repeated flags. |

Source: `cmd/harnesscli/main.go:127-141`

**Streaming output format (non-TUI):**

```
run_id=<uuid>
<event_type> <json_payload>
...
terminal_event=<run.completed|run.failed|run.cancelled>
```

**Exit codes (non-TUI streaming):** `0` on `run.completed`, `2` on `run.failed`, `6` on `run.cancelled`, `3` when the run blocks on input while stdin is non-interactive, `1` on client/transport errors, `130` on SIGINT/SIGTERM. Full contract: [Exit Codes](/docs/reference/exit-codes).

### `list` / `runs` subcommand

Both `list` and `runs` tokens route to `runList`. Calls `GET /v1/runs`.

| Flag | Default | Description |
|---|---|---|
| `-base-url` | `http://localhost:8080` | Harness API base URL. |
| `-status` | `""` | Filter by status: `queued`, `running`, `completed`, or `failed`. |
| `-conversation-id` | `""` | Filter by conversation ID. |

Output: a 4-column table (ID, STATUS, MODEL, PROMPT). Prompt is truncated at 40 characters.

Source: `cmd/harnesscli/runctl.go:53-58`

### `cancel` subcommand

Takes one positional argument: the run ID. Calls `POST /v1/runs/{id}/cancel`.

| Flag | Default | Description |
|---|---|---|
| `-base-url` | `http://localhost:8080` | Harness API base URL. |

Source: `cmd/harnesscli/runctl.go:118-121`

### `status` / `show` subcommand

Both `status` and `show` tokens route to `runStatus`. Takes one positional argument: the run ID. Calls `GET /v1/runs/{id}`.

| Flag | Default | Description |
|---|---|---|
| `-base-url` | `http://localhost:8080` | Harness API base URL. |

Source: `cmd/harnesscli/runctl.go:174-177`

### `continue` subcommand

Takes a run ID as the first positional argument and the continuation prompt as the remaining arguments (joined with spaces). Calls `POST /v1/runs/{id}/continue`.

| Flag | Default | Description |
|---|---|---|
| `-base-url` | `http://localhost:8080` | Harness API base URL. |
| `-no-stream` | `false` | Create the continuation run without streaming events. Prints only `run_id=<id>` and exits `0` (`1` on error); no terminal event is observed, so the exit-code mapping does not apply. |

Source: `cmd/harnesscli/runctl.go:260-264`

### `replay` subcommand

Takes one positional argument: a run ID or a path to a rollout JSONL file. Calls `POST /v1/runs/replay`.

| Flag | Default | Description |
|---|---|---|
| `-base-url` | `http://localhost:8080` | Harness API base URL. |
| `-mode` | `simulate` | Replay mode: `simulate` (offline integrity check) or `fork` (reconstruct up to a step and resume live). |
| `-fork-step` | `0` | Step to fork from when `-mode=fork`. Only included in the request payload when mode is `fork`. |
| `-detect-drift` | `false` | Run drift detection during a `simulate` replay. |

Output is pretty-printed JSON to stdout.

Source: `cmd/harnesscli/runctl.go:334-340`

### `search` subcommand

Takes one or more positional arguments as the query (joined with spaces). Fetches all runs from `GET /v1/runs` and filters client-side — there is no dedicated server search endpoint.

| Flag | Default | Description |
|---|---|---|
| `-base-url` | `http://localhost:8080` | Harness API base URL. |
| `-status` | `""` | Filter by status before searching. |

Search is a case-insensitive substring match over: ID, conversation ID, tenant ID, model, prompt, output, status, error, and workflow recap fields.

Source: `cmd/harnesscli/runctl.go:403-407`

### `improve` subcommand

Delegates to `scripts/autoresearch-loop.sh`. Must be run from the repo root.

| Flag | Default | Description |
|---|---|---|
| `-target` | (empty, repeatable) | Target seam to inspect. May be repeated. |
| `-dry-run` | `false` | Print the planned autoresearch command without running it. |
| `-score-only` | `false` | Run `go test ./...`, `go test ./... -race`, and `./scripts/test-regression.sh`, then exit. |
| `-iterations` | `"1"` | Autoresearch loop iteration count. |
| `-pause` | `"0"` | Seconds to pause between autoresearch runs. |
| `-report-dir` | `.tmp/autoresearch` | Directory for autoresearch report output. |
| `-base-url` | `http://localhost:8080` | Harness API base URL. |
| `-profile` | `"full"` | Run profile sent to harnessd. |
| `-prompt-profile` | `"autoresearch"` | Prompt routing profile. |
| `-model` | `""` | Optional model override. |
| `-max-steps` | `"50"` | Step budget passed to autoresearch runs. |
| `-test-cmd` | `./scripts/test-regression.sh` | Default validation command for unknown targets. |

The `-test-cmd` value is also exported as `HARNESS_AUTORESEARCH_DEFAULT_TEST_CMD` when launching the loop script.

Source: `cmd/harnesscli/improve.go:31-43`

### `auth login` subcommand

Generates an API key locally (no server call) and saves it to `~/.harness/config.json`.

| Flag | Default | Description |
|---|---|---|
| `-server` | `http://localhost:8080` | Harness server URL stored in the config file. |
| `-tenant` | `"default"` | Tenant ID for the new key. |
| `-name` | `"cli"` | Human-readable name for this key. |

On success, prints the config path, the raw API key, and an `Authorization: Bearer <key>` example.

Source: `cmd/harnesscli/auth.go:32-37`

<Callout type="warning">
`auth login` does not contact the server. The key is generated locally and stored at `~/.harness/config.json` (permissions: directory `0700`, file `0600`). The server URL you pass is stored as metadata only — no validation occurs.
</Callout>

---

## `go-code` — the user-facing wrapper

`go-code` (`scripts/go-code.sh`) is the single user-facing command. It auto-starts `harnessd` when no server is healthy at the configured port, delegates to `harnesscli` for all subcommands, and shuts the server down on exit only if it started it.

### Invocation modes

| Invocation | Mode | What it does |
|---|---|---|
| `go-code` | `tui` | Launches the BubbleTea TUI. |
| `go-code "prompt text"` | `prompt` | Runs a single prompt and streams events. |
| `go-code --server` | `server` | Starts harnessd in background, prints the URL, and exits. |
| `go-code runs` | `list` | Lists known runs (alias: `go-code list`). |
| `go-code show <run-id>` | `status` | Shows one run (alias: `go-code status <run-id>`). |
| `go-code cancel <run-id>` | `cancel` | Cancels a run. |
| `go-code continue <run-id> "prompt"` | `continue` | Continues a completed run and streams events. |
| `go-code replay <run-id-or-path>` | `replay` | Replays a recorded run. |
| `go-code search <query>` | `search` | Searches run metadata. |
| `go-code improve [--target seam]` | `improve` | Runs or plans the self-improvement test loop. |

Source: `scripts/go-code.sh:31-56`, `scripts/go-code.sh:210-282`

### Relevant environment variables

| Variable | Default | Description |
|---|---|---|
| `HARNESS_ADDR` | `:8080` | Server listen address. The port is extracted and used to construct the base URL passed to `harnesscli`. |
| `GO_CODE_DATA_DIR` | (auto-detected) | Override the runtime asset root (prompts, catalog). |
| `HARNESS_MODEL_CATALOG_PATH` | (auto-detected) | Path to `catalog/models.json`. |

Source: `scripts/go-code.sh:23-24`, `scripts/go-code.sh:76-79`, `scripts/go-code.sh:99`

---

## `cronctl` — cron job management CLI

`cronctl` (`cmd/cronctl`) is the CLI client for `cronsd`. It communicates over HTTP.

### Environment variable

| Variable | Default | Description |
|---|---|---|
| `CRONSD_URL` | `http://localhost:9090` | Base URL of the cronsd service. |

Source: `cmd/cronctl/main.go:34-36`

### Subcommands and flags

| Subcommand | Required flags | Optional flags | Description |
|---|---|---|---|
| `create` | `--name`, `--schedule`, `--command` | `--type` (default `shell`), `--timeout` (default `30`) | Create a recurring job. `--schedule` accepts a standard 5-field UTC cron expression (no seconds field). |
| `list` | — | — | List all jobs in tabular format. |
| `get <id-or-name>` | — | — | Get a single job by ID or name. |
| `delete <id-or-name>` | — | — | Soft-delete a job. |
| `history <id-or-name>` | — | `--limit` (default `20`) | List execution history for a job. |
| `pause <id-or-name>` | — | — | Pause a job (sets status to `"paused"`). |
| `resume <id-or-name>` | — | — | Resume a paused job (sets status to `"active"`). |
| `health` | — | — | Check cronsd health. |

Source: `cmd/cronctl/main.go:41-96`

**Example:**

```bash
cronctl create \
  --name daily-report \
  --schedule "0 9 * * 1-5" \
  --command "curl -s http://localhost:8080/v1/runs -d '{}' -H 'Content-Type: application/json'" \
  --timeout 60
```

<Callout type="warning">
Cron expressions are 5-field UTC only (Minute Hour DOM Month DOW). The `robfig/cron/v3` parser does not accept a seconds prefix — 6-field expressions will fail validation.
</Callout>

---

## `cronsd` — cron scheduling daemon

`cronsd` (`cmd/cronsd`) is the standalone cron daemon. It owns a SQLite database and exposes a REST API. `harnessd` embeds an equivalent scheduler by default (when `HARNESS_CRON_URL` is empty) so you only need `cronsd` when running a dedicated external cron service.

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `CRONSD_ADDR` | `:9090` | Listen address. |
| `CRONSD_DB_PATH` | `~/.go-harness/cronsd.db` | SQLite database file path. |
| `CRONSD_MAX_CONCURRENT` | `5` | Maximum simultaneous job executions. |

Source: `cmd/cronsd/main.go:65-69`

`cronsd` has no CLI flags beyond the implicit help output. All configuration is via environment variables.

---

## `symphd` — issue-driven orchestration daemon

`symphd` (`cmd/symphd`) is an orchestration daemon that polls GitHub Issues, provisions workspaces, dispatches agent runs, and applies retry-with-backoff. It is configured primarily through a YAML file.

### CLI flags

| Flag | Description |
|---|---|
| `-config <path>` | Path to the YAML config file. |
| `-addr <addr>` | Override the listen address (e.g. `:8888`). |

Source: `cmd/symphd/main.go:32-33`

### YAML config keys (with defaults)

The YAML config file controls all orchestration behavior. Below are the keys and their built-in defaults.

| Key | Default | Description |
|---|---|---|
| `addr` | `":8888"` | HTTP listen address for the symphd API. |
| `workspace_type` | `"local"` | Workspace type: `local`, `worktree`, `container`, `vm`, or `pool`. |
| `max_concurrent_agents` | `10` | Maximum simultaneously running agent dispatches. |
| `poll_interval_ms` | `5000` | GitHub Issue poll interval in milliseconds. |
| `harness_url` | `"http://localhost:8080"` | URL of the `harnessd` instance inside each workspace. |
| `base_dir` | `"$TMPDIR/symphd"` | Base directory for workspace provisioning. |
| `track_label` | `"symphd"` | GitHub Issue label that marks issues for dispatch. |
| `retry_max_attempts` | `5` | Maximum dispatch attempts per issue before dead-lettering. |
| `retry_base_delay_ms` | `10000` | Base backoff delay in milliseconds (10 seconds). |
| `retry_max_delay_ms` | `300000` | Maximum backoff delay in milliseconds (5 minutes). |
| `pool_size` | `3` | Number of pre-provisioned workspaces when `workspace_type: pool`. |
| `pool_workspace_type` | `"container"` | Type of workspace each pool slot uses. |

Source: `internal/symphd/config.go:69-108`

### Environment variables

| Variable | Description |
|---|---|
| `GITHUB_TOKEN` | GitHub API token. Used when `github_token` is not set in the config file. |
| `HETZNER_API_KEY` | Required when `workspace_type: vm`. |
| `OPENAI_API_KEY` | Forwarded to each provisioned workspace's environment. |
| `ANTHROPIC_API_KEY` | Forwarded to each provisioned workspace's environment. |
| `HARNESS_MODEL` | Forwarded to each provisioned workspace's environment. |

API keys are never written to disk or to TOML — they are captured from the parent process at startup and forwarded via `workspace.Options.Env`.

Source: `internal/symphd/config.go:91-93`, `internal/symphd/orchestrator.go:267-284`

---

## `trainerd` — training and scoring CLI

`trainerd` (`cmd/trainerd`) reads JSONL rollout files produced by the harness, computes structural metrics, optionally sends batches to Claude for analysis, and can auto-apply high-confidence findings to system prompts or tool descriptions on a git branch.

### Persistent flags (all subcommands)

| Flag | Default | Description |
|---|---|---|
| `--db-path` | `~/.trainerd/training.db` | SQLite database path for traces, findings, and applied changes. |
| `--log-level` | `info` | Log level. |

Source: `cmd/trainerd/main.go:38-39`

### Subcommands

<Tabs defaultValue="score">
<TabsList>
<TabsTrigger value="score">score</TabsTrigger>
<TabsTrigger value="analyze">analyze</TabsTrigger>
<TabsTrigger value="loop">loop</TabsTrigger>
<TabsTrigger value="status">status</TabsTrigger>
<TabsTrigger value="history">history</TabsTrigger>
</TabsList>
<TabsContent value="score">

**`trainerd score`** — load a JSONL rollout file and compute structural scores.

| Flag | Required | Default | Description |
|---|---|---|---|
| `--run-id <id>` | yes | — | Run ID to score. Reads the rollout from `HARNESS_ROLLOUT_DIR/<date>/<run_id>.jsonl`. |
| `--rollout-dir` | no | `~/.trainerd/rollouts` | Directory containing rollout JSONL files. Overridden by `HARNESS_ROLLOUT_DIR`. |

Source: `cmd/trainerd/commands.go:16-35`

</TabsContent>
<TabsContent value="analyze">

**`trainerd analyze`** — send one or more trace bundles to Claude for deeper analysis.

| Flag | Required | Default | Description |
|---|---|---|---|
| `--run-ids <comma-list>` | yes | — | Comma-separated list of run IDs to analyze. |
| `--rollout-dir` | no | `~/.trainerd/rollouts` | Directory containing rollout JSONL files. Overridden by `HARNESS_ROLLOUT_DIR`. |
| `--output-format` | no | `text` | Output format: `text` or `json`. |

Requires `ANTHROPIC_API_KEY`. Without it, `analyze` returns an error.

Source: `cmd/trainerd/commands.go:64-83`

</TabsContent>
<TabsContent value="loop">

**`trainerd loop`** — iterate all JSONL files, score all, and optionally analyze with Claude.

| Flag | Default | Description |
|---|---|---|
| `--task-set` | `all` | Task set filter. |
| `--trainer` | `claude-opus` | Trainer name. Accepted but does not change the model used; the model is fixed to the package default `claude-opus-4-6` (const at `claude_trainer.go:15`, applied by `NewClaudeTrainer`); the `--trainer` flag value is never passed to `WithModel()`. |
| `--dry-run` | `false` | Score without persisting or analyzing. |
| `--rollout-dir` | `~/.trainerd/rollouts` | Directory containing JSONL rollout files. |

If `ANTHROPIC_API_KEY` is absent, the loop skips Claude analysis and performs structural scoring and DB save only.

Source: `cmd/trainerd/commands.go:165-168`

</TabsContent>
<TabsContent value="status">

**`trainerd status`** — print counts for traces, findings, and applied changes from the SQLite database.

No additional flags beyond the persistent flags.

Source: `cmd/trainerd/commands.go`

</TabsContent>
<TabsContent value="history">

**`trainerd history`** — print applied changes since a given date.

| Flag | Required | Description |
|---|---|---|
| `--since YYYY-MM-DD` | yes | Show applied changes on or after this date. |

Source: `cmd/trainerd/commands.go`

</TabsContent>
</Tabs>

<Callout type="warning">
The `--trainer` flag on `trainerd loop` is accepted but has no effect on which model is used. The model is fixed to the package default `claude-opus-4-6` (const `defaultModel` at `internal/training/claude_trainer.go:15`, applied by `NewClaudeTrainer`); the `--trainer` flag value is never passed to `WithModel()`. Source: `internal/training/claude_trainer.go:13-18`, `cmd/trainerd/commands.go:103,249`.
</Callout>

---

## `forensics` — rollout diffing CLI

`forensics` (`cmd/forensics`) has one subcommand: `diff`. It compares two JSONL rollout files and scores which run performed better.

### `forensics diff`

```bash
forensics diff <rollout_a.jsonl> <rollout_b.jsonl>
```

Takes two positional arguments: paths to JSONL rollout files. No flags.

**Output format:**

```
Run A: <N> steps, $<cost>
Run B: <N> steps, $<cost>
Steps: <N> identical, <N> diverged, <N> only in A, <N> only in B
Winner: A|B|Tie (<reasons>)
```

The winner is determined by a regression scorer that awards points for: outcome (3 pts), error count (1 pt), cost (1 pt), and step count (1 pt).

Source: `cmd/forensics/main.go:5-6`, `cmd/forensics/main.go:36-138`

---

## `install.sh` — source installer

`scripts/install.sh` installs `harnessd`, `harnesscli`, and the `go-code` wrapper into a user-accessible location. The default target is `~/.local/bin` (no sudo required).

### Flags

| Flag | Description |
|---|---|
| `--prefix DIR` | Install under `DIR/bin`. Default: `~/.local`. |
| `--bin-dir DIR` | Direct binary destination (overrides the `bin/` subdirectory of `--prefix`). |
| `--data-dir DIR` | Override the runtime assets destination (prompts, catalog). |
| `--system` | Install to `/usr/local/bin`. May require sudo. |
| `--add-to-path` | Append the install directory to your shell profile. |
| `--no-build` | Reuse pre-built `./harnesscli` and `./harnessd` binaries in the repo root instead of running `go build`. |
| `--uninstall` | Remove `go-code`, `harnesscli`, `harnessd`, and the data directory. |
| `--dry-run` | Print what would happen; write nothing to disk. |

Source: `scripts/install.sh:23-48`

### Environment variable overrides (checked before flag parsing)

| Variable | Overrides |
|---|---|
| `GO_CODE_PREFIX` | `--prefix` default |
| `GO_CODE_BINDIR` | `--bin-dir` |
| `GO_CODE_DATA_DIR` | `--data-dir` |

Source: `scripts/install.sh:15-17`

---

## `scripts/init.sh` — worktree bootstrap

`scripts/init.sh` is the canonical way to create a new isolated development worktree. It creates the worktree, downloads dependencies, builds local binaries, and writes a sourceable env file.

```bash
scripts/init.sh [options] <task-slug>
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `--base-ref <ref>` | `main` | Base git ref for the new worktree. |
| `--branch <name>` | `codex/<task-slug>` | Branch name. The `codex` prefix can be overridden with `INIT_BRANCH_PREFIX`. |
| `--worktree-root <dir>` | `.codex-worktrees` | Directory where worktrees are stored. |
| `--session <name>` | — | Start harnessd in a tmux session with this name. |
| `--start-server` | — | Start harnessd in tmux after bootstrapping. |
| `--skip-build` | — | Skip the `go build` step. |
| `--skip-download` | — | Skip `go mod download`. |
| `--check` | — | Verify prerequisites and exit. |

Source: `scripts/init.sh:11-32`

---

## Next steps

- [Configuration reference](/docs/concepts/configuration) — the full 6-layer TOML + env var cascade and every `HARNESS_*` variable.
- [HTTP API reference](/docs/server/http-api-guide) — all REST routes, request bodies, and response shapes.
- [Event Catalog](/docs/reference/events-catalog) — every SSE event type, terminal events, and the streaming wire format.
- [Key-free testing](/docs/getting-started/key-free-testing) — running the fake provider smoke end-to-end.
