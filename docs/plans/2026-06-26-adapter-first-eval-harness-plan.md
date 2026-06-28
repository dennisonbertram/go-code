# Plan: Adapter-First Eval Harness

## Context

- Problem: The Terminal-Bench path existed as a smoke adapter, but it did not produce a schema-validated benchmark JSONL with externally merged oracle results, reliable preflight, or campaign provenance.
- User impact: Eval runs were hard to trust because task pass/fail, harness telemetry, logs, and baseline comparisons were split across artifacts without a single honest output contract.
- Constraints:
  - Keep Terminal-Bench as the first-class operator interface for this slice.
  - Do not add public `go-code eval` docs until the adapter path has a proven real-provider run.
  - Keep `is_resolved` external to the harness adapter.

## Scope

- In scope:
  - Harden `scripts/run-terminal-bench.sh` with preflight checks, explicit Terminal-Bench flags, fake-provider mode, one-time Linux binary builds, and postprocessing.
  - Make the Terminal-Bench adapter write per-trial `benchmark_result.json` records from `/v1/runs/{id}` and `/summary`.
  - Add a standard-library postprocessor that merges Terminal-Bench oracle output into schema-validated JSONL, classifies failures, writes reports, and records provenance.
  - Add cheap unit smoke tests for merge, validation, baseline comparison, failure classification, and fake-provider preflight.
- Out of scope:
  - Adding a native `go-code eval` command.
  - Declaring a new accepted baseline without a green real-provider Terminal-Bench campaign.
  - Expanding the task suite beyond the current smoke tier in this slice.

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: none beyond benchmark/operator docs; README should not claim a native eval product.
- Spec docs updated before code: this plan plus the user-provided implementation plan in the task prompt.
- Implementation notes to add after code: engineering and system logs.

## Test Plan (TDD)

- New failing tests added first:
  - `scripts/test_terminal_bench_artifacts.py`
    - schema-valid oracle merge to JSONL
    - failure classification
    - baseline comparison metrics
    - fake-provider `--preflight-only` without `OPENAI_API_KEY`
- Existing tests to update:
  - GitHub fast workflow now runs the Python artifact smoke test.
- Regression tests required:
  - `python3 scripts/test_terminal_bench_artifacts.py`
  - `python3 -m py_compile scripts/terminal_bench_artifacts.py scripts/test_terminal_bench_artifacts.py benchmarks/terminal_bench/agent.py`
  - `bash -n scripts/run-terminal-bench.sh scripts/build-bench-images.sh`
  - `go test ./internal/... ./cmd/...`
  - Full race/coverage regression remains required before merge.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] Add characterization coverage before structural refactors.
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Refactor while tests remain green.
- [x] Update docs, status ledgers, and indexes.
- [x] Update engineering/system/observational logs as needed.
- [x] Run PR fast suite.
- [x] Run full regression suite to green.
- [x] Run a real-provider smoke campaign and preserve artifacts.
- [x] Accept a real baseline from a green real-provider campaign.
- [ ] Merge branch back to `main` after tests pass.

Full regression status on 2026-06-27: `scripts/test-regression.sh` passed in
tmux with `coveragegate: PASS (total=84.6%, min=80.0%, zero-functions=0)`.

Real-provider smoke status on 2026-06-27: the accepted campaign at
`.tmp/terminal-bench/real-smoke-20260627-002630/2026-06-27__00-26-42`
ran with `provider=openai`, `model=gpt-5-mini`, Terminal-Bench `0.2.18`,
dataset hash `31b29122bfa16205e6a66967fc444f5d46924a8ed9f39167cb27fc1e676d5457`,
concurrency `1`, attempts `1`, and timeouts `1800/300`. It passed 7/7,
preserved raw Terminal-Bench results, merged JSONL, run provenance, per-task
benchmark/telemetry artifacts, harness logs, task logs, summary, and report.
`benchmarks/terminal_bench/baseline.json` was promoted from this run with an
explicit `cost_status=unpriced_model` caveat because the current pricing catalog
does not include `gpt-5-mini`.

## Risks and Mitigations

- Risk: Terminal-Bench CLI flags can drift.
- Mitigation: Preflight resolves the runnable `tb`/`uv tool run` command and tests the command before the expensive run.
- Risk: Adapter-generated pass/fail could corrupt benchmark honesty.
- Mitigation: The adapter writes only harness facts; postprocessing is the only place `is_resolved` and parser results are merged from Terminal-Bench.
- Risk: Baseline numbers look authoritative before they are proven.
- Mitigation: `baseline.json` remains non-authoritative until a green campaign writes full run provenance.
