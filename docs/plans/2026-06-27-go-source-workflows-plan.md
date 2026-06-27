# Plan: Go-Authored Custom Workflows

## Intent

- Command intent: let agents create, save, hot-load, run, and monitor custom workflows written in Go.
- User intent: match the practical Claude workflow pattern while keeping go-code local-first and reusable through skills.
- Success definition: workflow bundles compile without restarting `harnessd`, can be created and run through tools, emit feedback to the parent agent/API stream, and can be bundled under skills.

## Implementation Shape

- Workflow bundles live under `.go-harness/workflows/<name>/` or under a skill at `.go-harness/skills/<skill>/workflows/<name>/`.
- Each bundle contains `workflow.json` and Go source using `go-agent-harness/pkg/workflowsdk`.
- The host compiles workflow source into a cache and executes it as a child process using a JSONL protocol.
- The child process calls back into the host for agent runs, nested workflows, phases, logs, feedback, and questions.
- Existing YAML `/v1/workflows` behavior is left unchanged; Go-authored workflows use the existing `/v1/script-workflows` route family.

## Solved Struggle

- Symptom: Go workflows need to be created and run without stopping `harnessd`, but Go plugins are brittle and cannot unload cleanly.
- Cause: Go plugin ABI compatibility and process lifetime do not fit agent-generated code that should be hot-loaded repeatedly.
- Fix: compile workflows as child binaries and bridge to the host over JSONL stdio; this preserves hot loading, cancellation, bounded diagnostics, and host-owned subagent execution.

## Verification

- Added tests for dynamic workflow creation/build/run, skill-bundled discovery, feedback propagation, questions, create/run tools, SSE feedback history, subagent RPC forwarding, process/protocol failure handling, harnessd script workflow wiring, and coveragegate zero-function gaps.
- Verified with focused package tests and `./scripts/test-regression.sh`.
- Final regression evidence: `coveragegate: PASS (total=84.4%, min=80.0%, zero-functions=0)` and `[regression] PASS`.
