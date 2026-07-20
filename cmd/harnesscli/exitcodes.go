package main

import "go-agent-harness/internal/harness"

// Exit codes for headless (streaming) harnesscli usage. These constants are
// the single source of truth for the contract documented in
// website/docs/reference/exit-codes.md (epic #823): the literal values are a
// public compatibility surface for shell scripts and CI, aligned with
// kimi-code's headless codes (0 = completed, 3 = blocked, 6 = paused).
const (
	// exitSuccess: the run terminated with run.completed.
	exitSuccess = 0
	// exitClientError: bad flags, missing prompt, connection/HTTP failure,
	// stream transport error — the failure is client-side, not a run outcome.
	exitClientError = 1
	// exitRunFailed: the run terminated with run.failed.
	exitRunFailed = 2
	// exitBlocked: the run cannot proceed without input a headless caller
	// will never provide (run.waiting_for_user, tool.approval_required, or
	// plan.approval_required observed while stdin is non-interactive).
	exitBlocked = 3
	// exitCancelled: the run terminated with run.cancelled; work is
	// interrupted but resumable via "harnesscli continue".
	exitCancelled = 6
	// exitInterrupted: SIGINT/SIGTERM while streaming (128 + SIGINT, the
	// conventional shell code).
	exitInterrupted = 130
)

// exitCodeForTerminalEvent maps a run terminal event to its contracted exit
// code. Unknown, non-terminal, or empty event types map to exitClientError as
// a defensive default, so a scripting caller never mistakes an unrecognized
// outcome for success.
func exitCodeForTerminalEvent(et harness.EventType) int {
	switch et {
	case harness.EventRunCompleted:
		return exitSuccess
	case harness.EventRunFailed:
		return exitRunFailed
	case harness.EventRunCancelled:
		return exitCancelled
	default:
		return exitClientError
	}
}
