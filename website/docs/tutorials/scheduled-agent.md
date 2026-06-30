---
title: "Tutorial: A Scheduled Agent with Cron"
sidebar_label: "Scheduled Agent (Cron)"
sidebar_position: 6
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

The go-code harness includes a built-in cron system that lets you fire shell commands — or full agent runs — on a recurring schedule. Use it to run nightly evals, periodic code-health checks, automated report generation, or anything else that should happen on a schedule without human intervention.

This tutorial walks you through creating a cron job, understanding how jitter affects fire times, and inspecting execution history.

---

## How the cron system is structured

The harness ships two cron surfaces:

<Tabs defaultValue="embedded">
<TabsList>
  <TabsTrigger value="embedded">Embedded scheduler (default)</TabsTrigger>
  <TabsTrigger value="cronsd">Standalone cronsd</TabsTrigger>
</TabsList>
<TabsContent value="embedded">

When `harnessd` starts and `HARNESS_CRON_URL` is not set, it automatically starts an embedded cron scheduler backed by a SQLite database at `<workspace>/.harness/cron.db`. No extra process to manage.

Jobs created through `harnessd`'s HTTP API (`POST /v1/cron/jobs`) are stored there and run in-process alongside your agent workloads.

**Limitation:** The embedded scheduler does not expose a standalone HTTP port. The `cronctl` CLI talks to a separate `cronsd` daemon, so `cronctl history` and similar commands require `HARNESS_CRON_URL` to point at a running `cronsd`. If you only need job creation and management, use the `POST /v1/cron/jobs` API directly on `harnessd`.

</TabsContent>
<TabsContent value="cronsd">

`cronsd` is a standalone HTTP daemon backed by its own SQLite database (`~/.go-harness/cronsd.db` by default). It listens on port 9090 and is what `cronctl` talks to.

Run it alongside `harnessd`:

```bash
HARNESS_CRON_URL=http://localhost:9090 go run ./cmd/cronsd &
go run ./cmd/harnessd
```

When `HARNESS_CRON_URL` is set, `harnessd` delegates all cron operations to that remote `cronsd` over HTTP instead of using its embedded scheduler.

</TabsContent>
</Tabs>

**Cron expressions are always 5-field UTC** (`Minute Hour Dom Month Dow`). There is no seconds field — a 6-field expression will fail validation.

---

## Before you start

This tutorial uses the **key-free fake provider** path. You don't need an API key to follow along.

<Steps>

<Step title="Build and start harnessd with the fake provider">

Build each binary separately (a multi-package `go build` without `-o` discards all output):

```bash
go build -o cronsd ./cmd/cronsd
go build -o harnessd ./cmd/harnessd
go build -o cronctl ./cmd/cronctl
```

The fake provider requires a turns file. Create one now:

```bash
cat > /tmp/fake_turns.json <<'EOF'
[
  {
    "content": "smoke ok",
    "usage": {"prompt": 100, "completion": 50},
    "cost_usd": 0.001,
    "cost_status": "available"
  }
]
EOF
```

Start the daemons:

```bash
# Start the standalone cronsd daemon (port 9090)
./cronsd &

# Start harnessd pointing at cronsd for cron operations
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake_turns.json \
HARNESS_CRON_URL=http://localhost:9090 \
./harnessd &
```

Both daemons should be up in under a second. Verify them:

```bash
curl -s http://localhost:8080/healthz
# {"status":"ok"}

./cronctl health
# cronsd is healthy.
```

</Step>

<Step title="Confirm cronctl knows where cronsd lives">

`cronctl` reads `CRONSD_URL` (default `http://localhost:9090`). Export it once for the session:

```bash
export CRONSD_URL=http://localhost:9090
```

</Step>

</Steps>

---

## Create a cron job

### Using cronctl

```bash
cronctl create \
  --name daily-report \
  --schedule "0 9 * * 1-5" \
  --command "curl -s -X POST http://localhost:8080/v1/runs \
    -H 'Content-Type: application/json' \
    -d '{\"prompt\":\"Generate a daily code-health summary\"}'" \
  --timeout 120
```

Flags:

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--name` | yes | — | Unique job name |
| `--schedule` | yes | — | 5-field UTC cron expression |
| `--command` | yes | — | Shell command to run via `sh -c` |
| `--type` | no | `shell` | `shell` or `harness` |
| `--timeout` | no | `30` | Max execution time in seconds |

On success, `cronctl` prints the created job including its ID, `next_run_at`, and status (`active`).

### Using the HTTP API

You can also create jobs directly against the `harnessd` API, which is useful from scripts or CI pipelines:

```bash
curl -s -X POST http://localhost:8080/v1/cron/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name":           "daily-report",
    "schedule":       "0 9 * * 1-5",
    "execution_type": "shell",
    "execution_config": "{\"command\":\"curl -s -X POST http://localhost:8080/v1/runs -H '\''Content-Type: application/json'\'' -d '\''{\\\"prompt\\\":\\\"Generate a daily code-health summary\\\"}'\''\"}",
    "timeout_seconds": 120
  }'
```

The `execution_config` field is a JSON-encoded string containing `{"command": "<shell command>"}`. `execution_type` must be `"shell"` or `"harness"`.

List jobs to confirm:

```bash
cronctl list
# or
curl -s http://localhost:8080/v1/cron/jobs
```

---

## Understand jitter

<Callout variant="warning" title="Jobs do not fire at the exact scheduled minute">
By default, every job fires 1–5 minutes after its cron-scheduled time. This is intentional: jitter prevents all jobs from hammering downstream services at the top of the minute simultaneously (the "thundering herd" problem).

If you schedule a job for `0 9 * * *`, it will actually fire somewhere between 9:01 and 9:05 UTC each day.
</Callout>

The jitter parameters are fixed at the scheduler's built-in defaults and are not currently configurable at runtime. Although `HARNESS_CRON_JITTER_*` environment variables and `[cron]` TOML keys are parsed into config, the parsed values are not wired into either the embedded or standalone scheduler; both are constructed without a `Jitter` field, so `NewScheduler` always falls back to `DefaultJitterConfig` regardless of what those settings contain. Do not rely on them to change or disable jitter.

The built-in defaults are:

| Parameter | Default |
|-----------|---------|
| Minimum delay | 60 s (1 minute) |
| Maximum delay | 300 s (5 minutes) |
| Avoided minute marks | :00 and :30 |
| Jitter logging | enabled |

The base jitter offset for a given job is **deterministic**: it is computed from a hash of the job ID and schedule, so the same job always gets the same base delay across restarts. The minute-mark avoidance walk then adjusts that offset at each fire time to avoid landing on the avoided marks.

When jitter logging is enabled (the built-in default), the scheduler logs the applied jitter offset for each job at fire time — for example: `cron: job <id> jittered by 2m15s (original schedule: 0 9 * * *, base jitter: 2m10s)`.

---

## Trigger a run on schedule

A cron job whose command calls `POST /v1/runs` will start a full agent run at each scheduled (jittered) time. Here is a minimal example that you can adapt:

```bash
cronctl create \
  --name hourly-eval \
  --schedule "0 * * * *" \
  --command "curl -s -X POST http://localhost:8080/v1/runs \
    -H 'Content-Type: application/json' \
    -d '{\"prompt\":\"Run the test suite and report failures\",\"max_steps\":10}'"
```

The command runs via `sh -c`. It has access to everything in the shell environment at the time `cronsd` (or `harnessd`) started, including any environment variables you exported before launch.

<Callout variant="info">
The `execution_type` of `"harness"` exists in the schema for future use. For now, use `"shell"` and have your command call the `POST /v1/runs` API to start agent runs.
</Callout>

---

## Observe and manage

### Inspect execution history

`cronctl history` and the underlying `GET /v1/jobs/{id}/history` endpoint require the job's UUID. Passing a name returns an empty list rather than an error, making the mismatch silent — always use the ID:

```bash
# List recent executions for a job (default: last 20)
cronctl history c9f9627f-1fa1-4320-aeac-9cfd4383f54f

# Limit to 5
cronctl history c9f9627f-1fa1-4320-aeac-9cfd4383f54f --limit 5
```

This calls `GET /v1/jobs/{id}/history` on `cronsd`. Each execution record includes:

| Field | Description |
|-------|-------------|
| `status` | `pending`, `running`, `success`, `failed`, or `timeout` |
| `started_at` / `finished_at` | Wall-clock timestamps |
| `duration_ms` | Actual execution time |
| `output_summary` | First 4096 bytes of combined stdout+stderr |
| `error` | Error message if status is `failed` or `timeout` |
| `run_id` | The harness run ID, when execution type is `harness` |

<Callout variant="warning" title="Output is truncated at 4096 bytes">
The execution record stores only the first 4096 bytes of combined stdout and stderr. Commands that produce more output will have their output silently cut off in the history record. Redirect verbose output to a file or log aggregator if you need the full stream.
</Callout>

### Get a single job

```bash
cronctl get daily-report
# or by ID
cronctl get 3f9a1c2e-...
```

### Pause and resume

Pausing a job prevents future executions without deleting it. Any execution already in progress will complete.

`cronctl pause`, `cronctl resume`, and `cronctl delete` require the job's UUID — they do **not** accept names. Use `cronctl get <name>` or `cronctl list` to find the ID first:

```bash
# Get the job ID
cronctl get daily-report
#   ID:         c9f9627f-1fa1-4320-aeac-9cfd4383f54f
#   ...

cronctl pause c9f9627f-1fa1-4320-aeac-9cfd4383f54f
# Job paused.

cronctl resume c9f9627f-1fa1-4320-aeac-9cfd4383f54f
# Job resumed.
```

Via the API on `harnessd` (ID required — names return 404):

```bash
JOB_ID="c9f9627f-..."   # substitute your actual ID

curl -s -X POST http://localhost:8080/v1/cron/jobs/$JOB_ID/pause
curl -s -X POST http://localhost:8080/v1/cron/jobs/$JOB_ID/resume
```

Both endpoints return the updated job JSON (`status: "paused"` or `status: "active"`).

### Update a job

Change the schedule or timeout without recreating the job (job ID required):

```bash
# PATCH via harnessd API — use the job's UUID, not its name
JOB_ID="c9f9627f-..."   # substitute your actual ID

curl -s -X PATCH http://localhost:8080/v1/cron/jobs/$JOB_ID \
  -H "Content-Type: application/json" \
  -d '{"schedule": "30 8 * * 1-5", "timeout_seconds": 180}'
```

Valid `PATCH` status values are `"active"` and `"paused"` — `"deleted"` is not a valid patch value. Use `DELETE` to remove a job.

### Delete a job

`cronctl delete` requires the job's UUID (names are not accepted):

```bash
cronctl delete c9f9627f-1fa1-4320-aeac-9cfd4383f54f
# Job deleted.
```

Deletion is a soft-delete: the job's name gets a `_deleted_<timestamp>` suffix appended, freeing the original name for reuse.

---

## Quick reference

<Card>
<CardHeader>
<CardTitle>cronctl commands</CardTitle>
</CardHeader>
<CardContent>

| Command | What it does |
|---------|-------------|
| `cronctl create --name ... --schedule ... --command ...` | Create a new job |
| `cronctl list` | List all jobs |
| `cronctl get <id-or-name>` | Get a single job (accepts name or ID) |
| `cronctl history <id>` | Show execution history (requires job UUID) |
| `cronctl pause <id>` | Pause a job (requires job UUID) |
| `cronctl resume <id>` | Resume a paused job (requires job UUID) |
| `cronctl delete <id>` | Delete a job (requires job UUID) |
| `cronctl health` | Check that cronsd is reachable |

</CardContent>
</Card>

<Card>
<CardHeader>
<CardTitle>Key environment variables</CardTitle>
</CardHeader>
<CardContent>

| Variable | Default | Effect |
|----------|---------|--------|
| `HARNESS_CRON_URL` | (empty) | When set, `harnessd` uses this `cronsd` URL instead of the embedded scheduler |
| `CRONSD_URL` | `http://localhost:9090` | Base URL used by `cronctl` |
| `CRONSD_ADDR` | `:9090` | Listen address for the `cronsd` daemon |

</CardContent>
</Card>

---

## Next steps

- Read [Configuration](/docs/concepts/configuration) to learn how TOML config layers and environment variables interact.
- See [Runs and Conversations](/docs/concepts/runs-and-conversations) for a full description of the `POST /v1/runs` request body and event stream.
- To trigger runs from external systems instead of a schedule, see the [Events](/docs/concepts/events) page for the SSE event format, or explore `POST /v1/external/trigger` for webhook-driven runs from GitHub, Slack, or Linear.
