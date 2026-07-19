# Plan: epic #813 slice 1 — quote-aware argument tokenizer

Parent epic: #813 (skill argument placeholders and slash-command namespacing), part of #803.
This plan covers ONLY slice 1: `feat(skills): quote-aware argument tokenizer`.

## Context

- Problem: skill argument splitting in `buildVars` (`internal/skills/hook.go`) uses
  `strings.Fields`, which is not quote-aware, so quoted multi-word arguments
  (the common case) are mangled into separate positional tokens.
- User impact: skills ported from kimi-code silently mis-expand; `SplitArgs` is the
  shared tokenizer every later slice (0-based placeholders, named args, TUI/plugin
  paths) will build on, so its API surface must stay clean and documented.
- Constraints: strict TDD per `docs/runbooks/testing.md`; minimal diff scoped to
  slice 1 only — no 0-based reindexing, no named args, no fallback append (later slices).

## Scope

- In scope:
  - New `internal/skills/argsplit.go`: `SplitArgs(s string) ([]string, error)` honoring
    single quotes, double quotes, and backslash escapes; unterminated quotes treat the
    rest of the string as part of the current token (no error path needed by callers).
  - Switch `buildVars` (`internal/skills/hook.go`) from `strings.Fields` to `SplitArgs`.
- Out of scope: 0-based `$0..$n` placeholders (slice 2), `ARGUMENTS:` append fallback
  (slice 2), frontmatter named arguments (slice 3), TUI `skill:` namespace (slice 4),
  docs migration of `$1..$9` (slice 5).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none in this slice (user-facing syntax docs land in slice 5).
- Spec docs to update before code: none (epic #813 body is the contract).
- Implementation notes to add after code: none beyond the exported `SplitArgs` doc comment.

## Test Plan (TDD)

- New failing tests to add first:
  - `internal/skills/argsplit_test.go` — table-driven: quoted multi-word tokens,
    mixed quotes, escaped quotes/spaces, empty input, unterminated quote,
    adjacent quoted+unquoted segments, empty quoted segments, acceptance case
    `SplitArgs(`run "hello world" --fast`)` → `[run hello world --fast]`.
  - Regression test in `internal/skills/hook_test.go`: `buildVars` positional vars
    (`$1..$n`) respect quotes end-to-end via `AutoInvokeHook` explicit invocation.
- Existing tests to update: none (1-based `$1..$9` indexing unchanged in this slice).
- Regression tests required: the quote-aware `buildVars` test above is the regression
  guard for the `strings.Fields` → `SplitArgs` switch.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Document feature status and exact contract before code.
- [x] Write failing tests first (watch them fail).
- [x] Implement `SplitArgs` minimal code.
- [x] Switch `buildVars` to `SplitArgs`.
- [x] gofmt + go vet clean.
- [x] `go test ./internal/skills/ -count=1` green.
- [x] Update `docs/plans/INDEX.md`.
- [ ] Push `epic/813-skill-args` and open PR against this repo (no merge).

## Risks and Mitigations

- Risk: over-engineering the tokenizer into a full shell lexer.
  - Mitigation: defined, documented semantics only (quotes, backslash escapes,
    unterminated-quote = rest-of-string token); error return reserved but currently nil.
- Risk: breaking existing positional behavior for unquoted input.
  - Mitigation: regression tests pin unquoted tokenization identical to `strings.Fields`.
