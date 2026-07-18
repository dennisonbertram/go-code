Sends a follow-up message to a running subagent, injecting it into the
subagent's own conversation before its next turn.

Use this to redirect, correct, or add information for a subagent you already
started with `start_subagent` — for example, to reprioritize its task, hand it
new information, or tell it to wrap up early. The subagent must currently be
running or waiting for input; sending a message to a subagent that has already
finished returns an error.

This does not wait for a reply. Use `get_subagent` or `wait_subagent` to check
the subagent's status and output afterward.
