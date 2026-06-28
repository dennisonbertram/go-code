# Go Relay Architecture

Date: 2026-06-27
Feature/system: Go Relay multi-location control plane
Parent epic: #676

## Problem Statement

`go-code` can execute coding work inside a workspace, stream events, continue runs,
use profiles/tools/MCP, and select workspace backends. The missing product layer answers:

- Where should this task run: local laptop, office machine, cloud VM, container, sandbox, or worktree?
- What context, memory, connector data, tools, and permissions should the worker receive?
- How should Slack/GitHub/Linear/cron/email triggers become safe, permissioned, observable runs?
- How does a user watch, steer, cancel, approve, or move work across devices?
- How do we keep local execution first-class while allowing cloud execution for clean or long-running work?

## Terminology

| Term | Definition |
|---|---|
| **Go Relay** | The multi-location control plane that routes and composes coding work across registered workers. |
| **Worker** | A registered execution location capable of running `go-code` tasks. Has an identity, location type, status, capabilities, and trust tier. |
| **Location** | The physical/logical place a worker runs: local machine, worktree, container, VM, or sandbox. |
| **Location Type** | `local`, `worktree`, `container`, `vm`, or `sandbox`. |
| **Capability Inventory** | What a worker advertises it can provide: tools, MCP servers, memory refs, repos, workspace modes, secret refs, output surfaces. |
| **Capability Pack** | The bounded subset of capabilities actually granted to a specific run contract. |
| **Run Contract** | The complete specification of what to execute: prompt, source, workspace target, capabilities, permissions, limits, output expectations, and mobility class. |
| **Placement Record** | A persistent record of which worker was selected for a run, which workers were eligible, which were rejected, and why. |
| **Artifact** | A durable output of a run: patch, PR link, test log, screenshot, summary, Slack/GitHub reply. |
| **Mobility Class** | How a run can be moved between workers: `pinned`, `resumable`, `cloneable`, `ephemeral`. |
| **Checkpoint Handoff** | Moving a run from one worker to another at a safe boundary (after LLM turn, after tool call, etc.), not live process migration. |
| **Connector** | An external integration (Slack, GitHub, Linear) that brings work into Go Relay. |

## Product Boundary

### Go Relay Owns

- Registered execution locations and worker heartbeats.
- Stale-worker marking is currently an explicit store operation for callers/operators; this PR does not add a `harnessd` background sweeper.
- Worker capability inventory: repos, workspace modes, tools, MCP, memory scopes, connector tools, runtime limits, availability.
- Routing policy and explainable placement decisions.
- Workflow composition from connector/user intent into a concrete `go-code` run contract.
- Event, log, patch, artifact, and status relay across clients/connectors.
- Capability-pack and permission policy for connector-provided tools/memory.
- Checkpointed handoff between workers at safe boundaries.

### `go-code` Owns

- Executing one run inside one workspace.
- Provider/model calls, tool execution, sandbox/approval enforcement, event emission, continuation, replay, summaries, and workspace lifecycle.

## Run Contract Schema

### Required Fields

```json
{
  "id": "relay-run-abc123",
  "prompt": "Fix the race condition in pool.go",
  "source": {
    "type": "github",
    "trigger_id": "gh-webhook-xyz",
    "thread_id": "github:anthropic/go-code:1234"
  },
  "workspace": {
    "mode": "worktree",
    "repo_url": "https://github.com/anthropic/go-code.git",
    "repo_path": "/home/user/repos/go-code",
    "clean": false
  },
  "capabilities": {
    "tools": ["bash", "read", "write", "edit", "git"],
    "mcp_servers": [],
    "memory_refs": ["repo:go-code"],
    "secret_refs": [],
    "output_surfaces": ["github:comment", "github:pr"]
  },
  "permissions": {
    "approval_required": ["bash:destructive", "git:push"],
    "trust_tier_minimum": "standard"
  },
  "limits": {
    "max_turns": 50,
    "budget_tokens": 200000,
    "timeout_seconds": 1800
  },
  "outputs": {
    "expected": ["patch", "github:pr"],
    "format": "diff"
  },
  "mobility": "resumable",
  "metadata": {
    "tenant_id": "org-abc",
    "created_by": "user:alice",
    "created_at": "2026-06-27T10:00:00Z",
    "tags": ["bugfix", "high-priority"]
  }
}
```

### Field Ownership

| Field Group | Relay-Owned | `go-code`-Owned | Notes |
|---|---|---|---|
| `id` | ✓ | | Relay generates the run contract ID |
| `prompt` | | ✓ | Passed through to the runner |
| `source` | ✓ | | Relay hydrates from trigger, `go-code` never sees raw webhook |
| `workspace` | ✓ | | Relay selects workspace target; `go-code` receives `workspace_type` |
| `capabilities` | ✓ | | Relay gates; `go-code` receives resolved tool/MCP set |
| `permissions` | ✓ | | Relay policy; `go-code` enforces approval broker |
| `limits` | ✓ | ✓ | Relay sets bounds; `go-code` enforces |
| `outputs` | ✓ | | Relay routes artifacts to connector reply surfaces |
| `mobility` | ✓ | | Relay decides if/when to move; `go-code` sees mobility class |
| `metadata` | ✓ | ✓ | Relay attaches audit metadata; `go-code` receives tenant/run ID |

## Security-Sensitive Field Classification

| Field | Classification | Handling |
|---|---|---|
| `prompt` | Context payload | Passed to worker; logged with redaction rules |
| `source.trigger_id` | Metadata | Stored for audit; not exposed to worker |
| `workspace.repo_path` | Secret reference | Never exposed to non-local workers; redacted in logs |
| `capabilities.secret_refs` | Secret reference | References only; values resolved by worker at execution time |
| `capabilities.mcp_servers[*].api_key` | Secret reference | References only; never stored in plaintext |
| `metadata.tenant_id` | Metadata | Used for isolation; visible in audit |
| `permissions.approval_required` | Tool permission | Gates tool execution; visible in run contract |
| `source.raw_body` | Context payload | Never persisted by Relay; worker never sees |

## Existing Feature Mapping

| Existing Feature | How Go Relay Uses It |
|---|---|
| `/v1/runs` (POST/GET) | Relay dispatches to worker's `go-code` instance |
| `/v1/runs/{id}/events` (SSE) | Worker streams to Relay; Relay proxies to clients |
| `continue` / `steer` / `cancel` | Relay proxies commands to the correct worker |
| `workspace_type` | Relay selects based on location capabilities |
| `profiles` | Relay attaches profile refs to run contracts |
| MCP servers | Relay gates via capability policy; worker receives resolved set |
| Trigger envelopes (#411) | Relay accepts as input to composition pipeline |
| GitHub webhook (#412) | Relay ingests; composes into run contract |
| Slack/Linear adapters (#413) | Relay ingests; composes into run contract |
| Workspace backend selection (#414) | Relay extends to multi-location routing |
| Checkpoints | Relay uses for handoff packages |
| Store (SQLite) | Relay extends schema for workers, placement records, artifacts |

## Sequencing Guidance

Implementation order follows the dependency chain:

1. **#678 (this doc)** — Design document and contract definitions. **DONE FIRST.**
2. **#679** — Worker registration and heartbeat. Foundation for all worker-aware features.
3. **#680** — Capability inventory and capability-pack schema. Extends worker model.
4. **#681** — Deterministic placement routing. Depends on worker + capability models.
5. **#682** — Run contract composition pipeline. Depends on contract schema + capability-pack.
6. **#683** — Local worker relay transport. Depends on worker registration + placement router.
7. **#684** — Cloud/sandbox worker pool. Depends on worker registration + placement router.
8. **#685** — Event/artifact persistence and relay. Depends on local transport + contract schema.
9. **#686** — Connector capability policy. Depends on capability-pack + composition.
10. **#687** — Checkpointed task handoff. Depends on events/artifacts + placement router.
11. **#688** — Operator UX. Depends on worker registration + placement router + events/artifacts.

Slices 2-4 are parallelizable once the design doc is stable. Slices 6-7 are parallelizable
after the placement router. Slice 11 can begin after its three dependencies are done.

## Open Questions

1. **Transport protocol**: WebSocket vs. long-poll vs. SSE+command channel for worker↔relay communication. Decision deferred to #683.
2. **Secret resolution**: How workers resolve secret refs at execution time. Deferred to #686.
3. **Cloud provisioning**: Whether Relay provisions VMs/containers itself or only routes to pre-provisioned workers. Deferred to #684.
4. **Handoff granularity**: Minimum safe checkpoint interval (every turn? every tool call?). Deferred to #687.
5. **Multi-tenancy model**: How tenant isolation works at the Relay layer vs. the `go-code` layer. Addressed in #679 and #686.

## Guardrails

- Do not turn `go-code` into the entire SaaS/product shell. Keep execution and orchestration boundaries explicit.
- Do not make connector capabilities ambient. Every injected tool, memory source, or credential must be visible in the run contract.
- Do not promise live process teleportation. Support checkpointed handoff at safe boundaries first.
- Keep local/private/dirty workspaces first-class; cloud is optional placement, not the only path.
- Prefer deterministic, explainable routing before model-driven placement.
- Preserve current direct local `go-code` usage: TUI, single-shot, daemon mode, run control, replay, and search must keep working without Go Relay.
