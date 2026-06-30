---
title: "Tutorial: Running Agents in Sandboxes"
sidebar_label: "Sandboxes (local/worktree/container/vm)"
sidebar_position: 2
---

import { Callout, Steps, Step, Card, CardHeader, CardTitle, CardContent } from '@site/src/components/ui';

A **workspace** is the isolated execution environment in which an agent run operates. It combines a filesystem directory, a reachable `harnessd` HTTP endpoint, and optional git state. Choosing the right workspace backend determines how much isolation a run gets, what it costs to provision, and what side-effects it can leave behind.

go-code ships four built-in workspace backends: `local`, `worktree`, `container`, and `vm`. This tutorial walks you through each one in order of increasing isolation, showing exactly how to start a run, what lifecycle events to watch for, and where the hard limits are.

---

## Prerequisites

- `harnessd` and `harnesscli` installed (see [Installation](/docs/getting-started/installation)).
- Docker installed and running (for the container section).
- A Hetzner Cloud API key (for the vm section only — skippable).

---

## Part 1: `local` and `worktree` backends

### The `local` backend

The `local` backend is the simplest option. It creates a temporary directory on your machine and points all file and shell tools at that directory. It does **not** start a new `harnessd` process; it expects one to already be running (the harness URL defaults to `http://localhost:8080`).

Use `local` when you want a clean scratch directory per run without any git overhead.

<Steps>
  <Step title="Start harnessd in key-free mode">

Open a terminal and start the daemon using the fake provider so no API key is required. First, create the fake turns file the provider reads:

```bash
cat > /tmp/fake_turns.json <<'EOF'
[{"content":"smoke ok","usage":{"prompt":100,"completion":50},"cost_usd":0.001,"cost_status":"available"}]
EOF
```

Then start harnessd:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake_turns.json \
go run ./cmd/harnessd
```

The server listens on `:8080` by default (`HARNESS_ADDR`).

  </Step>
  <Step title="Submit a run with workspace_type local">

In a second terminal, post a run request and ask for `local` isolation:

```bash
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"list the workspace directory","workspace_type":"local","allow_fallback":true}' \
  | jq -r .run_id)
echo "run id: $RUN_ID"
```

The `"allow_fallback":true` field is required when running with `HARNESS_PROVIDER=fake`: the model-catalog lookup runs before the fake provider and fails if no API key is present; `allow_fallback` lets it fall through to the fake default.

The server responds immediately with HTTP 202 and a `run_id`.

  </Step>
  <Step title="Stream the events and find workspace.provisioned">

```bash
curl -sN "http://localhost:8080/v1/runs/$RUN_ID/events" \
  | grep -E '"type"' \
  | head -20
```

Look for the `workspace.provisioned` event — it carries the provisioned path. Each SSE `data:` line contains a full event envelope:

```json
{
  "id": "run_8f3a2c1d-...:1",
  "run_id": "run_8f3a2c1d-...",
  "type": "workspace.provisioned",
  "timestamp": "2026-01-01T00:00:00Z",
  "payload": {
    "workspace_type": "local",
    "workspace_path": "/tmp/run_8f3a2c1d-..."
  }
}
```

The `workspace_path` is `os.TempDir()/<run_id>`. On Linux this is `/tmp/run_<uuid>`; on macOS it is under `/var/folders/...`.

After the run ends you will see `workspace.destroyed` with the same fields. The directory is deleted by `os.RemoveAll`.

  </Step>
</Steps>

### The `worktree` backend

The `worktree` backend gives each run its own git branch and checkout directory. Under the hood it calls `git worktree add` in a serialized per-repo lock, so many runs can be in flight simultaneously without checkout conflicts.

**Why use `worktree`?** It is ideal when you want the agent to make commits, run tests, or modify files without touching your working tree. When the run finishes, the branch and directory are deleted automatically.

The branch name is derived from the run ID by replacing characters outside `[A-Za-z0-9._-]` with `-`, then prepending `workspace-`. Run IDs have the form `run_<uuid>` (underscores are allowed and retained), so a run ID such as `run_8f3a2c1d-...` becomes branch `workspace-run_8f3a2c1d-...`.

<Steps>
  <Step title="Start harnessd with the repo path configured">

Worktree runs branch from `HEAD` of the repo at `HARNESS_WORKSPACE` (defaults to the harnessd working directory, which must be a git repo). The base ref is not configurable per-run via env on this path — it is always `HEAD`. (The `HARNESS_SUBAGENT_BASE_REF` variable affects only the separate subagents feature, not top-level `workspace_type=worktree` runs.)

If you have not already created the fake turns file, do so first:

```bash
cat > /tmp/fake_turns.json <<'EOF'
[{"content":"smoke ok","usage":{"prompt":100,"completion":50},"cost_usd":0.001,"cost_status":"available"}]
EOF
```

Then start harnessd from inside your git repo:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake_turns.json \
go run ./cmd/harnessd
```

  </Step>
  <Step title="Submit a run with workspace_type worktree">

```bash
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "create a file called hello.txt with the text Hello",
    "workspace_type": "worktree",
    "allow_fallback": true
  }' \
  | jq -r .run_id)
echo "run id: $RUN_ID"
```

  </Step>
  <Step title="Observe the provisioned branch and path">

Stream events and look for `workspace.provisioned`:

```bash
curl -sN "http://localhost:8080/v1/runs/$RUN_ID/events" \
  | grep -A4 'workspace.provisioned'
```

Expected output (each `data:` line is a full event envelope):

```json
{
  "id": "run_8f3a2c1d-...:1",
  "run_id": "run_8f3a2c1d-...",
  "type": "workspace.provisioned",
  "timestamp": "2026-01-01T00:00:00Z",
  "payload": {
    "workspace_type": "worktree",
    "workspace_path": "/path/to/repo-name-subagents/run_8f3a2c1d-..."
  }
}
```

The sibling directory `<repo-name>-subagents` (a sibling of the repo root, not inside it) is created automatically if it does not exist. When the run ends, the worktree and branch are removed and `git worktree prune` is called.

  </Step>
</Steps>

<Callout variant="info" title="Per-repo locking">
  The worktree backend uses a per-repo mutex so that concurrent <code>git worktree add</code> calls never race. You can start dozens of runs against the same repo safely.
</Callout>

---

## Part 2: `container` backend (Docker)

The `container` backend creates a Docker container, bind-mounts a host directory to `/workspace` inside it, and maps an internal port `8080/tcp` to a dynamically allocated host port. Files written by the run appear inside the container because the workspace dir is bind-mounted there. However, in the single-node `harnessd` flow described here, the agent's file and shell tools (`write`, `edit`, `bash`) execute in the host `harnessd` process rooted at the host bind-mount path — not inside the container's process or namespace. Genuine in-guest tool routing requires the orchestrator (`symphd`) to dispatch sub-runs to the container's `harnessd` endpoint.

<Callout variant="warning" title="Docker daemon required">
  The container backend requires a running Docker daemon accessible to the process that starts <code>harnessd</code>. If Docker is not installed or the socket is not accessible, provisioning will fail with <code>workspace.provision_failed</code>.
</Callout>

### Build the harnessd Docker image

The repository ships a multi-stage Dockerfile at `build/Dockerfile.harnessd`. Build the image once:

```bash
docker build -f build/Dockerfile.harnessd -t go-agent-harness:latest .
```

What this produces:

- **Stage 1 (builder):** Compiles `harnessd` with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` in a `golang:1.25-alpine` image.
- **Stage 2 (runtime):** Copies only the binary into `alpine:3.21` plus `ca-certificates` and `git`. The result is a small image with `harnessd` at `/bin/harnessd`.
- The image exposes port `8080` and includes a healthcheck that polls `http://localhost:8080/healthz` every 5 seconds.

Default image name is `go-agent-harness:latest`. You can override this per-run with the `HARNESS_IMAGE` environment variable passed via `opts.Env`.

### Secrets via env, never via config file

<Callout variant="warning" title="Never put API keys in harness.toml">
  The <code>harness.toml</code> file written into the workspace is a TOML config file — not a secrets store. API keys and other credentials must be passed via environment variables (<code>opts.Env</code> or the container env), never in <code>ConfigTOML</code>. The file is written at mode <code>0600</code>, but env vars are the right channel for secrets.
</Callout>

All keys in `opts.Env` are injected directly as container environment variables. The orchestrator layer (`symphd`) propagates `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `HARNESS_MODEL` from the parent process this way — they never touch disk.

### Run a container-isolated agent

<Steps>
  <Step title="Start harnessd on the host (coordinates the container)">

If you have not already created the fake turns file, do so first:

```bash
cat > /tmp/fake_turns.json <<'EOF'
[{"content":"smoke ok","usage":{"prompt":100,"completion":50},"cost_usd":0.001,"cost_status":"available"}]
EOF
```

Then start harnessd:

```bash
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake_turns.json \
go run ./cmd/harnessd
```

  </Step>
  <Step title="Submit a run with workspace_type container">

```bash
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "write hello to /workspace/greeting.txt",
    "workspace_type": "container",
    "allow_fallback": true
  }' \
  | jq -r .run_id)
echo "run id: $RUN_ID"
```

  </Step>
  <Step title="Watch for workspace.provisioned with the dynamic host port">

```bash
curl -sN "http://localhost:8080/v1/runs/$RUN_ID/events" \
  | grep -A4 'workspace.provisioned'
```

The `workspace_path` field shows the host-side directory that is bind-mounted into the container at `/workspace`. Inside the container the path is always `/workspace` (set via `HARNESS_WORKSPACE=/workspace`).

The container name follows the pattern `workspace-<sanitized-run-id>`. The host port is allocated dynamically from a free port on `0.0.0.0`.

  </Step>
  <Step title="Confirm cleanup">

After the run completes, the `workspace.destroyed` event confirms the container has been stopped and removed (with a 30-second force-stop timeout, 5-second graceful stop window).

  </Step>
</Steps>

---

## Part 3: `vm` backend (Hetzner Cloud)

The `vm` backend creates a real cloud VM on Hetzner Cloud, bootstraps it via cloud-init, waits for `harnessd` to be reachable at `http://<public-ip>:8080`, and then runs the agent against that endpoint.

**When to use it:** Long-running tasks that need a fully isolated network environment, or workloads that must not share any host resources with other runs.

**Defaults:**

| Setting | Value |
|---|---|
| Provider | Hetzner Cloud |
| Server type | `cax11` (ARM 2c/4G, cheapest current SKU) |
| Image | `ubuntu-24.04` |
| Location | `nbg1` (Nuremberg) |
| Workspace path inside VM | `/workspace` (hardcoded) |
| Provision timeout | 5 minutes |

The bootstrap script installs `curl` and `git`, writes a systemd service for `harnessd`, and starts it. If `HARNESS_DOWNLOAD_URL` is set, the script downloads the `harnessd` binary; otherwise it expects the binary to already be at `/usr/local/bin/harnessd` in the image.

### Provisioning a VM run

<Callout variant="warning" title="Real cost and real API key required">
  VM runs create real Hetzner Cloud resources that you are billed for. Make sure you destroy the VM after each run (the harness calls <code>VMProvider.Delete</code> when the run ends and <code>workspace.destroyed</code> is emitted). A forgotten VM will continue to incur charges. A valid <code>HETZNER_API_KEY</code> is required at provision time; without it the run still reaches the <code>vm</code> backend but provisioning fails at the Hetzner API call (the backend is always registered).
</Callout>

```bash
# Set your Hetzner Cloud API key
export HETZNER_API_KEY=your_key_here

# Create the fake turns file if not already present
cat > /tmp/fake_turns.json <<'EOF'
[{"content":"smoke ok","usage":{"prompt":100,"completion":50},"cost_usd":0.001,"cost_status":"available"}]
EOF

# Start harnessd with the vm workspace type available
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS=/tmp/fake_turns.json \
go run ./cmd/harnessd
```

Then submit a run:

```bash
RUN_ID=$(curl -s -X POST http://localhost:8080/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "create /workspace/hello.txt",
    "workspace_type": "vm",
    "allow_fallback": true
  }' \
  | jq -r .run_id)
echo "run id: $RUN_ID"
```

Poll for provisioning (can take up to 5 minutes while the VM boots and cloud-init runs):

```bash
curl -sN "http://localhost:8080/v1/runs/$RUN_ID/events" \
  | grep -E 'workspace\.(provisioned|provision_failed)'
```

On success the SSE `data:` line will contain a full event envelope like:

```json
{
  "id": "run_...:1",
  "run_id": "run_...",
  "type": "workspace.provisioned",
  "timestamp": "2026-01-01T00:00:00Z",
  "payload": {
    "workspace_type": "vm",
    "workspace_path": "/workspace"
  }
}
```

The harness URL for the VM run is `http://<public-ip>:8080`.

### Known limitation: tool routing runs on the host, not the guest

<Callout variant="warning" title="File and shell tools execute on the HOST, not inside the VM">
  This is a significant correctness issue: <code>write</code>, <code>edit</code>, and <code>bash</code> tool calls execute on the machine running <code>harnessd</code> (the host), not inside the Hetzner VM. The harness emits a <code>prompt.warning</code> event with code <code>"vm_workspace_tool_routing"</code> at the start of every VM-type run to flag this. Tracked in issue #564.

  In practice this means a VM run today does not provide true guest-level isolation for file/shell operations. Use the <code>container</code> backend if you need the agent's writes to land inside an isolated environment.
</Callout>

---

## Part 4: Verifying isolation

### Reading workspace lifecycle events

Every run that uses a non-empty `workspace_type` emits three possible workspace events:

| Event | When emitted | Key payload fields |
|---|---|---|
| `workspace.provisioned` | Workspace ready; run begins | `workspace_type`, `workspace_path` |
| `workspace.destroyed` | Workspace torn down after run | `workspace_type`, `workspace_path`, `error` (only on destroy failure) |
| `workspace.provision_failed` | Provisioning failed; run fails immediately | `workspace_type`, `error` |

These events are emitted on the run's SSE stream at `GET /v1/runs/{id}/events`. To watch only the workspace lifecycle:

```bash
curl -sN "http://localhost:8080/v1/runs/$RUN_ID/events" \
  | grep -E '"workspace\.'
```

### When to use which backend

<Card>
  <CardHeader>
    <CardTitle>Choosing a workspace backend</CardTitle>
  </CardHeader>
  <CardContent>

| Backend | Isolation | Startup cost | Best for |
|---|---|---|---|
| `local` | Directory only | Near-zero | Dev iteration, single-machine runs |
| `worktree` | Git branch + directory | ~100 ms | Parallel runs on the same repo, multi-agent pipelines |
| `container` | Docker container (OS-level) | ~1–5 s | Production isolation, untrusted code, reproducible environments |
| `vm` | Hetzner Cloud VM | ~1–5 min | Long-running workloads, network-isolated tasks (see tool-routing caveat above) |

  </CardContent>
</Card>

### Confirming the workspace path

For `local` and `worktree` runs you can confirm the workspace path by reading it from the `workspace.provisioned` event payload and verifying the directory exists on your machine. For `container` runs the path is the host-side bind mount; the path inside the container is always `/workspace`.

---

## Next steps

- Go deeper on how workspace backends, the permission model, and per-run/profile selection interact in [Workspaces and Isolation](/docs/concepts/workspaces).
- Learn about **profiles** to bundle a workspace isolation mode with model, step limits, and tool allowlists: [Skills, Profiles, and Subagents](/docs/concepts/skills-profiles-subagents).
- See how the **orchestrator** (`symphd`) provisions a fresh workspace per GitHub issue and dispatches runs over HTTP in [Symphony: Issue-Driven Orchestration](/docs/workflows/symphony).
- Explore the full workspace event reference in the [Event catalog](/docs/reference/events-catalog).
