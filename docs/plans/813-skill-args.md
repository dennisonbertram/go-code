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
- [x] Push `epic/813-skill-args` and open PR against this repo (no merge). — PR #825, merged.

## Risks and Mitigations

- Risk: over-engineering the tokenizer into a full shell lexer.
  - Mitigation: defined, documented semantics only (quotes, backslash escapes,
    unterminated-quote = rest-of-string token); error return reserved but currently nil.
- Risk: breaking existing positional behavior for unquoted input.
  - Mitigation: regression tests pin unquoted tokenization identical to `strings.Fields`.

---

# Plan: epic #813 slice 2 — $0..$n positional placeholders and ARGUMENTS: append fallback

This section covers ONLY slice 2 (branch `epic/813-skill-args-s2`).

## Context

- Problem: positional expansion is fixed to 1-based `$1..$9`; kimi parity requires
  0-based `$0..$n` (explicit breaking decision in epic #813), and skills whose body
  references no argument placeholder silently drop the caller's args.
- User impact: skills ported from kimi-code expand the wrong tokens; placeholder-less
  skills (the majority) lose their arguments entirely.
- Constraints: strict TDD; scoped to `internal/skills` only; named-argument detection
  in the fallback is a slice 3 extension point (noted in code, not implemented).

## Scope

- In scope:
  - `internal/skills/interpolate.go`: replace the fixed `$1..$9` reverse loop with
    arbitrary `$N` expansion (full digit run matched, so `$10` never collides with
    `$1`); unset positions expand empty.
  - `internal/skills/hook.go` `buildVars`: populate `$0..$n` (0-based) from
    `SplitArgs` tokens; more than nine tokens now supported.
  - ARGUMENTS fallback shared by both invocation paths (`AutoInvokeHook` and
    `Resolver.ResolveSkill`): when raw args are non-empty and the body references no
    argument placeholder (`$ARGUMENTS` or any `$N`), append `\nARGUMENTS: <raw args>`.
  - Migrate existing 1-based tests to the 0-based contract (sanctioned break).
- Out of scope: named frontmatter arguments (slice 3), TUI `skill:` namespace
  (slice 4), user-facing docs + in-repo SKILL.md migration (slice 5).

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (slice 5 documents the syntax and the breaking change).
- Known hazard to document in slice 5: literal prices like `$0.50` in a skill body
  now expand `$0`; authors must avoid `$<digit>` literals.

## Test Plan (TDD)

- New failing tests first:
  - `interpolate_test.go`: `$0` expansion; `$10`/`$12` longest-run (no `$1`
    collision); out-of-range `$N` expands empty; named vars unchanged.
  - `hook_test.go`: `buildVars` populates `$0..$n` including >9 tokens; quoted
    tokens land in `$0`/`$1`/`$2`.
  - Fallback: resolver + hook tests — placeholder-less body with args ends with
    `ARGUMENTS: <raw args>` (raw, untokenized, quotes preserved); bodies with
    `$ARGUMENTS` or `$N` never get the append; empty args never append.
- Existing tests migrated to 0-based: `TestAutoInvokeHook_ExplicitWithArgs`,
  `TestBuildVars`, `TestBuildVars_QuotedArgs`, `TestBuildVars_NoArgs`,
  `TestAutoInvokeHook_ExplicitWithQuotedArgs`, resolver happy-path/many-args tests.
- Regression guards: fallback-off conditions (placeholder present) pinned by tests.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first (watch them fail).
- [x] Implement arbitrary `$N` in `Interpolate`.
- [x] Implement `$0..$n` in `buildVars` + shared fallback helper used by hook and resolver.
- [x] gofmt + go vet clean.
- [x] `go test ./internal/skills/... ./internal/harness/tools/ -count=1` green.
- [x] Update `docs/plans/INDEX.md` (description unchanged; no new file).
- [x] Push `epic/813-skill-args-s2` and open PR (no merge). — PR #862, merged.

## Risks and Mitigations

- Risk: `$N` expansion hits literal dollar-digit text in bodies (e.g. prices).
  - Mitigation: inherent to the epic's sanctioned 0-based decision; flagged for the
    slice 5 docs; regex matches only `$<digits>`, never `$<letter>`.
- Risk: fallback fires when it should not (body uses placeholder via computed string).
  - Mitigation: detection is on the raw body; placeholder-present cases pinned by tests.

---

# Plan: epic #813 slice 3 — named arguments from frontmatter arguments field

This section covers ONLY slice 3 (branch `epic/813-skill-args-s3`).

## Context

- Problem: skills can only reference arguments positionally; kimi-code skills let
  authors declare named arguments in SKILL.md frontmatter and reference `$<name>`.
- User impact: multi-argument skills are unreadable (`$0 $1 $2` vs `$target $env`);
  skills ported from kimi-code fail to expand declared names.
- Constraints: strict TDD; scoped to `internal/skills`, the `SkillInfo` struct, and
  the harnessd adapter; no default values or required-arg enforcement (epic defers).

## Scope

- In scope:
  - `internal/skills/types.go`: `Arguments []string` on `Skill`; `arguments` yaml key
    on the `frontmatter` struct.
  - `internal/skills/loader.go`: validate declared names — identifier shape
    `[A-Za-z_][A-Za-z0-9_]*`, reject reserved `ARGUMENTS`/`WORKSPACE`/`SKILL_DIR`,
    reject numeric names, reject duplicates — with a clear load error naming the
    offending entry.
  - `internal/skills/interpolate.go`: expand `$<declared-name>` via a maximal
    identifier-run match looked up in vars; unknown `$identifier` text (e.g. shell
    `$HOME`) stays literal; `hasArgPlaceholder` now recognizes declared names.
  - `internal/skills/hook.go` `buildVars`: bind declared names to tokens in
    declaration order; unbound names expand empty.
  - `internal/harness/tools/types.go` + `cmd/harnessd/main.go`: surface `Arguments`
    on `SkillInfo` (`json:"arguments,omitempty"`) via `skillListerAdapter`.
- Out of scope: TUI `skill:` namespace (slice 4), user docs (slice 5), default
  values / required enforcement.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: none (slice 5 documents the `arguments` field).

## Test Plan (TDD)

- New failing tests first:
  - `loader_test.go`: valid `arguments` parse; invalid identifier, numeric name,
    reserved name, duplicate — each fails load naming the offender.
  - `hook_test.go`: `buildVars` named binding order + unbound-empty; acceptance
    `/deploy prod eu` → `$target`=prod `$env`=eu; quoted token binds one name;
    named ref suppresses the ARGUMENTS fallback; undeclared `$HOME` stays literal.
  - `resolver_test.go`: full-load acceptance for `arguments: [target, env]`.
  - `interpolate_test.go`: declared name expands; undeclared `$foobar` literal.
  - `cmd/harnessd/main_test.go`: adapter surfaces `Arguments` on `SkillInfo`.
- Regression guards: fallback still fires when body has no placeholder even if the
  skill declares arguments; `$ARGUMENTS`/`$WORKSPACE`/`$SKILL_DIR` behavior pinned
  by existing tests.

## Implementation Checklist

- [x] Define acceptance criteria in tests.
- [x] Write failing tests first (watch them fail).
- [x] Implement types + loader validation.
- [x] Implement named expansion + binding + adapter surfacing.
- [x] gofmt + go vet clean.
- [x] `go test ./internal/skills/... ./internal/harness/tools/ ./cmd/harnessd/ -count=1` green.
- [ ] Push `epic/813-skill-args-s3` and open PR (no merge).

## Risks and Mitigations

- Risk: naive named replacement mangles shell snippets (`$HOME`, `$PATH`) in bodies.
  - Mitigation: identifier-run regex + vars-membership lookup; unknown identifiers
    stay literal; pinned by test.
- Risk: `$env` clobbering `$env2` via prefix replacement.
  - Mitigation: maximal identifier run matched as one placeholder; pinned by test.
