# Impact Map: epic #818 — clipboard image input (TUI)

## Task

- Task / issue: #818 — slice 1 (clipboard reader, PR #837), slice 2 (Ctrl-V paste + chips, PR #872), slice 3 (`feat(harness): image attachments through the run plumbing with modality gating`, in implementation)
- Plan link: `docs/plans/2026-07-19-issue-818-clipboard-image-plan.md`
- Owner: agent worktree `epic/818-image-input` (s1), `epic/818-image-input-s2` (s2), `epic/818-image-input-s3` (s3)
- Status: slice 3 in implementation

## Config

- User-facing config added or changed: None — the reader, paste UX, and run plumbing have no settings; platform/tool detection is automatic.
- Defaults / fallbacks: darwin → `osascript`; linux → `wl-paste` then `xclip`; headless (`TERM` unset/`dumb`) short-circuits before any subprocess. Modality pre-flight (client and server) falls back to *allow* when the model's modalities are unknown (offline fetch, old server, OpenRouter-sourced list, nil provider registry, discovered models).
- Environment variables, config files, or saved settings touched: `TERM` read only (existing `IsHeadless()` semantics, unchanged).
- Migration / backward-compatibility notes: `GET /v1/models` gained additive `modalities` (slice 2); `POST /v1/runs` gains additive `attachments` (omitted/ignored by older clients and servers). `harness.Message` gains additive `Blocks`; `Content` is untouched, so text-only callers and stored JSON are unchanged. Image blocks are in-memory only — the text snapshot/history path does not persist them (documented limitation).

## Server API

- Endpoints, request fields, response fields, or server wiring affected:
  - `GET /v1/models`: `modalities` array (slice 2, additive).
  - `POST /v1/runs`: new optional `attachments` array of `{type, media_type, data}` (base64) on the run request (slice 3). Decoded through `harness.RunRequest` — no handler change.
- Provider/model resolution or registry changes: None — the modality gate reads the existing catalog via the runner's `providerRegistry` (`Catalog().ModelInfo`).
- Error states / validation changes: `POST /v1/runs` now returns HTTP 400 `invalid_request` when (a) an attachment is malformed (`type` != "image", media type not `image/png`/`image/jpeg`, empty or invalid base64) or (b) the effective model's catalog entry is known to lack the `image` modality (message names model + provider). No other request validation changed.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: `ctrl+v` binding and chip row (slice 2); slice 3 consumes pending chips on submit — the images are base64-encoded into the run request, chips and temp dirs are cleared; an attachment encode failure aborts the submit, restores the text, and keeps the chips; `startRunCmd` surfaces the server's error body so the 400 modality message reaches the status bar.
- Persisted client state or local config changes: None.
- Keyboard/navigation implications: unchanged from slice 2 (Backspace-on-empty removes chips; ctrl+v gated to no-overlay).

## Regression Tests

- New acceptance tests required: `go test ./internal/harness/ ./internal/server/ -run 'Image|Attachment|Modality'` — blocks flow to `fakeprovider.LastRequest()`; malformed-attachment matrix; modality gate (reject/allow matrix); `Message.Clone` Blocks independence; HTTP 400/202 matrix; TUI submit encodes chips into the POST body and consumes them.
- Existing tests to update: None — all slice 1–2 and existing harness/server/TUI tests must stay green unchanged.
- Cross-surface regressions to guard: text-only runs serialize identically (Blocks/attachments `omitempty`); run-request JSON without attachments unchanged; chip behavior when no chips exist unchanged.

## Warning Check

- No blank headings. Config stays `None` with rationale; provider-side *encoding* of image blocks to Anthropic/OpenAI wire shapes is slice 4 and mapped there; ReadMediaFile/downscale is slice 5.

