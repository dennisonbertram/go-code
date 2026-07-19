# Impact Map: epic #818 slice 1 — clipboard image reader (TUI)

## Task

- Task / issue: #818 slice 1 — `feat(tui): read images from the system clipboard into a temp file`
- Plan link: `docs/plans/2026-07-19-issue-818-clipboard-image-plan.md`
- Owner: agent worktree `epic/818-image-input`
- Status: in implementation

## Config

- User-facing config added or changed: None — the reader has no settings; platform/tool detection is automatic.
- Defaults / fallbacks: darwin → `osascript`; linux → `wl-paste` then `xclip`; headless (`TERM` unset/`dumb`) short-circuits before any subprocess.
- Environment variables, config files, or saved settings touched: `TERM` read only (existing `IsHeadless()` semantics, unchanged).
- Migration / backward-compatibility notes: None — additive API, no existing call sites.

## Server API

- Endpoints, request fields, response fields, or server wiring affected: None in slice 1. Attachment transport over `POST /v1/runs` is slice 3.
- Provider/model resolution or registry changes: None in slice 1. Catalog modality gating (`FilterOptions.Modality`, `internal/provider/catalog/query.go`) is consumed in slices 2–3; this slice only records the decision that gating is required before attach/send.
- Error states / validation changes: None server-side. Client-side typed errors: `ErrClipboardHeadless`, `ErrClipboardUnsupported`, `ErrClipboardNoImage`.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: None in slice 1 — no keybinding or inputarea wiring (slice 2).
- Persisted client state or local config changes: None. Pasted images land in `os.MkdirTemp` directories owned by the caller; no state persisted.
- Keyboard/navigation implications: None yet; Ctrl-V paste is slice 2.

## Regression Tests

- New acceptance tests required: `go test ./cmd/harnesscli/tui/ -run Clipboard` covering headless short-circuit (no subprocess), unsupported platform, darwin/linux happy paths (exact PNG bytes in temp file), no-image and tool-missing error paths.
- Existing tests to update: None — `clipboard.go` copy-out behavior untouched.
- Cross-surface regressions to guard: `CopyToClipboard`/`IsHeadless` behavior unchanged (existing `TestTUI028_*` tests must stay green); no new dependencies (stdlib only).

## Warning Check

- No blank headings: server/config surfaces are `None` with rationale because slice 1 is a pure client-side reader; the provider/model surfaces of epic #818 are exercised by slices 2–5 and mapped there.
