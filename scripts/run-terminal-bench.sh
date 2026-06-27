#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DATASET_PATH="${TERMINAL_BENCH_DATASET_PATH:-${REPO_ROOT}/benchmarks/terminal_bench/tasks}"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUTPUT_DIR="${TERMINAL_BENCH_OUTPUT_DIR:-${REPO_ROOT}/.tmp/terminal-bench/${TIMESTAMP}}"
AGENT_IMPORT_PATH="${TERMINAL_BENCH_AGENT_IMPORT_PATH:-benchmarks.terminal_bench.agent:GoAgentHarnessAgent}"
PYTHON_VERSION="${TERMINAL_BENCH_PYTHON_VERSION:-3.12}"
MODEL="${TERMINAL_BENCH_MODEL:-${HARNESS_BENCH_MODEL:-gpt-5-mini}}"
N_CONCURRENT="${TERMINAL_BENCH_N_CONCURRENT:-1}"
N_ATTEMPTS="${TERMINAL_BENCH_N_ATTEMPTS:-1}"
GLOBAL_AGENT_TIMEOUT_SEC="${TERMINAL_BENCH_GLOBAL_AGENT_TIMEOUT_SEC:-1800}"
GLOBAL_TEST_TIMEOUT_SEC="${TERMINAL_BENCH_GLOBAL_TEST_TIMEOUT_SEC:-300}"
TARGET_ARCH="${HARNESS_BENCH_TARGET_ARCH:-}"
SKIP_IMAGE_BUILD="${TERMINAL_BENCH_SKIP_BUILD:-false}"
SKIP_HARNESS_BUILD="${TERMINAL_BENCH_SKIP_HARNESS_BUILD:-false}"
PREFLIGHT_ONLY=false
BENCH_MIN_ACCURACY="${BENCH_MIN_ACCURACY:-70}"

TB_CMD=()
TB_LIVENESS_CMD=()

info() { printf '[terminal-bench] %s\n' "$*" >&2; }
die() { printf '[terminal-bench] ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: scripts/run-terminal-bench.sh [options] [extra tb run args]

Options:
  --skip-build        Skip Docker image build.
  --build-base-only   Build the shared Terminal-Bench base image and exit.
  --preflight-only    Run local preflight checks and exit.

Environment:
  TERMINAL_BENCH_DATASET_PATH
  TERMINAL_BENCH_OUTPUT_DIR
  TERMINAL_BENCH_AGENT_IMPORT_PATH
  TERMINAL_BENCH_MODEL or HARNESS_BENCH_MODEL
  TERMINAL_BENCH_N_CONCURRENT
  TERMINAL_BENCH_N_ATTEMPTS
  TERMINAL_BENCH_GLOBAL_AGENT_TIMEOUT_SEC
  TERMINAL_BENCH_GLOBAL_TEST_TIMEOUT_SEC
  HARNESS_PROVIDER=fake with HARNESS_FAKE_TURNS for key-free smoke
EOF
}

EXTRA_ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_IMAGE_BUILD=true
      shift
      ;;
    --build-base-only)
      "${SCRIPT_DIR}/build-bench-images.sh"
      exit 0
      ;;
    --preflight-only)
      PREFLIGHT_ONLY=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      EXTRA_ARGS+=("$1")
      shift
      ;;
  esac
done

resolve_target_arch() {
  if [[ -n "${TARGET_ARCH}" ]]; then
    printf '%s\n' "${TARGET_ARCH}"
    return
  fi
  case "$(uname -m)" in
    arm64|aarch64) printf 'arm64\n' ;;
    *) printf 'amd64\n' ;;
  esac
}

resolve_tb_cmd() {
  if command -v tb >/dev/null 2>&1; then
    TB_CMD=(tb)
    TB_LIVENESS_CMD=(tb --help)
    return
  fi
  if command -v uv >/dev/null 2>&1; then
    TB_CMD=(uv tool run --python "${PYTHON_VERSION}" terminal-bench)
    TB_LIVENESS_CMD=(uv tool run --python "${PYTHON_VERSION}" terminal-bench --help)
    return
  fi
  die "terminal-bench is required; install 'tb' or make 'uv' available"
}

terminal_bench_version() {
  if command -v uv >/dev/null 2>&1; then
    uv tool run --python "${PYTHON_VERSION}" --with terminal-bench python - <<'PYEOF' 2>/dev/null || true
import importlib.metadata as metadata

try:
    print(metadata.version("terminal-bench"))
except metadata.PackageNotFoundError:
    pass
PYEOF
    return
  fi
  python3 - <<'PYEOF' 2>/dev/null || true
import importlib.metadata as metadata

try:
    print(metadata.version("terminal-bench"))
except metadata.PackageNotFoundError:
    pass
PYEOF
}

preflight() {
  [[ -d "${DATASET_PATH}" ]] || die "dataset path does not exist: ${DATASET_PATH}"
  command -v python3 >/dev/null 2>&1 || die "python3 is required"
  command -v docker >/dev/null 2>&1 || die "docker is required"
  docker info >/dev/null 2>&1 || die "docker daemon is not reachable"
  command -v tmux >/dev/null 2>&1 || die "tmux is required"
  resolve_tb_cmd
  if ! "${TB_LIVENESS_CMD[@]}" >/dev/null 2>&1; then
    die "terminal-bench command is not runnable"
  fi
  if [[ "${HARNESS_PROVIDER:-}" == "fake" ]]; then
    [[ -n "${HARNESS_FAKE_TURNS:-}" ]] || die "HARNESS_FAKE_TURNS is required when HARNESS_PROVIDER=fake"
    [[ -f "${HARNESS_FAKE_TURNS}" ]] || die "HARNESS_FAKE_TURNS does not exist: ${HARNESS_FAKE_TURNS}"
  elif [[ -z "${OPENAI_API_KEY:-}" ]]; then
    die "OPENAI_API_KEY is required unless HARNESS_PROVIDER=fake"
  fi
  case "$(resolve_target_arch)" in
    amd64|arm64) ;;
    *) die "HARNESS_BENCH_TARGET_ARCH must be amd64 or arm64" ;;
  esac
  info "preflight ok"
}

build_harness_binaries() {
  local arch="$1"
  local bin_dir="${OUTPUT_DIR}/go-code-bin/${arch}"
  mkdir -p "${bin_dir}"
  if [[ "${SKIP_HARNESS_BUILD}" == "true" ]]; then
    [[ -x "${bin_dir}/harnessd" && -x "${bin_dir}/harnesscli" ]] || \
      die "TERMINAL_BENCH_SKIP_HARNESS_BUILD=true but binaries are missing in ${bin_dir}"
  else
    info "building linux/${arch} harness binaries once for this campaign"
    GOOS=linux GOARCH="${arch}" CGO_ENABLED=0 \
      go build -o "${bin_dir}/harnessd" ./cmd/harnessd
    GOOS=linux GOARCH="${arch}" CGO_ENABLED=0 \
      go build -o "${bin_dir}/harnesscli" ./cmd/harnesscli
  fi
  chmod +x "${bin_dir}/harnessd" "${bin_dir}/harnesscli"
  printf '%s\n' "${bin_dir}"
}

find_results_json() {
  if [[ -f "${OUTPUT_DIR}/results.json" ]]; then
    printf '%s\n' "${OUTPUT_DIR}/results.json"
    return
  fi
  local candidate
  for candidate in "${OUTPUT_DIR}"/*/results.json; do
    if [[ -f "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  done
}

preflight
if [[ "${PREFLIGHT_ONLY}" == "true" ]]; then
  exit 0
fi

mkdir -p "${OUTPUT_DIR}"
export TERMINAL_BENCH_VERSION="$(terminal_bench_version)"
TARGET_ARCH="$(resolve_target_arch)"
export HARNESS_BENCH_TARGET_ARCH="${TARGET_ARCH}"
export HARNESS_BENCH_MODEL="${MODEL}"
export PYTHONPATH="${REPO_ROOT}${PYTHONPATH:+:${PYTHONPATH}}"
export HARNESS_BENCH_BINARY_DIR="$(build_harness_binaries "${TARGET_ARCH}")"

if [[ "${SKIP_IMAGE_BUILD}" != "true" ]]; then
  "${SCRIPT_DIR}/build-bench-images.sh"
fi

info "running tb dataset=${DATASET_PATH} model=${MODEL} concurrent=${N_CONCURRENT} attempts=${N_ATTEMPTS}"
TB_RUN_ARGS=(
  run
  --dataset-path "${DATASET_PATH}" \
  --agent-import-path "${AGENT_IMPORT_PATH}" \
  --model "${MODEL}" \
  --output-path "${OUTPUT_DIR}" \
  --n-concurrent "${N_CONCURRENT}" \
  --n-attempts "${N_ATTEMPTS}" \
  --global-agent-timeout-sec "${GLOBAL_AGENT_TIMEOUT_SEC}" \
  --global-test-timeout-sec "${GLOBAL_TEST_TIMEOUT_SEC}" \
)
if [[ ${#EXTRA_ARGS[@]} -gt 0 ]]; then
  TB_RUN_ARGS+=("${EXTRA_ARGS[@]}")
fi
"${TB_CMD[@]}" "${TB_RUN_ARGS[@]}"

info "raw results written to ${OUTPUT_DIR}"

python3 "${SCRIPT_DIR}/terminal_bench_artifacts.py" "${OUTPUT_DIR}" \
  --write-run-env \
  --model "${MODEL}" \
  --provider "${HARNESS_PROVIDER:-openai}" \
  --dataset-path "${DATASET_PATH}" \
  --n-concurrent "${N_CONCURRENT}" \
  --n-attempts "${N_ATTEMPTS}" \
  --global-agent-timeout-sec "${GLOBAL_AGENT_TIMEOUT_SEC}" \
  --global-test-timeout-sec "${GLOBAL_TEST_TIMEOUT_SEC}" \
  --cleanup "terminal-bench-default"

REPORT_PATH="${OUTPUT_DIR}/report.md"
python3 "${SCRIPT_DIR}/terminal_bench_artifacts.py" "${OUTPUT_DIR}" --report "${REPORT_PATH}"

RESULTS_JSON="$(find_results_json || true)"
if [[ -n "${RESULTS_JSON}" ]]; then
  echo ""
  echo "=============================="
  echo "  Terminal-Bench Summary"
  echo "=============================="
  echo ""
  python3 - "${RESULTS_JSON}" "${BENCH_MIN_ACCURACY}" <<'PYEOF'
import json
import sys

results_file = sys.argv[1]
min_accuracy = int(sys.argv[2])
data = json.load(open(results_file))
results = data.get("results", [])
n_resolved = data.get("n_resolved", 0)
n_total = len(results)
accuracy = data.get("accuracy", 0)

print(f"  {'Task':<35} {'Result':>8}")
print(f"  {'-'*35} {'-'*8}")
for r in sorted(results, key=lambda x: x["task_id"]):
    status = "PASS" if r.get("is_resolved") else "FAIL"
    print(f"  {r['task_id']:<35} {status:>8}")
print(f"  {'-'*35} {'-'*8}")
print(f"  {'TOTAL':<35} {n_resolved}/{n_total}")
print(f"  Accuracy: {accuracy:.1%}")
print()
sys.exit(0 if accuracy * 100 >= min_accuracy else 1)
PYEOF
  ACCURACY_OK=$?
  if [[ ${ACCURACY_OK} -ne 0 ]]; then
    die "accuracy below threshold (${BENCH_MIN_ACCURACY}%)"
  fi
  info "accuracy meets threshold (>= ${BENCH_MIN_ACCURACY}%)"
else
  die "could not find results.json for summary"
fi
