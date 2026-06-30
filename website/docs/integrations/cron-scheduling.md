---
title: "Scheduling Recurring Work (Cron)"
sidebar_label: "Cron Scheduling"
sidebar_position: 4
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The go-code harness includes a built-in cron system for scheduling recurring shell commands or agent tasks on a calendar. You describe a job with a standard five-field cron expression, and the scheduler fires it at the right time, records the execution, and makes the results queryable — no external cron daemon required.

The system is useful for things like periodic benchmark runs, nightly cleanup, recurring agent tasks that check external services, or any work you would otherwise manage with `crontab` or a cloud scheduler.

---

## What cron provides

### Embedded scheduler vs. remote `cronsd`

By default, when `harnessd` starts up it spins an **embedded cron scheduler** backed by a SQLite database at `<workspace>/.harness/cron.db`. No extra process needed. Jobs are loaded from the store at startup, registered with the scheduler, and fired according to their schedule for as long as `harnessd` runs.

When you need to share a cron database across multiple `harnessd` instances, or you want the scheduler to outlive any single harness process, you can run `cronsd` — the standalone cron daemon — and point `harnessd` at it:

```bash
export HARNESS_CRON_URL=http://localhost:9090
```

When `HARNESS_CRON_URL` is set, the embedded scheduler is **not started**. `harnessd` instead proxies all cron operations over HTTP to the remote `cronsd`. The two modes are mutually exclusive.

<Callout type="info">
`cronsd` listens on `:9090` by default (`CRONSD_ADDR`) and stores its database at `~/.go-harness/cronsd.db` (`CRONSD_DB_PATH`). These are distinct from the embedded scheduler's `.harness/cron.db` path inside the workspace.
</Callout>

### Five-field UTC cron expressions

All schedules use standard five-field UTC cron syntax: `Minute Hour DayOfMonth Month DayOfWeek`. The scheduler is built on `github.com/robfig/cron/v3` configured for exactly this five-field format — **no seconds prefix**.

| Field | Range | Examples |
|-------|-------|---------|
| Minute | 0–59 | `*/5`, `0`, `30` |
| Hour | 0–23 | `9`, `*/2` |
| Day of month | 1–31 | `*`, `1` |
| Month | 1–12 | `*`, `6` |
| Day of week | 0–6 (Sun=0) | `1-5`, `*` |

```
# Every 5 minutes
*/5 * * * *

# Weekdays at 09:00 UTC
0 9 * * 1-5

# First day of each month at midnight
0 0 1 * *
```

<Callout type="warning">
Six-field expressions (with a leading seconds field) are **not supported** and will fail validation when you try to create a job. Keep it to five fields.
</Callout>

---

## Managing jobs

There are three ways to manage cron jobs, depending on your workflow.

<Tabs>
<TabsList>
  <TabsTrigger value="api">harnessd API</TabsTrigger>
  <TabsTrigger value="cronctl">cronctl CLI</TabsTrigger>
  <TabsTrigger value="agent">Agent tools</TabsTrigger>
</TabsList>

<TabsContent value="api">

#### harnessd HTTP API (`/v1/cron/jobs`)

`harnessd` exposes cron routes under `/v1/cron/jobs`. These require a Bearer token with the `runs:read` or `runs:write` scope (see [Authentication](/docs/server/authentication)).

| Method | Path | Scope | What it does |
|--------|------|-------|-------------|
| `GET` | `/v1/cron/jobs` | `runs:read` | List all jobs (tenant-filtered) |
| `POST` | `/v1/cron/jobs` | `runs:write` | Create a job |
| `GET` | `/v1/cron/jobs/{id}` | `runs:read` | Get a job by ID or name |
| `PATCH` | `/v1/cron/jobs/{id}` | `runs:write` | Update schedule, exec config, status, timeout, or tags |
| `DELETE` | `/v1/cron/jobs/{id}` | `runs:write` | Soft-delete a job |
| `POST` | `/v1/cron/jobs/{id}/pause` | `runs:write` | Pause a job |
| `POST` | `/v1/cron/jobs/{id}/resume` | `runs:write` | Resume a paused job |

**Create a job:**

```bash
curl -X POST http://localhost:8080/v1/cron/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "daily-report",
    "schedule": "0 9 * * 1-5",
    "execution_type": "shell",
    "execution_config": "{\"command\": \"curl -s http://localhost:8080/v1/runs -d '\''{}'\'' -H '\''Content-Type: application/json'\''\"}",
    "timeout_seconds": 60
  }'
```

**List jobs:**

```bash
curl http://localhost:8080/v1/cron/jobs
```

**Pause a job:**

```bash
curl -X POST http://localhost:8080/v1/cron/jobs/daily-report/pause
```

On success, `DELETE` returns HTTP 204. Create and updates return the job object.

**`PATCH` status values:** You can patch `status` to `"active"` or `"paused"` only — the value `"deleted"` is not accepted via `PATCH`. Use `DELETE` to remove a job.

</TabsContent>

<TabsContent value="cronctl">

#### `cronctl` CLI

`cronctl` is a CLI client for `cronsd`. It reads the base URL from `CRONSD_URL` (default `http://localhost:9090`).

```
cronctl <command> [flags]

Commands: create, list, get, delete, history, pause, resume, health
```

**Create a job:**

```bash
cronctl create \
  --name daily-report \
  --schedule "0 9 * * 1-5" \
  --command "curl -s http://localhost:8080/v1/runs -d '{}' -H 'Content-Type: application/json'" \
  --timeout 60
```

The `--type` flag defaults to `shell`. The `--timeout` flag defaults to `30` seconds.

**Other useful commands:**

```bash
# List all jobs
cronctl list

# Get a job and its recent executions
cronctl get daily-report

# View execution history
cronctl history daily-report --limit 20

# Pause and resume
cronctl pause daily-report
cronctl resume daily-report

# Check if cronsd is healthy
cronctl health
```

`cronctl` targets `cronsd` directly — it does not go through `harnessd`. Use the `harnessd` HTTP API or agent tools when you want to manage jobs through the harness server.

</TabsContent>

<TabsContent value="agent">

#### Agent cron tools

Running LLM agents have access to six cron tools when the cron client is available. Because these are **deferred** tools, the agent must activate them via `find_tool` before use.

| Tool | What it does | Mutating |
|------|-------------|---------|
| `cron_create` | Create a recurring job | yes |
| `cron_list` | List all jobs | no |
| `cron_get` | Get a job and its last 5 executions | no |
| `cron_delete` | Delete a job | yes |
| `cron_pause` | Pause a job | yes |
| `cron_resume` | Resume a paused job | yes |

`cron_create` accepts these parameters:

| Parameter | Type | Required | Default |
|-----------|------|----------|---------|
| `name` | string | yes | — |
| `schedule` | string | yes | — |
| `command` | string | yes | — |
| `timeout_seconds` | integer | no | `30` |

`cron_get` is handy for diagnostics: it returns both the job metadata and the five most recent executions in a single call, so the agent can see at a glance whether the last few runs succeeded or timed out.

</TabsContent>
</Tabs>

---

## Job structure

Every job has the following fields:

```json
{
  "id": "job_abc123",
  "name": "daily-report",
  "schedule": "0 9 * * 1-5",
  "execution_type": "shell",
  "execution_config": "{\"command\": \"./scripts/report.sh\"}",
  "status": "active",
  "timeout_seconds": 60,
  "tags": "reports,nightly",
  "next_run_at": "2026-06-29T09:00:00Z",
  "last_run_at": "2026-06-28T09:04:17Z",
  "created_at": "2026-06-01T00:00:00Z",
  "updated_at": "2026-06-01T00:00:00Z"
}
```

`execution_type` is either `"shell"` (run a shell command via `sh -c`) or `"harness"` (for harness-integrated execution). `execution_config` is a JSON blob whose shape depends on the type — for shell jobs it must contain a `"command"` key.

**Job status values:** `"active"`, `"paused"`, `"deleted"`

**Execution status values:** `"pending"`, `"running"`, `"success"`, `"failed"`, `"timeout"`

---

## Jitter and gotchas

### Jitter: why your jobs are never exactly on time

Every scheduled job is delayed by a random offset — called **jitter** — after the cron clock fires. The default range is 1 to 5 minutes (60–300 seconds). This intentional delay exists to prevent multiple jobs from stampeding simultaneously at minute-mark boundaries.

How it works:
1. When a job is registered, a deterministic base jitter offset is computed from a hash of the job's ID and schedule. The same job always gets the same base offset across restarts.
2. At fire time, the scheduler walks the offset forward (one second at a time, up to 120 additional seconds) to avoid landing on any of the configured "avoided minute marks" — by default `:00` and `:30`.
3. The scheduler sleeps for the final adjusted offset before dispatching the job.

<Callout type="warning">
Every job fires **1–5 minutes after its scheduled cron time** by default. If you write a test that creates a cron job and immediately checks for an execution, you will be surprised by this delay. Disable jitter in test environments or use a short window.
</Callout>

### Configuring jitter

<Tabs>
<TabsList>
  <TabsTrigger value="toml">TOML</TabsTrigger>
  <TabsTrigger value="env">Environment variables</TabsTrigger>
</TabsList>

<TabsContent value="toml">

```toml
[cron]
jitter_enabled = true
jitter_min_sec = 60
jitter_max_sec = 300
avoid_minute_marks = [0, 30]
log_jittered_times = true
```

</TabsContent>

<TabsContent value="env">

| Variable | Default | Description |
|----------|---------|-------------|
| `HARNESS_CRON_JITTER_ENABLED` | `true` | Enable or disable jitter (`"true"` / `"false"`) |
| `HARNESS_CRON_JITTER_MIN_SEC` | `60` | Minimum jitter offset in seconds |
| `HARNESS_CRON_JITTER_MAX_SEC` | `300` | Maximum jitter offset in seconds |

To disable jitter entirely:

```bash
HARNESS_CRON_JITTER_ENABLED=false
```

Note: `avoid_minute_marks` and `log_jittered_times` are **TOML-only** — there are no corresponding environment variable overrides.

</TabsContent>
</Tabs>

### Output truncation

Shell job output (stdout + stderr combined) is truncated to **4096 bytes** in the execution record. Jobs that produce more output will have their `output_summary` field silently cut off. If you need full output, redirect to a file from within the shell command.

### Soft delete and name reuse

`DELETE /v1/cron/jobs/{id}` does not immediately free the job's name. Instead it:
1. Sets `status` to `"deleted"`.
2. Appends `_deleted_<UnixNano>` to the job name.

This frees the original name so a new job can use it — but only after the rename completes. If you delete a job and immediately create one with the same name, the create will succeed.

### Execution concurrency

The scheduler caps concurrent job executions at `MaxConcurrent: 5` (both the embedded scheduler and the default `cronsd` setup). Jobs that would exceed this limit are queued but may be delayed.

---

## External triggers

`POST /v1/external/trigger` is a normalized webhook endpoint that routes one-off webhook events from external services (GitHub, Slack, Linear) to start, steer, or continue a harness run. It is separate from the cron scheduler — think of it as a push-based complement to cron's pull-based polling.

### Actions

| Action | Behavior |
|--------|---------|
| `"start"` | Always starts a new run |
| `"steer"` | Injects a message into an existing running or queued run |
| `"continue"` | Starts a new run continuing from a completed or failed run |

### Request shape

```json
{
  "source":     "github",
  "source_id":  "<delivery-id>",
  "repo_owner": "myorg",
  "repo_name":  "myrepo",
  "thread_id":  "42",
  "action":     "start",
  "message":    "Run the eval suite on PR #42",
  "tenant_id":  "",
  "agent_id":   "",
  "signature":  "<hmac>"
}
```

The signature can be provided either in the `"signature"` JSON field or in the `X-Trigger-Signature` HTTP header. The header takes precedence.

### Signature validators

Each source uses a different HMAC format:

| Source | Signature format |
|--------|----------------|
| `"github"` | `"sha256=<hex>"` (HMAC-SHA256) |
| `"slack"` | `"<unix_ts>:v0=<hex>"` (±5 min freshness check) |
| `"linear"` | Raw hex HMAC-SHA256 |

The corresponding secrets are set via `GITHUB_WEBHOOK_SECRET`, `SLACK_SIGNING_SECRET`, and `LINEAR_WEBHOOK_SECRET`. When a secret is set, the handler for that source is registered automatically.

### Response codes

| Code | Meaning |
|------|---------|
| `202` | Accepted |
| `400` | Invalid JSON or missing required fields |
| `401` | Signature validation failed, or no validator configured for the source |
| `404` | `steer` or `continue` action but no existing run for the thread |
| `409` | Run state mismatch (e.g. `steer` on a completed run) |
| `501` | Run store not configured |

<Callout type="info">
There are also source-specific webhook routes — `POST /v1/webhooks/github`, `POST /v1/webhooks/slack`, and `POST /v1/webhooks/linear` — that bypass Bearer auth and use HMAC validation directly. These are convenient when a platform requires a fixed webhook URL, but the `/v1/external/trigger` route gives you more flexibility for multi-source setups.
</Callout>

<Callout type="warning">
The `cloudscheduler` package (`internal/cloudscheduler/`) is a distinct, unrelated system for one-shot Docker-backed async job dispatch. Its package documentation describes HTTP routes (`POST /v1/cloud-jobs`, etc.), but **those routes are not implemented** in the current codebase — they exist only as design intent in the package comment. Do not attempt to call them.
</Callout>

---

## Reference: `cronsd` environment variables

When running `cronsd` as a standalone daemon:

| Variable | Default | Description |
|----------|---------|-------------|
| `CRONSD_ADDR` | `:9090` | Listen address |
| `CRONSD_DB_PATH` | `~/.go-harness/cronsd.db` | SQLite database file |
| `CRONSD_MAX_CONCURRENT` | `5` | Max simultaneous job executions |
| `CRONSD_URL` | `http://localhost:9090` | Used by `cronctl` to locate the daemon |

---

## Next steps

- To understand how `harnessd` authenticates these API calls, see [Authentication and Tenancy](/docs/server/authentication).
- To drive cron jobs from within a running agent, activate the deferred tools with `find_tool` and call `cron_create`.
- To connect external webhooks (GitHub, Slack, Linear) to run triggers, set the corresponding `*_WEBHOOK_SECRET` environment variables and point your webhook at the appropriate `/v1/webhooks/*` route or at `/v1/external/trigger`.
