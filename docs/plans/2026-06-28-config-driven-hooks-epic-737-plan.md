# Plan: Config-driven lifecycle hooks (Epic #737)

## Context

- Problem: The harness has a complete Go-level hook mechanism (`PreMessageHook`, `PostMessageHook`, `PreToolUseHook`, `PostToolUseHook` in `internal/harness/types.go`), but registering a hook requires writing a Go package and recompiling (see `plugins/conclusion-watcher/`). End users have no config-file, shell, or HTTP path.
- User impact: Users cannot block dangerous tool calls (`rm -rf`) from a shell script, POST tool results to an audit service, or see which hooks loaded — without Go development.
- Constraints:
  - No changes to the four hook interface signatures — they already carry events, deny decisions, and mutation.
  - No parallel hook system: config-driven hooks append to existing `RunnerConfig` hook slices (conclusion-watcher pattern, `cmd/harnessd/main.go:794-809`).
  - Project-level hook files require explicit trust keyed by content hash; trust state lives in the user-global dir (a project must not trust itself).
  - Fail-closed by default via existing `HookFailureMode`; adapters return errors only, never reimplement failure policy.
  - Per-hook timeout bounds every subprocess/HTTP call.
  - Strict TDD per slice; each slice committed separately on one branch.

## Scope

- In scope (child issues, implemented in dependency order):
  - #741 — `internal/hooks` schema + loader + `[hooks]` TOML section.
  - #744 — command-exec adapter for `pre_tool_use`/`post_tool_use`.
  - #750 — HTTP adapter + `pre_message`/`post_message` for both adapters.
  - #755 — trust model for project-level hook files + `harnesscli hooks trust|revoke|list`.
  - #759 — harnessd startup wiring + structured observability + stored summary.
  - #763 — `GET /v1/hooks` route + TUI `/hooks` command.
- Out of scope: SessionStart/Stop events (no runner call sites exist), hook retries/auth/mTLS, TUI interactive trust flows, hot-reload of hook files, hook sandboxing, mutation of message requests/responses via config hooks.

## Key Code Facts (discovered during recon)

- Hook interfaces: `internal/harness/types.go:875-1001`. `PreToolUseResult{Decision, Reason, ModifiedArgs}`; `PostToolUseResult{ModifiedResult}`; message results carry `Action` (`HookActionContinue`/`HookActionBlock`) + `Reason`.
- Runner call sites: `applyPreHooks` (runner.go:2194), `applyPostHooks` (:2253), `applyPreToolUseHooks` (:2330), `applyPostToolUseHooks` (:2473). All honor `RunnerConfig.HookFailureMode` (default fail_closed, :323).
- SSE finding for #759: the runner already emits `hook.started`/`hook.completed`/`hook.failed` and `tool.hook.started/completed/failed` events **with the hook name and decision**. A config-hook deny is therefore already attributable in the run-event stream — no new SSE event types are needed; the wiring slice documents this instead of adding events.
- Config: layered TOML in `internal/config/config.go` (`rawLayer` pointer-merge). `[hooks]` follows the `rawForensics` pattern. User-global dir `~/.harness/`, project dir `<workspace>/.harness/` (`cmd/harnessd/main.go:323-326`) → hook discovery dirs `~/.harness/hooks/` and `<workspace>/.harness/hooks/`; trust store at `~/.harness/hooks-trust.json`.
- Registration point: `cw.Register(&runnerCfg)` at `cmd/harnessd/main.go:809`; compiled-in hooks first, config-driven appended after (order asserted in tests).
- Server: routes registered in `internal/server/http.go` `buildMux`; feature routes live in `http_<feature>.go` (e.g. `http_script_workflows.go`); summary rides `ServerOptions` → `Server` struct field.
- TUI: slash commands registered in `cmd_parser.go` (CommandRegistry), async fetch via `tea.Cmd` in `api.go` (pattern: `loadSubagentsCmd` → `SubagentsLoadedMsg` → viewport lines via `formatSubagentsLines`).
- harnesscli maintenance subcommands dispatch through `dispatch()` in `cmd/harnesscli/auth.go` — `hooks trust|revoke|list` lands there.

## Documentation Contract

- Feature status: `in implementation`
- Public docs affected: `docs/design/plugins.md` (new "Config-driven hooks" section, extended per slice), config reference (TOML `[hooks]` section), routes doc (CLAUDE.md route list + relevant runbook), TUI command reference.
- Spec docs to update before code: `docs/design/plugins.md` stub lands with slice 1 and is extended each slice (per-issue doc requirements).
- Implementation notes to add after code: engineering log entry per slice; folder indexes on every doc change.

## Test Plan (TDD)

- New failing tests to add first (per slice):
  1. `internal/hooks/loader_test.go` (table-driven: valid command/http defs, unknown event, missing argv/URL, malformed JSON, empty/nonexistent dir, mixed dir, matcher parse) + `internal/config` `[hooks]` tests.
  2. `internal/hooks/command_adapter_test.go` (allow, deny+reason, modified_args, modified_result, empty stdout, garbage stdout, non-zero exit, timeout kill, matcher skip via sentinel file, stdin golden fields) + harness-level deny integration under both failure modes.
  3. `internal/hooks/http_adapter_test.go` (httptest: allow/deny/block, empty body, non-2xx, timeout, refused, garbage) + command message-event cases + harness-level pre_message block integration.
  4. `internal/hooks/trust_test.go` (untrusted skip, trust→load, edit→modified_since_trusted, revoke, user-global bypass, corrupt store, atomic write) + `harnesscli hooks` subcommand tests.
  5. `cmd/harnessd` bootstrap helper tests (adapters land in correct slices, compiled-in first, disabled = unchanged) + server integration: trusted deny-all hook blocks a tool end-to-end (fake provider pattern).
  6. `internal/server/http_hooks_test.go` (populated/empty/shape) + TUI tests (parse `/hooks`, render populated/empty/error, dispatch regression).
- Existing tests to update: none expected (additive); slash-command dispatch and config tests must stay green.
- Regression tests required: any bug found during implementation gets a permanent regression test + engineering log entry.

## Cross-Surface Impact Map

- Not a provider/model flow change — impact map not required. Hooks touch config (new `[hooks]` section), server API (new `GET /v1/hooks`), and TUI (new `/hooks` command); all three are covered by the per-slice test plan above.

## Implementation Checklist

- [ ] Define acceptance criteria in tests.
- [ ] Document feature status and exact contract before code.
- [ ] Slice 1 (#741): schema + loader + `[hooks]` config — red, green, commit.
- [ ] Slice 2 (#744): command adapter tool-use events — red, green, commit.
- [ ] Slice 3 (#750): HTTP adapter + message events — red, green, commit.
- [ ] Slice 4 (#755): trust model + CLI — red, green, commit.
- [ ] Slice 5 (#759): harnessd wiring + observability + stored summary — red, green, commit.
- [ ] Slice 6 (#763): `GET /v1/hooks` + TUI `/hooks` — red, green, commit.
- [ ] Review ownership/copy semantics for exported or state-storing types.
- [ ] Update docs (plugins.md complete, config ref, routes, TUI ref), indexes, logs.
- [ ] Run `gofmt -l`, `go vet`, `go test ./internal/... ./cmd/...` (fast PR gate).
- [ ] Run `./scripts/test-regression.sh` (full gate incl. coverage).
- [ ] Push branch, open PR, comment on epic + child issues.

## Risks and Mitigations

- Risk: subprocess hooks hanging a run. Mitigation: per-hook `context.WithTimeout` + process kill, tested for orphans.
- Risk: a cloned repo executing commands via project hooks. Mitigation: content-hash trust store in user-global dir; untrusted files skipped with visible reasons; tested.
- Risk: duplication of wire types between command/HTTP adapters. Mitigation: wire types defined once in `internal/hooks/wire.go`, shared by both adapters; golden-field tests.
- Risk: failure policy double-application. Mitigation: adapters only return errors/decisions; runner's existing `HookFailureMode` loops decide; integration tests cover both modes.
- Risk: coverage gate (no zero-coverage functions) on new packages. Mitigation: behavior tests for every exported function, including error paths; run full regression gate before PR.

## Wire Protocol (locked contract for docs + golden tests)

- Common stdin/POST payload fields: `event` (`pre_message`|`post_message`|`pre_tool_use`|`post_tool_use`), `run_id`, `hook_name`.
- `pre_tool_use`: + `tool_name`, `call_id`, `args` (raw JSON).
- `post_tool_use`: + `result` (string), `duration_ms` (int), `error` (string, empty when nil).
- `pre_message`: + `step`, `model`, `message_count`; `messages` only when def sets `include_messages: true` (payload-size guard — documented choice per #750).
- `post_message`: + `step`, `model`, `message_count`, `response_text`, `tool_call_count`.
- Command stdout / HTTP response body (tool-use): `{"decision":"allow"|"deny","reason":"...","modified_args":<json>}` (pre); `{"modified_result":"..."}` (post).
- Message events: `{"action":"continue"|"block","reason":"..."}`.
- Exit-0 + empty stdout / 2xx + empty body = allow/no-op (nil result). Non-zero exit, non-2xx, timeout, unparseable output = adapter error → runner `HookFailureMode` decides.
