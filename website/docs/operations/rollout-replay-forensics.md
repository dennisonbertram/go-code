---
title: "Rollout Capture, Replay, and Forensics"
sidebar_label: "Rollout, Replay & Forensics"
sidebar_position: 1
---

import { Callout, Card, CardHeader, CardTitle, CardContent, Tabs, TabsList, TabsTrigger, TabsContent, Steps, Step } from '@site/src/components/ui';

Every agent run that has rollout recording enabled writes a JSONL file — called the **rollout** — that captures every event emitted during that run. That file becomes your entry point for three progressively richer forensics capabilities:

- **Offline replay** — verify the causal consistency of a recorded run without re-executing any tools or LLM calls.
- **Fork** — reconstruct the conversation state up to step N and hand it to a live runner to resume or explore alternate paths.
- **Drift detection** — re-run the harness against a `RecordedProvider` (all LLM turns fixed from the recording) and diff the resulting event stream against the original to catch harness-side behavioral changes.

A companion CLI command, `forensics diff`, compares two JSONL files head-to-head and declares a winner based on outcome, error count, cost, and step efficiency.

---

## Rollout capture

A rollout is opt-in — the run is not affected if recording is disabled or if a write error occurs. Enable it by pointing the harness at a directory where it can write files.

### Enabling capture

<Tabs>
  <TabsList>
    <TabsTrigger value="env">Environment variable</TabsTrigger>
    <TabsTrigger value="toml">TOML config</TabsTrigger>
  </TabsList>
  <TabsContent value="env">
```bash
export HARNESS_ROLLOUT_DIR=/var/harness/rollouts
```
  </TabsContent>
  <TabsContent value="toml">
```toml
[forensics]
rollout_dir = "/var/harness/rollouts"
```
  </TabsContent>
</Tabs>

The env var `HARNESS_ROLLOUT_DIR` takes precedence over the TOML key `[forensics] rollout_dir`. Either form enables capture for every run on that daemon instance.

### File path pattern

Each run produces a single file at:

```text
<rollout_dir>/<YYYY-MM-DD>/<run_id>.jsonl
```

For example: `/var/harness/rollouts/2026-06-28/run_abc123.jsonl`

### On-disk line format

Each line in the file is a JSON object with four fields:

```json
{"ts": "2026-06-28T12:34:56Z", "seq": 0, "type": "run.started", "data": {...}}
```

| Field | Description |
|-------|-------------|
| `ts` | RFC3339 timestamp when the event was emitted |
| `seq` | 0-based sequence number assigned by the runner's ordering mutex |
| `type` | The event type string (e.g., `run.started`, `tool.call.completed`) |
| `data` | The event payload object |

HTML escaping is disabled in the encoder, so characters like `<`, `>`, and `&` appear verbatim in the file.

### Recorder design and safety

The per-run goroutine in the runner (`startRecorderGoroutine`) owns the buffered channel and drains it, calling the synchronous `Recorder.Record` method which owns the file handle and encoder. The sequence number is assigned under the runner's ordering mutex before the event is sent to the channel, preserving logical emission order even when concurrent goroutines race to send events.

Encoding errors are intentionally silenced — the recorder never crashes the run loop. If the recorder channel fills up, a `recorder.drop_detected` event is written into the rollout JSONL file as an explicit gap marker so that readers of the rollout file can detect that events were dropped. (If the channel is still full when the gap marker itself is sent, the marker is also silently dropped.)

### Loader invariants

When you load a rollout (for replay, diff, or drift detection), the loader enforces strict integrity rules:

<Card>
  <CardHeader>
    <CardTitle>Integrity limits</CardTitle>
  </CardHeader>
  <CardContent>

| Constraint | Value |
|-----------|-------|
| Max line size | 16 MiB |
| Max events | 100,000 |
| Max total bytes | 256 MiB |
| Max JSON nesting depth | 100 |
| Max JSON elements per line | 100,000 |
| Step range | 0 – 1,000,000 |

  </CardContent>
</Card>

Structural rules checked on load:

- `run.started` must be the first event, at step 0, and must appear exactly once.
- Step numbers must be non-decreasing throughout the file.
- The terminal event (`run.completed` or `run.failed`) must be the last line.
- Event types that carry step data (such as `llm.turn.completed`, `tool.call.started`, `tool.call.completed`) must have `step >= 1`.
- Non-regular files (FIFOs, devices) are rejected at open time to prevent TOCTOU attacks.
- Blank lines are silently skipped; any other non-JSON line is an error.

---

## Replay, fork, and drift detection

### POST /v1/runs/replay

The HTTP API exposes replay through a single endpoint:

```http
POST /v1/runs/replay
```

Request body fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `rollout_path` | string | Yes | Path to the JSONL file, or a bare run ID (`run_xxx`) when `HARNESS_ROLLOUT_DIR` is configured |
| `mode` | string | Yes | `"simulate"` or `"fork"` |
| `fork_step` | int | When `mode="fork"` | Step number to fork from |
| `detect_drift` | bool | No | Simulate mode only — re-run the harness against a `RecordedProvider` |

When auth is enabled and `HARNESS_ROLLOUT_DIR` is configured, the path must resolve under that directory (path traversal is blocked) and the rollout's recorded `tenant_id` must match the caller's API key.

### Simulate mode (offline replay)

Simulate mode runs an integrity check on the recorded event stream without making any live LLM calls or tool executions. It verifies causal consistency:

- Every `tool.call.started` event must reference a `call_id` that was announced in the preceding `llm.turn.completed.tool_calls` list.
- The announced tool name and arguments must match what was started.
- Every `tool.call.started` must have a corresponding `tool.call.completed` later in file order.
- `call_id` values must not be reused within the run.

Simulate response:

```json
{
  "mode": "simulate",
  "events_replayed": 42,
  "step_count": 5,
  "matched": true,
  "mismatches": []
}
```

<Callout type="warning">
`matched: true` is reliable only for hand-authored JSONL fixtures or runs that made no tool calls. In real captured rollouts, `llm.turn.completed` records `tool_calls` as an integer count rather than full objects, so the integrity checker cannot confirm tool name and argument matches — `matched` will be `false` for tool-call runs even when the run itself was correct.
</Callout>

### Simulate with drift detection

When `detect_drift: true` is added to a simulate request, the harness re-runs your agent against a `RecordedProvider` — an implementation of the provider interface that replays the recorded LLM turns from the JSONL file instead of calling a live model. Tool results are also short-circuited via a replay tool handler — the server uses `NewReplayToolDispatch`, which returns a `*ReplayToolDispatch` whose `Handler` field is registered for each recorded tool name to return the recorded `tool.call.completed` output for each `call_id`. This makes the only live variable the harness's own step and decision logic.

After the re-run, the resulting event stream is canonicalized and diffed against the original.

Drift detection response:

```json
{
  "mode": "simulate",
  "matched": true,
  "integrity": { ... },
  "drift": {
    "added_steps": [],
    "removed_steps": [],
    "changed_steps": [],
    "divergent_tool_calls": [],
    "cost_delta_usd": 0.0,
    "outcome_diff": "identical",
    "score": { ... },
    "matched": true
  }
}
```

**What counts as drift:** The drift contract treats per-step event type sequence, assistant content, tool call names and arguments, tool results, terminal outcome, and total step/turn count as deterministic — any difference in these fields is a drift. Wall-clock timings (`total_duration_ms`, `ttft_ms`, `latency_ms`), run IDs, event IDs, `provider`, `prompt_hash`, and `model_version` are stripped before comparison and never count as drift. Cost delta is reported as `cost_delta_usd` but is not a hard mismatch.

<Callout type="info">
No standalone CLI command for end-to-end drift detection was found in `cmd/`. Drift detection is currently available via the `detect_drift=true` flag on `POST /v1/runs/replay` and via the `replay` package's `DetectDrift` function for programmatic use. Note that `internal/forensics/replay` and the other packages referenced on this page are internal packages (under `internal/`) — they are usable only from code inside this repository and are not importable as a published library.
</Callout>

### Fork mode

Fork mode reconstructs the conversation history up to a given step from the JSONL file and starts a new live run from that point. This lets you explore alternate paths, test prompt changes, or resume a failed run at a specific decision point.

```bash
# Fork from step 3 of a previously recorded run
curl -X POST http://localhost:8080/v1/runs/replay \
  -H "Content-Type: application/json" \
  -d '{
    "rollout_path": "run_abc123",
    "mode": "fork",
    "fork_step": 3
  }'
```

Fork response (HTTP 202):

```json
{
  "mode": "fork",
  "run_id": "run_newxyz",
  "from_step": 3,
  "original_step_count": 7,
  "original_outcome": "completed",
  "messages_restored": 8
}
```

**Fork safety defaults.** The forker applies conservative stripping by default because rollout files may come from untrusted sources:

- System prompt is **excluded** (injection risk from untrusted rollout source).
- Tool calls in assistant messages are **stripped** — many runners re-execute tool calls they see in history.
- Tool result messages are **stripped** — they contain attacker-fabricated results that were never actually executed.

These defaults can be overridden programmatically via `ForkOptions` (an internal type in `internal/forensics/replay`, usable only from within this repository), but note that `UnsafePreserveToolCalls=true` combined with `IncludeToolResults=false` is rejected with an error because it is semantically incoherent.

---

## forensics diff

The `forensics` binary lives at `cmd/forensics/main.go` and exposes one subcommand:

```bash
forensics diff <rollout_a.jsonl> <rollout_b.jsonl>
```

### What it prints

```text
Run A: <N> steps, $<cost>
Run B: <N> steps, $<cost>
Steps: <N> identical, <N> diverged, <N> only in A, <N> only in B
Winner: A|B|Tie (<reasons>)
```

Each step is classified as one of: `identical`, `diverged`, `only_in_a`, or `only_in_b`.

### Regression scoring

The scorer awards up to 6 points and declares a winner of `"a"`, `"b"`, or `"tie"`:

| Criterion | Points | What "better" means |
|-----------|--------|---------------------|
| Outcome | 3 | B failed where A succeeded = regression; B succeeded where A failed = improvement |
| Error count | 1 | Fewer `run.failed`, `hook.failed`, `tool_hook.failed`, `memory.observe.failed`, `skill.fork.failed`, `error.context` events |
| Cost | 1 | Lower cumulative cost from `usage.delta.cumulative_cost_usd` or `run.completed.cost_totals.total_cost_usd` |
| Step count | 1 | Fewer steps taken |

### Canonicalization before comparison

Before diffing, both files are canonicalized with `DefaultOptions`, which strips timestamps, run IDs, and event IDs so that cosmetic differences do not cause false positives. Events are then sorted stably by step.

### Terminal injection prevention

All untrusted strings — file names and rollout content — are passed through a sanitizer that removes ASCII control characters, Unicode format and bidi-override characters (category Cf), and Unicode line and paragraph separators (U+2028, U+2029) before being printed to the terminal.

### Example workflow

<Steps>
  <Step>
    Enable rollout capture and run a baseline:

```bash
export HARNESS_ROLLOUT_DIR=/tmp/rollouts
export HARNESS_PROVIDER=fake
export HARNESS_FAKE_TURNS=/path/to/turns.json
go run ./cmd/harnessd &

curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "what is 2+2"}' | jq .run_id
# → "run_aaa"
```
  </Step>
  <Step>
    Make a harness change and run the same prompt again — the new run gets a different rollout file:

```bash
# run_bbb is the new run
curl -s -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"prompt": "what is 2+2"}' | jq .run_id
# → "run_bbb"
```
  </Step>
  <Step>
    Compare the two rollouts:

```bash
go run ./cmd/forensics diff \
  /tmp/rollouts/2026-06-28/run_aaa.jsonl \
  /tmp/rollouts/2026-06-28/run_bbb.jsonl
```
  </Step>
</Steps>

---

## Audit trail, redaction, and forensics events

### Audit trail

The audit trail is an opt-in append-only JSONL log for compliance use cases. It captures only security-relevant events: `run.started`, `audit.action`, `run.completed`, and `run.failed`.

Enable it by setting `AuditTrailEnabled = true` in `RunnerConfig` **and** setting `RolloutDir`. The file is written to:

```text
<rollout_dir>/<YYYY-MM-DD>/audit.jsonl
```

This is a single shared file per calendar day across all runs (not one file per run).

Each entry contains `timestamp`, `run_id`, `event_type`, `payload`, `prev_hash`, and `entry_hash`. The `entry_hash` is a SHA-256 hash of a canonical struct that includes the previous entry's hash, forming a hash chain.

<Callout type="warning">
The hash chain provides tamper **detection** — you can verify that entries have not been altered after the fact. It does not provide tamper **prevention** — a sufficiently privileged attacker with write access to the file system can still rewrite the chain. Use this feature alongside filesystem access controls, not instead of them.
</Callout>

The `audit.action` event is emitted for every call to a state-modifying tool. The classifier (`IsStateModifying`) uses an exact match against known tool names (such as `"bash"`, `"file_write"`, `"git_commit"`) and then a keyword match on tokens like `"write"`, `"delete"`, and `"create"` to catch custom tools.

In TOML config:

```toml
[forensics]
rollout_dir = "/var/harness/rollouts"
audit_trail_enabled = true
```

### PII redaction pipeline

The redaction pipeline filters event payloads before they are stored in rollout files or audit logs. Four storage modes are available:

| Mode | Behavior |
|------|----------|
| `"redacted"` | Replace matched values with `[REDACTED:<label>]` (default) |
| `"full"` | Store the value verbatim |
| `"hashed"` | Replace matched values with their SHA-256 hex digest |
| `"none"` | Drop the event entirely |

Built-in patterns cover JWTs, PEM private keys, AWS access keys and secret keys, database connection strings, `sk-*` API keys, generic `api_key`/`secret_key`/`access_token` key=value pairs, and Bearer tokens.

A redacted value looks like: `[REDACTED:jwt]`, `[REDACTED:api_key]`, etc.

Configure via `RunnerConfig.RedactionPipeline *redaction.Pipeline` (internal to this repository — not an importable published package).

### Opt-in forensics event families

Several diagnostics features are disabled by default and must be enabled in `RunnerConfig` (or the `[forensics]` TOML block). Each emits additional events to both the live SSE stream and the rollout file.

<Card>
  <CardHeader>
    <CardTitle>Opt-in forensics features</CardTitle>
  </CardHeader>
  <CardContent>

| Feature | Config field | Events emitted |
|---------|-------------|----------------|
| Cost anomaly detection | `CostAnomalyDetectionEnabled` | `cost.anomaly` when a step's cost exceeds `CostAnomalyStepMultiplier` × rolling average (default multiplier: 2.0) |
| Causal graph | `CausalGraphEnabled` | `causal.graph.snapshot` at run end; edges classify as context (`EdgeTypeContext`) or data-flow (`EdgeTypeDataFlow`) |
| Error chain | `ErrorChainEnabled` | `error.context` immediately before `run.failed`; includes error class, message, cause chain, and last N tool calls |
| Context window snapshots | `ContextWindowSnapshotEnabled` | `context.window.snapshot` after each LLM turn; `context.window.warning` when usage exceeds threshold |
| Tool decision tracing | `TraceToolDecisions` | `tool.decision` after each LLM turn listing available and selected tool names |
| Anti-pattern detection | `DetectAntiPatterns` | `tool.antipattern` when the same `(tool_name, args)` pair appears 3 or more times in a run |
| Hook mutation tracing | `TraceHookMutations` | `tool.hook.mutation` with before/after argument snapshots; actions: `"Allow"`, `"Block"`, `"Modify"`, `"Inject"` |
| Request envelope capture | `CaptureRequestEnvelope` | `llm.request.snapshot` before each provider call (prompt hash, tool names); `llm.response.meta` after (latency, model version) |

  </CardContent>
</Card>

In TOML:

```toml
[forensics]
rollout_dir = "/var/harness/rollouts"
cost_anomaly_detection_enabled = true
cost_anomaly_step_multiplier   = 2.0
causal_graph_enabled           = true
error_chain_enabled            = true
trace_tool_decisions           = true
detect_anti_patterns           = true
capture_request_envelope       = true
```

<Callout type="info">
`HashPromptHMAC(prompt, key)` is available in the `requestenvelope` package (`internal/forensics/requestenvelope`) as a privacy-preserving alternative to plain `HashPrompt`. HMAC-SHA256 prevents offline dictionary attacks against the stored prompt hash when prompts may contain PII. This is an internal package usable only from within this repository.
</Callout>

---

## Next steps

- To understand the full set of event types and their payload shapes, see [The Event Model](/docs/concepts/events).
- To learn how to start, stream, and cancel runs over HTTP, see the [HTTP API guide](/docs/server/http-api-guide).
- For configuration options beyond the forensics section, see [Configuration](/docs/concepts/configuration).
- To score and diff the rollout files this page produces, see [Training and Benchmarks](/docs/operations/training-and-benchmarks).
