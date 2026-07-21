# Impact Map: epic #818 — clipboard image input (TUI)

## Task

- Task / issue: #818 — slice 1 (clipboard reader, PR #837), slice 2 (Ctrl-V paste + chips, PR #872), slice 3 (run plumbing + modality gating, PR #897), slice 4 (`feat(provider): encode base64 image blocks for anthropic and openai`, in implementation)
- Plan link: `docs/plans/2026-07-19-issue-818-clipboard-image-plan.md`
- Owner: agent worktree `epic/818-image-input` (s1), `epic/818-image-input-s2` (s2), `epic/818-image-input-s3` (s3), `epic/818-image-input-s4` (s4)
- Status: slice 4 in implementation

## Config

- User-facing config added or changed: None — the reader, paste UX, run plumbing, and provider encoding have no settings; platform/tool detection is automatic.
- Defaults / fallbacks: darwin → `osascript`; linux → `wl-paste` then `xclip`; headless (`TERM` unset/`dumb`) short-circuits before any subprocess. Modality checks (client pre-flight, server gate, provider refusal) all fall back to *allow* when the model's modalities are unknown (offline fetch, old server, OpenRouter-sourced list, nil registry, nil catalog/lookup on the provider client).
- Environment variables, config files, or saved settings touched: `TERM` read only (existing `IsHeadless()` semantics, unchanged).
- Migration / backward-compatibility notes: `GET /v1/models` gained additive `modalities` (slice 2); `POST /v1/runs` gained additive `attachments` (slice 3); `harness.Message` gained additive `Blocks`; `openai.Config` gains optional `ModelModalityLookup` (nil = skip refusal). Text-only messages serialize byte-identically to before slice 4 in both provider clients.

## Server API

- Endpoints, request fields, response fields, or server wiring affected:
  - `GET /v1/models`: `modalities` array (slice 2, additive).
  - `POST /v1/runs`: optional `attachments` array of `{type, media_type, data}` (base64); 400 on malformed blocks or known text-only models (slice 3). Slice 4 changes no request/response shapes.
- Provider/model resolution or registry changes: `cmd/harnessd` wires a `lookupModelModalities` closure (mirroring `lookupModelAPI`, with alias resolution) into `openai.Config.ModelModalityLookup`; the anthropic client's refusal reuses its existing catalog field. No registry behavior change.
- Error states / validation changes: provider clients return errors wrapping `provider.ErrImageModalityUnsupported` when asked to send an image block to a model the catalog marks text-only (defense in depth under the slice-3 gate; fires before any HTTP request). No HTTP-surface error change.

## TUI State

- Slash commands, overlays, selection state, routing, or status bar changes: unchanged from slices 2–3 (ctrl+v, chips, consume-on-submit). Slice 4 touches no TUI surface.
- Persisted client state or local config changes: None.
- Keyboard/navigation implications: None.

## Regression Tests

- New acceptance tests required: `go test ./internal/provider/...` — anthropic text+image block-array shape, image-only message (no empty text block), text-only string regression, refusal (typed error, zero HTTP calls), runner→wire proof via captured /v1/messages body; openai chat `text`/`image_url` parts, responses `input_text`/`input_image` parts, text-only regressions, refusal via `ModelModalityLookup` (typed error, zero HTTP calls).
- Existing tests to update: None — all slice 1–3 and existing provider tests must stay green unchanged.
- Cross-surface regressions to guard: text-only serialization identical in both clients; streaming paths unaffected (block mapping is shared); slice-3 harness/server tests unchanged and green.

## Warning Check

- No blank headings. TUI State is `None` with rationale (slice 4 is provider-internal); the remaining surface of epic #818 (ReadMediaFile/downscale) is slice 5 and mapped there.
