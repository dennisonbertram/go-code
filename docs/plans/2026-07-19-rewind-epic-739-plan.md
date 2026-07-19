# Plan: Session rewind — file-snapshot undo (Epic #739)

## Context

- Problem: Agent edits cannot be safely undone without a recent Git commit.
- User impact: Users can select a prior agent edit and restore real captured file pre-images while truncating that session's stored conversation.
- Constraints: Capture must reuse the existing tool mutation classification, be non-fatal, be disk-bounded, and never involve model reconstruction. `internal/checkpoints` is out of scope.

## Scope

- In scope: SQLite snapshot storage, pre-tool capture, safe restore/truncation, tenant-scoped HTTP routes, a confirmed TUI picker, and retention/pruning.
- Out of scope: Human approval checkpoints, Git-based restore, model-generated file contents, and rewinding arbitrary shell mutations.

## Documentation Contract

- Feature status: in implementation
- Public docs affected: CLAUDE.md route/tool surface and operator logs/runbook notes.
- Spec docs to update before code: this plan and long-term thinking log.
- Implementation notes to add after code: engineering and system logs, docs indexes as required.

## Test Plan (TDD)

- New failing tests to add first: store schema/CRUD, runner capture, restore conflict/force/truncation, HTTP tenancy/routes, TUI picker/confirmation, and cap/retention behavior.
- Existing tests to update: conversation, server routes, and TUI command snapshots as required.
- Regression tests required: an external modification refusal test and delete/age-prune cascade tests.

## Implementation Checklist

- [x] Slice 1 — SQLite schema and rewind snapshot store (#743).
- [x] Slice 2 — pre-edit capture in the step engine (#747).
- [x] Slice 3 — filesystem restore and conversation truncation (#752).
- [x] Slice 4 — tenant-scoped HTTP list/restore routes (#756).
- [x] Slice 5 — confirmed `/rewind` TUI picker (#761).
- [x] Slice 6 — snapshot caps and retention/pruning (#770).
- [x] Update docs and logs.
- [x] Run gofmt, vet, and regression gate.
- [x] Push branch and open, but do not merge, PR.

## Risks and Mitigations

- Risk: snapshots consume excessive disk. Mitigation: per-file and per-conversation caps, skipped-file records, and cascade/age pruning.
- Risk: rewind destroys external work. Mitigation: current-content hash checks, force-only override, and explicit TUI confirmation.
- Risk: snapshot infrastructure disrupts a run. Mitigation: capture failures emit warnings and do not prevent tool execution.
