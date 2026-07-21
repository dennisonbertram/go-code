Fan one prompt template out over many items into concurrent subagents with a single call, and receive one aggregated report covering every member.

Usage notes:

- `prompt_template` must contain the `{{item}}` placeholder; each entry of `items` (1-128) is substituted in to produce one member prompt. Expanded prompts must be distinct.
- **Sole-call rule: when you call `agent_swarm`, it must be the only tool call in that response. Any other tool call in the same response is rejected and must be re-issued in a later turn.**
- Members run concurrently: 5 start immediately, then the in-flight allowance grows by 1 every 700ms up to `HARNESS_SWARM_MAX_CONCURRENCY` (default 128, hard-capped at 128).
- `resume_agent_ids` reuses existing subagents instead of creating new ones: entry i is paired with `items[i]` and receives that item's expanded prompt as a follow-up message. Targets must be running or waiting for user input; resumed members are scheduled before new items.
- Members never receive `agent_swarm` in their own tool set — nested swarms are not allowed.
- Member failures do not abort the cohort: every member reports its own status in the aggregated report.
- The result is a single JSON report: per-member id, item, status, output/error (resumed members marked), plus total/completed/failed/cancelled counts. Order is deterministic: new item members first (item order), then resumed members.
- Cancelling the parent run cancels every swarm member.
