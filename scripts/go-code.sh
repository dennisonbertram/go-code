#!/usr/bin/env bash
set -euo pipefail

# go-code — Launch the harness TUI or run a single prompt from any directory.
#
# Usage:
#   go-code                  Launch the interactive TUI (default).
#   go-code "prompt"         Run a single prompt, stream output, then exit.
#   go-code --server         Start harnessd in the background and print its URL.
#   go-code --resume <id>    Resume a conversation in the interactive TUI.
#   go-code runs             List known runs.
#   go-code show <run-id>    Show one run.
#   go-code cancel <run-id>  Cancel one run.
#   go-code continue <run-id> "prompt"
#                            Continue a completed run and stream events.
#   go-code replay <run-id-or-rollout-path>
#                            Replay a recorded run.
#   go-code search <query>   Search run metadata.
#   go-code improve [--target seam]
#                            Run or plan the self-improvement test loop.
#
# Environment:
#   HARNESS_ADDR   Listen address (default :8080). Only the port is used: it
#                  constructs the BASE_URL for health checks and CLI
#                  invocations, and a wrapper-started harnessd always binds
#                  127.0.0.1 on that port. Run harnessd directly for a daemon
#                  that listens beyond this machine.

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
  go-code --resume <id>    Resume a conversation in the interactive TUI.
  go-code runs             List known runs.
  go-code show <run-id>    Show one run.
  go-code cancel <run-id>  Cancel one run.
  go-code continue <run-id> "prompt"
                           Continue a completed run and stream events.
  go-code replay <run-id-or-rollout-path>
                           Replay a recorded run.
  go-code search <query>   Search run metadata.
  go-code improve [--target seam]
                           Run or plan the self-improvement test loop.

Environment:
  HARNESS_ADDR   Override the server port (default: :8080). A wrapper-started
                 harnessd always binds 127.0.0.1 on that port.
                 Example: HARNESS_ADDR=:9090 go-code "ls *.go"

Description:
  Single-command entry point for go-code. Auto-starts harnessd
  when no server is already running. The server is only stopped on exit when
  go-code started it — a pre-existing server is always left alone.
EOF
}

# Color is an enhancement, never the only signal: the WARN:/ERROR: words stay in
# the text, so severity survives a monochrome terminal, a pipe, a captured log,
# and colorblind readers. Only the 8 standard ANSI colors are used, so each
# terminal applies its own theme and nothing turns invisible on a light
# background. Issue #1413.
#
# Support is detected once, here, and must not be tested lazily inside style():
# style() is called from command substitution, where stdout is a pipe rather
# than the terminal, so an `-t 1` check there is always false and stdout would
# never be colored. stdout and stderr are tracked separately because either can
# be redirected on its own.
COLOR_STDOUT=0
COLOR_STDERR=0
if [[ -z "${NO_COLOR:-}" && "${TERM:-}" != "dumb" ]]; then
  [[ -t 1 ]] && COLOR_STDOUT=1
  [[ -t 2 ]] && COLOR_STDERR=1
fi

# style <stream-fd> <sgr> <text> — style text only when that stream is a terminal.
style() {
  local enabled="$COLOR_STDOUT"
  [[ "$1" == "2" ]] && enabled="$COLOR_STDERR"
  if [[ "$enabled" == "1" ]]; then
    printf '\033[%sm%s\033[0m' "$2" "$3"
  else
    printf '%s' "$3"
  fi
}

# The `|| true` on each printf is load-bearing, not defensive noise: this script
# runs under `set -e`, and a write to a closed stdout (`go-code runs | head`)
# fails with EPIPE. Without the guard, a status line aborts its caller — which
# orphaned the daemon, because stop_server's first statement is an info line and
# the kill never ran. Issue #1416.
info()  { printf '%s %s\n' "$(style 1 '36' '[go-code]')" "$*" || true; }
warn()  { printf '%s %s %s\n' "$(style 2 '33' '[go-code]')" "$(style 2 '1;33' 'WARN:')" "$*" >&2 || true; }
die()   { printf '%s %s %s\n' "$(style 2 '31' '[go-code]')" "$(style 2 '1;31' 'ERROR:')" "$*" >&2 || true; show_harnessd_log; exit 1; }

# show_harnessd_log prints the captured daemon log when a wrapper-started
# harnessd failed. The daemon's stdout is redirected to a file so a healthy
# start is not buried in boot noise and no log line can scribble into the TUI
# after handoff — which means a failure has to bring that output back, or the
# operator is left with no reason at all. Lines are colored per logical line, so
# a long fatal message stays emphasized across the terminal's soft wrap.
show_harnessd_log() {
  [[ -n "${HARNESSD_LOG:-}" && -s "${HARNESSD_LOG:-}" ]] || return 0
  printf '\n  %s\n' "$(style 2 '1' 'harnessd said:')" >&2 || true
  local line
  while IFS= read -r line; do
    case "$line" in
      *fatal:*|*panic:*|*"refusing to start"*)
        printf '    %s\n' "$(style 2 '1;31' "$line")" >&2 || true ;;
      *)
        printf '    %s\n' "$(style 2 '2' "$line")" >&2 || true ;;
    esac
  done < <(tail -n 20 "$HARNESSD_LOG")
  printf '\n  %s %s\n\n' "$(style 2 '2' 'full log:')" "$HARNESSD_LOG" >&2 || true
}

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
  # Cleanup must not depend on being able to write. This runs from the EXIT
  # trap, often with stdout already closed (`go-code runs | head`), and a
  # SIGPIPE taken here kills the shell mid-trap — the daemon then survives and
  # holds the workspace lock, breaking the next go-code in the project.
  # Ignoring PIPE turns that fatal signal into an EPIPE the `|| true` in info()
  # absorbs, so the kill below always runs. Issue #1416.
  trap '' PIPE

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

# port_in_use returns 0 (success) when something is already listening on the
# given TCP port on the loopback interface. Uses lsof when available and falls
# back to a bash /dev/tcp connect probe.
port_in_use() {
  local port="$1"
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1
    return
  fi
  (exec 3<>"/dev/tcp/127.0.0.1/${port}") >/dev/null 2>&1 || return 1
  exec 3>&- 3<&- 2>/dev/null || true
  return 0
}

# find_free_port echoes the first free TCP port at or above the given start
# port, scanning up to 50 candidates. Returns non-zero if none is free.
find_free_port() {
  local port="$1"
  local tries=0
  while port_in_use "$port"; do
    port=$((port + 1))
    tries=$((tries + 1))
    if [[ $tries -ge 50 ]]; then
      return 1
    fi
  done
  echo "$port"
}

start_server() {
  local port="${1}"
  local base_url="${2}"

  info "starting harnessd on port ${port}"

  local harnessd_bin
  harnessd_bin="$(command -v harnessd)"

  # go-code is the local, interactive entry point. It uses the neutral base
  # system prompt (no intent overlay) — the strong default. The container/
  # benchmark "autonomous" overlay is opt-in and never applied here. An explicit
  # HARNESS_DEFAULT_AGENT_INTENT in the environment still passes through to
  # harnessd unchanged.
  #
  # Start in background, capturing the PID.
  # Loopback only. The wrapper talks to this daemon exclusively over
  # http://127.0.0.1:${port}, so a wildcard bind has no consumer — it would just
  # publish an unauthenticated agent-execution service to the local network, and
  # harnessd's bind guard (cmd/harnessd/bind_guard.go, issue #1328) refuses to
  # start there without auth. HARNESS_ADDR supplies the port; the host is ours.
  # Issue #1411.
  local tmpdir="${TMPDIR:-/tmp}"
  HARNESSD_LOG="${tmpdir%/}/harnessd.${$}.log"
  ( umask 077; : > "$HARNESSD_LOG" )
  HARNESS_ADDR="127.0.0.1:${port}" "$harnessd_bin" >"$HARNESSD_LOG" 2>&1 &
  local pid=$!
  PID_FILE="${TMPDIR:-/tmp}/harnessd.${$}.pid"
  echo "$pid" > "$PID_FILE"
  STARTED_BY_US=1

  # Arm cleanup the moment the daemon exists, rather than at mode dispatch
  # further below: anything that exits in between would orphan it. stop_server
  # checks STARTED_BY_US itself, so this can never touch a daemon we did not
  # start. Issue #1416.
  trap stop_server EXIT

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
      die "harnessd (pid ${pid}) exited before becoming healthy on port ${port}. If it reports the port is already in use, free it or run on another port with HARNESS_ADDR=:PORT (e.g. HARNESS_ADDR=:9090 go-code)."
    fi
  done
  info "$(style 1 '32' 'server ready') at ${base_url}"
  info "log: ${HARNESSD_LOG}"
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
  local mode="tui"   # tui | prompt | server | cli
  local prompt=""
  local cli_command=""
  local -a cli_args=()
  local resume_id=""

  # Parse arguments.
  case "${1:-}" in
    --server)
      mode="server"
      ;;
    --tui)
      mode="tui"
      ;;
    --resume)
      if [[ -z "${2:-}" ]]; then
        die "--resume requires a conversation id, e.g. go-code --resume <conversation-id>"
      fi
      mode="tui"
      resume_id="$2"
      ;;
    --help|-h|help)
      usage
      exit 0
      ;;
    runs)
      mode="cli"
      cli_command="list"
      shift
      cli_args=("$@")
      ;;
    list)
      mode="cli"
      cli_command="list"
      shift
      cli_args=("$@")
      ;;
    show)
      mode="cli"
      cli_command="status"
      shift
      cli_args=("$@")
      ;;
    status)
      mode="cli"
      cli_command="status"
      shift
      cli_args=("$@")
      ;;
    cancel)
      mode="cli"
      cli_command="cancel"
      shift
      cli_args=("$@")
      ;;
    continue)
      mode="cli"
      cli_command="continue"
      shift
      cli_args=("$@")
      ;;
    replay)
      mode="cli"
      cli_command="replay"
      shift
      cli_args=("$@")
      ;;
    search)
      mode="cli"
      cli_command="search"
      shift
      cli_args=("$@")
      ;;
    improve)
      mode="cli"
      cli_command="improve"
      shift
      cli_args=("$@")
      ;;
    "")
      mode="tui"
      ;;
    *)
      mode="prompt"
      prompt="$1"
      shift
      # Refuse rather than silently truncate. `prompt="$1"` alone discarded
      # every later positional, so the natural unquoted form —
      # `go-code explain this repo` — ran against `-prompt explain` and threw
      # the rest away with exit 0. A short prompt still produces plausible
      # output, so the loss was invisible. Joining the words instead would guess
      # at intent; refusing teaches the rule once. Issue #1435.
      if [[ $# -gt 0 ]]; then
        die "unexpected extra argument: $1. Quote the whole prompt as one argument, e.g. go-code \"${prompt} $*\""
      fi
      ;;
  esac

  require_command harnesscli \
    "Install with: ./scripts/install.sh --add-to-path"
  require_command harnessd \
    "Install with: ./scripts/install.sh --add-to-path"
  require_command curl

  configure_runtime_assets

  # Parse the port from HARNESS_ADDR (default :8080). Track whether the caller
  # set HARNESS_ADDR explicitly, so we only auto-relocate off the default port.
  local addr_explicit=0
  [[ -n "${HARNESS_ADDR:-}" ]] && addr_explicit=1
  local addr="${HARNESS_ADDR:-:8080}"
  local port="${addr##*:}"
  local base_url="http://127.0.0.1:${port}"

  # Ensure a server is reachable.
  if curl -sf "${base_url}/healthz" >/dev/null 2>&1; then
    info "server already running at ${base_url}"
  else
    # Nothing healthy answered. If the port is held by a foreign process (e.g.
    # a container runtime), harnessd cannot bind there.
    if port_in_use "$port"; then
      if [[ "$addr_explicit" -eq 1 ]]; then
        die "port ${port} is already in use by another process and is not a go-code server. Free it, or set HARNESS_ADDR to a different port (e.g. HARNESS_ADDR=:9090)."
      fi
      local free_port
      free_port="$(find_free_port "$port")" || die "port ${port} is in use and no free port was found nearby; set HARNESS_ADDR to a free port."
      warn "port ${port} is in use by another process; using free port ${free_port} instead (set HARNESS_ADDR to override)"
      port="$free_port"
      base_url="http://127.0.0.1:${port}"
    fi
    start_server "$port" "$base_url"
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
      if [[ -n "$resume_id" ]]; then
        harnesscli -base-url "$base_url" -workspace "$project_root" --tui -resume "$resume_id"
      else
        harnesscli -base-url "$base_url" -workspace "$project_root" --tui
      fi
      ;;
    prompt)
      harnesscli -base-url "$base_url" -workspace "$project_root" -prompt "$prompt"
      ;;
    cli)
      harnesscli "$cli_command" -base-url "$base_url" ${cli_args[@]+"${cli_args[@]}"}
      ;;
  esac
}

main "$@"
