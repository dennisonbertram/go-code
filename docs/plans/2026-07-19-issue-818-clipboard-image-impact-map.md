# Impact Map: epic #818 — clipboard image input (TUI)

## Task

- Task / issue: #818 — slice 1 (clipboard reader, merged in PR #837) and slice 2 (`feat(tui): Ctrl-V image paste with placeholder chips in inputarea`, in implementation)
- Plan link: `docs/plans/2026-07-19-issue-818-clipboard-image-plan.md`
- Owner: agent worktree `epic/818-image-input` (slice 1), `epic/818-image-input-s2` (slice 2)
- Status: slice 2 in implementation

## Config

- User-facing config added or changed: None — the reader and paste UX have no settings; platform/tool detection is automatic.
- Defaults / fallbacks: darwin → `osascript`; linux → `wl-paste` then `xclip`; headless (`TERM` unset/`dumb`) short-circuits before any subprocess. Modality pre-flight falls back to *allow* when the model's modalities are unknown (offline fetch, pre-slice-2 server, OpenRouter-sourced list).
- Environment variables, config files, or saved settings touched: `TERM` read only (existing `IsHeadless()` semantics, unchanged).
- Migration / backward-compatibility notes: `GET /v1/models` gains an additive `modalities` array — older clients ignore it; older servers omit it, which the client treats as "unknown → allow".

## Server API

- Endpoints, request fields, response fields, or server wiring affected: slice 2 adds `modalities` (string array, `omitempty`) to `ModelResponse` on `GET /v1/models` (`internal/server/http_catalog.go`), populated from the catalog in both the provider-registry and raw-catalog branches. No request-shape, route, or auth change.
- Provider/model resolution or registry changes: None — the field is read straight off the existing catalog `Model.Modalities` (`internal/provider/catalog/types.go`).
- Error states / validation changes: None server-side. Client-side typed errors: `ErrClipboardHeadless`, `ErrClipboardUnsupported`, `ErrClipboardNoImage` (slice 1), surfaced as inline status messages; modality rejection is a client-side status message (`"<model>" does not support image input…`). Server-side modality enforcement for runs is slice 3.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: new `ctrl+v` binding (`KeyMap.PasteImage` + help-dialog Keybindings row); new `clipboardImageReadMsg`; inputarea model carries pending `Attachment` chips rendered above the prompt as `[image #N]`; status-bar hints for attach/no-image/unsupported/headless/modality rejection.
- Persisted client state or local config changes: None. Chip temp files live in `os.MkdirTemp` dirs, deleted eagerly on chip removal; no state persisted across sessions. Chips survive text submit (pending until slice 3 consumes them).
- Keyboard/navigation implications: `ctrl+v` only fires when no overlay is active; Backspace on an empty input buffer removes the most recent chip (text-present Backspace is unchanged); shell-mode Backspace-to-exit is unaffected (it binds on empty input with no chips in shell mode — chip removal takes the same path but only when chips exist).

## Regression Tests

- New acceptance tests required: `go test ./cmd/harnesscli/tui/...` — inputarea chip add/render/remove/cleanup; parent Ctrl-V happy path (stubbed reader), typed-error status mapping, modality-gate rejection (unit + public-message flow), overlay no-op; `fetchModelsCmd` modality decode; `internal/server` `/v1/models` modalities in the response.
- Existing tests to update: None — slice 1 clipboard tests and all existing TUI/server tests must stay green unchanged.
- Cross-surface regressions to guard: Backspace/history/shell-mode input behavior unchanged when no chips exist; help-dialog snapshots (Commands tab) unchanged; `/v1/models` consumers that ignore unknown JSON fields unaffected.

## Warning Check

- No blank headings. Config stays `None` with rationale; the Server API delta is deliberately additive-only in this slice; the remaining provider/model surfaces of epic #818 (run plumbing, provider encoding) are exercised by slices 3–5 and mapped there.
