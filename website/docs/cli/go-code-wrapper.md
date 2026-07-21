---
title: "The go-code Command"
sidebar_label: "go-code Wrapper"
sidebar_position: 1
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

`go-code` is the single terminal entry point for the entire go-code runtime. It is a shell wrapper (`scripts/go-code.sh`) that sits in front of `harnessd` (the daemon) and `harnesscli` (the CLI client). You never have to start the daemon manually — `go-code` detects whether a server is already running, boots one if needed, and shuts it down again when you are done.

**What you get from this one command:**

- An interactive, full-screen TUI for multi-turn agent conversations
- A single-shot streaming mode for scripted or piped use
- A daemon-only mode for long-lived background servers
- All run-management operations (list, inspect, cancel, continue, replay, search, improve)

---

## What the wrapper does

When you invoke `go-code`, the wrapper:

1. Checks whether `harnessd` is healthy at the configured port by hitting `GET /healthz`.
2. If no healthy server is found, starts `harnessd` in the background and waits up to 10 seconds for it to become ready.
3. Resolves your project root by walking up from the current directory.
4. Delegates to `harnesscli` (for TUI and prompt modes) or passes the subcommand straight through (for run-management operations).
5. On exit, stops the server **only if the wrapper started it**. A server you started independently is always left running.

<Callout type="info">
The auto-start/auto-stop contract is intentional. Running `go-code` in two terminals at the same time is safe: the second invocation detects the already-running server and reuses it without touching its lifecycle.
</Callout>

---

## Invocation modes

<Tabs>
  <TabsList>
    <TabsTrigger value="tui">TUI (default)</TabsTrigger>
    <TabsTrigger value="prompt">Single prompt</TabsTrigger>
    <TabsTrigger value="server">Server only</TabsTrigger>
    <TabsTrigger value="runs">Run management</TabsTrigger>
  </TabsList>

  <TabsContent value="tui">

**Interactive TUI**

```bash
go-code
```

Launches the BubbleTea full-screen TUI. The TUI connects to `harnessd` via `harnesscli --tui` and shows a live event stream, conversation history, and model/profile controls.

Use this mode when you want an interactive coding-agent session in your terminal.

  </TabsContent>

  <TabsContent value="prompt">

**Single-prompt streaming mode**

```bash
go-code "Summarize the files changed in the last commit"
```

Any argument that is not a recognized subcommand is treated as a prompt. The wrapper calls `harnesscli -prompt "<your text>"`, which:

1. Creates a run via `POST /v1/runs`
2. Streams SSE events from `GET /v1/runs/{id}/events` to stdout
3. Exits when a terminal event (`run.completed`, `run.failed`, or `run.cancelled`) is received

The underlying `harnesscli -prompt` stream produces three kinds of lines: `run_id=<id>` (first), `<event_type> <json_payload>` (one per event), and `terminal_event=<event_type>` (last). However, the `go-code` wrapper itself prints `[go-code] ...` status lines to stdout before delegating — for example `[go-code] server already running at ...` and `[go-code] project root: <path>`. These precede the `run_id=` line, so scripts must not rely on line position to extract the run id. Instead, grep for the `run_id=` prefix:

```bash
run_id=$(go-code "your prompt" | grep '^run_id=' | cut -d= -f2)
```

This mode is well-suited for shell scripts and CI pipelines.

  </TabsContent>

  <TabsContent value="server">

**Daemon-only mode**

```bash
go-code --server
```

Starts `harnessd` in the background, prints the server URL, and exits immediately. The server keeps running until you stop it explicitly. This is useful when you want a persistent server that outlives any single `go-code` invocation — for example, to connect a separate client or to share across terminal sessions.

Example output:

```
[go-code] no server at http://127.0.0.1:8080, starting harnessd on port 8080
[go-code] waiting for server to become healthy (pid 12345)...
[go-code] server is ready
[go-code] project root: /your/project/root
[go-code] server running at http://127.0.0.1:8080 (pid 12345)
http://127.0.0.1:8080
```

  </TabsContent>

  <TabsContent value="runs">

**Run management subcommands**

The wrapper delegates run-management operations directly to `harnesscli`:

```bash
go-code runs                            # list all known runs
go-code show <run-id>                   # inspect one run
go-code cancel <run-id>                 # cancel a running run
go-code continue <run-id> "next prompt" # continue a completed run and stream events
go-code replay <run-id-or-path>         # replay a recorded run
go-code search <query>                  # search run metadata
go-code improve [--target seam]         # run the self-improvement test loop
```

  </TabsContent>
</Tabs>

---

## Headless scripting and exit codes

The wrapper propagates the `harnesscli` exit code unchanged, so shell scripts and CI can branch on `$?` exactly as they would when calling `harnesscli` directly — including when the wrapper started the server itself (the auto-stop `EXIT` trap does not override the exit status):

```bash
go-code "summarize the diff"
case $? in
  0) echo "run completed" ;;
  2) echo "run failed" ;;
  3) echo "run blocked on input — resume interactively" ;;
  6) echo "run cancelled — resumable via go-code continue <run-id> ..." ;;
esac
```

The full code table (`0` completed, `1` client error, `2` failed, `3` blocked, `6` cancelled, `130` interrupted) and its per-command coverage are documented in [Exit Codes](/docs/reference/exit-codes).

---

## Subcommand reference

<Callout type="info">
`go-code runs` and `go-code list` are both aliases — they both map to `harnesscli list`. Similarly, `go-code show` and `go-code status` both map to `harnesscli status`.
</Callout>

| Invocation | Maps to `harnesscli` subcommand | What it does |
|---|---|---|
| `go-code` | `--tui` | Launches the interactive BubbleTea TUI |
| `go-code "prompt"` | `-prompt "..."` | Runs a single prompt, streams events, exits |
| `go-code --server` | (server lifecycle only) | Starts `harnessd` in background and exits |
| `go-code runs` | `list` | Lists known runs |
| `go-code list` | `list` | Alias for `runs` |
| `go-code show <id>` | `status` | Shows one run |
| `go-code status <id>` | `status` | Alias for `show` |
| `go-code cancel <id>` | `cancel` | Cancels one run |
| `go-code continue <id> "prompt"` | `continue` | Continues a completed run and streams events |
| `go-code replay <id-or-path>` | `replay` | Replays a recorded run |
| `go-code search <query>` | `search` | Searches run metadata (client-side substring match) |
| `go-code improve [flags]` | `improve` | Runs or plans the self-improvement test loop |

---

## Address and project root

### Server address: `HARNESS_ADDR`

The `HARNESS_ADDR` environment variable controls the listen address. The default is `:8080`. The wrapper extracts the port from this value and constructs the base URL as `http://127.0.0.1:<port>`.

```bash
# Run on a different port
HARNESS_ADDR=:9090 go-code "List the Go source files"
```

The address can also be set in your project or user config file (`~/.harness/config.toml` or `.harness/config.toml`). `HARNESS_ADDR` takes precedence over the TOML layers.

### Project root detection

`go-code` automatically resolves the workspace root before launching TUI or prompt mode. It walks parent directories from `$PWD`, looking for:

1. A `.git/` directory
2. A `.harness/config.toml` file

The first directory that contains either marker is used as the workspace root. If neither is found at any level, `$PWD` is used as the fallback.

The resolved path is passed to `harnesscli` as `-workspace <root>`, so the agent always operates relative to your project root — not the directory you happened to be in when you ran the command.

```
~/projects/
  myapp/          ← .git/ lives here → workspace root
    src/
      api/        ← you run "go-code" here
```

In the example above, running `go-code` from `src/api/` resolves the workspace root as `~/projects/myapp/`.

---

## Key-free smoke testing

You can run `go-code` without any provider API key by setting `HARNESS_PROVIDER=fake`. The fake provider returns deterministic responses and is the right choice for CI smoke tests and local integration checks.

```bash
# Start the server with the fake provider
HARNESS_PROVIDER=fake go-code --server

# In another terminal, run a prompt against it
go-code "hello"
```

Or in a single invocation:

```bash
HARNESS_PROVIDER=fake go-code "Does this pipeline work?"
```

<Callout type="warning">
The `--server` flag removes the auto-stop trap. After using `go-code --server`, stop the daemon manually with `pkill harnessd`. If you need to use the PID file directly, note that it is written to `${TMPDIR:-/tmp}/harnessd.<wrapper-pid>.pid` — on macOS `$TMPDIR` is a per-user temp directory (e.g. `/var/folders/.../T/`), not `/tmp`. The filename uses the launching wrapper's PID; the file's contents are harnessd's PID.
</Callout>

---

## Prerequisites

`go-code` requires three commands on your `PATH`: `harnessd`, `harnesscli`, and `curl`. If any of these are missing it exits with an error. The recommended install (`brew install --HEAD dennisonbertram/go-code/go-code` or `./scripts/install.sh --add-to-path`) places all three in the correct location automatically.

---

## Next steps

- **Configure the server** — set default model, cost limits, and provider keys: see the [Configuration](/docs/concepts/configuration) reference.
- **Understand run events** — learn what `run.completed`, `run.failed`, and the rest of the SSE event stream mean: see [Events](/docs/concepts/events).
- **Use `harnesscli` directly** — for flag-level control over individual subcommands without the wrapper: see the [harnesscli reference](/docs/cli/harnesscli).
