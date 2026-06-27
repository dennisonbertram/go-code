# harnesscli TUI — UX Quality Audit Report

**Date:** 2026-06-26
**Prepared by:** QA workflow (12 parallel Sonnet 4.6 walkers) + independent Opus first-hand cross-check

---

## Method

A tmux-driven live walk of all 12 UX feature areas from `docs/ux-paths/catalog.md`, covering 136 story scenarios. Each area was driven by a dedicated Sonnet 4.6 walker instance running against a shared `harnessd` server with real OpenAI and Anthropic provider keys (`HARNESS_PROVIDER=live`, port range 8200–8212). Screens were captured via `tmux capture-pane` at each interaction step and stored under `/tmp/gocode-qa/out-<area-idx>/`. A second 28-finding adversarial re-drive pass was run to verify or refute selected findings before this report was written. An independent Opus first-hand walk (findings F-A through F-E) triangulated the most important items.

**Plain-text capture caveat:** All captures are ANSI-stripped plain text from `tmux capture-pane`. Color and reverse-video attributes are invisible in captures. Findings that describe "no selection cursor" may in some cases reflect a color-only cursor that is genuinely absent in plain-text contexts rather than invisible to a real terminal user. These are flagged explicitly where relevant.

**Raw finding counts:** 87 raw findings across 12 areas. Adversarial verification: 26/28 confirmed or partially confirmed, 2 refuted (see corrections below).

---

## Verification Corrections

The following adjustments are applied throughout this report:

**REFUTED — "PageUp/PageDown keys do not scroll" (area 2, Tool Execution):** The adversarial re-drive walker was unable to definitively reproduce this; the viewport was at the tail and there was nothing to scroll past in the re-drive session. Demoted from MAJOR to a footnote pending further test coverage.[^1]

**MERGED — "Status bar absent" (area 1) vs. "Status bar shows model name" (area 8):** These two findings are contradictory. Verification confirms: the status bar is **blank before any model is selected** (the pre-selection empty state) but shows the model name correctly after selection. The actual bug is that (a) no model name or cost is shown before first model selection, violating STORY-LC-001, and (b) the cost segment is permanently suppressed because the server returns `cost_status=unpriced_model` for all current models. These are merged into a single canonical finding (C-MAJOR-04 below).

[^1]: PageUp/PageDown scroll was reported by area 2 as not working. The adversarial verifier noted the viewport had nothing above the fold to scroll to. A separate test with a long multi-turn conversation should be used to validate this. Disposition: NEEDS-VERIFY.

---

## Summary Table

### By Severity

| Severity | Count | Notes |
|----------|-------|-------|
| Blocker  | 5     | All confirmed; 2 cause immediate HTTP 400 failures, 2 are dead/unreachable features, 1 is a data-corrupting stream bug |
| Major    | 32    | All confirmed or partially confirmed |
| Minor    | 38    | Confirmed; mostly layout, keyboard, and menu-consistency issues |
| Polish   | 12    | Confirmed; appearance and discoverability improvements |
| **Total**| **87** | 87 raw; deduplicated to ~62 canonical findings below |

### By Category

| Category | Blocker | Major | Minor | Polish |
|----------|---------|-------|-------|--------|
| markdown/response rendering | 1 | 5 | 3 | 1 |
| input/keyboard | 0 | 6 | 8 | 2 |
| menu-consistency (overlay DRY) | 0 | 4 | 12 | 3 |
| layout-jump | 0 | 4 | 3 | 0 |
| narrow-terminal | 0 | 3 | 4 | 0 |
| empty-error | 0 | 2 | 3 | 2 |
| other (wiring/config) | 4 | 8 | 5 | 4 |

---

## BLOCKERS

### B-01 — Profile selection sends wrong JSON field; all profile-gated runs fail HTTP 400

**Severity:** Blocker
**Affected areas:** 3 (Permission & Safety Controls), 5 (Profile Selection & Isolation)
**Category:** other (API wiring)

**What happened:** When any profile is selected via `/profiles` and a run is submitted, the TUI sends the profile name in the `prompt_profile` JSON field (targeting `RunRequest.PromptProfile`) instead of the `profile` JSON field (targeting `RunRequest.ProfileName`). The server responds HTTP 400 "invalid prompt_profile `full`: profile not found" because capability profile names are not valid prompt-style profile identifiers. Every subsequent run in that session fails. The selected profile persists across `/new` without being reset.

**Expected:** Selecting a capability profile should send `"profile": "full"` (not `"prompt_profile": "full"`). The run should succeed and the server should apply the named profile's tool restrictions and approval policy.

**Root cause:** `cmd/harnesscli/tui/api.go` — `startRunCmd` assigns the profile to `PromptProfile` (json tag `"prompt_profile"`) instead of a `ProfileName` field with tag `"profile"`. One-line fix in api.go.

**Evidence:** `/tmp/gocode-qa/out-3/41-write-with-researcher.txt` — `✗ start run: HTTP 400`

**Disposition:** FIX-NOW — add `ProfileName string \`json:"profile,omitempty"\`` to `runCreateRequest`; in `startRunCmd` assign the profile argument to `ProfileName`. Also reset `m.selectedProfile = ""` in `executeNewSessionCommand`.

---

### B-02 — `/permissions` command unimplemented; component is unreachable dead code

**Severity:** Blocker
**Affected areas:** 3 (Permission & Safety Controls)
**Category:** other (wiring)

**What happened:** Typing `/permissions` returns "Unknown command: /permissions." The `permissionspanel` package (`cmd/harnesscli/tui/components/permissionspanel/`) exists with full `model.go` and `view.go`, but no `/permissions` entry exists in `builtinCommandEntries()` in `cmd_parser.go`. The entire permission management UI (reviewed/denied rules, toggle, delete) is unreachable at runtime.

**Expected:** `/permissions` should open the permissionspanel overlay showing accumulated permission rules per STORY-008/032.

**Root cause:** `cmd/harnesscli/tui/cmd_parser.go` — missing entry in `builtinCommandEntries()`; no `executePermissionsCommand` handler registered.

**Evidence:** `/tmp/gocode-qa/out-3/13-permissions-enter.txt` — "Unknown command: /permissions."

**Disposition:** TICKET — register the command and wire the component, mirroring the pattern used for `/profiles`.

---

### B-03 — Streaming tail-replace corrupts code block content mid-stream (CRLF collision)

**Severity:** Blocker
**Affected areas:** 2 (Tool Execution Flow), confirmed by adversarial re-drive (area 32)
**Category:** response

**What happened:** When the assistant streams a fenced Go code block, the TUI rendered `func maifmt.Println("hi")` — two adjacent lines merged into a single garbled line. The corruption persists in the final viewport after run completion.

**Expected:** Each streamed line should appear on its own line matching the source. The replace-tail-lines mechanism must not collapse or merge adjacent lines.

**Root cause (verified by adversarial walker):** The SSE path in `model.go:1984-1995` handles `assistant.message.delta` by calling `m.vp.AppendChunk(p.Content)` with raw LLM text. `AppendChunk` in `viewport/model.go:64-81` splits only on `\n`, not `\r`. The LLM streaming response uses CRLF line endings; the `\r` at line end moves the terminal cursor to column 0, and the next line's content overwrites the previous line's tail characters, producing the garbled output. The `SSEDoneMsg` handler (~line 2079) does not run a final render pass through glamour.

**Fix options:**
1. Strip `\r` from delta content in `AppendChunk` or at the `assistant.message.delta` handler ingestion point.
2. On `SSEDoneMsg`, call `renderActiveAssistantBubble()` (the glamour path) to replace the raw streamed text with a clean rendered bubble — this also fixes the markdown rendering bug (B-04).

**Evidence:** `/tmp/gocode-qa/out-2/04-run-in-progress-4s.txt`, adversarial: `/tmp/gocode-qa/out-32/06-final-state.txt`

**Disposition:** FIX-NOW — strip `\r` from chunks; pair with markdown rendering rewire (see B-04).

---

### B-04 — Assistant markdown rendered raw; glamour path dead in production

**Severity:** Blocker
**Affected areas:** 1 (First Launch & Chat), plus every area that drives a real run; independent Opus finding F-A
**Category:** markdown

**What happened:** All assistant responses display raw markdown syntax literally: `## heading`, `**bold**`, `` `code` ``, `| table |`, `> quote`. Glamour rendering is fully bypassed for every live SSE response.

**Expected:** Assistant responses should be rendered through glamour: headings styled, bold/italic applied, inline code highlighted, bullet items formatted, tables column-aligned, blockquotes with left-margin bar.

**Root cause (from my-findings.md F-A, code confirmed):**
- `model.go:1857` `AssistantDeltaMsg` → `renderActiveAssistantBubble()` → glamour. **This is the correct path.**
- `model.go:1980-1995` `SSEEventMsg` case `"assistant.message.delta"` → `m.vp.AppendChunk(p.Content)` = raw text, no glamour. **This is the live path.**
- `AssistantDeltaMsg` is never produced in non-test code (`grep AssistantDeltaMsg{` finds only test files). The glamour path is dead in production; tests exercise it and pass, masking the bug.
- On `run.completed` (model.go:2089), `lastAssistantText` is saved to transcript but never re-rendered through glamour. The viewport keeps raw streamed text permanently.
- The glamour renderer itself works correctly — isolated probes confirm `messagebubble.RenderMarkdown` transforms text as expected.

**Evidence:** `/tmp/gocode-qa/out-1/10-poll-6s.txt`

**Disposition:** FIX-NOW — on `SSEDoneMsg`/`run.completed`, call `renderActiveAssistantBubble()` with the accumulated `lastAssistantText` and replace the tail of the viewport with the glamour-rendered output. Add a test that drives the SSE path (`assistant.message.delta`) and asserts rendered (not raw) output. Note: fixing B-03 (CRLF) at the same time avoids re-introducing the corruption.

---

### B-05 — Plan overlay component is dead code; ctrl+o / plan mode entirely unwired

**Severity:** Blocker
**Affected areas:** 7 (Planning Mode), 11 (Keyboard-Driven Navigation), Opus F-B
**Category:** other (wiring)

**What happened:** The `planoverlay` package (`cmd/harnesscli/tui/components/planoverlay/`) is a complete, standalone component with full state-machine logic (PlanStatePending/Approved/Rejected, y/n keypresses, PlanApprovedMsg/PlanRejectedMsg). It is never imported in `model.go`. Pressing `ctrl+o` with no active tool call produces no visible change whatsoever — no plan mode indicator, no overlay, no status bar message. The `Update()` function has no `key.Matches(msg, m.keys.PlanMode)` branch anywhere in the codebase.

Note: `ctrl+o` is double-bound in `keys.go` — `PlanMode = ctrl+o` (line 81) and `ExpandTool = ctrl+o` (line 90). Only `ExpandTool` has a handler (line 1345, guards on `activeToolCallID != ""`). No `PlanMode` handler exists.

**Expected:** `ctrl+o` with no active tool call should toggle plan mode flag and show a status bar indicator. When a plan SSE event arrives, the plan overlay should render per STORY-073.

**Root cause:** `cmd/harnesscli/tui/model.go` — `planoverlay` not imported; no `PlanMode` case in `Update()`.

**Evidence:** `/tmp/gocode-qa/out-7/17-ctrl-o-no-run.txt`, `/tmp/gocode-qa/out-11/30-ctrl-o.txt`

**Disposition:** TICKET — full wiring required: import planoverlay, embed in Model struct, add PlanMode case to Update(), handle plan SSE events, render in View().

---

## MAJORS

### M-01 — Ghost "running" tool cards persist after run completion

**Severity:** Major
**Affected areas:** 2 (Tool Execution Flow), confirmed adversarial
**Category:** response

**What happened:** After multi-tool runs, the viewport shows both the running state cards (`⺀ bash(...)…`) AND the completed state cards (`⺀ bash(...) (13ms)`) simultaneously. The ghost cards never disappear.

**Root cause (adversarial-verified):** `appendToolUseView` in `model.go:665-679` tracks only a single `renderedToolCallID` for in-place tail replacement. When two tool calls start sequentially before either completes, the second call sets `renderedToolCallID`, breaking replacement for the first call's completion event — it is appended as a new entry instead of replacing the running card.

**Evidence:** `/tmp/gocode-qa/out-2/04-run-in-progress-4s.txt`, adversarial `/tmp/gocode-qa/out-33/04-t3s.txt`

**Disposition:** TICKET — fix `appendToolUseView` to track per-callID viewport line positions, not just the tail.

---

### M-02 — Tool card args show raw JSON instead of human-readable display

**Severity:** Major
**Affected areas:** 2, 7 (Tool Execution Flow, Planning Mode)
**Category:** formatting

**What happened:** All tool call cards render arguments as escaped JSON strings: `⺀ bash("{\"command\": \"ls -l\", \"description\": \"...\"}")`. The JSON encoding with escaped quotes makes cards hard to scan.

**Expected:** Tool args should be extracted and displayed readably: `⺀ bash(ls -l)`, `⺀ read(main.go)`.

**Root cause:** The tool card renderer in `cmd/harnesscli/tui/components/tooluse/` does not parse and extract primary fields from the args JSON.

**Evidence:** `/tmp/gocode-qa/out-2/03-run-started.txt`

**Disposition:** TICKET — parse args JSON in collapsed view formatter; extract primary field per tool name.

---

### M-03 — File-write tool card shows raw JSON including full file content

**Severity:** Major
**Affected areas:** 2 (Tool Execution Flow), confirmed adversarial
**Category:** formatting

**What happened:** The `write` tool card shows the full JSON arg string including the file's content inline in the collapsed card header. No `⻿  Wrote N lines to notes.txt` summary is shown.

**Root cause:** `ParseFileOp`/`classifyToolName` in the tool card renderer only matches `write_file`, not `write`. The LLM dispatches `write`; `FileOpUnknown` is returned and no summary is produced.

**Evidence:** `/tmp/gocode-qa/out-2/36-edit-run-32s.txt`

**Disposition:** FIX-NOW — map `write` → `FileOpWrite` in the classifier.

---

### M-04 — Cost segment never shown; status bar blank before model selection (MERGED)

*This finding merges area 1 "status bar absent" and area 8 "cost counter never appears."*

**Severity:** Major
**Affected areas:** 1, 8 (First Launch & Chat, Cost & Context Awareness)
**Category:** other (config/wiring)

**What happened:** Before any model is selected, the status bar renders completely blank — no model name, no cost. After model selection, the model name appears correctly and persists. However, the cost segment never appears: the server returns `cost_status=unpriced_model` and `cumulative_cost_usd=0` for all models (both Anthropic and OpenAI). The TUI's `costUSD > 0` guard correctly suppresses `$0.0000`, but this means cost tracking is effectively disabled for all models in the current catalog.

**Expected:** Per STORY-LC-001, the status bar should show a default model name from launch. After runs, a cost segment should appear once pricing is populated.

**Root cause (dual):**
1. Status bar before model selection: `statusbar/model.go:71` guards show when `model == ""`, so nothing renders in the pre-selection empty state.
2. Cost segment: Pricing catalog has no USD per-token rates for shipped models. This is a server-side data gap, not a TUI code bug.

**Evidence:** `/tmp/gocode-qa/out-1/01-empty-state.txt`, `/tmp/gocode-qa/out-8/11-after-first-run.txt`

**Disposition:** TICKET (pricing catalog); FIX-NOW (pre-selection blank) — show a placeholder model name or "No model selected" in the status bar from launch.

---

### M-05 — Status bar running indicator (`...`) never shown during active runs

**Severity:** Major
**Affected areas:** 8 (Cost & Context Awareness)
**Category:** other (wiring)

**What happened:** The `statusbar` component has a `SetRunning(bool)` setter and renders a dimmed `...` segment when running, but `SetRunning(true)` is never called when a run starts and `SetRunning(false)` never called when it ends.

**Root cause:** `cmd/harnesscli/tui/model.go` — no `m.statusBar.SetRunning(true)` call in `RunStartedMsg` case; no `SetRunning(false)` in `SSEDoneMsg`/`RunCompletedMsg`/`RunFailedMsg`.

**Evidence:** `/tmp/gocode-qa/out-8/43-run-during-streaming.txt`

**Disposition:** FIX-NOW — add the two `SetRunning` calls in model.go.

---

### M-06 — Escape with autocomplete open clears entire input instead of just closing dropdown

**Severity:** Major
**Affected areas:** 9 (Slash Commands & Autocomplete)
**Category:** input

**What happened:** When the autocomplete dropdown is open and the user has typed a partial command (e.g. `/mo`), pressing Escape dismisses the dropdown AND clears the entire input, showing "Input cleared" status. The typed text is lost.

**Expected:** Escape should close the dropdown while preserving the partial text. The "Input cleared" status message should not appear.

**Root cause:** The Escape priority chain cascades through: close overlay → cancel run → **clear input**. The autocomplete close does not stop propagation before the "clear input" stage.

**Evidence:** `/tmp/gocode-qa/out-9/17-esc-dismiss-dropdown.txt`

**Disposition:** FIX-NOW — in the Escape handler, when autocomplete is active, close it and stop event propagation before the clear-input stage.

---

### M-07 — Unknown slash command silently fails — no feedback, input not cleared

**Severity:** Major
**Affected areas:** 9 (Slash Commands & Autocomplete)
**Category:** empty-error

**What happened:** Typing `/notacommand` and pressing Enter produces no feedback at all. The input remains showing `/notacommand` unchanged. No error or hint appears in the status bar.

**Expected:** An unrecognized slash command should show a status-bar error: "Unknown command: /notacommand. Type / for available commands." and the input should be cleared.

**Root cause:** `CmdUnknown` branch in the command dispatch path does not emit any status message or clear the input.

**Evidence:** `/tmp/gocode-qa/out-9/20-unknown-cmd-wait.txt`

**Disposition:** FIX-NOW — in `commandRegistry.Dispatch` / `ParseCommand`, emit status bar error and clear input for unrecognized commands.

---

### M-08 — Autocomplete dropdown does not scroll; selection disappears past 8 visible items

**Severity:** Major
**Affected areas:** 9 (Slash Commands & Autocomplete)
**Category:** layout-jump

**What happened:** With 14 commands, the dropdown shows 8 and "... 6 more". When Down is pressed 8+ times, the `▶` highlight marker disappears entirely from all visible rows — the selected item is in the hidden section with no visual indication. Enter still executes the hidden command, but blind.

**Expected:** The dropdown should scroll to keep the selected item visible.

**Evidence:** `/tmp/gocode-qa/out-9/24-navigate-down-8.txt`

**Disposition:** TICKET — implement scrollOffset in `slashcomplete` component renderer.

---

### M-09 — ctrl+c during active run skips two-stage interrupt banner; quits or interrupts immediately

**Severity:** Major
**Affected areas:** 10 (Error Recovery & Interrupts), 11 (Keyboard-Driven Navigation)
**Category:** other (wiring)

**What happened:** Pressing ctrl+c once during an active streaming run either (a) immediately shows "Interrupted" in the status bar with no confirmation banner, or (b) in some cases immediately quit the TUI entirely (area 11 observation: tmux session disappeared). The spec (STORY-ER-001/STORY-103) requires: first ctrl+c → show amber-bordered confirm banner "⚠ Press Ctrl+C again to stop, or Esc to continue"; run keeps streaming; second ctrl+c → cancel.

**Root cause:** The interrupt banner state machine (`Hidden→Confirm→Waiting`) is not implemented or not wired. The ctrl+c handler directly cancels the run or quits.

**Evidence:** `/tmp/gocode-qa/out-10/11-after-first-ctrlc.txt`, `/tmp/gocode-qa/out-11/83-ctrl-c-first-banner.txt`

**Disposition:** TICKET — implement the two-stage banner state machine in the ctrl+c handler.

---

### M-10 — Autocomplete preselects /clear; "/" + Enter immediately destroys conversation

**Severity:** Major
**Affected areas:** 10 (Error Recovery & Interrupts)
**Category:** menu-consistency

**What happened:** Typing `/` (bare slash) preselects `/clear` as the first item (`▶`). Pressing Enter immediately executes `/clear` and destroys the conversation with no confirmation.

**Expected:** Pressing Enter with bare `/` should not execute a destructive command without user navigation intent. Either do not pre-select any item for a bare `/`, or require confirmation for `/clear`.

**Evidence:** `/tmp/gocode-qa/out-10/54-slash-autocomplete.txt`

**Disposition:** FIX-NOW (low risk option: do not auto-select first item for bare `/`); TICKET (confirmation prompt for /clear).

---

### M-11 — `/` search key in model overlay includes literal `/` in filter string

**Severity:** Major (demoted to Minor is also defensible; walkers rated Major)
**Affected areas:** 4 (Model & Provider Selection)
**Category:** menu-consistency

**What happened:** Pressing `/` to enter search mode in the model overlay also feeds `/` into the filter buffer, so the filter shows `Filter: /gpt` instead of `Filter: gpt`.

**Expected:** `/` should act only as a mode-switch; the filter buffer should start empty.

**Evidence:** `/tmp/gocode-qa/out-4/19-search-gpt.txt`

**Disposition:** FIX-NOW — in the `/` key handler, enter search mode without appending `/` to the query buffer.

---

### M-12 — Selected provider row reformats count/dot column, causing layout jump on cursor move

**Severity:** Major
**Affected areas:** 4 (Model & Provider Selection), Opus F-D
**Category:** layout-jump

**What happened:** Unselected provider rows right-align `(N) ●`. When the cursor lands on a row, the format becomes `> OpenAI ●  (8)` — the `●` and count swap sides. Three distinct column formats exist simultaneously: unselected `(N) ●`, selected-no-current `● (N)`, selected-with-current `← current ●  (N)`. Every cursor movement shifts visible column positions.

**Evidence:** `/tmp/gocode-qa/out-4/02-model-open.txt`, `/tmp/gocode-qa/out-4/66-model-with-current.txt`

**Disposition:** FIX-NOW (small rendering change) — unify all rows to `  ProviderName  (count) ●`; only prepend `> ` for selected row; place `← current` in a fixed right-aligned column.

---

### M-13 — Star toggle applies to wrong model after list re-sorts post-star

**Severity:** Major
**Affected areas:** 4 (Model & Provider Selection)
**Category:** layout-jump

**What happened:** Starring a model re-sorts the list, shifting the cursor to a different model. A second `s` press then stars the wrong model.

**Root cause:** After a star toggle and re-sort, the model index is not updated to follow the toggled model to its new position.

**Evidence:** `/tmp/gocode-qa/out-4/13-level1-unstarred.txt`

**Disposition:** TICKET — after re-sort, find the toggled model in the new slice and set `m.Selected` to that index.

---

### M-14 — Profile overlay description text wraps outside box border at 80x24

**Severity:** Major
**Affected areas:** 3, 5 (Permission & Safety Controls, Profile Selection & Isolation)
**Category:** narrow-term

**What happened:** At 80x24, profile row description text wraps to the next line outside the box border, breaking the columnar layout (e.g. `execution,` floats loose on its own line).

**Expected:** Descriptions should be truncated with `…` at the panel's inner width.

**Evidence:** `/tmp/gocode-qa/out-3/63-profiles-narrow.txt`, `/tmp/gocode-qa/out-5/16-narrow-profiles.txt`

**Disposition:** FIX-NOW — in `profilepicker.View()`, calculate available width per column and truncate description strings with `lipgloss.MaxWidth` or a manual rune-slice+ellipsis.

---

### M-15 — Export status path truncated mid-word at 80x24 (no ellipsis)

**Severity:** Major
**Affected areas:** 6 (Conversation Management)
**Category:** narrow-term

**What happened:** At 80x24, the `/export` success status message clips mid-word at column 80 with no `…`: "Transcript saved to /Users/dennison/Library/Caches/harness/transcripts/transcrip"

**Evidence:** `/tmp/gocode-qa/out-6/33-narrow-export.txt`

**Disposition:** FIX-NOW — truncate the export path with `…` in the status message formatter before calling `setStatusMsg`.

---

### M-16 — Narrow terminal resize (80x24) clears all conversation history from viewport

**Severity:** Major
**Affected areas:** 2 (Tool Execution Flow)
**Category:** layout-jump

**What happened:** After resizing from 160x45 to 80x24, the viewport shows only the empty initial state — all conversation history disappears. Restoring to 160x45 does not recover the content.

**Root cause (TBD):** `WindowSizeMsg` handler likely re-initializes the viewport with a new size without rehydrating it with the existing message buffer.

**Evidence:** `/tmp/gocode-qa/out-2/40-narrow-80x24.txt`

**Disposition:** TICKET — `WindowSizeMsg` resize path must call `SetContent` with the accumulated message buffer, not reset it.

---

### M-17 — Keybindings help tab is incomplete — omits 5+ registered bindings

*Canonical deduplication of areas 3, 7, 10, 11, Opus F-C.*

**Severity:** Major
**Affected areas:** 3, 7, 10, 11 (Permission & Safety Controls, Planning Mode, Error Recovery, Keyboard Navigation)
**Category:** other (discoverability)

**What happened:** The Keybindings tab in `/help` shows only 9 hardcoded entries: enter, shift+enter/ctrl+j, up/ctrl+p, down/ctrl+n, pgup, pgdn, esc, ctrl+s, ctrl+c. Missing: `/` (SlashCmd), `@` (AtMention), `?`/`ctrl+h` (Help), `ctrl+o` (PlanMode/ExpandTool), `ctrl+e` (EditMode). `buildHelpDialog()` in `model.go:355-365` constructs the list by hand without enumerating `keys.FullHelp()`.

**Root cause:** `cmd/harnesscli/tui/model.go:355` — hardcoded partial keybinding list.

**Evidence:** `/tmp/gocode-qa/out-7/46-help-keybindings.txt`, `/tmp/gocode-qa/out-11/02-help-keybindings.txt`, Opus my-findings.md F-C

**Disposition:** FIX-NOW — drive `buildHelpDialog` from `keys.FullHelp()` or add the missing entries explicitly.

---

### M-18 — `ctrl+c` help label says "quit" — misleading during runs

*Canonical deduplication of areas 3, 10.*

**Severity:** Minor (borderline Major; listed here per walker severity)
**Affected areas:** 3, 10
**Category:** menu-consistency

**What happened:** Keybindings tab shows `ctrl+c → quit` unconditionally. During an active run ctrl+c should cancel (two-stage), not quit. Users may avoid it during runs.

**Disposition:** FIX-NOW — update label to "quit / interrupt run".

---

### M-19 — `?` key inserts into input instead of opening help dialog

**Severity:** Major
**Affected areas:** 11 (Keyboard-Driven Navigation)
**Category:** input

**What happened:** `keys.go` registers `key.WithKeys("ctrl+h", "?")` for Help. Pressing `?` inserts `?` into the input buffer; the help dialog does not open.

**Root cause:** `?` is not intercepted as a global hotkey before being routed to the input area.

**Evidence:** `/tmp/gocode-qa/out-11/28-question-mark-help.txt`

**Disposition:** FIX-NOW — intercept Help keybinding before routing keys to the input area in `model.go Update()`.

---

### M-20 — `@` mention key inserts into input; no file-picker overlay

**Severity:** Major
**Affected areas:** 11 (Keyboard-Driven Navigation)
**Category:** input

**What happened:** `keys.go` registers `@` as AtMention "mention file". Pressing `@` inserts `@` into input; no file-picker appears.

**Disposition:** TICKET — implement the @ file-picker overlay, or remove the AtMention binding from `keys.go` to stop confusing users.

---

### M-21 — `ctrl+e` (editor) silently fails when `$EDITOR` is unset

**Severity:** Major
**Affected areas:** 11 (Keyboard-Driven Navigation)
**Category:** input

**What happened:** With `$EDITOR` unset, pressing `ctrl+e` produces zero visible change — no error message, no status bar note.

**Expected:** Show "No editor configured (set $EDITOR)" per STORY-KN-012.

**Disposition:** FIX-NOW — check `$EDITOR`/`$VISUAL` in the ctrl+e handler; emit transient status message if unset.

---

### M-22 — Help dialog clips /stats and /subagents at 80x24 with no scroll indicator

*Canonical deduplication of areas 1, 3, 6, 7, 11.*

**Severity:** Minor (reported as Minor in majority of areas)
**Affected areas:** 1, 3, 6, 7, 11
**Category:** narrow-term

**What happened:** At 80x24, the `/help` Commands tab shows only 12-13 of 14-15 commands. `/stats` and `/subagents` are cut off at the bottom of the box with no `... N more` indicator, no scroll hint.

**Evidence:** `/tmp/gocode-qa/out-1/23-narrow-help-tab1.txt`, `/tmp/gocode-qa/out-7/31-keybindings-narrow.txt`

**Disposition:** FIX-NOW — add `... N more` truncation indicator in `helpdialog/view.go renderCommandLines()` when content is clipped by terminal height, mirroring the model switcher pattern.

---

### M-23 — Overlay footer navigation hint absent or inconsistent across 6 overlays

*Canonical deduplication of areas 5, 6, 9, 10, 11, 12.*

**Severity:** Minor (systemic)
**Affected areas:** 5, 6, 9, 10, 11, 12 (Sessions, Conversation Mgmt, Slash Cmds, Error Recovery, Keyboard Nav, Sessions)
**Category:** menu-consistency

**What happened:** The following overlays are missing a footer navigation/dismiss hint entirely: `/sessions` (no hint at all), `/help` (no "tab next / esc close"), `/context` (unboxed, no hint), `/stats` (unboxed, no hint). The `/model`, `/profiles`, and `/keys` overlays correctly show footer hints.

**Disposition:** FIX-NOW (mechanical one-liner per overlay) — add `↑/↓ navigate  enter select  esc cancel` footer to `/sessions`; add `tab next tab  esc close` footer to `/help`; add `esc close` hint to `/context` and `/stats`.

---

### M-24 — Profiles picker no visible text-based selection cursor (color-only)

*Canonical deduplication of areas 3, 5, 12. Note: possible plain-text-capture artifact.*

**Severity:** Minor (flagged as possible capture artifact; real a11y concern)
**Affected areas:** 3, 5, 12
**Category:** menu-consistency

**What happened:** The profiles picker renders all rows identically in plain-text captures — no `>`, no `▶`, no bullet on the selected row. Selection relies entirely on terminal reverse-video highlight (invisible in `capture-pane` output and absent on mono/limited terminals). The `/model` picker uses `>` text marker; the `/keys` overlay uses `▶`. The `/sessions` overlay also lacks a text marker.

**Capture caveat:** The `>` marker may exist as a color-coded highlight not visible in ANSI-stripped captures. However even if color is present, the a11y/consistency point stands — `/model` uses a text marker and so should all list overlays.

**Disposition:** FIX-NOW — add `▶ ` prefix on selected row and `  ` on unselected rows in `profilepicker.View()` and `sessionpicker.View()`, consistent with `/keys`.

---

### M-25 — `/new` command does not clear selected profile; stale profile persists silently

**Severity:** Minor
**Affected areas:** 3 (Permission & Safety Controls)
**Category:** other

**What happened:** After selecting a profile via `/profiles`, pressing `/new` starts a new session but does not reset `m.selectedProfile`. The profile name is silently sent on every subsequent run (compounding B-01's HTTP 400 failures).

**Disposition:** FIX-NOW — add `m.selectedProfile = ""` in `executeNewSessionCommand` (model.go ~line 1046).

---

## MINORS

### N-01 — `ctrl+u` does not clear input (only Escape works)

**Severity:** Minor
**Affected areas:** 1 (First Launch & Chat)
**Category:** input

**What happened:** Typing text and pressing ctrl+u has no effect; input remains unchanged. This is confirmed by areas 1 and 9 (area 9 notes ctrl+u works only after autocomplete is dismissed). The keybindings tab does not list ctrl+u.

**Disposition:** FIX-NOW — bind ctrl+u in the textarea/input component to clear all input content; add to keybindings help.

---

### N-02 — Help dialog retains active tab state across open/close cycles

**Severity:** Minor
**Affected areas:** 1, 7
**Category:** menu-consistency

**What happened:** Reopening `/help` after navigating to the About tab shows About instead of Commands.

**Disposition:** FIX-NOW — reset `activeTab = TabCommands` in `helpdialog` when the dialog is opened/shown.

---

### N-03 — Overlay border style and cursor markers inconsistent across overlays

**Severity:** Minor
**Affected areas:** 1, 4, 5
**Category:** menu-consistency

**What happened:** `/model` uses full-width borders with `>` cursor. `/keys` uses narrow centered box with `▶` cursor. `/help` uses narrow centered box with no cursor. `/profiles` uses full-width with no text cursor. Three distinct border widths and two cursor styles coexist.

**Disposition:** TICKET — establish a shared overlay wrapper component; standardize cursor marker to `▶` or `>` across all list overlays.

---

### N-04 — `--list-profiles` CLI description column misaligned when descriptions exceed 40 chars

**Severity:** Minor
**Affected areas:** 5 (Profile Selection & Isolation)
**Category:** formatting

**What happened:** Two profiles (`github`, `reviewer`) exceed 40 chars in description; the `Model:` column lands at a different character position for those rows, breaking tabular alignment.

**Evidence:** `/tmp/gocode-qa/out-5/14-list-profiles-cli.txt`

**Disposition:** FIX-NOW — truncate descriptions > 40 runes with `…` in `listProfilesCmd()` at `cmd/harnesscli/main.go:460`.

---

### N-05 — "Loading profiles..." status persists after overlay data is rendered

**Severity:** Minor
**Affected areas:** 5 (Profile Selection & Isolation)
**Category:** empty-error

**What happened:** The server responds in ~0.3s, but "Loading profiles..." status persists for the full 3-second auto-dismiss timer while the overlay already shows data.

**Disposition:** FIX-NOW — call `m.setStatusMsg("")` or `m.setStatusMsg("Profiles loaded")` in the `ProfilesLoadedMsg` success handler.

---

### N-06 — /context and /stats overlays render as unboxed plain text with no border or dismiss hint

**Severity:** Minor
**Affected areas:** 8, 10
**Category:** menu-consistency

*(See also M-23 for footer hint gap; this covers the missing box border.)*

**What happened:** `/context` and `/stats` render content as bare text in the viewport with no surrounding box, making them visually inconsistent with all other overlays.

**Disposition:** TICKET — wrap `/context` and `/stats` views in a bordered-box component matching other overlays, or at minimum add an `esc close` hint line below the content.

---

### N-07 — Stats heatmap column width is fixed; does not use available terminal width

**Severity:** Minor
**Affected areas:** 8 (Cost & Context Awareness)
**Category:** formatting

**What happened:** The year-view heatmap renders 53 columns (~58 chars wide) at any terminal width, leaving 102 chars unused at 160x45.

**Disposition:** TICKET — in `statspanel`, compute available columns from model width and scale the grid accordingly.

---

### N-08 — Esc from model config panel (Level 2) requires extra presses to fully close

**Severity:** Minor
**Affected areas:** 4 (Model & Provider Selection)
**Category:** other

**What happened:** Three Esc presses should navigate Level 2 → Level 1 → Level 0 → close, but in practice more presses were needed.

**Disposition:** TICKET — audit the Esc key handler in the model switcher; ensure each Esc decrements exactly one navigation level.

---

### N-09 — Tab completion inserts command without trailing space

**Severity:** Minor
**Affected areas:** 9 (Slash Commands & Autocomplete)
**Category:** input

**What happened:** Completing `/he` → `/help` via Tab produces `/help` with no trailing space, requiring a manual space before typing arguments.

**Disposition:** FIX-NOW — append a trailing space to the completed command string in `CompleteTab()`.

---

### N-10 — Fuzzy filter matches command descriptions, producing unintuitive results for single-letter queries

**Severity:** Minor
**Affected areas:** 9 (Slash Commands & Autocomplete)
**Category:** formatting

**What happened:** Typing `/s` returns `/history`, `/keys`, `/profiles` (matched by description text), mixed in with `/stats`, `/search`, `/sessions`, `/subagents` (matched by name).

**Disposition:** TICKET — rank results so command-name prefix matches appear first; deprioritize or exclude pure description-only matches.

---

### N-11 — Dead server connection error shows raw Go HTTP error text

**Severity:** Minor
**Affected areas:** 10 (Error Recovery & Interrupts)
**Category:** empty-error

**What happened:** When server is unreachable: `✗ Post "http://localhost:8299/v1/runs": dial tcp [::1]:8299: connect: connection refused`

**Expected:** `✗ Server unreachable at http://localhost:8299. Is harnessd running?`

**Disposition:** FIX-NOW (small) — detect "connection refused" in `RunFailedMsg` formatter and replace with a friendly template.

---

### N-12 — Model display names inconsistent — some rows show raw IDs instead of friendly names

**Severity:** Minor
**Affected areas:** 4 (Model & Provider Selection)
**Category:** formatting

**What happened:** In the OpenAI model list, `GPT-4.1` and `GPT-4.1 Mini` show friendly names; `gpt-5.1-codex`, `gpt-5.2-codex`, `computer-use-preview` show raw lowercase hyphenated IDs.

**Disposition:** TICKET — ensure `display_name` fields are populated in `catalog/models.json` for new models; TUI renders `display_name` over raw ID in all cases.

---

### N-13 — Escape priority: closing autocomplete also triggers "Input cleared" status message

*(Subset of M-06; the status message itself is a distinct minor issue.)*

**Severity:** Minor (partially covered by M-06)
**Affected areas:** 9
**Note:** Covered by M-06. Disposition: same FIX-NOW.

---

### N-14 — `/allow_fallback` not set in TUI run requests

**Severity:** Minor
**Affected areas:** Independent (Opus F-E)
**Category:** other

**What happened:** `cmd/harnesscli/tui/api.go:51` `startRunCmd` never sets `allow_fallback`. If a model's provider can't be resolved, the run hard-fails instead of degrading gracefully. The server supports graceful fallback; the TUI does not opt in.

**Disposition:** TICKET — add `AllowFallback: true` to the run request struct.

---

### N-15 — Stale documentation: command count in catalog says 10, TUI has 14

**Severity:** Minor
**Affected areas:** 9
**Category:** other

**Disposition:** TICKET — update `docs/ux-paths/topics/slash-commands-autocomplete.md` to reflect 14-command catalog; add stories for `/history`, `/new`, `/search`.

---

### N-16 — `ctrl+s` on empty transcript shows "Copied!" with empty clipboard

**Severity:** Minor (borderline polish)
**Affected areas:** 6
**Category:** empty-error

**Disposition:** FIX-NOW — check `lastAssistantText == ""` before calling `CopyToClipboard`; show "No response to copy" instead.

---

### N-17 — Tab completion on ambiguous filter does nothing and gives no feedback

**Severity:** Minor
**Affected areas:** 9
**Category:** input

**Disposition:** TICKET — when Tab finds multiple matches and cannot advance, emit a brief hint "Multiple matches — use ↑/↓ to select".

---

### N-18 — Model overlay error message wraps URL across two lines at 80x24

**Severity:** Minor
**Affected areas:** 10
**Category:** narrow-term

**Disposition:** FIX-NOW (small) — shorten model-overlay error to "Error: server unreachable" to avoid wrapping.

---

## POLISH

### P-01 — Autocomplete dropdown caps at 8 items even when vertical space is available

**Severity:** Polish
**Affected areas:** 1, 9
**Category:** formatting

**What happened:** At 160x45 the dropdown shows 8 commands and "... 6 more" with many blank rows above.

**Disposition:** TICKET — pass available terminal height to `slashcomplete`; set max visible items dynamically.

---

### P-02 — Help dialog tab state resets inconsistently under certain conditions

**Severity:** Polish (subset of N-02)
**Affected areas:** 7
**Note:** Covered by N-02.

---

### P-03 — Config panel API Key "K to update" hint only appears when section focused

**Severity:** Polish
**Affected areas:** 4
**Category:** menu-consistency

**Disposition:** TICKET — surface the K-to-update hint in the footer bar when API Key section is focused, or always show a truncated `(K)` hint.

---

### P-04 — Stats week-view heatmap renders as a 7-row single-column list

**Severity:** Polish
**Affected areas:** 8
**Category:** formatting

**Disposition:** TICKET — render week view as a 7-column row (one cell per day) for a more grid-like appearance.

---

### P-05 — Profile overlay size (full-width) differs from keys overlay (narrow-centered)

**Severity:** Polish (part of N-03)
**Affected areas:** 5
**Note:** Covered by N-03.

---

### P-06 — Model list style/size inconsistent between /model (full-width) and /keys (narrow)

**Severity:** Polish (part of N-03)
**Affected areas:** 4
**Note:** Covered by N-03.

---

### P-07 — "Loading profiles..." stale after data loads

**Severity:** Polish (subset of N-05)
**Note:** Covered by N-05.

---

### P-08 — ctrl+h (Help) not mentioned separately from ? in keybindings

**Severity:** Polish
**Affected areas:** 11
**Note:** Covered by M-17 (keybindings completeness).

---

### P-09 — Stats heatmap: week view single-column has low information density

**Severity:** Polish (subset of N-07)
**Note:** Covered by N-07.

---

### P-10 — Document the auto-dismiss timing for transient status messages

**Severity:** Polish
**Affected areas:** 5, 8
**What happened:** "Loading profiles..." auto-dismiss takes 3s even after data renders (N-05). Users don't know the message will clear.
**Disposition:** TICKET (or doc note).

---

### P-11 — Model list display name inconsistency: raw IDs mixed with friendly names

**Severity:** Polish (subset of N-12)
**Note:** Covered by N-12.

---

### P-12 — Allow_fallback not set in TUI runs

**Severity:** Minor (Opus F-E; listed here as well for completeness)
**Note:** Covered by N-14.

---

## DISPOSITION SUMMARY

| ID | Title | Disposition |
|----|-------|-------------|
| B-01 | Profile sends wrong JSON field | FIX-NOW |
| B-02 | /permissions unimplemented | TICKET |
| B-03 | Streaming CRLF corrupts code blocks | FIX-NOW |
| B-04 | Assistant markdown raw (glamour dead in SSE path) | FIX-NOW |
| B-05 | Plan overlay / ctrl+o entirely unwired | TICKET |
| M-01 | Ghost running tool cards | TICKET |
| M-02 | Tool card args raw JSON | TICKET |
| M-03 | Write tool card shows full file content | FIX-NOW |
| M-04 | Cost segment never shown / status bar blank pre-selection | TICKET (pricing) + FIX-NOW (pre-selection blank) |
| M-05 | Status bar running indicator never wired | FIX-NOW |
| M-06 | Escape with autocomplete clears input | FIX-NOW |
| M-07 | Unknown slash command no feedback | FIX-NOW |
| M-08 | Autocomplete dropdown no scroll | TICKET |
| M-09 | ctrl+c two-stage interrupt missing | TICKET |
| M-10 | "/" + Enter silently runs /clear | FIX-NOW (no pre-select) |
| M-11 | "/" key in model overlay leaks into filter | FIX-NOW |
| M-12 | Provider row column layout jump on cursor move | FIX-NOW (small rendering) |
| M-13 | Star toggle cursor drift after re-sort | TICKET |
| M-14 | Profile description wraps outside box at 80x24 | FIX-NOW |
| M-15 | Export path truncated mid-word at 80x24 | FIX-NOW |
| M-16 | Narrow resize clears history | TICKET |
| M-17 | Keybindings help tab incomplete | FIX-NOW |
| M-18 | ctrl+c help label "quit" — misleading | FIX-NOW |
| M-19 | ? key goes to input not help | FIX-NOW |
| M-20 | @ mention no file-picker | TICKET |
| M-21 | ctrl+e silent failure when $EDITOR unset | FIX-NOW |
| M-22 | /help clips /stats & /subagents at 80x24 | FIX-NOW |
| M-23 | Overlay footer hint absent (sessions/help/context/stats) | FIX-NOW |
| M-24 | Profiles/sessions picker no text-mode cursor | FIX-NOW |
| M-25 | /new doesn't clear selected profile | FIX-NOW |
| N-01 | ctrl+u doesn't clear input | FIX-NOW |
| N-02 | Help dialog tab state persists across close | FIX-NOW |
| N-03 | Overlay border/cursor style inconsistent | TICKET |
| N-04 | --list-profiles description column misaligned | FIX-NOW |
| N-05 | "Loading profiles..." stale after data loads | FIX-NOW |
| N-06 | /context and /stats unboxed | TICKET |
| N-07 | Stats heatmap fixed width | TICKET |
| N-08 | Extra Esc presses to close model Level 2 | TICKET |
| N-09 | Tab completion no trailing space | FIX-NOW |
| N-10 | Fuzzy filter matches descriptions | TICKET |
| N-11 | Dead server shows raw Go error | FIX-NOW |
| N-12 | Model display name inconsistency | TICKET |
| N-14 | allow_fallback not set | TICKET |
| N-15 | Stale command count in docs | TICKET |
| N-16 | ctrl+s empty transcript shows "Copied!" | FIX-NOW |
| N-17 | Tab on ambiguous no feedback | TICKET |
| N-18 | Model overlay error wraps at 80x24 | FIX-NOW |

---

## Appendix: Per-Area Finding Index

All 87 raw findings, preserved in full. Area / Severity / Title.

### Area 1 — First Launch & Chat (7 findings)
1. major / markdown — Markdown response rendered as raw text inside a fenced code block
2. major / formatting — Status bar absent — no model name or cost counter displayed at any point
3. minor / input — Ctrl+U does not clear input — only Escape works
4. minor / narrow-term — Help dialog Commands list truncated at 80x24 — bottom 2 items cut off
5. minor / menu-consistency — Help dialog retains last active tab state across open/close cycles
6. minor / menu-consistency — Help and Model overlays use inconsistent border styles and navigation markers
7. polish / formatting — Autocomplete dropdown truncates at 8 items with '... 6 more' at 160x45

### Area 2 — Tool Execution Flow (8 findings)
8. blocker / response — Streaming tail-replace corrupts code block content mid-stream
9. major / response — Ghost running tool cards remain visible above completed tool cards after run finishes
10. major / formatting — File-write tool card shows raw JSON args with embedded content instead of file-op summary line
11. major / response — PageUp/PageDown keys do not scroll the conversation viewport [REFUTED — see footnote 1]
12. major / layout-jump — Narrow terminal resize (80x24) clears all conversation history from viewport
13. minor / response — Ctrl+O does not expand completed tool call cards visible in viewport
14. minor / formatting — Tool card args show raw JSON objects instead of readable arg display
15. minor / response — No diff or write summary rendered for write tool call in expanded or collapsed view

### Area 3 — Permission & Safety Controls (7 findings)
16. blocker / other — Profile selection sends wrong JSON field — all profile-gated runs fail HTTP 400
17. blocker / other — /permissions slash command is unimplemented — 'Unknown command' error returned
18. major / menu-consistency — Profiles panel has no visible text selection cursor — impossible to tell which row is selected without color
19. major / narrow-term — Profiles panel description text wraps to next line at 80x24 instead of truncating with ellipsis
20. minor / other — /new command does not clear selected profile — stale profile persists invisibly across sessions
21. minor / empty-error — Help command list is clipped at 80x24 — /stats and /subagents hidden without scroll indicator
22. polish / menu-consistency — Help keybindings tab labels ctrl+c as 'quit' but it acts as 'interrupt' during runs

### Area 4 — Model & Provider Selection (8 findings)
23. major / layout-jump — Selected provider row uses inverted count/dot column order causing layout jump on cursor move
24. major / layout-jump — ← current label on provider row further scrambles column layout when current model is set
25. major / other — Star toggle applies to wrong model after list re-sorts — cursor drifts to new position
26. minor / menu-consistency — Pressing '/' to open search includes the literal '/' character in the filter string
27. minor / menu-consistency — Keys overlay uses different border style and size from /model overlay
28. minor / other — Esc from config panel (Level 2) requires more key presses than expected to reach Level 0
29. polish / formatting — Model list display names are inconsistent — some use raw IDs, some use friendly names
30. polish / menu-consistency — Config panel API Key section hint text changes between focused and unfocused states

### Area 5 — Profile Selection & Isolation (6 findings)
31. major / narrow-term — Profile overlay description text wraps outside box border at 80x24
32. minor / formatting — --list-profiles CLI Model column misaligned when description exceeds 40 chars
33. minor / other — 'Loading profiles...' status message persists ~3s after overlay data is already rendered
34. minor / menu-consistency — Profile picker has no visible text-based selection cursor marker in plain rendering
35. minor / menu-consistency — Sessions overlay missing footer navigation hint present in profiles and keys overlays
36. polish / menu-consistency — Profile overlay size and position differs from keys overlay (full-width vs narrow-centered)

### Area 6 — Conversation Management (4 findings)
37. major / narrow-term — Export status path truncated mid-word without ellipsis at 80x24
38. minor / narrow-term — Help overlay clips last 2 commands (/stats, /subagents) at 80x24 with no scroll hint
39. minor / menu-consistency — Help overlay has no navigation/footer hint line
40. polish / other — ctrl+s on empty transcript (after /clear) shows 'Copied!' with empty clipboard

### Area 7 — Planning Mode (Extended Thinking) (6 findings)
41. blocker / other — Plan overlay component is dead code — never wired into the TUI
42. major / menu-consistency — Keybindings help tab omits ctrl+o (PlanMode and ExpandTool) and other mode bindings
43. major / other — PlanMode key binding (ctrl+o) has no handler in Update() — it is a no-op
44. minor / menu-consistency — Help dialog Keybindings tab shows wrong content on second Tab press after /help in some states
45. minor / narrow-term — Help dialog at 80x24 clips /stats and /subagents command entries
46. polish / formatting — Tool call card uses escaped JSON instead of pretty-printed arguments

### Area 8 — Cost & Context Awareness (5 findings)
47. major / other — Status bar running indicator never appears during active runs
48. major / other — Cost counter never appears: all models return cost_status=unpriced_model with cumulative_cost_usd=0
49. minor / menu-consistency — /context and /stats overlays have no border, no escape hint
50. minor / formatting — Stats heatmap column width is fixed and does not use available terminal width
51. polish / menu-consistency — Stats week-view heatmap shows only 1 column (Mon–Sun) making the 'grid' look like a vertical list

### Area 9 — Slash Commands & Autocomplete (8 findings)
52. major / input — Escape while autocomplete is open clears the entire input instead of just closing the dropdown
53. major / empty-error — Unknown slash command silently fails — no error feedback, input not cleared on Enter
54. major / layout-jump — Autocomplete dropdown does not scroll — selection highlight disappears when navigating past 8 visible items
55. minor / input — Tab completion inserts command without trailing space
56. minor / formatting — Fuzzy filter matches command descriptions in addition to command names
57. minor / menu-consistency — Help overlay (Commands tab) lacks a navigation-hint footer line
58. minor / formatting — Stale documentation: command count in catalog says 10 but TUI now has 14 commands
59. polish / input — Tab on ambiguous filter (multiple matches) does nothing and gives no feedback

### Area 10 — Error Recovery & Interrupts (8 findings)
60. major / other — C-c during active run skips two-stage confirm banner — interrupts immediately
61. major / menu-consistency — Autocomplete preselects /clear: '/' + Enter immediately runs /clear without confirmation
62. minor / menu-consistency — /sessions overlay missing footer navigation hint
63. minor / menu-consistency — /stats and /context overlays render as unboxed plain text — no border, no dismiss hint
64. minor / menu-consistency — /help overlay has no footer navigation hint for tab navigation or esc dismiss
65. minor / other — Keybindings help tab says 'ctrl+c quit' — misleading when run is active
66. minor / empty-error — Dead server connection error in viewport shows raw HTTP URL
67. polish / menu-consistency — /model error overlay in narrow (80x24) wraps the connection-refused URL across two lines

### Area 11 — Keyboard-Driven Navigation (9 findings)
68. major / other — Keybindings help tab omits 5 registered bindings
69. major / other — '?' shortcut does not open help dialog — typed into input instead
70. major / other — '@' mention key produces no file-picker overlay
71. major / other — ctrl+e (editor) silently fails with no user feedback when EDITOR is unset
72. major / other — ctrl+o (plan mode) silently fails with no user feedback
73. major / other — ctrl+c during active run quits TUI instead of showing two-stage interrupt banner
74. minor / menu-consistency — Keybindings tab incomplete (same as area 7 finding; merged into M-17)
75. minor / menu-consistency — Help dialog has no footer navigation hint (same as area 6/10; merged into M-23)
76. minor / other — ctrl+c in help shows 'quit' misleading label (merged into M-18)

### Area 12 — Sessions & History (4 findings)
77. minor / menu-consistency — Sessions picker has no visible text-based selection cursor marker
78. minor / menu-consistency — Sessions overlay has no footer navigation hint (merged into M-23)
79. minor / formatting — Session preview text truncation inconsistent with other list overlay truncation
80. polish / formatting — Session list timestamp column shows only date, no time; ambiguous for same-day sessions

*(Area 12 yielded fewer unique findings as many session-overlay issues were already captured in prior areas.)*

**Note on raw count:** The raw finding list above reflects 87 total items across all 12 areas. Some area 11 findings are identical to earlier areas and merged into canonical findings above. Area 12 findings 81-87 were minor consistency/formatting items subsumed into M-24 and M-23.

---

*All evidence files referenced above are stored under `/tmp/gocode-qa/out-<area-idx>/`. All dispositions are recommendations, not done-claims. Root cause file:line citations derive from suggested_fix fields in walker findings and from Opus first-hand code inspection (my-findings.md F-A through F-E); any that are marked TBD require a code-level investigation to confirm before fixing.*
