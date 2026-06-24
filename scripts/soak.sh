#!/usr/bin/env bash
# soak.sh — long-running autonomy soak test for go-agent-harness
#
# Exercises: cron jobs, multi-turn memory (shared conversation_id), auto-compact
# (tiny context window), and budget-limited runs (low max_cost_usd).
# Writes a timestamped markdown report under soak-reports/.
#
# Usage:
#   ./scripts/soak.sh                    # 8h soak (default)
#   ./scripts/soak.sh --duration 30m     # short soak
#   ./scripts/soak.sh --duration 2h30m   # custom duration
#
# Flags:
#   --duration <dur>   How long to run (e.g. 30m, 2h, 8h). Default: 8h.
#   --port <port>      Port for harnessd (default: random 50000-59999).
#   --model <model>    LLM model to use (default: gpt-4.1-mini).
#   -h, --help         Show help.
#
# OPTIONAL: gated on OPENAI_API_KEY.  Exits cleanly with a clear message if unset.
# NOT part of any test gate — never referenced by test-regression.sh.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ---------------------------------------------------------------------------
# Defaults (overridable via flags)
# ---------------------------------------------------------------------------

DURATION="${SOAK_DURATION:-8h}"
PORT="${SOAK_PORT:-}"
MODEL="${SOAK_MODEL:-gpt-4.1-mini}"

# ---------------------------------------------------------------------------
# Parse flags
# ---------------------------------------------------------------------------

usage() {
  cat <<'EOF'
Usage: scripts/soak.sh [options]

Options:
  --duration <dur>   How long to run the soak (e.g. 30m, 2h, 8h). Default: 8h.
  --port <port>      Port for harnessd.  Default: random 50000-59999.
  --model <model>    LLM model name.  Default: gpt-4.1-mini.
  -h, --help         Show this message.

Env vars:
  OPENAI_API_KEY     Required — script exits cleanly if unset.
  SOAK_DURATION      Same as --duration.
  SOAK_PORT          Same as --port.
  SOAK_MODEL         Same as --model.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --duration)
      [[ $# -ge 2 ]] || { echo "[soak] --duration requires a value" >&2; exit 1; }
      DURATION="$2"; shift 2 ;;
    --port)
      [[ $# -ge 2 ]] || { echo "[soak] --port requires a value" >&2; exit 1; }
      PORT="$2"; shift 2 ;;
    --model)
      [[ $# -ge 2 ]] || { echo "[soak] --model requires a value" >&2; exit 1; }
      MODEL="$2"; shift 2 ;;
    -h|--help)
      usage; exit 0 ;;
    --)
      shift; break ;;
    -*)
      echo "[soak] unknown option: $1" >&2; usage >&2; exit 1 ;;
    *)
      echo "[soak] unexpected argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

# ---------------------------------------------------------------------------
# Gate on OPENAI_API_KEY
# ---------------------------------------------------------------------------

if [[ -z "${OPENAI_API_KEY:-}" ]]; then
  echo "[soak] OPENAI_API_KEY is not set — soak test is OPTIONAL and will not run."
  echo "[soak] Set OPENAI_API_KEY and re-run to execute the soak."
  exit 0
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

SOAK_PASS=0
SOAK_FAIL=0
SOAK_SKIP=0
ITERATION=0

log()  { echo "[soak] $*"; }
pass() { log "PASS: $*"; SOAK_PASS=$(( SOAK_PASS + 1 )); }
fail() { log "FAIL: $*"; SOAK_FAIL=$(( SOAK_FAIL + 1 )); }
skip() { log "SKIP: $*"; SOAK_SKIP=$(( SOAK_SKIP + 1 )); }

# parse_duration converts a human duration string to seconds.
# Supports: s, m, h (e.g. 30s, 5m, 2h, 1h30m, 8h).
parse_duration() {
  local dur="$1"
  local total=0
  local remaining="$dur"

  # hours
  if [[ "$remaining" =~ ^([0-9]+)h(.*)$ ]]; then
    total=$(( total + ${BASH_REMATCH[1]} * 3600 ))
    remaining="${BASH_REMATCH[2]}"
  fi
  # minutes
  if [[ "$remaining" =~ ^([0-9]+)m(.*)$ ]]; then
    total=$(( total + ${BASH_REMATCH[1]} * 60 ))
    remaining="${BASH_REMATCH[2]}"
  fi
  # seconds
  if [[ "$remaining" =~ ^([0-9]+)s?$ ]]; then
    total=$(( total + ${BASH_REMATCH[1]} ))
    remaining=""
  fi

  if [[ -n "$remaining" ]]; then
    echo "[soak] ERROR: cannot parse duration '${dur}' (unparsed: '${remaining}')" >&2
    return 1
  fi

  echo "$total"
}

DURATION_SECS="$(parse_duration "${DURATION}")"
log "soak duration: ${DURATION} (${DURATION_SECS}s)"

# ---------------------------------------------------------------------------
# Paths and dirs
# ---------------------------------------------------------------------------

BINARY="${SOAK_BINARY:-${REPO_ROOT}/harnessd}"
LOG_DIR="${REPO_ROOT}/soak-reports"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
HARNESS_LOG="${LOG_DIR}/${TIMESTAMP}-harnessd.log"
REPORT="${LOG_DIR}/${TIMESTAMP}-soak.md"

HARNESS_CONFIG_DIR="${REPO_ROOT}/.harness"
HARNESS_CONFIG="${HARNESS_CONFIG_DIR}/config.toml"
HARNESS_CONFIG_BACKUP="${HARNESS_CONFIG_DIR}/config.toml.soak.bak"

mkdir -p "${LOG_DIR}"

# ---------------------------------------------------------------------------
# Port selection
# ---------------------------------------------------------------------------

if [[ -z "${PORT}" ]]; then
  PORT=$(( ( RANDOM % 10000 ) + 50000 ))
fi
BASE_URL="http://localhost:${PORT}"
log "harnessd port: ${PORT}"

# ---------------------------------------------------------------------------
# State
# ---------------------------------------------------------------------------

SERVER_PID=""
CONFIG_BACKED_UP=0

# ---------------------------------------------------------------------------
# Cleanup trap (restores config, stops harnessd)
# ---------------------------------------------------------------------------

cleanup() {
  local exit_code=$?

  # Restore workspace config if we backed it up.
  if [[ "${CONFIG_BACKED_UP}" -eq 1 ]]; then
    if [[ -f "${HARNESS_CONFIG_BACKUP}" ]]; then
      log "restoring workspace config from backup..."
      mv -f "${HARNESS_CONFIG_BACKUP}" "${HARNESS_CONFIG}" 2>/dev/null || \
        log "WARN: could not restore config backup"
    else
      # We created the file from scratch — remove it.
      rm -f "${HARNESS_CONFIG}" 2>/dev/null || true
      # Remove the dir if it is now empty and didn't exist before.
      rmdir "${HARNESS_CONFIG_DIR}" 2>/dev/null || true
    fi
    CONFIG_BACKED_UP=0
  fi

  if [[ -n "${SERVER_PID}" ]]; then
    log "stopping harnessd (pid=${SERVER_PID})..."
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
    SERVER_PID=""
  fi

  # Write final report footer.
  if [[ -f "${REPORT}" ]]; then
    {
      echo ""
      echo "---"
      echo ""
      echo "## Final Summary"
      echo ""
      echo "| Metric | Value |"
      echo "|--------|-------|"
      echo "| Stopped at | $(date) |"
      echo "| Iterations | ${ITERATION} |"
      echo "| PASS | ${SOAK_PASS} |"
      echo "| FAIL | ${SOAK_FAIL} |"
      echo "| SKIP | ${SOAK_SKIP} |"
      echo "| Exit code | ${exit_code} |"
    } >> "${REPORT}"
    log "report written to: ${REPORT}"
  fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Write soak config (tiny auto_compact context window so compaction fires)
# ---------------------------------------------------------------------------

log "writing soak workspace config (tiny context window for auto_compact)..."
mkdir -p "${HARNESS_CONFIG_DIR}"

if [[ -f "${HARNESS_CONFIG}" ]]; then
  cp -f "${HARNESS_CONFIG}" "${HARNESS_CONFIG_BACKUP}"
  log "backed up existing workspace config to: ${HARNESS_CONFIG_BACKUP}"
fi
CONFIG_BACKED_UP=1

cat > "${HARNESS_CONFIG}" <<'TOML'
# Soak test workspace config — written by scripts/soak.sh
# Restored automatically via trap on exit.

[auto_compact]
enabled = true
mode = "strip"
# Tiny context window so compaction fires during normal soak runs.
model_context_window = 2000
threshold = 0.50
keep_last = 2

[memory]
enabled = true
mode = "auto"
default_enabled = true
observe_min_tokens = 1
TOML

log "soak config written: ${HARNESS_CONFIG}"

# ---------------------------------------------------------------------------
# Build harnessd (if binary not already present)
# ---------------------------------------------------------------------------

if [[ ! -x "${BINARY}" ]]; then
  log "building harnessd..."
  cd "${REPO_ROOT}"
  go build -o "${BINARY}" ./cmd/harnessd
  log "build complete: ${BINARY}"
fi

# ---------------------------------------------------------------------------
# Start harnessd in background (auth disabled)
# ---------------------------------------------------------------------------

log "starting harnessd on :${PORT} (auth disabled)..."
HARNESS_ADDR=":${PORT}" \
  HARNESS_AUTH_DISABLED=true \
  HARNESS_PROJECT_CONFIG="${HARNESS_CONFIG}" \
  "${BINARY}" \
  > "${HARNESS_LOG}" 2>&1 &
SERVER_PID=$!
log "harnessd pid=${SERVER_PID}, log=${HARNESS_LOG}"

# ---------------------------------------------------------------------------
# Wait for /healthz
# ---------------------------------------------------------------------------

log "waiting for /healthz (up to 30s)..."
HEALTH_WAITED=0
while true; do
  if curl -sf "${BASE_URL}/healthz" >/dev/null 2>&1; then
    log "/healthz ready"
    break
  fi
  HEALTH_WAITED=$(( HEALTH_WAITED + 1 ))
  if [[ "${HEALTH_WAITED}" -ge 30 ]]; then
    log "ERROR: harnessd did not respond within 30s"
    tail -20 "${HARNESS_LOG}" >&2 || true
    exit 1
  fi
  sleep 1
done

# ---------------------------------------------------------------------------
# Resolve a working model from /v1/models (prefer the requested MODEL)
# ---------------------------------------------------------------------------

log "resolving model via /v1/models..."
MODELS_BODY="$(curl -sf "${BASE_URL}/v1/models" || echo '{}')"
RESOLVED_MODEL="$(echo "${MODELS_BODY}" | python3 -c "
import sys, json, os
target = os.environ.get('SOAK_MODEL_NAME', '')
data = json.load(sys.stdin)
models = data if isinstance(data, list) else data.get('models', [])
ids = [m.get('id', '') for m in models]
# Prefer the requested model if present.
if target and target in ids:
    print(target)
elif ids:
    print(ids[0])
else:
    print('')
" SOAK_MODEL_NAME="${MODEL}" 2>/dev/null || echo "")"

if [[ -z "${RESOLVED_MODEL}" ]]; then
  log "WARN: could not resolve model from /v1/models — using configured value: ${MODEL}"
  RESOLVED_MODEL="${MODEL}"
fi
log "model: ${RESOLVED_MODEL}"

# ---------------------------------------------------------------------------
# Create a cron job for the soak (fires every minute; we'll poll its last-run)
# ---------------------------------------------------------------------------

CRON_JOB_ID=""
log "creating cron job (every 1 minute)..."
CRON_RESP="$(curl -sf -X POST "${BASE_URL}/v1/cron/jobs" \
  -H "Content-Type: application/json" \
  -d "{
    \"schedule\": \"* * * * *\",
    \"model\": \"${RESOLVED_MODEL}\",
    \"prompt\": \"Respond with the single word: SOAK_CRON_OK\",
    \"max_steps\": 1
  }" 2>/dev/null || echo "{}")"

CRON_JOB_ID="$(echo "${CRON_RESP}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('id', data.get('job_id', '')))
" 2>/dev/null || echo "")"

if [[ -n "${CRON_JOB_ID}" ]]; then
  log "cron job created: id=${CRON_JOB_ID}"
else
  log "WARN: cron endpoint not available or returned no id (response: ${CRON_RESP:0:120}) — cron checks will be skipped"
fi

# ---------------------------------------------------------------------------
# Initialise soak report
# ---------------------------------------------------------------------------

cat > "${REPORT}" <<MDEOF
# Soak Report — ${TIMESTAMP}

**Started:** $(date)
**Duration target:** ${DURATION} (${DURATION_SECS}s)
**Model:** ${RESOLVED_MODEL}
**Server:** ${BASE_URL}
**Harnessd log:** ${HARNESS_LOG}

---

MDEOF

log "report initialised: ${REPORT}"

# ---------------------------------------------------------------------------
# Helper: poll a run to completion (timeout 120s)
# ---------------------------------------------------------------------------

# poll_run <run_id>
# Echoes the final status to stdout. Writes progress to stderr.
poll_run() {
  local run_id="$1"
  local elapsed=0
  local status=""
  while true; do
    local body
    body="$(curl -sf "${BASE_URL}/v1/runs/${run_id}" 2>/dev/null || echo '{}')"
    status="$(echo "${body}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('status', ''))
" 2>/dev/null || echo "")"

    case "${status}" in
      completed|failed|cancelled)
        echo "${status}"
        return 0
        ;;
    esac

    elapsed=$(( elapsed + 2 ))
    if [[ "${elapsed}" -ge 120 ]]; then
      echo "timeout"
      return 0
    fi
    sleep 2
  done
}

# ---------------------------------------------------------------------------
# Helper: append a run summary block to the report
# ---------------------------------------------------------------------------

# append_run_summary <iteration> <label> <run_id> <status> <elapsed_s>
append_run_summary() {
  local iteration="$1"
  local label="$2"
  local run_id="$3"
  local status="$4"
  local elapsed_s="$5"

  {
    echo ""
    echo "### Iter ${iteration}: ${label}"
    echo ""
    echo "| Field | Value |"
    echo "|-------|-------|"
    echo "| run_id | \`${run_id}\` |"
    echo "| status | ${status} |"
    echo "| elapsed_s | ${elapsed_s} |"
    echo "| timestamp | $(date) |"
    echo ""
  } >> "${REPORT}"
}

# ---------------------------------------------------------------------------
# Soak loop
# ---------------------------------------------------------------------------

SOAK_START="$(date +%s)"
DEADLINE=$(( SOAK_START + DURATION_SECS ))

# Reuse one conversation_id for multi-turn memory runs.
MEMORY_CONV_ID="soak-conv-${TIMESTAMP}"

log "starting soak loop — deadline in ${DURATION_SECS}s"

{
  echo "## Soak Iterations"
  echo ""
} >> "${REPORT}"

while true; do
  NOW="$(date +%s)"
  if [[ "${NOW}" -ge "${DEADLINE}" ]]; then
    log "deadline reached — stopping soak loop"
    break
  fi
  REMAINING=$(( DEADLINE - NOW ))

  ITERATION=$(( ITERATION + 1 ))
  log "--- iteration ${ITERATION} (${REMAINING}s remaining) ---"

  # -------------------------------------------------------------------------
  # Exercise 1: Budget-limited run (low max_cost_usd — expect completed or
  # cost_limit_reached; both are acceptable outcomes in a soak).
  # -------------------------------------------------------------------------

  ITER_START="$(date +%s)"
  log "  [1] budget-limited run..."
  BUDGET_RUN_RESP="$(curl -sf -X POST "${BASE_URL}/v1/runs" \
    -H "Content-Type: application/json" \
    -d "{
      \"model\": \"${RESOLVED_MODEL}\",
      \"prompt\": \"Reply with exactly: SOAK_BUDGET_OK\",
      \"max_cost_usd\": 0.001,
      \"max_steps\": 3
    }" 2>/dev/null || echo "{}")"

  BUDGET_RUN_ID="$(echo "${BUDGET_RUN_RESP}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('run_id', data.get('id', '')))
" 2>/dev/null || echo "")"

  if [[ -n "${BUDGET_RUN_ID}" ]]; then
    BUDGET_STATUS="$(poll_run "${BUDGET_RUN_ID}")"
    ITER_ELAPSED=$(( $(date +%s) - ITER_START ))
    case "${BUDGET_STATUS}" in
      completed|failed)
        pass "budget-limited run ${BUDGET_RUN_ID} → ${BUDGET_STATUS}"
        ;;
      timeout)
        fail "budget-limited run ${BUDGET_RUN_ID} timed out"
        ;;
      *)
        # cost_limit_reached is an acceptable terminal state for a soak.
        pass "budget-limited run ${BUDGET_RUN_ID} → ${BUDGET_STATUS} (accepted)"
        ;;
    esac
    append_run_summary "${ITERATION}" "budget-limited" \
      "${BUDGET_RUN_ID}" "${BUDGET_STATUS}" "${ITER_ELAPSED}"
  else
    fail "budget-limited run: no run_id returned (response: ${BUDGET_RUN_RESP:0:80})"
    skip "budget-limited run summary (no id)"
  fi

  # -------------------------------------------------------------------------
  # Exercise 2: Multi-turn memory run (reuse same conversation_id)
  # -------------------------------------------------------------------------

  ITER_START="$(date +%s)"
  log "  [2] multi-turn memory run (conv=${MEMORY_CONV_ID})..."
  MEMORY_RUN_RESP="$(curl -sf -X POST "${BASE_URL}/v1/runs" \
    -H "Content-Type: application/json" \
    -d "{
      \"model\": \"${RESOLVED_MODEL}\",
      \"prompt\": \"Reply with exactly: SOAK_MEMORY_OK\",
      \"conversation_id\": \"${MEMORY_CONV_ID}\",
      \"max_steps\": 5
    }" 2>/dev/null || echo "{}")"

  MEMORY_RUN_ID="$(echo "${MEMORY_RUN_RESP}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('run_id', data.get('id', '')))
" 2>/dev/null || echo "")"

  if [[ -n "${MEMORY_RUN_ID}" ]]; then
    MEMORY_STATUS="$(poll_run "${MEMORY_RUN_ID}")"
    ITER_ELAPSED=$(( $(date +%s) - ITER_START ))
    if [[ "${MEMORY_STATUS}" == "completed" ]] || [[ "${MEMORY_STATUS}" == "failed" ]]; then
      pass "memory run ${MEMORY_RUN_ID} → ${MEMORY_STATUS}"
    else
      fail "memory run ${MEMORY_RUN_ID} → ${MEMORY_STATUS}"
    fi
    append_run_summary "${ITERATION}" "multi-turn-memory" \
      "${MEMORY_RUN_ID}" "${MEMORY_STATUS}" "${ITER_ELAPSED}"
  else
    fail "memory run: no run_id returned (response: ${MEMORY_RUN_RESP:0:80})"
    skip "memory run summary (no id)"
  fi

  # -------------------------------------------------------------------------
  # Exercise 3: Auto-compact run (small prompt over multiple steps so the tiny
  # context window has a chance to trigger compaction)
  # -------------------------------------------------------------------------

  ITER_START="$(date +%s)"
  log "  [3] auto-compact run..."
  COMPACT_RUN_RESP="$(curl -sf -X POST "${BASE_URL}/v1/runs" \
    -H "Content-Type: application/json" \
    -d "{
      \"model\": \"${RESOLVED_MODEL}\",
      \"prompt\": \"Count from 1 to 5, one number per sentence. After listing all numbers, reply: SOAK_COMPACT_OK\",
      \"max_steps\": 8
    }" 2>/dev/null || echo "{}")"

  COMPACT_RUN_ID="$(echo "${COMPACT_RUN_RESP}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('run_id', data.get('id', '')))
" 2>/dev/null || echo "")"

  if [[ -n "${COMPACT_RUN_ID}" ]]; then
    COMPACT_STATUS="$(poll_run "${COMPACT_RUN_ID}")"
    ITER_ELAPSED=$(( $(date +%s) - ITER_START ))
    if [[ "${COMPACT_STATUS}" == "completed" ]] || [[ "${COMPACT_STATUS}" == "failed" ]]; then
      pass "auto-compact run ${COMPACT_RUN_ID} → ${COMPACT_STATUS}"
    else
      fail "auto-compact run ${COMPACT_RUN_ID} → ${COMPACT_STATUS}"
    fi
    append_run_summary "${ITERATION}" "auto-compact" \
      "${COMPACT_RUN_ID}" "${COMPACT_STATUS}" "${ITER_ELAPSED}"
  else
    fail "auto-compact run: no run_id returned (response: ${COMPACT_RUN_RESP:0:80})"
    skip "auto-compact run summary (no id)"
  fi

  # -------------------------------------------------------------------------
  # Exercise 4: Check cron job last-run timestamp (if cron is available)
  # -------------------------------------------------------------------------

  if [[ -n "${CRON_JOB_ID}" ]]; then
    log "  [4] polling cron job ${CRON_JOB_ID}..."
    CRON_JOB_BODY="$(curl -sf "${BASE_URL}/v1/cron/jobs/${CRON_JOB_ID}" 2>/dev/null || echo "{}")"
    CRON_LAST_RUN="$(echo "${CRON_JOB_BODY}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('last_run_at', data.get('last_fired_at', '')))
" 2>/dev/null || echo "")"
    if [[ -n "${CRON_LAST_RUN}" ]]; then
      pass "cron job ${CRON_JOB_ID} has last_run_at=${CRON_LAST_RUN}"
    else
      # Cron may not have fired yet in early iterations — not a failure.
      log "  cron job ${CRON_JOB_ID}: no last_run_at yet (next_run_at may be in the future)"
    fi
  else
    skip "cron check (no cron job id)"
  fi

  # -------------------------------------------------------------------------
  # Inter-iteration pause (avoid hammering the API)
  # -------------------------------------------------------------------------

  LOOP_SLEEP=10
  NOW="$(date +%s)"
  REMAINING=$(( DEADLINE - NOW ))
  if [[ "${REMAINING}" -le 0 ]]; then
    log "deadline reached after iteration ${ITERATION}"
    break
  fi
  if [[ "${LOOP_SLEEP}" -gt "${REMAINING}" ]]; then
    LOOP_SLEEP="${REMAINING}"
  fi

  log "  sleeping ${LOOP_SLEEP}s before next iteration..."
  sleep "${LOOP_SLEEP}"
done

# ---------------------------------------------------------------------------
# Cleanup cron job (best-effort)
# ---------------------------------------------------------------------------

if [[ -n "${CRON_JOB_ID}" ]]; then
  log "deleting cron job ${CRON_JOB_ID}..."
  curl -sf -X DELETE "${BASE_URL}/v1/cron/jobs/${CRON_JOB_ID}" >/dev/null 2>&1 || true
fi

# ---------------------------------------------------------------------------
# Final status line
# ---------------------------------------------------------------------------

log ""
log "============================================"
log " Soak Complete"
log "============================================"
log " Iterations : ${ITERATION}"
log " PASS       : ${SOAK_PASS}"
log " FAIL       : ${SOAK_FAIL}"
log " SKIP       : ${SOAK_SKIP}"
log " Report     : ${REPORT}"
log "============================================"

if [[ "${SOAK_FAIL}" -gt 0 ]]; then
  exit 1
fi
exit 0
