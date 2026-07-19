# Plan: theme token schema and JSON loader (epic #810, slice 1)

## Context

- Problem: the TUI has exactly one hardcoded theme (`DefaultTheme()` in
  `cmd/harnesscli/tui/theme.go`); there is no token schema, no JSON theme
  files, and `TUIConfig.Theme` is display-only.
- User impact: users cannot restyle the TUI or share theme files (kimi-code
  parity gap).
- Constraints: strict TDD; slice 1 only — schema + loader + fallback, no
  component re-wiring, no picker, no persistence (later slices). Default
  rendering must remain byte-identical when no theme file is present.

## Scope

- In scope:
  - `cmd/harnesscli/tui/themes.go`: `TokenSet` schema (~17 tokens, JSON
    camelCase, string or `{light,dark}` adaptive form), color validation,
    `Load(dir, name)`, `List(dir)`, `DefaultThemesDir()`, built-in base
    themes `default-dark`/`default-light`, token→`Theme` style application
    with per-token and per-side fallback onto `DefaultTheme()` values.
  - `cmd/harnesscli/tui/themes_test.go`: behavior tests first.
- Out of scope: threading themes through components (slice 2), `/theme`
  picker (slice 3), config persistence (slice 4), docs website + example
  theme file (slice 5). No changes to `theme.go` itself.

## Documentation Contract

- Feature status: `in implementation` (slice 1 of 5).
- Public docs affected: none (website docs land in slice 5 per epic).
- Spec docs to update before code: this plan; `docs/plans/INDEX.md` entry.
- Implementation notes to add after code: engineering-log entry.

## Test Plan (TDD)

- New failing tests to add first (`themes_test.go`, package `tui_test`):
  - Load of missing file returns the base palette (== `DefaultTheme()`), no error.
  - Built-in names `default-dark`/`default-light` resolve to `DefaultTheme()`.
  - Partial JSON: set tokens applied, omitted tokens fall back individually.
  - Invalid color strings fall back per-token without erroring the load.
  - Adaptive `{light,dark}` objects fall back per-side; plain strings apply to both.
  - Malformed JSON returns an error and the base palette.
  - ANSI-256 numeric colors (`"196"`) accepted; junk rejected.
  - `List` returns built-ins + filename-derived names, files sorted; missing
    dir returns built-ins only.
  - Token→style mapping covers every `Theme` field (full distinctive theme
    changes every style's resolved color; verified via lipgloss getters).
  - Path-traversal names (`../x`) rejected with error + base palette.
- Existing tests to update: none.
- Regression tests required: existing `go test ./cmd/harnesscli/tui/...`
  must stay green unchanged (proves zero default-appearance drift).

## Cross-Surface Impact Map

- Not required: no provider/model flow, gateway routing, model catalog,
  API-key, or server/TUI provider plumbing is touched.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code (this file).
- [x] Write failing tests first and confirm red (compile error: undefined symbols).
- [x] Implement minimal code (`themes.go`).
- [x] Run `go test ./cmd/harnesscli/tui/... -count=1` green.
- [x] `gofmt` + `go vet` clean.
- [x] Update `docs/plans/INDEX.md`, engineering-log entry.
- [ ] Commit, push `epic/810-theme-system`, open PR (no merge).

## Risks and Mitigations

- Risk: changing `DefaultTheme()` appearance by accident. Mitigation:
  `theme.go` untouched; loader overlays resolved tokens onto a
  `DefaultTheme()` copy only for explicitly valid token values.
- Risk: reflection-heavy mapping drifts from the `Theme` struct. Mitigation:
  coverage test reflects over all `Theme` fields and asserts each is bound
  to a token, so adding a field without a binding fails the test.
