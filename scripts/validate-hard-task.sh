#!/usr/bin/env bash
# validate-hard-task.sh <task-name>
# Faithful discrimination gate for an authored hard task, run inside the same
# base image the grader uses. Verifies the hidden oracle:
#   - FAILS against the buggy starting code (task ships a real bug)
#   - PASSES against the reference solution (a correct fix is accepted)
# A task is only sound if BOTH hold.
set -uo pipefail

TASK="${1:?usage: validate-hard-task.sh <task-name>}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/benchmarks/terminal_bench"
TDIR="$ROOT/tasks/$TASK"
REFDIR="$ROOT/reference_solutions/$TASK"
BASE="go-agent-harness-tb-base:latest"

[ -d "$TDIR" ] || { echo "no task dir: $TDIR"; exit 2; }
[ -d "$REFDIR" ] || { echo "no reference dir: $REFDIR"; exit 2; }

run_oracle () {  # $1=label  $2=overlay-dir(optional)
  local label="$1" overlay="${2:-}"
  local work; work="$(mktemp -d)"
  cp "$TDIR"/*.go "$work"/ 2>/dev/null
  cp "$TDIR"/go.mod "$work"/
  [ -n "$overlay" ] && cp "$overlay"/*.go "$work"/
  docker run --rm -v "$work":/app -v "$TDIR/tests":/tests:ro "$BASE" \
    bash -lc 'cd /app && python3 -m pytest -rA -q /tests' >"$work/out.txt" 2>&1
  local rc=$?
  echo "----- [$label] pytest rc=$rc -----"
  tail -n 8 "$work/out.txt"
  rm -rf "$work"
  return $rc
}

echo "### validating task: $TASK"
run_oracle "BUGGY (expect FAIL)"; buggy_rc=$?
run_oracle "REFERENCE (expect PASS)" "$REFDIR"; ref_rc=$?

echo
if [ "$buggy_rc" -ne 0 ] && [ "$ref_rc" -eq 0 ]; then
  echo "RESULT: $TASK OK  (buggy fails, reference passes — task discriminates)"
  exit 0
else
  echo "RESULT: $TASK BROKEN  (buggy_rc=$buggy_rc want!=0 ; ref_rc=$ref_rc want=0)"
  exit 1
fi
