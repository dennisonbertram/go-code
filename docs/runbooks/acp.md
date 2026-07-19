# ACP Server Mode

`harness-acp` exposes the existing `harnessd` run API to ACP-capable editors
over stdio. It is a protocol adapter only: it starts, continues, streams,
cancels, approves, and denies runs through harnessd's HTTP/SSE endpoints.

## Start

Start harnessd normally, then configure the editor command as:

```sh
HARNESS_ADDR=http://localhost:8080 harness-acp
```

`HARNESS_ADDR` defaults to `http://localhost:8080`. Standard output is reserved
for ACP JSON-RPC; diagnostics are written to standard error.

## Protocol Mapping

- `initialize` advertises the `go-code` ACP agent.
- `session/new` creates an ACP session and stable harnessd conversation ID.
- `session/prompt` posts a new run (or continues the prior run) and bridges
  harnessd SSE events to assistant text/thought, tool-call, and plan updates.
- `session/cancel` posts `/v1/runs/{id}/cancel`.
- Tool approval events use ACP `session/request_permission`; allow/deny posts
  the existing `/approve` or `/deny` endpoint.

## Manual Zed Verification Checklist

1. Build `go build ./cmd/harness-acp` and start `harnessd` with a local fake or
   configured provider.
2. Add `harness-acp` as an ACP agent in Zed, with `HARNESS_ADDR` set to the
   running daemon.
3. Start a new agent session and send a short prompt. Confirm streamed assistant
   text and, when supported by the provider, thinking appear before completion.
4. Ask for a safe tool action. Confirm the editor shows a live tool call and its
   final status.
5. Trigger a tool governed by the existing approval policy; verify both Allow
   and Deny are reflected in the harness run outcome.
6. Send a long-running prompt, cancel from Zed, and confirm a cancelled stop
   reason rather than a hung session.
7. Create/update todos and confirm the editor shows the corresponding ACP plan.

The automated fake ACP test is key-free; this checklist covers editor rendering
and interactive UX that cannot be asserted in a local Go test.
