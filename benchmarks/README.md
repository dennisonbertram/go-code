# benchmarks/

This directory contains two separate benchmark harnesses for go-agent-harness. Neither is a Go package — both are Python and require external Python dependencies.

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

### Python dependencies

The `terminal_bench` package is an **external pip package** (not vendored here). There is no
`requirements.txt` or `pyproject.toml` in this directory — the dependency must be installed
manually:

```bash
pip install terminal-bench
```

**Honesty note:** `terminal_bench` and its transitive dependencies (`terminal_bench.agents`,
`terminal_bench.terminal.tmux_session`, etc.) are **not installed in this development
environment** and cannot be imported (`ModuleNotFoundError: No module named 'terminal_bench'`).
The intended commands below are documented from reading the source; they have **not been
end-to-end executed** in this environment.

### Entry point (intended — not verified here)

Run from the **project root** so that `benchmarks/` is on the Python path:

```bash
# Example: run the go-interface-migration task with terminal-bench
terminal-bench run \
  --agent benchmarks.terminal_bench.agent:GoAgentHarnessAgent \
  --task go-interface-migration
```

Or follow the Terminal-Bench framework docs for how to register custom tasks and agents.

### Required environment variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | Yes (default) | OpenAI API key for the LLM backend |
| `OPENAI_BASE_URL` | No | Override for OpenAI-compatible base URL |
| `HARNESS_BENCH_MODEL` | No | Model name (default: `gpt-5-mini`) |
| `HARNESS_BENCH_MAX_STEPS` | No | Max harness steps per run (default: `100`) |
| `HARNESS_BENCH_MEMORY_MODE` | No | Memory mode (default: `off`) |
| `HARNESS_BENCH_TARGET_ARCH` | No | Linux target arch: `amd64` or `arm64` (auto-detected) |

**Note:** The default model hardcoded in `agent.py` is `gpt-5-mini`. The `baseline.json` file
contains illustrative/sample baseline values (attributed in its `_comment` to a run named
`full-final-20260308-005332`, but no archived run data exists in this repo to verify that
provenance). Treat the numbers as sample expectations, not as verified historical measurements.

### Docker

Each task has a `Dockerfile` and `docker-compose.yaml`. The base image is defined in
`terminal_bench/Dockerfile.base` (based on `golang:1.22-bookworm`). These are used by the
Terminal-Bench framework — you do not need to build them directly.

---

## 2. overnight-tasks/ — Overnight training task lists

Plain-text task tables used by `scripts/overnight-training.sh`. These are **not Python** — they
are shell-script data files sourced by the overnight training loop.

See `overnight-tasks/README.md` for format, difficulty tiers, and output paths.

---

## What is NOT here

- No `requirements.txt` or `pyproject.toml` — Python dependencies are undeclared and must be
  installed manually based on the imports in each agent file.
- No top-level entry-point script for `benchmarks/` — each sub-harness has its own mechanism.
- `benchmarks/__init__.py` exists only to make the directory importable as a Python package.
