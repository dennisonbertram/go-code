---
title: "Development and Testing Workflow"
sidebar_label: "Development & Testing"
sidebar_position: 3
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent } from '@site/src/components/ui';

go-code is developed and tested without any LLM API key for the vast majority of the test surface. This page explains how to bootstrap a contributor development environment, write and run deterministic tests using the **fake provider**, exercise the full regression suite, and understand how CI gates work.

The **fake provider** (`fakeprovider.Provider`) is the cornerstone of this workflow. It is a scripted, in-memory implementation of the `harness.Provider` interface that returns pre-programmed responses — no network, no API key, no Docker required. Every CI check and most contributor workflows use it.

---

## Worktree bootstrap

Contributors work in isolated git worktrees rather than directly on the main checkout. `scripts/init.sh` is the canonical entry point — it creates the worktree, downloads dependencies, builds local binaries, and writes a ready-to-source environment file.

<Callout type="info">
`scripts/bootstrap-worktree.sh` is a thin compatibility wrapper that delegates to `scripts/init.sh`. Always use `scripts/init.sh` directly.
</Callout>

<Steps>
<Step>

### Run init.sh with a task slug

```bash
scripts/init.sh <task-slug>
```

Replace `<task-slug>` with a short identifier for your work, for example `fix-coverage-gate` or `add-retry-tool`. The script:

1. Creates a git worktree at `.codex-worktrees/<task-slug>/go-agent-harness`.
2. Runs `go mod download` to pull dependencies.
3. Builds three binaries into `.tmp/bootstrap/bin/`: `harnessd`, `harnesscli`, and `coveragegate`.
4. Writes a sourceable env file at `.tmp/bootstrap/dev.env`.
5. Optionally starts `harnessd` in a tmux session (when `--start-server` is passed).

</Step>
<Step>

### Source the generated env file

```bash
source "<worktree>/.tmp/bootstrap/dev.env"
cd "<worktree>"
```

The `dev.env` file exports the following variables:

| Variable | Value |
|---|---|
| `HARNESS_WORKSPACE` | Absolute path to the worktree |
| `HARNESS_ROLLOUT_DIR` | `<worktree>/.tmp/rollouts` |
| `HARNESS_SUBAGENT_WORKTREE_ROOT` | The `.codex-worktrees` root |
| `HARNESS_PROMPTS_DIR` | `<worktree>/prompts` |
| `HARNESS_MODEL_CATALOG_PATH` | `<worktree>/catalog/models.json` |
| `HARNESS_BINARY` | `<worktree>/.tmp/bootstrap/bin/harnessd` |
| `HARNESS_CLI_BINARY` | `<worktree>/.tmp/bootstrap/bin/harnesscli` |

</Step>
<Step>

### Run the regression suite

```bash
./scripts/test-regression.sh
```

If everything is green, the worktree is healthy and ready for work.

</Step>
</Steps>

### init.sh flags

```text
scripts/init.sh [options] <task-slug>

  --base-ref <ref>       Base git ref for the new worktree (default: main)
  --branch <name>        Branch name (default: codex/<task-slug>)
  --worktree-root <dir>  Where worktrees are stored (default: .codex-worktrees)
  --session <name>       Start harnessd in tmux with this session name
  --start-server         Start harnessd in tmux after bootstrapping
  --skip-build           Skip the go build step
  --skip-download        Skip go mod download
  --check                Verify prerequisites and exit
  -h, --help             Show help
```

When `--branch` is omitted, the branch name defaults to `codex/<task-slug>`. The `codex` prefix can be changed via the `INIT_BRANCH_PREFIX` environment variable.

**Prerequisites:** `git` and `go` are always required. `tmux` and `lsof` are only needed when `--start-server` is used.

---

## Fake provider for tests

`fakeprovider.Provider` lets you write Go tests that exercise the full harness run pipeline — including streaming, tool calls, retries, and cost accounting — without calling any external service.

### Construction

```go
import "go-agent-harness/internal/fakeprovider"

p := fakeprovider.New(turns []Turn, opts ...Option) *Provider
```

Pass a slice of `Turn` values. Each `Turn` describes one scripted response that the provider returns when its `Complete` method is called, in order.

### Turn fields

| Field | Type | Description |
|---|---|---|
| `Content` | `string` | Returned in `CompletionResult.Content` |
| `ToolCalls` | `[]harness.ToolCall` | Returned in `CompletionResult.ToolCalls` |
| `Deltas` | `[]harness.CompletionDelta` | Emitted via `req.Stream` for streaming tests |
| `Usage` | `*harness.CompletionUsage` | Returned verbatim |
| `Cost` | `*harness.CompletionCost` | Returned verbatim |
| `CostUSD` | `*float64` | Returned verbatim |
| `UsageStatus` | `harness.UsageStatus` | Returned verbatim |
| `CostStatus` | `harness.CostStatus` | Returned verbatim |
| `Error` | `error` | Returned as the `Complete` error for this turn |
| `Delay` | `time.Duration` | Wait before returning; context-aware |
| `InterDeltaDelay` | `time.Duration` | Pause between streaming deltas; context-aware |
| `Hang` | `bool` | Block indefinitely until `Release()` or context cancel |

### Options

| Option | Description |
|---|---|
| `WithDefaultDelay(d)` | Applies `d` to every turn whose `Delay` is zero |
| `WithExhaustedBehavior(b)` | Controls behavior when all scripted turns are consumed |
| `WithName(name)` | Sets the provider name used in error messages |

### Exhausted-turns behavior

When all scripted turns are consumed and `Complete` is called again, the behavior depends on the `ExhaustedBehavior` constant passed to `WithExhaustedBehavior`:

| Constant | Behavior |
|---|---|
| `ExhaustEmpty` (default) | Returns a zero `CompletionResult` with a nil error |
| `ExhaustError` | Returns `GenericError("fakeprovider: all scripted turns exhausted")` |
| `ExhaustRepeatLast` | Repeats the last scripted turn indefinitely |

<Callout type="warning">
The default `ExhaustEmpty` returns nil error when turns run out — it does **not** fail the test automatically. If your test should catch over-calling the provider, use `WithExhaustedBehavior(fakeprovider.ExhaustError)` so the unexpected call surfaces as a real error.
</Callout>

### Assertion helpers

After the code under test runs, use these methods to assert provider behavior:

```go
p.Calls() int                                  // total Complete calls made
p.Invocations() []Invocation                   // full call log (copy)
p.LastRequest() (harness.CompletionRequest, bool) // most recent request
p.Release()                                    // unblock a Hang turn (idempotent)
p.Reset()                                      // rewind for next subtest
```

### Example: basic test with fakeprovider

```go
func TestMyFeature(t *testing.T) {
    prov := fakeprovider.New([]fakeprovider.Turn{
        {
            Content: "all tests pass",
            Usage: &harness.CompletionUsage{
                PromptTokens:     100,
                CompletionTokens: 50,
            },
        },
    })

    // ... wire prov into the component under test ...

    // Assert the provider was called exactly once.
    if prov.Calls() != 1 {
        t.Fatalf("expected 1 call, got %d", prov.Calls())
    }
}
```

### Resetting between subtests

Call `p.Reset()` when reusing a single provider across multiple subtests. `Reset` rewinds the call index, clears the invocation log, and creates a fresh release channel for any `Hang` turns:

```go
func TestMultipleScenarios(t *testing.T) {
    prov := fakeprovider.New([]fakeprovider.Turn{
        {Content: "response A"},
    })

    t.Run("scenario one", func(t *testing.T) {
        // ... test with prov ...
        prov.Reset()
    })

    t.Run("scenario two", func(t *testing.T) {
        // prov is back at turn 0
        // ... test with prov ...
    })
}
```

### Error helpers

`internal/fakeprovider/errors.go` provides helpers for testing retry logic:

| Helper | `IsRetryable`? |
|---|---|
| `RetryableError(err)` — wraps in a 500 `ProviderHTTPError` | Yes |
| `RateLimitError(msg)` — `ProviderHTTPError{StatusCode: 429}` | Yes |
| `GenericError(msg)` — plain `fmt.Errorf` | No |

`IsRetryable(err)` checks for a `ProviderHTTPError` with status code 429, 500, 502, 503, or 504.

---

## Regression suite and coverage gate

`scripts/test-regression.sh` is the full pre-merge gate. It runs four steps in order:

```bash
./scripts/test-regression.sh
```

<Steps>
<Step>

### All tests, no race detector

```bash
go test ./...
```

A broad pass to catch failures fast before spending time on the race run.

</Step>
<Step>

### All tests with race detector

```bash
go test ./... -race
```

Required before any merge. Catches data races that deterministic tests miss.

</Step>
<Step>

### Coverage profile

```bash
go test ./... -coverpkg=./internal/...,./cmd/... -coverprofile=coverage.out
```

Measures statement coverage across all internal and cmd packages. The `-coverpkg` flag ensures the profile covers code exercised via integration paths, not just direct package tests.

</Step>
<Step>

### Coverage gate

```bash
go run ./cmd/coveragegate -coverprofile=coverage.out -min-total=80.0
```

Passes when both conditions hold:

- Total statement coverage is at least **80.0%**.
- **No function has 0.0% coverage** — any completely untested function is a hard failure, regardless of the total percentage.

Exits 0 on pass. Prints: `coveragegate: PASS (total=X.X%, min=Y.Y%, zero-functions=Z)`.

</Step>
</Steps>

<Callout type="warning">
The coverage gate enforces two independent requirements: total coverage must reach 80.0% **and** every function must have at least some coverage. Adding a new exported function and leaving it entirely untested will fail the gate even if total coverage stays above 80%. Write at least one test path through every new function before merging.
</Callout>

### Overridable environment variables

You can adjust the gate thresholds without editing the script:

| Variable | Default | Description |
|---|---|---|
| `MIN_TOTAL_COVERAGE` | `80.0` | Minimum total statement coverage percentage |
| `COVERPROFILE_PATH` | `coverage.out` | Path for the coverage profile |
| `PKG_PATTERNS` | `./...` | Package pattern for all test runs |
| `COVER_PKG_PATTERNS` | `./internal/... ./cmd/...` | Packages measured for coverage |

### Key-free smoke (fastest check)

When you only want to verify that the run pipeline is healthy without running the full suite:

```bash
# In-process — no server, no binary, no API key
go test ./internal/server/... -run TestRunSmoke

# With race detector
go test ./internal/server/... -race -count=1 -run TestRunSmoke
```

Or exercise the actual `harnessd` binary end-to-end:

```bash
bash scripts/run-bench-smoke.sh
```

Both smokes require no API key and are byte-stable: they assert `status=completed`, `steps_taken=1`, `total_prompt_tokens=100`, `total_completion_tokens=50`, `total_cost_usd=0.001`, `cost_status=available`.

---

## CI gates

Two GitHub Actions workflows gate contributions:

<Tabs defaultValue="fast">
<TabsList>
  <TabsTrigger value="fast">Pull requests — test-fast</TabsTrigger>
  <TabsTrigger value="regression">Push to main / nightly — test-regression</TabsTrigger>
</TabsList>
<TabsContent value="fast">

The **`test-fast`** workflow runs on every pull request:

```bash
go test ./internal/... ./cmd/...
```

It also runs `python3 scripts/test_terminal_bench_artifacts.py` to verify benchmark artifact schemas. This gate is intentionally narrow — it skips the race detector and coverage gate to keep PR feedback fast.

**What it catches:** compile errors, panics, logic bugs in internal packages.
**What it does not catch:** data races, coverage regressions, integration issues only visible under `-race`.

</TabsContent>
<TabsContent value="regression">

The **`test-regression`** workflow runs on push to `main`, on a nightly schedule (03:00 UTC), and on manual `workflow_dispatch`:

```bash
./scripts/test-regression.sh
```

This is the full gate: all tests, race detector, coverage profile, and the 80% + no-zero-functions coverage gate.

</TabsContent>
</Tabs>

### Merge flow

The recommended contributor merge path uses `scripts/verify-and-merge.sh`:

```bash
./scripts/verify-and-merge.sh <feature-branch> "./scripts/test-regression.sh" main
```

The script runs the test command on your feature branch, fast-forward merges to `main` (falling back to a merge commit if needed), reruns the test command on `main`, and pushes to origin if a remote is configured.

---

## Next steps

- **Server reference** — `harnessd` environment variables, full HTTP route table, and auth configuration: [harnessd](/docs/server/harnessd).
- **Key-free testing guide** — a walkthrough of `TestRunSmoke` and the shell smoke in detail: [Key-free testing](/docs/getting-started/key-free-testing).
- **Training and benchmarks** — `trainerd` scoring, the Terminal-Bench integration, and the overnight training loop: [Training and Benchmarks](/docs/operations/training-and-benchmarks).
- **Benchmark runbook** — full benchmark smoke, result schema, and Python harness paths: `docs/runbooks/benchmark-smoke.md` in the repository.
