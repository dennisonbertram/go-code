# benchmarks/comparison/

Comparison harness **shape** for go-code.

This directory defines the structural framework for running go-code alongside
other tools on a shared task set. **No competitor tools are built or
configured here.** No head-to-head numbers are asserted. The framework exists
so an operator can fill in entrants and run an honest comparison.

---

## What is here

| File | Purpose |
|---|---|
| `tools.json` | Entrant registry. go-code is fully specified; all other entrants are commented stubs in `_stub_entrants_not_active`. |
| `result.schema.json` | JSON Schema for one result record. Mirrors `internal/benchresult.BenchmarkResult` fields plus adapter-added fields (`tool_id`, `task_id`, `git_sha`) and the external oracle field (`is_resolved`). |
| `adapters/template.sh` | Documents the adapter I/O contract. Copy and implement for each entrant. |
| `adapters/go-code.sh` | Real adapter wrapping the go-code run API. |
| `run.sh` | Env-driven orchestrator: iterates entrants × tasks, writes `results.jsonl` and a report. |

---

## What is NOT here

- **No competitor adapters.** `_stub_entrants_not_active` in `tools.json` are
  placeholders. Move one into the active `entrants` array and write its
  adapter before running it.
- **No head-to-head numbers.** No accuracy comparisons are made by this
  harness. The report only records adapter success/failure counts.
- **No task pass/fail.** `is_resolved` (task pass/fail) is an **external**
  field from the pytest oracle (terminal-bench or equivalent). The harness
  API only reports `completed`/`failed` — meaning the harness finished, not
  that the agent solved the task. Merge oracle results separately.

---

## Honesty rules

These are non-negotiable when using this harness:

1. **`is_resolved` must come from an external oracle.** Never set it in an
   adapter from a heuristic or guess. The harness has no task-level
   pass/fail signal — it only knows if the run ended.
2. **Do not invent metrics.** `duration_ms` is derived from timestamps;
   `cost_status` reflects the harness value. If a field is unknown, leave it
   absent or use the zero value.
3. **Record provenance.** `run-env.json` captures the git SHA, model, and
   task set used. Always keep it with the `results.jsonl`.
4. **Mark derived vs. raw.** Fields annotated DERIVED/RECONSTRUCTED/EXTERNAL
   in `result.schema.json` are not raw API fields — treat them as such.

---

## Quick start

### Key-free smoke (go-code only, fake provider)

Requires: `harnessd` built; `HARNESS_PROVIDER=fake`; a turns JSON file.

```bash
# Build harnessd first
go build -o harnessd ./cmd/harnessd

# Create a minimal task set
mkdir -p /tmp/tasks/hello
echo "Reply with: HELLO_OK" > /tmp/tasks/hello/prompt.txt

# Run the comparison harness with the fake provider
TASK_SET_DIR=/tmp/tasks \
MODEL=fake-model \
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/path/to/turns.json \
  bash benchmarks/comparison/run.sh
```

### Real provider run (go-code only)

```bash
export OPENAI_API_KEY=sk-...

TASK_SET_DIR=/path/to/terminal_bench/tasks \
MODEL=gpt-4.1-mini \
  bash benchmarks/comparison/run.sh
```

### Adding a competitor

1. Copy `adapters/template.sh` to `adapters/<tool-id>.sh`.
2. Implement the adapter body following the contract documented in the
   template (env inputs: `TASK_DIR`, `WORKSPACE`, `PROMPT`, `MODEL`,
   `RESULT_JSON`, `TASK_ID`, `TOOL_ID`; exit 0 on success, 1 on infra error).
3. In `tools.json`, move the stub from `_stub_entrants_not_active` into the
   `entrants` array (or add a new entry). Fill in `adapter`, `key_env`, etc.
4. Make the adapter executable: `chmod +x adapters/<tool-id>.sh`.
5. Run as above with `ENTRANT_IDS="go-code <tool-id>"`.

---

## Result schema

Each row in `results.jsonl` conforms to `result.schema.json`. Key fields:

| Field | Source | Notes |
|---|---|---|
| `run_id` | RunSummary.RunID | Harness-assigned UUID |
| `status` | RunSummary.Status | `completed` or `failed` — harness status, NOT task pass/fail |
| `steps_taken` | RunSummary.StepsTaken | LLM turns |
| `total_prompt_tokens` | RunSummary.TotalPromptTokens | |
| `total_completion_tokens` | RunSummary.TotalCompletionTokens | |
| `total_cost_usd` | RunSummary.TotalCostUSD | |
| `duration_ms` | **DERIVED** | UpdatedAt − CreatedAt in ms |
| `rollout_path` | **RECONSTRUCTED** | Empty unless caller supplies rolloutDir |
| `is_resolved` | **EXTERNAL** | From pytest oracle only — never from adapter |
| `drift` | **EXTERNAL/opt-in** | From drift-analysis oracle |
| `forensic_events` | **EXTERNAL/opt-in** | From rollout JSONL loader |

---

## Environment variables reference

| Variable | Required | Default | Description |
|---|---|---|---|
| `TASK_SET_DIR` | Yes | — | Directory of task subdirectories |
| `MODEL` | Yes | — | Model name (e.g. `gpt-4.1-mini`) |
| `ENTRANT_IDS` | No | all active | Space-separated IDs to run |
| `OUTPUT_DIR` | No | `.tmp/comparison/<ts>` | Results output directory |
| `HARNESS_PROVIDER` | No | — | Set to `fake` for key-free mode |
| `HARNESS_FAKE_TURNS` | No | — | Turns JSON for fake provider |
| `HARNESS_BINARY` | No | `harnessd` | Path to harnessd binary |
| `HARNESS_ADDR` | No | `:8081` | Harnessd listen address |
| `POLL_TIMEOUT_S` | No | `300` | Per-run poll timeout (seconds) |
| `OPENAI_API_KEY` | Conditional | — | Required unless using fake provider |
