---
title: "Installing go-code"
sidebar_label: "Installation"
sidebar_position: 2
---

import { Callout, Steps, Step } from '@site/src/components/ui';

go-code is a local-first coding agent runtime written in Go. It gives you three binaries on your `PATH`: a user-facing launcher (`go-code`), an interactive terminal client (`harnesscli`), and a local HTTP daemon (`harnessd`). Once installed you can run coding agents against your projects from the terminal, stream their events over HTTP, and compose multi-agent pipelines — all without leaving your machine.

This page covers the three supported ways to install go-code and shows you how to confirm the install worked.

---

## Prerequisites

- **macOS or Linux** — Windows is not yet tested.
- **Go 1.25+** — required for building from source (Options 2 and 3). The module declares `go 1.25.0` in go.mod. Check with `go version`.
- **Git** — required for cloning and for Homebrew HEAD builds.
- **Homebrew** — required for Option 1 only.

---

## Option 1: Homebrew (recommended on macOS)

Homebrew is the fastest path on macOS. The formula builds `harnesscli` and `harnessd` from the current `main` branch, then installs the `go-code` launcher plus the `prompts/` and `catalog/` runtime assets under Homebrew's `share/go-code/` prefix.

<Callout variant="warning" title="HEAD-only formula">
  There is no tagged bottle yet. The formula always builds from source with <code>--HEAD</code>. The first install takes a few minutes while Go compiles the binaries.
</Callout>

<Steps>
  <Step title="Install via the tap">

```bash
brew install --HEAD dennisonbertram/go-code/go-code
```

If you prefer to manage the tap explicitly:

```bash
brew tap dennisonbertram/go-code
brew install --HEAD go-code
```

  </Step>
  <Step title="Verify">

```bash
go-code --help
```

You should see the go-code usage block. If the command is not found, make sure Homebrew's bin directory is on your `PATH` (usually `/opt/homebrew/bin` on Apple Silicon, `/usr/local/bin` on Intel).

  </Step>
</Steps>

---

## Option 2: Source install (no Homebrew)

Use this path on Linux or on macOS without Homebrew. The `scripts/install.sh` script builds and installs everything to a user-local location by default — no `sudo` required.

<Steps>
  <Step title="Clone the repository">

```bash
git clone https://github.com/dennisonbertram/go-code.git
cd go-code
```

  </Step>
  <Step title="Run the installer">

```bash
./scripts/install.sh --add-to-path
```

`--add-to-path` appends the install directory to your shell profile (`~/.bashrc`, `~/.zshrc`, or equivalent) so the binaries are available in new terminals.

**Default install locations (no flags needed for most users):**

| What | Where |
|------|-------|
| `go-code`, `harnesscli`, `harnessd` binaries | `~/.local/bin/` |
| `prompts/` and `catalog/` runtime assets | `~/.local/share/go-code/` |

  </Step>
  <Step title="Reload your shell and verify">

```bash
source ~/.bashrc   # or ~/.zshrc, etc.
go-code --help
```

  </Step>
</Steps>

### install.sh flag reference

Pass these flags to customize the install location or behavior:

| Flag | Effect |
|------|--------|
| `--prefix DIR` | Install binaries under `DIR/bin` (default: `~/.local`) |
| `--bin-dir DIR` | Set binary install directory directly |
| `--data-dir DIR` | Override where `prompts/` and `catalog/` are installed |
| `--system` | Install to `/usr/local/bin`; may require `sudo` |
| `--add-to-path` | Append the install directory to your shell profile |
| `--no-build` | Reuse pre-built `harnesscli` and `harnessd` binaries already in the repo root |
| `--uninstall` | Remove `go-code`, `harnesscli`, `harnessd`, and the data directory |
| `--dry-run` | Print what would happen without writing anything |

You can also set these environment variables **before** running the script to override the defaults without flags:

| Variable | Overrides |
|----------|-----------|
| `GO_CODE_PREFIX` | `--prefix` default |
| `GO_CODE_BINDIR` | `--bin-dir` default |
| `GO_CODE_DATA_DIR` | `--data-dir` default |

---

## Option 3: Build from source (development)

Use this path when you are working on go-code itself or want to run directly from a cloned checkout without a formal install step.

```bash
# Run the daemon directly
go run ./cmd/harnessd

# In a second terminal, run the CLI client
go run ./cmd/harnesscli -base-url http://127.0.0.1:8080 -prompt "Summarize the repository"
```

Or use `make` to build versioned binaries into `build/bin/`:

```bash
# Build harnesscli and harnessd into build/bin/
make build

# Delegate to install.sh (installs to ~/.local/bin by default)
make install
```

<Callout variant="info" title="No API key needed to smoke-test">
  The daemon supports a fake provider mode that requires no LLM credentials. To run the key-free server smoke test: <code>go test ./internal/server/... -run TestRunSmoke</code>. See the <a href="/docs/getting-started/quickstart">Quickstart</a> for a full walkthrough using <code>HARNESS_PROVIDER=fake</code>.
</Callout>

---

## What gets installed

Regardless of which method you use, a complete install puts these three things in place:

| Item | What it is |
|------|------------|
| `go-code` | Shell wrapper (`scripts/go-code.sh`). Auto-starts `harnessd` when no server is running, then launches the TUI or streams a prompt. This is the command you will use day-to-day. |
| `harnesscli` | Terminal client and BubbleTea TUI (`cmd/harnesscli`). `go-code` delegates to it under the hood. |
| `harnessd` | Local HTTP daemon and runtime bootstrap (`cmd/harnessd`). Listens on `:8080` by default. Handles runs, events, tools, providers, workflows, and more. |
| `prompts/` + `catalog/` | Runtime assets — bundled prompt templates and the model/provider catalog. `harnessd` reads these at startup. |

---

## Verify the install

```bash
# Check all three binaries are on PATH
which go-code harnesscli harnessd

# Print the go-code usage block
go-code --help
```

If `which` returns a path for all three and `go-code --help` prints without errors, you are ready to go.

<Callout variant="warning" title="PATH not updated?">
  If you used <code>--add-to-path</code> but the commands are still not found, open a new terminal session — changes to shell profile files take effect only in new shells. You can also run <code>source ~/.bashrc</code> (or the relevant profile file) in the current shell.
</Callout>

---

## Next steps

- **[Quickstart](/docs/getting-started/quickstart)** — Start `harnessd`, POST your first run, and stream events in under five minutes.
- **[Configuration](/docs/concepts/configuration)** — Learn the six-layer config cascade and the `HARNESS_*` environment variables that control the daemon.
- **[Providers & Routing](/docs/concepts/providers-and-models)** — Connect an LLM provider (OpenAI, Anthropic, Gemini, and more) by setting the right API key.
