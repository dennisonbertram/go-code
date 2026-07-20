# Plan: epic #818 — clipboard image input (slice 1 merged; slice 2 in implementation)

## Slice status

- Slice 1 (clipboard reader): **merged** via PR #837 (`feat(tui): read images from the system clipboard into a temp file`).
- Slice 2 (Ctrl-V paste + chips): **in implementation** on branch `epic/818-image-input-s2` — see "Slice 2" below.

## Context

- Problem: go-code has no way to get an image from the user's system clipboard into the harness. The TUI clipboard support is copy-out only (`CopyToClipboard` via OSC52 in `cmd/harnesscli/tui/clipboard.go`).
- User impact: pasting a screenshot into the TUI (epic #818, slices 2+) needs a platform clipboard-image reader that yields PNG bytes persisted to a temp file, with clean typed errors for the common failure modes.
- Constraints: slice 1 is the reader only — no inputarea wiring, no chips, no run-plumbing changes (later slices). Stdlib only. Strict TDD. Never shell out in headless mode.

## Scope

- In scope:
  - New `ReadImageFromClipboard()` in `cmd/harnesscli/tui/clipboard_image.go`: returns `ClipboardImage{Path, MediaType}`; PNG bytes written into an `os.MkdirTemp` directory.
  - Typed sentinel errors: `ErrClipboardHeadless`, `ErrClipboardUnsupported`, `ErrClipboardNoImage`.
  - Platform matrix: darwin via `osascript`; linux via `wl-paste` (Wayland) or `xclip` (X11); everything else → `ErrClipboardUnsupported`.
  - Behavior tests in `cmd/harnesscli/tui/clipboard_test.go` (external, headless path) and `cmd/harnesscli/tui/clipboard_image_internal_test.go` (hook-faked exec paths).
- Out of scope: Ctrl-V paste keybinding, chips, modality gating, run plumbing, provider encoding, ReadMediaFile tool (slices 2–5). Windows clipboard read. TIFF/other formats.

## Platform Support Matrix (Slice 1 decision record)

| Platform | Tooling | Notes |
| --- | --- | --- |
| macOS (darwin) | `osascript` | `clipboard info` probes for `«class PNGf»`; `get the clipboard as «class PNGf»` returns `«data PNGf<hex>»` which is hex-decoded in Go. |
| Linux (Wayland) | `wl-paste` | `--list-types` probes for `image/png`; `--type image/png` emits PNG bytes on stdout. |
| Linux (X11) | `xclip` | `-selection clipboard -t TARGETS -o` probes for `image/png`; `-t image/png -o` emits PNG bytes. |
| Windows / other | — | `ErrClipboardUnsupported` (explicitly out of scope for the epic). |
| Headless (`IsHeadless()`: TERM unset or `dumb`) | — | `ErrClipboardHeadless`; no subprocess is ever spawned. |

**osascript vs pbpaste on macOS:** `pbpaste` can only emit text flavors (`-Prefer` accepts just `txt`, `rtf`, `ps`); it cannot read image data off the clipboard. `osascript` is the only stdlib-adjacent macOS tool that exposes the `PNGf` clipboard class, so it is the read path; the hex payload it prints is decoded in Go so all file writing stays in-process and testable. PNG-only: TIFF-only clipboards map to `ErrClipboardNoImage` for this slice.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (no user-visible surface yet; paste UX lands in slice 2).
- Spec docs to update before code: this plan + `2026-07-19-issue-818-clipboard-image-impact-map.md`.
- Implementation notes to add after code: engineering-log entry when the slice lands.

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/tui`):
  - headless short-circuit (error is `ErrClipboardHeadless`, zero subprocess calls);
  - unsupported platform (`windows` → `ErrClipboardUnsupported`);
  - darwin: `PNGf` present → temp file contains exact PNG bytes, media type `image/png`;
  - darwin: text-only clipboard → `ErrClipboardNoImage`;
  - darwin: `osascript` missing → `ErrClipboardUnsupported`; malformed `«data PNGf…»` payload → error;
  - linux: `wl-paste` happy path + no-image path; `xclip` happy path; neither tool installed → `ErrClipboardUnsupported`.
- Existing tests to update: none.
- Regression tests required: acceptance `go test ./cmd/harnesscli/tui/ -run Clipboard` green; real-machine macOS smoke if the clipboard already holds an image.

## Cross-Surface Impact Map

- See `docs/plans/2026-07-19-issue-818-clipboard-image-impact-map.md` (required: epic touches provider/model flows). Summary: slice 1 touches no config, server, or provider surface; gating lands in slices 2–3.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] One-page impact map before implementation.
- [x] Write failing tests first, watch them fail (compile errors on the missing API, then red-to-green).
- [x] Implement minimal code changes.
- [x] gofmt + go vet clean.
- [x] Run package tests (`go test ./cmd/harnesscli/tui/... -count=1` green); regression script run before commit.
- [x] Update docs indexes; engineering-log entry.
- [x] Commit, push `epic/818-image-input`, open PR (no merge). → PR #837, merged.

## Risks and Mitigations

- Risk: `osascript` output format variations (line wrapping, locale) break hex decoding.
- Mitigation: lenient parser (whitespace-stripped, prefix/suffix checked) plus a malformed-payload test; PNG magic-byte validation before writing the temp file.
- Risk: CI runs with TERM unset, making every non-headless test see `IsHeadless() == true`.
- Mitigation: tests pin TERM explicitly via `t.Setenv`.

---

# Slice 2: Ctrl-V image paste with placeholder chips in inputarea

## Context

- Problem: the slice-1 clipboard reader has no user-facing entry point; there is no way to attach an image to a prompt.
- User impact: Ctrl-V attaches the clipboard image as a visible, removable placeholder chip (`[image #1]`) in the input box; clear inline errors when the clipboard has no image, the platform is unsupported, or the selected model is text-only.
- Constraints: no run-plumbing changes (attachments are NOT sent to the server in this slice — that is slice 3). Strict TDD.

## Scope

- In scope:
  - `inputarea`: `Attachment{Path, MediaType}` list on the model; `AddAttachment`/`Attachments`; chip row rendered above the prompt (`[image #N]`, contiguous numbering); Backspace on an empty buffer removes the last chip and deletes its temp-file directory (test seam `removeAttachmentFiles`); text-present Backspace unchanged.
  - Parent `tui` model: `PasteImage` key binding (`ctrl+v`, added to `KeyMap` + help dialog row); Ctrl-V handler gated on `!overlayActive`; async clipboard read via `pasteImageCmd` (seam `readClipboardImage = ReadImageFromClipboard`); `clipboardImageReadMsg` handling — success adds the chip, failure maps the slice-1 typed errors to inline status messages.
  - Modality pre-flight: `/v1/models` response (`ModelResponse` + `ServerModelEntry`) gains additive `modalities`; the parent stores fetched entries (`m.serverModels`) and rejects the paste with a clear status when the effective model is known to lack `image`. Unknown model/modalities (offline, old server, OpenRouter-sourced list) → allow; slice 3's server gate enforces at send time.
  - Chips survive text submit (still "pending"); slice 3 consumes them into the run request.
- Out of scope: sending attachments in runs (slice 3), provider encoding (slice 4), ReadMediaFile (slice 5), chip removal of a specific chip (only last-chip Backspace), quit-time temp-dir cleanup (OS `/tmp` hygiene; removal path deletes eagerly).

## Test Plan (TDD)

- New failing tests first:
  - `inputarea` (internal): add chip → `Attachments()` + View shows `[image #1]`; two chips number contiguously; Backspace with text deletes text only; Backspace on empty removes last chip + cleanup seam called with its path; Backspace empty/no-chips is a no-op.
  - `tui` internal: gate unit tests on the bare model (image model → nil; text-only → error naming the model; unknown model/modalities → nil); stubbed `readClipboardImage` happy path (Ctrl-V → cmd → msg → chip attached + hint status); typed-error → status text mapping; gate rejection fires before any read (call counter); Ctrl-V with overlay active is a no-op; Backspace routes to chip removal through the parent.
  - `tui` external: public-message flow — `ModelsFetchedMsg` (text-only entry with `modalities:["text"]`) + `ModelSelectedMsg` + Ctrl-V → rejection status, no chip in `View()`.
  - `tui` external: `fetchModelsCmd` decodes `modalities` from `/v1/models` JSON.
  - `internal/server`: `/v1/models` entries include catalog `modalities` (both registry and catalog branches where reachable).
- Existing tests to update: none expected (help snapshots show only the Commands tab; new keybinding row lands on the Keybindings tab).
- Acceptance: `go test ./cmd/harnesscli/tui/...` green; manual: paste a screenshot, see `[image #1]`, remove it, re-paste, send with a text prompt.

## Cross-Surface Impact Map

- Updated in `docs/plans/2026-07-19-issue-818-clipboard-image-impact-map.md` — slice 2 adds an additive `modalities` field to `GET /v1/models` (Server API) and a Ctrl-V binding + chip state (TUI State).

## Implementation Checklist

- [x] Slice-2 failing tests first, watch them fail (compile errors on the new API surface, then red-to-green).
- [x] inputarea attachment chips + removal + cleanup seam.
- [x] Parent Ctrl-V binding, async paste cmd, error mapping.
- [x] `/v1/models` modalities + client decode + pre-flight gate.
- [x] Help dialog keybinding row.
- [x] Regression: chips survive WindowSizeMsg input re-creation (bug found in implementation; regression test added).
- [x] gofmt + go vet clean; touched-package tests green; regression run.
- [x] Docs/indexes/engineering log updated.
- [ ] Commit, push `epic/818-image-input-s2`, open PR (no merge).
