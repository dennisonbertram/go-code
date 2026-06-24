#!/usr/bin/env bash
# adapters/template.sh — Adapter I/O contract documentation.
#
# This file is a TEMPLATE. It documents the interface every adapter must
# implement. Copy it to adapters/<tool-id>.sh and implement the body.
#
# ---------------------------------------------------------------------------
# CONTRACT: Environment inputs (set by run.sh before invoking the adapter)
# ---------------------------------------------------------------------------
#
#   TASK_DIR      Absolute path to the task directory for this run.
#                 The adapter reads the task prompt from here.
#                 Convention: a file named "prompt.txt" or "task.md" holds
#                 the user-visible prompt. Adapter decides how to read it.
#
#   WORKSPACE     Absolute path to a fresh per-run scratch directory the
#                 tool may use for intermediate files. Created by run.sh
#                 before calling the adapter; cleaned up by run.sh after.
#
#   PROMPT        The task prompt text (already read from TASK_DIR by
#                 run.sh). Adapters MAY use this directly instead of
#                 re-reading TASK_DIR, but must not modify it.
#
#   MODEL         Model name the adapter should use (e.g. "gpt-4.1-mini").
#                 If the tool does not support model selection, ignore it
#                 and document the fixed model in the entrant's tools.json
#                 entry.
#
#   RESULT_JSON   Absolute path where the adapter MUST write one JSON
#                 result record conforming to result.schema.json.
#                 The file must be valid JSON when the adapter exits 0.
#                 run.sh reads this file and appends it to the run report.
#
#   TASK_ID       Task identifier string (basename of TASK_DIR by default;
#                 run.sh may set it from a task manifest). The adapter must
#                 include this value as the "task_id" field in RESULT_JSON.
#
#   TOOL_ID       Entrant ID string (from tools.json). The adapter must
#                 include this value as the "tool_id" field in RESULT_JSON.
#
# ---------------------------------------------------------------------------
# CONTRACT: Exit code
# ---------------------------------------------------------------------------
#
#   0   The adapter ran successfully. RESULT_JSON must exist and be valid.
#       A status=failed run (the tool errored) is still exit 0 — the
#       failure is recorded inside RESULT_JSON, not via the exit code.
#
#   1   The adapter itself failed to run (infrastructure error: binary not
#       found, API unreachable, timeout, RESULT_JSON not written).
#       run.sh will record a synthetic error result for this task.
#
# ---------------------------------------------------------------------------
# CONTRACT: RESULT_JSON content
# ---------------------------------------------------------------------------
#
#   Must be a single JSON object matching result.schema.json.
#   Required fields: run_id, status, steps_taken, total_prompt_tokens,
#   total_completion_tokens, total_cost_usd, cost_status, cache_hit_rate,
#   model, prompt, created_at, updated_at, duration_ms, tool_calls,
#   tool_id, task_id.
#
#   HONESTY RULES (non-negotiable):
#   - "is_resolved" MUST NOT be set by the adapter. It is EXTERNAL — it
#     comes from the pytest oracle merged in by run.sh or post-processing.
#     Writing a made-up value here will corrupt benchmark results.
#   - "drift" and "forensic_events" are EXTERNAL/opt-in. Leave them absent
#     unless populated by an external oracle.
#   - "duration_ms" is DERIVED (updated_at − created_at milliseconds).
#     Compute it from the actual timestamps; do not invent it.
#   - "cost_status" must reflect the harness value, not be invented.
#     If the tool does not report costs, set cost_status="provider_unreported"
#     and total_cost_usd=0.
#
# ---------------------------------------------------------------------------
# TEMPLATE BODY (replace everything below with real implementation)
# ---------------------------------------------------------------------------

set -euo pipefail

: "${TASK_DIR:?TASK_DIR must be set}"
: "${WORKSPACE:?WORKSPACE must be set}"
: "${PROMPT:?PROMPT must be set}"
: "${MODEL:?MODEL must be set}"
: "${RESULT_JSON:?RESULT_JSON must be set}"
: "${TASK_ID:?TASK_ID must be set}"
: "${TOOL_ID:?TOOL_ID must be set}"

echo "[template] ERROR: This is a template. Implement a real adapter." >&2
echo "[template] Copy this file to adapters/<tool-id>.sh and fill in the body." >&2
exit 1
