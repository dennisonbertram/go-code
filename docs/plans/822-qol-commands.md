# Plan: quality-of-life commands — epic #822 slices

Epic: #822. Parent: #803. Slice 1 branch: `epic/822-qol-commands` (merged, PR #842). Slice 2 branch: `epic/822-qol-commands-s2` (merged, PR #863). Slice 3 branch: `epic/822-qol-commands-s3` (merged, PR #894). Slice 4 branch: `epic/822-qol-commands-s4`.

---

# Slice 4: /feedback — bundle local diagnostics into a zip

## Context

- Problem: bug reports lack a one-command way to gather diagnostics (rollouts, config, runtime info).
- User impact: `/feedback` writes `<config-dir>/feedback/go-code-feedback-<timestamp>.zip` and prints the path; the user attaches it manually.
- Constraints: local-only (no upload/telemetry); no secrets in the bundle (canary-tested); strict TDD.

## Decided shape

- New `cmd/harnesscli/tui/feedback.go`: `executeFeedbackCommand` + pure builder `buildFeedbackBundle(outPath, feedbackInput)`.
- Bundle members:
  - `version.json` — `harnesscli_version` (slice 5 stamp when it exists; `"unstamped"` today), `go_version` (`runtime.Version()`), `goos`, `goarch`, `base_url`, `model`, `generated_at`, `notes` (e.g. rollout-dir absence).
  - `config.json` — the persistent CLI config (`harnessconfig.Load()`), redacted two ways: (1) exact-string replacement of every stored `api_keys` value (format-agnostic guarantee), then (2) `internal/forensics/redaction` pattern pass (catches secrets pasted into history).
  - `rollouts/<date>/<run>.jsonl` — newest 5 `.jsonl` files (by modtime) from the rollout dir, each run through the redactor; `rollouts/NOT_PRESENT.txt` marker + note when the rollout dir is unset/missing/empty.
- Rollout dir resolution: `HARNESS_ROLLOUT_DIR` env (the same wiring harnessd uses, `cmd/harnessd/main.go:429`); unset → absence note (per epic fallback).
- Output: `<defaultSessionConfigDir()>/feedback/go-code-feedback-<yyyymmdd-HHMMSS>.zip`; status message reports the path (pattern after `executeExportCommand`).

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: `website/docs/cli/tui.md`, `docs/ux-paths.md`
- Spec docs to update before code: none (epic #822 body is the contract)
- Implementation notes to add after code: none required

## Test Plan (TDD)

- New failing tests first:
  - `feedback_internal_test.go` (package tui): bundle members against a temp rollout dir (newest-5 cap, paths under `rollouts/`); redaction canary table (`sk-…` in api_keys value AND pasted into history, JWT, AWS `AKIA…`, postgres connection string, bearer token, short non-pattern key covered by exact-value replace); rollout content redaction canary; unset/missing rollout dir → `NOT_PRESENT.txt` + note, no error.
  - `feedback_test.go` (package tui_test): `/feedback` writes the zip under `<HOME>/.config/harnesscli/feedback/`, prints the path, canary key absent from the bundled config; works with no rollout dir; registry + slash-complete; `TestTUI364_RegistryCompleteness` += `feedback`.
- Regression: none beyond the above (additive change).

## Cross-Surface Impact Map

- None required: reads local files only; no run-request schema, server routes, or provider/model flows touched.

## Implementation Checklist

- [x] Acceptance criteria in tests (zip exists with JSONL + redacted config; canary secret never survives).
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Update docs (`tui.md`, `ux-paths.md`, plan).
- [x] Run `go test ./cmd/harnesscli/... -count=1`; gofmt + go vet clean.
- [ ] Push branch, open PR (no merge).

## Risks and Mitigations

- Risk: a stored API key in a format the regexes miss → mitigated by exact-string replacement of every `api_keys` value before the pattern pass (canary-tested).
- Risk: tests touching the developer's real `~/.config/harnesscli` → every test sets `t.Setenv("HOME", t.TempDir())`.
- Risk: huge rollout files bloat the zip → capped at newest 5 files.

---

# Slice 3: /add-dir — attach extra workspace directories to the session

## Investigation (required by the epic, drives the shape)

Traced from `runCreateRequest.WorkspacePath` (`cmd/harnesscli/tui/api.go`) through the server:

1. **The TUI's `workspace_path` is silently dropped today.** `handlePostRun` (`internal/server/http_runs.go:43`) decodes into `harness.RunRequest`, which has no `workspace_path` field. The effective tool workspace is the harnessd startup config `WorkspaceBaseOptions.RepoPath` (`Runner.defaultPermissionWorkspaceRoot`), or a per-run provisioned path when `workspace_type` is set (`runPreflight`, runner.go:1147-1219). Fixing the dropped `workspace_path` is NOT this slice; `extra_dirs` are additive to whatever root a run already uses.
2. **Confinement is real and single-rooted.** Default permission sandbox is `SandboxScopeWorkspace` (runner.go:5413, safety-biased default). Every file tool (`read/write/edit/ls/grep/apply_patch/...` in both `tools/` and `tools/core|deferred`) resolves paths through `ResolveWorkspacePathConfined` → `ConfineWorkspacePath(scope, root, extraAllowedRoots, abs)` (`internal/harness/tools/common_paths.go:165`). `ConfineWorkspacePath` **already implements extra roots** (incl. symlink-safe canonicalization, tested by `TestConfineWorkspacePath_ExtraAllowedRoots_Permitted`) — but every caller passes `nil`.
3. **Per-run overrides reach tools via context.** The step engine sets `htools.WithSandboxScope(toolCtx, effectiveSandboxScope)` per tool call (`runner_step_engine.go:996`) from `req` — the exact seam to thread extra roots.
4. **Bash is confined differently.** `CheckSandboxCommand` string heuristics (`sandbox.go`) plus OS-level seatbelt/bubblewrap profiles (`sandbox_darwin.go`/`sandbox_linux.go`) assume one root. Threading extra roots into OS sandbox profiles is a large change.

## Decided shape (minimal viable, per epic preference)

- `harness.RunRequest.ExtraDirs []string` (`json:"extra_dirs,omitempty"`); validated synchronously in `StartRun` (each entry: non-empty, absolute, exists, is a directory) → HTTP 400 on violation.
- New context key in `internal/harness/tools/types.go`: `WithExtraAllowedRoots` / `ExtraAllowedRootsFromContext` (mirrors `WithSandboxScope`).
- Step engine: `toolCtx = htools.WithExtraAllowedRoots(toolCtx, req.ExtraDirs)` next to the scope line.
- `ResolveWorkspacePathConfined` passes the context roots to `ConfineWorkspacePath` (instead of `nil`).
- TUI: `/add-dir <path>` (add, resolves relative to the session workspace, dedupes), `/add-dir` (list), `/add-dir remove <path>` (remove); dirs kept client-side per session and sent on every run via `startRunCmd` (new `extraDirs` parameter before the variadic `planMode`).
- **Documented limits (deliberately out of scope):** bash tool commands remain confined to the primary workspace root (OS sandbox profiles unchanged); `glob` still only matches inside the primary root; extra dirs are session-scoped (not persisted); the pre-existing dropped `workspace_path` is untouched.

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: `website/docs/cli/tui.md`, `docs/ux-paths.md`, server API docs note for the new `extra_dirs` run-request field (`website/docs/server/http-api-guide.md`)
- Spec docs to update before code: none (epic #822 body + this investigation)
- Implementation notes to add after code: none required

## Test Plan (TDD)

- New failing tests first:
  - `internal/harness` (acceptance, mirrors `default_sandbox_acceptance_test.go`): run with `ExtraDirs` → `read` tool reads a file under the extra root (content returned) and is still denied outside all roots; control run without `ExtraDirs` is denied under the extra root. StartRun validation table: relative/nonexistent/not-a-directory rejected; valid accepted.
  - `internal/harness/tools` (mirrors `workspace_confinement_test.go`): `ResolveWorkspacePathConfined` + ctx roots allows the extra root, denies elsewhere; no ctx roots → unchanged behavior.
  - `internal/server` (mirrors `http_workspace_type_test.go`): POST /v1/runs with a nonexistent `extra_dirs` entry → 400 `invalid_request`; valid entry → 202.
  - `cmd/harnesscli/tui`: `/add-dir` add/list/remove/dedupe/not-a-directory/relative-resolution through the model; `startRunCmd` marshals `extra_dirs` (httptest body capture); registry + slash-complete; `TestTUI364_RegistryCompleteness` += `add-dir`.
- Regression: default run (no extra_dirs) confinement unchanged — the existing GAP-1 acceptance test must stay green.

## Cross-Surface Impact Map

- Server API: new optional `extra_dirs` field on POST /v1/runs — additive, backward compatible. No provider/model flow, gateway, catalog, or API-key changes.

## Implementation Checklist

- [x] Investigation documented (above) and shape decided.
- [x] Acceptance criteria in tests (read under added root succeeds; outside all roots denied).
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Update docs (`tui.md`, `ux-paths.md`, server API note, plan).
- [x] Run `go test ./cmd/harnesscli/... ./internal/harness/... ./internal/server/... -count=1`; gofmt + go vet clean.
- [ ] Push branch, open PR (no merge).

## Risks and Mitigations

- Risk: scope creep into bash/seatbelt profiles. Mitigation: file-tools-only enforcement; bash limit documented in tui.md and the PR.
- Risk: symlinked extra root escapes. Mitigation: reuse `ConfineWorkspacePath`'s canonicalization (existing, tested) — roots are canonicalized per check.
- Risk: `/add-dir remove` collides with a dir literally named `remove`. Mitigation: `remove` is a subcommand only with an argument; `/add-dir remove` (one arg) adds the relative path `remove`. Documented in the command description.

---


# Slice 2: /init — generate AGENTS.md for the current workspace

## Context

- Problem: `internal/systemprompt` auto-injects `<workspace>/AGENTS.md` into the system prompt when present (`readAgentsMd`, engine.go), but nothing helps users create one.
- User impact: `/init` produces a starter AGENTS.md via a normal harness run; the next run's system prompt contains it.
- Constraints: write happens client-side after the run completes; overwrite requires explicit confirm; strict TDD.

## Scope

- In scope:
  - `init` command in `builtinCommandEntries()` (`cmd_parser.go`).
  - New `cmd/harnesscli/tui/init_agents.go`: fixed generation prompt (`initAgentsPrompt`), `executeInitCommand`, `extractAgentsMarkdown` (unwrap a single outer ``` fence), and the completion write path.
  - Model state `pendingInitAgentsMd`; on `RunCompletedMsg` with the flag set, write `<workspace>/AGENTS.md` (workspace = `m.config.Workspace`, falling back to cwd like `resolveWorkspacePath`); on `RunFailedMsg`, clear the flag without writing.
  - Overwrite guard: existing AGENTS.md + `/init` → hint to run `/init confirm`; `/init confirm` proceeds. Mirrors the `/rewind <id> confirm` approval pattern.
  - `/init` refused while a run is active (avoids mixing the assistant-text accumulator).
  - Docs: one row in `website/docs/cli/tui.md`, rows in `docs/ux-paths.md`, this plan.
- Out of scope: server changes; other epic slices (`/add-dir`, `/feedback`, `/upgrade`).

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: `website/docs/cli/tui.md`, `docs/ux-paths.md`
- Spec docs to update before code: none (epic #822 body is the contract)
- Implementation notes to add after code: none required

## Test Plan (TDD)

- New failing tests to add first (`cmd/harnesscli/tui/init_agents_test.go`):
  - `/init` starts a run carrying the generation prompt (observed via transcript), sets status, and writes `<workspace>/AGENTS.md` from the assistant markdown on completion.
  - Existing AGENTS.md + `/init` → no run, file untouched, hint message. `/init confirm` → run proceeds, file overwritten on completion.
  - `/init` while a run is active → refused; completion of the other run writes nothing.
  - Empty/fence-only assistant output → no file written. Run failure → no file, flag cleared.
  - `extractAgentsMarkdown` table: plain passthrough, ```markdown unwrap, ``` unwrap, unterminated fence, empty.
  - Written file is picked up by `internal/systemprompt` (`NewFileEngine` + `Resolve` on a temp fixture; AGENTS_MD section contains the content).
  - Registration: registry + slash-complete (`/in`).
- Existing tests to update: `TestTUI364_RegistryCompleteness` known-commands list (add `init`).
- Regression tests required: none beyond the above (additive change).

## Cross-Surface Impact Map

- None required: no provider/model flow, gateway, catalog, API-key, or server changes. The run uses the existing `startRunCmd` path unchanged.

## Implementation Checklist

- [x] Define acceptance criteria in tests (acceptance: repo without AGENTS.md → `/init` writes a plausible file; next run's prompt contains it — covered by the systemprompt integration test).
- [x] Write failing tests first.
- [x] Implement minimal code changes.
- [x] Update docs (`tui.md`, `ux-paths.md`, plan).
- [x] Run `go test ./cmd/harnesscli/... -count=1`; gofmt + go vet clean.
- [ ] Push branch, open PR (no merge).

## Risks and Mitigations

- Risk: model wraps the markdown in ``` fences → `extractAgentsMarkdown` strips a single outer fence; fence-only/empty output is treated as failure and nothing is written.
- Risk: concurrent run mixing up `lastAssistantText` → `/init` refused while `runActive`.
- Risk: tests writing outside temp dirs → every test sets `cfg.Workspace = t.TempDir()` (and HOME where the store matters).

---

# Slice 1: /title — name sessions and show the title in statusbar and picker

Status: **implemented and merged** (PR #842). Original slice-1 plan below for reference.

## Context

- Problem: sessions are identified only by a bare conversation ID; users cannot label them.
- User impact: `/title fix auth bug` names a session; the name survives restarts and shows in the statusbar and `/sessions` picker.
- Constraints: title stays client-side (`sessions.json`); no server store changes; strict TDD.

## Scope

- In scope:
  - `Title string \`json:"title,omitempty"\`` on `StoredSessionEntry` + `SetTitle(id, title) bool` setter (`cmd/harnesscli/tui/sessionstore.go`).
  - `title` command in `builtinCommandEntries()` (`cmd_parser.go`): no args shows current title; args set it (`Save()`); `/title clear` removes it.
  - Optional title segment in `components/statusbar` (`SetTitle`), rendered after the model segment.
  - `Title` on `sessionpicker.SessionEntry`; picker row prefers title over the 8-char ID.
  - Wiring: statusbar title refreshed on WindowSizeMsg, `/title`, session switch, `/new`, and RunStartedMsg.
  - Docs: one row in `website/docs/cli/tui.md`, one row in `docs/ux-paths.md`, plans index entry.
- Out of scope: server-side title columns, other epic slices (`/init`, `/add-dir`, `/feedback`, `/upgrade`).

## Documentation Contract

- Feature status: `implemented`
- Public docs affected: `website/docs/cli/tui.md` (slash-command table), `docs/ux-paths.md` (command table)
- Spec docs to update before code: none (epic #822 body is the contract)
- Implementation notes to add after code: none required for this slice

## Test Plan (TDD)

- New failing tests to add first:
  - `sessionstore_test.go`: title round-trip; legacy JSON without `title` loads with empty Title; empty title omitted from saved JSON; `SetTitle` found/not-found.
  - `title_test.go` (new): `/title` set + show + clear via the model; no-session error path; persistence across store reload; statusbar shows title; session switch loads the titled session's title; `/new` clears it; picker shows title; registry/slash-complete contains `title`.
  - `components/statusbar/statusbar_test.go`: title rendered when set; unchanged when empty; long title truncated.
  - `components/sessionpicker/model_test.go`: row prefers title over short ID; falls back to ID when empty.
- Existing tests to update: none expected (additive change).
- Regression tests required: legacy `sessions.json` without `title` key loads unchanged.

## Cross-Surface Impact Map

- None required: no provider/model flow, gateway routing, model catalog, API-key, or server plumbing changes. All state is client-side in the TUI.

## Implementation Checklist

- [x] Define acceptance criteria in tests (listed above; acceptance from epic: `/title fix auth bug`, restart, `/sessions` shows title, statusbar shows it in session).
- [x] Write failing tests first (store, command, statusbar, picker).
- [x] Implement minimal code changes.
- [x] Update docs (`tui.md`, `ux-paths.md`, plans index).
- [x] Run `go test ./cmd/harnesscli/... -count=1` (27 packages ok); gofmt + go vet clean on touched files.
- [ ] Push branch, open PR (no merge).

## Risks and Mitigations

- Risk: `/title clear` collides with a literal one-word title "clear".
  - Mitigation: `clear` clears only when it is the sole argument; `/title clear screen` still sets the literal title "clear screen". Documented in the command description.
- Risk: tests writing to the developer's real `~/.config/harnesscli/sessions.json`.
  - Mitigation: every new model-level test sets `t.Setenv("HOME", t.TempDir())` before `initModel` (existing repo pattern).
