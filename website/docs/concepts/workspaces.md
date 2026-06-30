---
title: "Workspaces and Isolation"
sidebar_label: "Workspaces & Isolation"
sidebar_position: 4
---

import { Callout, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent, Badge } from '@site/src/components/ui';

A **workspace** is the isolated execution environment in which one agent run operates. It bundles together a filesystem directory, a reachable `harnessd` HTTP endpoint, and (for some backends) managed git state. Workspaces are set up and torn down by the orchestration layer — the agent loop itself never manages its own workspace.

Choosing a workspace backend lets you control what each run can see and modify: its own branch of a repo, a fresh Docker container, a remote Hetzner VM, or simply a directory on the current host.

<Callout variant="warning">
Workspace choice is separate from the permission model. Even a worktree or container workspace runs with the default `{sandbox: unrestricted, approval: none}` unless you also set a `permissions` block in your run request or profile. Picking an isolated workspace backend does not automatically sandbox the agent's filesystem or approval behavior. See [Tools and Permissions](/docs/concepts/tools-and-permissions) for details.
</Callout>

---

## Selecting a workspace for a run

Pass `workspace_type` in the `POST /v1/runs` request body:

```json
{
  "prompt": "refactor the auth module",
  "workspace_type": "worktree"
}
```

Valid values are `"local"`, `"worktree"`, `"container"`, and `"vm"`. An empty string means no workspace is provisioned — the run executes in the calling process.

If `workspace_type` is absent from the request, the runner falls back to the `isolation_mode` field of the named profile (if one is set). Profile `isolation_mode` accepts `"none"`, `"worktree"`, `"container"`, and `"vm"`. Only `"worktree"`, `"container"`, and `"vm"` actually trigger provisioning — `"none"` and an empty string both mean no provisioning. Note that `"local"` is not a valid profile isolation mode: setting it in a profile is treated as no preference and results in no provisioning.

---

## The four backends

<Tabs>
<TabsList>
  <TabsTrigger value="local">local</TabsTrigger>
  <TabsTrigger value="worktree">worktree</TabsTrigger>
  <TabsTrigger value="container">container</TabsTrigger>
  <TabsTrigger value="vm">vm</TabsTrigger>
</TabsList>

<TabsContent value="local">

<Card>
<CardHeader>
<CardTitle>local <Badge variant="secondary">dev default</Badge></CardTitle>
</CardHeader>
<CardContent>

The `local` backend creates a directory `<baseDir>/<runID>` and points the run at an **already-running** `harnessd` process. It does not start or stop any process — the caller is responsible for having one available.

**Harness URL resolution** (highest priority first):

1. `opts.Env["HARNESS_URL"]`
2. The URL passed to the constructor
3. `http://localhost:8080` (default)

**When to use it:** Local development and single-agent use on the same host where `harnessd` is already running.

**Destroy:** Deletes the run directory with `os.RemoveAll`.

</CardContent>
</Card>

</TabsContent>

<TabsContent value="worktree">

<Card>
<CardHeader>
<CardTitle>worktree <Badge variant="secondary">parallel-safe</Badge></CardTitle>
</CardHeader>
<CardContent>

The `worktree` backend runs `git worktree add` to give each run its own branch and directory. Git operations are serialized per-repo via an internal lock (`sync.Map` of `*sync.Mutex` keyed by absolute repo path), so many concurrent runs against the same repo do not cause checkout conflicts.

- **Branch name:** `workspace-<sanitized-runID>` — characters outside `[A-Za-z0-9._-]` are replaced with `-`.
- **Worktree path:** `<WorktreeRootDir>/<sanitized-runID>`. When `WorktreeRootDir` is not set it defaults to a sibling of the repo: `<repoParent>/<repoName>-subagents`.
- **Base ref:** The `WorktreeBaseRef` option controls which ref the new branch starts from; empty defaults to `HEAD`.

**When to use it:** Parallel work on the same repo — multi-agent fan-out, benchmark suites, or CI pipelines that must not interfere with each other's working tree.

**Destroy:** Runs `git worktree remove --force`, `git branch -D`, and `git worktree prune` in sequence. "Already gone" errors are silently ignored.

**Requires:** A local git repository accessible at `opts.RepoPath` (or `opts.BaseDir` as a fallback for backward compatibility).

</CardContent>
</Card>

</TabsContent>

<TabsContent value="container">

<Card>
<CardHeader>
<CardTitle>container <Badge variant="secondary">requires Docker</Badge></CardTitle>
</CardHeader>
<CardContent>

The `container` backend launches a Docker container running `harnessd`, bind-mounts a host directory to `/workspace` inside, finds a free host TCP port, and polls `ContainerInspect` until the container reaches `running` state (up to 30 seconds, polling every 500 ms).

- **Default image:** `go-agent-harness:latest` — override with `opts.Env["HARNESS_IMAGE"]`.
- **Port:** Container port `8080/tcp` is mapped to a dynamically allocated host port. `HarnessURL()` returns `http://localhost:<hostPort>`.
- **Env vars:** All entries in `opts.Env` are injected as container environment variables — this is how API keys reach the agent inside the container.
- **ConfigTOML:** Written to the host side of the bind mount at `<wsPath>/harness.toml` (visible inside the container at `/workspace/harness.toml`).
- **Container name:** `workspace-<sanitized-runID>`.

**When to use it:** When you want each run completely isolated from the host filesystem, or when you need a reproducible environment defined by a Docker image.

**Destroy:** Stops the container (5-second grace period) then removes it, using a separate 30-second force context even if the caller's context has been cancelled.

**Requires:** Docker daemon accessible on the host.

#### Building the image

```bash
docker build -f build/Dockerfile.harnessd -t go-agent-harness:latest .
```

The Dockerfile uses a two-stage build: Go compiler in the builder stage, a minimal `alpine:3.21` runtime image with `git` and `ca-certificates`. The container exposes port `8080` and includes a health check at `/healthz`.

</CardContent>
</Card>

</TabsContent>

<TabsContent value="vm">

<Card>
<CardHeader>
<CardTitle>vm <Badge variant="secondary">Hetzner Cloud</Badge></CardTitle>
</CardHeader>
<CardContent>

The `vm` backend provisions a Hetzner Cloud VM using cloud-init user data, then polls until `harnessd` is reachable. The VM runs `harnessd` as a systemd service on port `8080`, with the workspace directory at `/workspace`.

**Defaults:**

| Parameter | Default |
|---|---|
| Server type | `cax11` (ARM 2c/4 GB — cheapest current Hetzner SKU) |
| Image | `ubuntu-24.04` |
| Location | `nbg1` (Nuremberg) |
| Poll interval | 3 seconds |
| Provision timeout | 5 minutes |

**Requires:** The `HETZNER_API_KEY` environment variable must be set to a valid Hetzner Cloud API key.

**Destroy:** Calls the Hetzner API to delete the VM.

</CardContent>
</Card>

<Callout variant="warning">
**VM tool routing is incomplete — read this before relying on the `vm` backend.**

File and shell tools (`write`, `edit`, `bash`) currently execute on the **host machine**, not inside the guest VM. This means the agent runs on the VM but its filesystem operations land on the host. This is a known issue tracked in [#564](https://github.com/dennisonbertram/go-code/issues/564).

The runner emits a `prompt.warning` event with code `"vm_workspace_tool_routing"` right after the VM workspace is provisioned (on every successful VM run), surfacing the host-execution caveat to event consumers.

Until issue #564 is resolved, use the `container` backend when you need genuine remote execution with correct tool routing.
</Callout>

</TabsContent>
</Tabs>

---

## Workspace lifecycle and events

When a `workspace_type` is set, the runner goes through a three-phase lifecycle before the agent loop starts:

1. **Provision** — calls `workspace.New(ctx, wsType, opts)`, which internally calls `Provision`. If provisioning fails, the run ends immediately.
2. **System prompt injection** — after provisioning, the system prompt is re-resolved with the workspace path so that any `AGENTS.md` file in the workspace directory is picked up.
3. **Destroy** — on run completion, failure, or cancellation, `ws.Destroy` is called and the workspace is torn down.

Each phase emits a corresponding SSE event on `GET /v1/runs/{id}/events`:

| Event type | String value | Payload fields |
|---|---|---|
| `EventWorkspaceProvisioned` | `workspace.provisioned` | `workspace_type`, `workspace_path` |
| `EventWorkspaceDestroyed` | `workspace.destroyed` | `workspace_type`, `workspace_path`, `error` (only on destroy failure) |
| `EventWorkspaceProvisionFailed` | `workspace.provision_failed` | `workspace_type`, `error` |

These events are only emitted when `workspace_type` is non-empty. A `workspace.provision_failed` event is always followed immediately by `run.failed`.

```json
// Example workspace.provisioned payload
{
  "workspace_type": "worktree",
  "workspace_path": "/home/user/myrepo-subagents/run-abc123"
}
```

---

## Passing configuration and secrets

Every workspace backend accepts two optional configuration surfaces:

| Field | What it is | Security note |
|---|---|---|
| `opts.Env` | Map of environment variables injected into the workspace | **Use this for secrets** (API keys, tokens) |
| `opts.ConfigTOML` | TOML string written to `harness.toml` in the workspace root (mode `0600`) | Never put secrets here — the file persists on disk |

API keys such as `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` are always passed via `opts.Env`, never written to `harness.toml`. The orchestration layer (`symphd`) enforces this pattern automatically.

<Callout variant="warning">
`harness.toml` is written to disk inside the workspace. Secrets placed in `ConfigTOML` will appear on the filesystem and may be captured in logs or git history. Always use `opts.Env` for credentials.
</Callout>

---

## Warm workspace pools

For workloads that provision many short-lived workspaces (such as benchmark suites), the `Pool` type pre-provisions a configurable number of slots in the background and hands them out via lease/return semantics. This eliminates cold-start latency on each run.

The `symphd` orchestrator exposes this as `workspace_type: pool` with a `pool_size` setting (default: `3`) and `pool_workspace_type` specifying the inner backend (default: `"container"`).

Pool workspaces are not registered in the default workspace registry — they require a configured `Pool` instance.

---

## Quick reference: choosing a backend

| Need | Recommended backend |
|---|---|
| Local dev, single agent, `harnessd` already running | `local` |
| Parallel agents on the same repo, no Docker required | `worktree` |
| Full filesystem isolation, reproducible environment | `container` |
| Long-running work on a cloud VM | `vm` (with awareness of the tool routing limitation) |
| High-throughput benchmark or eval suite | `pool` (via `symphd`) |

---

## Next steps

- [Tools and Permissions](/docs/concepts/tools-and-permissions) — configure sandbox behavior and approval policies that apply inside any workspace.
- [Events](/docs/concepts/events) — subscribe to `workspace.provisioned` and `workspace.destroyed` to observe workspace lifecycle in real time.
- [Skills, Profiles, and Subagents](/docs/concepts/skills-profiles-subagents) — set `isolation_mode` in a profile to select a workspace backend per task type without changing caller code.
