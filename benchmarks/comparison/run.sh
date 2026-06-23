#!/usr/bin/env bash
# benchmarks/comparison/run.sh — Env-driven orchestrator for the comparison harness.
#
# Runs each ACTIVE entrant in tools.json against each task in TASK_SET_DIR,
# captures one result record per (tool, task) pair, and writes a run report.
#
# HONESTY NOTICE: This harness records what the tools actually produce — it
# does NOT compute head-to-head comparisons or accuracy numbers. is_resolved
# (task pass/fail) is an EXTERNAL field from the pytest oracle; it is not
# available here and is left absent in the raw records. If you want pass/fail
# numbers, merge the oracle results into the raw JSONL after the run.
# No comparison numbers are asserted by this script.
#
# ---------------------------------------------------------------------------
# Usage
# ---------------------------------------------------------------------------
#
#   TASK_SET_DIR=/path/to/tasks MODEL=gpt-4.1-mini ./run.sh
#
#   Or with fake provider (key-free smoke):
#
#   TASK_SET_DIR=/path/to/tasks \
#   MODEL=fake-model \
#   HARNESS_PROVIDER=fake \
#   HARNESS_FAKE_TURNS=/path/to/turns.json \
#   ./run.sh
#
# ---------------------------------------------------------------------------
# Required environment
# ---------------------------------------------------------------------------
#
#   TASK_SET_DIR    Absolute path to a directory of task directories.
#                   Each subdirectory is one task; its name is the TASK_ID.
#                   Each task directory must contain a "prompt.txt" or
#                   "task.md" file with the agent prompt.
#
#   MODEL           Model name passed to every adapter (e.g. "gpt-4.1-mini").
#
# ---------------------------------------------------------------------------
# Optional environment
# ---------------------------------------------------------------------------
#
#   ENTRANT_IDS     Space-separated list of entrant IDs to run (default: all
#                   active entrants in tools.json).
#                   Example: ENTRANT_IDS="go-code"
#
#   OUTPUT_DIR      Directory for results and report (default:
#                   .tmp/comparison/<timestamp>).
#
#   HARNESS_PROVIDER  Pass through to adapters (e.g. "fake" for key-free mode).
#   HARNESS_FAKE_TURNS  Pass through to go-code adapter for fake provider.
#   HARNESS_BINARY  Path to harnessd binary (default: harnessd on PATH).
#   HARNESS_ADDR    Harnessd listen address (default: :8081).
#
#   POLL_TIMEOUT_S  Per-run poll timeout in seconds (default: 300).
#
# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------
#
#   $OUTPUT_DIR/results.jsonl   — one JSON record per (tool, task) pair.
#   $OUTPUT_DIR/report.txt      — human-readable summary (counts only; no
#                                  head-to-head numbers or accuracy claims).
#   $OUTPUT_DIR/run-env.json    — captured run configuration for provenance.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ---------------------------------------------------------------------------
# Validate required env
# ---------------------------------------------------------------------------
: "${TASK_SET_DIR:?TASK_SET_DIR must point to a directory of task subdirectories}"
: "${MODEL:?MODEL must be set (e.g. gpt-4.1-mini or fake-model)}"

if [[ ! -d "${TASK_SET_DIR}" ]]; then
  echo "[comparison] ERROR: TASK_SET_DIR does not exist: ${TASK_SET_DIR}" >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
TIMESTAMP="$(date +%Y%m%dT%H%M%S)"
OUTPUT_DIR="${OUTPUT_DIR:-.tmp/comparison/${TIMESTAMP}}"
TOOLS_JSON="${SCRIPT_DIR}/tools.json"
POLL_TIMEOUT_S="${POLL_TIMEOUT_S:-300}"

mkdir -p "${OUTPUT_DIR}"

RESULTS_JSONL="${OUTPUT_DIR}/results.jsonl"
REPORT_TXT="${OUTPUT_DIR}/report.txt"
RUN_ENV_JSON="${OUTPUT_DIR}/run-env.json"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
info()  { printf '[comparison] %s\n' "$*"; }
warn()  { printf '[comparison] WARN: %s\n' "$*" >&2; }

# ---------------------------------------------------------------------------
# Resolve active entrants
# ---------------------------------------------------------------------------
if ! command -v python3 &>/dev/null; then
  echo "[comparison] ERROR: python3 is required to parse tools.json" >&2
  exit 1
fi

ALL_ENTRANT_IDS="$(python3 - "${TOOLS_JSON}" <<'PYEOF'
import json, sys
data = json.load(open(sys.argv[1]))
for e in data.get("entrants", []):
    print(e["id"])
PYEOF
)"

if [[ -z "${ALL_ENTRANT_IDS}" ]]; then
  echo "[comparison] ERROR: no active entrants found in ${TOOLS_JSON}" >&2
  exit 1
fi

# Filter to ENTRANT_IDS if set
if [[ -n "${ENTRANT_IDS:-}" ]]; then
  ACTIVE_ENTRANT_IDS=""
  for id in ${ENTRANT_IDS}; do
    if echo "${ALL_ENTRANT_IDS}" | grep -qx "${id}"; then
      ACTIVE_ENTRANT_IDS="${ACTIVE_ENTRANT_IDS} ${id}"
    else
      warn "entrant '${id}' not found in active entrants; skipping"
    fi
  done
  ACTIVE_ENTRANT_IDS="${ACTIVE_ENTRANT_IDS# }"
else
  ACTIVE_ENTRANT_IDS="${ALL_ENTRANT_IDS}"
fi

if [[ -z "${ACTIVE_ENTRANT_IDS}" ]]; then
  echo "[comparison] ERROR: no entrants to run" >&2
  exit 1
fi

info "active entrants: ${ACTIVE_ENTRANT_IDS}"

# ---------------------------------------------------------------------------
# Resolve tasks
# ---------------------------------------------------------------------------
TASK_IDS=()
for task_dir in "${TASK_SET_DIR}"/*/; do
  if [[ -d "${task_dir}" ]]; then
    TASK_IDS+=("$(basename "${task_dir%/}")")
  fi
done

if [[ ${#TASK_IDS[@]} -eq 0 ]]; then
  echo "[comparison] ERROR: no task subdirectories found in TASK_SET_DIR: ${TASK_SET_DIR}" >&2
  exit 1
fi

info "tasks (${#TASK_IDS[@]}): ${TASK_IDS[*]}"

# ---------------------------------------------------------------------------
# Write run-env provenance record
# ---------------------------------------------------------------------------
python3 - "${RUN_ENV_JSON}" <<PYEOF
import json, sys, os, subprocess, datetime

sha = ""
try:
    sha = subprocess.check_output(
        ["git", "-C", "${SCRIPT_DIR}", "rev-parse", "--short", "HEAD"],
        stderr=subprocess.DEVNULL, text=True
    ).strip()
except Exception:
    pass

env = {
    "timestamp":      "${TIMESTAMP}",
    "model":          "${MODEL}",
    "task_set_dir":   "${TASK_SET_DIR}",
    "active_entrants": "${ACTIVE_ENTRANT_IDS}".split(),
    "task_ids":       "${TASK_IDS[*]}".split(),
    "harness_provider": os.environ.get("HARNESS_PROVIDER", ""),
    "git_sha":        sha,
    "honesty_notice": (
        "is_resolved (task pass/fail) is EXTERNAL — merge oracle results after the run. "
        "No head-to-head accuracy claims are made by this script."
    ),
}
with open(sys.argv[1], "w") as f:
    json.dump(env, f, indent=2)
    f.write("\n")
PYEOF
info "run-env written: ${RUN_ENV_JSON}"

# ---------------------------------------------------------------------------
# Run loop: for each entrant × task
# ---------------------------------------------------------------------------
PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0
RUN_COUNT=0

for ENTRANT_ID in ${ACTIVE_ENTRANT_IDS}; do
  # Resolve adapter path from tools.json
  ADAPTER_REL="$(python3 - "${TOOLS_JSON}" "${ENTRANT_ID}" <<'PYEOF'
import json, sys
data = json.load(open(sys.argv[1]))
for e in data.get("entrants", []):
    if e["id"] == sys.argv[2]:
        print(e["adapter"])
        sys.exit(0)
print("")
PYEOF
  )"

  if [[ -z "${ADAPTER_REL}" ]]; then
    warn "no adapter found for entrant '${ENTRANT_ID}'; skipping"
    continue
  fi

  ADAPTER="${SCRIPT_DIR}/${ADAPTER_REL}"
  if [[ ! -x "${ADAPTER}" ]]; then
    warn "adapter not executable: ${ADAPTER}; skipping entrant '${ENTRANT_ID}'"
    SKIP_COUNT=$(( SKIP_COUNT + ${#TASK_IDS[@]} ))
    continue
  fi

  for TASK_ID in "${TASK_IDS[@]}"; do
    TASK_DIR="${TASK_SET_DIR}/${TASK_ID}"
    RUN_COUNT=$(( RUN_COUNT + 1 ))

    # Read prompt from task directory
    PROMPT=""
    if [[ -f "${TASK_DIR}/prompt.txt" ]]; then
      PROMPT="$(cat "${TASK_DIR}/prompt.txt")"
    elif [[ -f "${TASK_DIR}/task.md" ]]; then
      PROMPT="$(cat "${TASK_DIR}/task.md")"
    else
      warn "no prompt.txt or task.md in ${TASK_DIR}; skipping"
      SKIP_COUNT=$(( SKIP_COUNT + 1 ))
      continue
    fi

    # Per-run scratch workspace
    WORKSPACE="${OUTPUT_DIR}/workspace/${ENTRANT_ID}/${TASK_ID}"
    mkdir -p "${WORKSPACE}"

    # Per-run result file
    RESULT_JSON="${OUTPUT_DIR}/workspace/${ENTRANT_ID}/${TASK_ID}/result.json"

    info "running: entrant=${ENTRANT_ID} task=${TASK_ID}"

    # Run adapter; record infra failures as synthetic error records
    ADAPTER_EXIT=0
    TASK_DIR="${TASK_DIR}" \
    WORKSPACE="${WORKSPACE}" \
    PROMPT="${PROMPT}" \
    MODEL="${MODEL}" \
    RESULT_JSON="${RESULT_JSON}" \
    TASK_ID="${TASK_ID}" \
    TOOL_ID="${ENTRANT_ID}" \
    POLL_TIMEOUT_S="${POLL_TIMEOUT_S}" \
    HARNESS_PROVIDER="${HARNESS_PROVIDER:-}" \
    HARNESS_FAKE_TURNS="${HARNESS_FAKE_TURNS:-}" \
    HARNESS_BINARY="${HARNESS_BINARY:-harnessd}" \
    HARNESS_ADDR="${HARNESS_ADDR:-:8081}" \
      bash "${ADAPTER}" || ADAPTER_EXIT=$?

    if [[ ${ADAPTER_EXIT} -ne 0 ]] || [[ ! -f "${RESULT_JSON}" ]]; then
      warn "adapter failed (exit=${ADAPTER_EXIT}) for ${ENTRANT_ID}/${TASK_ID}; writing synthetic error record"
      python3 - "${RESULT_JSON}" <<PYEOF
import json, sys, datetime
record = {
    "tool_id":                  "${ENTRANT_ID}",
    "task_id":                  "${TASK_ID}",
    "run_id":                   "adapter-infra-error",
    "status":                   "failed",
    "steps_taken":              0,
    "total_prompt_tokens":      0,
    "total_completion_tokens":  0,
    "total_cost_usd":           0.0,
    "cost_status":              "provider_unreported",
    "cache_hit_rate":           0.0,
    "model":                    "${MODEL}",
    "prompt":                   "",
    "created_at":               datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "updated_at":               datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "duration_ms":              0,
    "tool_calls":               [],
    "error_message":            "adapter infrastructure error (exit=${ADAPTER_EXIT})",
}
with open(sys.argv[1], "w") as f:
    json.dump(record, f, indent=2)
    f.write("\n")
PYEOF
      FAIL_COUNT=$(( FAIL_COUNT + 1 ))
    else
      PASS_COUNT=$(( PASS_COUNT + 1 ))
    fi

    # Append result to JSONL
    cat "${RESULT_JSON}" >> "${RESULTS_JSONL}"

  done  # tasks
done    # entrants

# ---------------------------------------------------------------------------
# Write report
# ---------------------------------------------------------------------------
{
  echo "=============================="
  echo " Comparison Harness Run Report"
  echo "=============================="
  echo " Timestamp : ${TIMESTAMP}"
  echo " Model     : ${MODEL}"
  echo " Task set  : ${TASK_SET_DIR}"
  echo " Entrants  : ${ACTIVE_ENTRANT_IDS}"
  echo " Tasks     : ${TASK_IDS[*]}"
  echo ""
  echo " Runs attempted : ${RUN_COUNT}"
  echo " Adapters OK    : ${PASS_COUNT}"
  echo " Adapter errors : ${FAIL_COUNT}"
  echo " Skipped        : ${SKIP_COUNT}"
  echo ""
  echo " Results JSONL  : ${RESULTS_JSONL}"
  echo " Provenance     : ${RUN_ENV_JSON}"
  echo ""
  echo "HONESTY NOTICE:"
  echo "  is_resolved (task pass/fail) is EXTERNAL — it must come from the"
  echo "  pytest oracle (terminal-bench or equivalent), not from this script."
  echo "  No head-to-head accuracy comparisons are asserted here."
  echo "  To get pass/fail numbers, merge oracle results into ${RESULTS_JSONL}."
  echo "=============================="
} | tee "${REPORT_TXT}"

info "report written: ${REPORT_TXT}"
info "done."
