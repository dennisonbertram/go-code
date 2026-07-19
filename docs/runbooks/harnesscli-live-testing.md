# Harness CLI Live Testing

This runbook covers the current `cmd/harnesscli` entrypoint and how it talks to the live harness server.

## Prerequisites

- A running harness server, usually `go run ./cmd/harnessd`
- `OPENAI_API_KEY` set in the server environment
- A reachable server base URL, usually `http://127.0.0.1:8080`

## What The CLI Does

The CLI creates a run with `POST /v1/runs`, then follows `GET /v1/runs/{id}/events` until the run reaches a terminal state.

## Current Flags

The CLI currently accepts:

- `-base-url`
- `-prompt`
- `-model`
- `-system-prompt`
- `-agent-intent`
- `-task-context`
- `-prompt-profile`
- `-prompt-behavior`
- `-prompt-talent`
- `-prompt-custom`
- `-list-profiles`
- `-tui`

The prompt-extension flags are forwarded into the run request and are the current way to exercise prompt customization from the CLI.

The CLI also supports:

- `harnesscli auth login` (flags: `-server`, `-tenant`, `-name`)

## Typical Commands

```bash
go run ./cmd/harnessd
```

```bash
go run ./cmd/harnesscli \
  -base-url http://127.0.0.1:8080 \
  -model gpt-4.1 \
  -prompt "Review the repository documentation for stale claims"
```

```bash
go run ./cmd/harnesscli \
  -base-url http://127.0.0.1:8080 \
  -prompt "Summarize the current API surface" \
  -tui
```

## Expected Behavior

- The CLI should print or render run progress from the event stream.
- Terminal events should stop the session cleanly.
- If a live run fails, inspect the server event stream first, then the run summary and conversation endpoints.

## Dashboard smoke

1. Start two runs, then enter the TUI and type `/dashboard` (or press `Ctrl+D`).
2. Confirm grouped running/waiting/completed rows refresh without closing the current session.
3. Select a running row and press `p`; confirm its event stream appears. Press `Esc` once to close peek and again to close the dashboard.
4. Use `s` to enter a steering prompt, `x` to cancel a selected run, and `n` to dispatch a new prompt. Confirm each change appears on the next refresh.

## Relevant Code Paths

- CLI entrypoint: `cmd/harnesscli/main.go`
- Auth subcommand dispatch and login flow: `cmd/harnesscli/auth.go`
- Run HTTP API: `internal/server/http.go`
- Run payload and response types: `internal/harness/types.go`
