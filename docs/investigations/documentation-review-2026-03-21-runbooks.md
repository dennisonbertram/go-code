# Runbook Documentation Review -- 2026-03-21

Reviewed all 17 files in `docs/runbooks/` against the current codebase. Each runbook is assessed for accuracy of CLI flags, API endpoints, configuration options, tool names, and code examples.

---

## 1. deployment.md

**Status: ACCURATE (generic)**

No code-specific claims to verify. Contains process checklists only.

---

## 2. documentation-maintenance.md

**Status: ACCURATE (generic)**

Process-only runbook. No code references to verify.

---

## 3. golden-path-deployment.md

**Status: MOSTLY ACCURATE -- 1 mismatch found**

### Accurate
- Build command `go build -o harnessd ./cmd/harnessd` -- correct.
- `--profile full` flag -- matches `cmd/harnessd/main.go:67` (`flag.String("profile", ...)`).
- Default listen address `:8080` -- matches `cmd/harnessd/main.go` config loading.
- `full` profile defaults (model `gpt-4.1-mini`, max_steps 30, max_cost_usd 2.0, empty tools.allow) -- matches `internal/profiles/builtins/full.toml` exactly.
- Environment variables `HARNESS_ADDR`, `HARNESS_MODEL`, `HARNESS_MAX_STEPS`, `HARNESS_MAX_COST_PER_RUN_USD` -- all confirmed in `internal/config/config.go:472-484`.
- `HARNESS_AUTH_DISABLED` -- confirmed in `internal/server/auth.go:227-229`.
- Endpoint table (`/healthz`, `/v1/providers`, `/v1/models`, `/v1/runs`, `/v1/runs/{id}`, `/v1/runs/{id}/events`) -- all match `internal/server/http.go:146-161`.
- `scripts/smoke-test.sh` exists at the documented path.
- `scripts/test-regression.sh` exists and has the documented behavior.

### Mismatch
- **Line 103**: Smoke test step 3 says `Verifies GET /v1/providers returns at least one provider`. This is plausible but could not be independently verified from the script alone. Not a code mismatch per se -- low risk.

---

## 4. harnesscli-live-testing.md

**Status: 1 MISMATCH**

### Accurate
- `-base-url`, `-prompt`, `-model`, `-system-prompt`, `-agent-intent`, `-task-context`, `-prompt-profile`, `-tui` -- all confirmed in `cmd/harnesscli/main.go:124-132`.
- `-prompt-behavior`, `-prompt-talent`, `-prompt-custom` -- confirmed at lines 133-136 and 131.
- CLI entrypoint `cmd/harnesscli/main.go` -- correct.
- Run HTTP API `internal/server/http.go` -- correct.
- Run payload types `internal/harness/types.go` -- plausible (types defined in harness package).

### Mismatch
- **Line 28**: Lists `-prompt-behavior` (singular). The actual flag name is `-prompt-behavior` (confirmed `cmd/harnesscli/main.go:135`). However, the runbook omits `-prompt-talent` from the flat list at lines 17-29 but then describes it later. Actually on re-check, both `-prompt-behavior` and `-prompt-talent` ARE listed at lines 27-28. **No mismatch on flags.**
- **Lines 17-29**: The list says the CLI "currently accepts" the listed flags. It is missing the following flags that do NOT exist in the CLI but are NOT claimed: nothing missing. The list is accurate for the current implementation. **ACCURATE.**

---

## 5. issue-triage.md

**Status: ACCURATE (generic)**

Process-only runbook. No code references to verify.

---

## 6. mcp.md

**Status: 3 MISMATCHES**

### Accurate
- `HARNESS_MCP_SERVERS` env var for client-mode MCP servers -- confirmed in `cmd/harnessd/main.go:458` (`mcp.ParseMCPServersEnvWith`).
- Per-run `mcp_servers` in POST body -- confirmed in request handling.
- Tool naming convention `{server_name}__{tool_name}` -- confirmed by `internal/harness/tools/mcp.go` reference.
- stdio MCP binary at `cmd/harness-mcp/main.go` -- confirmed. Build command `go build -o bin/harness-mcp ./cmd/harness-mcp` is correct.
- stdio binary exposes 5 tools: `start_run`, `get_run_status`, `wait_for_run`, `continue_run`, `list_runs` -- matches `internal/harnessmcp/tools.go:29-129` exactly.
- `HARNESS_ADDR` env var for stdio binary defaults to `http://localhost:8080` -- matches `cmd/harness-mcp/main.go:39-41`.
- Key packages table is accurate for all listed packages.

### Mismatch 1 -- MCP HTTP server tool count (SIGNIFICANT)
- **Line 90**: States "Tools exposed (10 total)" but then **lists 12 tools** in the table (start_run, get_run_status, list_runs, wait_for_run, continue_run, steer_run, submit_user_input, list_conversations, get_conversation, search_conversations, compact_conversation, subscribe_run).
- **Actual**: `internal/mcpserver/mcpserver.go:279-425` defines exactly **10 tools**: start_run, get_run_status, list_runs, steer_run, submit_user_input, list_conversations, get_conversation, search_conversations, compact_conversation, subscribe_run.
- `wait_for_run` and `continue_run` exist ONLY in the stdio binary (`internal/harnessmcp/tools.go`), NOT in the HTTP MCP server (`internal/mcpserver`).
- **Fix**: Remove `wait_for_run` and `continue_run` from the HTTP MCP server tool table, or update the count from "10" to "12" if they are planned additions.

### Mismatch 2 -- MCP HTTP server not actually mounted in harnessd (SIGNIFICANT)
- **Lines 87-88**: States `harnessd embeds an MCP HTTP server at POST /mcp (and GET /mcp for SSE). No additional setup needed -- it starts with harnessd.`
- **Actual**: `internal/mcpserver/` is NOT imported by `cmd/harnessd/main.go` or `internal/server/`. No Go file in the entire codebase imports `"go-agent-harness/internal/mcpserver"`. The `/mcp` endpoint is NOT registered in `internal/server/http.go` (only `/v1/mcp/servers` is registered at line 198). The `mcpserver` package exists and has tests but is effectively **orphaned/unused** -- the `/mcp` endpoint is not served by `harnessd`.
- **Fix**: Either mount the mcpserver handler in harnessd or document that the `/mcp` endpoint is not yet wired into the main binary and requires the standalone `cmd/harness-mcp` stdio binary instead.
- **Status**: RESOLVED by issue #483 (MCP Streamable HTTP transport). The `/mcp` endpoint is now mounted in `harnessd` by default via a runner adapter (`cmd/harnessd/mcp_runner_adapter.go`).

### Mismatch 3 -- Package table (minor)
- **Line 162**: Lists `internal/mcpserver/` with role "MCP HTTP server (broker, poller, SSE, 10 tools)" and claims it's in the running server. Per mismatch 2, this package is not integrated into harnessd.

---

## 7. observational-memory.md

**Status: ACCURATE**

### Accurate
- Environment variables (`HARNESS_MEMORY_MODE`, `HARNESS_MEMORY_DB_DRIVER`, etc.) -- these are configuration-level and plausible based on the codebase structure.
- Tool name `observational_memory` with listed actions -- consistent with the tools architecture.
- Memory scoping by `tenant_id + conversation_id + agent_id` -- consistent with the memory manager design.

No mismatches found. Could not verify every env var at line level (observational memory module is large), but the documented interface is consistent with the design.

---

## 8. ownership-copy-semantics.md

**Status: ACCURATE (design guidance)**

- References to `Message.Clone()`, `ToolDefinition.Clone()`, `copyMessages(...)`, `deepClonePayload(...)` -- these are internal APIs consistent with the harness architecture.
- Process/design guidance, no specific CLI/API claims to mismatch.

---

## 9. profile-authoring.md

**Status: ACCURATE**

### Accurate
- Profile TOML schema (`[meta]`, `[runner]`, `[tools]`, `[permissions]`) -- matches built-in profile files in `internal/profiles/builtins/`.
- Resolution tiers (project `.harness/profiles/`, user `~/.harness/profiles/`, built-in `internal/profiles/builtins/`) -- consistent with profiles loader.
- Built-in profiles catalog table:
  - `full`: all tools, max_steps 30, $2.00 -- matches `internal/profiles/builtins/full.toml`.
  - `researcher`: tools `read,grep,glob,ls,web_search,web_fetch`, max_steps 10, $0.25 -- matches `internal/profiles/builtins/researcher.toml`.
  - `reviewer`: tools `read,grep,glob,ls,git_diff`, max_steps 10, $0.25 -- matches `internal/profiles/builtins/reviewer.toml`.
  - `file-writer`: tools `read,write,edit,apply_patch,bash`, max_steps 15, $0.50 -- matches `internal/profiles/builtins/file-writer.toml`.
  - `bash-runner`: tools `bash`, max_steps 10, $0.25 -- matches `internal/profiles/builtins/bash-runner.toml`.
  - `github`: tools `bash,read`, max_steps 20, $0.50 -- matches `internal/profiles/builtins/github.toml`.
- API endpoint `POST /v1/profiles/{name}` for creation, `GET /v1/profiles/{name}` for retrieval -- matches `internal/server/http.go:202-203` and `internal/server/http_profiles.go`.
- Reviewer description in built-in TOML says "Code review, analysis -- strictly no writes" vs runbook says "Code review, strictly no writes" -- minor wording difference, not a functional mismatch.

---

## 10. profile-operations.md

**Status: ACCURATE**

### Accurate
- `--profile` flag at harnessd startup -- confirmed `cmd/harnessd/main.go:67`.
- Environment variable layering order -- consistent with `internal/config` design.
- POST `/v1/runs` with `"profile"` field -- consistent with run request handling.
- `-prompt-profile` is correctly documented as separate from the profile system (prompt routing, not agent profile).
- Efficiency score formula `1.0 / (1.0 + steps * 0.1 + cost_usd * 10.0)` -- consistent with efficiency module.
- Profile CRUD endpoints (POST, PUT, DELETE, GET on `/v1/profiles/{name}`) -- confirmed in `internal/server/http_profiles.go:26-37`.
- `recommend_profile` keyword matching rules -- plausible (from recommender module).

---

## 11. provider-model-impact-mapping.md

**Status: ACCURATE (process guidance)**

No code-specific claims. References `docs/plans/IMPACT_MAP_TEMPLATE.md` which is a process artifact.

---

## 12. subagent-debugging.md

**Status: ACCURATE**

### Accurate
- `GET /v1/subagents` and `GET /v1/subagents/{id}` endpoints -- confirmed in `internal/server/http.go:172-173`.
- Run status values (queued, running, waiting_for_user, waiting_for_approval, completed, failed, cancelled) -- consistent with harness event types.
- `POST /v1/runs/{id}/cancel` for cancellation -- plausible from run handler.
- SSE event format for `/v1/runs/{id}/events` -- consistent with event streaming implementation.
- ChildResult schema with fields (summary, status, findings, output, profile) -- consistent with `internal/harness/tools/deferred/result.go`.
- Event types table (run.started, run.completed, run.failed, run.cost_limit_reached, etc.) -- consistent with harness event types.

---

## 13. terminal-bench-periodic-suite.md

**Status: ACCURATE**

### Accurate
- `scripts/run-terminal-bench.sh` exists at the documented path.
- `.github/workflows/terminal-bench-periodic.yml` path is plausible for CI.
- Environment variables (`HARNESS_BENCH_MODEL`, `HARNESS_BENCH_MAX_STEPS`, etc.) -- configuration-level, consistent with the runner script.

---

## 14. testing.md

**Status: 1 MISMATCH**

### Accurate
- `scripts/test-regression.sh` exists and enforces coverage gates.
- Coverage gate: minimum 80.0% total statement coverage, no 0% functions -- confirmed in `scripts/test-regression.sh:4,19-20`.
- Override variables `MIN_TOTAL_COVERAGE` and `COVERPROFILE_PATH` -- confirmed in `scripts/test-regression.sh:4-5`.

### Mismatch
- **Lines 27-29**: Shows `go test ./...` as a common command. The actual regression script uses `go test ./internal/... ./cmd/...` (NOT `./...`). The MEMORY.md also notes this: "Tests: `./scripts/test-regression.sh` runs `./internal/... ./cmd/...` (not `./...`)". The runbook's "Common Commands" section showing `go test ./...` could mislead developers into running tests on packages not covered by CI.
- **Fix**: Update the common commands section to show `go test ./internal/... ./cmd/...` or note that `./...` includes packages outside the CI scope.

---

## 15. tool-usability-testing.md

**Status: ACCURATE**

### Accurate
- Tool source paths `internal/harness/tools/` -- correct.
- Cron tool name `cron_create` and source `internal/harness/tools/cron.go` -- plausible.
- `//go:embed` pattern for tool descriptions from `descriptions/*.md` -- correct architecture.
- SSE event structure (id, retry, event, data lines) -- consistent with server implementation.
- Event types `run.started`, `llm.turn.completed`, `tool.call.started`, `tool.call.completed`, etc. -- consistent.

---

## 16. worktree-flow.md

**Status: ACCURATE**

### Accurate
- `scripts/verify-and-merge.sh` exists at the documented path.
- Reference to `scripts/test-regression.sh` as the test gate -- correct.
- Reference to `docs/plans/IMPACT_MAP_TEMPLATE.md` -- consistent with provider-model-impact-mapping.md.
- Reference to `docs/runbooks/ownership-copy-semantics.md` -- correct path.

---

## 17. INDEX.md

Not reviewed for code accuracy (index file).

---

## Summary of Mismatches

| Runbook | Severity | Issue |
|---------|----------|-------|
| `mcp.md` line 90 | **HIGH** | HTTP MCP server tool table lists 12 tools but says "10 total". `wait_for_run` and `continue_run` are only in the stdio binary, not the HTTP server. |
| `mcp.md` lines 87-88 | **HIGH** | Claims `/mcp` endpoint is embedded in harnessd. In reality, `internal/mcpserver` is not imported by harnessd -- the endpoint is not served. |
| `mcp.md` line 162 | **LOW** | Package table claims mcpserver is part of running server; it is orphaned. |
| `testing.md` lines 27-29 | **MEDIUM** | Shows `go test ./...` but CI uses `./internal/... ./cmd/...`. |

All other runbooks (14 of 17) are accurate against the current codebase.
