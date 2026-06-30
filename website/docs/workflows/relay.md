---
title: "Go Relay: Multi-Location Control Plane"
sidebar_label: "Go Relay"
sidebar_position: 4
---

import { Callout, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

Go Relay is the orchestration layer that sits **above** go-code's execution runtime. While a single `harnessd` instance executes one run inside one workspace, Relay answers a higher-level question: *where*, *what*, and *how* should each run execute across a fleet of registered workers?

Think of go-code as a capable executor and Relay as the dispatcher. You register workers — processes running on your laptop, in containers, on VMs, or in sandboxes — and Relay decides which worker is best suited for each piece of work, then hands the work off with a fully specified `RunContract` that describes the prompt, the workspace, the granted capabilities, and the expected outputs.

<Callout variant="warning">
  Go Relay is a **library-level control-plane design**, not a shipped multi-region service. Today only worker registration and heartbeats are exposed over HTTP. Placement routing, run contract composition, handoff, and the cloud worker pool exist as in-memory Go packages and are not yet network-accessible. This page documents what exists in the codebase and clearly marks what is deferred.
</Callout>

---

## What Relay owns vs. what go-code owns

The division of responsibility is intentional and stable:

<div className="grid grid-cols-1 sm:grid-cols-2 gap-4 my-6">
  <Card>
    <CardHeader>
      <CardTitle>Go Relay owns</CardTitle>
    </CardHeader>
    <CardContent>
      <ul>
        <li>Worker registration and heartbeats</li>
        <li>Capability inventory and policy enforcement</li>
        <li>Placement routing (where does this run go?)</li>
        <li>Run contract composition (what does the worker receive?)</li>
        <li>Checkpointed handoff between workers</li>
        <li>Event, log, and artifact relay</li>
      </ul>
    </CardContent>
  </Card>
  <Card>
    <CardHeader>
      <CardTitle>go-code owns</CardTitle>
    </CardHeader>
    <CardContent>
      <ul>
        <li>Executing one run inside one workspace</li>
        <li>Provider and model calls</li>
        <li>Tool execution and sandbox enforcement</li>
        <li>Event emission and conversation history</li>
        <li>Workspace lifecycle (provision, destroy)</li>
        <li>Replay and continuation</li>
      </ul>
    </CardContent>
  </Card>
</div>

A direct go-code workflow — the TUI, single-shot CLI, daemon mode, run control, replay, search — continues to work without Go Relay. Relay is an additive layer for multi-worker scenarios.

---

## Workers and capabilities

### Worker model

A *worker* is any process that can accept and execute a run contract. Workers register themselves with the Relay control plane over HTTP and stay alive by sending periodic heartbeats.

Each worker carries these key attributes:

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique worker identifier |
| `tenant_id` | string | Tenant this worker belongs to |
| `name` | string | Human-readable name |
| `location_type` | string | Where the worker runs (see table below) |
| `status` | string | Current availability state |
| `trust_tier` | string | Permission level granted to this worker |
| `load` | int | Current concurrency count (0 = idle) |
| `supported_workspace_modes` | []string | Workspace backends this worker supports |
| `last_heartbeat` | time | Timestamp of the most recent heartbeat |

#### Location types

| Value | Meaning |
|---|---|
| `"local"` | Developer's own machine |
| `"worktree"` | Isolated git worktree on the same machine |
| `"container"` | Docker container |
| `"vm"` | Cloud or local virtual machine |
| `"sandbox"` | Restricted execution sandbox |

#### Worker status values

| Value | Meaning |
|---|---|
| `"online"` | Connected and accepting new runs |
| `"offline"` | Explicitly disconnected |
| `"stale"` | No heartbeat received within 30 seconds (`StaleDuration`) |
| `"draining"` | Finishing current work, not accepting new runs |

#### Trust tiers

| Value | Meaning |
|---|---|
| `"untrusted"` | Minimal permissions; destructive tools are denied by default |
| `"standard"` | Normal operation permissions |
| `"privileged"` | Full permissions, including cross-surface output; requires `admin` scope to assign |

<Callout variant="warning">
  Setting `trust_tier` to `"privileged"` requires the `admin` API scope. Standard write tokens (`runs:write`) cannot promote a worker to privileged.
</Callout>

### Stale detection

Workers that have not heartbeated within **30 seconds** transition to `"stale"` when `MarkStaleWorkers` is called. There is no background sweeper in the current codebase — callers or operators must invoke `MarkStaleWorkers` explicitly. The `ListWorkers` query excludes stale workers by default unless you filter by status explicitly.

### Capability inventory vs. capability pack

The capability system separates what a worker *can* provide from what a specific run *receives*:

- A **`CapabilityInventory`** is everything a worker advertises. It is keyed by `WorkerID`. Examples of capability types: `"tool"`, `"mcp_server"`, `"memory"`, `"repo"`, `"workspace_mode"`, `"secret"`, `"output_surface"`, `"browser"`, `"docker"`.

- A **`CapabilityPack`** is the bounded subset of capabilities actually granted to one specific run contract. It is keyed by `RunID`. Every capability in a pack must be explicitly approved — inventory is never automatically inherited.

**Secret handling:** `SecretCapability` stores only a reference (`Ref`) plus non-sensitive metadata (name, scope, provider), never the actual secret value. The `SanitizeInventoryForDisplay` function redacts repository paths and secret refs for non-local workers, and redacts any secret `Ref` longer than 128 characters (replacing it with a placeholder) for display.

---

## Placement and run contracts

### Three-phase placement

When a run needs to be dispatched, the `PlacementRouter` selects a worker in three phases:

1. **Hard constraints** — workers that fail any mandatory check are immediately rejected. Checks include: wrong status, insufficient trust tier, wrong location type, `LocalOnly` flag set, missing required workspace modes, missing required capabilities.

2. **Soft scoring** — each surviving worker receives a base score of 100 plus bonuses:

   | Condition | Bonus |
   |---|---|
   | Worker is `local` (default local bias) | +5 |
   | `PreferLocal` requested and worker is `local` | +25 |
   | `PreferCleanWorkspace` and worker is non-local | +25 |
   | `PreferCloudForLongRunning` and worker is `vm`, `sandbox`, or `container` | +20 |
   | Worker `load == 0` (idle) | +10 |
   | Worker `load < 3` (lightly loaded) | +5 |
   | Worker is `privileged` | +5 |

3. **Select best** — the highest-scoring worker wins. Ties are broken by worker ID for determinism.

When no worker passes hard constraints, the placement fails with a `PlacementRecord` that explains every rejection. Rejection categories are: `"offline"`, `"tenant"`, `"capability"`, `"trust"`, `"location"`, `"workspace"`, `"repo"`.

Every placement decision is recorded in a `PlacementRecord` that includes the selected worker, all eligible workers, all rejected workers with reasons, a routing reason string, and per-worker soft-score breakdowns.

### Run contracts

A `RunContract` is the fully specified unit of work that Relay hands to a worker. It is produced by the `Composer` from a user or connector request and includes:

| Field | Description |
|---|---|
| `ID` | Generated by Relay; format: `"rc-"` followed by 24 hex characters |
| `Prompt` | The task prompt |
| `Source` | Trigger origin: `"api"`, `"cron"`, `"github"`, `"linear"`, or `"slack"` |
| `Workspace` | Target workspace mode: `"local"`, `"worktree"`, `"container"`, `"vm"`, or `"sandbox"` |
| `Capabilities` | The `CapabilityPack` granted to this run |
| `Permissions` | Permission set (sandbox scope, approval policy) |
| `Limits` | Cost, step, and time limits |
| `Outputs` | Expected output types: `"artifact"`, `"approval"`, `"comment"`, `"patch"`, `"pr"`, `"summary"` |
| `Mobility` | How the run can move between workers (see below) |
| `Metadata` | Tenant, agent ID, labels |
| `ContextHint` | Additional context up to 64 KiB; longer inputs are truncated |

The `Composer` applies sensible defaults: workspace mode defaults to `"local"` when not specified; `api`-sourced runs without explicit outputs receive a summary output expectation (`type: "summary"`, `format: "markdown"`) automatically.

### Mobility classes

Mobility determines whether and how a run can be transferred to a different worker mid-execution:

| Value | Can be handed off? | Notes |
|---|---|---|
| `"pinned"` | No | Run stays on its original worker |
| `"resumable"` | Yes | Default; work can be picked up by another worker |
| `"cloneable"` | Yes | State can be copied to a new worker |
| `"ephemeral"` | No | State is not preserved after execution |

The default mobility when unspecified is `"resumable"`.

### Capability policy defaults

The default `CapabilityPolicy` (created with `NewCapabilityPolicy`) enforces:

- **`DenyUntrustedTools`**: untrusted workers cannot use `"bash:destructive"`, `"git:push"`, `"write:outside_workspace"`, or `"network:outbound"`.
- **`DenyRemoteSecrets`**: `true` — `org`-scoped secrets cannot be sent to non-local, non-privileged workers.
- **`DenyCrossTenantMemory`**: `true` — memory cannot cross tenant boundaries.
- **`RequireExplicitOutputSurface`**: `true` — every run must declare its output surface.

For non-local-only runs, `["bash:destructive", "git:push", "write:outside_workspace"]` require explicit approval by default.

---

## What ships today

<Callout variant="info">
  The table below distinguishes between capabilities that are **HTTP-exposed** (usable via `curl` or any HTTP client today) and capabilities that exist as **in-memory Go library code** (usable by embedding Go packages, but not via network API).
</Callout>

| Capability | Status | Notes |
|---|---|---|
| Worker CRUD + heartbeat | **HTTP-exposed** | `/v1/relay/workers` routes; requires `HARNESS_RELAY_DB` for persistence |
| Worker persistence (SQLite) | **HTTP-exposed** | Enabled by setting `HARNESS_RELAY_DB` |
| Placement router | In-memory library | `PlacementRouter` exists in `internal/relay/`; no HTTP route |
| Run contract composer | In-memory library | `Composer` exists in `internal/relay/`; no HTTP route |
| Checkpointed handoff | In-memory library | `HandoffManager` exists; packages are in-memory only, lost on restart |
| Cloud worker pool | In-memory library | `CloudWorkerConfig` exists; no auto-provisioning or HTTP route |
| Transport layer | In-memory library | No network transport; worker-to-relay communication is in-process only |
| Operator UX surface | In-memory library | `OperatorUX` exists; no HTTP route |
| Event/artifact store | In-memory library | SQLite implementation exists; not wired to HTTP routes yet |

### HTTP routes: `/v1/relay/workers`

All relay worker routes require `HARNESS_RELAY_DB` to be set. Without it, these endpoints return `501 Not Implemented`.

| Method | Path | Scope | Description |
|---|---|---|---|
| `GET` | `/v1/relay/workers` | `runs:read` | List workers; filter by `status`, `location_type`, `trust_tier`, `tenant_id` |
| `POST` | `/v1/relay/workers` | `runs:write` | Register a new worker |
| `GET` | `/v1/relay/workers/{id}` | `runs:read` | Get a worker by ID |
| `PUT` | `/v1/relay/workers/{id}` | `runs:write` | Update worker fields |
| `DELETE` | `/v1/relay/workers/{id}` | `runs:write` | Deregister a worker |
| `POST` | `/v1/relay/workers/{id}/heartbeat` | `runs:write` | Submit a heartbeat (`load` and `status` in body) |

Heartbeat status must be `"online"` or `"draining"` — sending `"stale"` or `"offline"` as a heartbeat status is rejected.

Workers are tenant-scoped. Requests can only see workers belonging to the authenticated tenant.

### Enabling worker persistence

Set `HARNESS_RELAY_DB` to a file path before starting `harnessd`:

```bash
export HARNESS_RELAY_DB=/var/lib/harness/relay.db
go run ./cmd/harnessd
```

The SQLite store uses WAL mode (`PRAGMA journal_mode=WAL`), a 5-second busy timeout, and foreign key enforcement. It creates a single `relay_workers` table with indexes on `tenant_id`, `status`, `location_type`, and `last_heartbeat`.

### Registering a worker and sending a heartbeat

The following example uses `curl` against a locally running `harnessd`. It assumes auth is disabled (`HARNESS_AUTH_DISABLED=true`) for simplicity — see [Authentication](/docs/server/authentication) for production token usage.

```bash
# Register a worker (id is required — the server never generates one)
curl -s -X POST http://localhost:8080/v1/relay/workers \
  -H "Content-Type: application/json" \
  -d '{
    "id": "dev-laptop-1",
    "name": "dev-laptop",
    "location_type": "local",
    "trust_tier": "standard",
    "supported_workspace_modes": ["local", "worktree"]
  }' | jq .

# The response echoes the worker you registered (with the id you supplied), e.g.:
# { "id": "dev-laptop-1", "status": "online", "last_heartbeat": "...", ... }

# Send a heartbeat
curl -s -X POST http://localhost:8080/v1/relay/workers/dev-laptop-1/heartbeat \
  -H "Content-Type: application/json" \
  -d '{
    "load": 0,
    "status": "online"
  }' | jq .

# List registered workers
curl -s "http://localhost:8080/v1/relay/workers" | jq .

# Filter by location type
curl -s "http://localhost:8080/v1/relay/workers?location_type=local" | jq .

# Deregister the worker
curl -s -X DELETE http://localhost:8080/v1/relay/workers/dev-laptop-1
```

---

## Deferred capabilities

These capabilities are designed and implemented as Go packages but are not yet connected to HTTP routes. Issue references are from the design document.

<Callout variant="info">
  The transport protocol for worker-to-relay communication (WebSocket, long-poll, or SSE plus command channel) is explicitly deferred. Secret resolution at execution time is tracked in issue #686. Cloud auto-provisioning (Relay provisioning VMs and containers itself) is tracked in issue #684. The `HandoffManager` uses in-memory storage only — handoff packages do not survive a restart.
</Callout>

**Checkpointed handoff** — the `HandoffManager` can create a `HandoffPackage` capturing a run's conversation summary, current todos, patch refs, artifact refs, workspace fingerprint, and non-portable state notes. The package records lineage (which worker ran which phase) and checks mobility class, tenant isolation, and target worker availability before allowing a transfer. This works today in Go unit tests but has no HTTP surface and no SQLite persistence.

**Cloud worker pool** — `CloudWorkerConfig` supports `"hetzner"`, `"aws"`, `"gcp"`, and `"sandbox"` providers with cost hints (e.g., `"~$0.05/hr"` for a VM) and risk hints. The `IsCloudWorker` and `IsLocalWorkerType` helpers classify workers. Auto-provisioning logic is deferred.

**Operator UX** — `OperatorUX` provides sorted worker summaries, per-run placement explanations, sanitized capability views, and artifact references for building dashboards. Not yet HTTP-exposed.

---

## Next steps

- **Run a workflow** — see the [Script Workflows API](/docs/server/script-workflows-api) page for composing multi-agent pipelines that the placement router can eventually dispatch.
- **Understand workspaces** — the [Workspaces](/docs/concepts/workspaces) page documents the `local`, `worktree`, `container`, and `vm` backends that map to Relay's location types.
- **Configure persistence** — the [Configuration reference](/docs/concepts/configuration) covers all `HARNESS_*` environment variables including `HARNESS_RELAY_DB`.
- **HTTP API reference** — the full route table is in the [HTTP API reference](/docs/reference/http-routes).
