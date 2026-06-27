# Terminal Bench debugging learnings - 2026-03-05

## Summary

We built and ran a periodic Terminal Bench smoke suite against the harness. The first benchmark run failed before the harness ever executed because Terminal Bench could not reach the local Docker daemon. After Docker Desktop was restarted, the benchmark infrastructure came up and the suite ran real tasks. At that point the remaining failures were harness robustness problems, not environment setup problems.

## What failed first

The initial run failed during Terminal Bench container startup with a Docker client timeout while fetching the server API version. The benchmark reported `unknown_agent_error`, but that label was misleading. The agent bridge never got a chance to run because the task containers were never created.

## Infrastructure fixes that were required

### Docker

- Symptom: `docker version` hung and Terminal Bench timed out fetching Docker server metadata.
- Fix: restart Docker Desktop.
- Result: Terminal Bench could create task containers and execute the custom bridge.

### Benchmark runner and bridge

The benchmark setup also needed several fixes before it could run the harness correctly:

- `scripts/run-terminal-bench.sh`
  - forced the `uv` fallback to Python `3.12`
  - corrected the Terminal Bench CLI flag to `--output-path`
- `benchmarks/terminal_bench/agent.py`
  - stopped relying on container-side Go builds
  - packages the current checkout as a tarball and extracts it in the task container
  - cross-builds Linux binaries on the host and copies them into the container
  - starts the harness against `/app` so the task workspace is correct
  - removed the invalid custom `agent_intent` and uses `general`
- Benchmark task scripts
  - normalized `/tests` handling
  - switched pytest output to `pytest -rA` so Terminal Bench parses the run reliably
- `go-retry-schedule-fix`
  - verification was reduced from `go test` to a source assertion because the task container does not include Go

## What the benchmark taught us about the harness

Once Docker and the bridge were fixed, the benchmark surfaced three concrete robustness gaps.

### 1. `apply_patch` is not compatible with the model's normal patch format

In the `go-retry-schedule-fix` and `incident-summary-shell` tasks, the model repeatedly attempted to use `apply_patch` with a standard unified patch blob. The tool rejected the call with `path is required`.

Impact:
- the model spent turns retrying an edit path that could never succeed
- simple patch-style tasks degraded into timeouts or no-op runs
- Terminal Bench is likely to underrate the harness until this compatibility gap is fixed

Interpretation:
- the tool contract is too narrow for the model's default editing behavior
- the highest-value fix is to accept unified patch payloads directly, instead of requiring a separate `path` field

### 2. `harnesscli` gives up on longer streaming runs

The `go-retry-schedule-fix` and `incident-summary-shell` tasks also ended with `scan event stream: context deadline exceeded` from `harnesscli`.

Impact:
- runs that are still making progress are converted into benchmark failures
- long-horizon task behavior is under-measured because the client disconnects too early

Interpretation:
- the CLI uses an HTTP timeout that is too short for streamed runs
- streamed event consumption should either have no deadline or a much larger one than request-style calls

### 3. Full-file writes are brittle for structured files

In `staging-deploy-docs`, the harness updated the README correctly but corrupted `deploy/targets.json` by writing escaped newline sequences into the file.

Impact:
- the harness can pass prose edits but fail adjacent structured-file edits
- JSON and other machine-readable formats are at elevated risk when the model falls back to raw write operations

Interpretation:
- this is partly a prompting/tooling issue rather than purely a model issue
- if `apply_patch` becomes reliable, the model is less likely to attempt brittle whole-file rewrites
- a later hardening pass may still be warranted for structured-file mutation tools

## Recommended debug order

1. Fix `apply_patch` compatibility with unified patch payloads.
2. Fix `harnesscli` streamed-run timeout behavior.
3. Re-run Terminal Bench.
4. Only then decide whether structured-file writes need stronger validation, format-aware helpers, or tighter prompting.

## Why this order matters

The first two items are harness defects that distort many tasks at once. The JSON-write issue is real, but it is downstream of the tool-selection problem. If patching becomes reliable and long streams stop timing out, the benchmark signal will get much cleaner before we spend time on narrower write-robustness work.

## Follow-up issues to track

The benchmark findings should be tracked as high-priority issues:

- `apply_patch` compatibility with unified diff payloads
- `harnesscli` timeout behavior for streamed runs
- structured file mutation robustness for JSON and similar formats

## GitHub issue tracking blocker

I attempted to create GitHub issues for the three high-priority follow-ups, but the configured remote repository could not be resolved by GitHub.

Observed behavior:
- `.git/config` points `upstream` at `https://github.com/dennisonbertram/go-agent-harness.git`
- `gh issue create` failed with `Could not resolve to a Repository with the name 'dennisonbertram/go-agent-harness'`
- direct HTTP requests to both the GitHub API and the repo web URL returned `404`

Prepared issue titles:
- `High priority: make apply_patch compatible with unified diff payloads`
- `High priority: remove premature harnesscli timeout for streamed runs`
- `High priority: harden structured file writes for JSON and machine-readable files`

This means the work items are identified and documented, but the repository slug or access configuration must be fixed before they can be created as GitHub issues.

## GitHub issue tracking resolution

The issue-creation failure was caused by `gh` using the `GITHUB_TOKEN` environment variable, which authenticated the CLI as `runner-protocol-team-lead` instead of the locally stored `dennisonbertram` account. That service account could not resolve the repository. Running `gh` with `GITHUB_TOKEN` unset switched resolution back to the correct personal account.

Created issues:
- #12: `High priority: remove premature harnesscli timeout for streamed runs`
- #13: `High priority: make apply_patch compatible with unified diff payloads`
- #14: `High priority: harden structured file writes for JSON and machine-readable files`

## 2026-06-27 real-provider recheck

Accepted recheck artifact directory:
`.tmp/terminal-bench/real-smoke-20260627-002630/2026-06-27__00-26-42`

Campaign facts:
- Provider/model: `openai` / `gpt-5-mini`
- Terminal-Bench version: `0.2.18`
- Dataset hash: `31b29122bfa16205e6a66967fc444f5d46924a8ed9f39167cb27fc1e676d5457`
- Concurrency/attempts: `1` / `1`
- Timeouts: agent `1800s`, test `300s`
- Result: 7/7 passed

Historical finding status:
- `harnesscli` streaming no longer reproduces after teaching the CLI to ignore
  SSE comment/keepalive blocks. The accepted run produced per-task
  `benchmark_result.json` and `harness_telemetry.json` for all seven tasks.
- The old `apply_patch path is required` failure did not reproduce in the
  accepted run. Patch/edit behavior should remain covered by the smoke tasks,
  but this specific historical action item should not be carried forward as
  currently reproducing.
- The structured JSON corruption symptom did not reproduce. `staging-deploy-docs`
  passed in the accepted run, so the old structured-file action item should not
  be kept as a current blocker.

Baseline decision:
- Promote `baseline.json` from the accepted green run. Cost is recorded with
  `cost_status=unpriced_model` because the repo pricing catalog does not yet
  include `gpt-5-mini`.
