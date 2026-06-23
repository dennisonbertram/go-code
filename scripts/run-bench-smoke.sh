#!/usr/bin/env bash
# run-bench-smoke.sh — key-free deterministic smoke for harnessd with the fake provider.
#
# Drives: prebuild harnessd → start with HARNESS_PROVIDER=fake → /healthz →
#         POST /v1/runs → poll GET /v1/runs/{id} → GET summary → assert fields.
#
# Requirements:
#   - Go toolchain on PATH (for prebuild step)
#   - curl on PATH
#   - No API key required (HARNESS_PROVIDER=fake)
#
# Exit codes:
#   0 — all assertions passed
#   1 — at least one assertion failed or fatal error
#
# Deterministic values (must match fakeProviderTurnJSON in cmd/harnessd/main.go):
#   content              = "smoke ok"
#   usage.prompt         = 100   (PromptTokens)
#   usage.completion     = 50    (CompletionTokens)
#   cost_usd             = 0.001 (TotalCostUSD, derived from CostUSD field)
#   cost_status          = "available"
#   expected status      = "completed"
#   expected steps_taken = 1

set -euo pipefail

# ---------------------------------------------------------------------------
# Paths and config
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

BINARY="${HARNESS_BINARY:-${REPO_ROOT}/harnessd-bench-smoke}"
LOG_FILE="${HARNESS_BENCH_SMOKE_LOG:-/tmp/harnessd-bench-smoke.log}"
OUTPUT_FILE="${HARNESS_BENCH_SMOKE_OUTPUT:-/tmp/harnessd-bench-smoke-result.json}"
TURNS_FILE="${HARNESS_BENCH_SMOKE_TURNS:-/tmp/harnessd-bench-smoke-turns.json}"
TIMEOUT_S="${HARNESS_BENCH_SMOKE_TIMEOUT:-30}"
SKIP_BUILD="${HARNESS_BENCH_SMOKE_SKIP_BUILD:-}"

# Pick a random ephemeral port to avoid conflicts.
PORT=$(( ( RANDOM % 10000 ) + 50000 ))
BASE_URL="http://localhost:${PORT}"

PASS_COUNT=0
FAIL_COUNT=0
SERVER_PID=""

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

info()  { printf '[bench-smoke] %s\n' "$*"; }
pass()  { printf '[bench-smoke] PASS: %s\n' "$*"; PASS_COUNT=$(( PASS_COUNT + 1 )); }
fail()  { printf '[bench-smoke] FAIL: %s\n' "$*" >&2; FAIL_COUNT=$(( FAIL_COUNT + 1 )); }
fatal() { printf '[bench-smoke] FATAL: %s\n' "$*" >&2; exit 1; }

cleanup() {
    if [ -n "${SERVER_PID}" ]; then
        info "stopping harnessd (pid=${SERVER_PID})"
        kill "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
        SERVER_PID=""
    fi
    # Clean up temp binary if we built it here.
    if [ -f "${BINARY}" ] && [ -z "${HARNESS_BINARY:-}" ]; then
        rm -f "${BINARY}"
    fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Step 1: Write the deterministic HARNESS_FAKE_TURNS JSON file.
#
# Schema (fakeProviderTurnJSON in cmd/harnessd/main.go):
#   content     string          → CompletionResult.Content
#   usage       {prompt,completion} → CompletionUsage (prompt/completion short keys)
#   cost_usd    float64         → CompletionResult.CostUSD (pointer)
#   cost_status string          → CompletionResult.CostStatus
#
# Deterministic values chosen to be byte-stable and match TestRunSmoke (B1):
#   prompt=100, completion=50, cost_usd=0.001, cost_status="available"
# ---------------------------------------------------------------------------

info "writing fake turns file: ${TURNS_FILE}"
cat > "${TURNS_FILE}" <<'TURNS_EOF'
[
  {
    "content": "smoke ok",
    "usage": {"prompt": 100, "completion": 50},
    "cost_usd": 0.001,
    "cost_status": "available"
  }
]
TURNS_EOF
pass "wrote fake turns file"

# ---------------------------------------------------------------------------
# Step 2: Prebuild harnessd (unless skipped or binary already present).
# ---------------------------------------------------------------------------

if [ -n "${SKIP_BUILD}" ] && [ -x "${BINARY}" ]; then
    info "SKIP_BUILD set and binary exists — skipping build"
    pass "prebuild skipped (binary: ${BINARY})"
elif [ -x "${BINARY}" ] && [ -z "${SKIP_BUILD}" ]; then
    info "binary already present at ${BINARY} — rebuilding to ensure freshness"
    info "building harnessd → ${BINARY}"
    (cd "${REPO_ROOT}" && go build -o "${BINARY}" ./cmd/harnessd) \
        || fatal "go build failed; see output above"
    pass "harnessd built: ${BINARY}"
else
    info "building harnessd → ${BINARY}"
    (cd "${REPO_ROOT}" && go build -o "${BINARY}" ./cmd/harnessd) \
        || fatal "go build failed; see output above"
    pass "harnessd built: ${BINARY}"
fi

# ---------------------------------------------------------------------------
# Step 3: Start harnessd with HARNESS_PROVIDER=fake + auth disabled.
# ---------------------------------------------------------------------------

info "starting harnessd on port ${PORT} (fake provider, auth disabled)..."
HARNESS_ADDR=":${PORT}" \
HARNESS_AUTH_DISABLED=true \
HARNESS_PROVIDER=fake \
HARNESS_FAKE_TURNS="${TURNS_FILE}" \
    "${BINARY}" \
    >"${LOG_FILE}" 2>&1 &
SERVER_PID=$!
info "harnessd pid=${SERVER_PID}, log=${LOG_FILE}"

# ---------------------------------------------------------------------------
# Step 4: Wait for /healthz (up to 15s).
# ---------------------------------------------------------------------------

info "waiting for /healthz (up to 15s)..."
HEALTH_WAITED=0
while true; do
    if curl -sf "${BASE_URL}/healthz" >/dev/null 2>&1; then
        pass "/healthz responding"
        break
    fi
    HEALTH_WAITED=$(( HEALTH_WAITED + 1 ))
    if [ "${HEALTH_WAITED}" -ge 15 ]; then
        info "server log tail:"
        tail -20 "${LOG_FILE}" >&2 || true
        fatal "/healthz did not respond within 15s"
    fi
    sleep 1
done

# ---------------------------------------------------------------------------
# Step 5: POST /v1/runs — start a run with no model (uses fake default).
# ---------------------------------------------------------------------------

info "POST /v1/runs..."
# allow_fallback=true: if the model registry lookup fails (no OPENAI_API_KEY),
# the runner falls back to r.provider — our fake — rather than failing the run.
POST_RESPONSE=$(curl -sf -X POST "${BASE_URL}/v1/runs" \
    -H "Content-Type: application/json" \
    -d '{"prompt":"bench smoke test prompt","allow_fallback":true}') \
    || fatal "POST /v1/runs failed"

RUN_ID=$(printf '%s' "${POST_RESPONSE}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('run_id', ''))
" 2>/dev/null || true)

if [ -z "${RUN_ID}" ]; then
    fail "POST /v1/runs: no run_id in response: ${POST_RESPONSE}"
    fatal "cannot continue without a run_id"
fi
pass "POST /v1/runs → run_id=${RUN_ID}"

# ---------------------------------------------------------------------------
# Step 6: Poll GET /v1/runs/{id} until terminal state (timeout TIMEOUT_S s).
# ---------------------------------------------------------------------------

info "polling GET /v1/runs/${RUN_ID} for terminal state (timeout ${TIMEOUT_S}s)..."
ELAPSED=0
FINAL_STATUS=""
while true; do
    GET_BODY=$(curl -sf "${BASE_URL}/v1/runs/${RUN_ID}" 2>/dev/null || echo '{}')
    STATUS=$(printf '%s' "${GET_BODY}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
print(data.get('status', ''))
" 2>/dev/null || echo "")

    case "${STATUS}" in
        completed|failed|cancelled)
            FINAL_STATUS="${STATUS}"
            break
            ;;
    esac

    ELAPSED=$(( ELAPSED + 1 ))
    if [ "${ELAPSED}" -ge "${TIMEOUT_S}" ]; then
        fail "run ${RUN_ID} did not reach terminal state within ${TIMEOUT_S}s (last status: ${STATUS})"
        info "server log tail:"
        tail -20 "${LOG_FILE}" >&2 || true
        fatal "timed out waiting for run completion"
    fi
    sleep 1
done

pass "run terminal status: ${FINAL_STATUS}"

# Assert status is "completed" (not failed/cancelled).
if [ "${FINAL_STATUS}" != "completed" ]; then
    fail "expected status=completed, got ${FINAL_STATUS}"
fi

# ---------------------------------------------------------------------------
# Step 7: GET /v1/runs/{id}/summary — fetch and save the run summary.
# ---------------------------------------------------------------------------

info "GET /v1/runs/${RUN_ID}/summary..."
SUMMARY_BODY=$(curl -sf "${BASE_URL}/v1/runs/${RUN_ID}/summary") \
    || fatal "GET /v1/runs/${RUN_ID}/summary failed"

# Save raw summary JSON to output file.
printf '%s\n' "${SUMMARY_BODY}" > "${OUTPUT_FILE}"
info "summary saved to ${OUTPUT_FILE}"
pass "summary fetched"

# Pretty-print for log visibility (best-effort).
info "summary contents:"
printf '%s\n' "${SUMMARY_BODY}" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    print(json.dumps(data, indent=2))
except Exception:
    pass
" 2>/dev/null || printf '%s\n' "${SUMMARY_BODY}"

# ---------------------------------------------------------------------------
# Step 8: Assert deterministic fields.
#
# Expected values (must match the scripted turn above and TestRunSmoke B1):
#   status                  = "completed"
#   steps_taken             = 1
#   total_prompt_tokens     = 100
#   total_completion_tokens = 50
#   total_cost_usd          = 0.001
#   cost_status             = "available"
# ---------------------------------------------------------------------------

info "asserting deterministic summary fields..."

assert_field() {
    local field_name="$1"
    local want="$2"
    local got="$3"
    if [ "${got}" = "${want}" ]; then
        pass "${field_name}=${got}"
    else
        fail "${field_name}: want=${want}, got=${got}"
    fi
}

extract_field() {
    local field="$1"
    printf '%s' "${SUMMARY_BODY}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
v = data.get('${field}')
print('' if v is None else str(v))
" 2>/dev/null || echo ""
}

SUMMARY_STATUS=$(extract_field "status")
SUMMARY_STEPS=$(extract_field "steps_taken")
SUMMARY_PROMPT_TOKENS=$(extract_field "total_prompt_tokens")
SUMMARY_COMPLETION_TOKENS=$(extract_field "total_completion_tokens")
SUMMARY_COST_USD=$(extract_field "total_cost_usd")
SUMMARY_COST_STATUS=$(extract_field "cost_status")
SUMMARY_RUN_ID=$(extract_field "run_id")

assert_field "summary.run_id"               "${RUN_ID}"     "${SUMMARY_RUN_ID}"
assert_field "summary.status"               "completed"     "${SUMMARY_STATUS}"
assert_field "summary.steps_taken"          "1"             "${SUMMARY_STEPS}"
assert_field "summary.total_prompt_tokens"  "100"           "${SUMMARY_PROMPT_TOKENS}"
assert_field "summary.total_completion_tokens" "50"         "${SUMMARY_COMPLETION_TOKENS}"
assert_field "summary.total_cost_usd"       "0.001"         "${SUMMARY_COST_USD}"
assert_field "summary.cost_status"          "available"     "${SUMMARY_COST_STATUS}"

# Grep-based assertions on the raw JSON body (belt-and-suspenders).
if ! printf '%s' "${SUMMARY_BODY}" | grep -q '"status":"completed"'; then
    fail 'grep: "status":"completed" not found in summary JSON'
fi
if ! printf '%s' "${SUMMARY_BODY}" | grep -q '"cost_status":"available"'; then
    fail 'grep: "cost_status":"available" not found in summary JSON'
fi

# ---------------------------------------------------------------------------
# Step 9: Summary
# ---------------------------------------------------------------------------

echo ""
echo "============================================================"
echo " Bench Smoke Summary"
echo "============================================================"
echo " PASS: ${PASS_COUNT}"
echo " FAIL: ${FAIL_COUNT}"
echo " run_id: ${RUN_ID}"
echo " output: ${OUTPUT_FILE}"
echo "============================================================"

if [ "${FAIL_COUNT}" -ne 0 ]; then
    printf '[bench-smoke] %d ASSERTION(S) FAILED — server log: %s\n' "${FAIL_COUNT}" "${LOG_FILE}" >&2
    exit 1
fi

echo "[bench-smoke] ALL ASSERTIONS PASSED"
exit 0
