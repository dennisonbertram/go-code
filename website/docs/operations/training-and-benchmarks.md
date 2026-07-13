---
title: "Training and Benchmarks"
sidebar_label: "Training & Benchmarks"
sidebar_position: 2
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardContent } from '@site/src/components/ui';

The training and benchmark system gives you two ways to measure and improve agent quality. `trainerd` is a Go CLI that scores and analyzes run traces produced by the harness — without ever re-running the agent. The benchmark harnesses (a key-free Go smoke, a Terminal-Bench integration, an overnight loop, and a comparison harness) let you measure task-completion accuracy across real workloads. Together they form a feedback loop: run tasks, capture rollout files, score them, find anti-patterns, apply fixes to prompts or tool descriptions, and guard against regressions.

---

## trainerd

`trainerd` reads JSONL rollout files produced by the harness (stored at `HARNESS_ROLLOUT_DIR`, default `~/.trainerd/rollouts`), computes structural quality metrics, and can optionally ask Claude to produce deeper findings. Results are persisted in a SQLite database at `--db-path` (default `~/.trainerd/training.db`).

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

Load a single rollout file, compute structural metrics, and print results to stdout. No API key required.

```bash
trainerd score --run-id <run-id>
```

`score` measures **tool quality** and **efficiency** using only the JSONL trace — it never calls an LLM. See [Scoring formulas](#scoring-formulas) below for exactly what it computes.

  </TabsContent>

  <TabsContent value="analyze">

Send one or more rollout traces to Claude for deeper analysis. Requires `ANTHROPIC_API_KEY`.

```bash
# Single run
trainerd analyze --run-ids <run-id>

# Multiple runs (analyzed as a batch)
trainerd analyze --run-ids <id-1>,<id-2>,<id-3>
```

A single run ID calls `Analyze`; multiple IDs call `AnalyzeBatch`. Claude is called at `POST https://api.anthropic.com/v1/messages` with model `claude-opus-4-6` and `max_tokens: 4096`.

  </TabsContent>

  <TabsContent value="loop">

Iterate all JSONL files in the rollout directory, score each one, persist findings to the database, and (if `ANTHROPIC_API_KEY` is set) run Claude analysis and save findings.

```bash
trainerd loop
trainerd loop --dry-run          # score only, skip analysis and skip all database writes
trainerd loop --task-set all     # default; filter to a named task set
```

If `ANTHROPIC_API_KEY` is absent, `loop` still runs structural scoring and saves to the database — it just skips the Claude analysis step.

<Callout type="info">
`trainerd loop` stops at saving findings to the SQLite database. Auto-apply (the `Applier`) and `RegressionGuard` are library primitives in `internal/training` — they are not wired into any CLI subcommand and cannot be triggered from `trainerd loop`.
</Callout>

  </TabsContent>

  <TabsContent value="status">

Print summary counts from the database — how many traces, findings, and applied changes are stored.

```bash
trainerd status
```

  </TabsContent>

  <TabsContent value="history">

Print applied changes since a given date.

```bash
trainerd history --since 2026-01-01
```

  </TabsContent>
</Tabs>

### Persistent flags

These flags apply to every subcommand:

| Flag | Default | Description |
|------|---------|-------------|
| `--db-path` | `~/.trainerd/training.db` | SQLite database path |
| `--log-level` | `info` | Log verbosity |

The rollout directory defaults to `~/.trainerd/rollouts` and can be overridden with `HARNESS_ROLLOUT_DIR`.

<Callout type="warning">
The `--trainer` flag on `loop` is accepted by the CLI but does not change the model that is actually called. The model is hardcoded as `claude-opus-4-6` inside `NewClaudeTrainer()`. Passing a different value to `--trainer` has no effect.
</Callout>

### Scoring formulas

`trainerd score` computes two structural metrics without an LLM:

- **Tool quality** = `FirstTryRate × (1 − antiPatternPenalty)`, where `antiPatternPenalty = min(1, weightedSum / 5)`.
- **Efficiency** = `1.0 / (1.0 + steps×0.1 + costUSD×10)`, capped to `[0, 1]`.

Anti-pattern weights used to compute `weightedSum`:

| Anti-pattern | Weight |
|---|---|
| `retry_loop` | 0.5 |
| `hedge_assertion` | 1.0 |
| `unverified_file_claim` | 1.0 |
| `premature_completion` | 1.25 |
| `skipped_diagnostic` | 1.0 |
| `architecture_assumption` | 1.0 |
| (any other) | 1.0 |

### Applier library (internal/training)

`trainerd loop` saves findings to the database but does not apply them to the repo. The `Applier` type in `internal/training/applier.go` is a library primitive for consumers that want to automate changes. It is not instantiated by any CLI subcommand.

Auto-apply eligibility (as implemented in `Applier`) is strict: confidence must be `CERTAIN`, evidence count must be at least 3 (the `MinEvidenceCount` default), priority must not be `"critical"`, and type must be `"system_prompt"` or `"tool_description"`.

When a finding qualifies:

- `system_prompt` findings create a new `.md` file under `prompts/behaviors/` named `training-<target>-<timestamp>.md`.
- `tool_description` findings append to `internal/harness/tools/descriptions/<target>.md`.
- Changes land on a git branch named `training/auto-<YYYY-MM-DD>-<8-hex>`.

### RegressionGuard library (internal/training)

`RegressionGuard` in `internal/training/regression.go` is a library primitive — it is not called by any CLI subcommand. It is available for custom tooling that wants to gate auto-applied changes behind a benchmark run:

| Condition | Action |
|---|---|
| Accuracy drop `>` 5% | `Revert` — deletes the training branch (`git branch -D <branch>`) |
| Cost rise `>` 15% | `flag` |
| Step count rise `>` 20% | `flag` |

Note: `Revert` deletes the training branch outright; it does not produce a `git revert` commit.

The benchmark command is configured via `RegressionConfig.BenchmarkCmd` or the `HARNESS_BENCHMARK_CMD` env var. The baseline metrics come from `benchmarks/terminal_bench/baseline.json` (loaded by `LoadBaseline`).

---

## Key-free smokes

The fastest way to verify the harness without any API key or Docker is the two-tier smoke suite. Both tiers test the same deterministic run: one fake LLM turn that returns `"smoke ok"` with 100 prompt tokens, 50 completion tokens, and `total_cost_usd = 0.001`.

<Callout type="info">
`total_cost_usd` in the smoke output is **derived** — it is accumulated by the runner from the scripted `Cost.TotalUSD` value, not a raw field returned by a provider API.
</Callout>

### 1. In-process Go test

```bash
# Fast: no race detector
go test ./internal/server/... -run TestRunSmoke

# Thorough: with race detector
go test ./internal/server/... -race -count=1 -run TestRunSmoke
```

`TestRunSmoke` exercises the full run API in-process: `POST /v1/runs` → poll `GET /v1/runs/{id}` → `GET /v1/runs/{id}/summary`. No Docker, no network, no API key. It also validates the `benchresult.FromRun` artifact round-trip.

Expected summary fields:

| Field | Expected value |
|---|---|
| `status` | `"completed"` |
| `steps_taken` | `1` |
| `total_prompt_tokens` | `100` |
| `total_completion_tokens` | `50` |
| `total_cost_usd` | `0.001` |
| `cost_status` | `"available"` |

### 2. Shell smoke

```bash
bash scripts/run-bench-smoke.sh
```

The shell smoke builds a real `harnessd` binary, starts it with `HARNESS_PROVIDER=fake`, and makes 13 PASS assertions against the live HTTP API (6 lifecycle steps plus 7 summary-field assertions). Requirements: Go toolchain, `curl`, `python3`. No API key.

When all assertions pass you see (abridged — the actual output includes a "Bench Smoke Summary" box between the last PASS line and the final message):

```text
[bench-smoke] PASS: wrote fake turns file
[bench-smoke] PASS: harnessd built: ...
[bench-smoke] PASS: /healthz responding
[bench-smoke] PASS: POST /v1/runs → run_id=<uuid>
[bench-smoke] PASS: run terminal status: completed
[bench-smoke] PASS: summary fetched
[bench-smoke] PASS: summary.run_id=<uuid>
[bench-smoke] PASS: summary.status=completed
[bench-smoke] PASS: summary.steps_taken=1
[bench-smoke] PASS: summary.total_prompt_tokens=100
[bench-smoke] PASS: summary.total_completion_tokens=50
[bench-smoke] PASS: summary.total_cost_usd=0.001
[bench-smoke] PASS: summary.cost_status=available
... (Bench Smoke Summary box: PASS/FAIL counts, run_id, output path) ...
[bench-smoke] ALL ASSERTIONS PASSED
```

The POST body includes `"allow_fallback":true` so the runner falls back to the direct fake provider when no model registry entry exists for the requested model.

Environment overrides for the shell smoke:

| Variable | Default | Description |
|---|---|---|
| `HARNESS_BINARY` | `$REPO_ROOT/harnessd-bench-smoke` | Path to built binary |
| `HARNESS_BENCH_SMOKE_LOG` | `/tmp/harnessd-bench-smoke.log` | Server log file |
| `HARNESS_BENCH_SMOKE_OUTPUT` | `/tmp/harnessd-bench-smoke-result.json` | Result JSON output |
| `HARNESS_BENCH_SMOKE_TURNS` | `/tmp/harnessd-bench-smoke-turns.json` | Fake turns file |
| `HARNESS_BENCH_SMOKE_TIMEOUT` | `30` | Seconds to wait for run completion |
| `HARNESS_BENCH_SMOKE_SKIP_BUILD` | (unset) | Skip build when non-empty and binary exists |

---

## Terminal-Bench integration

Terminal-Bench is an external framework for evaluating agents on containerized terminal tasks. The go-code integration lives in `benchmarks/terminal_bench/`.

<Callout type="warning">
**`benchmarks/` and `harness_agent/` are Python, not Go.** They require external pip dependencies that are NOT vendored in this repository. You must install them yourself before use. The Go toolchain alone is not sufficient.

```bash
pip install terminal-bench
```
</Callout>

### How it works

`GoAgentHarnessAgent` (in `benchmarks/terminal_bench/agent.py`) implements `terminal_bench.BaseAgent`. For each task it:

1. Cross-compiles `harnessd` and `harnesscli` for `linux/amd64` or `linux/arm64` (`GOOS=linux GOARCH=<arch> CGO_ENABLED=0`).
2. Packages the repo as a tar archive (excluding `.git`, `.tmp`, `node_modules`).
3. Copies the archive and binaries into the task container.
4. Starts `harnessd` in a tmux session at `http://127.0.0.1:8080`.
5. Runs `harnesscli` with `-agent-intent=autonomous` and `-task-context="Terminal Bench private smoke suite"`.
6. Fetches the run summary via `GET /v1/runs/{id}/summary`.
7. Returns success only if `"terminal_event=run.completed"` appears in the terminal output.

### Running locally

```bash
./scripts/run-terminal-bench.sh
```

Preflight requirements for the script:

- `OPENAI_API_KEY` (or `HARNESS_PROVIDER=fake` with `HARNESS_FAKE_TURNS`)
- Docker daemon running
- `tmux`
- `tb` or `uv` available

Output is written to `.tmp/terminal-bench/<timestamp>/`.

Key environment variables:

| Variable | Default |
|---|---|
| `TERMINAL_BENCH_MODEL` / `HARNESS_BENCH_MODEL` | `gpt-5-mini` |
| `TERMINAL_BENCH_N_CONCURRENT` | `1` |
| `TERMINAL_BENCH_N_ATTEMPTS` | `1` |
| `TERMINAL_BENCH_GLOBAL_AGENT_TIMEOUT_SEC` | `1800` |
| `TERMINAL_BENCH_GLOBAL_TEST_TIMEOUT_SEC` | `300` |
| `BENCH_MIN_ACCURACY` | `70` |
| `TERMINAL_BENCH_SKIP_BUILD` | `false` |

### 7 custom Go tasks

The suite includes 7 tasks in `benchmarks/terminal_bench/tasks/`:

<Card>
  <CardContent>

| Task | Difficulty | Category |
|---|---|---|
| `go-interface-migration` | hard | bugfix |
| `go-rename-refactor` | hard | refactor |
| `go-race-condition-fix` | hard | bugfix |
| `go-retry-schedule-fix` | medium | bugfix |
| `staging-deploy-docs` | easy | configuration |
| `multi-report-pipeline` | hard | shell |
| `incident-summary-shell` | medium | shell |

  </CardContent>
</Card>

### Baseline

`benchmarks/terminal_bench/baseline.json` was promoted from a real run (git SHA `89b5064`, model `gpt-5-mini`, Terminal Bench 0.2.18, 2026-06-27). All 7 tasks passed (`accuracy: 1.0`).

<Callout type="warning">
Costs in `baseline.json` are recorded as `0.0` with `cost_status: "unpriced_model"` because `gpt-5-mini` is not yet listed in `catalog/pricing.json`. This is expected — it does not indicate a bug in cost tracking.
</Callout>

### CI schedule

The periodic CI workflow runs the full 7-task dataset (`benchmarks/terminal_bench/tasks`) via `./scripts/run-terminal-bench.sh --skip-build` on a nightly cron (`0 6 * * *`) plus manual `workflow_dispatch`. No task filter is applied — all 7 tasks run. Workflow: `.github/workflows/terminal-bench-periodic.yml`. Artifacts are uploaded from `.tmp/terminal-bench/`.

---

## Overnight training loop

`scripts/overnight-training.sh` runs an indefinite loop of agent tasks at escalating difficulty tiers, scoring and optionally analyzing each batch with `trainerd`. It is designed to run unattended (in tmux overnight) and produces rollout files, logs, and a markdown report.

**Requirements:** `OPENAI_API_KEY`. The script builds `trainerd`, `harnesscli`, and `harnessd` once up front.

**Difficulty tier progression:**

| Batch | Tier |
|---|---|
| 1 | `easy` |
| 2 | `terminal-easy` |
| 3 | `medium` |
| 4 | `terminal-hard` |
| 5 | `hard` |
| 6 | `expert` |
| 7 | `ultra` |
| 8+ | alternates `terminal-hard` / `ultra` |

Task files live at `benchmarks/overnight-tasks/{easy,terminal-easy,medium,terminal-hard,hard,expert,ultra}.sh`, with each line in `task_name|prompt` format (`#` for comments).

**Outputs:**

| Output | Path |
|---|---|
| Rollout JSONL | `$HARNESS_ROLLOUT_DIR/<YYYY-MM-DD>/<run_id>.jsonl` |
| Log | `./training-reports/<DATE>-overnight.log` |
| Markdown report | `./training-reports/<DATE>-overnight.md` |

**Key env vars:**

| Variable | Default |
|---|---|
| `HARNESS_ROLLOUT_DIR` | `~/.trainerd/rollouts` |
| `TRAINERD_DB` | `~/.trainerd/training.db` |
| `HARNESS_MODEL` | `gpt-4.1` |
| `HARNESS_MAX_STEPS` | `1000` |

---

## Comparison harness

`benchmarks/comparison/` is an operator-run, env-driven shell orchestrator that records what each entrant tool produces on a task set. It does not assert accuracy — that is an external judgment.

```bash
TASK_SET_DIR=/path/to/tasks MODEL=gpt-4.1-mini ./benchmarks/comparison/run.sh
```

One active entrant is configured in `tools.json`: `go-code` (this harness). Two stub entries (`entrant-b`, `entrant-c`) are present but inactive in `_stub_entrants_not_active`.

Outputs land in `.tmp/comparison/<timestamp>/`:

- `results.jsonl` — one record per (tool, task) pair
- `report.txt` — counts only; no accuracy claims
- `run-env.json` — git SHA, model, and entrant list for provenance

---

## Harbor framework adapters

`harness_agent/` provides two Python adapters for the Harbor benchmarking framework.

<Callout type="warning">
**`harness_agent/` is Python and requires external pip dependencies that are NOT vendored here.** Install before use:

```bash
pip install harbor anthropic openai httpx   # for HarnessAgent
pip install harbor                          # for HarnessInstalledAgent
```
</Callout>

`HarnessAgent` (`harness_agent/agent.py`) is a direct API agent — no `harnessd` required. It supports Anthropic and OpenAI providers selected by a `provider/model` prefix format (for example, `anthropic/claude-opus-4-6` or `openai/gpt-4.1`). It exposes a single `bash` tool with a hard turn limit of 100 and truncates bash output at 20,000 characters.

```bash
./harness_agent/run_bench.sh                               # default: anthropic/claude-sonnet-4-6, 5 tasks
./harness_agent/run_bench.sh anthropic/claude-opus-4-6    # custom model
./harness_agent/run_bench.sh anthropic/claude-sonnet-4-6 20  # custom count
```

`HarnessInstalledAgent` (`harness_agent/installed_agent.py`) runs the full harness stack (harnessd + harnesscli) inside the Harbor container. It requires pre-built binaries in `harness_agent/bin/` — the directory currently contains only a `.gitkeep`. Build them first:

```bash
./harness_agent/build_binaries.sh
./harness_agent/run_installed.sh
```

---

## Benchmark result provenance

Understanding where each field in a benchmark result comes from helps you reason correctly about what the numbers mean.

`benchresult.FromRun(summary harness.RunSummary, run harness.Run)` produces the JSON artifact attached to each run. Its fields come from different sources:

- **`duration_ms`** is DERIVED: `Run.UpdatedAt.Sub(Run.CreatedAt).Milliseconds()`. It is wall-clock time from the harness perspective, not a provider-reported field.
- **`rollout_path`** is always empty when produced by `FromRun`. To populate it, call `benchresult.ReconstructRolloutPath(rolloutDir, run)` separately.
- **`total_cost_usd`** is accumulated by the runner from per-turn cost values, not reported directly by the provider API.

<Callout type="warning">
**`BenchmarkResult` has no pass/fail field.** Whether a task was completed successfully is an external judgment — in the Terminal-Bench and comparison harnesses, it comes from an external pytest oracle. The harness records what happened; it does not decide whether the outcome was correct.
</Callout>

---

## Next steps

- To understand rollout file format, JSONL event names, and how traces are replayed or diffed, see [Rollout Capture, Replay & Forensics](/docs/operations/rollout-replay-forensics).
- To set up a development worktree, write deterministic fake-provider tests, and run the regression suite and coverage gate before merging, see [Development and Testing](/docs/operations/development).
- For a beginner-friendly walkthrough of the key-free smokes (`TestRunSmoke` and the shell smoke), see [Key-free testing](/docs/getting-started/key-free-testing).
