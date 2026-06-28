# benchmarks/

This directory contains two separate benchmark harnesses for go-agent-harness. Neither is a Go package; the Terminal-Bench path uses Python plus Docker/tmux, and the overnight task lists are shell-script data.

---

## 1. terminal_bench/ — Terminal-Bench private task suite

Custom tasks for the [Terminal-Bench](https://github.com/terminal-bench/terminal-bench) framework.
`agent.py` (the `GoAgentHarnessAgent` class) implements the `BaseAgent` interface from the
`terminal_bench` Python package.

### What it does

- Packages this repo as a tar archive and copies it into the Terminal-Bench container.
- Cross-compiles `harnessd` and `harnesscli` for linux/amd64 or linux/arm64 (via `go build`).
- Starts `harnessd` inside the container via tmux, runs the task via `harnesscli`, and fetches
  telemetry from the `/v1/runs/{id}/summary` endpoint.
- Includes 7 custom Go coding tasks under `terminal_bench/tasks/`:
  - `go-interface-migration` — implement missing interface methods
  - `go-race-condition-fix` — fix a concurrent data race
  - `go-rename-refactor` — rename/refactor across a package
  - `go-retry-schedule-fix` — fix retry/scheduling logic
  - `incident-summary-shell` — shell-based incident summarizer
  - `multi-report-pipeline` — multi-step shell pipeline
  - `staging-deploy-docs` — documentation generation task

### Entry point

Run from the **project root** so that `benchmarks/` is on the Python path:

```bash
./scripts/run-terminal-bench.sh
```

The script is the supported operator interface for this slice. It preflights Docker, tmux,
Python, Terminal-Bench, model/provider settings, and target architecture; builds `harnessd` and
`harnesscli` once per campaign; invokes `tb run` with the custom agent import path; and
post-processes the run artifacts.

Useful environment variables:

| Variable | Required | Description |
|---|---|---|
| `TERMINAL_BENCH_DATASET_PATH` | No | Dataset path (default: `benchmarks/terminal_bench/tasks`) |
| `TERMINAL_BENCH_OUTPUT_DIR` | No | Output directory (default: `.tmp/terminal-bench/<timestamp>`) |
| `TERMINAL_BENCH_MODEL` or `HARNESS_BENCH_MODEL` | No | Model name (default: `gpt-5-mini`) |
| `TERMINAL_BENCH_N_CONCURRENT` | No | Terminal-Bench concurrency (default: `1`) |
| `TERMINAL_BENCH_N_ATTEMPTS` | No | Attempts/pass@k input (default: `1`) |
| `TERMINAL_BENCH_GLOBAL_AGENT_TIMEOUT_SEC` | No | Agent timeout in seconds (default: `1800`) |
| `TERMINAL_BENCH_GLOBAL_TEST_TIMEOUT_SEC` | No | Oracle/test timeout in seconds (default: `300`) |
| `BENCH_MIN_ACCURACY` | No | Accuracy gate percentage (default: `70`) |

For a key-free local/CI smoke, use the fake provider:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/path/to/fake-turns.json \
./scripts/run-terminal-bench.sh
```

Preflight only:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/path/to/fake-turns.json \
./scripts/run-terminal-bench.sh --preflight-only
```

### Required environment variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | Yes unless fake provider | OpenAI API key for the LLM backend |
| `OPENAI_BASE_URL` | No | Override for OpenAI-compatible base URL |
| `HARNESS_BENCH_MODEL` | No | Model name (default: `gpt-5-mini`) |
| `HARNESS_BENCH_MAX_STEPS` | No | Max harness steps per run (default: `100`) |
| `HARNESS_BENCH_MEMORY_MODE` | No | Memory mode (default: `off`) |
| `HARNESS_BENCH_TARGET_ARCH` | No | Linux target arch: `amd64` or `arm64` (auto-detected) |
| `HARNESS_PROVIDER` | No | Set to `fake` for key-free smoke runs |
| `HARNESS_FAKE_TURNS` | Required for fake provider | Path to fake model turns JSON |
| `HARNESS_BENCH_BINARY_DIR` | No | Prebuilt Linux `harnessd`/`harnesscli` directory |

**Note:** The default model hardcoded in `agent.py` is `gpt-5-mini`. The `baseline.json` file
contains illustrative/sample baseline values (attributed in its `_comment` to a run named
`full-final-20260308-005332`, but no archived run data exists in this repo to verify that
provenance). Treat the numbers as sample expectations, not as verified historical measurements.

### Canonical artifacts

Each campaign should preserve:

- Terminal-Bench raw `results.json`.
- Merged `results.jsonl` in `benchmarks/comparison/result.schema.json` format, with external
  Terminal-Bench `is_resolved` and parser results merged into the harness telemetry row.
- `run-env.json` with git SHA, model, provider, Terminal-Bench version, dataset hash, concurrency,
  attempts, timeouts, and cleanup mode.
- Per-task `harness_telemetry.json`, `benchmark_result.json`, task logs, and `harnessd.log`.
- `summary.json` and `report.md` with accuracy, pass@k when reported by Terminal-Bench, cost per
  resolved task, duration/step/token totals, regressions/improvements, and failure classification.

A real baseline must come from a current green real-provider campaign with the metadata above.
Do not promote `baseline.json` as authoritative until that campaign exists.

### Docker

Each task has a `Dockerfile` and `docker-compose.yaml`. The base image is defined in
`terminal_bench/Dockerfile.base` (based on `golang:1.22-bookworm`). It installs tmux, Python, and
`python3-pytest` for the task oracles. These are used by the Terminal-Bench framework; you do not
need to build them directly unless debugging images. The runner builds them through
`scripts/build-bench-images.sh` unless `--skip-build` is passed.

---

## 2. overnight-tasks/ — Overnight training task lists

Plain-text task tables used by `scripts/overnight-training.sh`. These are **not Python** — they
are shell-script data files sourced by the overnight training loop.

See `overnight-tasks/README.md` for format, difficulty tiers, and output paths.

---

## What is NOT here

- No public `go-code eval` command yet; `scripts/run-terminal-bench.sh` is the current interface.
- No authoritative real-provider baseline yet.
- `benchmarks/__init__.py` exists only to make the directory importable as a Python package.
