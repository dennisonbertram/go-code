# Known Issues

## AllowedTools Lost on ContinueRun (GitHub #524)

**Severity:** HIGH / Security
**Found:** 2026-03-31
**Status:** Resolved / stale doc reconciled 2026-06-26

This note is preserved for history, but the concrete continuation bug described here is no longer present in the current runner implementation.

`ContinueRunWithOptions` now snapshots `state.allowedTools`, inherits that filter by default, and only replaces it when the continuation request provides an explicit `allowed_tools` value. The continuation request type also documents this inheritance contract.

**Current residual security work:** adjacent `allowed_tools` issues may still exist, especially deferred-tool activation and stale conversation history behavior tracked outside this note. Do not use this entry as evidence that all `allowed_tools` security work is complete.

**Verification reference:** `internal/harness/runner.go` snapshots and propagates `allowedTools` during continuation, and `internal/server/http_continuation_test.go` covers continuation policy overrides.
