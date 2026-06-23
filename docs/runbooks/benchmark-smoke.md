# Benchmark Smoke Runbook

This runbook covers the two deterministic, key-free smokes for the harness benchmark infrastructure,
the grounded result schema, the comparison-harness shape, and the real Python benchmark paths that
require external dependencies and API keys.

---

## 1. In-process Go smoke

**Command:**

```bash
go test ./internal/server/... -run TestRunSmoke
```

With race detector (recommended before committing):

```bash
go test ./internal/server/... -race -count=1 -run TestRunSmoke
```

**What it proves:**

- The real run API (`POST /v1/runs` → poll `GET /v1/runs/{id}` → `GET /v1/runs/{id}/summary`) executes
  end-to-end in a single test process with no Docker, no network, no API key, and no LLM.
- A scripted `fakeprovider` returns a deterministic turn (`content="smoke ok"`, `usage.prompt=100`,
  `usage.completion=50`, `cost_usd=0.001`, `cost_status="available"`).
- The following `RunSummary` fields are asserted byte-stable:

  | Field | Value |
  |---|---|
  | `status` | `"completed"` |
  | `steps_taken` | `1` |
  | `total_prompt_tokens` | `100` |
  | `total_completion_tokens` | `50` |
  | `total_cost_usd` | `0.001` |
  | `cost_status` | `"available"` |

- `benchresult.FromRun` is called on the completed run, producing a JSON artifact; grounded fields
  (`run_id`, `model`, `provider_name`, `tokens`, `cost`, `steps`, `status`, `duration_ms`) are
  re-asserted against the same constants.
- `fakeprovider.Calls() == 1` verifies the provider was called exactly once.
- Passes under `-race`.

**File:** `internal/server/run_smoke_test.go` (package `server`; no import cycle).

**Honesty note:** `total_cost_usd` is a DERIVED field — it is accumulated by `Runner.recordAccounting`
from the scripted `Cost.TotalUSD` in the fake turn. It is not a raw provider API field.

---

## 2. Key-free shell smoke

**Command:**

```bash
bash scripts/run-bench-smoke.sh
```

**Expected deterministic output (all 13 assertions pass):**

```
[bench-smoke] PASS: wrote fake turns file
[bench-smoke] PASS: harnessd built: .../harnessd-bench-smoke
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
[bench-smoke] ALL ASSERTIONS PASSED
```

The `run_id` UUIDs will differ between runs; all other asserted values are byte-stable.

**What it proves:**

The real `harnessd` binary (built with `go build ./cmd/harnessd`) starts with the fake-provider path
(`HARNESS_PROVIDER=fake`), serves the real HTTP API, and produces the same deterministic summary fields
as the in-process Go smoke. No OpenAI or Anthropic API key is needed.

**Output file:** `/tmp/harnessd-bench-smoke-result.json` (overwritten on each run; path is
configurable via `HARNESS_BENCH_SMOKE_OUTPUT`).

**Environment overrides:**

| Variable | Default | Description |
|---|---|---|
| `HARNESS_BINARY` | `$REPO_ROOT/harnessd-bench-smoke` | Path to harnessd binary |
| `HARNESS_BENCH_SMOKE_LOG` | `/tmp/harnessd-bench-smoke.log` | Server log |
| `HARNESS_BENCH_SMOKE_OUTPUT` | `/tmp/harnessd-bench-smoke-result.json` | Result JSON |
| `HARNESS_BENCH_SMOKE_TURNS` | `/tmp/harnessd-bench-smoke-turns.json` | Fake turns file |
| `HARNESS_BENCH_SMOKE_TIMEOUT` | `30` | Max seconds to wait for run completion |
| `HARNESS_BENCH_SMOKE_SKIP_BUILD` | _(unset)_ | Skip `go build` when non-empty and binary exists |

**Design note:** The POST body includes `"allow_fallback":true`. When no API key is set, the runner's
provider-registry lookup for the default model (`gpt-4.1-mini`) fails; `allow_fallback=true` causes the
runner to fall back to its direct `r.provider` (the fake), which is the correct key-free path.

**Requirements:** Go toolchain on PATH, `curl`, `python3`.

---

## 3. Result schema — `internal/benchresult`

**Package:** `internal/benchresult` (`result.go` + `result_test.go`)

**Entry point:** `benchresult.FromRun(summary harness.RunSummary, run harness.Run) BenchmarkResult`

Every field in `BenchmarkResult` is sourced exactly:

| Field | Source | Notes |
|---|---|---|
| `run_id` | `RunSummary.RunID` = `Run.ID` | Raw |
| `status` | `RunSummary.Status` | `"completed"` or `"failed"` — NOT task pass/fail |
| `steps_taken` | `RunSummary.StepsTaken` | Raw |
| `total_prompt_tokens` | `RunSummary.TotalPromptTokens` | Raw |
| `total_completion_tokens` | `RunSummary.TotalCompletionTokens` | Raw |
| `total_cost_usd` | `RunSummary.TotalCostUSD` | Raw field (itself accumulated by runner) |
| `cost_status` | `RunSummary.CostStatus` | Raw (`"available"`, `"unpriced_model"`, etc.) |
| `cache_hit_rate` | `RunSummary.CacheHitRate` | Raw |
| `error_message` | `RunSummary.Error` | Raw; omitted when empty |
| `model` | `Run.Model` | Raw |
| `provider_name` | `Run.ProviderName` | Raw; omitted when empty |
| `prompt` | `Run.Prompt` | Raw |
| `output` | `Run.Output` | Raw; omitted when empty |
| `tenant_id`, `conversation_id`, `agent_id` | `Run.*` fields | Raw; omitted when empty |
| `created_at`, `updated_at` | `Run.CreatedAt`, `Run.UpdatedAt` | Raw UTC timestamps |
| `duration_ms` | **DERIVED** | `Run.UpdatedAt.Sub(Run.CreatedAt).Milliseconds()` — not a provider API field |
| `rollout_path` | **RECONSTRUCTED** | `<rolloutDir>/<YYYY-MM-DD>/<run_id>.jsonl`; always empty from `FromRun`; populate via `benchresult.ReconstructRolloutPath(rolloutDir, run)` |
| `tool_calls` | `RunSummary.ToolCalls` | Mapped 1:1 to `ToolCallRecord{ToolName, Step}` |
| `drift` | **EXTERNAL/opt-in** | Always `nil` from `FromRun`; set by external drift oracle |
| `forensic_events` | **EXTERNAL/opt-in** | Always `nil` from `FromRun`; set by external log loader |

**No task pass/fail field exists in `BenchmarkResult`.** Whether a task was solved is an external
judgment (from a pytest oracle such as terminal-bench's grader). It is not present in the harness API.

The JSON schema for comparison harness records is at `benchmarks/comparison/result.schema.json`, which
mirrors these fields with honesty annotations.

---

## 4. Comparison harness — `benchmarks/comparison/`

The comparison harness is an **operator-run, env-driven shell orchestrator** — not an automated CI job.
It records what entrant tools produce; it does not assert accuracy.

### Shape

```
benchmarks/comparison/
  tools.json              — entrant registry (one active entrant: go-code)
  result.schema.json      — JSON schema for result records (mirrors benchresult)
  run.sh                  — orchestrator: TASK_SET_DIR × MODEL × active entrants
  adapters/
    template.sh           — adapter contract (I/O specification)
    go-code.sh            — real adapter for this harness
```

### Adding an entrant

1. Write `adapters/<your-tool>.sh` following the contract in `adapters/template.sh`.
   - The adapter receives: `TASK_DIR`, `WORKSPACE`, `PROMPT`, `MODEL`, `RESULT_JSON`, `TASK_ID`, `TOOL_ID` env vars.
   - It must write a JSON record conforming to `result.schema.json` to `$RESULT_JSON`.
   - It must exit 0 on success, non-zero on failure.
   - It must NOT set `is_resolved` — that comes from the pytest oracle after the run.
2. Move the stub entry for your tool from `_stub_entrants_not_active` into the `entrants` array in
   `tools.json`, filling in `id`, `adapter`, `display_name`, and `key_env`.
3. Run the harness:

```bash
TASK_SET_DIR=/path/to/tasks \
MODEL=gpt-4.1-mini \
./benchmarks/comparison/run.sh
```

Output lands in `.tmp/comparison/<timestamp>/`:
- `results.jsonl` — one record per (tool, task) pair
- `report.txt` — counts only; no accuracy claims
- `run-env.json` — git SHA + model + entrants for provenance

**Honesty notice:** The report does not make head-to-head accuracy comparisons. `is_resolved` (task
pass/fail) must come from the pytest oracle; it is absent from the raw records.

---

## 5. Python benchmark paths

Both Python harnesses require external dependencies and API keys. They have **not been end-to-end
executed in this development environment** — the intended commands are documented from reading the
source. Treat these as operational guides, not verified recipes.

### 5a. `benchmarks/terminal_bench/` — Terminal-Bench private task suite

**What it is:** A `GoAgentHarnessAgent` class implementing the `terminal_bench.BaseAgent` interface.
It cross-compiles `harnessd` and `harnesscli` into the task container and drives tasks via the harness
HTTP API. 7 custom Go coding tasks are included under `benchmarks/terminal_bench/tasks/`.

**Python dependency (not installed here):**

```bash
pip install terminal-bench
```

`terminal_bench` and its transitive dependencies (`terminal_bench.agents`,
`terminal_bench.terminal.tmux_session`, etc.) are not installed in this environment
(`ModuleNotFoundError: No module named 'terminal_bench'`).

**Intended command (run from project root):**

```bash
terminal-bench run \
  --agent benchmarks.terminal_bench.agent:GoAgentHarnessAgent \
  --task go-interface-migration
```

**Required keys / env:**

| Variable | Required | Notes |
|---|---|---|
| `OPENAI_API_KEY` | Yes (default) | Or set `OPENAI_BASE_URL` for a compatible endpoint |
| `HARNESS_BENCH_MODEL` | No | Default: `gpt-5-mini` (as hardcoded in `agent.py`) |
| `HARNESS_BENCH_MAX_STEPS` | No | Default: `100` |
| `HARNESS_BENCH_MEMORY_MODE` | No | Default: `off` |
| `HARNESS_BENCH_TARGET_ARCH` | No | `amd64` or `arm64`; auto-detected |

**Honesty caveat:** The `baseline.json` in `benchmarks/terminal_bench/` contains
illustrative/sample baseline values. Its `_comment` attributes them to a run named
`full-final-20260308-005332`, but no archived run data exists in this repo to verify that
provenance. Treat the numbers as sample expectations, not as verified historical measurements.

### 5b. `harness_agent/` — Harbor framework adapters (Terminal-Bench 2.0 leaderboard)

Two adapters for the [Harbor](https://github.com/harbor-ai/harbor) framework:

- `harness_agent/agent.py` — `HarnessAgent`: calls Anthropic or OpenAI APIs directly; exposes a
  `bash` tool; runs up to 100 turns.
- `harness_agent/installed_agent.py` — `HarnessInstalledAgent`: uploads pre-built `harnessd` /
  `harnesscli` binaries into the Harbor container and runs the full harness.

**Python dependencies (not installed here):**

```bash
# For HarnessAgent (direct API mode)
pip install harbor anthropic openai httpx

# For HarnessInstalledAgent (full harness mode)
pip install harbor
```

`harbor` is not installed in this environment (`ModuleNotFoundError: No module named 'harbor'`).

**`harness_agent/bin/` is empty.** Before using `HarnessInstalledAgent`, build binaries first:

```bash
./harness_agent/build_binaries.sh
```

**Intended commands (run from project root):**

```bash
# HarnessAgent — 5 tasks, claude-sonnet-4-6
./harness_agent/run_bench.sh

# HarnessAgent — custom model and count
./harness_agent/run_bench.sh anthropic/claude-opus-4-6 20

# HarnessInstalledAgent — 3 tasks, gpt-4.1-mini
./harness_agent/build_binaries.sh
./harness_agent/run_installed.sh
```

**Required keys / env:**

| Variable | Required by | Notes |
|---|---|---|
| `ANTHROPIC_API_KEY` | `HarnessAgent` (anthropic/ models) | Anthropic API key |
| `OPENAI_API_KEY` | `HarnessAgent` (openai/ models) | OpenAI API key |
| `GOOGLE_API_KEY` | `HarnessInstalledAgent` | Passed through to `harnessd` |

---

## 6. Honesty caveats for real-LLM benchmark runs

- **Every real-LLM benchmark run is paid and non-deterministic.** Results differ across runs even with
  the same model, prompt, and temperature settings.
- **Always record the exact model name, provider, git SHA, and n (number of runs).** A single
  data point is an anecdote, not a benchmark. Use `run-env.json` from `benchmarks/comparison/run.sh`
  for provenance.
- **`status=completed` does not mean the task was solved.** It means the harness finished without
  error. Task pass/fail is a separate external judgment.
- **Default models vary across harnesses:**
  - `benchmarks/terminal_bench/agent.py`: default `gpt-5-mini` (hardcoded)
  - `docs/runbooks/terminal-bench-periodic-suite.md`: default `gpt-5-nano`
  - `harness_agent/`: model passed by Harbor at runtime
  - Comparison harness (`benchmarks/comparison/`): `MODEL` env var; no default
- **`duration_ms` is DERIVED** (`updated_at − created_at`). It measures harness wall-clock time, not
  pure model latency.
- **`rollout_path` is RECONSTRUCTED** and requires the server's `RolloutDir` out-of-band. Leave empty
  if unknown; never guess.
