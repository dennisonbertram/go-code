---
title: "Troubleshooting"
sidebar_label: "Troubleshooting"
sidebar_position: 7
---

import { Callout } from '@site/src/components/ui';

Troubleshooting is a symptom-first guide to the most common problems you will encounter with go-code. For each symptom you will find the real cause and the exact fix — drawn from documented gotchas in the server, HTTP API, workspace, cron, Slack, MCP, TUI, and CLI layers.

<Callout type="warning">
Every entry here is grounded in source code. If behavior you see does not match a description below, the server version may differ from what is documented. Check the `/healthz` endpoint to confirm the server is running, then cross-reference the relevant fact sheet.
</Callout>

---

## Server and auth

### `GET /v1/runs` returns 501

**Symptom:** You call `GET /v1/runs` and receive HTTP 501 (not implemented), even though the server is running and `POST /v1/runs` works fine.

**Cause:** The run list endpoint requires a persistent store. When `HARNESS_RUN_DB` is not set, in-memory runs are not queryable through the list endpoint, and the handler returns 501.

**Fix:** Set `HARNESS_RUN_DB` to a SQLite file path before starting `harnessd`:

```bash
HARNESS_RUN_DB=./harness.db ./harnessd
```

The same applies to any endpoint that requires historical run data. The `/v1/runs/{id}` get-by-ID endpoint will still work for in-flight runs because it checks in-memory state first and falls back to the store.

---

### Auth is off but you never set `HARNESS_AUTH_DISABLED`

**Symptom:** Requests succeed without any `Authorization` header. You did not explicitly disable auth, but there are no 401 errors.

**Cause:** This is by design. Auth is implicitly disabled when no `store.Store` is configured. Without a key store there is nothing to validate a Bearer token against, so the auth middleware skips all checks. Setting `HARNESS_RUN_DB` enables the store and also enables auth.

**Fix:** If you want auth enforced, set `HARNESS_RUN_DB`. If you intend to run auth-free, set `HARNESS_AUTH_DISABLED=true` explicitly so the intent is clear to readers of your configuration.

```bash
# Explicit no-auth mode
HARNESS_AUTH_DISABLED=true ./harnessd
```

<Callout type="info">
This is by-design behavior, not a bug. The auth-off-without-a-store coupling is intentional: the store is both the persistence layer and the key registry.
</Callout>

---

### 501 from skills, subagents, script-workflows, or relay endpoints

**Symptom:** A route such as `GET /v1/skills`, `GET /v1/subagents`, `GET /v1/script-workflows`, or `GET /v1/relay/workers` returns 501.

**Cause:** These subsystems must be explicitly wired into `ServerOptions` before the server can serve them. Each subsystem is controlled by its own field: `Skills` and `SkillLister` for skills, `SubagentManager` for subagents, `ScriptWorkflows` for script workflows, and `RelayWorkerStore` for relay. When a field is nil, the corresponding handler returns 501.

**Fix:** Ensure the subsystem env vars and stores are configured. For relay workers, set `HARNESS_RELAY_DB`. For script workflows, confirm workflow scripts are compiled and registered. For skills, confirm `HARNESS_SKILLS_ENABLED=true` (the default) and that skill files exist under `~/.go-harness/skills` or `<workspace>/.go-harness/skills`.

---

## Runs and providers

### No provider configured

**Symptom:** `harnessd` starts but every run immediately fails with a provider or API key error.

**Cause:** No LLM provider was resolved at startup. The provider resolution order is: `HARNESS_PROVIDER=fake` first, then a catalog provider matching `HARNESS_MODEL`, then `OPENAI_API_KEY`, then an error.

**Fix:** Either set an API key for your provider, or use the key-free fake provider for local testing:

```bash
# Production: set an API key
OPENAI_API_KEY=sk-... ./harnessd

# Local smoke test: fake provider (no key required)
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/path/to/turns.json \
HARNESS_AUTH_DISABLED=true \
  ./harnessd
```

See the fake provider documentation for the format of `HARNESS_FAKE_TURNS`.

---

### Run never starts in fake mode — no events emitted

**Symptom:** You start `harnessd` with `HARNESS_PROVIDER=fake`, post a run, and the run either stays in `queued` state or fails immediately. The SSE stream is empty or contains only a `run.failed` event.

**Cause:** `HARNESS_FAKE_TURNS` is not set or the file is missing. The fake provider requires a turns file at startup — if the path is empty or the file cannot be read, `harnessd` fails to start (or the provider errors immediately). The fake provider serves turns as an ordered sequential script: it plays turn 0 on the first call, turn 1 on the second, and so on. There is no prompt-matching mechanism; when turns run out the default behavior is to return an empty result with no error.

**Fix:** Ensure `HARNESS_FAKE_TURNS` points to a valid JSON turns file (an array of turn objects in the order they should be served):

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/path/to/turns.json \
HARNESS_AUTH_DISABLED=true \
  ./harnessd
```

---

### `max_steps` is unexpectedly 8 when you expected unlimited

**Symptom:** Runs hit the step cap at 8 even though you did not set `HARNESS_MAX_STEPS` and your config file has `max_steps = 0`.

**Cause:** `harnessd` applies a backward-compatibility rule: if `max_steps` resolves to `0` after all config layers are merged and `HARNESS_MAX_STEPS` env var is absent, the runtime resets it to `8`. The value `0` in the config means "unlimited," but only when `HARNESS_MAX_STEPS` is explicitly present in the environment — even as `0`.

**Fix:** Set `HARNESS_MAX_STEPS=0` explicitly in your environment to unlock unlimited steps:

```bash
HARNESS_MAX_STEPS=0 ./harnessd
```

<Callout type="warning">
This is intentional behavior, not a bug. The default of 8 prevents runaway cost on misconfigured deployments. Always set `HARNESS_MAX_STEPS` explicitly in production.
</Callout>

---

## Workspaces and integrations

### VM workspace: file and shell tools run on the host, not in the guest

**Symptom:** You start a run with `"workspace_type": "vm"` expecting all file operations to happen inside the VM, but `write`, `edit`, and `bash` tool calls appear to affect the host machine instead.

**Cause:** This is a known, documented limitation tracked in issue #564. The VM workspace tool routing is incomplete. File and shell tools (`write`, `edit`, `bash`) execute on the host, not inside the guest VM. The runner emits a `prompt.warning` event with code `"vm_workspace_tool_routing"` on each VM-type run to signal this.

**Fix:** There is no workaround in the current release. With a `container` workspace, the workspace directory is bind-mounted from the host into the container so file edits are visible inside the guest, but tool execution (including `bash`) still runs in the host `harnessd` process against the bind-mounted directory — full in-guest tool routing is not implemented.

<Callout type="warning">
This is a known bug (issue #564), not a misconfiguration. The `prompt.warning` event in the SSE stream is the runtime signal that this limitation is active.
</Callout>

---

### Cron job fires later than the schedule says

**Symptom:** A job scheduled at `0 9 * * 1-5` (9:00 AM weekdays) consistently fires at 9:02 or later, never exactly on the minute.

**Cause:** By design. The cron system applies jitter to every active job to avoid thundering-herd effects. The default jitter range is 60–300 seconds (1–5 minutes) with minute marks at `:00` and `:30` avoided. The base jitter offset is deterministic per job (derived from a hash of the job ID and schedule), but it means every job will always fire 1–5 minutes after its nominal schedule time.

**Fix:** To disable jitter entirely, set `HARNESS_CRON_JITTER_ENABLED=false`:

```bash
HARNESS_CRON_JITTER_ENABLED=false ./harnessd
```

To narrow the range but keep some jitter, set `HARNESS_CRON_JITTER_MIN_SEC` and `HARNESS_CRON_JITTER_MAX_SEC`:

```bash
HARNESS_CRON_JITTER_MIN_SEC=0 HARNESS_CRON_JITTER_MAX_SEC=5 ./harnessd
```

<Callout type="info">
The `avoid_minute_marks` and `log_jittered_times` settings are TOML-only — there are no corresponding env vars. Configure them in `.harness/config.toml` under `[cron]`.
</Callout>

---

### Slack webhook returns 401

**Symptom:** Slack sends an event to `POST /v1/webhooks/slack` and receives HTTP 401. Your harnessd is running and other routes work.

**Cause:** The Slack adapter is only registered when `SLACK_SIGNING_SECRET` is set at startup. When the env var is absent, any request to the Slack webhook endpoint returns `401` with `"Slack webhook adapter not configured"`. A 401 can also occur if `SLACK_SIGNING_SECRET` is set but the request's HMAC signature does not match (wrong secret, stale timestamp, or missing headers).

**Fix:**

1. Set `SLACK_SIGNING_SECRET` to the signing secret from your Slack app's Basic Information page before starting `harnessd`.
2. Verify both `X-Slack-Request-Timestamp` and `X-Slack-Signature` headers are present on the request — Slack sends both automatically; missing either returns 400.
3. Check that the timestamp is within 5 minutes of server time (the validator enforces a ±300 second freshness window).

```bash
SLACK_SIGNING_SECRET=xoxb-... ./harnessd
```

<Callout type="info">
The Slack webhook integration only supports steering existing runs — all Slack events produce `Action = "steer"`. To start a new run from Slack, use the generic `POST /v1/external/trigger` endpoint with `"action": "start"` instead.
</Callout>

---

### MCP tool name not found in agent's tool list

**Symptom:** You configure an MCP server and expect the agent to call a tool named `filesystem__read_file` (double underscore), but the agent cannot find the tool.

**Cause:** The runbook at `docs/runbooks/mcp.md` documents the tool name format incorrectly as `{server_name}__{tool_name}` (double underscore). The actual format produced by the code is `mcp_{server}_{tool}` — a `mcp_` prefix followed by the server name and tool name joined with a single underscore. Names are lowercased and trimmed, and the characters `-`, ` `, `/`, and `.` are replaced with `_` (an empty part becomes `x`).

**Fix:** Use the `mcp_` prefix and single-underscore separators:

| Server | Tool | Correct name |
|--------|------|--------------|
| `filesystem` | `read_file` | `mcp_filesystem_read_file` |
| `my-server` | `get_data` | `mcp_my_server_get_data` |

You can verify the full list of available MCP tools by calling `GET /v1/mcp/servers`, which returns each connected server's tool list.

<Callout type="warning">
The runbook `docs/runbooks/mcp.md` documents the double-underscore format. This is incorrect. The source code at `internal/harness/tools/mcp.go` and `internal/harness/tools/deferred/mcp.go` both use `mcp_{server}_{tool}` (single underscore, `mcp_` prefix).
</Callout>

---

## TUI and CLI

### `--tui` fails with "requires a terminal"

**Symptom:** Running `harnesscli --tui` exits immediately with an error like:

```
--tui requires a terminal; pipe output or use without --tui for streaming mode
```

**Cause:** The TUI requires a real TTY on stdout. If you pipe output, run inside a non-interactive script, or redirect stdout, `term.IsTerminal` returns false and the TUI refuses to launch. This is by design — the BubbleTea framework uses alternate-screen mode and does not work correctly without a real terminal.

**Fix:** Run `harnesscli --tui` in an interactive terminal session, not inside a pipe or script. For automated usage, use the one-shot mode instead:

```bash
# Interactive TUI
harnesscli --tui

# Scriptable one-shot mode (no TTY needed)
harnesscli --prompt "do the thing" | grep run_id
```

---

### Profile rejected with HTTP 400 when using TUI or API

**Symptom:** You send a run request with a profile name in the `prompt_profile` field (or the TUI sends it there) and receive HTTP 400.

**Cause:** There are two distinct profile fields in `RunRequest`:
- `profile` — a capability profile (tool restrictions, isolation mode). This is what the TUI sends when you select a profile via `/profiles`.
- `prompt_profile` — a prompt routing profile, used for model routing. This is a separate, narrower concept.

Sending a capability profile name in `prompt_profile` causes the server to reject the request because the two fields expect different value spaces.

**Fix:** Use the correct field for what you are trying to do:

```json
{
  "prompt": "...",
  "profile": "my-capability-profile"
}
```

Not:

```json
{
  "prompt": "...",
  "prompt_profile": "my-capability-profile"
}
```

The TUI's `/profiles` command correctly uses the `profile` field automatically.

---

### `harnesscli search` finds nothing even when runs exist

**Symptom:** `harnesscli search "my query"` returns no results even though you can see runs with `harnesscli list`.

**Cause:** The `search` subcommand is client-side only. It calls `GET /v1/runs` to fetch all runs, then performs a case-insensitive substring match in-process. It does not call a dedicated server search endpoint.

This means:
- If `HARNESS_RUN_DB` is not set, `GET /v1/runs` returns 501 (no store), and `search` has nothing to search.
- Only runs that `GET /v1/runs` returns can be found — in-memory runs not persisted to the store may not appear.
- The query matches against: run ID, conversation ID, tenant ID, model, prompt, output, status, and error fields.

**Fix:** Ensure `HARNESS_RUN_DB` is set so `GET /v1/runs` returns results. If it is set and you still see nothing, confirm your query substring appears in one of the matched fields — the match is case-insensitive but must be a literal substring.

---

## Next steps

- For server configuration reference, see the [Config and Environment Variables](/docs/reference/environment-variables) page.
- For the full HTTP route catalog, see the [HTTP API Reference](/docs/reference/http-routes).
- For workspace backend details, see the [Workspaces and Sandboxes](/docs/concepts/workspaces) guide.
