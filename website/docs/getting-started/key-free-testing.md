---
title: "Running Without an API Key (Fake Provider)"
sidebar_label: "Key-Free Testing"
sidebar_position: 4
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

The **fake provider** lets you run `harnessd` and exercise the full run pipeline — HTTP request in, SSE events out, final summary — without an LLM API key, without a network call, and without Docker. Every response is read from a small JSON file you write yourself. Results are byte-stable across runs, which makes the fake provider the right choice for CI smoke tests, local integration checks, and any situation where you want to verify pipeline plumbing rather than model behavior.

Two canonical smokes ship with the repository and both use this path:

- **`TestRunSmoke`** — an in-process Go test that spins up a real HTTP server against a scripted `fakeprovider` instance.
- **`scripts/run-bench-smoke.sh`** — a shell smoke that builds `harnessd`, starts it with the fake provider, and drives it over HTTP with `curl`.

You can run either one right now without setting any API key.

---

## Why a fake provider

Real LLM calls have three properties that make them awkward for automated testing: they cost money, they are non-deterministic, and they require credentials. The fake provider removes all three constraints.

`fakeprovider.Provider` is a scripted, in-memory implementation of the `harness.Provider` interface. When `harnessd` starts with `HARNESS_PROVIDER=fake`, it reads your JSON turns file instead of making any network request. The same token counts and cost figure come back every time, so your assertions can be byte-stable.

This is also how you keep the CI pipeline green when no API key is available — `TestRunSmoke` and the shell smoke both set `HARNESS_PROVIDER=fake` and assert exact field values.

<Callout variant="info" title="No key, no Docker, no flakiness">
  The two canonical smokes require only a Go toolchain (for the Go test) or a Go toolchain plus `curl` and `python3` (for the shell smoke). Neither test makes a network call or starts a container.
</Callout>

---

## The turns file format

When `HARNESS_PROVIDER=fake`, `harnessd` reads the path stored in `HARNESS_FAKE_TURNS` at startup and parses it as a JSON array of turn objects. **`HARNESS_FAKE_TURNS` is required when `HARNESS_PROVIDER=fake`** — if the variable is empty or the file is missing, `harnessd` will fail at startup.

Each object in the array maps to one LLM turn that the fake provider will return, in order. The supported fields are:

| Field | Type | Description |
|---|---|---|
| `content` | string | Text returned as the assistant message for this turn |
| `tool_calls` | array (optional) | Tool calls emitted alongside the content |
| `usage` | object (optional) | Token counts — uses the **short keys** `prompt` and `completion` |
| `cost_usd` | number (optional) | Cost for this turn in USD |
| `cost_status` | string (optional) | One of `"available"`, `"unavailable"`, `"unpriced_model"` |

<Callout variant="warning" title="Short keys, not long names">
  The `usage` object uses `"prompt"` and `"completion"` as its keys — not `"prompt_tokens"` and `"completion_tokens"`. Using the long names will silently produce zero token counts. This is the `fakeProviderUsageJSON` struct in `cmd/harnessd/main.go`.
</Callout>

A minimal single-turn file looks like this:

```json
[
  {
    "content": "smoke ok",
    "usage": {"prompt": 100, "completion": 50},
    "cost_usd": 0.001,
    "cost_status": "available"
  }
]
```

These are the exact values used by both canonical smokes. When the run finishes, `GET /v1/runs/{id}/summary` will return `total_prompt_tokens: 100`, `total_completion_tokens: 50`, and `total_cost_usd: 0.001`.

<Callout variant="warning" title="total_cost_usd is derived, not provider-reported">
  The `total_cost_usd` field in the run summary is accumulated internally by the runner from each turn's scripted `cost_usd` value (the JSON `cost_usd` field, loaded into `CompletionResult.CostUSD`). It is not a field the LLM API returns — there is no billing data involved. Do not use it to infer what a real provider would charge.
</Callout>

---

## Starting harnessd in fake mode

You need three environment variables:

| Variable | Value | Purpose |
|---|---|---|
| `HARNESS_PROVIDER` | `fake` | Activates the fake provider path |
| `HARNESS_FAKE_TURNS` | path to your JSON file | Supplies the scripted turns |
| `HARNESS_AUTH_DISABLED` | `true` | Disables Bearer token auth so unauthenticated requests are accepted |

`HARNESS_AUTH_DISABLED=true` is the explicit way to skip Bearer auth for local smokes. (In the no-database smoke configuration auth is also implicitly disabled because no key store is configured, so requests already succeed; the env var makes the intent explicit and keeps the smoke correct even if a store is later configured.)

The runnable example below mirrors `scripts/run-bench-smoke.sh` exactly. Save the JSON to a file, then run the three-command sequence:

<Steps>
  <Step title="Write the turns file">

```bash
cat > /tmp/my-turns.json <<'EOF'
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

  </Step>
  <Step title="Build and start harnessd">

```bash
go build -o /tmp/harnessd-fake ./cmd/harnessd

HARNESS_ADDR="127.0.0.1:8093" \
HARNESS_AUTH_DISABLED=true \
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/my-turns.json \
  /tmp/harnessd-fake
```

The server prints `harness server listening on 127.0.0.1:8093` when it is ready.

  </Step>
  <Step title="Post a run and read the summary">

```bash
# Start a run — allow_fallback:true lets the runner fall back to the fake
# provider when there is no model registry entry for the requested model.
RUN_ID=$(curl -sf -X POST http://127.0.0.1:8093/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt":"hello","allow_fallback":true}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['run_id'])")

echo "run_id: ${RUN_ID}"

# Poll until the run reaches a terminal status.
while true; do
  STATUS=$(curl -sf "http://127.0.0.1:8093/v1/runs/${RUN_ID}" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['status'])")
  echo "status: ${STATUS}"
  [[ "${STATUS}" == "completed" || "${STATUS}" == "failed" ]] && break
  sleep 1
done

# Fetch the summary.
curl -sf "http://127.0.0.1:8093/v1/runs/${RUN_ID}/summary" | python3 -m json.tool
```

Expected summary (all fields shown; `run_id` is a UUID and varies across runs):

```json
{
  "run_id": "<uuid>",
  "status": "completed",
  "steps_taken": 1,
  "total_prompt_tokens": 100,
  "total_completion_tokens": 50,
  "total_cost_usd": 0.001,
  "cost_status": "available",
  "tool_calls": [],
  "cache_hit_rate": 0
}
```

  </Step>
</Steps>

<Callout variant="info" title="allow_fallback in the POST body">
  The `"allow_fallback": true` field in the POST body tells the runner to fall back to the configured default provider when the model registry lookup fails. Without a real API key, the registry cannot resolve a model entry, so fallback is needed to reach the fake provider. The shell smoke (`scripts/run-bench-smoke.sh`) includes this field; the in-process `TestRunSmoke` does not need it because it hands the fake provider straight to the runner.
</Callout>

---

## What is deterministic vs. derived

Not every field in the run summary is a direct copy of what you put in the turns file. Knowing which fields are stable lets you write reliable assertions.

<Tabs defaultValue="stable">
  <TabsList>
    <TabsTrigger value="stable">Byte-stable fields</TabsTrigger>
    <TabsTrigger value="derived">Derived fields</TabsTrigger>
  </TabsList>
  <TabsContent value="stable">

These fields are accumulated directly from the scripted turn values and produce the same output every run:

| Summary field | Source |
|---|---|
| `status` | `"completed"` when the runner finishes cleanly (no tool calls in the last turn) |
| `steps_taken` | One step per scripted turn consumed |
| `total_prompt_tokens` | Sum of `usage.prompt` across all turns |
| `total_completion_tokens` | Sum of `usage.completion` across all turns |
| `cost_status` | Value of `cost_status` from the last turn |

  </TabsContent>
  <TabsContent value="derived">

These fields are computed by the harness at runtime, not copied from the turns file:

| Field | Source | How it is computed |
|---|---|---|
| `total_cost_usd` | Run summary | Accumulated by `Runner.recordAccounting` from each turn's `cost_usd` value — not a provider API field |
| `run_id` | Run summary | A UUID generated at request time — never stable across runs |
| `duration_ms` | BenchmarkResult artifact (`benchresult.FromRun`) | `Run.UpdatedAt - Run.CreatedAt` in milliseconds — wall-clock time, will vary slightly across machines. **Not present in the run summary** (`GET /v1/runs/{id}/summary`). |

  </TabsContent>
</Tabs>

---

## The two canonical smokes

### In-process Go test — `TestRunSmoke`

This test lives in `internal/server/run_smoke_test.go` and exercises the full HTTP run pipeline in-process using `net/http/httptest`. No binary, no port, no shell required.

```bash
# Run without race detector (fast)
go test ./internal/server/... -run TestRunSmoke

# Run with race detector (required before merge)
go test ./internal/server/... -race -count=1 -run TestRunSmoke
```

The test asserts `status = "completed"`, `steps_taken = 1`, `total_prompt_tokens = 100`, `total_completion_tokens = 50`, `total_cost_usd = 0.001`, and `cost_status = "available"`. It also calls `prov.Calls()` to verify the fake provider was invoked exactly once, and exercises the `benchresult.FromRun` artifact schema.

### Shell smoke — `scripts/run-bench-smoke.sh`

This script builds a real `harnessd` binary, starts it with the fake provider, drives it over HTTP, and asserts the same deterministic fields. It is the integration-level counterpart to `TestRunSmoke`.

```bash
bash scripts/run-bench-smoke.sh
```

Requirements: Go toolchain, `curl`, `python3`. No API key.

When all assertions pass, the output ends with:

```
[bench-smoke] ALL ASSERTIONS PASSED
```

You can override the binary path, log file, turns file, and timeout via environment variables — see the table in the [Training & Benchmarks](/docs/operations/training-and-benchmarks) runbook for the full list.

---

## Next steps

Once you have the fake provider working, you can:

- Add more turns to the JSON file to simulate multi-step tool-calling sequences.
- When the provider runs out of scripted turns, `harnessd`'s fake provider uses the default `ExhaustEmpty` behavior (returns an empty result with no error). `ExhaustError` and `ExhaustRepeatLast` are only selectable in-process via `WithExhaustedBehavior` and are not reachable through `HARNESS_FAKE_TURNS`.
- Replace `HARNESS_PROVIDER=fake` with a real provider key (`OPENAI_API_KEY`) to run the same HTTP flow against a live model.
