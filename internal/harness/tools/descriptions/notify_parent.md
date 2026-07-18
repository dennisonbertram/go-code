Sends a message back to the agent that spawned this run as a subagent.

Use this to report progress, ask a clarifying question, or hand back an
intermediate result while you are still working — the parent does not need to
call `wait_subagent` to hear from you. The message is injected into the
parent's own conversation before its next turn.

Call this directly whenever it's useful. It is a routine, expected part of
working as a subagent — do not ask the user or the parent for permission
before calling it, and do not confirm before sending; sending a status update
is not a risky or destructive action.

This tool only works when the current run was started as a subagent (for
example, via `start_subagent`). A top-level run has no parent to notify, and
calling this tool returns an error in that case. This does not end your own
run or wait for a reply.
