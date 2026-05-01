#!/usr/bin/env bash
set -euo pipefail

# Install go-code, harnesscli, and harnessd for local use.
#
# Default install is user-local and does not require sudo:
#   ./scripts/install.sh
#
# To also add the install directory to your shell profile:
#   ./scripts/install.sh --add-to-path

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

PREFIX="${GO_CODE_PREFIX:-${PREFIX:-${HOME}/.local}}"
BIN_DIR="${GO_CODE_BINDIR:-}"
DATA_DIR="${GO_CODE_DATA_DIR:-}"
DRY_RUN=0
DO_BUILD=1
ADD_TO_PATH=0
UNINSTALL=0

usage() {
  cat <<'EOF'
Usage:
  scripts/install.sh [options]

Options:
  --prefix DIR       Install under DIR. If DIR ends in /bin, it is used as the
                     binary directory. Otherwise binaries go in DIR/bin.
                     Default: ~/.local
  --bin-dir DIR      Install directly into DIR.
  --data-dir DIR     Install prompts and catalog assets into DIR.
                     Default: sibling share/go-code directory for the bin dir.
  --system           Install into /usr/local/bin. May require sudo.
  --add-to-path      Append the install directory to your shell profile when it
                     is not already on PATH.
  --no-build         Reuse ./harnessd and ./harnesscli from the repo root.
  --uninstall        Remove go-code, harnesscli, and harnessd from the target.
  --dry-run          Print what would happen without writing files.
  -h, --help         Show this help.

Examples:
  ./scripts/install.sh
  ./scripts/install.sh --add-to-path
  ./scripts/install.sh --prefix "$HOME/.local"
  sudo ./scripts/install.sh --system
EOF
}

log() {
  printf '[install] %s\n' "$*"
}

warn() {
  printf '[install] WARN: %s\n' "$*" >&2
}

die() {
  printf '[install] ERROR: %s\n' "$*" >&2
  exit 1
}

quote_cmd() {
  local out=()
  local arg
  for arg in "$@"; do
    out+=("$(printf '%q' "$arg")")
  done
  printf '%s\n' "${out[*]}"
}

run() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf '+ %s\n' "$(quote_cmd "$@")"
    return 0
  fi
  "$@"
}

path_contains() {
  local needle="$1"
  case ":${PATH:-}:" in
    *":${needle}:"*) return 0 ;;
    *) return 1 ;;
  esac
}

default_profile() {
  local shell_name
  shell_name="$(basename "${SHELL:-}")"
  case "$shell_name" in
    zsh) echo "${HOME}/.zshrc" ;;
    bash) echo "${HOME}/.bashrc" ;;
    *) echo "${HOME}/.profile" ;;
  esac
}

infer_bin_dir() {
  if [[ -n "$BIN_DIR" ]]; then
    :
  elif [[ "$(basename "$PREFIX")" == "bin" ]]; then
    BIN_DIR="$PREFIX"
  else
    BIN_DIR="${PREFIX}/bin"
  fi
  if [[ -z "$DATA_DIR" ]]; then
    DATA_DIR="$(dirname "$BIN_DIR")/share/go-code"
  fi
}

install_one() {
  local src="$1"
  local dst="$2"
  if [[ "$DRY_RUN" -ne 1 && ! -f "$src" ]]; then
    die "missing install source: ${src}"
  fi
  run install -m 0755 "$src" "$dst"
}

install_tree() {
  local src="$1"
  local dst="$2"
  if [[ "$DRY_RUN" -ne 1 && ! -d "$src" ]]; then
    die "missing install source directory: ${src}"
  fi
  run rm -rf "$dst"
  run mkdir -p "$(dirname "$dst")"
  run cp -R "$src" "$dst"
}

append_path_to_profile() {
  local profile="${PROFILE:-$(default_profile)}"
  local line="export PATH=\"${BIN_DIR}:\$PATH\""

  if [[ -f "$profile" ]] && grep -F "$BIN_DIR" "$profile" >/dev/null 2>&1; then
    log "PATH entry already present in ${profile}"
    return 0
  fi

  log "adding ${BIN_DIR} to ${profile}"
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf '+ append to %q: %s\n' "$profile" "$line"
    return 0
  fi
  mkdir -p "$(dirname "$profile")"
  {
    printf '\n'
    printf '# Added by go-code installer\n'
    printf '%s\n' "$line"
  } >>"$profile"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix)
      [[ $# -ge 2 ]] || die "--prefix requires a directory"
      PREFIX="$2"
      shift 2
      ;;
    --prefix=*)
      PREFIX="${1#*=}"
      shift
      ;;
    --bin-dir)
      [[ $# -ge 2 ]] || die "--bin-dir requires a directory"
      BIN_DIR="$2"
      shift 2
      ;;
    --bin-dir=*)
      BIN_DIR="${1#*=}"
      shift
      ;;
    --data-dir)
      [[ $# -ge 2 ]] || die "--data-dir requires a directory"
      DATA_DIR="$2"
      shift 2
      ;;
    --data-dir=*)
      DATA_DIR="${1#*=}"
      shift
      ;;
    --system)
      PREFIX="/usr/local"
      BIN_DIR="/usr/local/bin"
      DATA_DIR="/usr/local/share/go-code"
      shift
      ;;
    --add-to-path)
      ADD_TO_PATH=1
      shift
      ;;
    --no-build)
      DO_BUILD=0
      shift
      ;;
    --uninstall)
      UNINSTALL=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

infer_bin_dir

if [[ "$UNINSTALL" -eq 1 ]]; then
  log "uninstalling from ${BIN_DIR}"
  run rm -f "${BIN_DIR}/go-code" "${BIN_DIR}/harnesscli" "${BIN_DIR}/harnessd"
  run rm -rf "${DATA_DIR}"
  log "uninstall complete"
  exit 0
fi

if [[ "$DO_BUILD" -eq 1 ]]; then
  command -v go >/dev/null 2>&1 || die "Go is required to build. Install Go, or rerun with --no-build after building ./harnesscli and ./harnessd."
  log "building harnesscli and harnessd"
  run mkdir -p "${REPO_ROOT}/build/install"
  (
    cd "$REPO_ROOT"
    run go build -o build/install/harnesscli ./cmd/harnesscli
    run go build -o build/install/harnessd ./cmd/harnessd
  )
  HARNESSCLI_SRC="${REPO_ROOT}/build/install/harnesscli"
  HARNESSD_SRC="${REPO_ROOT}/build/install/harnessd"
else
  HARNESSCLI_SRC="${REPO_ROOT}/harnesscli"
  HARNESSD_SRC="${REPO_ROOT}/harnessd"
fi

log "installing to ${BIN_DIR}"
run mkdir -p "$BIN_DIR"
install_one "$HARNESSCLI_SRC" "${BIN_DIR}/harnesscli"
install_one "$HARNESSD_SRC" "${BIN_DIR}/harnessd"
install_one "${REPO_ROOT}/scripts/go-code.sh" "${BIN_DIR}/go-code"

log "installing runtime assets to ${DATA_DIR}"
install_tree "${REPO_ROOT}/prompts" "${DATA_DIR}/prompts"
install_tree "${REPO_ROOT}/catalog" "${DATA_DIR}/catalog"

if [[ "$ADD_TO_PATH" -eq 1 ]]; then
  append_path_to_profile
fi

if path_contains "$BIN_DIR"; then
  log "installed: $(command -v go-code 2>/dev/null || printf '%s/go-code' "$BIN_DIR")"
else
  warn "${BIN_DIR} is not currently on PATH."
  printf '\nAdd it for this shell with:\n\n'
  printf '  export PATH="%s:$PATH"\n\n' "$BIN_DIR"
  printf 'For future shells, rerun:\n\n'
  printf '  %s --add-to-path\n\n' "${REPO_ROOT}/scripts/install.sh"
fi

log "done"
