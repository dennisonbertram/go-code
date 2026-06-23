# harness_agent/

Two [Harbor](https://github.com/harbor-ai/harbor) framework agent adapters that let
go-agent-harness compete on the Terminal-Bench 2.0 public leaderboard.

The Python package lives at `harness_agent/` (not `harbor/`) to avoid shadowing the installed
`harbor` framework package.

---

## Agents

### agent.py — HarnessAgent (direct API mode)

`HarnessAgent` is a `BaseAgent` subclass that:

- Calls the **Anthropic Messages API** or **OpenAI Chat Completions API** directly (provider
  selected from the `provider/model` prefix passed by Harbor).
- Uses the `anthropic` or `openai` SDK when available; falls back to raw `httpx` for both.
- Exposes a single **`bash` tool** to the LLM.
- Routes every bash call through `environment.exec()` so it runs inside the Harbor-managed
  container, not on the host.
- Runs up to **100 turns** before stopping.
- Populates `AgentContext` with token counts and estimated cost in USD.

### installed_agent.py — HarnessInstalledAgent (full harness mode)

`HarnessInstalledAgent` is a `BaseAgent` subclass that:

- Uploads pre-built `harnessd` and `harnesscli` binaries from `harness_agent/bin/` into the
  Harbor container during `setup()`.
- Uploads `prompts/` and (if present) `catalog/models.json` into the container.
- Starts `harnessd` inside the container, waits for it to be healthy, then runs the task via
  `harnesscli`.
- This mode tests the **full harness** (all tools, the system prompt, the agent loop) — the
  model is just the backend.

---

## Prerequisites

### Python dependencies

```bash
# For HarnessAgent (direct API mode)
pip install harbor anthropic openai httpx

# For HarnessInstalledAgent (full harness mode)
pip install harbor
```

**Honesty note:** The `harbor` package is **not installed in this development environment** and
cannot be imported (`ModuleNotFoundError: No module named 'harbor'`). The intended commands
below are documented from reading the source; they have **not been end-to-end executed** in
this environment. The `anthropic` package is also not installed here; `openai` and `httpx` are
available.

### Environment variables

| Variable | Required by | Description |
|---|---|---|
| `ANTHROPIC_API_KEY` | HarnessAgent (anthropic/ models) | Anthropic API key |
| `OPENAI_API_KEY` | HarnessAgent (openai/ models) | OpenAI API key |
| `GOOGLE_API_KEY` | HarnessInstalledAgent | Google API key (passed through to harnessd) |

---

## bin/ directory

`harness_agent/bin/` is **empty** (contains only `.gitkeep`). Pre-built binaries are not
committed to the repository. Before using `HarnessInstalledAgent` or `run_installed.sh`, you
must build the binaries:

```bash
# From the project root
./harness_agent/build_binaries.sh
```

This cross-compiles `harnessd` and `harnesscli` for linux/amd64 and linux/arm64 using the Go
toolchain from the project root, writing binaries to `harness_agent/bin/`:

```
harness_agent/bin/
  harnessd-linux-amd64
  harnesscli-linux-amd64
  harnessd-linux-arm64
  harnesscli-linux-arm64
```

Requires: Go toolchain on PATH, `GOOS`/`GOARCH` cross-compilation support (standard with the
Go distribution).

---

## Quick start

### HarnessAgent (direct API, no pre-built binaries needed)

Run from the **project root** so that `harness_agent/` is on the Python path:

```bash
# Run 5 tasks from terminal-bench 2.0 with claude-sonnet-4-6 (Anthropic)
./harness_agent/run_bench.sh

# Run 20 tasks with opus
./harness_agent/run_bench.sh anthropic/claude-opus-4-6 20

# Run with an OpenAI model
./harness_agent/run_bench.sh openai/gpt-4.1 10
```

Or invoke harbor directly:

```bash
harbor run \
  -d terminal-bench@2.0 \
  --agent-import-path harness_agent.agent:HarnessAgent \
  -m anthropic/claude-sonnet-4-6 \
  -n 5
```

### HarnessInstalledAgent (full harness inside container)

```bash
# Build binaries first
./harness_agent/build_binaries.sh

# Run 3 tasks with gpt-4.1-mini (default)
./harness_agent/run_installed.sh

# Run with a different model
./harness_agent/run_installed.sh openai/gpt-4.1 5
```

Or invoke harbor directly:

```bash
harbor run \
  -d terminal-bench@2.0 \
  --agent-import-path harness_agent.installed_agent:HarnessInstalledAgent \
  -m openai/gpt-4.1-mini \
  -n 3
```

---

## System prompt

`agent.py` loads `prompts/compiled/system_prompt.txt` relative to the project root, if that
file exists, prepending a bash-directive block. Falls back to a built-in default system prompt
(`prompts.py::DEFAULT_SYSTEM_PROMPT`) if the file is absent or empty, with a warning to stderr.

There is no built-in `harnessd` prompt-compilation subcommand in this repository. If you
maintain a compiled prompt file, generate or update `prompts/compiled/system_prompt.txt` using
your external prompt build workflow before benchmark runs.

---

## Leaderboard submission

Follow the official Terminal-Bench submission guide. Key flags:

- For direct API mode: `--agent-import-path harness_agent.agent:HarnessAgent`
- For full harness mode: `--agent-import-path harness_agent.installed_agent:HarnessInstalledAgent`

Run from the project root so that `harness_agent/` is on the Python path.

---

## File layout

```
harness_agent/
  __init__.py          — package metadata (version 0.1.0)
  agent.py             — HarnessAgent: direct Anthropic/OpenAI API mode
  installed_agent.py   — HarnessInstalledAgent: full harness inside container
  prompts.py           — system prompt loader (compiled or built-in default)
  build_binaries.sh    — cross-compile harnessd + harnesscli for linux/amd64 + arm64
  run_bench.sh         — convenience wrapper around `harbor run` (HarnessAgent)
  run_installed.sh     — convenience wrapper around `harbor run` (HarnessInstalledAgent)
  bin/                 — pre-built binaries (empty; run build_binaries.sh first)
  README.md            — this file
```
