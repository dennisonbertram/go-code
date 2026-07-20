# Plan: epic #818 slice 1 — read images from the system clipboard into a temp file

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
- [ ] Commit, push `epic/818-image-input`, open PR (no merge).

## Risks and Mitigations

- Risk: `osascript` output format variations (line wrapping, locale) break hex decoding.
- Mitigation: lenient parser (whitespace-stripped, prefix/suffix checked) plus a malformed-payload test; PNG magic-byte validation before writing the temp file.
- Risk: CI runs with TERM unset, making every non-headless test see `IsHeadless() == true`.
- Mitigation: tests pin TERM explicitly via `t.Setenv`.
