#!/usr/bin/env bash
set -euo pipefail

# go-code — Launch the harness TUI or run a single prompt from any directory.
#
# Usage:
#   go-code                  Launch the interactive TUI (default).
#   go-code "prompt"         Run a single prompt, stream output, then exit.
#   go-code --server         Start harnessd in the background and print its URL.
#
# Environment:
#   HARNESS_ADDR   Listen address (default :8080). The port is extracted and
#                  used to construct the BASE_URL for health checks and CLI
#                  invocations.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR=""

# --- helpers -----------------------------------------------------------------

usage() {
  cat <<'EOF'
Usage:
  go-code                  Launch the interactive TUI (default).
  go-code "your prompt"    Run a single prompt, stream output, then exit.
  go-code --server         Start harnessd in the background, print the URL,
                           and exit. The server keeps running until killed.

Environment:
  HARNESS_ADDR   Override the server address (default: :8080).
                 Example: HARNESS_ADDR=:9090 go-code "ls *.go"

Description:
  Single-command entry point for go-code. Auto-starts harnessd
  when no server is already running. The server is only stopped on exit when
  go-code started it — a pre-existing server is always left alone.
EOF
}

info()  { printf '[go-code] %s\n' "$*"; }
warn()  { printf '[go-code] WARN: %s\n' "$*" >&2; }
die()   { printf '[go-code] ERROR: %s\n' "$*" >&2; exit 1; }

require_command() {
  local cmd="$1"
  local hint="${2:-}"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    if [[ -n "$hint" ]]; then
      die "required command not found: ${cmd}. ${hint}"
    fi
    die "required command not found: ${cmd}"
  fi
}

find_data_dir() {
  local candidates=()
  if [[ -n "${GO_CODE_DATA_DIR:-}" ]]; then
    candidates+=("${GO_CODE_DATA_DIR}")
  fi
  candidates+=(
    "${SCRIPT_DIR}/../share/go-code"
    "${SCRIPT_DIR}/.."
    "${SCRIPT_DIR}/../.."
  )

  local candidate
  for candidate in "${candidates[@]}"; do
    if [[ -f "${candidate}/prompts/catalog.yaml" && -f "${candidate}/catalog/models.json" ]]; then
      (cd "$candidate" && pwd)
      return 0
    fi
  done
  return 1
}

configure_runtime_assets() {
  if DATA_DIR="$(find_data_dir 2>/dev/null)"; then
    export HARNESS_PROMPTS_DIR="${HARNESS_PROMPTS_DIR:-${DATA_DIR}/prompts}"
    export HARNESS_MODEL_CATALOG_PATH="${HARNESS_MODEL_CATALOG_PATH:-${DATA_DIR}/catalog/models.json}"
  else
    warn "could not find installed prompts/catalog assets; set GO_CODE_DATA_DIR or HARNESS_PROMPTS_DIR/HARNESS_MODEL_CATALOG_PATH"
  fi
}

# --- server lifecycle --------------------------------------------------------

# PID_FILE is set only when we start the server ourselves.
PID_FILE=""
STARTED_BY_US=0

stop_server() {
  if [[ "$STARTED_BY_US" -ne 1 ]]; then
    return 0
  fi
  if [[ -z "$PID_FILE" ]] || [[ ! -f "$PID_FILE" ]]; then
    return 0
  fi

  local pid
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [[ -z "$pid" ]]; then
    rm -f "$PID_FILE"
    return 0
  fi

  # Stale-PID guard: only signal a process that is actually running.
  if ! kill -0 "$pid" 2>/dev/null; then
    rm -f "$PID_FILE"
    return 0
  fi

  info "stopping harnessd (pid ${pid})"
  kill "$pid" 2>/dev/null || true

  # Wait briefly for graceful shutdown, then force-kill if needed.
  local waited=0
  while kill -0 "$pid" 2>/dev/null && [[ $waited -lt 25 ]]; do
    sleep 0.2
    waited=$((waited + 1))
  done
  if kill -0 "$pid" 2>/dev/null; then
    warn "harnessd did not shut down gracefully; force-killing"
    kill -9 "$pid" 2>/dev/null || true
  fi

  rm -f "$PID_FILE"
}

start_server() {
  local port="${1}"
  local base_url="${2}"

  info "no server at ${base_url}, starting harnessd on port ${port}"

  local harnessd_bin
  harnessd_bin="$(command -v harnessd)"

  # Start in background, capturing the PID.
  HARNESS_ADDR=":${port}" "$harnessd_bin" &
  local pid=$!
  PID_FILE="${TMPDIR:-/tmp}/harnessd.${$}.pid"
  echo "$pid" > "$PID_FILE"
  STARTED_BY_US=1

  # Wait up to 10 s for /healthz to return 200.
  info "waiting for server to become healthy (pid ${pid})..."
  local waited=0
  while ! curl -sf "${base_url}/healthz" >/dev/null 2>&1; do
    sleep 0.5
    waited=$((waited + 1))
    if [[ $waited -ge 20 ]]; then
      die "server did not become healthy within 10 s"
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      die "harnessd process (pid ${pid}) died before becoming healthy"
    fi
  done
  info "server is ready"
}

# --- project-root detection --------------------------------------------------

find_project_root() {
  local dir
  dir="$PWD"
  while true; do
    if [[ -d "${dir}/.git" ]] || [[ -f "${dir}/.harness/config.toml" ]]; then
      echo "$dir"
      return 0
    fi
    local parent
    parent="$(dirname "$dir")"
    if [[ "$parent" == "$dir" ]]; then
      break
    fi
    dir="$parent"
  done
  echo "$PWD"
}

# --- main --------------------------------------------------------------------

main() {
  local mode="tui"   # tui | prompt | server
  local prompt=""

  # Parse arguments.
  case "${1:-}" in
    --server)
      mode="server"
      ;;
    --tui)
      mode="tui"
      ;;
    --help|-h|help)
      usage
      exit 0
      ;;
    "")
      mode="tui"
      ;;
    *)
      mode="prompt"
      prompt="$1"
      ;;
  esac

  require_command harnesscli \
    "Install with: ./scripts/install.sh --add-to-path"
  require_command harnessd \
    "Install with: ./scripts/install.sh --add-to-path"
  require_command curl

  configure_runtime_assets

  # Parse the port from HARNESS_ADDR (default :8080).
  local addr="${HARNESS_ADDR:-:8080}"
  local port="${addr##*:}"
  local base_url="http://127.0.0.1:${port}"

  # Ensure a server is reachable.
  if ! curl -sf "${base_url}/healthz" >/dev/null 2>&1; then
    start_server "$port" "$base_url"
  else
    info "server already running at ${base_url}"
  fi

  # Resolve the project root for -workspace.
  local project_root
  project_root="$(find_project_root)"
  info "project root: ${project_root}"

  case "$mode" in
    server)
      # Daemon mode: leave the server running, print the URL, and exit.
      if [[ "$STARTED_BY_US" -eq 1 ]]; then
        trap - EXIT  # Do NOT stop the server on exit.
        info "server running at ${base_url} (pid $(cat "$PID_FILE"))"
      fi
      echo "${base_url}"
      ;;
    tui)
      # Only stop what we started.
      if [[ "$STARTED_BY_US" -eq 1 ]]; then
        trap stop_server EXIT
      fi
      harnesscli -base-url "$base_url" -workspace "$project_root" --tui
      ;;
    prompt)
      if [[ "$STARTED_BY_US" -eq 1 ]]; then
        trap stop_server EXIT
      fi
      harnesscli -base-url "$base_url" -workspace "$project_root" -prompt "$prompt"
      ;;
  esac
}

main "$@"
