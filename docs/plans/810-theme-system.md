# Plan: theme system (epic #810)

## Slice status

- Slice 1 (token schema + JSON loader): **implemented**, merged via PR #833.
- Slice 2 (thread resolved theme through components): **implemented**, merged via PR #871.
- Slice 3 (/theme picker with live apply and re-scan): **implemented**, merged via PR #881.
- Slice 4 (persist theme selection, apply at startup): **implemented** (this branch).
- Slice 5 (docs + example theme): planned, not started.

## Slice 1: token schema and JSON loader

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

---

## Slice 2: thread resolved theme through components

## Context

- Problem: `Model.theme` is set at init (`model.go:357`) and never read;
  components hardcode styles — statusbar package vars
  (`dimStyle`/`boldStyle`/`warnStyle`), diffview inline `AdaptiveColor`
  literals (`view.go:60-63`), messagebubble package vars (user bg 237/fg 252,
  assistant dot color 15), spinner inline `Faint(true)`, approval overlay
  fully unstyled.
- User impact: a resolved theme (slice 1 loader) changes nothing visible.
- Constraints: default rendering must stay byte-identical for components
  whose current styles have theme-token equivalents (statusbar, diffview,
  spinner, messagebubble); the approval overlay is unstyled today, so styling
  it from the theme is a deliberate, documented default change (its tests
  assert via `strings.Contains` and stay green).

## Scope

- In scope:
  - `components/statusbar`: `Styles{Dim,Bold,Warn}` + `DefaultStyles()` +
    `SetStyles`; zero-value models fall back to defaults.
  - `components/diffview`: `Styles{Add,Remove,Hunk,Border}` + `DefaultStyles()`;
    `View.Styles`/`Model.Styles` optional pointer fields.
  - `components/spinner`: `Styles{Dim}` + `WithStyles` (immutable).
  - `components/messagebubble`: `Styles{User,AssistantDot,Title}` +
    `DefaultStyles()`; optional `Styles` on `Model`, `UserBubble`,
    `AssistantBubble`.
  - `components/tooluse`: `DiffStyles *diffview.Styles` pass-through so the
    TUI's diff rendering path is themed (tooluse's own styles stay as-is —
    out of epic scope).
  - `cmd/harnesscli/tui/theme_components.go` (new): derive component styles
    from the resolved `Theme` (zero-drift mappings; colors extracted where
    shapes differ, e.g. user-bubble fg from `roleUser` when set).
  - `model.go`: `SetTheme(Theme)` keeps the current theme and re-distributes
    styles to live components (statusbar, spinner) + stored derived styles
    for per-render constructions (bubbles via `renderMessageBubble`, diffs
    via `appendToolUseView`); re-apply after `WindowSizeMsg` statusbar
    re-creation and spinner re-creation. Foundation for slice-3 hot reload.
  - `approval.go`: render overlay chrome from theme (border token color for
    the box, primary for the tool name, warning for the action line).
- Out of scope: `/theme` picker (slice 3), persistence (slice 4), docs
  (slice 5); tooluse collapsed/expanded chrome, plan-approval overlay,
  inputarea, search highlight, and other overlays with hardcoded colors in
  `model.go` (not named in the epic scope).
- Mapping notes: diffview dashed border uses `DimStyle` (consistent with
  slice 1 mapping `SeparatorStyle`←`textDim`; the dashed rule is a separator,
  not a focusable box border) — keeps default diff snapshots byte-identical.

## Test Plan (TDD)

- New failing tests to add first:
  - statusbar: injected `Warn` color appears on warning segments; `Dim`/`Bold`
    injection; zero-value model renders with defaults.
  - diffview: `View.Styles` custom colors on add/remove/hunk/border lines;
    `Model.Styles` pass-through.
  - messagebubble: custom user fg / assistant dot / title colors render;
    defaults unchanged.
  - spinner: `WithStyles` custom color in `View` and `CompletionLine`.
  - tui (internal): `SetTheme` with a distinctive theme file (warning,
    diffAdd, roleUser, textDim, accent) restyles statusbar view, themed diff
    rendering through the tooluse funnel, user-bubble fg, spinner view, and
    approval overlay chrome; styles survive `WindowSizeMsg` statusbar
    re-creation.
- Existing tests to update: none expected (zero-drift mappings); approval
  overlay tests assert via `Contains` and must stay green.
- Regression: `go test ./cmd/harnesscli/... -count=1` green.

## Implementation Checklist

- [x] Write failing tests first, confirm red.
- [x] Implement component `Styles` injection + derivation + model wiring.
- [x] Run `go test ./cmd/harnesscli/... -count=1` green; gofmt/vet clean.
- [x] Update indexes/logs; commit, push `epic/810-theme-system-s2`, open PR #871.

## Risks and Mitigations

- Risk: default-appearance drift breaking golden snapshots. Mitigation:
  zero-drift token mappings verified by the unchanged component suites;
  `assertSameTheme`-style default tests per component.
- Risk: ephemeral components (bubbles, tool cards) constructed in many
  places miss styles. Mitigation: inject at the two render funnels
  (`renderMessageBubble`, `appendToolUseView`) rather than every call site.

---

## Slice 3: /theme picker overlay with live apply and directory re-scan

## Context

- Problem: slices 1–2 can load and apply themes, but there is no in-TUI way
  to pick one. `Model.SetTheme` exists (slice 2) but nothing calls it at
  runtime; `m.themeName` does not exist, so the UI cannot show the active
  theme name.
- User impact: users must not restart the TUI to try a theme
  (kimi-code `/theme` parity).
- Constraints: persistence to config.json is slice 4 — selection is
  in-memory only here. Directory scan must happen on every picker open.

## Scope

- In scope:
  - New `cmd/harnesscli/tui/components/themepicker/` component modeled on
    `profilepicker` (`New(entries).Open()` value-semantics pattern):
    `ThemeEntry{Name, Builtin, Active}`, navigation, `ThemeSelectedMsg`,
    view with built-in/file tags and an active marker.
  - `/theme` registration in `cmd_parser.go` beside `/profiles`;
    `executeThemeCommand` re-scans `ListThemes(dir)` on every open and
    rebuilds the picker (sessions-picker pattern).
  - Model: `themeName` field (default `"default-dark"`), `themesDir` test
    seam (empty = `DefaultThemesDir()`), `themePicker` field, overlay kind
    `"theme"` wired into escape/enter/catch-all key routing and the render
    switch (mirrors `"profiles"` at every site).
  - Select handler: `LoadTheme` + `SetTheme` (live apply, no restart) +
    status message; on loader error keep the current theme and surface the
    failure.
- Out of scope: persistence (slice 4), docs/example (slice 5), theming the
  picker overlay itself, config-panel `theme` row (slice 4 wires it to the
  active theme).

## Test Plan (TDD)

- New failing tests to add first:
  - themepicker package: navigation wraps; Enter emits `ThemeSelectedMsg`;
    Esc closes; `SetEntries` resets; view lists names/tags/active marker.
  - tui internal (`theme_picker_test.go`): `/theme` (executeThemeCommand)
    opens the overlay listing built-ins + theme files from `themesDir`;
    re-open after dropping a new file re-scans and lists it; Enter +
    `ThemeSelectedMsg` applies the theme live (statusbar restyles,
    `themeName` updates, overlay closes); malformed theme keeps the current
    theme and sets an error status message; `/theme` is registered.
- Existing tests to update: none expected.
- Regression: `go test ./cmd/harnesscli/... -count=1` green.

## Implementation Checklist

- [x] Write failing tests first, confirm red.
- [x] Implement themepicker + wiring.
- [x] Run `go test ./cmd/harnesscli/... -count=1` green; gofmt/vet clean.
- [ ] Update indexes/logs; commit, push `epic/810-theme-system-s3`, open PR.

## Risks and Mitigations

- Risk: tests touching the real `~/.config/harnesscli/themes`. Mitigation:
  `themesDir` model field overrides the dir; production leaves it empty.
- Risk: key-routing divergence between overlays. Mitigation: mirror the
  profiles overlay at the same three sites + render switch; esc/enter/j/k
  covered by tests.

---

## Slice 4: persist theme selection and apply at startup

## Context

- Problem: slice 3's `/theme` selection is in-memory only; restart loses it.
  `TUIConfig.Theme` is still display-only (`newTUIConfig` never sets it), and
  the config panel's `theme` row shows the (always-empty) startup value.
- User impact: theme choice must survive `quit` → relaunch; a deleted or
  broken theme file must not break startup.
- Constraints: reuse `cmd/harnesscli/config` `Load()`/`Save()`; silent
  fallback to default on any resolution error.

## Scope

- In scope:
  - `cmd/harnesscli/config/config.go`: `Theme string` (`json:"theme,omitempty"`).
  - `model.go`: persist the picker selection after successful apply (same
    load-mutate-save pattern as gateway/starring); resolve `cfg.Theme` at
    `New()` via the slice-1 loader with silent default fallback
    (`applyStartupTheme`); config-panel `theme` row shows `m.themeName`
    (active theme); `themesDirOrDefault()` helper dedupes dir resolution.
  - `main.go` `newTUIConfig`: load the saved theme name into `TUIConfig.Theme`.
- Out of scope: docs website + example theme (slice 5).

## Test Plan (TDD)

- New failing tests to add first:
  - config: `Theme` save/load round-trip; omitted field loads as `""`.
  - tui internal (`theme_persistence_test.go`, HOME redirected to temp dir):
    startup with saved valid theme resolves + restyles; startup with missing
    theme file renders default without failing; startup with malformed theme
    keeps `default-dark` entirely; picker selection writes config.json and a
    fresh model on the same "home" starts in that theme (relaunch
    simulation); config-panel `theme` row reflects `themeName`.
  - main: `newTUIConfig` fills `TUIConfig.Theme` from the saved config;
    empty when none saved.
- Regression: `go test ./cmd/harnesscli/... -count=1` green.

## Implementation Checklist

- [x] Write failing tests first, confirm red.
- [x] Implement Config field, save-on-select, startup resolution, panel row.
- [x] Run `go test ./cmd/harnesscli/... -count=1` green; gofmt/vet clean.
- [ ] Update indexes/logs; commit, push `epic/810-theme-system-s4`, open PR.

## Risks and Mitigations

- Risk: tests touching the real home directory. Mitigation: every new test
  redirects `HOME` via `t.Setenv`; no test writes outside temp dirs.
- Risk: startup failure from a broken saved theme. Mitigation: resolution
  error path keeps `DefaultTheme()` and `default-dark` silently, covered by
  the malformed/missing-file tests.
