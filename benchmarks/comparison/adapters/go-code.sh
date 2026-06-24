#!/usr/bin/env bash
# adapters/go-code.sh — Adapter for go-agent-harness (go-code).
#
# Wraps the harness run API:
#   1. Start harnessd (or reuse a running instance).
#   2. POST /v1/runs with the task prompt.
#   3. Poll GET /v1/runs/{id} until terminal status.
#   4. GET /v1/runs/{id}/summary for telemetry.
#   5. Build RESULT_JSON from the run + summary responses.
#
# KEY-FREE SMOKE MODE:
#   HARNESS_PROVIDER=fake  — use the fakeprovider path (PC1 in issue5-plan).
#   HARNESS_FAKE_TURNS     — path to a turns JSON file for scripted responses.
#   When both are set, no API key is required.
#
# ENVIRONMENT INPUTS (set by run.sh per adapter contract in adapters/template.sh):
#   TASK_DIR      Absolute path to task directory.
#   WORKSPACE     Per-run scratch directory.
#   PROMPT        Task prompt text.
#   MODEL         Model name to use (e.g. "gpt-4.1-mini").
#   RESULT_JSON   Path where this adapter must write the result record.
#   TASK_ID       Task identifier (included in result).
#   TOOL_ID       Entrant ID ("go-code" — included in result).
#
# ADDITIONAL ENVIRONMENT (optional, adapter-specific):
#   HARNESS_BINARY        Path to harnessd binary (default: harnessd on PATH).
#   HARNESS_ADDR          Listen address for harnessd (default: :8081 to avoid
#                         colliding with a dev server on :8080).
#   HARNESS_PROVIDER      Provider name ("fake" for key-free mode).
#   HARNESS_FAKE_TURNS    Path to fake-turns JSON (used with HARNESS_PROVIDER=fake).
#   OPENAI_API_KEY        Required unless HARNESS_PROVIDER=fake.
#   HARNESS_AUTH_DISABLED Set to "true" if server has auth disabled (default: true
#                         for benchmark runs on localhost).
#   POLL_INTERVAL_S       Seconds between status polls (default: 2).
#   POLL_TIMEOUT_S        Max seconds to wait for run completion (default: 300).

set -euo pipefail

# ---------------------------------------------------------------------------
# Validate required contract inputs
# ---------------------------------------------------------------------------
: "${TASK_DIR:?TASK_DIR must be set by run.sh}"
: "${WORKSPACE:?WORKSPACE must be set by run.sh}"
: "${PROMPT:?PROMPT must be set by run.sh}"
: "${MODEL:?MODEL must be set by run.sh}"
: "${RESULT_JSON:?RESULT_JSON must be set by run.sh}"
: "${TASK_ID:?TASK_ID must be set by run.sh}"
: "${TOOL_ID:?TOOL_ID must be set by run.sh}"

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
HARNESS_BINARY="${HARNESS_BINARY:-harnessd}"
HARNESS_ADDR="${HARNESS_ADDR:-:8081}"
POLL_INTERVAL_S="${POLL_INTERVAL_S:-2}"
POLL_TIMEOUT_S="${POLL_TIMEOUT_S:-300}"

PORT="${HARNESS_ADDR##*:}"
BASE_URL="http://127.0.0.1:${PORT}"

LOG_FILE="${WORKSPACE}/harnessd.log"
PID_FILE="${WORKSPACE}/harnessd.pid"
STARTED_BY_US=0
SERVER_PID=""

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
info() { printf '[go-code-adapter] %s\n' "$*" >&2; }
die()  { printf '[go-code-adapter] ERROR: %s\n' "$*" >&2; exit 1; }

# Prefer jq for JSON extraction; fall back to python3.
json_field() {
  local json="$1" field="$2"
  if command -v jq &>/dev/null; then
    printf '%s' "$json" | jq -r --arg f "$field" '.[$f] // empty'
  else
    printf '%s' "$json" | python3 -c "
import json, sys
data = json.load(sys.stdin)
print(data.get('$field', ''))
"
  fi
}

json_field_num() {
  local json="$1" field="$2" default="${3:-0}"
  if command -v jq &>/dev/null; then
    printf '%s' "$json" | jq -r --arg f "$field" --argjson d "$default" '.[$f] // $d'
  else
    printf '%s' "$json" | python3 -c "
import json, sys
data = json.load(sys.stdin)
val = data.get('$field')
print(val if val is not None else $default)
"
  fi
}

# ---------------------------------------------------------------------------
# Server lifecycle
# ---------------------------------------------------------------------------
stop_server() {
  if [[ "$STARTED_BY_US" -ne 1 ]]; then
    return 0
  fi
  if [[ -z "$SERVER_PID" ]]; then
    return 0
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    return 0
  fi
  info "stopping harnessd (pid=${SERVER_PID})"
  kill "$SERVER_PID" 2>/dev/null || true
  local waited=0
  while kill -0 "$SERVER_PID" 2>/dev/null && [[ $waited -lt 25 ]]; do
    sleep 0.2
    waited=$(( waited + 1 ))
  done
  if kill -0 "$SERVER_PID" 2>/dev/null; then
    kill -9 "$SERVER_PID" 2>/dev/null || true
  fi
}
trap stop_server EXIT

start_server() {
  if ! command -v "$HARNESS_BINARY" &>/dev/null; then
    die "harnessd binary not found: ${HARNESS_BINARY}. Build with: go build -o harnessd ./cmd/harnessd"
  fi

  info "starting harnessd on ${HARNESS_ADDR} (log: ${LOG_FILE})"

  HARNESS_ADDR="${HARNESS_ADDR}" \
  HARNESS_AUTH_DISABLED="${HARNESS_AUTH_DISABLED:-true}" \
  "${HARNESS_BINARY}" \
    >"${LOG_FILE}" 2>&1 &
  SERVER_PID=$!
  echo "$SERVER_PID" > "$PID_FILE"
  STARTED_BY_US=1

  info "waiting for /healthz (pid=${SERVER_PID})..."
  local waited=0
  while ! curl -sf "${BASE_URL}/healthz" >/dev/null 2>&1; do
    sleep 0.5
    waited=$(( waited + 1 ))
    if [[ $waited -ge 20 ]]; then
      die "harnessd did not become healthy within 10s. See log: ${LOG_FILE}"
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      die "harnessd process (pid=${SERVER_PID}) died before becoming healthy. See log: ${LOG_FILE}"
    fi
  done
  info "harnessd ready at ${BASE_URL}"
}

# ---------------------------------------------------------------------------
# Ensure a server is running
# ---------------------------------------------------------------------------
if curl -sf "${BASE_URL}/healthz" >/dev/null 2>&1; then
  info "reusing existing harnessd at ${BASE_URL}"
else
  start_server
fi

# ---------------------------------------------------------------------------
# Record git SHA for reproducibility (best-effort)
# ---------------------------------------------------------------------------
GIT_SHA=""
if command -v git &>/dev/null; then
  GIT_SHA="$(git -C "$(dirname "${BASH_SOURCE[0]}")/../../.." rev-parse --short HEAD 2>/dev/null || true)"
fi

# ---------------------------------------------------------------------------
# Build POST /v1/runs payload
# ---------------------------------------------------------------------------
if command -v jq &>/dev/null; then
  RUN_PAYLOAD="$(jq -n \
    --arg prompt "$PROMPT" \
    --arg model  "$MODEL" \
    '{prompt: $prompt, model: $model, allow_fallback: true}')"
else
  RUN_PAYLOAD="$(python3 -c "
import json, sys
print(json.dumps({'prompt': sys.argv[1], 'model': sys.argv[2], 'allow_fallback': True}))
" "$PROMPT" "$MODEL")"
fi

# ---------------------------------------------------------------------------
# POST /v1/runs
# ---------------------------------------------------------------------------
info "POST ${BASE_URL}/v1/runs (model=${MODEL})"
RUN_RESPONSE="$(curl -sf -X POST \
  -H "Content-Type: application/json" \
  -d "$RUN_PAYLOAD" \
  "${BASE_URL}/v1/runs")" || die "POST /v1/runs failed"

RUN_ID="$(json_field "$RUN_RESPONSE" "id")"
if [[ -z "$RUN_ID" ]]; then
  # Some server versions use run_id instead of id
  RUN_ID="$(json_field "$RUN_RESPONSE" "run_id")"
fi
if [[ -z "$RUN_ID" ]]; then
  die "POST /v1/runs did not return an id. Response: ${RUN_RESPONSE}"
fi
info "run started: ${RUN_ID}"

# ---------------------------------------------------------------------------
# Poll for completion
# ---------------------------------------------------------------------------
info "polling for completion (timeout=${POLL_TIMEOUT_S}s)..."
ELAPSED=0
RUN_STATUS=""
FINAL_RUN_JSON=""
while true; do
  FINAL_RUN_JSON="$(curl -sf "${BASE_URL}/v1/runs/${RUN_ID}" || echo '{}')"
  RUN_STATUS="$(json_field "$FINAL_RUN_JSON" "status")"

  case "$RUN_STATUS" in
    completed|failed|cancelled)
      info "run ${RUN_ID} reached status: ${RUN_STATUS}"
      break
      ;;
  esac

  ELAPSED=$(( ELAPSED + POLL_INTERVAL_S ))
  if [[ $ELAPSED -ge $POLL_TIMEOUT_S ]]; then
    info "WARNING: run ${RUN_ID} did not complete within ${POLL_TIMEOUT_S}s (last status: ${RUN_STATUS}). Writing partial result."
    RUN_STATUS="failed"
    break
  fi
  info "  status=${RUN_STATUS}, elapsed=${ELAPSED}s"
  sleep "$POLL_INTERVAL_S"
done

# ---------------------------------------------------------------------------
# GET /v1/runs/{id}/summary
# ---------------------------------------------------------------------------
SUMMARY_JSON=""
if [[ "$RUN_STATUS" == "completed" || "$RUN_STATUS" == "failed" ]]; then
  SUMMARY_JSON="$(curl -sf "${BASE_URL}/v1/runs/${RUN_ID}/summary" 2>/dev/null || echo '{}')"
fi

# ---------------------------------------------------------------------------
# Build RESULT_JSON
#
# Fields sourced from FINAL_RUN_JSON (Run struct):
#   model, provider_name, prompt, output, tenant_id, conversation_id, agent_id,
#   created_at, updated_at
#
# Fields sourced from SUMMARY_JSON (RunSummary struct):
#   run_id, status, steps_taken, total_prompt_tokens, total_completion_tokens,
#   total_cost_usd, cost_status, cache_hit_rate, error_message, tool_calls
#
# DERIVED fields (computed here):
#   duration_ms = (updated_at − created_at) in ms  [python computes this]
#
# EXTERNAL fields (not set by adapter):
#   is_resolved, drift, forensic_events, rollout_path
#
# Adapter-added fields:
#   tool_id, task_id, git_sha
# ---------------------------------------------------------------------------
info "building result record at ${RESULT_JSON}"

python3 - <<PYEOF
import json, sys, os
from datetime import datetime, timezone

run_json    = json.loads("""${FINAL_RUN_JSON}""")
summary_json = json.loads("""${SUMMARY_JSON:-{}}""")

# --- Identity ---
run_id = summary_json.get("run_id") or run_json.get("id") or run_json.get("run_id") or "${RUN_ID}"

# --- Status (from summary if available, else from run) ---
status = summary_json.get("status") or run_json.get("status") or "${RUN_STATUS}"

# --- Counters from RunSummary ---
steps_taken             = int(summary_json.get("steps_taken") or 0)
total_prompt_tokens     = int(summary_json.get("total_prompt_tokens") or 0)
total_completion_tokens = int(summary_json.get("total_completion_tokens") or 0)
total_cost_usd          = float(summary_json.get("total_cost_usd") or 0.0)
cost_status             = summary_json.get("cost_status") or "provider_unreported"
cache_hit_rate          = float(summary_json.get("cache_hit_rate") or 0.0)
error_message           = summary_json.get("error") or run_json.get("error") or ""

# --- Tool calls from RunSummary ---
raw_tool_calls = summary_json.get("tool_calls") or []
tool_calls = [
    {"tool_name": tc.get("tool_name", ""), "step": int(tc.get("step", 0))}
    for tc in raw_tool_calls
]

# --- Run identity (from Run struct) ---
model         = run_json.get("model") or "${MODEL}"
provider_name = run_json.get("provider_name") or ""
prompt        = run_json.get("prompt") or "${PROMPT}"
output        = run_json.get("output") or ""
tenant_id     = run_json.get("tenant_id") or ""
conversation_id = run_json.get("conversation_id") or ""
agent_id      = run_json.get("agent_id") or ""

# --- Timestamps (source: Run) ---
created_at_str = run_json.get("created_at") or ""
updated_at_str = run_json.get("updated_at") or ""

# --- DERIVED: duration_ms ---
duration_ms = 0
if created_at_str and updated_at_str:
    try:
        # RFC3339 with possible fractional seconds
        def parse_ts(s):
            s = s.rstrip("Z")
            for fmt in ("%Y-%m-%dT%H:%M:%S.%f", "%Y-%m-%dT%H:%M:%S"):
                try:
                    return datetime.strptime(s, fmt).replace(tzinfo=timezone.utc)
                except ValueError:
                    continue
            return None
        t_created = parse_ts(created_at_str)
        t_updated = parse_ts(updated_at_str)
        if t_created and t_updated:
            duration_ms = int((t_updated - t_created).total_seconds() * 1000)
    except Exception:
        pass

result = {
    "tool_id":                  "${TOOL_ID}",
    "task_id":                  "${TASK_ID}",
    "run_id":                   run_id,
    "status":                   status,
    "steps_taken":              steps_taken,
    "total_prompt_tokens":      total_prompt_tokens,
    "total_completion_tokens":  total_completion_tokens,
    "total_cost_usd":           total_cost_usd,
    "cost_status":              cost_status,
    "cache_hit_rate":           cache_hit_rate,
    "model":                    model,
    "prompt":                   prompt,
    "created_at":               created_at_str,
    "updated_at":               updated_at_str,
    "duration_ms":              duration_ms,
    "tool_calls":               tool_calls,
}

# Optional fields — only include when non-empty
if error_message:
    result["error_message"] = error_message
if output:
    result["output"] = output
if provider_name:
    result["provider_name"] = provider_name
if tenant_id:
    result["tenant_id"] = tenant_id
if conversation_id:
    result["conversation_id"] = conversation_id
if agent_id:
    result["agent_id"] = agent_id
if "${GIT_SHA:-}":
    result["git_sha"] = "${GIT_SHA}"

# is_resolved, drift, forensic_events, rollout_path are EXTERNAL — NOT set here.

with open("${RESULT_JSON}", "w") as f:
    json.dump(result, f, indent=2)
    f.write("\n")

print(f"[go-code-adapter] wrote result: status={status}, run_id={run_id}")
PYEOF

info "adapter complete: ${RESULT_JSON}"
