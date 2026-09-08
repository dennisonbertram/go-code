# Engineering Log

## 2026-09-08 — Issue #1428 CLAUDE.md: tickets, real-run proof, delegation

- Three practices this session kept proving necessary were absent from
  `CLAUDE.md`, so each had to be re-established by instruction every time.
- **Issues are tickets to work, not places to park problems.** The existing
  discipline required an issue before implementation but never said the issue
  must then be worked. The predictable failure, observed twice this session, is
  stopping after filing to ask whether to proceed. Added: file, implement,
  verify, merge, close, in the same stretch of work; discovering separate work
  means filing it and finishing the task in hand, not stopping; and a diagnosis
  that turns out wrong must be corrected on the issue before implementing,
  never built against.
- **Green tests are not proof a change works.** `CLAUDE.md` already warned that
  merging is not shipping and binaries must be rebuilt; it said nothing about
  driving the real thing. Three green-and-wrong cases from this session are
  recorded in the file as evidence rather than exhortation:
  - #1415's truthful spinner label passed every test and still ate the cancel
    hint at 40 columns. Found by rendering the line.
  - #1420's colour detection passed its tests while never colouring stdout,
    because `style` runs inside command substitution where `-t 1` is false.
    Found by a pty capture.
  - #1424's model memory passed unit tests while the suite wrote into the
    developer's real `~/.config/harnesscli/config.json`. Found by reading the
    file.
  The requirement includes stating plainly what could *not* be exercised: an
  honest gap is a result, a silent one is a false claim.
- **`/efficient-fable` is now named as the default for token-heavy work**, with
  the split written down — searches, sweeps, log reduction, docs drafting and
  live captures delegated; architecture, diagnosis, final diff review and what
  to tell the user kept local. Also recorded: subagent reports are leads to
  verify, not facts to repeat, with the two real cases from this session where a
  confident subagent finding was wrong (a pre-existing unrelated build failure,
  and a test failure caused by another session's processes).
- Docs-only change: no rebuild required.

## 2026-09-08 — Issue #1426 --tui -model reaches the TUI

- Symptom: `harnesscli --tui -model X` parsed the flag and threw it away. The
  TUI started on the daemon default or, after #1424, on the remembered model —
  which is precisely what a user passing `-model` is trying to override. No
  warning, no error: the flag was silently discarded.
- Cause: `main.go:179` called `runTUI(*baseURL, workspacePath, *resume,
  *planMode)`. `*model` was never among the arguments, and `newTUIConfig` never
  set `TUIConfig.Model`. The non-TUI path (`main.go:216`) passed the flag
  correctly all along, so the defect was confined to the TUI branch.
- Fix: `model` threaded from the dispatch through `runTUI` into
  `newTUIConfig`, following the route `planMode` already takes. Nothing in the
  TUI changed — `selectedModel: cfg.Model` and #1424's `cfg.Model == ""` guard
  were already correct and simply had no producer of a non-empty value.
- Precedence, now real: flag, then the model remembered from last session, then
  the daemon default. The flag deliberately does **not** write to
  `~/.config/harnesscli/config.json`: a flag is a one-off instruction, not a
  preference, and silently rewriting the saved model would be a worse bug than
  the one being fixed.
- Tests: `TestNewTUIConfigCarriesExplicitModel` and, as its control,
  `TestNewTUIConfigWithoutModelLeavesItEmpty` — empty must stay empty, or a fix
  that defaulted to some model would pass the first test while breaking #1424.
  `runTUI` itself requires a terminal and cannot be unit tested, so
  `newTUIConfig` is the closest honest seam; the consumer half is already
  covered by `TestExplicitModelBeatsRememberedModel` in the `tui` package.
  Neither test alone proves the flag works — producer and consumer are pinned
  separately and meet only in the live check.

## 2026-09-08 — Issue #1424 remember the last used model

- Symptom: the TUI forgot the model you picked. `/model`, quit, restart, and you
  were back on the daemon default. `newTUIConfig` (`cmd/harnesscli/main.go:562`)
  never set `Model`, so `selectedModel` started empty every session.
- Fix: `harnessconfig.Config` gains `Model`, `Provider` and `ReasoningEffort`
  (all `omitempty`, so old files load unchanged and older binaries ignore them).
  `ModelSelectedMsg` persists all three through the existing
  `persistConfigField` idiom; the constructor applies them where starred models,
  gateway, keys and history are already applied. The three are stored together
  because they are chosen together — a provider or reasoning effort without its
  model describes nothing.
- Precedence: applying is guarded on `cfg.Model == ""`, so an explicitly
  requested model always wins. Remembering a preference must never override an
  instruction. Empty still means "let the daemon choose", so a first run and a
  corrupt config both degrade to today's behavior.
- Reused rather than invented: `~/.config/harnesscli/config.json` already
  persisted starred models, gateway, API keys, history and theme. The model was
  the conspicuous omission, and it is the one users change most.
- **Test-isolation defect this exposed, worth more than the feature.** Making
  model selection persistent meant the TUI test suite began writing to the
  developer's real `~/.config/harnesscli/config.json` — a run left
  `"model": "gpt-4.1-mini"` there that no human had chosen, and three tests then
  failed against it. Fixed with a package `TestMain` that redirects HOME once
  before any test runs, plus a per-test reset of the scratch config in
  `initModel`.
  - `t.Setenv` in a shared helper does not work here: it panics for any test
    that calls `t.Parallel`, and several in this package do. Redirecting once in
    `TestMain`, before any test starts, is both parallel-safe and race-free.
  - The reset is needed as well as the redirect: one scratch HOME is shared by
    the whole package, so without it a test that selects a model leaks into
    every test after it.
- Durable lesson: adding persistence to a UI component silently converts its
  test suite into something that mutates the developer's machine. When making
  anything persistent, check what the tests write before checking what the
  feature reads.
- Noted, not fixed here: `runTUI` never receives the `-model` flag, so
  `harnesscli --tui -model X` ignored it. Filed as #1426 and fixed there; the
  precedence guard above needed no change, exactly as predicted.

## 2026-09-08 — Issue #1422 interrupt test flake, and a diagnosis that was wrong twice

- Symptom: `TestGoCodeScriptStopsHarnessdOnInterrupt` failed on Linux CI with
  "wrapper did not exit within 10s of SIGINT", red-flagging PR #1421 whose diff
  touched only the spinner and docs. Passed on re-run.
- First diagnosis, wrong: that the stub daemon's foreground `sleep` made bash
  defer SIGTERM, forcing the wrapper down its 5-second graceful-shutdown wait on
  every run and leaving no headroom in a 10-second budget. Measured directly,
  the wrapper exits **0.21s** after a process-group SIGINT. A non-interactive
  bash does not defer SIGTERM behind a foreground child; it terminates. The
  5-second path is never reached.
- Second diagnosis, also wrong: that the stub did not really survive SIGINT,
  because `sleep` is a separate process that would die from the group signal.
  Checked directly — it survives. POSIX inherits an *ignored* disposition across
  exec, so `trap '' INT` in the stub makes the `sleep` ignore INT too. The
  test's premise is sound and that trap should stay.
- Confirmed defect, and the only one proven: the test called
  `cmd.Process.Wait()` twice — once in the deferred cleanup, once in the
  goroutine feeding the timeout `select`. `os.Process.Wait` is not safe to call
  twice on the same process. Introduced in #1417.
- Fix: one owner for `Wait` (the deferred cleanup now only kills); the stub
  daemon traps TERM and takes its `sleep` child with it instead of orphaning it;
  the exit budget widened to 30s as headroom for a loaded runner rather than as
  a claim about how long cleanup takes.
- **Root cause of the Linux failure remains unconfirmed.** Not reproduced on
  macOS: 8/8 under `-race` with four CPU hogs running, 3/3 idle. The issue stays
  open until CI shows a clean run without a re-run; a green that needed a re-run
  proves nothing.
- Durable lesson: when a test fails only on CI, measure the thing you are about
  to blame before writing the fix. Two plausible mechanisms here were both
  disproven in under a minute by timing the actual path, and either would have
  produced a confident fix for a problem that does not exist.

## 2026-09-08 — Issue #1420 spinner breathes instead of ticking

- Symptom: after #1415 stopped the *word* rotating, the owner reported the
  spinner still "spins too fast" and asked to "slow it down so that it
  breaths." The complaint was the glyph, not the label: six frames advancing
  one per 120ms tick is a full rotation every 720ms, with every frame weighted
  identically — a mechanical tick rather than a living indicator.
- Second, subtler problem: the frame order `✶ · ✻ ✽ ✳ ✢` was not monotonic in
  visual weight. It jumped from the heaviest glyph straight to the lightest and
  back, which reads as a stutter at any speed.
- Fix: the same six glyphs, reordered by weight and ping-ponged into a pulse
  (`· ✢ ✳ ✻ ✽ ✶ ✽ ✻ ✳ ✢`), with a per-step hold table
  (`3 2 1 1 2 3 2 1 1 2` ticks) so advance is gated rather than one-per-tick.
  The extremes hold 360ms, the mid-pulse frames pass in 120ms, and a cycle runs
  18 ticks — about 2.16s, against 720ms before. The uneven hold is the easing:
  a flat cadence is precisely what made it feel mechanical.
- Deliberately unchanged: `tui.SpinnerInterval` stays at 120ms. The same tick
  redraws the elapsed-time counter, so slowing the timer would slow the clock.
  Gating the glyph's advance slows only the glyph. The glyph set itself is also
  unchanged — it matches the six independently reported for Claude Code, and
  was never the problem.
- Provenance note, since it informed the design: the claim that Claude Code
  eases its frames (first and last held longer) comes from third parties
  hand-timing screen recordings, not from reading its source. That is a
  description, not a measurement, and the hold table here is our own choice
  rather than a reproduction of theirs.
- Tests: `TestSpinnerCycleTakesAboutTwoSeconds` (18 ticks, not 6),
  `TestSpinnerEasesAtTheExtremes` (extremes strictly outlast mid-pulse frames),
  `TestSpinnerPulseGrowsThenShrinks` (monotonic up then down, no heaviest-to-
  lightest jump), and `TestSpinnerStillAnimatesUnderEasing` as the control
  against a hold table that would freeze the animation. All in
  `cmd/harnesscli/tui/components/spinner/breathing_test.go`.
  `TestTUI024_SpinnerCyclesFrames` pinned one-frame-per-tick and was replaced by
  `TestTUI024_SpinnerAdvancesThroughThePulse`; the `#1415` glyph-animation guard
  widened its bound to the longest hold. Snapshot goldens regenerated.

## 2026-09-08 — Issue #1415 truthful spinner label

- Symptom/motivation: `cmd/harnesscli/tui/components/spinner/verbs.go` held
  `DefaultVerbs`, a pool of fifteen near-synonyms for "thinking" ("Thinking",
  "Reasoning", "Pondering", "Analyzing", "Processing", "Computing",
  "Synthesizing", "Evaluating", "Reflecting", "Deliberating", "Considering",
  "Examining", "Contemplating", "Strategizing", "Planning"). `verbRotateEvery
  = 8` re-picked one every 8 ticks at the 120ms tick rate, so the word changed
  roughly once a second while telling the user nothing new — motion without
  novelty reads as a stuck animation, not a live one. A comment on
  `DefaultVerbs` also claimed these were "the same verbs Claude Code uses in
  its thinking indicator," which was false.
- Research that informed the decision (secondary, not verified against the
  actual Claude Code source): an extraction across 139 published
  `@anthropic-ai/claude-code` npm versions (the `levindixon/tengu_spinner_words`
  repo) reports roughly 90 varied whimsical words plus a runtime-fetched
  Statsig dynamic-config set merged in, and a word-rotation interval of
  roughly 1000ms. Our own rotation was ~960ms (8 ticks × 120ms) — essentially
  the same speed. So speed was never the differentiator here; vocabulary was.
  Nobody on this project has extracted the literal interval from the minified
  Claude Code bundle, so that 1000ms figure is a well-sourced hypothesis, not
  a confirmed fact — treat it accordingly. By contrast, opencode
  (`anomalyco/opencode`, `packages/tui/src/component/spinner.tsx`) is a
  primary source we could read directly, and it uses no randomized words at
  all.
- Decision: matching a 90-plus-word decorative list would mean maintaining it
  against a scale we cannot verify, from a secondary source, to reproduce an
  effect that depends on a runtime-fetched set we do not have. Rather than
  that, or keeping the false "same as Claude Code" claim, say something true
  about what the run is doing. The false comment was removed along with `DefaultVerbs`.
- Fix: `verbs.go` now holds one `fallbackLabel = "Working"` constant
  (`cmd/harnesscli/tui/components/spinner/verbs.go:12`), used only when no
  action is known. `currentSpinnerAction()` in
  `cmd/harnesscli/tui/model.go:1175` returns a most-specific-first ladder: a
  tool genuinely still running -> "Running <tool>"; assistant text streaming
  (`responseStarted`) -> "Writing response"; reasoning deltas arrived
  (`thinkingText != ""`) -> "Thinking"; otherwise -> "Waiting for <model>" (or
  "Waiting for model" with none selected). It never returns "", which is what
  makes the verb pool unnecessary — there is always something true to say, and
  it is always narrower than "thinking". The glyph still animates every 120ms so the line reads as
  live; only the word now holds still until the state actually changes. The
  trailing "..." was dropped too ("Running bash" is a fact, not a vague one).
  `TUIConfig.SpinnerSeed` and the `spinnerSeed()` helper were removed — they
  existed only to make random verb selection deterministic for snapshots, and
  rendering is now deterministic by construction, so the field would have
  been a setting that no longer did anything. `spinner.New(seed)` keeps its
  parameter for its ~115 call sites but ignores it.
- Trade-off: "Waiting for gpt-4.1-mini" is much longer than the "Computing..."
  it replaced, and at 40 columns a right-truncated line ate the cancel hint,
  rendering "(esc to inter" with no way to tell the user how to stop the run.
  `shortenLabel` (`cmd/harnesscli/tui/components/spinner/model.go:235`) now
  drops the duration first, then truncates the label with an ellipsis, so
  "(esc to interrupt)" always survives — the label yields, the hint does not.
  At 40 columns this renders `✶ Waiting for gpt-4.… (esc to interrupt)`.
- Durable note: the six glyphs (`✶ · ✻ ✽ ✳ ✢`,
  `cmd/harnesscli/tui/components/spinner/model.go:18`) do match the six
  independently reported for Claude Code, so the glyph layer was already
  right and was deliberately left alone — only the word layer changed.
- Tests: added `TestSpinnerLabelDoesNotRotateOnTicks`,
  `TestSpinnerGlyphStillAnimates` (control against freezing the whole line),
  `TestSpinnerLabelIsSeedIndependent`, `TestSpinnerFallbackLabelWhenNoActionKnown`,
  `TestSpinnerKeepsCancelHintAtNarrowWidth`,
  `TestSpinnerHintSurvivesEvenWhenLabelCannotFit` in
  `cmd/harnesscli/tui/components/spinner/truthful_label_test.go`, and
  `TestCurrentSpinnerActionLadder` (5 cases) plus
  `TestCurrentSpinnerActionNeverEmptyWhileRunning` in
  `cmd/harnesscli/tui/spinner_truthful_action_test.go`. Removed
  `TestTUI024_SpinnerVerbFromSeed` and `TestTUI024_EmptyVerbFallback`, which
  pinned the deleted rotation behavior. Snapshot goldens regenerated:
  `✽ Computing... (esc to interrupt)` became `✽ Working (esc to interrupt)`.

## 2026-09-08 — Issue #1416 closed output pipe orphaned harnessd

- Symptom: `go-code runs | head -5` (or piping into a pager the user quits
  early) left `harnessd` running after the wrapper exited. The orphaned
  daemon holds the workspace's callback-recovery lock
  (`internal/harness/tools/delayed_callback_store.go:156`), so the next
  `go-code` invocation in that project died about 11 seconds later with
  `fatal: recover callbacks: acquire callback recovery authority: callback
  workspace is already owned: resource temporarily unavailable`. The
  wrapper's own error hint made it worse by suggesting a port conflict —
  advice that cannot work, since the lock is workspace-scoped, not
  port-scoped.
- Cause, confirmed with a `bash -x` trace rather than inferred: the script
  runs under `set -euo pipefail` (`scripts/go-code.sh:2`). `stop_server()`'s
  first statement after its guards was an `info "stopping harnessd (pid
  ...)"` call, which writes to stdout. With stdout already closed, that
  write fails, `set -e` aborts the function right there, and the `kill
  "$pid"` on the next line never runs. Cleanup was killed by its own status
  message. The trace ended exactly at:
  ```
  + info 'stopping harnessd (pid 42794)'
  + printf '%s %s\n' '[go-code]' 'stopping harnessd (pid 42794)'
  scripts/go-code.sh: line 99: printf: write error: Broken pipe
  ```
- Fix: `trap '' PIPE` at the top of `stop_server()` (`scripts/go-code.sh:178`)
  so cleanup can no longer be killed by a failed write — necessary on its
  own, because `|| true` alone did **not** fix the leak: a `SIGPIPE` taken
  while the `EXIT` trap is already running terminates the shell outright
  instead of returning control to the trap, which was verified by
  re-running the trace with only the `|| true` guard in place and still
  seeing the daemon leak. `|| true` was then added to the `printf` calls in
  `info`, `warn`, `die` (`scripts/go-code.sh:99-101`), and in
  `show_harnessd_log`, so a status line can never abort its caller under
  `set -e`. The `EXIT` trap is now armed in `start_server()`
  (`scripts/go-code.sh:281`) immediately after the daemon is spawned,
  replacing three separate per-mode `trap stop_server EXIT` arms in `main()`
  (tui/prompt/cli), so there is one owner of the cleanup contract and no
  window between spawning the daemon and arming its cleanup.
- The durable lesson: under `set -e`, a status message inside a cleanup path
  is load-bearing, and a `SIGPIPE` taken during an `EXIT` trap kills the
  shell rather than returning to it — so cleanup must be immune to write
  failures, not merely tolerant of them.
- The corrected diagnosis: issue #1416 was originally filed claiming Ctrl+C
  (`SIGINT`) caused the leak. That was wrong. The evidence was an artifact
  of how the reproduction was run: the wrapper was signalled alone rather
  than as a process group, so bash deferred its trap while a foreground
  child was running, and the liveness check happened while the wrapper was
  still alive — making it look like Ctrl+C failed to clean up when it
  actually just hadn't run yet. Ctrl+C is handled correctly; the real
  trigger is a closed output pipe, not a signal, and the fix was found by
  `bash -x` tracing the pipe case, not by reasoning about signals. An
  earlier draft of the fix added `INT`/`TERM`/`HUP`/`PIPE` signal traps and
  an `on_signal` helper; those were removed — they were written against the
  wrong mechanism and were not needed.
- Tests: `TestGoCodeScriptStopsHarnessdWhenOutputPipeCloses`
  (`cmd/harnesscli/go_code_script_test.go`) is the real bug, red first with
  `harnessd (pid 41667) still running after the wrapper's output pipe
  closed`. `TestGoCodeScriptStopsHarnessdOnInterrupt` covers two cases
  (wrapper-started daemon stopped; pre-existing daemon left alone),
  signalling the whole process group as a real Ctrl+C does; it passes today
  and is a guard against regression, not a red-first test.

## 2026-09-08 — Issue #1413 readable go-code startup output

- Symptom: `go-code` printed 13 lines of undifferentiated output on a normal
  startup, mixing three different voices with no visual separation — the
  wrapper's own `[go-code] ...` status lines, harnessd's own boot log (since
  the daemon inherited the wrapper's stdout), and (on failure) a fatal error —
  so the one actionable line was buried in the noise. Because the daemon
  inherited the terminal, a stray daemon log line could also render into the
  TUI after handoff, not just during startup.
- Cause: `scripts/go-code.sh`'s three one-line output helpers (`info`, `warn`,
  `die`) gave every line the same undifferentiated `[go-code] ...` prefix with
  no severity distinction, and `start_server()` let harnessd inherit the
  wrapper's stdout/stderr instead of capturing them.
- Fix: color detection now runs once at startup into `COLOR_STDOUT` /
  `COLOR_STDERR` (disabled under `NO_COLOR`, `TERM=dumb`, or when the relevant
  stream isn't a terminal), feeding a `style` helper; `info` is cyan-prefixed,
  `warn` yellow, `die` red, with the literal words `WARN:`/`ERROR:` always kept
  in the text so severity never depends on color alone. A wrapper-started
  harnessd now writes to `${TMPDIR:-/tmp}/harnessd.<pid>.log` (created with
  `umask 077`) instead of the terminal. On success the wrapper prints
  `server ready at <url>` plus a `log: <path>` line, and the daemon boot log no
  longer appears at all. On failure, `die` calls `show_harnessd_log`, which
  prints a `harnessd said:` block with the last 20 log lines indented four
  spaces (lines matching `fatal:`, `panic:`, or `refusing to start` in bold
  red, everything else dimmed) followed by `full log: <path>`.
- Gotcha (the durable lesson here): the first implementation tested `[[ -t 1 ]]`
  lazily, inside the `style` helper itself. But `style` is invoked from
  command substitution (`$(style ...)`), where `$( )` only redirects stdout —
  so inside that substitution, fd 1 is a pipe, not the terminal, and stdout
  was never colored even on a real tty, while stderr colored correctly. Color
  detection has to happen once at startup, before any command substitution
  runs. This was caught by looking at real rendered pty output, not by the
  test suite — the tests (`TestGoCodeScriptEmitsNoAnsiWhenNotATty`,
  `TestGoCodeScriptSurfacesHarnessdLogOnStartupFailure`, both in
  `cmd/harnesscli/go_code_script_test.go`) check the no-color and log-surfacing
  paths but don't exercise a real tty, so they would have passed either way.

## 2026-09-08 — Issue #1411 go-code wrapper bound harnessd beyond loopback

- Symptom: plain `go-code` (no flags) died on a clean machine with no API key
  store configured and no `HARNESS_AUTH_DISABLED` set — the daemon exited
  before becoming healthy, and the wrapper's failure hint blamed port
  contention.
- Cause: `scripts/go-code.sh:196` (pre-fix) hardcoded
  `HARNESS_ADDR=":${port}"` — a wildcard bind — when spawning `harnessd` for
  its own use, even though the wrapper's client `base_url` is always built as
  `http://127.0.0.1:${port}`. `cmd/harnessd/bind_guard.go` (issue #1328)
  refuses to start an unauthenticated daemon that listens beyond loopback, so
  the wider bind had no legitimate purpose and just tripped the guard.
- Fix: `start_server()` now spawns with `HARNESS_ADDR="127.0.0.1:${port}"`;
  the failure hint no longer asserts port contention as the only cause and
  points at the harnessd log above it instead; the header comment and
  `--help` text now say `HARNESS_ADDR` supplies only the port and that a
  wrapper-started `harnessd` always binds `127.0.0.1`.
- Security note: this is strictly tightening, not a new restriction users
  need to work around. Before the #1328 guard existed, this same line
  silently ran an unauthenticated agent-execution daemon on every network
  interface, reachable by anyone on the LAN with the user's provider
  credentials.
- Regression: `TestGoCodeScriptStartsHarnessdOnLoopback` in
  `cmd/harnesscli/go_code_script_test.go`, red before the fix with
  `harnessd bind address = ":19282", want "127.0.0.1:19282"`.
- Cross-reference: the 2026-09-05 `#1380` entry below first spotted this
  pattern in `internal/workspace/bootstrap.go:36` (VM cloud-init template),
  filed as a follow-up rather than fixed there; that sibling instance is
  tracked separately as issue #1392 and is unaffected by this fix.
- Left alone deliberately: `scripts/soak.sh:271-272`, `scripts/smoke-test.sh:87`,
  and `scripts/run-bench-smoke.sh:135-136` also bind `:${PORT}` (wildcard),
  but all three pass `HARNESS_AUTH_DISABLED=true`, so the bind guard admits
  them.

## 2026-09-06 — TUI scenario walk: plan mode, @ completion, question box, bubble width (#1407)

- Twelve multi-step TUI scenarios driven live in tmux (fake provider with scripted streaming/tool turns, and OpenRouter DeepSeek). Fixes: `/plan` command toggles enforced plan mode with a `PLAN` status badge and `harnesscli --tui --plan-mode` now honored (`runTUI` passes the flag into `TUIConfig`); `@name` Tab completion completes bare relative file names, not only `./`, `/`, `~/` paths; the AskUserQuestion and Plan-Approval boxes size their top/bottom borders to the content instead of a fixed 40-col rule; assistant markdown bubbles render at the indented width, expand tabs, and trim padding so a bubble never exceeds the terminal width. Guards added: streamed-transcript integrity and no duplicate tool card on ctrl+o after interrupt.
- Filed separately: the streamed-markdown truncation under a real color profile is a streaming re-render accounting defect (`ReplaceTailLines` vs. glamour reflow as `looksLikeMarkdown` flips), not the bubble width; not fixed here.

## 2026-09-06 — Settings overlays read wrong to a first-time user (#1405)

- Symptom: `/cost` showed `↑ 0 in  ↓ 15,760 out` after a run (the TUI only tracked a single total and passed it as output); `/profiles` wrapped its highlighted row mid-word; `/config` cut values at 20 characters with no ellipsis (the model id read as `deepseek/deepseek-v4`) and never explained `[RO]`, and showed an empty model cell before a model was chosen; `/permissions` drew a stray `──` line because its separator was as wide as the terminal inside a narrower box.
- Fix: `applyUsageDelta` keeps cumulative prompt/completion tokens for the cost snapshot; the profile picker sizes rows to the box content area; the config panel widens the value column to the dialog, ends cut values with `…`, adds `[RO] read-only` to the footer and a "(server default — use /model to choose)" placeholder; the permissions panel is sized to the overlay box. Config snapshot goldens regenerated. Live tmux captures in PR.

## 2026-09-06 — A chat message could be saved as an API key (#1403)

- Symptom: in the TUI, selecting a model whose provider had no key jumped to the API Keys panel with no explanation; letters typed while the panel was open fell through into the chat input; Enter then opened the key form, and the next text plus Enter (`/model`) was stored as the DeepSeek key both client-side (`~/.config/harnesscli/config.json`) and on the daemon. Keys rows also wrapped inside the box and `kimi-subscription` was labelled "ChatGPT subscription"; the picker had no legend for `●/○/(n)` and sorted providers case-sensitively.
- Cause: the final key-routing fallthrough in `cmd/harnesscli/tui/model.go` had no overlay guard; the key form accepted any non-empty string; the redirect set no message; the keys box had a fixed 54-column width with 14/24-column fields.
- Fix: swallow printable keys while an overlay is open (status hint), validate keys (no whitespace, not starting with `/`), set `apiKeyReason` on redirect and render it under the panel title, size the keys box to its rows and truncate with `…`, product-specific subscription labels, a legend line in the picker footers, case-insensitive provider order. Live tmux captures at 120x40 in PR #1404.

## 2026-09-06 — Slash-command menu polish (#1401)

- Symptom: driving `harnesscli --tui` in tmux, Tab ignored the item highlighted with ↑/↓ (the input box's prefix completer handled the key), Enter on a bare `/` ran `/add-dir`, a query with no matches made the menu vanish, descriptions were chopped mid-word at 40-60 columns, a blank row appeared under the menu, no key hint was shown, and the menu added rows to the screen (38 → 47 at 120x40) instead of borrowing them from the transcript.
- Cause: `components/slashcomplete/view.go` returned "" for empty results and a trailing newline otherwise, truncated by rune count without an ellipsis, and the model only wired Enter to the dropdown; the screen stack appended the dropdown without shrinking the viewport.
- Fix: `Model.HasUserChoice` (query typed or ↑/↓ used) gates Enter; a Tab arm in `model.go` accepts the highlighted item into the input; `View` renders a dim no-match row, `…` truncation, a full-width highlight bar, a footer hint, and no trailing newline; `View()` drops the top rows of the main content by the menu height. Snapshot goldens regenerated. Live tmux captures at 120x40, 60x20 and 40x15 attached to the PR.

## 2026-09-05 — Issue #1372 workspace_path silently ignored

- Cause: harnesscli (`resolveWorkspacePath`, defaulting to the CLI's cwd) and
  the TUI both sent `workspace_path` on `POST /v1/runs`, but
  `harness.RunRequest` had no such field and `handlePostRun` decoded without
  `DisallowUnknownFields`, so the field was silently dropped. Every run's
  file/shell tools operated under the daemon's own `HARNESS_WORKSPACE`
  regardless of the caller's actual working directory, with no error surfaced.
- Fix: added `RunRequest.WorkspacePath` (`workspace_path`), validated at
  `StartRun` the same way `ExtraDirs` is (absolute path, must exist, must be a
  directory — otherwise a synchronous 400). When set and no `WorkspaceType`
  provisioning is requested, `runPreflight` routes the run through the same
  per-run tool registry seam already used for provisioned workspaces
  (`NewDefaultRegistryWithOptions(wsPath, rc.BaseRegistryOptions)`), and emits
  `workspace.provisioned` with the explicit path. `completeRun` now records
  the run's effective workspace root (`permissionWorkspaceRoot`) on the
  conversation instead of unconditionally `WorkspaceBaseOptions.RepoPath`, so
  `/conversations` and rewind reflect where the run actually operated.
- Regression: `TestStartRun_WorkspacePathRootsToolsAndSandboxConfinement`
  proves a write lands under the requested root and a write to an absolute
  path outside it is refused by the sandbox; `TestPostRunWorkspacePathNonexistentReturns400`/
  `TestPostRunWorkspacePathRelativeReturns400` prove the 400; and
  `TestStartRun_WorkspaceTypeProvisioningTakesPrecedenceOverWorkspacePath`
  proves explicit `WorkspaceType` provisioning still wins when both fields are
  set. ACP and MCP `start_run` paths were checked and neither sends a
  workspace field today; both still compile unchanged.
## 2026-09-05 — Issue #1376 remove implicit 8-step run cap

- Symptom: with no `max_steps` configured anywhere, a run whose model made 8
  tool calls ended `failed` with `error: "max steps (8) reached"`, even though
  `internal/config.Defaults()` documents `MaxSteps: 0` as unlimited and the
  runner's `effectiveMaxSteps` already treats `0` as unlimited.
- Cause: `cmd/harnessd/config_reload.go`'s `loadHarnessConfig` silently reset
  `MaxSteps` from `0` to `8` whenever `HARNESS_MAX_STEPS` was unset, and
  `cmd/harnesscli/tui/config.go`'s `DefaultTUIConfig` also defaulted to `8`.
  Both defaults were purely a daemon/TUI-layer artifact — the config schema,
  `applyProfileDefaults`, and the runner never intended this.
- Fix: removed the `harnessCfg.MaxSteps == 0 && getenv("HARNESS_MAX_STEPS")
  == "" { harnessCfg.MaxSteps = 8 }` rule from `loadHarnessConfig`, and
  changed `DefaultTUIConfig().MaxSteps` from `8` to `0`. Explicit caps set via
  config, `HARNESS_MAX_STEPS`, a profile (e.g. the built-in `full` profile's
  `max_steps = 30`), or a per-run request field are unaffected — only the
  implicit substitution when nothing was set is removed.
- Regression coverage: `TestLoadHarnessConfig_NoDefaultStepCap` asserts the
  resolved config keeps `MaxSteps == 0`; `TestDaemonConfigPath_RunSurvivesMoreThanEightSteps`
  runs the exact issue repro end to end (a fake provider scripted with 8
  tool-call turns plus a final answer turn) through the full
  `loadHarnessConfig -> assembleRunnerConfig -> harness.Runner` daemon wiring
  and asserts the run completes rather than failing at the 8th step.
  `TestTUI010_DefaultTUIConfigHasNoStepCap` covers the TUI default.
- Docs: updated `website/docs/concepts/configuration.md`,
  `website/docs/concepts/runs-and-conversations.md`,
  `website/docs/reference/environment-variables.md`, and removed an
  obsolete troubleshooting entry in `website/docs/reference/troubleshooting.md`
  that described the old implicit-8 behavior as intentional.
  `docs/runbooks/golden-path-deployment.md`'s `max_steps = 30` table row was
  checked and left unchanged — it documents the `full` profile's own explicit
  `max_steps = 30` (`internal/profiles/builtins/full.toml`), which is a
  real, unrelated explicit cap, not the implicit daemon default this issue
  fixed.
- Verification: `go test ./cmd/harnessd ./cmd/harnesscli/... ./internal/config
  -race` and `go vet` on the same packages, all green.

## 2026-09-05 — Issue #1380 website/docs staleness correction

- Scope: `website/docs/**` reference/concept/tutorial/server pages plus the one-line
  `harnesscli service install --addr` help-text default at `cmd/harnesscli/service.go:410`.
  A prior read-only audit (attached to epic #1369) flagged 19 website pages with
  verified-wrong claims; every claim was re-verified against the current tree with `rg`
  before being corrected (the audit was treated as a lead, not gospel).
- Bind default: `HARNESS_ADDR` changed to `127.0.0.1:8080` in issue #1328
  (`internal/config/config.go:254`) with a non-loopback-bind refusal
  (`cmd/harnessd/bind_guard.go:29-42`); ~15 stray `:8080` claims across
  reference/getting-started/concepts/server/tutorials pages were corrected to match,
  and the refusal behavior is now documented next to each default.
- Resume story: `harnesscli continue` / `POST /v1/runs/{id}/continue` requires the
  source run's status to be `completed` and returns 409 `run_not_completed`
  otherwise (`internal/harness/runner.go:2217`, `internal/server/http_runs.go:817`);
  a cancelled run cannot be resumed. Corrected in `reference/exit-codes.md`,
  `cli/harnesscli.md`, `cli/go-code-wrapper.md`, `server/http-api-guide.md`, and
  `concepts/runs-and-conversations.md`, which previously told operators to
  `harnesscli continue` a blocked or cancelled run.
- Route inventory (`reference/http-routes.md`) was rebuilt from the actual
  `mux.Handle`/`mux.HandleFunc` registrations in `internal/server/http*.go`: added
  `/v1/tools`, `/v1/hooks`, `/v1/config/reload`, `/v1/model-settings*`, `/v1/tasks`,
  `/v1/jobs/{id}/kill|output`, `/v1/callbacks/{id}/cancel`, `/v1/cron/runs`,
  `/v1/cron/jobs/{id}/executions`, `/v1/conversations/{id}/events|rewind-points|rewind|undo`,
  `/v1/runs/{id}/replay`, `/v1/providers/{name}/import-subscription`, the 6
  `/v1/relay/*` control-plane routes, and `/viz`. The `RunRequest` schema block gained
  `attachments`, `plan_mode`, `plan_file`, `extra_dirs`, `denied_tools`, `rules`, and
  `workspace_path` (documented as the post-#1372 state — the field does not exist in
  `RunRequest` yet on this branch).
- Events catalog (`reference/events-catalog.md`): `AllEventTypes()` returns 79 of the
  86 event constants actually declared and emitted; the 7 gap events
  (`callback.dispatching|failed|retry_wait|started`, `plan.approval_required|granted|denied`)
  are genuinely emitted via a separate string-keyed bridge
  (`internal/harness/tools/delayed_callback.go`, `internal/harness/plan_mode.go:75-107`) and
  are now documented alongside a corrected 86-event total. Also added `todos.updated` and
  `job.completed` (`EventBackgroundJobCompleted`, delivered to the conversation stream, not
  the originating run), and fixed a stale `"tool": "ask_user_question"` payload example to
  the real `"AskUserQuestion"` value.
- Tools catalog (`reference/tools-catalog.md`): added `list_models`, `deploy`, `goals`,
  `cron_update`, `cron_history`, `message_subagent`, `notify_parent`, and `agent_swarm` —
  all verified as real `Definition{Name: ...}` registrations in
  `internal/harness/tools_default.go`. Rejected the audit's `compact_summary` entry after
  verification: it is an internal message-tagging label used by the `compact_history` tool,
  not a callable tool.
- Providers: `catalog/models.json` has 15 providers, not 10 — added `cerebras`,
  `codex-subscription`, `kimi-subscription`, `lmstudio`, `ollama` to
  `reference/providers-and-models-reference.md`, `reference/environment-variables.md`,
  `concepts/providers-and-models.md`, and `getting-started/what-is-go-code.md`. Live model
  discovery is provider-agnostic (OpenRouter, OpenAI, Anthropic, DeepSeek all refresh on a
  5-minute TTL via `internal/provider/openai/discovery.go:24` and
  `internal/provider/anthropic/discovery.go:24`), not OpenRouter-only as previously stated.
- `max_steps`: `0` means unlimited with no daemon-level fallback to a non-zero cap
  (documented as the post-#1376 state — `cmd/harnessd/config_reload.go:44-47` on this
  branch still resets a resolved `0` to `8`).
- `server/expose-as-mcp-server.md` needed a substantial rewrite beyond the audit's
  findings: `/mcp` is mounted via `harnessmcp.NewHTTPHandler`
  (`cmd/harnessd/runtime_container.go:348-352`), not `internal/mcpserver.NewServer` (which
  has no production caller). `/mcp` and the `harness-mcp` stdio proxy share the same
  25-tool, REST-backed dispatcher (`internal/harnessmcp`) — the page previously described
  them as two different tool sets (10 vs. 5) and claimed `/mcp` supports `GET` SSE
  notifications via `subscribe_run`; the handler is POST-only and 405s on GET
  (`internal/harnessmcp/httptransport.go:29-31`). Same "five tools" and unmounted-SSE
  staleness also existed in `tutorials/claude-desktop-mcp.md`, corrected there too.
  `list_mcp_resources`/`read_mcp_resource` are implemented (`cmd/harnessd/mcp_setup.go:68-90`,
  `internal/mcp/mcp.go:231,246`), contradicting the "not yet implemented" claim in
  `integrations/mcp-consume.md`.
- Added missing `harnesscli` subcommand documentation (dispatch table in
  `cmd/harnesscli/auth.go`): `steer`, `viz`, `acp`, `plugin`, `mcp`, `hooks`, `service`,
  `auth kimi`, `auth codex`, and `input` (documented as the post-#1374 CLI addition; the
  route `POST /v1/runs/{id}/input` it calls already exists today).
- One-line code fix: `cmd/harnesscli/service.go:410`'s `--addr` flag help text said the
  resolved default was `:8080`; corrected to `127.0.0.1:8080` to match
  `internal/config/config.go:254`. No test asserted the old string;
  `go test ./cmd/harnesscli -run Service -race` passes unchanged.
- Verification: `rg` re-check of every corrected route/flag/env/tool/event against the
  current tree; `npm run build` in `website/` (Docusaurus) completed with no broken
  internal links or MDX errors.
- New finding, not fixed here (outside `website/docs` + the one help string): the VM
  workspace cloud-init template (`internal/workspace/bootstrap.go:36`) sets
  `HARNESS_ADDR=:8080` (a non-loopback bind) with no `HARNESS_AUTH_DISABLED` and no
  configured auth, so `bind_guard.go`'s #1328 protection likely now rejects `harnessd`
  startup on provisioned VM workspaces; `systemctl start harnessd || true` would swallow
  the failure silently. Filed as a follow-up rather than fixed in this docs-only PR.

## 2026-08-08 — Issue #1285 attached lifecycle PTY (in implementation)

- Planned boundary: ptyrunner will accept only the typed identity returned by
  `scheduledlifecycle.Lifecycle.PTY()`. It will start a 100x30 harnesscli PTY,
  never harnessd, so a later cron/callback proof cannot silently cross daemon
  boundaries.
- Regression/fix: the first real two-message run initially wrote sealed frames
  into the package working directory because its collector lacked
  `artifactRoot`; a subsequent run correctly rejected the stale immutable name.
  Assigning the supplied artifact root before frame collection restores
  containment. The focused normal/race run now proves both source-SHA-bound
  identity and same-conversation two-message frame sealing.
- Independent-review P1 repair: a boolean active state could accept an old
  action handle after the next action became active, allowing historic bytes to
  receive stale credit. Session-owned monotonically increasing action tokens
  now require pointer/token/name identity and reject concurrent/double seals.
  Action labels must be safe relative slugs and all attached output is derived
  through a canonical non-root artifact directory; traversal, absolute labels,
  and root output fail before writing.
- Full-suite repair: the real lifecycle deadline originally began before its
  two disposable Go binaries were built. `-coverpkg` setup could therefore
  consume the acceptance window and falsely report that the first prompt had
  not created a run. The deadline now starts immediately before lifecycle
  startup, keeping build setup outside the behavior being measured.

## 2026-08-07 — Issue #1268 PTY EOF drain before success cleanup

- Symptom: acceptance artifacts could be raw-empty or omit a terminal tail
  after an otherwise successful real TUI exit.
- Cause: both success paths in `internal/acceptance/ptyrunner` called
  `master.Close` after `ptyCmd.Wait` but before the sole collector observed
  EOF. Closing the master can cancel the pending reader and lose unread PTY
  bytes.
- Fix: both success paths now defer master cleanup immediately after collector
  setup, then perform `Wait -> collector.waitEOF -> sealFinal`. Review found
  that separate LIFO defers could run process cleanup before master close on an
  error return, so both now use one tested helper that closes the master before
  process cleanup. The Linux slave-close fixture now proves EOF and final bytes
  with `context.Background` before deferred master cleanup.
- Compatibility: no harnessd, API, TUI, tool, timeout, artifact-format, or
  persistence behavior changed. Linux `EIO` remains normalized only after any
  returned bytes are appended.
- Verification: focused normal/race repetitions and package normal/race pass;
  full regression is the remaining pre-PR command.

## 2026-08-07 — Issue #1264 Runner test recursive-RLock deadlock

- Symptom: GitHub's race test for the untrusted fork-policy assertion ran for
  nearly twenty minutes and blocked #1263.
- Cause: the test held `Runner.mu.RLock`, called `forkedRunID`, and the helper
  tried a second `RLock`. A concurrent terminal child queued the production
  pruning writer; Go's writer-preference behavior then blocked the nested read
  while the outer read could not release.
- Repair direction: copy only required child test fields under a sole helper
  `RLock`, then assert after unlock at both recursive call sites. Production
  Runner locking/pruning is intentionally unchanged.
- Review correction: `PermissionConfig` can carry mutable data, so the
  snapshot retains only its asserted `Sandbox` and `Approval` scalar values;
  diagnostics render those scalars rather than dereferencing a post-unlock
  container.
- TDD evidence: the pre-fix bounded local race target compiled and passed once
  because the writer schedule did not arise; issue #1264 retains the decisive
  hosted timeout stack. The repair's focused policy tests then passed 50 normal
  and 50 race repetitions without a timeout or assertion relaxation. Complete
  `go test -race ./internal/harness/...` and
  `./scripts/test-regression.sh` then passed; the latter reported 85.2% total
  coverage and zero uncovered functions.

## 2026-08-07 — Issue #1260 resumed TUI reply race

- Symptom: the harness persisted a fresh same-conversation assistant reply,
  while the resumed 100x30 TUI transcript omitted it.
- Cause: a terminal bridge message discarded its server `run_id`; it could
  settle a newer run, while shared assistant finalization suppressed one SSE
  copy before global ID dedupe discarded the other.
- Fix: retain terminal `RunID`, reject stale run terminals, and scope terminal
  finalization/pre-start assistant accumulation to a run ID.
- Regression: deterministic conversation/run ordering tests cover same-ID
  copies, conversation-before-start, terminal-before-final, and late prior-run
  terminal order.

## 2026-08-07 — Issue #1261 continuation conversation identity

- Cause: `POST /v1/runs/{id}/continue` created a child run in the source
  conversation but returned only the child run ID. A blank TUI then applied
  its fresh-run fallback and subscribed to `/v1/conversations/{child}/events`,
  which correctly returned 404 even after the assistant had replied.
- TDD: the server response first omitted inherited `conversation_id`; TUI
  regressions then selected the child endpoint and skipped legacy identity
  lookup.
- Repair: the continuation response now adds the child run's authoritative
  `conversation_id`. `RunStartedMsg` transports it; a blank reducer adopts it,
  while an already selected mismatched conversation is not overwritten.
  Older servers trigger an authenticated child-run GET and fail visibly if it
  cannot establish identity, rather than inventing child-as-conversation.
- Verification: focused normal/race server and TUI tests passed; real 30x100
  (100 columns × 30 rows) PTY `/resume` and `/continue` evidence passed; and
  the exact rebased full gate passed at 85.2% coverage with zero uncovered
  functions (`/private/tmp/gocode-1261-rebased-regression.log`).

## 2026-08-07 — Issue #1254 Child Task Completion

- Cause: `spawn_agent` forwarded a child allowlist but the new child run never activated its deferred `task_complete`; the step engine also treated its validated marker like ordinary tool output and requested another provider turn.
- Fix: `RunForkedSkill` marks only internally-created depth-positive children for per-run completion activation. The Runner retains that child-only marker through allowlist, selected-profile, and skill filtering while root runs remain excluded. The step engine rejects a mixed completion turn before executing siblings and terminally completes only a successful, validated marker; existing terminal paths clean activation state.
- Regression coverage: restrictive child activation/one provider turn, root non-visibility, mixed sibling mutation rejection, malformed marker continuation, and terminal activation cleanup.

## 2026-08-07 — Issue #1255 Trusted Fork Origin

- Correction: a public `ForkDepth` context value could be forged and direct fork callers could receive child control semantics. The step engine now installs a private capability binding the current Runner and live parent run; `RunForkedSkill` validates it, derives depth from stored parent state, and otherwise creates an ordinary root-like run with no control activation.
- Precedence: mandatory child completion survives restrictive allow/profile/skill filters, but an explicit `DeniedTools` entry, pre-tool hook, or permission rule still denies it. This preserves explicit security policy while keeping normal `spawn_agent` children reliable.
## 2026-08-07 — Issue #1256 trusted rewind workspace

- Cause: rewind capture could create its point before conversation metadata was
  durable; terminal persistence then stored an empty workspace, especially for
  the default tenant, so the intentionally strict restore route returned 404.
- TDD: default and named tenant cases observed a captured point with an empty
  owner workspace on exact `36237749`.
- Repair: SQLite now has a metadata-only upsert that records the configured
  `WorkspaceBaseOptions.RepoPath` before capture; terminal persistence stamps
  the same canonical root for every tenant. Fork and 404 route boundaries are
  unchanged.
- Verification: focused runner/server normal tests pass; full regression and
  real 30x100 PTY evidence remain required before delivery.
- Concurrency repair: preserving a zero-workspace terminal run through a
  runner-side read then write still raced a concurrent configured mutating
  run. `PreserveConversationMeta` now makes the non-empty workspace/tenant
  decision inside one SQLite statement; a concurrent empty writer cannot erase
  the trusted root. A focused race regression starts configured and empty
  writers together and requires the configured root to survive.

## 2026-08-07 — Issue #1246 selected-session TUI rehydration

- Symptom: a rendered `/sessions` overlay listed durable conversations, but
  Enter left the transcript server-only and `/search` could not find its reply.
- Cause: the global Submit handler consumed Enter before the generic sessions
  overlay router; the picker never emitted `SessionPickerSelectedMsg`.
- TDD red: `TestIssue1246_SessionSelectionRehydratesDurableTranscript` failed
  on exact `f8b43be` with `Enter on sessions overlay must emit a
  selected-session command`.
- Fix: route sessions-overlay Submit through `sessionPicker.Update`, translate
  its component message once, and start the existing atomic selected-session
  replay boundary. Unsupported servers retain the existing cancel-before-GET
  fallback; an empty legacy cursor stays snapshot-only. No API change.
- Verification: normal and race `TestIssue1246_` passed; full
  `./scripts/test-regression.sh` passed (85.1% total coverage, zero uncovered
  functions). Exact-head 30x100 PTY evidence at
  `/private/tmp/gocode-issue1246-pty-20260807T154241` selected `conversation_a`,
  rendered `FIRST_REPLY`, returned `Search: FIRST_REPLY (1 result)`, then
  rendered `POST_SELECTION_REPLY`; the durable post-selection run was recorded
  in that same selected conversation.
- Review repair: selected sessions now request the supported atomic boundary
  rather than a GET-first handoff. Regressions prove one supported boundary
  stream/no history GET, snapshot-plus-same-text future behavior, and an
  unsupported empty-cursor fallback that remains snapshot-only.
## 2026-08-07 — Issue #1252 explicit model-facing cron execution mode

- Cause: the deferred model-facing `cron_create` handler silently selected
  `shell` when `execution_type` was omitted. A recurring job could therefore
  fire and retain shell history while producing no child conversation run.
- TDD: `TestCronCreateRequiresExplicitExecutionType` first failed because the
  schema required only name/schedule and the handler accepted omission.
- Repair: `execution_type` is now required in both schema and handler. Agents
  explicitly choose `shell` with a command or `harness` with a prompt; the
  latter retains immutable run scope and the existing same-conversation child
  execution path. Persisted shell jobs and REST/operator APIs are unchanged.
- Verification: focused model-tool and composed embedded harness tests pass;
  full regression and owned PTY evidence remain required before PR completion.

## 2026-08-07 — Issue #1249 causal replay-boundary repair

- Review finding: an SSE marker followed by an independent `/messages` GET
  still allowed the GET snapshot to include a later event before its queued SSE
  frame was processed; client cursor reconciliation could not prove which copy
  to render.
- Repair: `Runner.SubscribeConversationSnapshotFrom` captures messages, replay,
  and live subscription under one conversation sequence/event critical section.
  The opt-in marker carries that snapshot; the TUI suppresses every pre-marker
  frame, renders the marker snapshot, and routes all later events through the
  normal reducer. Legacy HTTP 200 without acknowledgement now cancels before
  GET-first fallback and restart.
- Evidence: A–F ordering/fallback regressions, full TUI normal/race, and the
  full regression gate passed (85.1% coverage; zero uncovered functions).

## 2026-08-07 — Issue #1249 conversation history/SSE replay boundary

- Cause: a selected TUI conversation fetched and rendered messages before it
  opened the empty-cursor conversation stream. Empty cursor is a valid durable
  fallback, so the server correctly replayed the historic assistant event and
  the user saw it twice.
- TDD: `TestResumedConversationEmptyCursorReplayBoundaryRendersHistoricAndFutureOnce`
  first timed out under the GET-first implementation. It now holds the
  messages response after the replay marker, injects a scheduled future event
  while that fetch is blocked, and proves historic and future transcript rows
  each occur exactly once.
- Repair: only an opt-in conversation SSE request uses the new boundary. The
  server registers its existing subscriber, writes historic replay, then sends
  an id-less `conversation.replay.completed` marker. The TUI suppresses
  pre-marker replay, fetches/render the snapshot after the marker, and uses
  only event order/cursor identity to discard buffered already-snapshotted
  events. Empty snapshot cursor retains all post-marker live events.
- Scope: no schema, tool, cron/callback execution, provider, GUI/native, or
  public-route change. Existing SSE callers do not opt in and retain their
  current replay wire behavior.

## 2026-08-07 — Issue #1247 cron core-tool documentation and registry contract

- Cause: `NewDefaultRegistryWithOptions` had already promoted all eight scoped
  cron tools to its core list, while website docs still advertised six deferred
  tools and instructed agents to call `find_tool`. That stale contract produced
  the invalid deferred-discovery premise in #1245.
- TDD: the first focused test read the public cron, tool-tier, and glossary
  pages and failed on the deferred wording and omitted `cron_update`/
  `cron_history` (and incomplete tier list). The registry assertion now also
  rejects any cron definition returned by `DeferredDefinitions`.
- Repair: public docs name all eight initial-turn core tools and state that
  they are called directly. No default-registry assembly, `find_tool`, scoped
  client, authorization, scheduling, persistence, or product-client code
  changed.
- Verification: focused normal/race `internal/harness` tests passed. The
  SHA-bound external-cache full regression log
  `/private/tmp/gocode-1247-full.log` passed normal, race, 85.1% total
  coverage, and zero uncovered functions.

## 2026-08-07 — Issue #1243 raw SSE header/envelope identity proof

- Cause: #1231 decoded event JSON while discarding `event:` and used `id:` as
  a fallback when the JSON envelope omitted its ID. A malformed stream could
  therefore look internally consistent after test-only reconstruction.
- TDD: the new decoder contract was first red because no provenance-preserving
  decoder existed. The permanent table rejects missing `id:`/`event:`, empty
  JSON ID/type, and header/JSON ID or type mismatches; it also proves a
  comment-only ping is ignored and a multi-`data:` JSON event remains valid.
- Repair: decoded test frames retain header ID/event and JSON envelope
  separately. Every data-bearing frame requires both nonempty representations
  and exact equality; headers never synthesize JSON identity. The existing
  #1241 `(runID, callID)` matcher now consumes those verified frames, retaining
  its valid concurrent A/B lifecycle behavior.
- Scope: acceptance test/docs only. `internal/server.writeSSE`, HTTP wire
  behavior, tools, persistence, clients, schedules, and callbacks are not
  changed.
- Verification: focused normal/race (including the real one-daemon #1231
  four-turn fixture) passed. The external-cache full regression passed normal,
  race, 85.1% coverage, and zero uncovered functions; log
  `/private/tmp/gocode-1243-full.log`.

## 2026-08-07 — Issue #1241 API raw-SSE tool lifecycle proof

- Cause: the #1231 acceptance helper first collected every start and every
  completion, then compared the two slices by index. A raw stream with a
  completion before its matching start passed that assertion.
- TDD: the first focused test fed exactly that stream and failed only at its
  explicit regression sentinel, proving the old assertion falsely accepted it.
  The permanent table covers completion-before-start, orphan completion,
  duplicate start/completion, name mismatch, wrong-run frame, unfinished
  start, concurrent A/B completion reversal, and later-run call-ID reuse.
- Repair: validation now walks decoded raw frames in order and keys state by
  `(runID, callID)`. It binds each start to one expected name/canonical-JSON
  argument multiset item, accepts any later matching completion, and rejects
  error output, extra/unmatched lifecycle frames, or an invalid terminal. The
  wrapper receives the HTTP-created expected run ID rather than inferring it
  from the first raw frame, so a wholly wrong-run stream cannot self-authorize.
- Scope: `cmd/harnessd` test-only evidence code. It does not change Runner,
  server SSE transport, tool implementations, stored data, or user clients.
- Verification: focused normal and race runs, including the real one-daemon
  #1231 four-turn API acceptance, passed. The SHA-bound full regression passed
  normal, race, and coverage at 85.1% total with zero uncovered functions.
- Delivery: code commit `8f8c58b6a2486acd3b8ef31f117be744ba6baf35` is pushed
  in stacked PR #1242 against #1232's branch. The independent code review
  approved that code head; this documentation amendment deliberately requires
  a fresh exact-head read-only review before any stack promotion.

## 2026-08-07 — Issue #1237 workflow terminal-history SSE completion

- Cause: `handleWorkflowRunByID` flushed every persisted workflow event, then
  unconditionally selected on the live subscriber channel. Plural workflow
  subscriber channels intentionally stay open after a terminal event, so late
  clients received `workflow.completed`/`workflow.failed` and then hung until
  they cancelled their request.
- Repair: after writing and flushing a terminal history event, the HTTP handler
  returns and its existing deferred cancellation releases the subscription.
  Nonterminal history still enters the existing live loop and terminates only
  when a live terminal event arrives.
- Regression: red-first completed/failed endpoint cases hold the live channel
  open and require a deterministic handler return with exactly one terminal
  frame. The nonterminal control verifies ordered history, live progress, and
  live terminal frames before return.
- Verification: focused normal/race and full `internal/server` package tests
  passed; `./scripts/test-regression.sh` passed normal, race, and coverage
  (85.1% total; zero uncovered functions).
- Scope: server control flow only; no #1236 subscription handshake, event
  schema, persistence, auth, client rendering, or script-workflow change.

## 2026-08-07 — Issue #1236 plural workflow subscription handoff

- Cause: `internal/workflows.Engine.Subscribe` copied persisted history before
  registering its live channel. An emit after the Store snapshot and before
  registration was persisted but reached neither returned history nor live
  stream; a burst was likewise lossy. Repeated cancellation could also close
  the same channel twice.
- Fix: register a per-run subscriber under the engine mutex with a sequence
  watermark, fetch history unlocked, trim it at the watermark, then atomically
  fold and clear the initializing pending buffer. `GetEvents` failure removes
  the entry; cancellation checks membership before close.
- Scope: plural `internal/workflows` engine only. The adjacent HTTP workflow
  SSE handler hangs after terminal history because it unconditionally enters
  the live loop; that independent issue is #1237.
- TDD: the controlled snapshot/return seam first failed with a lost event,
  lost 64-event burst, and double-cancel panic. Focused normal/race and repeat
  stress are green.
- Durable review correction: a fresh engine began with `eventSeqs[runID] == 0`,
  so the first watermark implementation trimmed durable pre-restart events and
  reused sequence 1. `Store.LastEventSeq` now hydrates each run exactly once
  before either Subscribe registration or emit; Memory and SQLite implement the
  read-only high-water lookup. Fresh-engine replay, next-sequence, and
  concurrent Subscribe/emit initialization are red-first regressions. The
  prior full gate does not cover this corrected head; a rebased full gate is
  required before the PR is updated.

## 2026-08-07 — Issues #1234/#1235 PTY evidence repairs

- Cause: an append-only PTY history could let a new action find an old matching
  frame; Linux PTY master close reports `EIO` after a slave exits normally.
- Repair: input-time barriers bind offset/version/baseline; semantic VT
  boundaries evaluate complete predicates and enforce visible transition
  chronology. The collector keeps final bytes then normalizes only EIO/EOF/
  closed-master lifecycle outcomes.
- Scope: acceptance infrastructure only; no product TUI, harness API, GUI,
  tools, cron, callback, persistence, or macOS behavior changes.
- Verification: full external-cache regression passed (85.0% coverage, zero
  uncovered functions); hosted checks and independent exact-head review remain
  promotion gates.
- Review repair: explicit collector regressions retain final `n > 0` bytes
  with wrapped EIO, assert nil EOF/read error and successful `waitEOF`, retain
  arbitrary read failures, and prove EOF cannot seal a pending action. A
  Linux-only real slave-close test is retained for hosted execution.

## 2026-08-07 — Issue #1230 causal non-mutating TUI PTY batch

- Symptom: the initial real batch expected the stale stats header `Activity
  (Week)` and stopped even though the rendered production panel correctly said
  `Activity (last 7 days)`. A later run reused the already-consumed source for
  `/continue`, which correctly returned HTTP 400 under the one-shot continuation
  contract.
- TDD: the new direct-PTY batch first failed to compile because its scenario
  was absent. The live red retained terminal evidence for the stats label and
  continuation target errors before either acceptance-only correction.
- Repair: the stats predicate requires the canonical header, toggle hint, total
  run/cost values, and an Escape frame before `/config`. `/continue` now probes
  and targets the completed `/resume` child; its keystroke/frame, same
  conversation, three distinct runs, durable messages, and exactly-once child
  terminal events are asserted.
- Compatibility: this changes acceptance infrastructure only. Product TUI,
  HTTP, provider, tools, cron, callback, and macOS behavior are unchanged.
- Verification: focused live normal and race PTY batches pass; the full
  regression gate passed with 85.0% total coverage and zero uncovered
  functions. Independent review remains required before promotion.

## 2026-08-06 — Issue #1224 deterministic script descendant cleanup fixture

- Symptom: under concurrent race load, the real descendant fixture could report
  a generic PID readiness timeout before it published its start barrier.
- Cause: the test used its configured handler timeout as a setup deadline and
  could not observe an early handler result while polling for the PID.
- TDD: the focused package first failed to compile after the regression required
  `waitForScriptPID` to return an early handler result rather than only fatal
  generically. The green helper returns that result and the real fixture uses a
  long test-only timeout plus parent cancellation after its barrier.
- Repair: no `loader.go` production behavior changed. The existing one-second
  configured-timeout test remains independent; the descendant case proves
  cancellation completion and child death.
- Verification: focused normal and race (`-count=3`) passed; `TMPDIR=/private/tmp
  GOCACHE=/private/tmp/gocode-1224-go-build ./scripts/test-regression.sh`
  passed normal, race, coverage, and the coverage gate (85.3% total, zero
  uncovered functions).
## 2026-08-06 — Issue #1222 semantic working-memory tool results

- Symptom: a valid JSON value stored by `working_memory set` was exposed by
  `get` and `list` as JSON text inside another JSON string. In the real API
  continuation, `"api-memory-value"` became `"\\\"api-memory-value\\\""`.
- Cause: `MemoryStore` and `SQLiteStore` intentionally return canonical JSON
  text for snippets, but `WorkingMemoryTool` placed that text directly in a
  `map[string]any` before marshalling its response.
- TDD: new scalar/object/list tests were red before the adapter, showing every
  valid JSON shape double encoded. The malformed-entry and missing-key tests
  preserve the compatibility boundaries.
- Repair: get/list now emit `json.RawMessage` only when stored text is valid
  JSON; malformed legacy text remains a string. Stores, SQLite schema, and
  snippet construction are unchanged.
- Verification: focused normal and race tests passed after the repair; SQLite
  close/reopen proves canonical storage and snippets remain stable; the real
  harnessd HTTP/SSE same-conversation continuation passed. `TMPDIR=/private/tmp
  ./scripts/test-regression.sh` then passed normal, race, coverage, and the
  coverage gate at 85.3% total coverage with zero uncovered functions.

## 2026-08-06 (Issue #1220 owner-created rendered-driver foundation)

- Replaced the environment-asserted permission placeholder with Darwin
  `AXIsProcessTrusted` and `CGPreflightScreenCaptureAccess` preflight calls.
  Neither API requests consent; incomplete grants fail before lifecycle start.
- Extended the #1205 owner with a distinct retained `0700` artifact root and a
  post-child-shutdown/post-runtime-removal completion boundary. Launch and
  scenario failures retain a diagnostic without widening cleanup beyond recorded
  child handles and the exact disposable root.
- Added an attested-PID Accessibility adapter for composer value/Send action and
  AX-tree capture, plus largest-owned-window screenshot capture. Added a fixed
  two-run collector for existing conversation/runs/messages/SSE APIs and a
  fail-closed typed proof validator for paths, kinds, sizes, hashes, PNG format,
  semantic markers, identities, and cleanup.
- Regression tests cover admission-before-effects, partial grants, inconsistent
  reports, retained launch diagnostics, post-cleanup finalization, deterministic
  two-run correlation, extra conversation rejection, and malformed artifacts.
- Review tightening rejects a final-component symlink even when its target is
  still inside the retained root. Cleanup and proof-finalization failures now
  always retain `failure.json`; only a fully sealed and validated proof can
  produce `proof.json`.
- Focused normal, race, and no-CGO fallback tests pass. The full Swift suite
  passes 259 tests. The first repository-wide regression attempt exposed the
  separately owned #1221 PTY readiness failure, so no commit or PASS was claimed
  while that accepted baseline remained red.
## 2026-08-06 — Issue #1221 fresh PTY conversation evidence

- Symptom: an ad-hoc piped `script(1)` launch could retain durable API/SSE
  records while inheriting zero rows and columns, making a deliberately empty
  viewport look like a missing transcript. The official runner only covered
  resumed conversations.
- TDD: fresh-launch geometry and VT-frame tests were added before the runner;
  the real fresh scenario first failed because its final incremental Bubble
  Tea frame was emitted before alternate-buffer exit and cumulative replay
  scrolled that frame out of the synthetic grid.
- Repair: `RunFreshConversation` uses the official BSD/util-linux launcher
  with explicit 30x100 geometry, drives two typed turns around `/search`, and
  records hashed terminal, VT, keystroke, SSE, and API/store artifacts. The
  reader snapshots both cumulative and latest-home frame views before buffer
  exit. No TUI product runtime or API behavior changed.
- Verification: focused VT/geometry plus fresh real PTY evidence, full
  `ptyrunner` normal suite, and `go test -race ./internal/acceptance/ptyrunner`
  passed before the repository regression gate.

## 2026-08-06 — Issue #1215 harnessd fixture causal readiness

- Symptom: aggregate race/load could fail cleaner and invalid-catalog daemon
  tests on short fixture-level startup/health waits, even though the tests
  already owned precise cleaner lifecycle channels and a listener-injection
  seam.
- Cause: the invalid-catalog case reserve-closed a port and polled that guessed
  address directly; the cleaner cases waited only for a two-second wall-clock
  channel observation and could not distinguish a real early daemon exit.
- TDD: the invalid-catalog test first failed to compile after it was moved to a
  not-yet-defined listener-aware helper. The green helper delegates to the
  established actual-listener matrix path; no daemon runtime code changed.
- Repair: lifecycle waits now race their injected event with the owned daemon
  result, retaining a ten-second diagnostic only when neither causal path
  resolves. The malformed-catalog case reaches real `/healthz` through the
  actual acquired listener and still requires clean interrupt shutdown.
- Verification: focused normal x20 and race x10 passed; complete
  `go test ./cmd/harnessd -race` passed; `TMPDIR=/private/tmp
  ./scripts/test-regression.sh` passed normal, race, coverage, and coverage
  gate phases at 85.3% total coverage with zero uncovered functions.

## 2026-08-06 — Issue #1216 script timeout process-tree ownership (in verification)

- Symptom: aggregate race/load reported `TestScriptHandler_Timeout` at roughly
  31 seconds despite a one-second tool timeout and a strict five-second
  completion requirement.
- Cause: `makeScriptHandler` combined `exec.CommandContext`, whose background
  cancellation kills only the direct script PID, with a later handler-owned
  process-group kill. That split ownership lets direct-child cancellation race
  the group cleanup and leaves `Cmd.Wait` vulnerable to inherited stdout/stderr
  descriptors held by a descendant.
- Test-first evidence: before the production change, a real shell fixture was
  added that starts a PID-recorded background `sleep`, holds inherited stdio,
  and requires prompt timeout plus descendant death. The historical aggregate
  race failure is the red evidence; focused pre-change normal/race stress did
  not reproduce the timing-sensitive delay, so it is recorded as a
  characterization rather than claimed as a deterministic local red.
- Repair: the handler now uses `exec.Command`; its context branch is the sole
  cancellation owner and kills the process group. A two-second `WaitDelay`
  bounds pipe draining after group exit. JSON stdin, restricted environment,
  normal stdout, stderr/non-zero errors, and the configured timeout contract
  are unchanged.
- Verification: focused normal lifecycle stress, focused race stress, and
  complete script-package normal/race suites pass. `TMPDIR=/private/tmp
  ./scripts/test-regression.sh` passed normal, race, coverage, and coverage
  gate phases at 85.3% total coverage with zero uncovered functions.

## 2026-08-06 — Issue #1214 source-workflow invalid-protocol fixture handshake

- Symptom: `TestSourceManagerRunWorkflowFailsOnInvalidProtocolAfterResult`
  used a raw child that wrote stdout and exited without consuming the parent's
  initial `start` frame. Linux scheduling could therefore return EPIPE before
  the intended late-message protocol assertion.
- Cause: the fixture did not exercise the established source-workflow startup
  boundary before manufacturing its terminal result.
- Fix: the fixture child now uses `workflowsdk.Main` to consume `start` and
  emit the valid result, then writes the same deliberately late raw `log`.
  Production source workflow behavior, the SDK, and #1209 native scenarios are
  unchanged.
- Verification: focused normal `-count=100` and race `-count=20` pass; the
  repository regression gate is recorded with the PR handoff.

## 2026-08-06 — Issue #1212 live provider fetch explicit opt-in

- Symptom: `TestLiveFetchAgainstRealProviders` ran from ordinary `go test`
  whenever `OPENAI_API_KEY` or `OPENROUTER_API_KEY` existed, allowing the
  normal regression gate to wait on real provider endpoints.
- Cause: the test filename did not exclude it from Go's package test set and
  its only guard was the credential itself.
- TDD: `TestLiveProviderFetchEnabled` first failed to compile because the
  explicit gate did not exist; the green table proves credential-only, flag
  only, and non-`1` flag values stay disabled while flag-plus-credential is
  enabled without a network request.
- Repair: the test-local gate now requires `HARNESS_TEST_LIVE_PROVIDERS=1`
  and the matching provider credential before constructing a fetcher. The
  testing runbook preserves the narrow intentional command; production
  modelstore behavior, endpoints, retries, and credentials are unchanged.
- Verification: focused normal and race gates passed; an invocation with a
  sentinel `OPENAI_API_KEY` but no opt-in visibly skipped both live provider
  subtests without a network request. `TMPDIR=/private/tmp
  ./scripts/test-regression.sh` passed normal, race, coverage, and coverage
  gate phases at 85.4% total coverage with zero uncovered functions.

## 2026-08-06 — Issue #1210 terminal SSE settlement

- Symptom: a run-scoped SSE reconnect could replay `run.completed`,
  `run.failed`, or `run.cancelled` before the corresponding `GET /v1/runs/{id}`
  read model became terminal.
- Cause: terminal event journaling precedes the durable terminal `UpdateRun`
  and in-memory status commit; `Subscribe` legitimately snapshots the journal
  during that interval.
- Repair: `handleRunEvents` now waits, only for a terminal frame, on a
  context-cancellable Runner status notification until the matching public
  terminal state commits. It does not add an acceptance-runner retry or alter
  journal/live ordering.
- Regression: a blocked terminal `UpdateRun` server test proves no terminal
  replay completes before release, then exactly one matching terminal frame and
  matching GET status after release for completed, failed, cancelled, and a
  `Last-Event-ID` replay.

## 2026-08-05 — Issue #1204 real PTY continuation evidence (in implementation)

- Adds an internal acceptance driver that builds disposable `harnessd` and
  `harnesscli`, starts a fake-only loopback daemon, completes a source run, and
  types both `/resume` and `/continue` through `script(1)` into the real TUI.
- The fake-turn fixture format gains optional `deltas`, intentionally limited
  to `HARNESS_PROVIDER=fake`, so the child lifecycle can prove its expected
  `assistant.message.delta` without altering real-provider behavior.
- Evidence is correlated by distinct source/child run IDs plus shared
  conversation ID, ANSI/VT-interpreted visible screen, raw terminal and typed
  keystrokes, child SSE, and an independent API/store probe; each retained
  artifact is SHA-256 addressed.
- Fixture repairs avoid false positives from startup escape bytes, alternate
  buffers, ANSI erasure, Bubble Tea's final blank redraw, and wide/combining
  Unicode cell geometry. Daemon process-group shutdown and artifact-root-local
  DB/HOME/log paths keep acceptance state outside the checkout.
- Verification remains pending: focused normal/race and full regression results
  must be recorded before this becomes implemented.
- #1207 portability repair: macOS BSD `script` accepts its direct child argv,
  but Ubuntu util-linux requires `-c` with one command string. The runner now
  selects the OS form, POSIX-quotes every Linux child argument, and observes
  the owned `script` child while waiting for semantic screen readiness so an
  early successful-or-failed exit is a useful error rather than a timeout.
- Red-first coverage pins the Darwin argv, util-linux argv with quote-bearing
  values, a real host `script` sentinel launch, and prompt early-exit failure.
  Focused normal and race package tests passed with `TMPDIR=/private/tmp`; the
  full regression also passed (normal, race, coverage `85.3%`, zero uncovered
  functions). Independent review and hosted CI remain merge gates.
- Follow-up review repair: `waitForChild` now receives the same owned PTY
  completion signal as screen readiness. A real `script(1)` child consumes
  post-input bytes then exits without a child run; discovery returns the useful
  exit error promptly instead of waiting for its timeout.
- The first real fixture used terminal-output readiness and could lose its
  marker under full-suite PTY scheduling. It now uses a sentinel-owned,
  mode-local filesystem rendezvous before post-input bytes are written; the
  terminal exit assertion remains real and is stress-pinned.

## 2026-08-06 — Issue #1208 owned native fake-provider scenario preflight

- Added one deterministic fixture manifest for the first owned native cases:
  core `ls` tool plus a second Chat message, recurring `cron_create` linked
  continuation, and exactly-once `set_delayed_callback` continuation. The
  owner generates a fresh nonce, validates the manifest before selecting its
  lifecycle, and writes only its flattened fake turns into the owned root.
- First red: the preflight types/functions were absent. The green tests reject
  a missing typed artifact, duplicate artifact path, and repeated conversation
  marker; they require separate nonce-scoped screenshot, AX, raw-SSE, and
  API/store paths for every scenario.
- No lifecycle was run. The change has no screenshot, AX/OCR, TCC, process, or
  rendered acceptance evidence; a later driver requires explicit operator
  foreground and Accessibility/Screen Recording authorization.

### Review repair

- P1 hardening moved path safety to the actual fake-turn write boundary:
  lexical `..`, absolute/backslash paths, escaped resolved parents, and an
  existing symlink target now fail before writing. New regression tests cover
  traversal and symlink escapes.
- P1 contract tests now parse and verify the core `ls` request and same-chat
  continuation, harness-scoped `cron_create` plus linked continuation, and
  five-second `set_delayed_callback` due/continuation/exactly-once semantics.
- Cron fixture preflight now calls `cron.NextRunTime`, the same parser-backed
  contract used by `cron_create`; a malformed `not-a-cron` schedule is rejected
  before the fixture can be written.
- The independently reported #1087 `tool:git_status` terminal `running`
  failure did not reproduce at this exact tree: one normal run and 20 `-race`
  repetitions passed. It remains subject to the required full gate; no test or
  product behavior was waived or changed for it.

## 2026-08-05 — Issue #1199 durable skill lifecycle

- `create_skill` now reloads the authored-skill registry synchronously after its exclusive file creation. Core, deferred, and HTTP verification converge on persistence-first adapter wiring: write verification frontmatter, then reload; reload failures do not report success.

## 2026-08-05 — Issue #1201 API/SSE executor foundation (parent #1087; in implementation)

- Added `internal/acceptance/apisserunner`, which rejects incomplete
  registry-derived API case sets before dispatch, starts normal runs through
  HTTP, records raw SSE plus terminal API artifacts with digests, and requires
  independently verified postconditions and cleanup through the v2 validator.
- First red: the new package had no implementation. The first real-harnessd
  run then exposed that the start response can omit `conversation_id`; the
  executor now binds it from the independent terminal probe and rejects any
  conflicting identity.
- Current proof is one isolated fake-provider `create_profile` intent case
  with durable filesystem probe and cleanup. It is not complete default-tool
  coverage; remaining case authoring is required before a #1087 PR can claim
  done.
- The real daemon now also derives a denied/no-mutation case for every current
  API row (63 at hash `8cafb764…190bc011`), requires the public blocked-event
  reason, and probes the isolated workspace after each request. This is
  explicitly negative coverage, not a substitute for positive intent cases.
- #1202 review repair: a reviewed API manifest now requires the exact
  `inventory_hash` it was authored against; coverage reporting rejects a
  same-ID current catalog whose hash drifted before it reports gaps, and still
  rejects stale, unavailable, wrong-surface, or invalid-invocation rows. Once
  a run receives `202 Accepted`, cleanup is deferred before response parsing,
  including malformed JSON or a missing `run_id`; a cleanup failure is joined
  with the original failure rather than replacing it.
## 2026-08-05 — Issue #1089 rendered-native proof validator

- Review repair: validation now treats a qualifying native manifest as a
  current one-shot proof rather than generic report history: every declared
  applicable case needs exactly one PASS. Artifact roots and files are resolved
  through symlinks, files must be regular and contained, and each record is
  bound to launcher-created collection provenance.
- Added a native-only validation lane around the #1086 suite overlay. It
  compiles the running daemon's catalog, validates ordered case/evidence data,
  and recomputes every passing artifact digest inside the declared isolated
  root. ToolWalk is intentionally not accepted as rendered proof.
- Strict TDD: the first regression records a valid native bundle, mutates its
  screenshot artifact, then proves validation rejects the changed digest.
- The launcher requires an explicit real AX/OCR driver and never discovers,
  stops, or reuses an existing GoCode/harnessd process.

## 2026-08-05 — Issue #1187 isolated harnessd profile CRUD

- Symptom: profile mutation implementations existed, but harnessd omitted the
  profile directory at registry, runner, and HTTP-server composition. Live
  catalog discovery therefore omitted create/update/delete and API mutations
  returned `501 not_configured`.
- Cause: startup derived only a local user path for config loading; the
  resolved paths were never carried across the runtime assembly boundary.
- Repair: resolve one opt-in absolute `HARNESS_PROFILES_DIR` (default unchanged),
  derive project profiles from `HARNESS_WORKSPACE`, and pass project/user paths
  into the registry, runner, and server. Named profile reads and MCP profiles
  preserve project > user > built-in precedence.
- Regression coverage: listener-owned real daemon HTTP CRUD plus three
  fake-provider agent-tool turns, explicit no-real-home write assertion,
  precedence, default/relative path validation, forwarding, normal/race, and
  canonical full regression.
- Review repair: MCP stdio had built a second production registry without the
  resolved profile paths. It now receives the same project/user directories;
  an isolated MCP catalog regression executes `create_profile` and proves the
  write does not fall back to the real home directory.
## 2026-08-05 — Issue #1188 selected ordinary-run profile policy

- Symptom: a TUI-selected `profile` reached `RunRequest.ProfileName`, but
  ordinary Runner admission ignored its model, prompt, budgets, tools, and
  capability policy; only later isolation/MCP preflight observed it.
- Cause: `profiles.Profile.ApplyValues()` was used by startup/subagent paths,
  not by `Runner.startRun`.
- Repair: compose the resolved profile once before ordinary validation/state
  creation. Explicit request values win for model/budgets/prompt/reasoning;
  a profile tool list is intersected with request tools, and explicit false
  bash/file-write/network settings add absolute denied tools.
- Regression: expected-red tests proved model omission and request widening;
  focused normal/race plus a fake-provider two-turn HTTP conversation prove
  profile model/prompt/reasoning and blocked tools on both messages.
- Follow-up regression: profile schema spells no isolation as `"none"`, while
  `RunRequest.WorkspaceType` uses empty for no provisioning. Composition now
  preserves empty at that boundary; the existing no-provisioning test and a
  direct selected-profile regression prevent a synchronous 400.
- Review P1: `download` combines outbound HTTP with `os.WriteFile`; the first
  audited category list omitted it. It is now denied by either explicit
  profile network or file-write denial, and a real handler regression proves
  direct provider invocation makes zero HTTP requests and writes no file.
- `allowed_commands` remains parsed profile metadata but is explicitly marked
  unsupported for ordinary selected runs; it is not presented as a command
  security boundary in the authoring runbook.
- Re-review P1: an exhaustive name mapping still missed registered
  `ActionFetch` tools such as `web_search`/`agentic_fetch` and future
  `ActionWrite` tools. Registry now retains an action category at default
  registration; selected profile capability denials filter offered definitions
  and reject direct dispatch by action. Real fetch/write/download probes prove
  no outbound request or file side effect before the handlers run.
- Re-review P1 follow-up: continuations copied named tool filters but dropped
  the selected profile identity and action-denial map, so a second turn could
  execute forbidden actions. `ContinueRunWithOptions` now snapshots both;
  a true two-turn provider regression proves continuation fetch/write/download
  calls make zero HTTP requests and write no files. Dynamic replacement now
  preserves a tool action, and externally hosted MCP calls are conservatively
  classified as fetch so network-denied profiles fail closed before RPC.
- Final review P1: `connect_mcp` was still actioned as execute even though it
  invokes an external HTTP/SSE connector before tool discovery. It is now
  actioned and registered as fetch; a selected net-denied profile regression
  proves direct provider invocation makes zero connector calls.
- Final review P1 follow-up: continuation `AllowedTools` replaced the source
  filter, so a selected profile allowlist could be widened on a second turn.
  The profile's non-empty allowlist is now persisted separately as an immutable
  upper bound; overrides are intersected and a disjoint request retains the
  source filter because an empty legacy filter means unrestricted. A true
  source-to-continuation bash probe proves zero handler side effects.
- Final review P1 follow-up: selected-profile intersection can itself be empty
  at StartRun, which must mean deny ordinary tools rather than legacy
  unrestricted; the immutable profile bound now also intersects skill
  constraints at offer and dispatch.
- Final review P1 follow-up: `run_recipe` held a prebuilt handler map, so the
  outer call gate did not govern member steps. The runner now supplies a
  context-scoped member authorizer; `run_recipe` checks every step's tool name
  and selected-profile action policy before the executor invokes any handler.
  The default-registry regression uses one recipe containing bash, write, and
  fetch and proves a recipes-only profile makes zero file and HTTP side effects.
- Final review P1 follow-up: member authorization initially covered only the
  selected-profile name/action boundary, leaving active skill constraints and
  per-call permission rules/approval outside the recipe dispatcher. The
  authorizer now receives each substituted member argument immediately before
  invocation and applies those direct-call gates; ask rules register and emit
  an approval for the member (not merely the outer recipe). Regressions prove
  an outer-only skill blocks a real bash write, deny blocks it, and ask executes
  only after approval.
- Final review P1/P2 follow-up: direct calls also pass `PreToolUseHooks`, which
  can deny or mutate args, but recipe members originally skipped that stage.
  Member authorization now invokes the same hook routine over substituted args
  and returns any mutation to the executor. Member approval IDs are indexed
  (`outer:recipe:index:step`) so empty or duplicate optional recipe step names
  cannot collide. Regressions prove hook deny/mutation parity and two unnamed
  ask-approved bash steps receive distinct approvals and execute in order.
- Final review P1 follow-up: recipe hooks initially ran before direct-call
  profile/allowlist/skill checks, so a rejected member could still trigger a
  hook. The member path now matches direct ordering: capability/name/skill
  gates first, hooks second, then permission rules/approval. Regressions prove
  a blocked bash member causes neither hook observation nor a file side effect,
  while hook errors retain direct fail-closed event behavior.

## 2026-08-05 — Issue #1174 `/init` real SSE persistence

- Symptom: `/init` wrote only through synthetic `RunCompletedMsg`; real `assistant.message` then `SSEDoneMsg(run.completed)` finalized the transcript without `AGENTS.md`.
- Cause: the real terminal branch bypassed the init completion helper and pending state lacked accepted-run identity.
- Repair: retain run/target/confirmation state, consume only matching terminals, and atomically re-stat/write/sync/rename the workspace file.
- Regression coverage: real SSE success, failed/fatal paths, foreign terminal, appearing-file conflict, and mode-preserving confirmed replacement.
- Follow-up P1: confirmed Ctrl+C and Escape cancel the local bridge without a guaranteed terminal frame, so each now consumes only the matching pending `/init` state; late output/terminal fixtures prove no post-cancel write.
- Follow-up P2: reconnect exhaustion already abandoned the owned pending state; a deterministic six-close acceptance test now proves late output cannot write after the bridge is lost.

## 2026-08-05 — Issue #1183 durable replay SSE fixture

- Symptom: after durable replay merged, hosted race CI's replay command fixture
  accepted the replay POST but rejected the intentionally started returned-run
  stream at `GET /v1/runs/run_replayed_1/events`.
- Cause: `Model.Update(RunStartedMsg)` has always started the returned run's
  SSE bridge; the fixture represented only the first HTTP step.
- Fix: fixture-only lifecycle coverage validates the durable POST, exact
  returned-run SSE path and `Accept: text/event-stream`, rendered assistant
  message, terminal `run.completed`, and handler closure. Rollout simulation
  now explicitly proves it makes zero `/events` requests.
- Verification: focused normal/race replay tests passed 20 repetitions each;
  full TUI normal passed in 41.776s and race in 44.455s. Production replay,
  server, cron, callback, and cancellation code remain unchanged.
- Final gate: retained canonical-temp repository regression passed normal, race,
  coverage, and coveragegate at 85.5% total coverage with zero uncovered
  functions.

## 2026-08-05 — Issue #1175 bootstrap provenance fixture

- Symptom: macOS `/var` test paths differed textually from Git's `/private/var` canonical output.
- Fix: fixture roots canonicalize through `EvalSymlinks` before expected worktree paths are derived; no bootstrap script or provenance acceptance rule changed.

## 2026-08-05 — Issue #1177 harnessd race-readiness fixtures

- Symptom: hosted race CI intermittently reported that the two memory startup
  fixtures never became healthy within their three-second guessed-address
  deadline.
- Cause: each fixture used `freeLocalAddr`, closing a reservation before the
  daemon bound it, so readiness could observe a recycled listener or an early
  startup failure only as a timeout.
- Fix: both tests retain their original environment/config cases but bind port
  zero and use the existing listener-aware matrix helper. The helper receives
  the daemon's actual listener address, reports early startup failure, then
  exercises the existing graceful interrupt lifecycle. A test-only overload
  retains their prior three-second health deadline while the shared matrix
  default remains ten seconds. Production code is unchanged.
- Verification note: the initial default-environment serial regression reached
  the normal phase and failed five bootstrap-provenance fixtures before race or
  coverage because macOS aliases the temporary path as `/private/var` while
  those fixtures expected `/var`. The #1177 focused and complete harnessd
  gates were green; the required canonical-temp serial run is recorded
  separately rather than treating the alias result as a product regression.
- Evidence: focused normal and race stress each passed 30 repetitions;
  complete `cmd/harnessd` normal/race passed; the serial canonical-temp
  regression passed normal, race, and coverage at 85.5% with zero uncovered
  functions.

## 2026-08-05 — Issue #1173 durable replay

- Symptom: `/runs` advertised completed durable IDs but `/replay` treated them as absent rollout files.
- Cause: rollout simulation and durable re-execution shared one endpoint and only the rollout directory could resolve a bare ID.
- Fix: an authenticated per-run replay route starts a distinct same-conversation run from terminal durable source state; TUI routes only bare IDs there.
## 2026-08-04 (Issue #1169 bootstrap VCS provenance)

- Strict red reproduced Go 1.26 linked-worktree contamination: a clean child
  `harnessd` inherited the parent revision and `vcs.modified=true`. The old
  bootstrap also accepted a fake executable whose build info named the wrong
  revision, leaving it at the normal binary path.
- `scripts/init.sh` now clears ambient Git routing variables, resolves the
  fetched origin commit (rather than stale local `main`), resolves the actual
  target worktree's absolute Git directory and HEAD, builds each local binary
  with those values plus `-buildvcs=true`, then compares the binary's own build
  info to that exact clean revision. Missing/dirty/mismatched output is deleted
  and returns a nonzero bootstrap error.
- The #1165 acceptance guard is unchanged. New disposable-repository tests
  cover dirty-parent isolation, inherited external metadata, and rejected
  candidate removal; scheduler, provider, API, TUI, and native code are out of
  scope.
- Hosted CI exposed a fixture-only portability error: a bare repository's
  symbolic HEAD is host-config dependent, so an unqualified clone could commit
  an unrelated initial branch and reject the intended `HEAD:main` update. The
  origin divergence regression now explicitly checks out `main`; a no-global-
  config run reproduces the CI default and passes before the full gate rerun.

## 2026-08-04 (Issue #830 Anthropic Retry Fixture Budget)

- Symptom: full-suite coverage instrumentation previously let
  `TestClientRetriesOn503` exhaust its test-local 100ms retry budget, returning
  `retry budget exhausted: server returned 503 Service Unavailable`.
- Cause: the shared package-local fixture treated wall-clock scheduling under
  coverage contention as part of a two-attempt HTTP retry assertion.
- Fix: increase only `internal/provider/anthropic/client_test.go`'s bounded
  fixture `MaxTotal` to one second. Production retry defaults, attempt count,
  delays, jitter, status classification, and mock-server behavior are unchanged.
- TDD evidence: issue #830 retains the coverage-phase red; unchanged focused
  normal/race stress passed, confirming the flake requires full-suite load.
  After the fixture change, 429/503 stress, the complete Anthropic normal/race
  package suites, and the full regression gate pass.
## 2026-08-04 (Issue #1165 acceptance runtime provenance)

- The incident is reproducible: a clean requested worktree at `3ffc3d764`
  produced a binary whose `go version -m` reported dirty `7f8b2c92557b`.
  Checkout metadata is therefore not acceptance evidence.
- Added the sourceable acceptance guard and wired the deterministic smoke lane
  before daemon startup. It requires clean git build metadata matching the
  exact requested SHA and writes raw build info plus binary SHA-256 only after
  success. It never rebuilds, retries, or dispatches after rejection.
- Red-first regression proves a stale/dirty binary cannot create the daemon
  marker; the matching clean fixture proves its artifact exists before daemon
  start. Provider routing, scheduler behavior, credentials, API, and storage
  remain outside this slice.

## 2026-08-04 (Issue #1161 scheduled routing preservation)

- Confirmed the narrowing defect before implementation: `RunRequest` owns the
  routing policy, but `RunMetadata`, harness cron config/`RunStartRequest`,
  remote cron JSON/server mapping, and durable callback rows omit it.
- The permanent first regression failed to compile because
  `cron.RunStartRequest` had no model/provider/fallback fields. Independent
  remote and server fingerprint reds failed on the same missing typed fields;
  the harnessd fallback regression then ran but used `default-model`. Callback
  reds failed on missing metadata and durable callback fields.
- The implementation copies the four safe routing values through immutable
  tool metadata, cron config, typed/remote/authenticated starts, durable
  callbacks, and Runner admission. The cron fingerprint binds all four values,
  including fallback order. A final red proved the initially persisted queued
  run omitted the requested provider; initial durable state now records it so
  crash recovery cannot narrow the scheduled policy before dispatch.
- Fix boundary is additive propagation of model/provider/fallback names and the
  fallback boolean only. Tenant/auth/scope, secrets, provider resolution,
  retries, leases, clients, and Issue #1162 are explicitly unchanged. Focused
  boundary suites and complete affected normal/race suites are green after the
  rebase to `c991a725`. Final handoff evidence is the repository regression
  gate plus exact branch/base/merge-base identity recorded outside this log.
## 2026-08-04 (Issue #1162 authoritative fake-provider routing)

- Strict red: daemon assembly with `HARNESS_PROVIDER=fake`, a loaded OpenAI catalog/client, and `allow_fallback=false` returned the real fixture response for `gpt-4.1-mini` and failed absent `fake-model` lookup.
- Added `ForcedDefaultProviderName` to the shared `RunnerConfig` assembly. Only explicit fake sets it to `fake`; `Runner.resolveProvider` now returns its direct default before requested-provider or model-catalog lookup. Registry/catalog ownership remains intact for metadata, tools, and pricing.
- Green daemon assembly covers both models, `/v1/models` catalog visibility, `provider_name=fake`, fake response completion, and zero configured-real-client factory calls. Independent review found that caller-supplied fallback providers could still construct a real registry client. Forced routing now terminates candidate construction before any fallback lookup; the regression explicitly requests `allow_fallback:true` plus `fallback_providers:["openai"]` and still proves zero real-factory calls. Focused normal/race and the final isolated full regression pass normal, race, coverage, 85.5% total coverage, and zero uncovered functions. An earlier retry-budget flake was not waived.
- Final review required the retryable execution branch itself to use the concrete `fakeprovider.Provider`, not a same-shape test double. The daemon regression now gives that provider a real 429 turn while the request names OpenAI as a fallback; the run fails locally and proves the real registry factory is never constructed.

## 2026-08-04 (Issue #1158 conversation history watermark foundation)

- Added `Runner.ConversationMessagesSnapshot`, which returns cloned messages
  and an exact durable event ID at one per-conversation publication boundary.
  `completeRun` now samples the durable event cursor, persists conversation
  content, and publishes the in-memory pair while holding the existing
  conversation sequence plus event locks. An in-flight later run therefore
  cannot advance the cursor ahead of the completed transcript snapshot.
- No cursor is reconstructed after restart: conversation mutations are not
  transactionally versioned with the event store, so even a historical
  `run.completed` cannot prove equivalence to loaded messages. Missing readers,
  store errors, destructive invalidation, overlapping runs, and absent
  process-local pairs all return empty and require full replay.
- `GET /v1/conversations/{id}/messages` now adds `last_event_id` after the
  unchanged `runs:read` and tenant gate. The TUI decodes it into
  `ConversationHistoryMsg`; omitted old-server fields remain empty.
- Strict red: the new TUI/API tests failed to compile with
  `ConversationHistoryMsg has no field or method LastEventID`. After the
  minimal implementation, the same-text two-turn/in-flight snapshot,
  restart-after-undo fallback, inverted overlapping-run ordering, copy
  isolation, no-reader fallback, API response,
  auth-before-lookup, tenant boundary, and old-server decode are green.
- Verification: focused normal x10 and race x5 passed. Complete affected
  package normal and race suites passed for `internal/harness`,
  `internal/server`, and `cmd/harnesscli/tui`. The non-concurrent full
  regression normal, race, and coverage phases passed at 85.5% with zero
  uncovered functions.
- Independent review found two P1s in the first green: a conversation-wide
  cursor could pass an overlapping later run absent from the message slice, and
  restart recovery could pass undo/rewind mutations. The final tree detects
  overlapping run lifetimes before sampling and never reconstructs a cursor
  without a process-local pair; deterministic regressions cover both findings.

## 2026-08-04 (Issue #1156 MCP HTTP test transport isolation)

- `NewHTTPConnForTest` previously left `http.Client.Transport` nil, so every
  MCP HTTP test shared `http.DefaultTransport`'s idle pool. Parallel
  `httptest.Server` shutdown could therefore turn a strict 401/403 assertion
  into a cross-test transport failure.
- The test-only constructor now clones `http.DefaultTransport` for each client.
  Production `dialHTTP`, pooling, timeout, error mapping, headers, and retry
  behavior remain unchanged. The clone owns its own idle pool.
- TDD evidence: `TestNewHTTPConnForTestOwnsTransport` first failed with
  `Client.Transport is nil; test client shares http.DefaultTransport`. A
  nonparallel spy proves `httptest.Server.Close` invokes global default
  cleanup, legacy nil clients are coupled to it, and a prebuilt clone is not.
  This is the actual standard-library boundary; it does not fabricate an
  active-dial cancellation claim.
- Verification: focused ownership plus strict-auth tests passed normal x20 and
  race x20; `go test -race ./internal/mcp -count=1` passed. The complete
  `./scripts/test-regression.sh` normal, race, and coverage phases passed at
  85.5% total coverage with zero uncovered functions.

## 2026-08-04 (Issue #1152 harnessd startup race fixtures)

- Hosted race CI for unrelated `cmd/harnessd` lifecycle tests became sensitive
  to #1150's intentionally durable default callback bootstrap. The affected
  fixtures now explicitly set `HARNESS_ENABLE_CALLBACKS=false`; callback
  enablement and shutdown retain their dedicated tests.
- `TestLookupModelAPIWiredInRunWithSignals` and
  `TestLookupModelAPIWithAlias` wait for their actual provider-factory call
  before signalling shutdown. `TestShutdownCronOrderingDeterministic` waits
  for the exact listener and `/healthz` readiness before each real shutdown.
  Cleaner tests retain their injected start/cancellation acknowledgement gates.
- TDD evidence: the first targeted command failed to compile because the five
  tests referred to the not-yet-defined
  `disableCallbacksForUnrelatedHarnessFixture`; the helper then made their
  test-owned opt-out explicit. No production source changed.
- Verification: targeted normal x20 passed in 4.945s, targeted race x20 passed
  in 7.142s, and complete `go test ./cmd/harnessd -race -count=1` passed in
  12.537s. The normal and race phases of `./scripts/test-regression.sh` passed;
  its coverage phase then failed the existing zero-function gate at
  `internal/server/cron_run_idempotency.go:266 waitForCronRunDispatch` (total
  85.5%). That production function is from `dd7737d6`, not this test-only
  slice, so the baseline remains a blocker rather than being waived.

## 2026-08-03 (Issue #1144 transient heartbeat-busy fixture)

- Hosted race evidence from #1143 exposed that
  `TestCallbackManagerTransientHeartbeatBusyRetainsClaim` inferred a post-busy
  heartbeat renewal with a 90 ms lease and `Sleep(150ms)`. The fixture could
  observe after its initial durable lease expired and then misclassify a safe
  retry as a product failure.
- `transientLeaseStore` remains test-only and still delegates every successful
  extension to the real SQLite store. It now emits one buffered event for its
  injected first `database is locked` error and one only after the first real
  delegated extension succeeds. The regression uses a one-second lease,
  captures the initial token/deadline, waits for both events, and re-reads the
  durable row to require same token, attempt one, and a later deadline.
- TDD evidence: the new causal assertions first failed to compile because the
  fixture had no `failed` or `renewedUntil` gates. No callback production
  source changed. Idempotent cleanup unblocks the starter before manager
  shutdown so assertion failure cannot leave dispatch work blocked.
- Final validation: focused normal x100 passed in 51.249s; persisted focused
  race x100 exited 0 in 53.917s; complete tools normal/race passed in
  13.740s/15.259s; full regression passed with 85.5% coverage and zero
  uncovered functions.

## 2026-08-03 (Issue #1140 matrix listener identity)

- Hosted race evidence showed that parallel `TestMatrix_` fixtures reserved an
  address with `freeLocalAddr`, released it, and could later query a sibling
  harness that had acquired that port. The affected custom-global-skill case
  therefore observed an empty registry even though its own runtime loaded one
  skill.
- `runDeps` now accepts an optional listener function and defaults to
  `net.Listen`, leaving production startup behavior unchanged. `runMatrixTest`
  requests `127.0.0.1:0`, captures the address of the listener actually
  returned through that dependency, and fails promptly if startup exits before
  health is available.
- TDD evidence: `TestRunMatrixTestUsesActualListenerAddress` was first red at
  compile time because `runMatrixTestWithListener` did not exist. It now starts
  the real daemon, serves a custom global skill, and proves its `/v1/skills`
  request uses the acquired address. Focused normal/race x100, full matrix
  normal/race x10, and `cmd/harnessd` normal/race passed before the full gate.

## 2026-08-03 (Issue #1124 retry-wait recovery fixture)

- Hosted race CI exposed a test boundary, not a confirmed early-dispatch
  product defect: the old recovery fixture used `next_attempt_at = now + 60ms`
  then asserted after `Sleep(15ms)`. Aggregate scheduling could move the real
  timer across that unowned observation point.
- The test-only `callbackFixtureClock` is mutex-protected for race safety.
  Recovery receives a one-hour persisted retry deadline; manual `fire` before
  fake expiry asserts no starter call and byte-for-byte-relevant durable
  checkpoint invariants, then manual fire after exact fake expiry asserts one
  attempt-two admission using the original run ID and cleared token/lease.
- Strict red evidence: focused compilation initially failed with
  `undefined: newCallbackFixtureClock`; no production callback source changed.
- Final-tree focused normal/race x100 passed in 0.419s/2.549s; complete tools
  normal/race passed in 13.200s/14.719s. Isolated foreground
  `./scripts/test-regression.sh` then passed normal/race plus 85.5% total
  coverage and zero uncovered functions in 2m26s.
## 2026-08-03 (Issue #1136 immutable timeout authority)

- Replaced the provisional public handle cancel with a package-visible opaque
  `TimedOutSubmissionTicket`. Its initializer is fileprivate to `Runner`; the
  only mint point is `waitForTerminal`'s final deadline-edge lifecycle check.
  Ticket consumption retains the private `RunSubmission` owner-token/generation
  recheck and is transport-only. Terminal, failure, reset, and load revoke it.
- `RunSession` now tracks every submission stream by handle. Reset/load cancels
  both displaced A and selected C rather than only the most recent stream.
- TDD red: removing the old API produced nine expected focused compile errors
  at former direct call sites. Gated proof now requires no ticket/action before
  deadline, B -> C -> A exact-one dispatch, duplicate refusal, and post-ticket
  terminal/failure/reset revocation. Remaining full-gate evidence is recorded
  by this corrected PR rather than inherited from the superseded implementation.
- Review correction: the first ticket implementation left a package-scoped raw
  transport method callable before deadline. The ticket, constructor, and
  transport closure now live in GoCodeUI; ToolWalk binds the immutable duration
  at submission and `submissionTimeoutGate(for:)` alone verifies the derived
  deadline and mints once. The #1146 CI-flake repair introduces an internal
  `RunSession` monotonic-now seam shared by `RunSubmission.markStarted` and
  `SubmissionTimeoutGate`; tests freeze/advance it at epsilon and exact
  deadline instead of sleeping. `Runner.waitForTerminal` now accepts only its
  poll interval so a caller cannot silently pass a conflicting timeout after
  submission. A direct
  gate regression plus a source-surface drift test prevents that bypass.

## 2026-08-03 (Issue #1133 passive displaced-submission outcome)

- Corrected the #1130 wait-policy gap: displacement is now a permanent action
  fence, not a terminal ToolWalk result. Runner waits for its immutable A
  handle's terminal/failure through deadline and never auto-answers/approves a
  mismatched or displaced selected B.
- `cancelTimedOutSubmission` retains exact locally owned A transport authority
  after B selection, but its displaced path is transport-only: it cannot alter
  B selection, transcript, pending UI, or cancellation state.
- Test-first evidence: four URLSession-gated `RunSession.submit()` + Runner
  tests were red before the repair (terminal/EOF/timeout returned displaced;
  delayed ACK returned without A identity). The resulting #1133 tests prove
  passive terminal, EOF failure, delayed acknowledgement, and B-safe timeout
  policy. The stronger B -> C authority and revocation proof is tracked in
  the separate #1136 entry above.
- Verification: final combined focused `PassiveSubmissionOutcomeIntegrationTests`
  passes 10/10 (not the earlier intermediate 4/4 or 8/8 counts); strict format
  (0/7 touched Swift files require formatting) and
  full `swift test --package-path macapp` (244 tests / 46 suites) pass. Full
  regression, independent review, and hosted checks remain.

## 2026-08-03 (Issue #1130 submission-local outcomes)

- Split `RunSubmission` into independent A-local `Lifecycle` and displacement
  facts. A terminal or failure therefore remains available to the initiating
  caller after scheduled B selection instead of becoming a false timeout.
- A delayed `startRun` acknowledgement now binds A's handle first, but only an
  exact, undisplaced active handle may select/activate/account it. Stream EOF
  and start/transport errors always settle A locally; they fail visible state
  only while that same A still owns it. `finishRunIfCurrent` clears by object
  identity rather than a reusable run-id lookup.
- ToolWalk now uses typed wait outcomes. Terminal/failure precede displacement,
  and only `.timedOut` reaches guarded A cancellation. New deterministic gate
  tests cover late acknowledgement, late start/EOF failure, reset/load
  detachment, and zero B mutation; outcome tests cover ordering and cancellation.
- Verification: strict Swift formatting, focused submission/ToolWalk suites
  (13 tests/2 suites), full `swift test` (238 tests/45 suites), and the
  retained-pane `./scripts/test-regression.sh` pass (normal, race, 85.5% total
  coverage, zero uncovered functions).

## 2026-08-03 (Issue #1128 submitted-run ownership)

- Added `RunSubmission`, returned by both native submit layers. It records A's
  `startRun` identity, per-run transcript, terminal result, failure, and
  displacement independently from the conversation's selected run.
- ToolWalk now waits, auto-controls, times out, and judges the handle. A
  selected B produces an explicit displaced result before any B endpoint call.
  A local lifecycle timestamp is retained so later authoritative B selection
  works without weakening provisional stale-replay protection.
- Composer captures `.submit` or `.steer(A)` once; the pure execution seam
  proves stale steering cannot fall through to a new submission.
- Verification: strict Swift format; focused submission/external/ToolWalk
  suite (37 tests/5 suites); full Swift package (230 tests/44 suites); exact
  repository normal/race regression; coverage 85.5% with zero uncovered
  functions.

## 2026-08-03 (Issue #1125 native action-owner fence)

- Added expected-run cancel/steer boundaries. Chat Stop, Composer, and ToolWalk
  timeout carry their rendered/decision A identity; a B mismatch exits before
  local mutation, Task creation, or HTTP.
- Deterministic A-to-B tests prove stale Stop, steer, and timeout send zero B
  endpoint requests while legitimate B actions still reach their endpoints.

## 2026-08-03 (Issue #1007 External Scheduled-Run Controls Rebase)

- System/component: `RunSession` control-owner reducer, accounting fence,
  request generations, `RunSession+RunControls`, and `InlineRunStatus`.
- Ownership/order: scoped conversation events select control ownership before
  accounting. A selected terminal is rendered before a live fallback resumes;
  a foreign terminal is tombstoned without changing the selected lifecycle or
  accounting. Selection changes invalidate outstanding answer/input/control
  requests.
- Visibility/safety: the scheduled-run accessibility/status text is composed
  alongside—not hidden by—project status. A second Stop cancels a per-run
  stream only when that stream belongs to the selected run.
- Verification: focused `RunSessionExternalControlTests` (6), full macapp
  (217 tests/43 suites), and `./scripts/test-regression.sh` normal/race/
  coverage pass from the exact rebased tree (85.5% total, zero uncovered
  functions). Hosted CI and independent review remain promotion gates.

## 2026-08-03 (Issue #1120 blocked-heartbeat fixture)

- Sol classified the hosted race failure as a test timing gap: a 90 ms fixture
  lease could cancel admission before its first blocking heartbeat entered.
  Production callback fencing, heartbeat, deadlines, and SQLite semantics stay
  untouched.
- The fixture now uses a one-second lease and orders starter, blocked renewal,
  process-fence rejection, deadline cancellation, exact-token durable release,
  and no replacement admission. It proves the released row is `retry_wait`
  with cleared token/lease, attempt one, and the original reserved run ID.
- Pre-change local race x200 passed in 22.373s, so it is recorded as
  characterization and not falsely presented as a production reproduction.
  Final normal/race x100 passed in 100.791s/103.045s; tools package
  normal/race passed in 13.562s/14.836s; isolated full regression passed normal,
  race, 85.5% coverage, and zero uncovered functions.

## 2026-08-03 (Issue #1117 callback duplicate-manager fixture)

- Sol classified the hosted duplicate-dispatch report as a test-fixture timing
  defect: a deliberately blocked admission outlived the test's 30 ms lease,
  so a sequential reclaim could legitimately create attempt two. This slice
  keeps #1106 production ownership/retry/lease code untouched.
- The fixture uses the manager's default lease, retains the direct second
  `Recover` process-fence failure and exact one starter/run/attempt assertions,
  and remove only the unrelated wait beyond an artificially short lease.
- The transient SQLite-claim-contention fixture additionally asserts that
  its durable single attempt/run resulted in exactly one `StartCallback` call.
  Branch provenance: #1119 (closing #1117) and child #1121 (closing #1120)
  are merged into the current #1106 stack; they are not yet on `main`.
- TDD evidence: pre-change focused normal x100 failed with `attempts = 2, want
  1` at the old 30 ms-lease/100 ms-wait fixture; focused race x100 passed,
  confirming schedule sensitivity. After the test-only correction, focused
  normal/race x100 and complete tools normal/race passed. A first overlapping
  repository run is explicitly rejected as non-authoritative. After its
  processes drained, one isolated foreground regression passed normal, race,
  85.5% coverage, and the zero-uncovered-function gate.

## 2026-08-03 — Issue #1112 authenticated cron assembly fixture cost

- Symptom: the repository race gate could record the assembled authenticated
  remote cron execution as timed out even though harnessd logged the same
  idempotent start at the five-second request boundary.
- Root cause: `assembly_integration_test.go` stored the production cost-12
  bcrypt hash returned by `store.GenerateAPIKey`. Race instrumentation and
  aggregate package CPU pressure amplified the authentication comparison from
  roughly 0.21 seconds normally to 2.47 seconds in isolation and beyond the
  request budget in the hosted aggregate gate.
- Fix: retain the random bearer, real harnessd auth/scope middleware, finite
  request/job deadlines, durable idempotency, run linkage, terminal
  observation, and scope assertions, but rehash that synthetic test token at
  `bcrypt.MinCost`. A deterministic cost assertion fails before dispatch if a
  production-cost hash returns to the fixture.
- TDD evidence: the new invariant first failed with `cost = 12, want 4`.
  After the fixture correction, the assembled path passed normal x25, race
  x10, the complete cron package in normal and race modes, and the repository
  regression gate (normal, all-package race, 85.5% total coverage, zero
  uncovered functions).
- Production behavior: unchanged. In particular, production bcrypt remains
  cost 12, no timeout changed, and no application retry was added; #1003
  explicitly classifies retryability without implementing remote retries.
- Delivery status: [PR #1113](https://github.com/dennisonbertram/go-code/pull/1113)
  merged to `main`; the test-only fixture repair is part of the #1106 rebase
  baseline.

## 2026-08-03 (Issue #1106 final liveness and mixed-version repair)

- Every filesystem-backed durable callback manager now acquires and holds the
  common workspace process-loss fence before `Set` or dispatch, for that
  manager's lifetime, instead of only during `Recover`. `Recover` additionally
  requires that authority; setup fails closed when it is unavailable, and the
  authority is released after failed bootstrap, on shutdown, or by process
  exit.
  Current claims persist private state `dispatching_fenced`; the pre-#1106
  binary's exact expired-reclaim predicate matches only `dispatching`. If old
  wins pending/retry admission, current leaves that live state untouched. If
  current wins, old cannot reclaim it. Manager/API reads normalize the private
  state to public `dispatching`.
- Crash recovery now requires both the kernel-released workspace lock and an
  expected-token CAS captured only from the bootstrap snapshot. It can mutate
  only current-version private `dispatching_fenced` rows, including expired or
  `NULL` leases; legacy public `dispatching` rows fail closed even when expired
  or `NULL`. A second timer in the same live manager cannot reuse the lock to
  reclaim its own unwinding admission. A killed child that claimed the current
  state is recovered under the same reserved ID after process death; stale
  observed tokens cannot clear a replacement.
- Replaced the finite local claim retry cap with cancellation-aware exponential
  rearming whose delay exponent saturates. Nine consecutive failed claim
  windows recover in the same daemon without consuming a durable admission
  attempt. Concurrent authority joins are serialized so valid concurrent Sets
  cannot fail the second check/acquire racer. The persisted safe retry reason
  remains `callback admission unavailable`; raw store/context errors are never
  surfaced.
- Deadline handoff persists the safe `callback admission unavailable` reason
  on `retry_wait`, making the retry state truthful to API consumers without
  exposing storage or context errors. Client presentation remains explicitly
  in #1007/#1009/#1010 rather than being claimed by this backend PR.
- TDD evidence: current-owner/legacy-takeover, finite-rearm, stale-token CAS,
  same-manager recovery, private cancel-state exposure, and concurrent
  authority join all failed before their production repairs. The mixed-version
  and crash/liveness matrix passes normal x30, race x20, all callback tests pass
  race x3, and the complete host tools package passes.
- The required exact-tree regression normal phase passed, but the race phase
  failed in the out-of-scope cron assembly integration test when its authenticated
  remote start exceeded the 5-second request timeout. A focused host-local race
  rerun passed x5 but reached 4.711 seconds of remote-start latency and 14.83
  seconds total. This remains an unwaived baseline blocker under #1112; no #1106
  commit or push was made and no cron code was changed here.

## 2026-08-03 (Issue #1110 — Notify-Parent Activation Test Lifetime)

- Symptom: hosted race coverage reported that a recorded-parent subagent lacked
  `notify_parent` after `StartRun` returned.
- Cause: the test used an instant provider and read the activation registry
  after starting asynchronous execution. A fast terminal run legitimately
  cleans transient activation before that read, so the assertion did not
  observe the user-visible first provider request.
- Fix: replace only the fixture with a capturing provider blocked after its
  first `CompletionRequest`; assert `notify_parent` there, then release and
  assert normal terminal cleanup. Production Runner activation remains out of
  scope unless this deterministic boundary fails.
- TDD evidence: the deliberately terminal-waited form of the old assertion
  fails deterministically (`expected notify_parent to be auto-activated...`),
  proving its lifetime boundary was wrong. The gated replacement passes
  focused positive/negative normal x1000 and race x1000 plus the adjacent
  stored parent-handoff test x100; it has no production diff.

## 2026-08-03 (Issue #994 — Terminal SSE Before Control Acknowledgement)

- Review found that `RunSession` tied control-post cleanup to `currentRunID`.
  A run's terminal SSE can arrive before its delayed approve/deny/steer HTTP
  acknowledgement, and the per-run stream then correctly clears that ID while
  incorrectly leaving `runControlInFlight` set forever.
- The deterministic red uses a real terminal SSE frame followed by delayed
  HTTP completion. It covers accepted approval, rejected steering with draft
  restoration, and verifies that reset and conversation loading still reject
  an old completion by request generation.
- Control completion now uses the request-generation ownership fence only.
  Reset/load increment that generation; a terminal event does not. This lets
  the same run settle its own pending control after it terminates without
  allowing an old conversation to mutate a replacement session.
- Follow-up review found the keyboard path did not use the visually disabled
  composer boundary: Return could start B after A terminaled but before A's
  pending control completion. `canSubmit` and `submit` now both reject while
  `runControlInFlight`; the terminal-SSE fixture attempts that exact B submit
  and proves no second start, no draft loss, and no stale error.
- Verification after the follow-up repair: focused `RunControlAckTests` passes
  16 tests; strict format, full Swift (211), build, live fake-harness (2),
  and the repository normal/race/coverage gate pass at 85.5% with zero
  uncovered production functions. Fresh independent review remains required
  before promotion.

## 2026-08-03 (Issue #1108 — Native Durable Reconciliation Barrier)

- Planned a test-only repair for a hosted native live-harness flake: terminal
  accounting can precede asynchronous durable `/messages` reconciliation.
- The accepted barrier must observe `RunSession`/`Transcript` state, then
  release the fixture and await rendered durable assistant rows; raw request
  presence and transport completion are insufficient evidence.
- The gated red failed exactly at the old premature C durable-text assertion.
  The repaired fixture passed strict format, the 11-test stream suite, C x20,
  full Swift (190), live RunSession tests (2), and Go normal/race/coverage
  regression (85.5% total, zero uncovered functions).
## 2026-08-03 (Issue #1106 durable callback claim ownership)

- Reproduced the two-manager duplicate dispatch deterministically: one
  transient heartbeat `database is locked` error canceled the original
  admission before its valid lease expired, allowing a competing manager to
  reclaim and start the same reserved callback run ID.
- SQLite callback stores now configure WAL and `busy_timeout=5000` in the
  driver DSN so every pooled physical connection receives them. `ClaimDue` and
  `ReclaimExpired` use conditional `UPDATE ... RETURNING` and verify the
  private caller token before reporting ownership; the manager retries bounded
  pre-claim contention and retains a successful lease through transient
  heartbeat errors until its confirmed deadline.
- New regressions prove transient-busy single dispatch, deadline-bounded
  surrender/takeover, pooled connection pragmas, and returned-token fencing.
- Review repair: a per-dispatch deadline guard now cancels admission even when
  `ExtendLease` remains blocked until its renewal context expires; the
  heartbeat also compares the actual return time rather than its stale tick.
  SQLite DSNs are now escaped `file:` URIs, preserving literal `?` filenames
  while applying pragma query values on every pooled connection.
- Follow-up repair: ordinary callback-store paths are first made absolute and
  then escaped as file URIs, so relative, `?`, and Windows-like names retain a
  physical database identity. A small bounded local fence
  cancels old admission before the persisted lease expires; a concurrent
  contender's starter now proves it cannot admit first at the handoff edge.
- Structural handoff repair: a local pre-expiry timer cannot establish a
  happens-before relation with another manager. A deadline-canceled owner now
  waits for `StartCallback` to return and `ReleaseLease` token-fences the row
  into `retry_wait`; ordinary timers never reclaim a live `dispatching` row.
  `RecoverExpiredLease` is reserved for the documented bootstrap process-loss
  boundary. The new contender is armed before expiry and requires durable
  release, rather than merely observing cancellation.

## 2026-08-03 (Issue #1102 — Deterministic AskUser Wait Test)

- Symptom: hosted race execution of `TestRunnerAskUserQuestionWaitsAndResumes` intermittently read `running` after `PendingInput` succeeded.
- Cause: `InMemoryAskUserQuestionBroker.Ask` deliberately registers readable pending input before its asynchronous `OnPending` notifier calls `Runner.setStatusAndEmitContext`; the old fixture used registration as though it were the later public status/event boundary.
- TDD evidence: the existing hosted exact red was `expected waiting_for_user status, got "running"`; local pre-fix `-race -count=500` passed, confirming that delay-based repetition cannot make this fixture deterministic. The revised test first failed to compile while naming the missing event-boundary test helper, then passed once the helper subscribed to runner history/live events without sleeps.
- Fix: the test now subscribes atomically after run creation, waits for `run.waiting_for_user`, then retains the pending-call ID, immediate `GetRun` waiting-state, submit, completion, provider-call, and full event-order assertions. Production runner/broker code is unchanged.
- Verification: focused normal and `-race -count=20` passed; complete `internal/harness` normal/race passed; foreground `./scripts/test-regression.sh` completed normal, full race, coverage, and the 80%/no-zero-function gate at 85.6%. The tmux run's Keychain/live-network failures were environment-only and not accepted as the final gate.

## 2026-08-03 (Issue #1006 — Callback Retry/Linkage Planning)

- Current architecture search found #1005's `CallbackManager.fire` commits
  `fired` before calling an error-only `RunStarter`; its SQLite store has only
  pending/fired/canceled semantics. The callback's resulting run is therefore
  neither durable nor linked on a failed start.
- The existing remote-cron path has a stronger, separate pattern:
  `Server.getOrStartCronRun` reserves a `run_` ID and calls
  `Runner.StartRunWithIDContext` under a durable lease. #1006 will adapt the
  Runner identity boundary for embedded callbacks without sharing cron's HTTP
  idempotency tables or changing #1005 durability behavior.
- Implementation: callback creation reserves `run_callback_<callback-id>`.
  SQLite conditionally claims due/retry work into `dispatching`, fences every
  completion by token, heartbeats live admission, reclaims expired leases, and
  records `started`, bounded `retry_wait`, or safe terminal `failed` state.
- Runner boundary: `EnsureRunWithIDContext` normalizes default scope, rejects
  prompt/scope identity conflicts, reconciles queued/terminal identities, and
  rereads duplicate-create races. A cancellation fence after durable create or
  replay preflight retains the queued identity without local publication.
- Runtime/API: `harnessd.callbackRunStarter` uses that authoritative boundary;
  callback tasks expose state, run ID, attempt, next attempt, and bounded safe
  error while never serializing lease tokens. Dispatching/terminal callbacks
  remain visible but do not advertise a cancel action.
- TDD evidence: the original retry red recorded `fired` with no attempt/next
  retry. Store review also captured a 300-byte retry summary before the 256-byte
  store fence. A later store red proved inserting Go's zero `time.Time` into
  nullable `next_attempt_at` made a future pending row immediately claimable;
  create now writes SQL NULL for an absent retry time. Focused callback/Runner
  tests pass under `-race -count=20`; the
  assembled recovery path admits the reserved run into the same tenant, agent,
  and conversation.
- Full-gate repair: a #1005 local-zone timestamp compared lexically with a UTC
  claim time lost the claim even though the parsed instant was overdue. The
  manager then rescheduled that overdue row at zero delay, producing millions
  of attempts while admission never began. Migration now accepts driver
  `time.Time`, string, byte, and NULL forms and rewrites all timestamps UTC;
  every new timestamp write is UTC and admission-wait tests are bounded.
- Verification: focused migration/cancel/parser race tests pass x20, complete
  affected normal/race suites pass, and `./scripts/test-regression.sh` passes
  at 85.5% total coverage with zero uncovered functions.
- Independent review found three release blockers. Later callback lifecycle
  events were discarded after the scheduling run became terminal; a durable
  callback-list failure could fall back to partial process memory and return
  HTTP 200; and truncating an arbitrary classified error did not prevent
  credentials from reaching SQLite, tasks, or SSE.
- Review repair: later lifecycle events publish at conversation scope without
  appending after a terminal run. Startup reads every durable callback state
  and republishes its current lifecycle snapshot before rearming active timers,
  rebuilding API/TUI replay semantics after Runner restart. Durable listing is
  error-aware and fails tasks, the agent list tool, and cancel authorization
  closed. Error summaries use a callback-owned allowlist at classification,
  persistence, read, and exposure boundaries. Focused red/green and affected
  normal/race suites pass; the final repository gate remains required on this
  reviewed candidate.
- Final gate observation: after recovery changed to all-state publication, the
  earlier active-only SQLite listing helper had no production caller and failed
  the zero-function gate. The dead interface/store method was removed; the
  all-state source of truth and compatibility pending-list method remain.
- Final verification: retained host tmux `issue1006-final-gate-v4` exited zero;
  the exact `./scripts/test-regression.sh` passed normal, full race, and coverage
  at 85.5% total with zero uncovered functions.

## 2026-08-03 (Issue #1098 — Deleted-Job Cron Reconciliation Coverage)

- Intent: restore the zero-function regression gate for the merged #1004
  deleted-job recovery helpers without broadening cron behavior.
- Planned regression: a recovered scoped active row with a definitively absent
  job must persist a failed unavailable terminal record, preserve RunID, avoid
  `TouchJobRun`, and release admission only after persistence; persistence
  failure must retain the lease and deny a duplicate.
- Status: documentation and architecture mapping precede strict red tests on
  exact `origin/main` `224d667a`.
- Outcome: direct test-only coverage exercises both `ErrJobNotFound` and
  `sql.ErrNoRows`, verifies unavailable status/RunID/error/duration, forbids
  deleted-job touches, proves persistence-before-release/readmission, and
  preserves duplicate denial on persistence failure. No production code was
  needed. Rebased focused normal/race x20 and full regression passed.

## 2026-08-02 (Issue #1093 — Deterministic Conversation-Cleaner Shutdown)

- Symptom: hosted PR #1092 race test `TestShutdownConversationCleanerCancellation` could exceed its five-second deadline. The cleaner accepted a context but exposed no completion acknowledgement, so daemon shutdown could only request cancellation before closing persistence and returning.
- TDD red: a channel-controlled cleaner observed cancellation and deliberately withheld completion. On the original code, `runWithSignalsWithDeps` returned `<nil>` before that cleaner acknowledgement; the original startup sleep was removed from the regression.
- Fix: `ConversationCleaner.Start` now returns a channel that closes exactly when its goroutine stops using the conversation store. `persistenceBootstrap` owns an idempotent cancel-and-await lifecycle; normal signal shutdown and every deferred startup-failure path invoke it before the conversation store closes.
- Compatibility: conversation retention interval, immediate startup sweep, disabled-retention behavior, pinned-conversation protection, persistence schema, API, CLI, and clients are unchanged. Startup failure still returns its original error after deterministic cleanup.
- Verification: normal signal and bound-port startup-failure tests block until a controlled cleaner releases; a direct lifecycle test proves idempotent ownership. `TestShutdownConversationCleanerCancellation -race -count=20`, `TestStartupFailureCancelsConversationCleaner -race -count=20`, combined lifecycle `-race -count=20`, and complete affected harnessd/harness normal/race suites pass. The tmux full gate's two real Keychain failures were reproduced as launch-context-only; the identical foreground Keychain tests pass, and the authoritative foreground full regression is the acceptance gate.

## 2026-08-01 (Issue #1086 — Acceptance Inventory and Evidence Schema)

- Added an additive internal compiler that derives available tool rows from the
  resolved harness registry (or the running `/v1/tools` boundary) and built-in
  TUI command rows from `NewCommandRegistry`; there is no second catalog of
  names or aliases.
- The canonical schema hashes sorted inventory rows, rejects duplicate command
  aliases and conditionless not-applicable records, records owner/condition/
  applicability, and requires exactly one intent case per available
  item/surface pair.
- Pass evidence requires exact ordered actions, matching expected postcondition
  contract, separate verified probe observation, run/conversation/event IDs,
  artifacts, timing, and verified cleanup. Tool completion or narration alone
  is not representable as a pass. Failed evidence requires a failure class.
- Added `acceptance-inventory`, a read-only command that compiles a Markdown
  report from a running daemon's `/v1/tools` catalog plus the actual TUI
  registry; it executes no tools and does not change ToolWalk behavior.
- Unavailable dynamic providers are represented as provenance-bearing `toolset`
  observations. The compiler rejects a configured unavailable provider without
  its observation and rejects unproven individual tool names, preventing both
  silent skips and fabricated static catalogs.
- Review symptom: the first Registry metadata pass flattened every default
  core/deferred source into two generic labels, while runtime MCP registration
  and `ReplaceByTag` wrote no owner or condition at all. This made the schema
  non-empty but not authoritative.
- Cause: tool construction accumulated bare `[]tools.Tool` values before
  registration, discarding the conditional branch that owned each value;
  dynamic Registry mutation paths bypassed `RegisterWithOptions`.
- Fix: the default builder now accumulates `catalogTool` values at each actual
  registration branch. Initial MCP discovery adds an exact `mcp_server:` tag,
  runtime MCP and hot reloads stamp Registry-owned provenance, and `/v1/tools`
  exposes those stable fields. No production tool-name ownership map exists.
- TDD/verification: focused reds observed missing MCP server tags, generic goals
  ownership, empty runtime MCP/hot-reload provenance, and generic conditional
  default provenance. A final fail-closed red showed whitespace-padded
  `harness.registry` could evade the generic-owner rejection; compiler input is
  now normalized before validation and hashing. The five affected Go packages
  pass normally, and the provenance/reconciliation/concurrency subset passes
  under `-race`. Swift and full repository gates are intentionally deferred to
  the promotion lane.
- Review repair: `RenderResultMarkdown` emitted its correct per-surface rows and
  then an extra six-column item summary under the seven-column table header.
  The regression observed four `tool:read` rows for its three applicable
  surfaces; removing only the unconditional legacy summary emission restored
  one well-formed row per item/surface.
- Independent review repair: evidence validation previously trusted the
  caller's `Case`, configured-unavailable reconciliation matched only a name,
  resolver provenance was dropped from the canonical item/hash, and the report
  renderer accepted raw structurally empty PASS records. Deterministic reds
  covered unknown/not-applicable items, unsupported surfaces, five provenance
  mismatches, duplicate configured toolsets, provenance-sensitive hashes,
  invalid TUI observations, and unvalidated rendering. Validation now resolves
  the authoritative compiled item and surface, configured and observed
  toolsets match the complete normalized tuple, provenance is hashed/reported,
  and rendering validates every record against its case before showing a
  result.
- Live-boundary repair: `/v1/tools` and `acceptance-inventory` previously
  carried only present tools, so a configured MCP provider that failed
  discovery vanished. The runtime now retains a redacted paired
  configured/observed toolset snapshot, preserves healthy partial catalogs,
  binds failure evidence to the exact discovery call, and exposes the pair
  additively through `/v1/tools`. Focused inventory, registry, MCP, server, CLI,
  and harnessd suites pass normally and under race.
- Exact-head coverage/review repair: the first command compiler fabricated
  every TUI row as a built-in and the HTTP/CLI resolver boundary treated absent
  evidence as an empty successful snapshot. Behavioral reds captured both
  paths. `CommandEntry` now owns owner/condition metadata for built-ins, bundle
  commands, and legacy plugins; compilation copies it and rejects omissions.
  The server requires resolver snapshots, the CLI distinguishes explicit empty
  arrays from absent/null fields, and unidentified generic MCP discovery
  failures mark resolution incomplete and make `/v1/tools` fail with 503.
- Coverage repair: the report command entrypoint has injected argument/output/
  run/exit seams with success and failure behavior tests; the obsolete
  present-only `InputFromHTTPTools` adapter was removed; `errors.Is` now proves
  `ToolsetResolutionError.Unwrap` preserves the discovery sentinel.
- Surface-runner integration repair: `ValidateCasesForSurface` validates one
  runner's complete mapping against the unchanged full inventory/hash, rejects
  missing, duplicate, stale, or cross-surface rows, and does not make an API
  runner fabricate TUI/native cases.
- Final verification: the seven affected Go packages pass normally, the full
  affected set passes under race, and the authoritative logged-in foreground
  `./scripts/test-regression.sh` passes normal, full race, and coverage at
  85.7% with zero uncovered functions. Swift is inapplicable because this
  repair changes no `macapp/` or ToolWalk schema/consumer.
- PTY-runner schema review exposed that the v1 draft collapsed canonical and
  alias spellings, required runtime IDs for local commands, supported only one
  untyped probe, and accepted bare artifact paths. Failing tests captured each
  weakness before the v2 change. Command aliases now produce distinct stable
  invocation IDs and completeness/report/evidence keys; evidence classes make
  runtime IDs conditional; every typed expected assertion requires its own
  matching verified observation; typed artifacts require kind, path, SHA-256
  digest, and an explicit redaction declaration.
- Required negative paths cannot be registry-derived. `CompileSuiteContract`
  therefore builds a separate stable-ID catalog for unknown-command and
  invalid-form scenarios, hashes it with the full inventory hash, and requires
  every selected-surface declaration exactly once. Undeclared/missing scenario
  cases and mismatched suite evidence fail; suite rendering keeps scenario rows
  visibly separate from registered inventory rows. The unshipped v1 draft is
  intentionally not accepted as a weaker compatibility path.
- Native-runner review exposed that registry presence was being treated as
  automatic native GUI applicability. A strict red showed missing/unknown
  mappings and proof-free native passes had no schema representation. Suite
  contracts now hash a complete per-item native applicability overlay with
  source references and UX rationale. Native-available items require cases;
  explicit N/A items reject cases. A native pass requires typed screenshot,
  AX snapshot, raw SSE/event, and API/store artifacts plus build SHA, bundle
  path, daemon PID/port, and asserted isolated workspace metadata.
- The pre-native-overlay v2 foreground regression checkpoint passed normal,
  full race, and coverage at 85.6% with zero uncovered functions. Focused
  final-v2 inventory normal and race tests pass. The authoritative final-v2
  foreground regression then passed normal, full race, and coverage at 85.7%
  with zero uncovered functions.
## 2026-08-03 (Issue #1096 — Deterministic Keychain Regression Gate)

- Symptom: unrelated regression runs could execute real `security add-generic-password` commands merely because `security(1)` was present, then die at the existing 15-second context deadline under an unavailable login-Keychain session.
- TDD red: new modelstore command-contract tests would not compile on the exact base because Keychain calls constructed `exec.Cmd` directly and no real-mutation opt-in or unique-account helper existed.
- Fix: modelstore now owns a package-private, default-to-`exec.CommandContext` command factory plus availability seam. Deterministic fakes cover `find`, `add -U`, and `delete` arguments; ensure secrets are stdin-only; and retain existing bounded context/error translation. Real mutation tests require `HARNESS_TEST_REAL_KEYCHAIN=1`, announce their skip reason, and use test/process-specific accounts with scoped cleanup.
- Safety: no timeout extension, retry, global serialization, suppressed error, provider behavior, persisted credential grammar, or HTTP/client contract changed.
- Verification: the exact fake/opt-in red failed to compile against base `2709fa1` because the seam/helpers were absent. Green evidence: `go test ./internal/modelstore -count=1`; `go test ./internal/modelstore -race -count=20`; and standard `-v` output showed both real mutation tests explicitly SKIP without the flag. The named host-live command passed both mutation paths for five repetitions (ten live mutations total, 1.044s). First full regression correctly failed only because the new adapter's `SetStdin` was 0.0%; a no-run adapter stream-wiring test closed that real coverage gap. The rerun `./scripts/test-regression.sh` passed normal, race, coverage 85.6%, and zero uncovered production functions.

## 2026-08-03 — Issue #1005 durable callbacks

- Added SQLite-backed callback persistence and manager recovery. The strict
  red failed because durable-store/recovery seams did not exist; the green
  proves scoped round-trip plus shutdown/restart overdue dispatch. Dispatch
  retry/idempotency is intentionally not encoded here (#1006).
- Review repair: recovery now follows successful runtime binding and listener
  acquisition; an occupied listener leaves preseeded overdue scoped work pending
  and the store reopenable. Durable cancellation persists before stopping its
  timer, one failed fired-state write gets one persistence-only re-arm, and a
  shutdown waits for a committed `StartRun` without adding dispatch retry or
  idempotency policy.
## 2026-08-03 (Issue #1038 — Native Live Terminal Usage Reconciliation)

- Symptom: the real fake-provider `RunSessionLiveTests.submitProducesTranscript` reached a completed transcript with `usage.totalTokens == 0`, making the native usage summary visibly wrong even though harnessd had completed successfully.
- Root cause: raw local SSE proved that `usage.delta` and the final terminal `usage_totals`/`cost_totals` both arrive correctly. The native conversation stream then reconciled durable messages at the terminal boundary; `Transcript.reconcile` called `load`, resetting the value-type transcript and discarding the accounting snapshot.
- TDD red: a terminal-only completed event with harnessd's real accounting JSON left all transcript totals at zero. A second deterministic red applied that terminal event and then reconciled durable messages; the rebuild reset total tokens and cost to zero.
- Fix: terminal reducers reconcile the existing final accounting snapshot before marking terminal state, and durable-message reconciliation retains that known usage while rebuilding only persisted conversation rows. Missing terminal fields preserve prior delta-derived values.
- Verification: focused reducer suite (19 tests), strict Swift formatting, build, full Swift suite (185 tests), and the same full Swift suite against a real fake-provider harness (185 tests) pass. The foreground repository regression completes normal, race, coverage, and coverage-gate phases at 85.6% total coverage with zero uncovered production functions. The equivalent tmux gate was discarded because Keychain integration prompted in that launch context.
- Review repair: accounting is now admitted by `RunSession` run identity. Every local/external run boundary clears prior totals; terminal accounting is retained through durable reconciliation only for that same accepted run; an ordinary durable sync clears unknown-run totals. Deterministic multi-run incomplete-terminal and local-stream-failure reds both previously displayed run A's `130` tokens and `$0.0025` for run B, and now pass. The full Swift suite has 187 passing tests; the repeated foreground repository gate again passes at 85.6% coverage with no uncovered production functions.
- Follow-up review repair: the per-run stream can admit a terminal event before the conversation stream receives its duplicate. Deduplication then returns `false`, but that means “already reduced,” not “unowned.” Conversation terminal reconciliation now retains accounting whenever `accountingRunID` still matches the terminal event's run. The deterministic URL-protocol regression gates the duplicate conversation terminal on completion of the per-run stream, confirms durable-message reconciliation occurred, and asserts prompt `120`, completion `10`, total `130`, priced `$0.0025`, and `available` status. The real fake-provider test now waits for the observable durable reconciliation fence (`lastEventID == nil`) before asserting all five usage fields.
- Fresh verification for that ordering repair: strict Swift format lint, build, and full suite pass (187 tests); the focused real fake-provider suite passes (2 tests). A fresh foreground `./scripts/test-regression.sh` completed normal, race, coverage, and coverage-gate phases; its newly generated profile passes at 85.5% total coverage with zero uncovered production functions.
- Final review repair: accounting admission correctly rejected a stale terminal from run A after run B took ownership, but the old path still reduced A's lifecycle and rebuilt durable rows as if A were current. Stale terminals are now lifecycle-suppressed while their durable rows are reconciled with B's usage and run state retained. A deterministic app-level barrier delivers B's full per-run terminal first, then releases A's conversation terminal and durable snapshot; it proves owner `run_b`, exact `120/10/130/$0.0025/available` accounting, B's completed state, A and B durable replies, and exactly one B durable row.
- Fresh verification for the stale-terminal repair: strict Swift format lint and build pass; the focused conversation suite passes 9 tests, full Swift passes 188 tests, and the real fake-provider suite passes 2 tests. A fresh foreground `./scripts/test-regression.sh` completed normal, race, coverage, and coverage-gate phases; its newly generated profile passes at 85.5% total coverage with zero uncovered production functions.
- Acceptance repair: the stale terminal test no longer waits for a transport response to finish. Its conversation response blocks on a named test gate; only after `RunSession` visibly exposes B's owner, completed state, and exact five accounting fields does the test release stale A, then reassert B plus durable rows. `Transcript.reconcile(preservingRunState:)` now restores failed-run errors rather than returning immediately after durable `load` erases them. The second deterministic gate drives failed B followed by stale A and proves B's event-only error survives. Fresh evidence: strict lint/build, focused 10 tests, full Swift 189 tests, real fake-provider 2 tests, and foreground repository normal/race/coverage gate at 85.5% with zero uncovered production functions.
- Final acceptance repair: terminal-only suppression still allowed stale A's earlier `run.started` through the transcript reducer. That changed completed B to running; when A's terminal then requested durable rows, `reconcilePersistedMessages` correctly refused because the UI appeared busy. Foreign frames rejected by the accounting owner now suppress all lifecycle, approval, and waiting mutations while still allowing transcript content history. The deterministic stale-A replay now places `run.started` before A's terminal after the application-level B fence, and proves B's completed state/accounting and A/B durable rows survive.
- Final cheap-review repair: local submission allocates B before its first SSE timestamp. A stale A timestamp was therefore incorrectly considered newer and could steal that provisional ownership. Foreign events no longer supersede a non-nil owner lacking a timestamp. The app-level regression opens A only after B is allocated but before B's first SSE; B remains queued with zero usage, then completes with exact `120/10/130/$0.0025/available`. Fresh verification: focused 11 tests, full Swift 190 tests, live fake-provider 2 tests, and foreground Go normal/race/coverage at 85.5% with zero uncovered production functions.
- Final Sol repair: when B's per-run stream failed before its first SSE, its nil-timestamp ownership was never released, permanently rejecting later external C. Failure handling now releases only that unobserved provisional owner; B's queued interval remains stale-protected until the failure is known. The deterministic stream-500 regression proves later timestamped C owns lifecycle/accounting, completes with `7/3/10/$0.0005/available`, and reconciles its durable reply. The pre-SSE stale-A test now waits on the terminal-triggered durable-message request rather than transport completion. Fresh evidence remains focused 11 tests, full Swift 190 tests, live fake-provider 2 tests, and foreground Go normal/race/coverage at 85.5% with zero uncovered production functions.
## 2026-08-03 (Issue #994 Native Run-Control Acknowledgements)

- Symptom: macOS run controls discarded HTTP acknowledgement failures, cleared a pending structured question before its answer was acknowledged, and could interpret a second cancel during the first request as a local force-stop.
- Cause: `RunSession` used fire-and-forget `try?` calls and a pre-acknowledgement boolean rather than request-owned state.
- Fix: the session now owns generation-scoped answer, pending-input, and shared control state; controls are single-flight, errors remain visible and VoiceOver-announced, answer state clears only for the acknowledged prompt, and steering restores an unedited draft after failure.
- Regression evidence: the stub suite covers endpoint/server failures, retry, cancel escalation, duplicate suppression, stale completions, request/prompt identity, and empty required answers.

- Review repair: steering now checks the shared single-flight guard before reading or clearing the composer draft, so a second keyboard action cannot erase new text while the first ACK is pending. Retries clear an old visible error at request start. Approve/deny remain disabled after HTTP 2xx until their own approval/terminal lifecycle event advances; per-run lifecycle generations handle ACK-versus-SSE reordering, and foreign terminal replay cannot release the current run's control.
- TDD evidence: three reds captured draft loss, stale retry error, and premature 2xx re-enable. The expanded deterministic acknowledgment suite passes 12 tests, including answer failure→retry→delayed success with duplicate suppression, the stale-A/matching-B lifecycle fence, matching lifecycle-before-HTTP-ack, and preservation of a newer error during delayed successful retry. Full Swift has 207 tests; format, build, and real fake-harness `RunSessionLiveTests` pass. Repository regression remains required before promotion.
## 2026-08-03 (Issue #1115 — Workflow Subscriber Terminal-Close Fixture)

- Symptom: hosted fast CI reported that a full-buffer workflow subscriber never observed channel closure after terminal execution.
- Root cause: production already closes and deregisters every registered subscriber under `Engine.mu`; the regression comment claimed subscribe-before-start but the test called asynchronous `Start` before `Subscribe`, allowing a loaded host to finish and delete the run's subscriber map before the test registered its channel.
- TDD red: an explicit execution gate was added and intentionally withheld; the focused test failed after 3.05 seconds with `run ... did not complete`, proving the gate controls script progress rather than relying on scheduling.
- Fix: release the chatty script only after `Subscribe` returns, emit 100 logs without draining, then require exactly 64 ordered buffered log events followed by `ok=false`. Production workflow, callback, cron, and late-subscriber replay semantics are unchanged.
- Verification: the focused close/cancel pair passes normal and race at `-count=100`; the complete `internal/workflow` package passes normal and race. The authoritative logged-in foreground repository regression passes normal, race, and coverage at 85.6% with zero uncovered functions. A prior tmux run failed only the two real-Keychain tests by `security(1)` process kill, matching the documented macOS launch-context boundary; workflow passed in that run too.

## 2026-08-01 (Issue #1083 — Approval Publication Readiness)

- Symptom: a live SSE client could receive `tool.approval_required` and immediately POST `/approve` or `/deny`, but the shared broker had not yet registered the request and the server correctly returned `ErrNoPendingApproval` as HTTP 404.
- Cause: both the ordinary tool gate and plan-exit gate emitted their approval-required event before invoking `ApprovalBroker.Ask`; both concrete brokers create pending state inside `Ask`.
- TDD red: `TestE2E_ToolApprovalEventIsImmediatelyResolvable` used a test-only pre-registration gate on the legacy `Ask` path, observed the real HTTP/SSE event, and deterministically failed `POST approve immediately after event: expected 200, got 404`.
- Review TDD red: registering with a 20 ms deadline, successfully approving or denying before `Wait`, delaying `Wait` for 40 ms, then waiting returned `ApprovalTimeoutError` for both in-memory and checkpoint brokers. The event precision regression also parsed the emitted timestamp and found it truncated fractional seconds relative to `PendingApproval.DeadlineAt`.
- Fix: the existing `ApprovalBroker` now separates `Register` from `ApprovalWaiter.Wait`. In-memory entries and checkpoint records are registered before the runner emits tool or plan approval events; the waiter retains a decision that arrives before it starts waiting. The tool event reads `deadline_at` from the exact registered pending entry rather than a second clock read.
- Review fix: in-memory resolution records its decision under the same mutex used by expiry, while checkpoint expiry uses `ExpirePending`; whichever operation wins is authoritative. A resolution winner is returned even after delayed `Wait`, and an expiry winner makes late approve/deny return `ErrNoPendingApproval`. Tool deadlines now use `RFC3339Nano`, so parsing the event round-trips the exact registered timestamp.
- Compatibility: direct `Ask` remains register-and-wait; duplicate/late resolution, option selection, timeout, fail-closed tool execution, and in-memory cancellation cleanup retain their existing behavior. Checkpoint parent-context cancellation continues to return cancellation while retaining the durable pending record (pre-existing; not changed by this slice); checkpoint expiry remains timeout-owned.
- Verification: focused harness/server/E2E approval regressions passed normally at `-count=10` and under `-race -count=5`, covering immediate approve and deny, terminal tool and plan conversations, in-memory and checkpoint registration readiness, timeout/duplicate/cancellation characterization, and existing HTTP routes. The concurrent resolution-vs-expiry tests additionally passed `-race -count=100` for both brokers.

## 2026-08-01 (Issue #1081 — Portable Keychain Parser Coverage)

- Root cause: hosted Ubuntu `test-regression` run `30672776651` completed the
  normal/race suites and profile collection, then rejected exact `main` because
  `internal/modelstore/credref.go:keychainParts` was 0.0%. macOS reaches that
  helper through the Darwin-only real Keychain integration; Linux correctly
  returns from `KeychainAvailable` before any Keychain CRUD path calls it.
- Fix scope: add portable, table-driven direct validation of the existing pure
  parser. The test preserves the first-slash split, retained account slashes,
  and established malformed-reference error contract without invoking
  `security(1)` or changing production credential behavior.
- Verification evidence: targeted modelstore normal/race and the authoritative
  foreground full regression pass; the latter completes normal, full race,
  and coverage at 85.7% with zero uncovered production functions. The retained
  Darwin integration passes in the logged-in foreground host context.
- Launch-context diagnostic: an earlier tmux-hosted full run killed both real
  Keychain tests at their 15-second process boundary. Re-running the identical
  candidate in the required non-TTY foreground host context passed, isolating
  the red to Keychain session access rather than the parser test.

## 2026-08-01 (Issue #1056 — TUI Terminal Assistant Message)

- Symptom: real non-streaming runs persisted `assistant.message` followed by
  `run.completed`, while the TUI displayed the user prompts but no assistant
  reply and exported no assistant transcript row.
- Cause: the SSE bridge forwarded `assistant.message`, but `Model.Update`
  reduced only `assistant.message.delta`; valid final-only responses therefore
  left `lastAssistantText` empty.
- Current-main red: two complete final-only turns produced only the two user
  transcript entries instead of exact user/assistant/user/assistant order.
- Fix: the existing reducer treats non-empty `assistant.message.content` as the
  authoritative response and reuses the active bubble renderer. Tool start
  closes the prior bubble's viewport-tail ownership, and a per-run finalization
  bit makes terminal replay/completion consumptive while reopening on the next
  `RunStartedMsg`.
- Regressions: final-only, delta plus identical/differing final, mixed
  delta -> tool -> final, replay idempotency, repeated completion, and two-turn
  viewport/transcript ordering.
- Focused evidence: the required two-turn red is green; the focused assistant
  suite and complete TUI suite pass normal and race. The full repository gate
  passes at 85.7% coverage with zero uncovered production functions.
- Real PTY evidence: an isolated exact-candidate `harnessd` and `harnesscli`
  rendered `PTY_1059_USER_ONE`, `PTY_1059_REPLY_ONE`,
  `PTY_1059_USER_TWO`, `PTY_1059_REPLY_TWO` once and in order. Both runs used
  the same conversation id; raw SSE emitted one authoritative
  `assistant.message` before `run.completed` per run; HTTP and SQLite stored
  exactly four alternating rows; and a fresh `--resume` TUI replayed the four
  rows once with an active composer.
- Exact-head review finding: `/resume` appended its user row but did not clear
  `lastAssistantText`; `RunStartedMsg` reopened transcript finalization while
  retaining the prior reply. A contentless completed or failed continuation
  therefore exported that stale reply again.
- Review fix: `RunStartedMsg`, the shared boundary for initial and continuation
  API starts, now clears the assistant accumulator before reopening per-run
  finalization. The actual `/resume` command regression failed with a third
  stale assistant row for both `run.completed` and `run.failed`, then passed
  with exactly the prior assistant row and continuation user prompt. The
  focused resume, new-content, replay, and two-turn matrix passes normal and
  race on the rebased candidate.
- Hosted-test isolation follow-up: the first terminal event correctly consumed
  the injected cancel function, so the regression's continuation start opened
  a real SSE bridge against its command-only `httptest.Server`; GitHub race
  exposed the unexpected `GET /v1/runs/run_next/events`. Reinstalling the
  cancel seam between runs keeps the test on its intended reducer/API path.
  The focused regression passes 20 normal and 10 race repetitions. The same
  hosted wave also hit the unrelated workflow subscriber-close timing test;
  that baseline test passed 20 normal and 10 race repetitions locally and is
  being rechecked independently rather than waived.

## 2026-08-01 (Conversational Cron CRUD Acceptance Repair — Issue #1002)

- Symptoms: model GET could fall through to global name lookup; same-name global lookup selected an arbitrary scope; active resume leaked a second robfig entry; scheduler failure followed persistence without rollback; migration recognized only a narrow `UNIQUE` spelling.
- Final lifecycle repair: create and paused→active resume use paused-first registration so failure remains restart-safe. Active schedule replacement uses inert `Prepare` → durable CAS → infallible `Commit`, preserving the prior active row/entry on prepare or CAS failure. Registration identities are globally monotonic; after jitter and durable reload, identity validation and `CreateExecution` form one scheduler-locked admission point shared with prepare/commit/remove.
- Deterministic reds: embedded model get accepted `shared-name`; the ID route invoked name lookup; global same-name lookup lacked `IsJobAmbiguous`; duplicate add left two live entries; quoted/bracketed/backtick migrations retained global uniqueness.
- Fix: split `/v1/jobs/{id}` from explicit `/v1/jobs/by-name?name=...`, add typed ambiguity, put remote/embedded ownership in SQLite predicates, replace old live entries through prepared scheduler transactions, and broaden transactional migration recognition. Query encoding preserves arbitrary non-empty names, including slash, spaces, percent, and Unicode.
- Durable proof: a four-variant legacy matrix preserves two jobs, two execution rows, run metadata, exact timestamps, and scoped uniqueness across an idempotent second migrate; `integrity_check` and `foreign_key_check` pass.
- Earlier candidate evidence (superseded by the lifecycle follow-ups below): remote CRUD/history and concurrent update/delete tests passed the then-current focused packages. It is not latest-head broad/full regression evidence.
- Review follow-up: `url.PathEscape` could not preserve a slash once Go exposed
  decoded `URL.Path`. The exact operator route now reads `name` from the query,
  rejects empty input at client/server boundaries, and advertises GET only.
  Slash/space/percent/Unicode regressions passed normal/race; complete
  `internal/cron` passed normal in 8.783s and race in 10.807s.
- Blocking review follow-up: production registries captured the raw cron
  adapter, pause/resume omitted the model's read version, and DDL regex parsing
  missed legal `UNIQUE(name COLLATE NOCASE)`. The mutation audit found the same
  stale-write gap on model delete.
- Deterministic reds: assembled embedded and remote default registries both let
  scope B read scope A; pause/resume/delete schemas required only `id`; stale
  model delete succeeded; and the semantic index inspector was absent while
  the collated migration variant retained global uniqueness.
- Fix: `NewDefaultRegistryWithOptions` now applies one idempotent scoped client,
  which covers top-level, worktree per-run, and subagent registry construction
  while operator adapters remain raw. Pause/resume/delete require
  `expected_updated_at`; remote and embedded delete use persistence CAS and
  return typed conflict. Migration now uses SQLite `index_list`/`index_xinfo`
  key metadata and ignores composite or partial uniqueness.
- Superseded lifecycle-convergence chronology: deterministic remote and embedded reds used
  scheduler add/replacement failures plus an injected `TouchJobRun` between the
  successful write and rollback CAS; both left an active durable row divergent
  from live dispatch. Separate create reds made scheduler registration and
  compensating delete fail, leaving an active stored orphan.
- Superseded fix chronology: both adapters called a rollback recovery policy. Rollback conflict
  reloads durable authority and re-registers that exact active row; persistent
  registration failure calls atomic `DeactivateJob`, which changes only status
  and version, then removes live dispatch. Failed create deletion uses the same
  durable deactivation. This design and the dead `DeactivateJob` API were later
  replaced by prepared scheduler transactions.
- Lifecycle-convergence verification: the bounded nine-package normal command
  passed in 8.993s/11.698s/1.642s/9.359s/1.307s/1.547s/4.757s/5.893s/3.169s;
  the same package set with `-race` passed in
  11.223s/12.901s/2.103s/10.298s/1.416s/2.454s/4.639s/7.720s/4.077s. No full
  regression, staging, commit, rebase, push, server launch, or GitHub mutation
  was authorized.
- Durable proof: the five-variant migration matrix preserves jobs, executions,
  timestamps, foreign keys, and integrity across an idempotent second migrate;
  assembled embedded/remote registries reject cross-scope and stale mutations
  without a manual wrapper.
- Focused verification: `go test ./internal/cron ./internal/harness/tools/... ./internal/harness ./cmd/harnessd -count=1` passed all nine emitted packages in 9.284s/11.486s/1.553s/9.323s/1.295s/1.573s/4.473s/6.469s/3.333s; the same bounded package set with `-race` passed in 10.958s/12.295s/2.499s/10.693s/1.971s/2.268s/4.424s/6.985s/3.122s. No full regression, live server, commit, push, or GitHub mutation was authorized.
- Final read-only review found that `cron_get` converted every history retrieval
  error into `recent_executions: []` without an availability signal, letting a
  model mistake database failure for proof that a job never ran. The new red
  required explicit unavailable state and warning while keeping the job and
  backward-compatible empty array. The tool now emits
  `recent_executions_available` on every result and
  `recent_executions_warning` on failure; its description documents the
  interpretation rule.
- Latest admission-lock and history-availability verification:
  `go test ./internal/cron ./internal/harness ./internal/harness/tools ./internal/harness/tools/deferred ./cmd/harnessd -count=1`
  passed in 9.522s/5.596s/11.403s/9.466s/2.402s; the same five packages with
  `-race` passed in 11.202s/8.741s/12.588s/10.954s/4.512s. The focused history
  red failed for missing availability/warning fields before production code;
  its normal/race green passed in 0.330s/1.349s. The candidate was then rebased
  onto `origin/main` `3506e01c`.
- Live provider compatibility follow-up: an OpenAI-compatible harness canary
  rejected `cron_create` before model execution because the function schema
  carried top-level `oneOf`. Those providers require the top-level schema to
  be a plain object and reject composition keywords there. The schema now
  advertises the optional shell `command` and harness `prompt` fields without
  top-level composition; the existing handler remains the fail-closed authority
  for execution-type pairing and required non-empty payloads. The focused
  regression asserts every provider-forbidden top-level composition keyword is
  absent while retaining the required object root.
- Final exact-tree verification: the foreground `./scripts/test-regression.sh`
  passed normal, complete race, and coverage at 85.7% total with zero uncovered
  production functions. The assembled core-registry regression also checks
  every visible root schema and explicitly covers all eight cron tools.
- Real-provider proof: OpenAI `gpt-4.1-mini` invoked all eight model-facing cron
  tools in one persisted conversation. The first job fired twice into distinct
  scheduler-started runs whose assistant output appeared in conversation SSE
  and transcript. A stale update version was rejected; a fresh update moved the
  schedule to 2027 and changed the harness prompt; get/history returned both
  execution records with linked run IDs; pause/resume changed durable status;
  and versioned delete ended with `jobs: []` plus HTTP 404 for the former ID.
  All eleven runs completed with the exact tenant/conversation/agent tuple.
- Final promotion-review repair: `cron_create` still told the model to use
  `bash` plus `sleep` for one-shot delayed work, contradicting the core-visible
  `set_delayed_callback` path and its same-conversation continuation intent.
  The description regression failed on the old guidance, then passed normal
  and race after routing one-shot conversational work to
  `set_delayed_callback`. Frontier review identified the defect and an
  independent cheap-agent exact-diff re-review returned CLEAR.
## 2026-08-01 — Issue #1003 cronsd ingress hardening

- Added mandatory static bearer ingress bound to one configured tenant,
  constant-time token comparison, authenticated `/readyz`, and authentication
  on all `/v1/jobs` CRUD/history routes. `/healthz` remains minimal liveness.
- Create derives tenant from the authenticated principal; list/get/update/
  delete/history return only owned rows. Startup claims legacy shell rows and
  rejects unowned harness or foreign-tenant rows before scheduler start.
- Wired `HARNESS_CRON_API_KEY` and `CRONSD_API_KEY` through the real harnessd
  and cronctl clients; missing runtime credentials fail before first use.
- Added real-HTTP full CRUD/history, auth, spoofing, tenant isolation,
  readiness, startup, legacy compatibility, and caller-wiring regressions.
- Follow-up P1 repair: added SQLite `ClaimJobTenant` with one conditional
  `UPDATE ... RETURNING` linearization point. Server visibility and daemon
  startup require the persisted claim; non-claiming stores fail closed.
- Follow-up P1 repair: run migration now scans normalized legacy
  tenant/agent ownership, rejects historical or persisted disagreement, and
  backfills owners in an immediate transaction. Upgrade/restart, preserved-row,
  two-store migration, two-server HTTP, and two-startup races are covered.

## 2026-07-31 (Workflow Initial Write Exit Arbitration — Issue #1076)

- Symptom: hosted `test-race` run `30660042116` reported only
  `write |1: broken pipe` for a source-workflow child that wrote
  `child stderr diagnostic` and exited status 7.
- Cause: the first `enc.Encode(start)` error returns directly after killing the
  process group, before stdin close, `cmd.Wait`, bounded stderr collection, and
  `resolveSourceWorkflowOutcome`.
- TDD contract: hold the parent after `cmd.Start` until a FIFO plus OS-released
  advisory lock prove the real child wrote stderr and exited; require child-exit
  diagnostics and a reaped PID. Extend the pure resolver table for initial-write
  precedence and its standalone-error control before production edits.
- First red: the exited-child fixture returned raw EPIPE instead of exit status
  7 plus stderr; the standalone resolver control returned missing-result.
- Review red: a live child closed stdin and remained active; cleanup killed and
  reaped it, but the resulting `signal: killed` wait error incorrectly masked
  the initial EPIPE.
- Fix: capture the initial-write error, retain process-group cleanup,
  skip protocol serving, then enter the same close/wait/arbitration path used by
  every other started-child outcome. Record when this path successfully requests
  SIGKILL and classify that matching wait status as cleanup, while natural exit
  status 7 remains primary with bounded stderr.
- Attribution boundary: a matching SIGKILL after this cleanup request cannot be
  distinguished from an identical concurrent signal without broader WNOWAIT or
  process-supervision machinery; EPIPE is intentionally primary in that narrow
  ambiguous case. Natural exit statuses remain unambiguous.
- Green evidence: both lifecycle branches and the resolver plus real timeout
  passed; focused normal/race x100 passed in 84.986s/90.588s; workflow
  normal/race passed in 13.719s/16.534s; and `make test-race` passed. Full
  non-PTY regression passed normal, full race, and coverage at 85.6% with zero
  uncovered functions. Parent-run hosted gates remain.

## 2026-07-31 (Runner Dispatcher Shutdown Isolation — Issue #1068)

- Symptom: `go test -race ./internal/harness -count=5` failed four of five
  repetitions because `TestRunnerWithoutShutdownLeaksDispatcher` found some
  `poolDispatcher` frame after its target Runner's `Shutdown` returned.
- Cause: the test scanned all goroutine stacks by shared function name, so the
  assertion had no target identity. Review then found a second defect: five
  bounded construction sites in `runner_worker_pool_test.go` create seven
  Runners per package repetition and omitted `Shutdown`, so their dispatchers
  survived after their tests completed. The production path already closes a
  target's `done` channel and waits its `dispatcherWG` before returning.
- TDD red: a deterministic two-Runner fixture kept a control Runner alive,
  shut down the target, and failed the old global-absence assertion immediately.
- Review TDD red: a bounded Runner returned from a subtest without its exact
  dispatcher-exit hook firing; the parent then shut it down explicitly so the
  red proof did not itself leak.
- Fix: replace target lifecycle inference with a narrow per-Runner dispatcher
  exit hook invoked immediately before the existing `dispatcherWG.Done`.
  The test blocks that exact target hook, proves `Shutdown` cannot return,
  releases it, then proves Shutdown returns while the control's global stack
  frame remains visible.
- Review fix: a shared worker-pool test constructor now registers cleanup that
  releases any blocked provider before calling bounded `Runner.Shutdown` with
  a five-second diagnostic deadline. Every affected worker-pool fixture uses
  that constructor; the cleanup regression itself blocks inside the provider
  until cleanup establishes the required release-before-Shutdown ordering.
- Compatibility: queue draining, inflight accounting, cancellation timeout,
  idempotency, and production shutdown ordering are unchanged.
- Verification: the cleanup regression passed normal/race; all worker-pool
  tests passed normal/race at `-count=100`; complete harness race passed at
  `-count=5`; harness vet passed; and unchanged foreground non-TTY
  `./scripts/test-regression.sh` passed at 85.6% total coverage with zero
  uncovered functions.

## 2026-07-31 (Terminal Status/Event Atomicity — Issue #1067)

- Symptom: aggregate race load exposed `RunStatusFailed` from `GetRun` while
  the same run's immediate replay ended at `llm.turn.requested` without
  `run.failed`; code inspection found the same status-first window on completed
  and cancelled paths.
- Cause: every terminal helper called `setStatus` before `emit`, splitting the
  public run record from the event journal's ledger, bounded store append,
  subscriber fanout, and recorder drain.
- Deterministic red: a no-sleep transition barrier reproduced all three states.
  Completed replay lacked `run.completed`, failed replay contained the required
  `error.context` but lacked `run.failed`, and cancelled replay lacked
  `run.cancelled` while `GetRun` already returned each terminal status.
- Fix: one `transitionTerminal` seam now lets the winning terminal emit seal and
  append the matching event, completes bounded store append and ordered recorder
  dispatch/drain, conditionally persists the matching status, commits in-memory
  status, then fans out. Every status transition shares a per-run mutex, so a
  delayed running/waiting snapshot cannot overwrite terminal state.
- Preserved reliability: terminal store I/O remains outside `Runner.mu`, and
  status-store I/O remains outside the global conversation journal lock;
  a refcounted per-conversation sequence guard prevents same-conversation
  overtaking while unrelated `GetRun` and unrelated event journals stay
  responsive, then reclaims idle keys. Terminal redaction sealing, event IDs,
  causal/error snapshot order, recorder order, backup, and pruning contracts
  remain intact.
- Failure policy: retained terminal status persistence is never attempted when
  terminal append reports failure. If append succeeds but final status update
  errors or reaches its context deadline, durable status may remain
  non-terminal while the durable event, in-memory terminal state, and subscriber
  fanout proceed. This is the strongest one-way guarantee available without a
  store transaction and does not claim two-way atomicity.
- Explicit exception: existing terminal `StorageModeNone` configurations still
  suppress the matching replay event while sealing and publishing status, as
  pinned by terminal-redaction tests. The stronger replay implication applies
  to terminal events retained by policy.
- Review regressions: append failure cannot persist terminal status; status
  update failure/timeout still completes live publication; unrelated
  conversations progress during terminal I/O while target events cannot
  overtake; delayed non-terminal status cannot overwrite terminal; explicit
  terminal redaction waits for recorder drain; and contended/distinct keyed
  locks reclaim to zero.
- Exact-head retention cause: the terminal event success marker made a run
  eligible for pruning even when the matching final `UpdateRun` failed, because
  that return value was discarded. The truthful in-memory terminal state could
  be evicted while the durable row remained running. Both append- and
  status-failure exceptions could also accumulate without an admission bound.
- Retention/admission fix: terminal event resolution and terminal status
  persistence are tracked separately. Store-backed pruning requires both;
  `StorageModeNone` is explicit event suppression plus durable status, while
  no-store runs remain process-local. Both unresolved append and status states
  count toward `MaxCompletedRetention`. At the cap, Start/Continue retry only
  status gaps under one shared deadline of at most 250 ms and otherwise return
  `TerminalDurabilityBackpressureError`; their HTTP routes map it to 503
  `terminal_durability_unavailable`.
- Recovery and boundedness: no recovery store I/O holds Runner, status,
  event-journal, or conversation locks. Status overwrite retries are safe and
  immediately restore the completed-retention bound before reopening admission
  once acknowledged. Ambiguous failed appends are protected but never retried
  because a third-party store may have applied the append.
  Already-admitted work finishes and remains visible; admission closes at the
  cap so permanent failure growth stops at that finite admitted population.
- Exact-head regressions: retention 1 preserves several already-admitted
  UpdateRun-failed completions while durable rows stay non-terminal; concurrent
  admissions reject during outage and recover under race; one blocked retry
  proves the shared deadline and unlocked state/journal access; append failure,
  StorageModeNone, no-store, Continue error precedence, and both HTTP 503 routes
  are pinned.
- Concurrent review red: a phase hook paused Continue immediately after its
  completed-source validation. A concurrent Start recovered three pending
  statuses, pruned the oldest validated source at retention 1, and the resumed
  Continue deterministically failed with `run not found`.
- Concurrent review fix: validation now increments an in-state continuation
  reservation under `Runner.mu`. The one shared completed-run prune candidate
  filter excludes reserved sources for every caller. A defer releases on every
  success/error path and immediately re-prunes; the existing later write-lock
  `continued` check still chooses exactly one continuation winner. Recovery
  store I/O remains outside Runner/status/journal/conversation locks.
- Gate-test correction: full race exposed one test treating terminal replay as
  proof status had already committed. The one-way contract is the reverse:
  terminal status implies replay, while replay may lead status. The test now
  waits independently for failed status and retains both replay/status checks.
- Verification: the concurrent red is green with cleanup and single-winner
  controls normal/race at `-count=100`; the expanded durability harness and HTTP
  suites pass normal/race at `-count=100`; complete `internal/harness` plus
  `internal/server` normal/race and affected `go vet` pass. The final direct
  foreground non-TTY `./scripts/test-regression.sh` passes normal, full race,
  and `coveragegate: PASS (total=85.7%, min=80.0%, zero-functions=0)`.
- Hosted settled-helper symptom: exact rebased race run `30656467482` failed
  `TestRunnerHookErrorFailOpen` because `collectRunEvents` returned terminal
  replay history before the valid later status commit; the immediate `GetRun`
  still reported `running`.
- Audit/cause: 215 `collectRunEvents` references, its sole configurable-timeout
  caller, and 79 `collectEvents` snapshot references were reviewed. No shared
  collector caller intentionally observes the event-leading-status window.
  Exact ordering regressions use direct phase hooks/subscriptions; snapshot
  consumers either wait separately or intentionally inspect nonterminal state.
- Deterministic red/fix: a pre-status terminal barrier made replay visible and
  failed the old collector immediately with `returned before terminal status
  commit`. Both collector variants now preserve event assertions and then poll
  for any terminal status within the same total deadline. A missing status is a
  timeout failure rather than a false settled result; production order is
  unchanged.
- Hosted-equivalent verification: the collector, all hook scenarios, and the
  configurable-timeout caller pass normal/race at `-count=100`; `make
  test-race` passes. The final outside-sandbox foreground
  `TMPDIR=/private/tmp GOCACHE=/private/tmp/gocode-go-cache
  ./scripts/test-regression.sh` passes normal, full race, and
  `coveragegate: PASS (total=85.7%, min=80.0%, zero-functions=0)`. GitHub
  comment publication was blocked by external-write safety review and was not
  retried; this repository evidence records the run.
- Exact-head helper review: commit `8757e8a3` still let settlement succeed when
  a closed stream had no terminal event, or when the sole terminal event did
  not match the later terminal status. This was a P1 test-integrity defect, not
  a production-path defect: it could mask the exact #1067 invariant across the
  shared collector callers.
- Review TDD reds: a completed run plus closed stream containing only
  `run.started` returned success, and failed status plus `run.completed`
  returned success. Both failures were immediate and deterministic.
- Review fix: the test-only subscribed-stream core now requires exactly one
  terminal event and requires its completed/failed/cancelled meaning to match
  the observed terminal status. Event slices remain unchanged on success and
  error, and both event collection and status settlement consume the original
  single deadline. The phase regression now waits for an explicit settlement
  entry signal before proving the collector cannot return ahead of status.
- Review verification: the two regressions, explicit settlement barrier, hook
  family, and configurable-timeout caller pass normal/race at `-count=100`.
  Outside-sandbox `make test-race` passes. The authoritative foreground
  `TMPDIR=/private/tmp GOCACHE=/private/tmp/gocode-go-cache
  ./scripts/test-regression.sh` passes normal, full race, and
  `coveragegate: PASS (total=85.7%, min=80.0%, zero-functions=0)` on this exact
  follow-up diff.
- Promotion integration regression: the semantic merge with #1054 initially
  persisted every non-terminal status before committing it in memory. A failed
  best-effort `UpdateRun` therefore left an executing run visibly stale; the
  AskUser broker could own a live pending question while API, TUI, and GUI
  still saw `running` instead of `waiting_for_user`. A deterministic failing
  store test reproduced `queued` after a requested transition to `running`.
  The unified per-run status lock now commits live non-terminal state first,
  retaining the persistence attempt and false return so strict
  waiting-status/event publication can retry without making the pending prompt
  invisible. Terminal transitions retain their event-before-status contract.

## 2026-07-31 (Provider-Key Matrix Health Wait — Issue #1062)

- Symptom: hosted race run `30583930460` failed
  `TestMatrix_ProviderAPIKeyCapture` when its three-second `/healthz` wait
  expired; the same address began listening roughly one second later.
- Cause: this fixture used a startup budget shorter than the established
  ten-second `runMatrixTest` budget and measured shared race-runner scheduling
  latency instead of the provider-key capture invariant.
- Planned fix: retain the real health probe, exact captured-key assertion, and
  bounded shutdown, but use the established ten-second matrix startup budget.
- Verification contract: focused normal/race stress, adjacent matrix tests,
  the complete regression gate, and hosted PR checks.
- Result: provider-key capture passed normal/race at `-count=100`; the complete
  matrix and harness slices passed normal/race; subscriber-pinned retention and
  adjacent pruning stress stayed green; and `./scripts/test-regression.sh`
  passed with 85.6% total coverage and zero uncovered functions.
- Hosted result: PR #1063 `test-fast` run `30592818353` and `test-race` run
  `30592818361` both passed on the pushed exact head.

## 2026-07-31 (Issue #1064 — Workflow Exit Error Precedence)

- Symptom: hosted PR #1057 run `30592451360` observed
  `TestSourceManagerRunWorkflowFailsOnProcessExit` return
  `write |1: broken pipe` even though the workflow child exited status 7.
- Cause: `runSourceWorkflow` captured both `closeErr` and `waitErr` after
  protocol serving, then returned the stdin-close cleanup error before
  inspecting the primary process-exit error.
- Fix: extracted one internal `sourceWorkflowOutcome` arbitration seam and
  ordered terminal evidence as deadline, protocol, non-zero process exit with
  bounded stderr, stdin-close cleanup, missing result, and success. Process
  cleanup and waiting remain unchanged.
- TDD evidence:
  - the deterministic dual-error test supplies both `syscall.EPIPE` and
    `exit status 7` without sleeps; against the old ordering it failed with
    actual `broken pipe`;
  - the same assertion is green after the fix and includes the child stderr;
  - table controls pin timeout/protocol precedence, close-only reporting,
    missing-result behavior, success behavior, and stderr bounding;
  - focused arbitration and real-child normal/race stress passed at
    `-count=100`;
  - complete `internal/workflow` normal/race passed;
  - unchanged foreground non-TTY `./scripts/test-regression.sh` passed normal,
    race, and coverage at 85.6% with zero uncovered functions.
## 2026-07-30 — Issue #1052 provider API-key capture readiness coupling

- Symptom: PR #1051's hosted race job failed because
  `TestMatrix_ProviderAPIKeyCapture` did not observe `/healthz` within three
  seconds; the same job log showed the server listening immediately after the
  deadline.
- Cause: A provider-configuration unit contract was synchronized through the
  entire parallel harness startup path, adding unrelated scheduler, watcher,
  persistence, and HTTP timing.
- Intended fix: Publish a test-local signal from the injected provider factory,
  assert the captured sentinel after that signal, then keep the existing
  interrupt and bounded shutdown proof.
- Scope: Test and documentation only; no runtime behavior change.
- TDD evidence: Adding the direct signal wait without emitting it failed with
  `timed out waiting for provider factory`; closing the channel after protected
  key capture made it green.
- Verification: Focused normal and race tests passed 100 repetitions each;
  complete `cmd/harnessd` normal/race suites passed; the repository regression
  gate passed normal, race, and coverage at 85.6% with zero uncovered functions.

## 2026-07-30 — Issue #1054 wait state precedes pending input

- Symptom: Hosted race execution observed `waiting_for_user`, then
  `PendingInput` returned `no pending input`.
- Cause: `runner_step_engine.go` publishes status/event before invoking the
  AskUserQuestion tool; broker registration happens later inside the handler.
- Impact: Event-driven TUI/macOS clients can render a wait state with no
  question available to display or submit.
- Intended fix: Let each broker notify after its pending state is
  readable/durable, forward that notification through the core tool, and
  publish the runner wait state from that point.
- TDD evidence: A gated broker made registration impossible while the tool was
  entered; pre-fix status was already `waiting_for_user`, proving the gap. The
  test now keeps status `running` until registration, then requires both
  readable pending input and `waiting_for_user`.
- Implementation: `AskUserQuestionRequest.OnPending` is a typed,
  post-registration notifier. Both built-in brokers start it exactly once with
  the question's deadline context; the core tool forwards it from context; the
  runner uses it to publish status and the existing event without polling.
- Verification: Focused AskUser/wait suites passed 100 normal and 100 race
  repetitions; complete harness normal/race suites passed; repository normal,
  race, and coverage gates passed at 85.6% with zero uncovered functions.
- Review follow-up: Exact-head Codex review correctly noted that both brokers
  computed `DeadlineAt` before `OnPending` but started their timeout afterward.
  Regressions that held notification beyond the deadline failed on both
  backends, then passed after the timer/context moved before notification.
  These deadline tests passed 10 normal and 10 race repetitions; complete
  harness normal/race and repository normal/race/coverage gates passed again.
- Second review follow-up: Starting the clock was insufficient because a
  notifier that never returned still prevented `Ask` from selecting the
  expired timer. Strengthened regressions kept both notifiers blocked while
  requiring `Ask` to return its timeout. Brokers now run notification
  independently with the same deadline context, while answer/cancel/timeout
  selection continues immediately; the runner checks that context before
  status and event publication.
- Third review follow-up: Letting answer selection race notification created
  the opposite ordering bug: a quick submission could emit `run.resumed` while
  waiting-state persistence was still blocked, then cancel the notifier before
  `run.waiting_for_user`. A deterministic blocking-store regression reproduced
  the reversed event order. Brokers now wait for notification completion before
  consuming a buffered answer, while the same deadline remains independently
  enforceable if notification stalls.
- Fourth review follow-up: Deadline resolution still exposed two durable-state
  races. A timely checkpoint answer could be overwritten as expired, and a
  blocked stale `waiting_for_user` write could land after the terminal failure
  write. Checkpoint resolution is now serialized and `ExpirePending` never
  replaces an accepted result; run status persistence uses a monotonic in-memory
  version and rewrites the latest state after any stale write completes.
  Deterministic regressions cover both races, followed by the full normal,
  race, and coverage gate at 85.6% with zero uncovered functions.
- Fifth review follow-up: The in-memory broker was not symmetric with the
  checkpoint broker when a timely answer was buffered while notification hit
  its deadline, and a losing checkpoint resume still returned false success.
  In-memory submission now publishes the buffered answer before removing the
  pending entry, deadline cleanup returns any accepted answer, and checkpoint
  resolution returns exported `ErrAlreadyResolved` when another terminal
  transition already won. Focused normal/race stress covers both contracts.
- Sixth review follow-up: The ordinary checkpoint wait deadline still bypassed
  accepted-answer recovery, and the new sentinel escaped as HTTP 500 through
  run input, approval/deny, and generic checkpoint resume paths. Both AskUser
  deadline branches now share one pending-only expiry/recovery function;
  broker/runner boundaries normalize lost races to their existing no-pending
  contracts; and generic resume returns stable `409 already_resolved` without
  changing status, payload, or update time. Deterministic gates cover accepted
  resume-before-notify, approval/deny expiry races, repeated resume, and both
  API error shapes without unbounded channel receives.
- Seventh review follow-up: Accepted-answer recovery at the notifier deadline
  returned before the blocked pending-state publication completed, so
  `run.resumed` could still overtake `run.waiting_for_user`. Deterministic
  regressions now require both brokers to retain the accepted answer without
  returning it until pending publication finishes. Unresolved timeout paths
  remain independent, while accepted answers wait on notification completion
  with the parent context as the cancellation escape hatch.
- Eighth review follow-up: A broader concurrency review found four remaining
  ownership gaps. The built-in notifier used a background store context;
  checkpoint resolution was process-local and service-wide; stale run writes
  still depended on a fallible corrective retry; and third-party brokers could
  omit `OnPending`. New deterministic reds pinned each failure. Status writes
  are now serialized per run and snapshot after a context-aware lock;
  notification passes its deadline through status and event persistence;
  checkpoint stores expose atomic pending-only resolution with per-record,
  context-aware service coordination and cross-service waiter observation; and
  the runner observes readable broker pending state as a callback fallback.
  Both callback and fallback paths share exactly-once wait publication.
  Status mutation, persistence, and its lifecycle event share the per-run lock;
  terminal state rejects any delayed nonterminal downgrade, so a notifier
  cannot publish stale waiting state after completion, failure, or cancellation.
- Ninth review follow-up: Pending publication still used once-on-attempt and
  the fallback observer cancelled its context as soon as the tool returned.
  Immediate `UpdateRun` or `AppendEvent` failures could therefore consume the
  only publication attempt, while a quick accepted answer could cancel an
  observer already persisting the wait. The callback and observer now share a
  serialized once-on-success publisher; started observer publication drains to
  success or the question deadline, and transient failures retry. Strict
  durable-before-visible event behavior is limited to this waiting lifecycle;
  ordinary nonterminal events preserve the existing best-effort persistence
  contract. Failed strict appends roll back the final sequence allocation, so
  run SSE IDs remain contiguous and `Last-Event-ID` reconnect returns only
  unseen events. A redaction-policy drop counts as successful suppression and
  cannot cause retries or block an accepted answer. Cross-Service checkpoint
  polling is opportunistic after local waiter registration: a transient poll
  read error is retried instead of unregistering the waiter or masking a later
  local/remote resolution; the caller context remains the termination bound.
## 2026-07-31 (TUI Waiting Conversation Overlay — Issue #1058)

- Review finding: PR #1061 comment 3687057509 showed that the first fix
  correctly opens the overlay but leaves pending GET completion correlated
  only by run ID. A resume can clear state before the GET returns, after which
  the late result reactivates the answered prompt; an older result can also
  overwrite a newer same-run call.
- Expanded TDD contract: hold the production pending GET in flight across a
  resume and across a superseding waiting call. Both regressions must fail
  before implementation, then pass using exact call/generation correlation for
  both success and error messages.
- Review-fix red evidence: the focused command failed with
  `late pending result resurrected overlay after run.resumed` and showed the
  old question replacing the newer one.
- Review fix: every waiting event advances a model-owned generation; its GET
  captures the run, call, and generation. Pending success and error messages
  mutate state only when all captured values still match the active wait, and
  the success response call ID must also match the originating call.
- Review-fix verification: focused AskUser/SSE normal and race passed; the
  complete TUI package passed normal in 37.926s and race in 40.596s; the full
  foreground `./scripts/test-regression.sh` passed normal, complete race, and
  `coveragegate` at 85.6% with zero uncovered functions.
- Symptom: an exact live-shape `run.waiting_for_user` SSE event reaches the TUI
  bridge, but the overlay remains inactive and no pending-input GET occurs.
- Cause: `sseEnvelope` omits top-level `run_id`; `decodeSSE` forwards only the
  payload, while the model waiting handler expects `run_id` inside that payload.
  Existing tests injected that non-production nested shape after decoding.
- Reproduction: the public bridge/model path on `fedcf607` produced
  `raw={"call_id":"call-1"} overlay_active=false`.
- Fix: `sseEnvelope` decodes top-level `run_id` into `SSEEventMsg.RunID`;
  `Raw` stays payload-only, and the waiting handler uses the canonical message
  field. Synthetic model tests now match the production split.
- TDD evidence: the corrected acceptance test failed before the fix with
  `overlay=false input_gets=0 input_posts=0`, then passed in 0.01s after the
  three-line seam repair. It proves the visible prompt/options, exact answer
  POST, resume dismissal, and later assistant continuation.
- Replay evidence: a `Last-Event-ID` bridge request retains the same top-level
  run identity and does not duplicate it into payload bytes.
- Verification: focused AskUser/SSE normal and race pass; complete TUI normal
  and race pass; the full gate passes normal, complete race, and
  `coveragegate: PASS (total=85.6%, min=80.0%, zero-functions=0)`.
- Environment learning: detached tmux made the two real-Keychain tests time out
  at 15 seconds. The required full script was rerun—not waived—in the logged-in
  foreground context, where `internal/modelstore` passed in normal and race
  phases and the entire gate completed green.
- Hosted verification: PR #1061 is mergeable; its first hosted `test-race`
  check passed in 2m11s and `test-fast` passed in 3m14s.

## 2026-07-30 (Workflow Failure-Event Test Timeout — Issue #1049)

- Symptom: the full race gate reached a stored failed workflow state but timed
  out after two seconds before its subscriber consumed `workflow.failed`.
- Cause: the fixture measured shared race-runner scheduling latency with an
  undersized wall-clock deadline.
- Planned fix: retain the live event assertion behind a stopped ten-second
  timer, consistent with the contention class recorded in #958.
- Verification contract: focused normal/race stress, workflows normal/race,
  full regression, and GitHub required checks.
- Result: the focused test passed normal/race at `-count=100`, the complete
  workflows package passed normal/race, and `./scripts/test-regression.sh`
  passed with 85.6% total coverage and zero uncovered functions.

## 2026-07-30 (AskUserQuestion Status-Test Publication Race — Issue #1044)

- Symptom: GitHub Actions `make test-race` reported a concurrent write/read of
  the test-local run ID, then sampled an empty status instead of `running`.
- Cause: `StartRun` dispatches the provider before returning, while the fixture
  assigned its closure-captured ID only after the return.
- Planned fix: publish the returned ID through a capacity-one channel before
  collecting events; completion step two consumes the handoff before sampling.
- Verification contract: focused normal/race stress, harness normal/race, full
  regression, and GitHub required checks.
- Result: the focused test passed normal/race at `-count=100`, the complete
  harness package passed normal/race, and `./scripts/test-regression.sh` passed
  with 85.6% total coverage and zero uncovered functions.

## 2026-07-30 (Swarm Activation Control Lifecycle Race — Issue #1046)

- Symptom: hosted fast CI intermittently reported `agent_swarm` missing from an
  unrestricted run immediately after test-local activation.
- Cause: the control reused an exhausted scripted provider, so terminal cleanup
  could clear the activation before the fixture inspected definitions.
- Planned fix: use a dedicated provider that blocks until after the definition
  assertion, then release it and wait for normal terminal cleanup.
- Verification contract: focused normal/race stress, harness normal/race, full
  regression, and GitHub required checks.
- Result: the focused test passed normal/race at `-count=100`, the complete
  harness package passed normal/race, and `./scripts/test-regression.sh` passed
  with 85.6% total coverage and zero uncovered functions.
## 2026-07-30 (Worktree Containment CI Synchronization — Issue #1039)

- Symptom: GitHub Actions fast run 30551198514 received
  `tool.call.completed` but found neither `out.txt` nor `marker.txt` in the
  provisioned worktree.
- Cause: the test consumes a buffered event while the runner immediately starts
  its terminal provider turn and can remove the worktree before the subscriber
  performs filesystem assertions.
- Fix: hold only the test provider's terminal turn until containment
  assertions finish, then release normal workspace cleanup.
- TDD evidence: a deterministic pre-subscription wait for terminal cleanup made
  the existing assertion fail with both files missing. The final provider
  handshake retains the same real bash command and exact filesystem checks.
- Verification: focused normal and race tests pass 100 consecutive runs each;
  the full harness package passes in normal and race modes; and
  `./scripts/test-regression.sh` passes normal, full race, and `coveragegate`
  at 85.6% with zero uncovered functions.
## 2026-07-30 (Default Workspace Registry Test Isolation — Issue #1042)

- Symptom: `TestDefaultRegistry_Functions` fails on its second in-process
  invocation with `workspace: implementation already registered`.
- Cause: every invocation registers the fixed
  `test-default-impl-unique-12345` name in the intentionally persistent package
  registry.
- Planned fix: assign each invocation a process-local atomic suffix while
  retaining the same-name duplicate assertion inside that invocation.
- Verification contract: focused normal/race `-count=100`, workspace
  normal/race, and full repository normal/race/coverage gate.
- Result: the focused normal/race tests passed at `-count=100`, the complete
  workspace package passed normal/race at `-count=5`, and
  `./scripts/test-regression.sh` passed with 85.6% total coverage and zero
  uncovered functions.

## 2026-07-30 (Subscriber-Pinned Retention Quota — Issue #1048)

- Symptom: hosted race CI could not subscribe to a just-completed extra run
  because pruning had already removed it.
- Cause: a persisted terminal run with an active subscriber consumed the
  retention quota despite being ineligible for deletion.
- Planned fix: compute pruning pressure only from persisted, terminal,
  zero-subscriber candidates; pinned states remain protected exceptions until
  cancellation re-runs pruning.
- Verification contract: focused/adjacent normal and race stress, harness
  normal/race, full regression, and GitHub required checks.
- Result: focused normal/race passed at `-count=100`, adjacent pruning
  normal/race passed at `-count=20`, the complete harness package passed
  normal/race, and the final full regression passed with 85.6% coverage and
  zero uncovered functions.

## 2026-07-30 (Workflow Subscription Cancellation Test — Issue #1035)

- Symptom: the full race gate failed in
  `TestEngineDefinitionSubscribeAndFailure` because its first receive after
  cancellation returned `ok == true`.
- Cause: the subscription channel is buffered. Cancellation synchronously
  removes and closes it under the same mutex as event fanout, but Go drains
  values accepted before close before a receive reports `ok == false`.
- Fix: the test now deterministically enqueues a pre-cancel event, drains
  accepted values, and retains a one-second assertion that the channel
  eventually closes.
- TDD evidence: with the buffered fixture and old single-receive assertion,
  the focused race test failed at `store_coverage_test.go:43`; the drain loop
  keeps that fixture and will fail if closure never arrives.
- Verification: the focused race test passes 100 consecutive runs; workflow
  package normal/race tests pass; `./scripts/test-regression.sh` passes normal,
  full race, and `coveragegate` at 85.6% with zero uncovered functions.

## 2026-07-30 (Terminal Reconciliation State — Issue #1028)

- Symptom: after a conversation replay delivered `run.failed` or
  `run.cancelled`, the macOS client fetched durable messages and showed the run
  as completed. Failure text carried only by the event stream also disappeared.
- Cause: `RunSession.reconcilePersistedMessages` called `Transcript.load`,
  whose historical-open contract resets all state and marks the snapshot
  completed. Reconciliation reused that row-loading behavior without preserving
  the authoritative terminal event state.
- Fix: `Transcript.reconcile` now rebuilds persisted message/tool rows while
  retaining failed/cancelled state and unique event-derived failure rows.
  Historical `load` and normal completed reconciliation keep their existing
  behavior.
- TDD evidence: `failedReplayReconciliationPreservesFailureState` first ended
  at `.completed` and lost `deployment probe failed`; the cancelled variant
  likewise ended completed. Both pass after the repair, along with completed
  replay deduplication and the adjacent transcript reducer suite.
- Verification: strict Swift formatting, the focused 22-test transcript /
  conversation-stream slice, and the complete Swift package (178 tests in 40
  suites) pass. `./scripts/test-regression.sh` also passes its normal and full
  race suites plus `coveragegate` at 85.6% with zero uncovered functions.

## 2026-07-30 (Anytime Contextual Feedback Intake — Issue #1023)

- Symptom: `/feedback` could only produce a small local archive containing
  config/runtime data and recent rollouts. The user could not state what should
  be fixed, preserve the active TUI transcript/session, include a screenshot,
  or carry the evidence into a structured GitHub issue flow.
- Cause: the original command ignored its arguments and the builder owned no
  snapshot of `tui.Model`; there was also no supported attachment handoff.
- Fix:
  - `/feedback [--issue] [--screenshot <path>] [--] [request]` now snapshots
    the active run, conversation, last event, workspace, selected model, and a
    copied transcript without mutating or interrupting the run;
  - the canonical archive adds redacted request/context/transcript members,
    bounded redacted service-log and rollout tails, absence markers, and an
    explicitly selected validated PNG/JPEG with checksum/provenance;
  - every capture writes a recoverable sanitized issue Markdown file, while
    explicit `--issue` asynchronously opens the supported
    `gh issue create --web` flow against `dennisonbertram/go-code` and
    preserves the local path on failure;
  - screenshot pixels are intentionally not redacted and the TUI/browser draft
    directs the user to review and attach the image and zip before submission.
- TDD evidence: the new acceptance slice first failed on the absent options,
  context fields, screenshot validation, and GitHub draft seam. Focused tests
  then passed for active-run preservation, canary redaction, malformed,
  symlinked, and oversized images, transcript truncation provenance, and
  recoverable partial-success behavior. A fake `gh` executable also proves the
  asynchronous Bubble Tea command wrapper, canonical repository arguments, and
  success status without opening a browser or creating an external issue.
- Verification:
  - the full TUI and harnesscli normal suites pass;
  - `go test -race ./cmd/harnesscli/... -count=1` passes;
  - `./scripts/test-regression.sh` passes in the logged-in launchd context,
    including the complete race suite and
    `coveragegate: PASS (total=85.6%, min=80.0%, zero-functions=0)`;
  - a rebuilt harnesscli was exercised through a real tmux TUI. The resulting
    archive contained all nine expected members, preserved the exact request,
    bundled a 2.46 MB PNG, and recorded its media type, byte size, raw-pixel
    warning, and SHA-256 checksum beside the recoverable issue Markdown.

## 2026-07-30 (Durable Conversation Event Replay — Issue #1008)

- Symptom: callback and cron continuations were durable in
  `GET /v1/conversations/{id}/messages`, but a macOS client that left Chat
  while the scheduled run completed could miss that assistant turn. Returning
  to Chat did not reconcile the transcript, and a later live event could make
  the apparently missing history reappear.
- Cause: `Runner.SubscribeConversation` replayed only the current live run,
  while the conversation endpoint parsed `<run-id>:<seq>` as a run-local
  integer and discarded the run identity. The GUI also retained its in-memory
  transcript across Activity/Chat navigation without fetching durable
  messages.
- Fix:
  - the existing run stores now query conversation events in global append
    order with tenant isolation and exact opaque event-ID resume;
  - the runner keeps a bounded no-store journal and serializes event
    persistence/fanout with replay-to-live subscription handoff;
  - the conversation SSE endpoint pages bounded replay, explicitly marks stale
    cursors with `X-Harness-Conversation-Resync: required`, and reconnects
    before attaching to live delivery when more history remains;
  - Chat appearance reconciles the persisted transcript when no user-started
    run is active, and terminal replay reconciles again so opening an already
    persisted conversation cannot double-render its historical replies.
- TDD evidence: completed-run replay, cross-run exact resume, SQLite restart,
  tenant isolation, bounded paging, stale cursor, persist-before-fanout,
  Activity-to-Chat reconciliation, and persisted-snapshot replay deduplication
  tests failed against the old behavior and pass with the repair.
- Verification:
  `go test -race ./internal/harness ./internal/server -run
  'TestSubscribeConversationReplays|TestEventJournalDispatch_|TestConversationEvents_'
  -count=1`; `swift test --package-path macapp --filter Conversation`;
  `./scripts/test-regression.sh` (normal and full race suites, 85.7% coverage,
  zero uncovered production functions); and the complete
  `swift test --package-path macapp` suite (176 tests, 40 suites) all pass.

## 2026-07-30 (Embedded Cron Jitter Wiring — Issue #1022)

- Symptom: a live embedded harness job created with
  `HARNESS_CRON_JITTER_ENABLED=false` passed its advertised `next_run_at`
  without firing; `last_run_at` stayed zero and the target conversation did
  not advance.
- Cause: config loading correctly resolved the cron jitter fields, but
  `buildCronBootstrap` discarded `harnessCfg.Cron` and constructed
  `cron.SchedulerConfig` with only `MaxConcurrent`, which caused
  `NewScheduler` to reinstall its 60–300 second defaults.
- Fix: the daemon composition root now maps the resolved `config.CronConfig`
  into the existing `cron.JitterConfig` and passes it to the embedded
  scheduler. The avoid-minute slice is copied at the boundary so later config
  mutation cannot alias live scheduler state.
- TDD evidence: `TestCronSchedulerConfigFromResolvedConfig` first failed to
  compile because the mapping seam did not exist, then passed with exact
  enabled/bounds/avoid/log assertions and a copy-semantics check.
- Focused verification: `go test` and `go test -race` pass together for
  `./cmd/harnessd`, `./internal/config`, and `./internal/cron`.
- Full verification: `./scripts/test-regression.sh` passes normal tests, the
  complete race suite, and `coveragegate: PASS (total=85.6%, min=80.0%,
  zero-functions=0)`.

## 2026-07-30 (Embedded Cron Scope Handoff — Issue #1001)

- Review repairs close the standalone-server gap: scoped create requests now
  copy tenant, conversation, and agent into the stored job, with client-wire
  and HTTP round-trip regressions protecting the additive contract.
- A deterministic composed acceptance test now proves
  `SQLite -> Scheduler -> DispatchExecutor -> HarnessExecutor ->
  cronRunStarter -> Runner`. The scheduler's narrow in-process `TriggerJob`
  method reloads and rejects inactive jobs before using the normal asynchronous
  fire path; no remote manual-trigger endpoint is added.
- Behavior tests cover the seven functions that the repository gate previously
  reported as untouched: background-job conversation delivery, model-store
  pricing/path/provider ordering, and catalog-rate fallback.
- Embedded cron jobs now persist tenant, conversation, and agent scope in
  additive SQLite columns. Existing rows migrate to empty values without
  rewriting or deleting data; legacy harness config still supplies its older
  `conversation_id` fallback when the new stored field is absent.
- The scheduler keeps the existing executor contract for shell jobs while
  passing execution IDs through an optional execution-aware seam. Harness jobs
  cross a typed `RunStartRequest` containing prompt, stored scope, job ID, and
  execution ID; the harnessd adapter maps only that request into `RunRequest`.
- The deferred `cron_create` tool derives all ownership fields from run
  metadata and does not expose them as model arguments. Lifecycle logging names
  the job and execution IDs without logging prompt contents or credentials.
- TDD coverage includes the red positional-handoff seam, SQLite scope
  round-trip and legacy migration, scheduler execution-ID propagation,
  stored-scope override rejection, same-conversation tenant isolation, the
  harnessd adapter, and model-facing cron scope stamping.
- Verification: focused affected-package tests pass; the full normal and race
  suites pass; `./scripts/test-regression.sh` ends with
  `coveragegate: PASS (total=85.6%, min=80.0%, zero-functions=0)`.
- macOS verification learning: both real-Keychain tests passed directly, but
  `security(1)` waited on the controlling terminal when the full suite ran
  inside tmux and hit its 15-second timeout. Running the suite in the logged-in
  launchd context while monitoring it from tmux preserved long-run
  observability and let the exact same Keychain tests complete.
- The first verified-merge attempt stopped before changing `main` when
  `TestWorkerPool_RunQueuedEventEmitted` lost its one-shot provider-entry
  signal: the helper performed a non-blocking send on an unbuffered channel
  before the receiver was guaranteed to be listening. Buffering that test-only
  signal preserves it across the scheduling race; the focused test passed 50
  consecutive runs before the merge gate was retried.
- Updating to the latest `main` at the merge boundary exposed three test-only
  integration conflicts that Git could not detect textually: older main-only
  cron fixtures still implemented the positional `RunStarter` signature, and
  both sides independently added a `TestProviderNamesAreSorted` function.
  Those fixtures now exercise the typed request and its full ownership scope;
  the redundant model-store test was removed. The affected daemon, cron, and
  model-store packages pass together after the repair.

## 2026-07-29 (Issue #987 — Issue-Driven Engineering Contract)

- Symptom: repository issue templates were permissive Markdown and PRs had no
  issue/evidence template, so agents could start coding without proving impact
  analysis, issue linkage, or scope discipline.
- Cause: planning discipline existed in scattered runbooks but was not made
  concrete at issue creation or repeated in the Claude-specific instructions.
- Fix:
  - replaced the generic templates with required feature/change, bug, epic,
    research, and minor documentation-only Issue Forms;
  - added an exhaustive PR evidence template and generalized the impact-map
    contract across callers, data flow, config/API/CLI, persistence, lifecycle,
    security, clients, deployment, compatibility, tests, and docs;
  - made issue-first scope/impact/TDD rules explicit in `AGENTS.md`,
    `CLAUDE.md`, plans, and contributor runbooks;
  - added a repository drift test that parses all five forms and pins their
    required fields, the PR contract, private-security route, and agent policy.
- Rollout decision: per user direction this is a process-guided pilot. No
  GitHub branch protection, required status check, or PR validator is added yet.
  Repeated agent noncompliance is the evidence threshold for proposing that
  harder gate as a separate issue.
- TDD evidence:
  - repository-contract tests then failed on the absent forms, PR
    template, security route, and agent policies before implementation;
  - repo-wide `actionlint` exposed Issue #988: the Terminal-Bench report marker
    used regex grep for literal Markdown. `grep -Fq` preserves the intended
    match and clears ShellCheck SC2063;
  - the first full regression reached 85.5% aggregate coverage but exposed
    Issue #989: nine production functions across cron dispatch, background-job
    delivery, model-store persistence, catalog pricing, and daemon run startup
    had no executed test path. Behavioral package tests now cover those seams
    without changing production logic or weakening the gate;
  - two detached tmux attempts killed the real Keychain-backed model-store
    self-tests at their 15-second timeout. Running the same script in the
    ordinary foreground execution context completed those tests normally,
    separating the execution-context artifact from a product failure;
  - `actionlint .github/workflows/*.yml`,
    `go test ./internal/quality/repostructure -count=1`,
    `go test ./internal/... ./cmd/... -count=1`, `make test-e2e`, and
    `python3 scripts/test_terminal_bench_artifacts.py` pass;
  - `./scripts/test-regression.sh` passes all normal, race, and coverage
    phases at 85.6% aggregate coverage with zero uncovered functions.

## 2026-07-28 (macOS Codex Visual Gauntlet — Round 6)

- The selected rail now uses semantic neutral selection tokens rather than the
  system-blue accent: dark selection is `#333333` with white label/icon.
  `Typography` now defines the shared 22/20/18/16.5/14/12pt hierarchy, and
  the transcript explicitly inherits the primary `#FFFFFF` foreground rung.
  The 16.5pt body role retains the pre-existing 21.5pt nominal line height so
  `Layout.userMessageMinimumHeight` remains 45.5pt.
- User prompts now use a dedicated content-hugging layout with the
  design-system 374.5pt cap, instead of accepting the entire transcript
  column. The rail background alone ignores the top safe area, preserving
  control placement while eliminating the differently coloured toolbar band.
- Regression coverage pins selected-row RGB neutrality, the body size and
  derived prompt-row height, the width cap, and production use of the semantic
  tokens. Swift formatting and strict lint completed; build/test could not
  enter the sandbox-owned Xcode module cache.

## 2026-07-21 (Provider Image Encoding — Epic #818 Slice 4)

- Both provider clients now translate `harness` image blocks into their
  native wire shapes: Anthropic emits `{"type":"image","source":{"type":
  "base64","media_type","data"}}` block-array content for user messages
  (image-only messages grow no empty text block); OpenAI emits chat-
  completions `image_url` parts and responses-API `input_image` parts, both
  as `data:<media_type>;base64,<data>` URLs. Text-only messages serialize
  byte-identically to before (regression-tested in both clients).
- Defense in depth: new shared sentinel `provider.ErrImageModalityUnsupported`
  is returned before any HTTP request when an image block targets a
  catalog-known text-only model — Anthropic checks its existing catalog
  field; OpenAI checks the new optional `Config.ModelModalityLookup`
  (mirrors `ModelAPILookup`), wired in `cmd/harnessd` via
  `lookupModelModalities` (alias-resolving, nil-safe). Unknown
  models/modalities skip the check.
- Acceptance proofs: runner → provider wire tests for both clients
  (`TestRunnerImageBlockReachesAnthropicWire`,
  `TestRunnerImageBlockReachesOpenAIWire`) capture the real HTTP body of a
  `StartRun` with an attachment.

## 2026-07-21 (ACP Server Mode — Epic #806, Slice 4)

- Permission bridge: `tool.approval_required` SSE events now issue `session/request_permission` to the editor (options `allow-once`/`allow_once`, `reject-once`/`reject_once`; `toolCall` carries `toolCallId`, `title`, parsed `rawInput`). Selected allow -> `POST /v1/runs/{id}/approve`; reject / `cancelled` outcome / client JSON-RPC error -> `/deny` (fail closed).
- Server grew editor-bound calls: `callClient` registers a pending call by id, and `dispatch` routes response-shaped messages (result/error, no method) to the waiter; unknown or already-completed ids stay logged-and-ignored.
- Deadline discipline: the permission call runs on a `deadline_at`-bounded context; when it passes, the pending call is deregistered and nothing is POSTed (harnessd auto-denies at the deadline). Bridge goroutines ride a turn-scoped context and are awaited before the prompt response, so no bridge outlives its turn.
- 501 no-broker: `ApproveRun`/`DenyRun` map harnessd's 501 to `ErrApprovalNotConfigured`, surfaced as an `agent_message_chunk` note instead of a hang until the deadline.
- Validation: strict TDD (red: `undefined: permissionParams`, `srv.callClient`). Flows via scripted io.Pipe client + fake harnessd (new `/approve`/`/deny` routes with 501 mode): grant -> approve + `end_turn`; reject -> deny; `cancelled` -> deny; client error -> deny; deadline expiry -> no POST and a late answer ignored; 501 -> note, no hang.

## 2026-07-19 (Plugin Markdown Command Files with $ARGUMENTS — Epic #821 Slice 4)

- Bundle `commands/*.md` files now register as TUI slash commands: YAML frontmatter (`name`, `description`; unknown fields rejected, body required) plus a prompt-template body. The expanded body is SUBMITTED as a prompt (transcript + message bubble + `startRunCmd`), unlike legacy JSON `prompt` plugins, which display their output — the acceptance is `/greet hello world` POSTing the expansion to `/v1/runs`, covered by a model-level test driving `inputarea.CommandSubmittedMsg`.
- Expansion reuses epic #813's merged engine: `internal/skills` now exports `BuildTemplateVars` + `ExpandTemplate` + `HasArgPlaceholder`, with the skill invocation path (`buildVars`/`expandBody`/`hasArgPlaceholder`) delegating unchanged. `$ARGUMENTS` is the raw typed args (quotes preserved via `rawCommandArgs` from `cmd.Raw`), `$0..$n` use `SplitArgs`, `$WORKSPACE`/`$SKILL_DIR` resolve, and the `ARGUMENTS: <args>` fallback applies when the body references no placeholder.
- Collision rule: plain name if free, else `<bundle>:<name>`, else skip — namespacings and skips surface through the plugin warnings slice. JSON `PluginDef` files in bundle `commands/` dirs keep loading through the unchanged legacy path; untrusted/disabled bundles contribute nothing (TrustedBundles gating, pinned by `installablePluginCommandSources` tests).
- TDD: failing-first tests cover the exported expansion contract, frontmatter parse/validation matrix, loader file handling, registration + namespacing + double-collision skip, trust gating, and the end-to-end submission.

## 2026-07-21 (Agent Swarm — Epic #808, Slice 4)

- Added the TUI live swarm progress panel (`cmd/harnesscli/tui/swarm_panel.go`):
  the model's `agent_swarm` tool call is tracked from SSE events
  (`tool.call.started` parses the item list, `tool.call.completed` parses the
  aggregated report), and a grouped panel renders in the viewport with a
  summary line (launched/completed counts, cap 128) plus per-member
  pending/running/completed/failed rows with the item label.
- Poll loop: while a swarm is active, `/v1/subagents` is re-fetched every 1s
  (`SwarmPollTickMsg`); poll responses refresh the panel block in place
  (keyed line-range replacement) and stop on completion or when every member
  is terminal. No server changes: `RemoteSubagent` only gained the
  already-sent `created_at` field for creation-window member matching.
- Before the report arrives, members are matched to items by creation window
  in schedule order (resumed entries render as `resumed`); the aggregated
  report at completion replaces the heuristic with exact member IDs/statuses.
  Single-run tracking is sound because agent_swarm is sole-call and blocks
  the parent run.
- `/subagents` listing groups the swarm section first via an extended
  `formatSubagentsLines(items, panel)`; no-swarm output is unchanged.
- TDD: internal rendering/matching tests (multi-status rows, creation window,
  resume entries, exact report members, grouping, no-swarm regression) and an
  external SSE-flow test (panel appears, updates in place on poll, freezes
  with exact statuses, stale poll ignored, tick starts/stops) landed first
  and failed on undefined symbols before implementation.
- Validation: `go test ./cmd/harnesscli/... -count=1` (29 packages) and
  `-race` green; full regression suite (see PR body).

## 2026-07-21 (`/undo` Picker Overlay — Epic #805, Slice 4)

- Bare `/undo` now opens a prompt picker matching kimi-code behavior: new `undopicker` component (`cmd/harnesscli/tui/components/undopicker/`, modeled on `sessionpicker`) lists the last 10 non-meta user prompts newest-first with their relative position (`Count`, 1 = newest); Enter confirms, Esc cancels, navigation wraps and skips disabled rows.
- Data path: `fetchUndoCandidatesCmd` GETs `{id}/messages` decoding `is_meta`/`is_compact_summary`; the pure `EntriesFromMessages` function filters prompts, assigns counts, and marks entries at/below the most recent compaction summary `Disabled` (the store refuses those undos — Slice 1 — so the picker never offers them; disabled rows render dimmed with a `(compaction boundary — cannot undo)` hint).
- Selection flow: `UndoPickerSelectedMsg{Count}` closes the overlay and dispatches the Slice 3 `undoConversationCmd` — the numeric `/undo [n]` path is unchanged.
- Key-routing note: overlay routing cases never see Enter (the `Submit` case claims it first) — profiles/theme handle Enter via explicit arms in the `Submit` case, and the undo picker follows that pattern (`wrapUndoPickerCmd` shared by the arm and the routing case). Discovered a pre-existing gap: the sessions overlay has no such arm, so Enter there is swallowed by the input area — filed as issue #917 (not fixed here; out of slice scope).
- Validation: failing-first component tests (`model_test.go` — navigation/disabled/Enter/Esc/`EntriesFromMessages` rules; `view_test.go` — order/hints/empty) and model-level flow tests (`undo_command_test.go` — bare `/undo` opens the picker with no POST, Down+Enter issues `{"count":2}`, Esc cancels without HTTP, fetch error stays in the status bar, disabled row unselectable end to end); Slice 3's `TestExecuteUndoCommand_DefaultCount` rewritten as `TestExecuteUndoCommand_BareOpensPicker` (behavior change is deliberate per epic). tmux smoke vs `harnessd`: bare `/undo` shows the picker; Down+Enter on the 2nd-newest trims the viewport to prompt 1 + its answer; on a conversation with a mid-history summary the older prompt renders `(compaction boundary — cannot undo)` and navigation skips it; Esc closes cleanly.

## 2026-07-21 (Mid-Turn Steering — Epic #820, Slice 4)

- TUI-originated steers now echo immediately on send and dedupe against the server-confirmed `steering.received` event; failed steers leave no orphan entry.
- Design (chosen after viewport exploration — `ReplaceTailLines`/`ReplaceLineRange` are the only primitives and assistant streaming re-renders the tail, so non-tail position tracking was rejected as too invasive): the viewport bubble renders in final slice-2 form at keypress (`steered ⟂ msg`), so confirmation never needs an in-place re-render; pending state lives in the transcript entry (`… (pending)`) and a `pendingSteers` set (`{message, transcriptIdx}`).
- `cmd/harnesscli/tui/model.go`: `appendSteeringEcho` (send-time echo + registration), `confirmPendingSteer` (dedupe: strip `(pending)`, consume record, no duplicate; unmatched → slice-2 external marker), `failPendingSteer` (drop record, delete transcript entry, tail-remove the bubble only via `steerEchoTail`, which any later `SSEEventMsg` invalidates), `clearPendingSteers` wired into the three transcript-reset sites (`resetTranscriptView`, new session, session switch).
- `messages.go`/`api.go`: `SteerErrorMsg` gained `Prompt` (populated at all six `steerRunCmd` error sites) so failures match their pending echo exactly.
- Strict TDD: `steer_echo_test.go` — immediate echo before any SSE event, confirmation dedupes to exactly one marker/entry (no longer pending), external steer appends a second marker, consumed dedupe treats a second identical payload as external, and 409/429 end-to-end (cmd driven through Update) removes echo + entry with the failure status. Two tests failed pre-implementation; the others pin invariants that held throughout.
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok; acceptance `go test ./cmd/harnesscli/tui/ -run Steer` ok; `go test ./internal/server/ ./internal/harness/ -count=1` ok; gofmt/vet clean.

## 2026-07-21 (Shell Mode Slice 4 — Epic #811)

- Ctrl+B backgrounds a running shell-mode command: the pump stops emitting
  live deltas (`detach()` on the executor) but keeps buffering output; the
  card collapses in place to a one-line `shell(<command> — backgrounded
  (ctrl+b))` note; `shellRunningID` clears so Esc/Ctrl-C no longer target it.
- Poll-chain safety: the output handler always re-issues the executor poll
  (even for deltas that raced the detach), so the done message is never
  orphaned; at done, the note is replaced in place by the completion card
  (exit code + bounded output tail) via the existing handleToolResult/Error
  pipeline — exactly one notice. Background completions feed the slice-3
  context block like foreground ones.
- Ctrl+B with nothing running is an explicit no-op. New test seam:
  `ShellExecCount()`. No shared-component changes; no `/tasks` panel (separate
  epic).
- Validation: `go test ./cmd/harnesscli/... -count=1` green (29 packages);
  gofmt/vet clean on touched files.

## 2026-07-21 (Unified /tasks Panel — Epic #814, Slice 4)

- The `/tasks` overlay now supports row actions. `o`/Enter views output: bash jobs via the new `GET /v1/jobs/{id}/output` (backed by `JobTracker.Output`, runs:read, tenant-checked), subagents via the existing detail endpoint, and cron/callback rows via a static detail built from the row (no endpoint exists for those). `x`/Ctrl+K stops the selected task through the type-appropriate path: bash job kill (`/v1/jobs/{id}/kill`), subagent cancel, cron DELETE, and the new `POST /v1/callbacks/{id}/cancel` (new `CallbackCanceler` server option, wired from the daemon's `CallbackManager`; runs:write, tenant-checked).
- Deleting a cron schedule is destructive, so `x` on a cron row opens a confirmation prompt (`taskspanel` confirm mode); DELETE only fires after `y`. Esc and `n` cancel the prompt.
- The panel gained detail (scrollable output) and confirm sub-modes plus a transient notice line for action errors; Esc inside a sub-mode backs out to the list instead of closing the overlay (the global Esc chain now consults `tasksPanel.Mode()` first). Enter is handled in the global Enter block via a tasks guard delegating to the shared `openSelectedTaskOutput` helper. A successful stop triggers a list refresh.
- Validation: failing-first tests across all layers — `job_tracker_test.go` Output, `http_tasks_test.go` (7 new endpoint tests), `taskspanel` mode/render tests (12 new), `api_tasks_test.go` (5 new incl. per-type dispatch table), `tasks_overlay_814_test.go` (10 new model tests covering every acceptance flow). `go test ./cmd/harnesscli/... -count=1` green; gofmt/vet clean.

## 2026-07-21 (ACP Server Mode — Epic #806, Slice 3)

- Prompt turns now stream live output to the editor: `assistant.message.delta` -> `agent_message_chunk`, `assistant.thinking.delta` -> `agent_thought_chunk` (payload field `content`), `tool.call.started` -> `tool_call` (`status: in_progress`, `title` = tool name, `kind` via a tool-name table), `tool.call.completed` -> `tool_call_update` (`completed`, or `failed` when the payload carries `error`; output included as content). `toolCallId` is the harness `call_id`, stable across start/complete.
- `RunsClient.WaitTerminal` generalized to `WatchRun(ctx, runID, onEvent)`; oversized SSE lines (cap now a test-shrinkable var) are drained and their event skipped with a logged warning instead of corrupting the stream.
- Backpressure discipline: one bounded (256) queue per turn with a single writer goroutine, so the SSE reader never blocks on a slow editor. Coalescing/drops trigger only on a FULL queue — same-kind deltas merge into the tail, other deltas drop (counted + logged), lifecycle updates evict buffered deltas but are never dropped. (First cut coalesced whenever the writer lagged, which made healthy streams lose chunk granularity and broke the golden ordering test; coalescing is now strictly an anti-overflow mechanism.)
- The prompt handler closes the queue at the terminal event and drains it fully before writing the `session/prompt` response, per the spec's updates-before-result rule.
- Validation: strict TDD (red: `undefined: runEvent`/`translateRunEvent`). Golden acceptance: scripted client observes two message chunks, a thought chunk, `tool_call`, `tool_call_update` (stable `toolCallId`) in exact order before the `end_turn` result. `go test ./internal/acp/... -count=1` green.

## 2026-07-21 (Agent Swarm — Epic #808, Slice 3)

- Added the deferred `agent_swarm` tool
  (`internal/harness/tools/deferred/agent_swarm.go`): `TierDeferred`,
  `ActionExecute`, `Mutating: true`, params `prompt_template`/`items`/
  `resume_agent_ids` plus profile/model overrides resolved exactly like
  `start_subagent`; returns the aggregated report via `MarshalToolResult`.
- Import-cycle design: `harness` and `deferred` cannot import `subagents`
  (subagents imports harness). Mirror types (`SwarmRequest`/`SwarmReport`) +
  `SwarmRunner` interface + `AgentSwarmToolName` const live in
  `internal/harness/tools` (same pattern as `SubagentManager`); the adapter
  `subagents.NewToolSwarmRunner` maps both directions; wiring happens in
  `cmd/harnessd/runtime_container.go` next to the InlineManager.
- Sole-call rule in `runner_step_engine.go`: a response containing
  `agent_swarm` plus any other call executes the first swarm call and rejects
  every other call with a corrective error naming the rule (a second
  `agent_swarm` call is rejected too).
- Nested-swarm prevention: new per-run `DeniedTools` denylist plumbed
  `tools.SubagentRequest` -> `subagents.Request` -> `harness.RunRequest` ->
  runState (carried over on continuation). `filteredToolsForRun` never offers
  denied tools (even when activated) and the step-engine call gate blocks
  them outright. The swarm sets `DeniedTools=[agent_swarm]` on every member.
- Approval flow: no new code needed — the existing destructive-policy path
  consults `Registry.IsMutating`, and the tool declares `Mutating: true`;
  a test proves the call pauses for approval and runs after approval.
- Description file `descriptions/agent_swarm.md` documents the sole-call
  rule, the 128 cap, the 5->+1/700ms ramp, the env cap, and resume semantics.
- TDD: tests landed first across four files (deferred tool contract, adapter
  mapping, runner sole-call/denied/approval gates, fakeprovider full-stack
  e2e showing a 4-item swarm and one aggregated tool result); all failed on
  undefined symbols before implementation.
- Validation: package tests + `-race` green for subagents, harness, tools,
  deferred, harnessd; full regression suite (see PR body).

## 2026-07-20 (Image Attachments Through Run Plumbing — Epic #818 Slice 3)

- Added `harness.ContentBlock{Type, MediaType, Data}` (base64), additive
  `Message.Blocks` and `RunRequest.Attachments` (text-only messages and
  callers unchanged; `Message.Clone` deep-copies Blocks).
- `Runner.StartRun` validates attachments (type `image`, media type
  `image/png`|`image/jpeg`, non-empty valid base64) and enforces the
  server-side modality gate: the effective model+provider is resolved via
  the provider registry's catalog and known text-only models are rejected
  with an actionable error; unknown models/modalities (nil registry,
  discovered models) are allowed. HTTP callers get a synchronous 400
  `invalid_request` through the existing `handlePostRun` error path.
- `runner.execute` builds the user message with the prompt text plus Blocks;
  blocks reach the provider `CompletionRequest` (asserted via
  `fakeprovider.LastRequest()`). Snapshot/history stays text-only
  (documented limitation: blocks do not persist across continuation runs).
- TUI send path: pending chips are base64-encoded into the `POST /v1/runs`
  body on submit and consumed (state + temp dirs); an encode failure aborts
  the submit, restores the text, and keeps the chips. `startRunCmd` now
  surfaces the server's error body so the 400 modality message reaches the
  status bar.
- Verified against a tmux `harnessd` on the real catalog: image-capable
  `gpt-4.1` POST → 202; text-only `claude-sonnet-4-6` → 400 naming model +
  provider; malformed base64 → 400 naming the attachment index.

## 2026-07-21 (`/undo` TUI Command — Epic #805, Slice 3)

- Added `/undo [n]` to the TUI (`cmd/harnesscli/tui`): registry entry next to `/clear` (`cmd_parser.go`), executor `executeUndoCommand` (`model.go`), API call `undoConversationCmd` (`api.go`), and result type `UndoResultMsg` (`messages.go`). Help dialog and slash completion pick the command up automatically via `buildHelpDialog` over the registry — no static lists.
- Behavior contract: bare `/undo` removes the last prompt, `/undo 3` the last three. Malformed counts (`abc`, `0`, negatives) and extra args are command errors — a usage status line, zero HTTP. `/undo` refuses with no conversation and while a run is active (an in-flight run's terminal persistence would rewrite the store and silently clobber the undo).
- Viewport refresh: on success the command POSTs `{"count": n}` to the Slice 2 route, then GETs the trimmed history in the same `tea.Cmd`; the `UndoResultMsg` case clears the view (`resetTranscriptView`, extracted from `/clear`) and re-renders (`appendConversationMessages`, extracted from `ConversationHistoryMsg`) so the removed tail bubbles disappear immediately. The `is_meta` undo-boundary marker is never rendered (only `user`/non-empty `assistant` roles render, matching resume).
- On 409 the compaction-boundary explanation renders inline in the viewport (`✗ Undo refused: …` plus a one-line hint) and the existing view is left intact; other failures land in the status bar without touching viewport or transcript.
- Bug found by the tmux smoke (issue #895): the undo truncated the store but `GET {id}/messages` kept serving the runner's stale in-memory conversation mirror, so the TUI refetch rebuilt the viewport with the removed messages. Fix: new `Runner.DropConversationCache` evicts the mirror entry (ownership records kept for cross-tenant validation; safe for active runs since run state lives in `r.runs`), called by `handleUndoConversation` after a successful `UndoPrompts`. Regression tests: `TestRunner_DropConversationCacheFallsBackToStore` (harness) and `TestUndoConversationEndpoint_RefreshesInMemoryMirror` (server, two real runs then undo then GET).
- Validation: failing-first tests in `undo_command_test.go` (13 tests: API success/conflict/error/network, executor default/numeric/parse-error/no-conversation/run-active paths, model-level viewport rebuild, conflict inline render, registry dispatch); `TestTUI364_RegistryCompleteness` allowlist and `harnessAuthCases()` auth-header table updated for the new surface. `go test ./cmd/harnesscli/tui/ -run Undo -count=1` green. tmux smoke vs `harnessd` (fake provider + conversation DB): 3 prompts, `/undo 2` → viewport shows only the first prompt and its response; `GET {id}/messages` serves `user, assistant, is_meta marker` ("removed 2 prompt(s)").

## 2026-07-21 (Unified /tasks Panel — Epic #814, Slice 3)

- `/tasks` opens a TUI overlay listing the unified background-work union from `GET /v1/tasks` with TYPE / STATUS / AGE / COMMAND columns.
- New `components/taskspanel` (value-semantics Model like `helpdialog`): cursor row navigation (↑/↓/j/k) with a bounded fixed-point window that keeps the selected row visible while reserving ▲/▼ overflow indicator slots; distinct empty ("No background tasks."), loading ("Loading tasks…"), and error ("Failed to load tasks: …") states; `FormatAge` renders ages as `5s`/`2m5s`/`1h3m`.
- `api.go` gained `RemoteTask` + `loadTasksCmd` (mirrors `loadSubagentsCmd`); `TasksLoadedMsg`/`TasksLoadFailedMsg` populate the panel or surface a status message. `executeTasksCommand` opens the overlay and kicks the fetch; `r` re-fetches; Esc/OverlayCloseMsg close. `/tasks` is registered in `cmd_parser.go`, which automatically feeds `/help`, slash-complete, and tab completion (all registry-driven).
- Validation: failing-first tests — `taskspanel` model/view tests (13), `api_tasks_test.go` (4), `tasks_overlay_814_test.go` (10, pattern: `overlay_670_test.go`); enumeration lists updated (`cmd_parser_test.go` incl. `TestTUI364_RegistryCompleteness`, `search_test.go`, `tabcomplete_test.go`). `go test ./cmd/harnesscli/... -count=1`: all 29 packages ok. gofmt/vet clean.

## 2026-07-21 (Issue #886 runBlockedError.Error Coverage Fix)

- Symptom: post-merge regression gate (`scripts/test-regression.sh`) failed after PR #882 landed: `coveragegate` flagged `(*runBlockedError).Error` (cmd/harnesscli/main.go) at 0.0% — `functions with zero coverage detected`.
- Cause: PR #882 added `runBlockedError` as a sentinel detected via `errors.As` in `run()`/`runContinue()`; its blocked-path tests assert exit code and stderr content but never invoke `Error()`, and the PR's validation ran package tests, not the full gate. Same failure shape as #875.
- Fix (test-only): added `TestRunBlockedErrorMessage` in `cmd/harnesscli/exitcodes_test.go` — pins both sentinel contracts across all three blocked signals: `Error()` names the blocked event type, and `errors.As` detection survives wrapping. Function now reports 100%; no production code changed.

## 2026-07-20 (OS-Service Install for harnessd — Epic #807)

- Shipped `harnesscli service install|uninstall|start|stop|status` for end users: user-level launchd agent on macOS (`~/Library/LaunchAgents/com.gocode.harnessd.plist`, `RunAtLoad`+`KeepAlive`) and `systemd --user` unit on Linux (`~/.config/systemd/user/harnessd.service`, `Restart=on-failure`, `WantedBy=default.target`). Slice 1 (PR #826): install/uninstall + pure unit generators with `--binary`/`--addr`/`--log-dir`/`--dry-run`; addr resolution reuses the daemon's own stack (`HARNESS_ADDR` env or `~/.harness/config.toml`, default `:8080`) exported into the unit environment. Slice 2 (PR #866): lifecycle commands over launchctl/systemctl behind an injectable runner; `status` distinguishes not-installed / installed-not-running / running-healthy / running-unreachable via a `GET /healthz` probe.
- Slice 3 (this change, docs-only): new "OS Service Install" section in `docs/runbooks/distribution.md` (commands, per-OS unit paths, log locations, flags, troubleshooting incl. `launchctl print gui/$(id -u)/com.gocode.harnessd` and `journalctl --user -u harnessd`, lingering note, scope guardrails); README gains an end-user pointer and the tmux guidance is explicitly re-scoped to repository dev agents.
- Validation: slices 1–2 landed under strict TDD (`go test ./cmd/harnesscli/ -run Service -count=1` — 32 tests) plus real launchd end-to-end on macOS (install → start → healthy status → stop → uninstall; `plutil -lint` OK). Slice 3 verified every documented flag/path/command against `cmd/harnesscli/service.go` and live `-h` output; no code changed.

## 2026-07-19 (Plugin Zip + GitHub Archive Install Sources — Epic #821 Slice 3)

- `internal/plugins.NormalizeSource` now detects zip sources: `.zip` suffix on remote URLs and local files, and GitHub `/archive/` URLs even without the suffix (`Source.Zip`). Local zip files are non-remote (trusted by default); zip URLs are remote (untrusted, install-time confirmation from slice 2 applies).
- `Installer.Stage` fetches zips with `net/http` (remote) or from disk (local) and extracts with stdlib `archive/zip` into the existing staging dir; `rejectSymlinks`, `LoadBundle` validation, and atomic promote are unchanged. Every entry name is validated before any write (no absolute paths, no `..` elements, no backslashes), symlink entries are rejected, and a single shared top-level directory (the GitHub archive convention) is stripped so the bundle root lands at the staging dir. Fetch, corrupt-zip, and bad-entry errors name the source.
- TDD: failing-first tests cover the detection matrix (zip URL / GitHub archive ± suffix / git URL / shorthand / local zip / local dir / local non-zip file), local and GitHub-style single-root installs, `..`/absolute/backslash/symlink rejection with no residue, and corrupt/404 sources naming the source; CLI regression proves `plugin install --yes <httptest zip URL>` end-to-end.

## 2026-07-20 (Shell Mode Slice 3 — Epic #811)

- After a foreground shell-mode command exits, the next agent prompt now
  carries a `<shell-command command="..." exit-code="...">` block with
  CDATA-wrapped output (same wrapping pattern as @-mention expansion:
  `xmlAttrEscape` + `cdataSafe`, so command text cannot break the block).
- The block is single-use (cleared on injection), prepended to `expandedValue`
  before `startRunCmd`; the display bubble and transcript keep the user's
  original text. Output gets a second, prompt-side head+tail truncation at
  10KB (rune-aligned) on top of the executor's 30KB capture cap.
- Only commands that exited on their own are captured (success or non-zero
  exit); interrupted/timed-out commands are excluded — the user killed them
  deliberately, so partial output is not context-worthy.
- Validation: `go test ./cmd/harnesscli/... -count=1` green (27 packages);
  gofmt/vet clean on touched files.

## 2026-07-20 (Headless Exit Codes — Epic #823, Slice 3)

- A headless run that blocks on input it will never receive no longer streams forever: `processSSEBlock` (`cmd/harnesscli/main.go`) now classifies `run.waiting_for_user`, `tool.approval_required`, and `plan.approval_required` via `isBlockedEvent` (`cmd/harnesscli/exitcodes.go`), and when stdin is non-interactive the stream loop returns a `runBlockedError`. `run()` and streaming `runContinue()` map it to exit 3 (`exitBlocked`) with a stderr message naming the run ID, the reason, and the `harnesscli continue <run-id>` resume command.
- The server-side run is never auto-cancelled on the blocked path (resumable by design); the blocked event line is still printed to stdout before exit, preserving the every-event-is-printed contract.
- Terminal detection reuses the package's existing injectable `stdinIsTerminal` double (`cmd/harnesscli/plugins.go:107`) — no new TTY dependency, tests stub it. Interactive stdin behavior is unchanged: blocked signals do not abort the stream (interactive answer wiring remains the ask-user epic's scope, per the epic's cross-epic constraint).
- TDD: failing-first tests cover all three blocked signals × (non-interactive → exit 3 + stderr content + no cancel POST; simulated TTY → stream continues to the terminal event's code), plus `runContinue()` blocked → 3 and unit tables for `isBlockedEvent`/`blockedEventReason`.
- Validation: `go test ./cmd/harnesscli/... ./internal/harness/... ./test/e2e/... -count=1` green; gofmt/vet clean.

## 2026-07-20 (Mid-Turn Steering — Epic #820, Slice 3)

- `ctrl+g` now steers the active run with the input-box content: new `Steer` binding in `cmd/harnesscli/tui/keys.go` (ctrl+s stays copy; ctrl+r stays reserved for future history search — re-grepped unbound), included in `ShortHelp`/`FullHelp` and the `buildHelpDialog` key list.
- `cmd/harnesscli/tui/model.go` KeyMsg handler: gated on `runActive && RunID != "" && TrimSpace(input) != ""` → clears the input (`input.Clear()`, cursor/history-pos safe), sets "Steering sent", fires slice 1's `steerRunCmd`; ungated presses are status hints only ("No active run to steer" / "Type a message to steer into the run"), never errors, never HTTP. New `SteerErrorMsg` case maps kinds via `steerErrorStatusText` (409 → "run already finished", 429 → "steering buffer full — try again shortly", 404 → "run not found"); `SteerAcceptedMsg` is consumed as a documented no-op for slice 4 to hook.
- `website/docs/cli/tui.md`: `Ctrl+G` keybinding row + a "Mid-turn steering" section (step-boundary injection, buffer-of-10 limit, `steered ⟂` marker, `harnesscli steer` one-shot).
- Strict TDD: `steer_key_test.go` drives `tea.KeyCtrlG` through the model against an httptest daemon (POST path/body observed, input cleared, `RunActive()` true, `cancelRun` not called), plus idle/empty-input no-HTTP hints and the error-kind mapping table. The 3s per-test cost is the existing `statusTickCmd(3s)` driven synchronously by the shared `runCmd` helper — same trade the ctrl+c/cancel tests already make.
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok; regression guards (`keys_test.go`, `escape_test.go`, `cancel_test.go`, `ctrlc_server_cancel_test.go`, `clipboard_test.go`, `sse_events_test.go`) green; `go test ./internal/server/ ./internal/harness/ -count=1` ok; gofmt/vet clean.

## 2026-07-20 (Issue #875 Shell-Mode Test-Seam Coverage Fix)

- Symptom: post-merge regression gate (`scripts/test-regression.sh`) failed after PR #870 landed: `coveragegate` flagged `(*Model).ShellCommandRunning` and `(*Model).WithShellExecTimeout` (cmd/harnesscli/tui/model.go) at 0.0%.
- Cause: PR #870 added both exported test seams but its `shellmode_exec_test.go` detects the running state via `ActiveToolCallStatus()` and never overrides the 120s default timeout, so the seams were dead code; the PR's validation ran only `go test ./cmd/harnesscli/...`, not the full gate.
- Fix (test-only): added `TestShellMode_CommandTimesOut` driving a real timeout kill through the executor — `WithShellExecTimeout(100ms)` + `sleep 999` — asserting the running flag transitions (`ShellCommandRunning` false→true→false), the timed-out error card, and prompt kill. Both functions now report 100%; the timeout finalization path (`timed out after …` card) is behaviorally pinned for the first time.

## 2026-07-20 (ACP Server Mode — Epic #806, Slice 2)

- `internal/acp` sessions over the runs API: `session/new` (unique `sess_<hex>` ids, cwd/mcpServers accepted but not acted on), `session/prompt` (content-block text extraction — text blocks joined, `resource_link` contributes its URI, empty extraction -> `-32602`), `session/cancel` notification -> `POST /v1/runs/{id}/cancel`. One ACP session maps to one run; a second prompt on a used session errors `-32603` (multi-turn is a later epic).
- New stdlib `RunsClient` (`client.go`): bounded client for `POST /v1/runs` and cancel, no-timeout client for the SSE subscription; `WaitTerminal` scans `GET /v1/runs/{id}/events` to a terminal event, tracking `run.cost_limit_reached` as a flag (it is non-terminal — the run then completes).
- Stop reasons: `run.completed` -> `end_turn`, cost-limit + completed -> `max_turn_requests`, `run.failed` -> `refusal`, `run.cancelled` -> `cancelled`.
- Dispatch is now concurrent: `session/prompt` holds its response open until the run terminates, so handlers run in goroutines or a mid-turn `session/cancel` could never be read. Writes stay mutex-serialized; `Serve` drains in-flight handlers at EOF. The slice-1 ordering test was updated to correlate pipelined responses by id (JSON-RPC clients never relied on order).
- `harness acp -server` flag; resolution flag > `loadConfig().Server` > `http://localhost:8080`; Bearer key from `loadConfig()`.
- Validation: strict TDD (failing tests first: `undefined: NewRunsClient`). `go test ./internal/acp/ -count=1` and `-race` green, incl. scripted-pipe flows — cancel mid-run issues the cancel POST and the prompt answers `cancelled`; concurrent sessions stay isolated; blocked-handler concurrency proof.

## 2026-07-20 (Agent Swarm — Epic #808, Slice 2)

- Extended `internal/subagents/swarm.go` with `resume_agent_ids`: entry `i`
  pairs with `items[i]`, and the item's expanded prompt is delivered to the
  existing subagent through the same messaging path `message_subagent` uses
  (`Manager.Get` to resolve the run ID, then `RunSteerer.SteerRun`).
- Validation rejects unknown IDs, duplicate IDs, more resume IDs than items,
  and active-incompatible statuses (only `running`/`waiting_for_user` accept
  steered messages, matching `SteerRun`'s contract). All checks run before
  any member launches, so a bad request starts nothing.
- Resumed members are scheduled first in the ramp, count against the same
  concurrency allowance, and are cancelled through the manager on parent
  cancellation like any started member.
- Report order stays deterministic: non-resumed item members in item order,
  then resumed members in resume order; resumed entries carry `Resumed: true`
  and their subagent ID from the start. Steer failures land in the report
  per member and never abort the cohort.
- The swarm takes the steerer via a new `WithSwarmSteerer` option; slice 3
  will wire it to the runner-backed steerer alongside the `agent_swarm` tool.
- TDD: resume behavior tests landed first (failed on undefined
  `WithSwarmSteerer`), covering the happy path, status compatibility table,
  unknown-ID/duplicate/overflow rejection, scheduling order with cap 1,
  report marking, steer-failure capture, and cancellation of resumed members.
- Validation: `go test ./internal/subagents/... -count=1` and `-race` green;
  new tests repeated 5x without flakes; full regression suite (see PR body).

## 2026-07-20 (`/undo` HTTP Route — Epic #805, Slice 2)

- Added `POST /v1/conversations/{id}/undo` (`internal/server/http_conversations.go`), routed next to `compact` with the same guards: POST-only (405 otherwise), `runs:write` scope (403), and `blockConversationCrossTenant` (cross-tenant 404, verified non-mutating).
- Handler `handleUndoConversation` delegates to Slice 1's `ConversationStore.UndoPrompts`. Body accepts `{"count": N}` (absent → default 1) or `{"to_step": S}`; empty body is treated as all-defaults. `to_step` is resolved to a count by `undoCountForStep`, which rejects steps that are negative, beyond history, or pointing at a non-prompt (non-`user` or `is_meta`) message with 400 before the store is called.
- Error mapping: `ErrUndoCrossesCompaction` → 409 `undo_crosses_compaction`; `ErrUndoCountOutOfRange` → 400; unknown conversation → 404; missing store → 501. Success returns `{"undone": true, "removed_from_step": S, "remaining_messages": M}` where M counts the persisted messages including the `is_meta` undo-boundary marker.
- Boundary semantics are store-enforced (Slice 1): the target prompt must sit strictly above the most recent `is_compact_summary` message; anything at or below the boundary is refused and the conversation is left untouched. This holds for both `count` and `to_step` forms.
- In-memory caveat, same as the existing compact route: the mutation is store-only, so `GET {id}/messages` on a conversation still resident in the runner's memory shows the pre-undo snapshot until the run ends or the daemon restarts (the store fallback then serves the truncated history). The TUI slice refetches after undo.
- Validation: failing-first tests in `internal/server/http_undo_test.go` (10 endpoint behavior tests) and `internal/server/http_undo_tenant_test.go` (cross-tenant 404 + no-mutation, `runs:read`-only 403); `go test ./internal/server/ -run 'Undo|TenantIsolation' -count=1` green. tmux smoke against `harnessd` (fake provider, `HARNESS_CONVERSATION_DB` set): `POST .../undo {"count":1}` → 200 `{"undone":true,"removed_from_step":2,"remaining_messages":3}`; after restart, `GET .../messages` serves the truncated history with the marker.

## 2026-07-19 (Unified /tasks Panel — Epic #814, Slice 2)

- Background bash jobs are now enumerable and killable daemon-wide. `JobManager.List` (`internal/harness/tools/bash_manager.go` unexported `list` + exported wrapper in `job_manager_exports.go`) returns `JobInfo` snapshots (id, command, working dir, started-at, tenant, running, exit code, timed-out) with a `Status()` of `running`/`exited`/`timed_out`; `runBackground` captures the originating run's tenant from `RunMetadataFromContext`.
- New `harness.JobTracker` (`internal/harness/job_tracker.go`): per-registry job managers register via the new `DefaultRegistryOptions.JobTracker` (unregister on registry shutdown), so the main registry, per-run provisioned-workspace registries (runner.go), and subagent worktree registries are all covered. Task IDs are namespaced `jm<N>:job_<n>` because managers number jobs from `job_1` independently.
- Server: `GET /v1/tasks` unions `bash_job` entries (label = command, cancel action while running) with the same tenant filtering as callbacks; new `POST /v1/jobs/{id}/kill` (runs:write, 404 unknown/cross-tenant, 501 unconfigured) reuses `JobManager.Kill`.
- harnessd: one `JobTracker` created in `main.go`, threaded via `baseRegistryOptions` and `runtime_container`/`bootstrap_helpers` into `ServerOptions.JobTracker`.
- Validation: failing-first tests — `bash_manager_list_test.go` (7 tests incl. race-checked concurrency), `job_tracker_test.go` (6 tests incl. registry-wiring integration through the real `bash` tool and `job_output`), and 8 new `http_tasks_test.go` tests. `go test ./internal/server/ ./internal/harness/ ./internal/harness/tools/ ./cmd/harnessd/ -count=1` all pass; the pre-existing flaky `TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly` passed in this run.
## 2026-07-20 (Headless Exit Codes — Epic #823, Slice 2)

- `harnesscli -prompt ...` and streaming `harnesscli continue` no longer exit 0 for every terminal run state: the terminal event now maps through `exitCodeForTerminalEvent` (`cmd/harnesscli/exitcodes.go`) — `run.completed` → 0, `run.failed` → 2, `run.cancelled` → 6, unknown/empty → 1 (defensive non-zero default). stdout is unchanged (`run_id=` / `terminal_event=` lines preserved); only the process exit code changed.
- New `cmd/harnesscli/exitcodes.go` holds all six contract codes (`exitSuccess`/`exitClientError`/`exitRunFailed`/`exitBlocked`/`exitCancelled`/`exitInterrupted`) as the single source of truth; literal `1`/`130` returns in `run()` and `handleStreamError()` were replaced with the named constants. `exitBlocked` (3) is wired in Slice 3.
- TDD: failing-first tests in `cmd/harnesscli/exitcodes_test.go` — mapping table (all terminal events + non-terminal/unknown/empty), contract-value pins, and `httptest` SSE end-to-end assertions that `run()`/`runContinue()` return 0/2/6 for completed/failed/cancelled streams while preserving the stdout lines; `-no-stream` proven to exit 0 without opening the event stream.
- Validation: `go test ./cmd/harnesscli/... ./internal/harness/... ./test/e2e/... -count=1` green; gofmt/vet clean; no existing test relied on the old failed/cancelled-exits-0 behavior.

## 2026-07-19 (Plugin Trust CLI + Install-Time Confirmation — Epic #821 Slice 2)

- Split `internal/plugins.Installer.Install` into `Stage` (fetch, symlink reject, manifest validation into a private `.install-*` staging dir) and `StagedBundle.Promote`/`Discard`, so the CLI can review declared surfaces between validation and promotion. `Install` keeps its one-step contract as Stage+Promote.
- Added `harnesscli plugin trust <name>` / `plugin untrust <name>` over `StateStore.SetTrusted`, making trust reachable for remote bundles for the first time; `plugin list` now appends an `untrusted — commands/hooks/MCP inactive` hint to untrusted entries.
- Remote installs now print the manifest's declared executable surfaces and require confirmation before promotion: interactive y/N on a terminal, `--yes`/`-y` for scripts, refusal with a `--yes` hint otherwise (no stdin deadlock in pipelines). Declined installs leave no files and no state record.
- `plugin update` re-stages from the recorded source, and for remote bundles re-prints surfaces and re-requires confirmation only when the declared surfaces changed; unchanged remote updates skip confirmation and preserve trust (pinned by tests).
- TDD: failing-first tests cover Stage/Promote/Discard residue discipline, trust/untrust round-trip with `plugins.TrustedBundles` gating proof, declined/non-TTY/`--yes`/prompt-accept install paths, and update trust preservation on both unchanged and changed surfaces. Remote fixtures use local `file://` git remotes (no network).

## 2026-07-20 (Mid-Turn Steering — Epic #820, Slice 2)

- The TUI no longer drops `steering.received` SSE events: `cmd/harnesscli/tui/model.go`'s dispatch switch gained a `case "steering.received"` that parses the fixed `{"message": "..."}` payload (harness `drainSteering` contract) and appends a user bubble + transcript entry (role `user`) via a new `appendSteeringMarker` helper.
- Both viewport and transcript/export carry a `steered ⟂ ` marker prefix so steered input reads distinctly from a typed prompt; the helper is the rendering slice 4's local echo will reuse. Malformed/empty payloads are dropped without panic; `m.lastEventID` bookkeeping is untouched (the case sits after ID tracking in the existing switch).
- Strict TDD: `cmd/harnesscli/tui/steer_events_test.go` (package `tui_test`, `sse_events_test.go` pattern) drives scripted `SSEEventMsg`s — marker+message in viewport, exactly one role-`user` transcript entry, distinction from a typed prompt, five malformed-payload shapes (no panic, no marker, transcript unchanged, run stays active).
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok (incl. `sse_events_test.go`, `escape_test.go`, `cancel_test.go`, `ctrlc_server_cancel_test.go`, `clipboard_test.go`, `keys_test.go` guards); `go test ./internal/server/ ./internal/harness/ -count=1` ok; gofmt/vet clean.

## 2026-07-20 (Shell Mode Slice 2 — Epic #811)

- Shell-mode submit now executes the command locally in the TUI process
  (`sh -c`, 120s default timeout) and streams combined stdout/stderr into a
  tool-style `shell` card, replacing the slice-1 stub. The executor
  (`cmd/harnesscli/tui/shellexec.go`) is fully async: a pump goroutine feeds
  output/done messages on a buffered channel that the model polls with a
  tea.Cmd, so `Update()` never blocks.
- Output is bounded twice: live deltas stop after 30KB, and the final done
  message carries a 30KB head+tail buffer (same strategy as bash plugins), so
  flood commands like `yes` stay memory-safe.
- Esc and Ctrl-C kill the whole process group (`Setpgid` + group SIGKILL +
  `WaitDelay`, mirroring `internal/harness/tools/exec_group_unix.go` #786);
  the done message then finalizes the card as interrupted. Cards reuse the
  existing `handleToolStart`/`handleToolChunk`/`handleToolResult`/
  `handleToolError` pipeline; `extractToolCommand` now covers `shell`.
- Known limitation: the tooluse `ErrorView` renders only `ErrorText`, so
  failed commands report `exit status N` plus the bounded output as reflowed
  error text (same behavior as agent bash errors today).
- Validation: `go test ./cmd/harnesscli/... -count=1` green; gofmt/vet clean
  on touched files.

## 2026-07-19 (Ctrl-V Image Paste + Chips — Epic #818 Slice 2)

- Wired the slice-1 clipboard reader into the TUI: `ctrl+v` (new
  `KeyMap.PasteImage`, also listed in the help dialog) runs a modality
  pre-flight, then reads the clipboard image asynchronously; success appends
  an `[image #N]` chip row above the input prompt, failure maps the typed
  errors (`ErrClipboardNoImage`/`ErrClipboardHeadless`/`ErrClipboardUnsupported`)
  to inline status messages.
- `inputarea` owns chip state (`Attachment{Path, MediaType}`): Backspace on an
  empty buffer removes the latest chip and deletes its temp directory
  (`removeAttachmentFiles` seam); chips survive text submit (pending until
  slice 3 sends them).
- Modality pre-flight: `GET /v1/models` now returns the catalog `modalities`
  (additive, both registry and catalog branches); the TUI keeps the fetched
  list (`m.serverModels`) and rejects the paste before any subprocess when
  the effective model is known text-only. Unknown modalities (offline, older
  server, OpenRouter-sourced list) allow the paste — slice 3's server gate
  enforces at send time.
- Bug found and fixed during implementation (regression test added):
  `WindowSizeMsg` re-creates the input component, which dropped pending
  chips; attachments are now carried across the re-create via
  `inputarea.Model.WithAttachments` (`TestPasteImageChipsSurviveWindowResize`).
- Verified on macOS against the real clipboard (image set then restored):
  paste → `[image #1]` + temp dir; Backspace → chip gone + temp dir deleted;
  re-paste → `[image #1]`; text prompt submits with the chip pending.

## 2026-07-20 (Issue #854 TUI Subscription Credential Import)

- Replaced the stale `/keys` startup hint based on nonexistent `KIMI_SUBSCRIPTION_AUTH` with synchronous, local-only reads of both harness-owned Codex and Kimi credential stores. The TUI stores only a non-secret availability marker.
- Added bodyless `POST /v1/providers/{codex-subscription,kimi-subscription}/import-subscription`. It reuses the existing vendor-file import functions and the exact daemon-bootstrap token-source construction, then replaces the live registry source so `GET /v1/providers` becomes configured without restarting `harnessd`.
- Added `/keys` `i` import action for subscription rows only. Successful imports refetch the provider catalog; errors show the server's actionable remediation rather than an HTTP/stack trace. API-key rows ignore `i`.
- Regression coverage uses temporary HOME vendor fixtures only. It proves Codex and Kimi import-to-live-registry transitions, absent-login guidance, route scoping, bodyless TUI requests, and provider-list refresh behavior. No token values are logged or included in the endpoint contract.

## 2026-07-19 (Coverage-Gate Fix — `internal/acp` Zero-Coverage Functions)

- Post-merge regression gate (`scripts/test-regression.sh`) failed after epic #806 slice 1 landed: `coveragegate` flagged `(*Conn).drainLine` (conn.go) and `(*rpcError).Error` (jsonrpc.go) at 0.0%.
- Cause: the existing oversized-line tests only exercised the path where the over-cap fragment already contains the newline (no drain needed); the drain path (fragment ends mid-line, `ErrBufferFull`) and the `error`-interface method were never called.
- Fix (test-only): added a `ReadLine` subtest that shrinks `maxMessageSize` below the bufio buffer size so the crossing fragment lacks the terminator (covers `drainLine` and stream realignment), plus a direct `rpcError.Error` test. Both functions now report 100%.

## 2026-07-19 (Plugin Home Decision + Manifest v1 Contract — Epic #821 Slice 1)

- Extended `docs/design/installable-plugin-bundles.md` into the stable v1 authoring contract: full `plugin.json` field reference with validation rules, install layout (`<name>/<version>` under `$HARNESS_GLOBAL_DIR/plugins`, default `~/.go-harness/plugins`), and the enabled-vs-trusted gating table grounded in the current loader wiring.
- Decided the single plugin home: `~/.go-harness/plugins` is the bundle home; `~/.config/harnesscli/plugins/*.json` is legacy-but-supported with a documented migration path.
- Added a TUI startup warning (`legacyPluginsDirWarning` in `cmd/harnesscli/tui/plugin_loader.go`, wired in `model.go`) when the legacy dir contains JSON plugins, pointing at the bundle format; startup status wording changed from "had errors loading" to "plugin warning(s) at startup" since warnings now include a non-error deprecation notice.
- TDD: failing-first tests cover the warning surface (non-empty/missing/empty/JSON-free dirs) and that legacy JSON plugins still register as working slash commands while the warning surfaces.
- Validation: `go test ./cmd/harnesscli/tui/ -run 'TestLegacyPluginsDir|TestNoLegacyPluginsDir|TestLoadAndRegisterPlugins|TestWithPluginsDir' -count=1` and the full touched-package runs below are green.

## 2026-07-19 (ACP Server Mode — Epic #806, Slice 1)

- Added `internal/acp`: a stdlib-only (`encoding/json`) newline-delimited
  JSON-RPC 2.0 transport for the Agent Client Protocol — framed `Conn`
  (partial lines, multiple messages per read, 16 MiB message cap with stream
  realignment, goroutine-safe writer), envelope types, and spec error codes
  (`-32700` parse, `-32600` invalid request, `-32601` method not found,
  `-32602` invalid params).
- `initialize` handshake negotiates protocol version (agent supports v1 only,
  always replies 1 per spec) and returns agent capabilities:
  `loadSession: false`, text-only `promptCapabilities`, `agentInfo`, empty
  `authMethods`. Notifications and client→agent responses never get replies;
  diagnostics go to a separate writer so stdout stays a pure protocol channel.
- Wired `harness acp` (`runACP` in `cmd/harnesscli/acp.go`, dispatch case in
  `cmd/harnesscli/auth.go`) serving the handshake over stdin/stdout.
- Distinct from the pre-existing SDK-based `internal/harnessacp` /
  `cmd/harness-acp` adapter (epic #746): this package is the epic-#806
  stdlib-only implementation; session methods land in slices 2–4.
- Bug found by TDD oversized-line test: `ReadLine` drained one line too many
  when an over-cap message's newline arrived in the same read fragment;
  fixed by only draining when the terminator is still unconsumed. Covered by
  `TestConnReadLine/oversized_line...` and
  `TestServerOversizedMessageRejectedStreamStaysAligned`.
- Validation: `go test ./internal/acp/... -count=1` (also `-race`) and
  `go test ./cmd/harnesscli/... -count=1` green; acceptance
  `printf '...initialize...' | harness acp` prints a single JSON-RPC result
  with capabilities and exits 0.
## 2026-07-19 (Unified /tasks Panel — Epic #814, Slice 1)

- Added `GET /v1/tasks` (`internal/server/http_tasks.go`): a read-scoped union endpoint returning subagents, cron jobs, and pending delayed callbacks as one `Task` DTO (`id`, `type`, `status`, `label`, `started_at`, `age_seconds`, `actions`). Unconfigured sources are skipped, so an empty daemon returns `{"tasks": []}`; a failing source fails the request rather than silently dropping entries.
- Added `CallbackManager.ListAll` (`internal/harness/tools/delayed_callback.go`) for cross-conversation enumeration of pending callbacks; fired/canceled callbacks stay excluded, matching `List` semantics.
- Tenant scoping reuses the existing per-source helpers verbatim (`filterSubagentsByTenant`, `filterCronJobsByTenant`) and mirrors the cron exact-match shape for callbacks; auth matches `/v1/subagents` and `/v1/cron/jobs` (`runs:read`).
- Wired the daemon's `*tools.CallbackManager` into `server.ServerOptions.CallbackLister` through `cmd/harnessd` (`main.go` → `runtime_container.go` → `bootstrap_helpers.go`).
- Validation: failing-first tests in `internal/server/http_tasks_test.go` (7 handler tests) and `TestCallbackManagerListAll`; `go test ./internal/server/ ./cmd/harnessd/ -count=1` pass. `go test ./internal/harness/tools/ -count=1` has one pre-existing failure (`TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly`) that fails identically on main (a439dc9f) and is unrelated to this slice.

## 2026-07-19 (Agent Swarm — Epic #808, Slice 1)

- Added `internal/subagents/swarm.go`: a `Swarm` orchestrator that fans one
  `prompt_template` (with a required `{{item}}` placeholder) over 1–128 items
  into concurrent subagents started through the existing
  `tools.SubagentManager` (`InlineManager`).
- Validation rejects missing placeholders, empty/oversized item lists,
  duplicate expanded prompts (compared trimmed, since the manager trims), and
  `resume_agent_ids` (reserved for Slice 2).
- Concurrency ramps kimi-code style: 5 members start immediately, then +1
  in-flight allowance every 700ms, capped by `HARNESS_SWARM_MAX_CONCURRENCY`
  (read once at construction; default 128, clamped to 128).
- Caller context cancellation cancels every started member via the manager
  (members finishing Start after the sweep self-cancel, closing the race);
  unstarted members are reported cancelled. Member failures land in the
  aggregated `SwarmReport` (deterministic item order) and never abort the
  cohort.
- TDD: behavior tests landed first (initially failing on undefined symbols),
  covering validation, ramp timing via an injected ticker, env cap,
  cancellation propagation, per-member failure capture, and an acceptance
  test through a real `Manager` + `InlineManager`.
- Coverage-gate lesson: the zero-coverage-function gate caught an unused
  speculative option (`WithSwarmRamp`) after the first full regression run.
  The epic fixes the ramp at 5/+1-per-700ms with only the env cap as a knob,
  so the option was removed (dead code) rather than padded with a test for
  behavior nothing calls. Keep slice API surface to what the epic specifies.
- Validation: `go test ./internal/subagents/... -count=1` and `-race` green;
  new tests repeated 5x without flakes.
- Note: epic-level docs (`agent_swarm` tool description, swarm design doc)
  land with their owning slices (3+); no pre-existing subagent design doc
  exists to update in this slice.

## 2026-07-19 (Mid-Turn Steering — Epic #820, Slice 1)

- Added the client plumbing for the existing `POST /v1/runs/{id}/steer` route, strict TDD:
  - `cmd/harnesscli/tui/api.go`: `steerRunCmd(baseURL, runID, prompt, apiKey)` mirroring `cancelRunCmd`; routes through `newHarnessRequest` so harnessd auth is preserved (pinned by the `api_auth_test.go` audit table + static routing regression).
  - `cmd/harnesscli/tui/messages.go`: `SteerAcceptedMsg` (202) and `SteerErrorMsg` with stable `Kind` strings (`not_found`, `run_not_active`, `steering_buffer_full`, `invalid_prompt`, `http`, `transport`) for slice 3's status-bar mapping.
  - `cmd/harnesscli/runctl.go` + `auth.go` dispatch: `harnesscli steer <run-id> <prompt>` one-shot subcommand mirroring `runCancel` (`-base-url` only — the epic's `-api-key` parenthetical does not match `runCancel`, which has no such flag; noted in the PR).
  - Empty/whitespace prompts are rejected client-side in both paths before any HTTP request (the server would 400).
- Live-daemon smoke: `harnessd` with `HARNESS_PROVIDER=fake` + a scripted bash turn held active via `HARNESS_TOOL_APPROVAL_MODE=all`; `harnesscli steer <id> "focus on X"` printed `Run <id> steering accepted` (exit 0); unknown run → `not found` (exit 1); finished run → `not active` (exit 1).
- Validation: `go test ./cmd/harnesscli/... -count=1` all ok; `go test ./internal/server/ ./internal/harness/ -count=1` ok; `go vet ./cmd/harnesscli/...` clean; gofmt clean on touched files (repo-wide gofmt drift on unrelated files pre-exists on main).

## 2026-07-19 (Clipboard Image Reader — Epic #818 Slice 1)

- Added `ReadImageFromClipboard` in `cmd/harnesscli/tui/clipboard_image.go`:
  reads a PNG off the system clipboard into an `os.MkdirTemp` file and returns
  `ClipboardImage{Path, MediaType}` with typed sentinel errors
  (`ErrClipboardHeadless`, `ErrClipboardUnsupported`, `ErrClipboardNoImage`).
- Platform matrix: macOS via `osascript` (pbpaste cannot read image flavors —
  its `-Prefer` accepts only txt/rtf/ps — so the `PNGf` class is read as a
  `«data PNGf<hex>»` record and hex-decoded in-process); Linux via `wl-paste`
  or `xclip`; anything else returns `ErrClipboardUnsupported`. Headless mode
  (`IsHeadless()`) short-circuits before any subprocess.
- Strict TDD: 13 failing-first tests cover the happy paths (exact PNG bytes in
  the temp file), no-image/tool-missing/malformed-payload errors, and the
  no-subprocess headless guarantee, using package-level exec seams
  (`clipboardImageGOOS`/`clipboardImageLookPath`/`clipboardImageOutput`).
- Verified on macOS against the real clipboard (image set then restored):
  reader produced a valid PNG temp file via the unfaked code path.

## 2026-07-19 (Shell Mode Slice 1 — Epic #811)

- Added shell-mode input state to the `harnesscli` TUI: `!` on an empty input
  (typed or pasted) enters shell mode; the input area renders a `!` prompt
  marker inside a violet rounded border; Backspace/Esc on an empty shell-mode
  input exits; submit is a stub status message (execution lands in slice 2).
- Root `Model` owns the `shellMode` flag and re-applies it to the re-created
  input component on every `WindowSizeMsg`; the inputarea component owns only
  rendering state (`SetShellMode`), keeping mode transitions in one place.
- Esc with a non-empty shell input clears the text but stays in shell mode —
  exit happens only on an already-empty input, matching kimi-code.
- Validation: `go test ./cmd/harnesscli/tui/ -count=1` and
  `go test ./cmd/harnesscli/tui/components/inputarea/... -count=1` pass.

## 2026-07-19 (ACP Server Mode — Epic #746)

- Added `cmd/harness-acp` and `internal/harnessacp`, using pinned
  `github.com/coder/acp-go-sdk v0.13.5` (compatible with Go 1.25) for stdio
  JSON-RPC lifecycle handling.
- The adapter keeps harnessd as the only execution path: ACP sessions map to
  stable conversations; prompt/cancel/approve/deny use existing run routes;
  the shared `HarnessClient` now exposes parsed SSE streaming for adapters.
- ACP updates project assistant message/thought deltas, tool lifecycle events,
  approval requests, and todo plan updates. The key-free fake HTTP/SSE ACP
  prompt-turn test covers request-to-terminal update flow.
- Validation before PR: targeted ACP and harness MCP package tests, then the
  repository formatting, vet, and regression gates.

## 2026-07-19 (Enforced Plan Mode — Epic #740)

- Added per-run plan state, central policy-wrapper gating, broker-backed plan-exit approval, SQLite plan persistence, CLI/TUI request plumbing, and a scrollable TUI approval preview. Mutations with absent or non-matching paths fail closed while planning.

## 2026-07-19 (Session Rewind — Epic #739)

- Added SQLite pre-image points, non-fatal runner capture, hash-checked restore/truncation, HTTP list/restore routes, and explicit TUI confirmation. Oversized files are skipped rather than stored.

## 2026-07-19 (Reliability Epic #644 Reconciliation)

- Reconciled the 2026-06-24 15-slice long-session reliability plan against the supplied `origin/main` baseline. The code and deterministic regressions for T03–T15, plus the original T01/T02 behavior, were already present on the baseline (principally from prior harness/TUI integration work), so they were not duplicated or falsely represented as new failing-first commits.
- Closed the two remaining plan-level correctness gaps with failing-first regressions:
  - T01: completed run states are retained until their terminal event has actually persisted, preventing a transient store failure from silently dropping the only in-memory terminal history.
  - T02: every event-store append now receives a five-second bounded context, preventing a wedged store from occupying a run goroutine indefinitely while preserving the existing lock-free terminal fanout path.
- Validation: focused T01/T02 tests passed under `-race`; full `go test ./... -race`, `go vet ./...`, and `./scripts/test-regression.sh` passed (`coveragegate: PASS`, 84.4% total, zero zero-coverage functions).

## 2026-07-19 (Multi-run TUI Dashboard — Epic #738)

- Implemented the six dashboard slices (#742, #745, #749, #753, #757, #762) as TUI-only changes: authenticated `/v1/runs` polling, grouped overlay navigation, `/dashboard`/`Ctrl+D`, one cancellable peek SSE bridge, selected-run steer/cancel, and isolated new-run dispatch.
- Added focused failing-first dashboard tests for list loading, grouped navigation, command/key opening, peek close lifecycle, control routing, and dispatch. No server route or dependency changes.
- Validation: `go test ./cmd/harnesscli/...` passes. Repository-wide formatting gate still reports pre-existing drift and a syntax-invalid training exercise; see final verification status.

## 2026-06-28 (Config-Driven Lifecycle Hooks — Epic #737)

- Implemented epic #737 and all six child issues (#741, #744, #750, #755, #759, #763) in worktree branch `codex/config-hooks-epic-737`, one commit per slice, strict TDD throughout.
- New package `internal/hooks`:
  - Hook-file schema + loader with strict JSON decoding (unknown fields rejected), structured per-file skip records, deterministic ordering, and user/project source classification.
  - Command + HTTP adapters implementing the four existing `internal/harness` hook interfaces unchanged; JSON wire types defined once in `wire.go` and shared by both adapters (pinned by golden tests).
  - Content-hash trust store (`~/.harness/hooks-trust.json`) gating project-level hook files; user-global files trusted implicitly; atomic temp+rename writes; corrupt/missing store fails closed (empty).
  - `Build` (def → adapter routed by event) and `Summary` (startup-computed listing, non-nil empty slices so JSON marshals `[]`).
- `internal/config`: `[hooks]` TOML section (`enabled`, `dirs`) following the existing rawLayer pointer-merge pattern.
- `cmd/harnessd`: `registerConfigDrivenHooks` appends adapters to existing `RunnerConfig` hook slices after compiled-in plugins; structured startup logs per loaded/skipped hook; summary flows through `runtime_container` → `buildServerOptions` → `ServerOptions.HooksSummary`.
- `internal/server`: `GET /v1/hooks` serves the startup summary (read scope); never re-derives per request.
- `cmd/harnesscli`: `hooks trust|revoke|list` maintenance subcommand; TUI `/hooks` command rendering the server listing (loaded table + skipped section + empty state) through the existing registry/API-client/viewport paths.
- `internal/harness/runner.go` (additive only): `duration_ms` on `tool_hook.completed`/`tool_hook.failed` events, matching the message-hook observability contract. No interface-signature changes anywhere.
- Bugs found during implementation (each got a permanent regression guard):
  - **Parallel test file collision**: table-driven command-adapter subtests shared one `hook.sh` path in one temp dir, so parallel subtests overwrote each other's scripts and every case saw the same script. Symptom: incoherent failures (deny results on allow cases). Cause: shared mutable file across `t.Parallel()` subtests. Fix: per-subtest `t.TempDir()` — the fixed table structure is the regression guard.
  - **httptest timeout test hung 30s**: the server handler blocked on `r.Context().Done()` but the Go server only cancels the request context on client disconnect after the handler has consumed the request body. Symptom: package suite took 30s. Cause: handler never read the body, so disconnect went undetected and the `time.After(30s)` backstop fired. Fix: consume the body first, then block on `r.Context().Done()`; suite back to ~1s.
  - **Timeout-kill test flake under race/parallel load**: the orphan assertion checked `kill(pid, 0)` once immediately after the kill; the background grandchild is reparented to init and reaped asynchronously, so a single instantaneous check raced reaping. Fix: poll for process death with a 5s deadline — assertion strength unchanged (processes must die), timing tolerance added.
  - **Same test, second flake mode (found by the fast PR gate)**: with a 1s hook timeout, full-suite CPU contention could fire the timeout before the just-exec'd script wrote its pid files — the orphan assertion then failed on missing files. Root cause: the test's pid discovery assumed script startup < hook timeout. Deterministic redesign: the hook runs in a goroutine, the test waits for pid files to appear (4s budget) before the 5s hook timeout fires, then asserts the timeout error and polls for process death; under pathological startup latency it degrades (with a `t.Logf`) to the timeout-error assertion only. Verified with `go test -race -count=3 ./internal/hooks/` under concurrent CPU load.
  - **Linux ETXTBSY (found by PR CI, invisible on macOS)**: `TestCommandHook_PostToolUse/empty_stdout_is_no_modification` failed in CI with `fork/exec .../post-empty.sh: text file busy` — the known overlayfs/Linux pattern of exec'ing a file written milliseconds earlier. Fix: all script-exec test sites (unit, integration, server e2e) run scripts through `/bin/sh <script>` — reading a just-written file never hits ETXTBSY. Adapter behavior unchanged (production hooks are exec'd directly; the window only exists for just-written files). PR CI then passed on both jobs.
- Observability: adapters log structured failure fields (`hook_name`, `event`, `tool_name`/`url`, `duration_ms`, `exit_code`/`status_code`, `error`) through the runner's `harness.Logger`; every exec emits existing `tool_hook.*`/`hook.*` SSE events with hook name, decision, and `duration_ms` — recon confirmed no new SSE event types were needed for config-hook deny attribution (documented in plugins.md).
- Docs: `docs/design/plugins.md` gained the full "Config-driven hooks" chapter (schema, discovery, command + HTTP wire protocols, message events, trust model, runtime semantics, end-to-end example); CLAUDE.md gained the Lifecycle Hooks HTTP API section; `docs/ux-paths.md` slash-command table gained `/hooks`; plans/design indexes updated.
- Note: no dedicated TOML config-reference doc exists (grepped docs/ for `conclusion_watcher` — only investigations/plans matched); the `[hooks]` section is documented in plugins.md instead, per the #741 fallback instruction.
- Validation:
  - Red phase per slice: new tests failed to compile/run before implementation (undefined `Load`, `NewCommandHook`, `NewHTTPHook`, `LoadTrustStore`, `Build`, `registerConfigDrivenHooks`, `loadHooksCmd`).
  - Green phase per slice: `go test ./internal/hooks/ ./internal/config/ ./cmd/harnessd/ ./internal/server/ ./cmd/harnesscli/...` all pass.
  - `go test -race -count=5 ./internal/hooks/` passes consecutively (post flake-fix).
  - Fast PR gate `go test ./internal/... ./cmd/...`: 95 packages ok, exit 0.
  - `./scripts/test-regression.sh`: PASS, `coveragegate: PASS (total=84.4%, min=80.0%, zero-functions=0)`.
  - PR #784 CI: both `test` jobs pass on the final head (`09569df8`).
  - `gofmt -l` clean on all touched files; `go vet` clean on all touched packages. (Pre-existing repo-wide gofmt drift on untouched files verified identical on `main`.)

## 2026-06-26 (Reliability T01 Memory Retention)

- Implemented reliability plan slice T01 locally:
  - Added bounded in-memory retention for terminal run states with default cap 32.
  - Added bounded in-memory conversation mirror retention with default cap 256.
  - Terminal runs with active subscribers are kept until the subscriber cancels; subscriber cancellation re-runs pruning.
- Added failing-first coverage in `internal/harness/runner_prune_test.go` for completed-run pruning, subscriber-protected terminal runs, and conversation mirror pruning.
- Validation:
  - Red phase: `go test ./internal/harness -run 'TestRunnerPrune' -count=1` failed to build because retention config fields did not exist.
  - `go test ./internal/harness -run 'TestRunnerPrune' -count=1`
  - `go test ./internal/harness -race -run 'TestRunnerPrune|TestRecorderGoroutine_DoneClosedAfterRun' -count=1`
  - `go test ./internal/harness -race -count=1`

## 2026-06-26 (Regression Coverage Gate Cleanup)

- Fixed the current `./scripts/test-regression.sh` coveragegate blocker without weakening the gate.
- Added meaningful zero-coverage tests across:
  - `cmd/harnessd/mcp_runner_adapter.go`
  - checkpoint service/store helpers
  - Docker fallback execution
  - replay tool dispatch lookup
  - callback manager construction
  - checkpoint approval denial
  - workspace path permission detection
  - deferred goal tool actions
  - networks/workflow/workflows stores and helpers
  - SQLite working-memory deletion
- Fixed two race/baseline issues surfaced by the regression run:
  - workflow subscriber cancellation can no longer close a channel while `emit` is sending;
  - the recorder goroutine test now holds the provider until `recorderDone` is observable.
- Validation:
  - `go run ./cmd/coveragegate -coverprofile=coverage.out -min-total=80.0` passed with total 84.5% and zero zero-coverage functions.
  - `./scripts/test-regression.sh` passed end to end.

## 2026-06-26 (Reliability T03 Empty Response Exhaustion)

- Implemented reliability plan slice T03 locally:
  - Empty-response retry exhaustion now fails the run with `max_empty_responses` instead of silently completing with empty output.
  - Retryable empty responses no longer consume outer step budget, so a run with `MaxSteps=1` can recover after retryable empty responses.
- Added failing-first coverage in `internal/harness/runner_empty_response_test.go` for both exhaustion failure and retry budget preservation.
- Validation:
  - `go test ./internal/harness -run 'TestEmptyResponseRetry_MaxRetriesExhausted|TestEmptyResponseRetry_DoesNotConsumeStepBudget' -count=1`
  - `go test ./...`
  - `go test ./... -race`
- Regression gate note:
  - `./scripts/test-regression.sh` still fails in the coverage gate because pre-existing zero-coverage functions remain outside this slice, including `cmd/harnessd/mcp_runner_adapter.go` and workflow/checkpoint store functions. Total coverage is above threshold at 83.7%, and the new daily TUI handlers are covered.

## 2026-06-26 (TUI-First Daily Harness Command Slice)

- Added first-pass daily run-control commands for the personal TUI-first harness plan:
  - `harnesscli continue <run-id> <prompt>` starts a continuation and streams the new run's events.
  - `harnesscli replay <run-id-or-rollout-path>` posts to the replay endpoint and prints formatted JSON.
  - `harnesscli search <query>` filters persisted run metadata locally.
  - `harnesscli runs` and `harnesscli show` alias the existing list/status behavior.
- Updated `scripts/go-code.sh` so installed `go-code` exposes `runs`, `show`, `cancel`, `continue`, `replay`, and `search` directly.
- Registered the remaining daily TUI slash-command entry points (`/attach`, `/runs`, `/replay`, `/resume`, `/doctor`) while preserving existing `/sessions`, `/search`, and `@path` file expansion behavior.
- Added bare run-ID replay resolution: `POST /v1/runs/replay` can now resolve `run_...` to `<RolloutDir>/*/<run_id>.jsonl` when a rollout directory is configured.
- Added shared Conductor repository settings for setup/build and concurrent workspace daemon runs.
- Reconciled stale `docs/context/known-issues.md` continuation-tool-filter status.
- Validation:
  - `go test ./cmd/harnesscli ./internal/server -run 'TestRunContinue|TestRunReplay|TestRunSearch|TestDispatch_DailyAliases|TestGoCodeScriptRoutesDailyCommands|TestHandleRunReplay_SimulateResolvesBareRunID|TestTUI041_BuiltinCommandsRegistered' -count=1`
  - `go test ./cmd/harnesscli ./cmd/harnesscli/tui ./internal/server -count=1`
  - `go test ./...`
  - `go test ./... -race`

## 2026-06-26 (Adapter-First Terminal-Bench Eval Harness)

- Hardened the Terminal-Bench runner and adapter.
  - `scripts/run-terminal-bench.sh` now performs preflight checks for dataset, Python, Docker daemon, tmux, Terminal-Bench command resolution, provider/key configuration, fake-provider turns, and target arch.
  - The runner now builds linux/amd64 or linux/arm64 `harnessd` and `harnesscli` once per campaign and passes the binary directory to the adapter through `HARNESS_BENCH_BINARY_DIR`.
  - The runner now passes explicit Terminal-Bench flags for model, custom agent import path, dataset path, output path, concurrency, attempts, and global timeouts.
- Added `scripts/terminal_bench_artifacts.py`.
  - Merges Terminal-Bench oracle output with adapter-produced `benchmark_result.json`.
  - Validates merged rows against `benchmarks/comparison/result.schema.json`.
  - Writes merged `results.jsonl`, `summary.json`, `run-env.json`, and an actionable `report.md`.
  - Classifies failed tasks as `oracle_fail`, `agent_timeout`, `harness_error`, `provider_error`, `tool_contract_error`, `workspace_error`, or `infra_error`.
- Updated the Terminal-Bench adapter to write per-trial `benchmark_result.json`, `harness_telemetry.json`, and `harnessd.log`, and to support key-free fake-provider mode.
- Extended the benchmark result schema with external Terminal-Bench `parser_results` and derived failure classification fields.
- Added `scripts/test_terminal_bench_artifacts.py` and wired it into the fast GitHub workflow.
- Stabilized `TestWorkerPool_RunQueuedEventEmitted` for race-mode regression runs by using the same longer wait as the adjacent queued-transition test and releasing held provider channels through cleanup-safe helpers.
- Validation:
  - `python3 scripts/test_terminal_bench_artifacts.py`
  - `python3 -m py_compile scripts/terminal_bench_artifacts.py scripts/test_terminal_bench_artifacts.py benchmarks/terminal_bench/agent.py`
  - `bash -n scripts/run-terminal-bench.sh scripts/build-bench-images.sh`
  - `git diff --check`
  - `go test ./internal/... ./cmd/...`
  - `go test ./internal/harness -race -run TestWorkerPool_RunQueuedEventEmitted -count=1`
  - `go test ./internal/harness -race -count=1`
- Full regression:
  - `scripts/test-regression.sh` was run in tmux.
  - First run failed in `go test ./... -race` on `internal/harness TestWorkerPool_RunQueuedEventEmitted`; the test was fixed and the package now passes under race.
  - Second run passed `go test ./...` and `go test ./... -race`, then failed at `coveragegate` despite 83.9% total statement coverage because existing zero-covered functions remain across packages such as `cmd/harnessd`, `internal/checkpoints`, `internal/workflows`, and `internal/workingmemory`.
- 2026-06-27 follow-up:
  - Added focused coverage tests for the remaining zero-covered functions across `cmd/harnessd`, checkpoints, cloud scheduler, replay, harness brokers/tools, networks, workflows, and working memory.
  - Stabilized `internal/harness TestWorktreePartialProvisionFailure_NoOrphan` under race mode by replacing the racy chmod watcher setup with a deterministic committed-directory blocker and bounded git setup.
  - `scripts/test-regression.sh` now passes in tmux with `coveragegate: PASS (total=84.6%, min=80.0%, zero-functions=0)`.
  - Refreshed Terminal-Bench CLI behavior from Context7, changed runner liveness from unsupported `--version` to `--help`, recorded the package version through Python metadata, and fixed empty extra-arg handling under `set -u`.
  - Fixed real-smoke adapter blockers discovered during live runs.
    - `cmd/harnesscli` now ignores SSE comment/heartbeat blocks such as `: ping` instead of failing with `invalid sse block`.
    - `benchmarks/terminal_bench/agent.py` now copies provider credentials through a private container env file instead of embedding them in Terminal-Bench `commands.txt`.
    - The adapter fetches run records, summaries, and harness logs through raw Docker `exec_run` output instead of parsing tmux-wrapped pane text.
    - The adapter sets `HARNESS_PRICING_CATALOG_PATH` to the copied repo catalog path for models that have catalog pricing.
  - Ran the accepted real-provider smoke campaign at `.tmp/terminal-bench/real-smoke-20260627-002630/2026-06-27__00-26-42`.
    - Provenance recorded: git SHA `89b5064fba6b17423029db4a41ac02fb8857d350`, provider `openai`, model `gpt-5-mini`, Terminal-Bench `0.2.18`, dataset hash `31b29122bfa16205e6a66967fc444f5d46924a8ed9f39167cb27fc1e676d5457`, concurrency `1`, attempts `1`, timeouts `1800/300`.
    - Result: 7/7 tasks passed with per-task `benchmark_result.json`, `harness_telemetry.json`, `harnessd.log`, command logs, pane logs, raw `results.json`, merged `results.jsonl`, `summary.json`, `run-env.json`, and `report.md`.
    - Secret check: the accepted artifact directory has zero files matching raw OpenAI key patterns.
  - Promoted `benchmarks/terminal_bench/baseline.json` from the accepted real-provider campaign. Cost is explicit but unpriced: `total_cost_usd=0.0`, `cost_status=unpriced_model`, because `catalog/pricing.json` does not yet include `gpt-5-mini`.

## 2026-06-26 (Issue #649 Completed Run Retention)

- Implemented reliability slice T01 from `docs/plans/2026-06-24-harness-reliability-plan.md` for issue `#649`.
- Added bounded in-memory retention for terminal run states:
  - `RunnerConfig.MaxCompletedRetention` defaults to 32.
  - completed, failed, and cancelled runs are eligible for pruning only when a durable run `Store` is configured, after terminal handling, and when no subscribers remain attached.
  - subscriber cancellation re-runs pruning so terminal runs held for streaming clients can be released after the stream detaches.
- Added bounded in-memory conversation mirror retention:
  - `RunnerConfig.MaxConversationRetention` defaults to 256.
  - `r.conversations`, `r.conversationOwners`, and conversation recency metadata evict together.
  - persistent `ConversationStore` history remains the fallback for pruned conversation mirrors.
- Added regressions in `internal/harness/runner_prune_test.go` covering completed-run pruning, active-subscriber retention, and persistent-store fallback for evicted conversation mirrors.
- Red phase:
  - `go test ./internal/harness -run TestRunner_Prune -count=1` failed to build because the retention config fields did not exist.
- Verification:
  - `go test ./internal/harness -run TestRunner_Prune -count=1`
  - `go test ./internal/harness -count=1`
  - `go test ./internal/server -run TestWorkerPoolLoad -count=1`
  - `go test ./internal/harness/... -race -count=1`
- Regression status:
  - `./scripts/test-regression.sh` passed the `go test ./...` and `go test ./... -race` phases.
  - `./scripts/test-regression.sh` failed at the coverage-gate phase because existing functions outside this slice still report `0.0%` coverage; total statement coverage was `83.9%`.

## 2026-05-05 (GitHub Pages User Repositioning)

- Recentered the go-code GitHub Pages copy around the developer visitor.
- Shifted the page from runtime/API-first positioning to the user problem: getting visible, steerable coding help inside a real repository.
- Added concrete use cases for failing tests, codebase orientation, and careful refactors.
- Reframed trust language around local-first work, visible tools, bounded runs, and recoverable context.
- Validation:
  - `python3` HTML parser sanity check for `docs/site/index.html`
  - Local browser preview at `http://127.0.0.1:4188/` with desktop and mobile viewport screenshots

## 2026-05-03 (Repository Rename and Public README Cleanup)

- Renamed the GitHub repository and public project surface from `go-agent-harness` to `go-code`.
- Reworked the top-level README for first-time browsers with a watercolor hero, quick start, install modes, repository map, HTTP surface summary, testing commands, and documentation links.
- Updated the GitHub Pages landing page and distribution runbook to use the new repository URL and Pages URL.
- Added `docs/assets/` for public documentation media and removed tracked root-level scratch files that made the repository look less presentable.
- Validation:
  - `file docs/assets/go-code-watercolor-hero.png`
  - `git diff --check`
  - `bash -n scripts/install.sh scripts/go-code.sh`
  - `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/pages.yml")'`

## 2026-05-03 (Repository Hygiene Cleanup)

- Removed tracked local/generated state that should not be part of the repository:
  - `.coord/`
  - `.codex-worktrees/`
  - benchmark `jobs/`
  - Python `__pycache__/` bytecode
  - root `code-reviews/` output
  - scratch `sol/`
- Moved root-level training exercise folders into `playground/training/` so the top-level tree reads like a product repository.
- Isolated incomplete `playground/examples/` and `playground/exercises/` behind their own module boundaries so `cd playground && go test ./...` covers the stable playground baseline without treating every scratch exercise as product code.
- Stabilized the moved persistent-trie training test by replacing random word generation with deterministic unique words, avoiding false "future word" failures from duplicate random samples.
- Tightened `.gitignore` to keep coordination state, generated job output, Python bytecode, and scratch training outputs from being recommitted.
- Validation:
  - `git diff --check`
  - `go test ./cmd/harnesscli/... -count=1`
  - `go test ./internal/quality/repostructure -count=1`
  - `go test ./... -count=1`
  - `cd playground && go test ./... -count=1`

## 2026-05-01 (User-Local Installer and Workspace-Aware TUI)

- Added a sudo-free local installer for distribution testing.
  - Added `scripts/install.sh`, defaulting to `~/.local/bin` via `~/.local`, with `--prefix`, `--bin-dir`, `--data-dir`, `--system`, `--add-to-path`, `--no-build`, `--uninstall`, and `--dry-run`.
  - Installer now copies runtime `prompts/` and `catalog/` assets into a sibling `share/go-code` directory so installed commands do not depend on the repo as the current working directory.
  - Updated `Makefile` so `make install` delegates to the user-local installer instead of copying directly into `/usr/local/bin`.
  - Updated `scripts/go-code.sh` to discover installed runtime assets and point missing-command hints at the installer.
- Made installed TUI launches preserve the caller's project workspace.
  - `harnesscli --tui -workspace <path>` now carries the workspace into `tui.TUIConfig`.
  - TUI run creation now includes `workspace_path`, matching single-shot prompt mode.
  - Added regressions for CLI workspace request payloads, TUI config workspace propagation, and TUI start-run workspace payloads.
- Validation:
  - `bash -n scripts/install.sh scripts/go-code.sh`
  - `scripts/install.sh --dry-run --no-build --prefix "$PWD/.tmp/install-dry-run"`
  - `GOCACHE=/tmp/go-build go test ./cmd/harnesscli ./cmd/harnesscli/tui -run 'TestRunCreatesAndStreamsToCompletion|TestNewTUIConfigIncludesWorkspace|TestRunTUIRequiresTerminal|TestStartRunCmdIncludesWorkspacePath' -count=1`
  - `GOCACHE=/tmp/go-build scripts/install.sh --prefix "$PWD/.tmp/install-verify"`
  - `.tmp/install-verify/bin/go-code --help`
  - `HOME=$(mktemp -d) GOCACHE=/tmp/go-build go test ./cmd/harnesscli/... -count=1`

## 2026-05-01 (Distribution Docs and GitHub Pages)

- Added public distribution documentation for Go Agent Harness.
  - `docs/runbooks/distribution.md` now documents the current source installer, installed command contract, GitHub Pages setup, release archive layout, future installer download mode, Homebrew tap direction, single-binary simplification path, and release checklist.
  - `README.md` now points daily users at `./scripts/install.sh --add-to-path`, `go-code`, and the distribution docs.
- Added a GitHub Pages-ready static site.
  - `docs/site/index.html` and `docs/site/styles.css` provide a single-page install and product overview for Go Agent Harness.
  - `docs/site/INDEX.md` indexes the site source folder.
  - `.github/workflows/pages.yml` publishes `docs/site` through GitHub Actions on pushes to `main` that touch the site or workflow.
- Updated documentation indexes:
  - `docs/INDEX.md`
  - `docs/runbooks/INDEX.md`
- Validation:
  - `curl -I http://127.0.0.1:4180/` against a temporary tmux-served `docs/site` static server
  - `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/pages.yml"); puts "yaml ok"'`
  - `git diff --check -- README.md docs/INDEX.md docs/runbooks/INDEX.md docs/runbooks/distribution.md docs/logs/engineering-log.md docs/logs/long-term-thinking-log.md .github/workflows/pages.yml`
  - `perl -ne 'print "$ARGV:$.: trailing whitespace\n" if /[ \t]$/; close ARGV if eof' docs/site/INDEX.md docs/site/index.html docs/site/styles.css`

- 2026-04-29: Fixed issue `#557` by making the container workspace provision success test use a unique, readable workspace ID per invocation instead of reusing `test-provision`.
  - Added `containerWorkspaceTestID(...)` in `internal/workspace/container_test.go`, combining a readable sanitized prefix with nanoseconds and an atomic sequence.
  - Updated `TestContainerWorkspace_Provision_Success` to register `t.Cleanup` with `Destroy(...)` after provisioning attempts so normal failures clean up the test container.
  - Added regressions proving:
    - generated test IDs are unique per call and keep the `test-provision-` prefix
    - Docker container name conflicts are not treated as skippable environment failures
  - Verification:
    - red phase: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/workspace -run TestContainerWorkspace_Provision_TestIDUniquePerCall -count=1` failed to build because `containerWorkspaceTestID` did not exist
    - green phase: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/workspace -run 'TestContainerWorkspace_Provision_(TestIDUniquePerCall|ConflictIsNotSkipped)' -count=1`
    - acceptance rerun: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/workspace -run TestContainerWorkspace_Provision_Success -count=2 -v` passed with both runs skipped because this sandbox cannot bind `:0`.
    - follow-up hardening: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness/tools/core -count=1` passed after making `TestGitDiffTool_MaxBytes` create its own dirty Git fixture instead of depending on this checkout having a diff.
  - Local environment blockers:
    - `go test ./internal/workspace -count=1` is blocked by sandbox network restrictions: `TestGetFreePort` cannot bind `:0`, and unrelated Hetzner `httptest` tests cannot listen on `[::1]:0`.
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh` is blocked by the same sandbox restriction across unrelated packages that use `httptest.NewServer`, `127.0.0.1:0`, or `[::1]:0`.
    - tmux session creation is blocked in this sandbox: `error connecting to /private/tmp/tmux-501/default (Operation not permitted)`.
    - GitHub CLI issue/PR access is blocked by `error connecting to api.github.com`.

- 2026-04-13: Added an autoresearch-style testing loop with a dedicated prompt-profile and target-driven run scripts.
  - Added `prompts/models/autoresearch.md` and wired it into `prompts/catalog.yaml` so the harness has a reusable testing-oriented prompt profile.
  - Added `scripts/autoresearch-run.sh` for one-shot autoresearch runs and `scripts/autoresearch-loop.sh` for cycling through coverage-gap-driven targets with per-run markdown reports under `.tmp/autoresearch/`.
  - Adjusted both runners to send `max_steps=50` by default and exposed `--max-steps` / `HARNESS_AUTORESEARCH_MAX_STEPS` overrides for future tuning.
  - Documented the workflow in `docs/runbooks/testing.md`, added the plan at `docs/plans/2026-04-13-autoresearch-testing-plan.md`, and updated the plans index and active-plan tracker.
  - Added prompt-profile resolution coverage in `internal/systemprompt/catalog_test.go` and refreshed the fixture catalog in `internal/systemprompt/testhelpers_test.go`.
  - Verification:
    - `bash -n scripts/autoresearch-run.sh scripts/autoresearch-loop.sh`
    - `go test ./internal/systemprompt`
    - `go test ./internal/systemprompt ./cmd/harnesscli`

- 2026-04-05: Added documentation-first orchestration guardrails and landed the stage-1 `harnessd` runtime-container extraction.
  - Added the umbrella orchestration program plan plus five stage specs under `docs/plans/`, with explicit feature statuses so planned checkpoints/workflows/memory/networks stay out of public docs until implemented.
  - Tightened `docs/runbooks/testing.md`, `docs/runbooks/documentation-maintenance.md`, and `docs/plans/PLAN_TEMPLATE.md` so large architecture work now requires characterization before refactors, failing tests before new behavior, permanent regression tests for discovered bugs, and status-aligned docs.
  - Extracted `cmd/harnessd/runtime_container.go` so:
    - `runMCPStdio(...)` delegates stdio assembly to `buildMCPStdioRuntime(...)`
    - `runWithSignals(...)` delegates runner/subagent/server assembly to `buildHTTPRuntime(...)`
  - Added direct tests in `cmd/harnessd/runtime_container_test.go` for the new MCP and HTTP runtime assembly helpers, including callback-runner and lazy-summarizer binding.
  - Verification:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -run 'TestBuild(MCPStdioRuntimeCreatesCatalogAndServer|HTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer)' -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server ./internal/subagents ./cmd/harnessd -count=1`

- 2026-04-01: Moved sandbox enforcement to the live tool execution boundary so per-run and continuation permissions now control bash/job behavior.
  - Added tool-context sandbox propagation in the runner step engine instead of relying on the registry startup sandbox.
  - Updated `JobManager` foreground/background execution to prefer the per-call sandbox from `context.Context`, while preserving the manager-level sandbox as a fallback default for non-run callers.
  - Added regressions proving:
    - start-run sandbox overrides can loosen a stricter registry default
    - continuation sandbox overrides can change behavior mid-conversation
    - direct `JobManager` calls respect context sandbox overrides for both foreground and background commands
  - Corrected the `SandboxScopeLocal` comment in `internal/harness/types.go` so the documented trust boundary matches the current enforcement model.
  - Verification:
    - `go test ./internal/harness/tools`
    - `go test ./internal/harness/tools/core`
    - `go test ./internal/harness`
    - `go test ./internal/server`

- 2026-03-29: Restored a green repo-wide test baseline after the structure cleanup.
  - Fixed `tmp/training-pubsub/broker.go` so active subscribers get retry-based delivery before a publish is counted as dropped, while lag accounting still works for genuinely full subscribers.
  - Simplified `tmp/training-skiplist/skiplist.go` to use a single RW lock for correctness under concurrent insert/search/delete paths.
  - Reworked `tmp/training-regex/regex.go` and `training-regex/regex.go` so `Regexp.Match(...)` uses AST-based full-string matching semantics that satisfy the current training tests.
  - Fixed `training-trie/trie.go` so `Delete(...)` returns whether a word was actually deleted instead of whether the root should be pruned.
  - Fixed `training-trie/trie_test.go` to remove a deadlocking parent/subtest `t.Parallel()` pattern from the stress test.
  - Verification:
    - `go test ./tmp/training-pubsub ./tmp/training-skiplist`
    - `go test ./tmp/training-regex ./training-regex`
    - `go test ./training-trie`
    - `go test ./...`

- 2026-03-28: Cleaned up the repository boundary between product code and experimental code.
  - Moved the ad hoc root-level Go snippets into `playground/examples/` and `playground/exercises/`.
  - Added `playground/go.mod` so example-code failures no longer break product-module verification.
  - Added `internal/quality/repostructure/root_layout_test.go` to prevent Go source from drifting back into the repo root and to enforce the separate-module boundary for `playground/`.
  - Removed the tracked root-level `trainerd` binary and ignored it going forward.
  - Updated the top-level `README.md` and added `playground/README.md` so the new structure is explicit to contributors.

- 2026-03-25: Split GitHub test gating so pull requests run a fast `go test ./internal/... ./cmd/...` workflow while the full `./scripts/test-regression.sh` suite runs on `main`, nightly schedule, and manual dispatch.
  - Updated `.github/workflows/test-regression.yml` to remove the PR trigger and add nightly/manual entrypoints.
  - Added `.github/workflows/test-fast.yml` as the lightweight PR gate.
  - Updated `docs/runbooks/testing.md` to document the new CI split and when the full regression suite still applies.

## 2026-03-25 (Issue #425 Step Engine Extraction)

- Added a dedicated internal step-engine abstraction in `internal/harness/runner_step_engine.go` and reduced `Runner.runStepEngine(...)` in `internal/harness/runner.go` to a thin delegator.
- Preserved the existing step-loop behavior by moving the full provider/hook/tool/accounting/compaction/steering path intact instead of redesigning the contract.
- Added focused characterization coverage in `internal/harness/runner_step_engine_test.go` for the step-boundary steering contract on the second step.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestRunnerStepLoop_SteeringDrainBeforeTurnRequest|TestSteerRun_BasicInjection|TestSteerRun_MultipleMessages|TestStepStartedEventHasTimestamp' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -count=1`

## 2026-03-25 (Issue #426 Bootstrap Wiring)

- Extracted focused `harnessd` bootstrap helpers in `cmd/harnessd/bootstrap_helpers.go` for:
  - catalog/pricing/provider-registry bootstrap
  - cron bootstrap
  - persistence + conversation-cleaner bootstrap
  - trigger/webhook adapter bootstrap
  - HTTP server option assembly
- Slimmed `cmd/harnessd/main.go` so `runWithSignals(...)` delegates those wiring concerns instead of inlining each subsystem's setup.
- Added direct failing-first coverage in `cmd/harnessd/bootstrap_helpers_test.go` for:
  - workspace catalog fallback and model API lookup behavior
  - secret-driven trigger validator/adapter registration
  - server option forwarding of the extracted runtime dependencies
  - persistence bootstrap setup and failure cleanup behavior
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -run 'TestBuild(CatalogBootstrapFallsBackToWorkspaceCatalog|TriggerRuntimeHonorsSecrets|ServerOptionsForwardsBootstrapRuntime|PersistenceBootstrapInitializesStoresAndCleaner|PersistenceBootstrapClosesRunStoreWhenConversationSetupFails)' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -race -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build COVERPROFILE_PATH=$PWD/.tmp/issue-426-coverage.out ./scripts/test-regression.sh`
- Regression status:
  - `cmd/harnessd` package tests and race tests passed after the extraction.
  - The repo-wide regression script is blocked locally by unrelated existing transcript-export tests that attempt to write under `~/Library/Caches`, which this sandbox forbids:
    - `cmd/harnesscli/tui: TestExportCommandWritesOutsideWorkingDirectory`
    - `cmd/harnesscli/tui/components/transcriptexport: TestTUI059_ExportDefaultOutputDirCreatesFileOutsideWorkingDirectory`
  - The issue-`#426` change itself did not introduce a package-level failure outside that pre-existing sandbox-specific blocker.

## 2026-03-25 (Issue #422 Run Persistence Ownership)

- Added focused HTTP persistence-ownership regressions in `internal/server/http_persistence_ownership_test.go` to prove that:
  - `POST /v1/runs` persists exactly once when a shared store is configured
  - external-trigger `start` persists exactly once
  - external-trigger `continue` persists exactly once for the new run record
- Confirmed the red state first:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -run 'Test(PostRunPersistsExactlyOnce|ExternalTriggerStartPersistsExactlyOnce|ExternalTriggerContinuePersistsExactlyOnce)' -count=1`
  - failed because each new run hit `CreateRun` twice
- Consolidated ownership by removing duplicate transport-layer `CreateRun` calls from:
  - `internal/server/http.go`
  - `internal/server/http_external_trigger.go`
- Updated `internal/server/http_test.go` so the existing store-backed run test uses a shared runner/server store and reflects runner-owned persistence explicitly.
- Baseline observation before changes:
  - `go test ./...` still fails in `go-agent-harness/training-regex` (`TestQuest`, `TestAlt`, `TestGroup`, `TestAnchors`, `TestEmptyString`, `TestEdgeCases`), which is unrelated pre-existing test debt outside this issue’s scope.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -run 'Test(PostRunPersistsExactlyOnce|ExternalTriggerStartPersistsExactlyOnce|ExternalTriggerContinuePersistsExactlyOnce|HarnessRunToStore)' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness`

## 2026-03-25 (Issue #430 Allowed-Tools Fallback Integrity)

- Preserved `allowed_tools` restrictions on prompt-based fallback execution paths by adding an optional constrained runner entrypoint and using it in:
  - `internal/server/http_agents.go` for `/v1/agents` prompt execution and skill-lister fallback execution
  - `internal/harness/tools/skill.go` for flat-catalog fork fallback execution
  - `internal/harness/tools/core/skill.go` for core skill fork fallback execution
- Implemented `Runner.RunPromptWithAllowedTools(...)` in `internal/harness/runner.go` so fallback execution can start a plain sub-run while still forwarding `RunRequest.AllowedTools`.
- Added regression coverage for:
  - `/v1/agents` prompt path preserving `allowed_tools`
  - `/v1/agents` skill fallback preserving `allowed_tools`
  - flat skill fallback preserving `allowed_tools`
  - core skill fallback preserving `allowed_tools`
  - runner-level forwarding of `RunPromptWithAllowedTools(...)`
- Verification:
  - baseline before edits: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness ./internal/harness/tools ./internal/harness/tools/core`
  - failing-first regressions:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_SkillFallbackPreservesAllowedTools|TestFlatSkillForkBasicRunPromptPreservesAllowedTools|TestSkillTool_Handler_ForkWithBasicRunnerPreservesAllowedTools' -count=1`
  - focused green verification:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_(PromptPreservesAllowedTools|SkillFallbackPreservesAllowedTools)|TestFlatSkillForkBasicRunPromptPreservesAllowedTools|TestSkillTool_Handler_ForkWithBasicRunnerPreservesAllowedTools|TestRunPrompt(ReturnsOutput|WithAllowedTools_ForwardsAllowedTools|_RespectsContextCancellation)' -count=1`
  - relevant package verification:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness ./internal/harness/tools ./internal/harness/tools/core`
  - repo regression gate:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh`
    - local package-test phase passed cleanly
    - local race phase produced repeated macOS linker warnings (`malformed LC_DYSYMTAB`) and did not yield a clean final exit inside the tmux wrapper before handoff, so final mergeability will be confirmed from PR CI

## 2026-03-25 (Issue #427 HTTP Feature Decomposition)

- Extracted the run transport slice from [`internal/server/http.go`](../../internal/server/http.go) into [`internal/server/http_runs.go`](../../internal/server/http_runs.go):
  - route registration helper for `/v1/runs`
  - run collection dispatch and run-by-id dispatch
  - run creation/listing, run SSE/events, approval, input, continuation, context, compaction, and cancellation transport handlers
- Extracted the conversation transport slice from [`internal/server/http.go`](../../internal/server/http.go) into [`internal/server/http_conversations.go`](../../internal/server/http_conversations.go):
  - route registration helper for `/v1/conversations/`
  - conversation dispatch, search/export/compact/cleanup handlers
  - list/delete conversation handlers
- Kept `buildMux()` behavior-identical while replacing the inline route wiring for runs/conversations with small registration helpers so `http.go` reads more like server assembly than server implementation.
- Added a focused `internal/profiles/profile_test.go` regression for `ListProfileSummaries(...)` so the branch still satisfies the repo zero-coverage gate after the extraction.
- Verification:
  - baseline before extraction:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -count=1`
  - post-extraction:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -count=1`
  - persistence-regression guard after rebasing onto `main`:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -run 'TestPostRunPersistsExactlyOnce|TestExternalTriggerStartPersistsExactlyOnce|TestExternalTriggerContinuePersistsExactlyOnce' -count=1`
  - profile coverage regression:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/profiles -run TestListProfileSummariesDeduplicatesByTierPriority -count=1`
  - repo regression rerun:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh`
    - blocked by the pre-existing `internal/harness` race test `TestRecorderGoroutine_RaceWithConcurrentEmit`, which reproduces in isolation without any `internal/harness` changes in this PR

## 2026-03-25 (Issue #429 Forked Child-Run Failure Propagation)

- Reproduced the bug with new failing regressions on all three affected caller surfaces:
  - `internal/server/http_agents_test.go`
  - `internal/harness/tools/skill_test.go`
  - `internal/harness/tools/core/skill_test.go`
- Added `internal/harness/tools/fork_result.go` with a small shared helper so tool-layer callers can treat `ForkResult.Error` as terminal child-run failure information.
- Updated:
  - `internal/server/http_agents.go` so `/v1/agents` returns `execution_error` instead of HTTP 200 when a forked skill completes with `result.Error`.
  - `internal/harness/tools/skill.go` so flat-catalog forked skills do not emit `status: completed` for failed child runs.
  - `internal/harness/tools/core/skill.go` so core skill-tool fork execution follows the same failure contract.
- Verification:
  - baseline before changes:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core`
  - red phase:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_SkillForkResultErrorReturns500|TestFlatSkillForkForkedAgentRunnerResultError|TestSkillTool_Handler_ForkResultError'`
  - green phase:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server ./internal/harness/tools ./internal/harness/tools/core -run 'TestAgentsEndpoint_SkillForkResultErrorReturns500|TestFlatSkillForkForkedAgentRunnerResultError|TestSkillTool_Handler_ForkResultError'`
## 2026-03-25 (Issue #431 Startup Cleaner Cancellation)

- Reproduced the `go vet` startup-leak warning in `cmd/harnessd/main.go` where `convCleanerCancel` was only reached from the normal shutdown path after the conversation cleaner had already been started.
- Added a deterministic regression seam in `cmd/harnessd/main.go` so tests can supply a fake conversation cleaner without mutating package globals across parallel test runs.
- Added `TestStartupFailureCancelsConversationCleaner` in `cmd/harnessd/main_test.go`:
  - starts the cleaner
  - forces a startup failure with a bound port
  - asserts the cleaner context is cancelled before `runWithSignals(...)` returns
- Tightened `runWithSignals(...)` cleanup so the cleaner cancel function is always deferred once startup begins, which preserves the existing clean-shutdown path while also covering early startup exits.
- Followed up on the PR CI failure in `internal/training`:
  - the temporary Git repositories created in tests were still using Git's default branch name, while the regression helper and tests expect `main`
  - updated `initGitRepo(...)` to rename the freshly created branch to `main` after the initial commit so the regression suite behaves the same in CI, worktrees, and local runs
- Followed up on the repo-wide coverage gate exposed by CI:
  - added direct coverage for `newEmptyCommandRegistry()` in `cmd/harnesscli/tui`
  - added direct coverage for `tooluse.New(...)`
  - added direct coverage for `ListProfileSummaries()` tier precedence via explicit project/user dirs plus built-in fallback
- Verification:
  - `go test ./cmd/harnessd -run TestStartupFailureCancelsConversationCleaner -count=1`
  - `go test ./cmd/harnessd -count=1`
  - `go vet ./internal/... ./cmd/...`
  - `go test ./internal/training -count=1`
  - `go test ./cmd/harnesscli/tui -run TestNewEmptyCommandRegistryStartsEmpty -count=1`
  - `go test ./cmd/harnesscli/tui/components/tooluse -run TestNewInitializesIdentityFields -count=1`
- `go test ./internal/profiles -run TestListProfileSummariesPrefersHigherPriorityDirs -count=1`

## 2026-03-25 (Issue #421 Config Runtime Contract)

- Centralized `cmd/harnessd` runner wiring behind `buildRunnerConfig(...)` so merged `config.Config` is the authoritative source for projected runner behavior instead of scattered field assignment in `runWithSignals(...)`.
- Projected the full currently-supported `auto_compact` and `forensics` surfaces into `harness.RunnerConfig`, including:
  - `enabled`, `mode`, `threshold`, `keep_last`, `model_context_window`
  - `trace_tool_decisions`, `detect_anti_patterns`, `trace_hook_mutations`
  - `capture_request_envelope`, `snapshot_memory_snippet`
  - `error_chain_enabled`, `error_context_depth`, `capture_reasoning`
  - `cost_anomaly_detection_enabled`, `cost_anomaly_step_multiplier`
  - `audit_trail_enabled`, `context_window_snapshot_enabled`, `context_window_warning_threshold`, `causal_graph_enabled`, `rollout_dir`
- Preserved the existing runtime-only dependencies and behavior around prompt engine, ask-user broker, role models, MCP registry wiring, and the legacy rollout-dir env override by folding that override back into the resolved config before building `RunnerConfig`.
- Added failing-first regression coverage in `cmd/harnessd/main_test.go` for:
  - projection of all supported `auto_compact` and `forensics` fields
  - preservation of existing runtime dependencies when using the new builder seam
- Verification:
  - Baseline before edits: `go test ./cmd/harnessd ./internal/config`
  - Red first: `go test ./cmd/harnessd -run 'TestBuildRunnerConfig(Project|Preserves)' -count=1`
  - Green after fix:
    - `go test ./cmd/harnessd -run 'TestBuildRunnerConfig(Project|Preserves)' -count=1`
    - `go test ./cmd/harnessd -count=1`
    - `go test ./internal/config -count=1`
  - Repo regression gate: `./scripts/test-regression.sh` launched in `tmux` (`issue421-regression`); final status recorded after completion.
  - `go test ./internal/profiles -run TestListProfileSummariesPrefersHigherPriorityDirs -count=1`

## 2026-03-25 (Issue #428 Timed-Out Subrun Cancellation)

- Reproduced the subrun cancellation leak in `internal/harness/runner.go`: `waitForTerminalResult(...)` returned on parent `ctx.Done()` without cancelling the spawned child run, leaving it in `running` status.
- Added regression coverage in:
  - `internal/harness/runner_orchestration_test.go`
    - `TestRunPrompt_CancelsChildRunOnContextCancellation`
    - `TestRunForkedSkill_CancelsChildRunOnContextCancellation`
  - `internal/server/http_agents_test.go`
    - `TestAgentsEndpoint_TimeoutCancelsSpawnedRun`
- Implemented a minimal runner fix:
  - `waitForTerminalResult(...)` now checks whether the child run already reached a terminal state before treating parent cancellation as authoritative.
  - if the child run is still active when the parent context ends, the runner now calls `CancelRun(runID)` before returning the parent cancellation error.
- Verification:
  - baseline before changes: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server`
  - red step: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestRunPrompt_CancelsChildRunOnContextCancellation|TestRunForkedSkill_CancelsChildRunOnContextCancellation'`
  - green focused step: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server -run 'TestRunPrompt_CancelsChildRunOnContextCancellation|TestRunForkedSkill_CancelsChildRunOnContextCancellation|TestAgentsEndpoint_Timeout(Exceeded_Returns408|CancelsSpawnedRun)'`
  - package verification: `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness ./internal/server`
## 2026-03-25 (Issue #423 Runner Preflight Extraction)

- Extracted the `Runner.execute()` setup path into a focused `runPreflight(...)` helper in [`internal/harness/runner.go`](../../internal/harness/runner.go):
  - profile-driven workspace isolation fallback
  - workspace provisioning and cleanup registration
  - workspace-path system-prompt re-resolution
  - provider/model setup and prompt events
  - conversation preloading and per-run MCP registry setup
- Added direct seam-level regression coverage in [`internal/harness/runner_preflight_test.go`](../../internal/harness/runner_preflight_test.go) for:
  - profile isolation fallback when `workspace_type` is unset
  - `workspace.provision_failed` emission on provisioning errors
  - prompt re-resolution against the provisioned workspace path
  - per-run scoped MCP registry creation
- Updated the plan/intent trail for the issue:
  - [`docs/plans/2026-03-25-issue-423-runner-preflight-plan.md`](../../docs/plans/2026-03-25-issue-423-runner-preflight-plan.md)
  - [`docs/plans/active-plan.md`](../../docs/plans/active-plan.md)
  - [`docs/plans/INDEX.md`](../../docs/plans/INDEX.md)
  - [`docs/logs/long-term-thinking-log.md`](../../docs/logs/long-term-thinking-log.md)
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestRunPreflight_' -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -race -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -race -run TestWorkerPool_QueuedTransitionsToRunning -count=5`
- Regression status:
  - the repo-wide `./scripts/test-regression.sh` run reached the multi-package race phase cleanly but hit a timeout in `TestWorkerPool_QueuedTransitionsToRunning` during the full-package `go test ./internal/... ./cmd/... -race` invocation.
  - that worker-pool race timeout did not reproduce in isolated reruns, so it currently looks like an unrelated pre-existing/full-suite flake rather than a deterministic issue-`#423` regression.

## 2026-03-25 (Issue #424 Event Journal Extraction)

- Extracted the runner event append/store/recorder path into a focused internal helper in `internal/harness/runner_event_journal.go`.
- Kept `Runner.emit()` as the orchestration wrapper while moving payload enrichment, terminal sealing, redaction handling, recorder capture, and event construction behind the new helper boundary.
- Added direct regression coverage in `internal/harness/runner_event_journal_test.go` for the terminal-ordering contract:
  - terminal events must be appended to the store before subscribers observe them as durable.
- Preserved the existing send-under-lock behavior for non-terminal subscriber fanout so the extraction stays race-clean with concurrent `Subscribe(...)/cancel()` behavior.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test -race ./internal/harness -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build COVERPROFILE_PATH=$PWD/.tmp/issue-424-coverage.out ./scripts/test-regression.sh` launched in `tmux` as `issue-424-regression`; the package-test phase passed and the repo-wide race phase advanced deep into `internal/...` and `cmd/...`, but the local sandbox run stopped making visible progress after repeated macOS linker warnings (`malformed LC_DYSYMTAB`). Final mergeability should be confirmed from PR CI.

## 2026-03-25 (Harness Review Bug Tickets)

- Reviewed the harness runtime and transport paths with focus on cancellation propagation, forked-run failure reporting, tool-allowlist integrity, and bootstrap cleanup.
- Created four bug issues with implementation-ready agent prompts, explicit TDD requirements, and regression-test expectations:
  - `#428` Cancel timed-out subruns instead of leaving them running
  - `#429` Propagate forked child-run failures instead of reporting success
  - `#430` Preserve `allowed_tools` restrictions on agent and skill fallback paths
  - `#431` Close the conversation cleaner on `harnessd` startup failures
- Verification:
  - `gh issue create` created issues `#428` through `#431`
  - no runtime code changed in this pass

## 2026-03-25 (Issue #428 Timed-Out Subrun Cancellation)

- Claimed GitHub issue `#428` in a dedicated worktree branch: `codex/issue-428-subrun-cancel`.
- Confirmed the current wait path in `internal/harness/runner.go` returns the parent context error from `waitForTerminalResult(...)` without calling `CancelRun(runID)`, which matches the reported leak risk.
- Baseline verification before changes:
  - `GOCACHE=$PWD/.tmp/go-build TMPDIR=$PWD/.tmp/tmp go test ./internal/harness -run 'TestRunPrompt_RespectsContextCancellation|TestRunForkedSkill_ReturnsFailedForkResult|TestWaitForTerminalResult_(UsesTerminalHistory|ReturnsOnStreamClose)' -count=1`
  - `GOCACHE=$PWD/.tmp/go-build TMPDIR=$PWD/.tmp/tmp go test ./internal/server -run 'TestAgentsEndpoint_TimeoutExceeded_Returns408' -count=1`
- Next step: add failing regression tests for child-run cancellation on parent timeout/cancellation before implementing the minimal runner fix.

## 2026-03-25 (Architecture Review Backlog)

- Reviewed the harness architecture with focus on config authority, persistence ownership, and monolithic orchestration boundaries.
- Converted the review into a dependency-ordered GitHub issue stack with TDD-first implementation guidance and explicit regression-test expectations:
  - `#421` Make merged harness config the authoritative runtime contract
  - `#422` Consolidate run persistence ownership into the runner boundary
  - `#423` Extract runner preflight orchestration from `execute()`
  - `#424` Extract runner event journal and sink path from `runner.go`
  - `#425` Extract the core step engine from the runner monolith
  - `#426` Split `harnessd` bootstrap into modular app wiring
  - `#427` Continue decomposing `internal/server/http.go` by feature
- Execution order captured in the issue bodies:
  - Start with config contract and persistence ownership so runtime boundaries are explicit.
  - Then split the runner monolith in slices: preflight, event journal, step engine.
  - Run `harnessd` bootstrap decomposition and `internal/server` transport decomposition alongside or after the runner work as dependencies allow.
- Verification:
  - Created GitHub issues `#421` through `#427`
  - No runtime code changed in this pass

## 2026-03-25 (Backend OpenRouter Discovery)

- Added additive backend discovery support in `internal/provider/catalog/discovery.go`:
  - live OpenRouter fetch from `https://openrouter.ai/api/v1/models`
  - in-memory TTL caching
  - stale-cache fallback when a refresh fails
- Extended `internal/provider/catalog/registry.go` so runtime provider resolution and merged model listing can use cached live OpenRouter data while preserving static catalog metadata as the overlay authority.
- Updated `internal/server/http.go` so `GET /v1/models` serializes the merged registry view when a provider registry is configured.
- Wired `cmd/harnessd/main.go` to enable OpenRouter discovery automatically when the loaded model catalog includes an `openrouter` provider, without making startup perform a live fetch.
- Added focused regression coverage in:
  - `internal/provider/catalog/discovery_test.go`
  - `internal/provider/catalog/discovery_registry_test.go`
  - `internal/server/http_models_test.go`
  - updated `internal/harness/runner_test.go`
- Verification:
  - `go test ./internal/provider/catalog ./internal/server ./internal/harness ./cmd/harnessd`

## 2026-03-18 (Issue #316 Context Grid Coverage)

- Added direct package coverage for `cmd/harnesscli/tui/components/contextgrid` in `cmd/harnesscli/tui/components/contextgrid/model_test.go`:
  - default total-token fallback when `TotalTokens <= 0`
  - used-token clamping for negative and over-limit values
  - width fallback / max-width bar sizing
  - rendered header, counts, percentage text, and bar glyph assertions
- Tightened `cmd/harnesscli/tui/components/contextgrid/model.go` so the progress bar fits within the requested width after accounting for the surrounding brackets:
  - `barWidth` now uses `width - 2`
  - narrow widths clamp to at least one cell instead of forcing a 10-cell overflow
- Verification:
  - `TMPDIR=$PWD/.tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui/components/contextgrid`
  - `TMPDIR=$PWD/.tmp GOCACHE=$PWD/.tmp/go-build go test -cover ./cmd/harnesscli/tui/components/contextgrid`
- Regression status:
  - package coverage for `cmd/harnesscli/tui/components/contextgrid` is now `93.1%`
  - full `./scripts/test-regression.sh` is blocked in this sandbox because many existing tests cannot bind local ports (`httptest.NewServer`, `listen tcp :0`, `127.0.0.1:0`) under the current environment; the failures are not isolated to the context-grid package

## 2026-03-18 (Issue #332 Runner Orchestration Coverage)

- Added direct orchestration regression tests in `internal/harness/runner_orchestration_test.go` for:
  - `SubmitInput` mapping broker validation failures to `ErrInvalidRunInput`
  - `SubmitInput` mapping missing pending-question submissions to `ErrNoPendingInput`
  - terminal-history and stream-closure wait semantics
  - failed `RunForkedSkill` terminal result mapping
- Refactored the shared wait logic in `internal/harness/runner.go` into `waitForTerminalResult(...)` so `RunPrompt` and `RunForkedSkill` keep the same behavior while the history/stream branches become directly testable.
- Verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness -run 'TestSubmitInput_MapsBrokerValidationFailure|TestSubmitInput_MapsMissingPendingQuestion|TestWaitForTerminalResult_UsesTerminalHistory|TestWaitForTerminalResult_ReturnsOnStreamClose|TestRunForkedSkill_ReturnsFailedForkResult|TestRunPrompt_ReturnsOutput|TestRunPrompt_RespectsContextCancellation'`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/harness`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build ./scripts/test-regression.sh`
- Regression status:
  - targeted harness tests and full `internal/harness` package tests passed.
  - the repo-wide regression script failed for unrelated environment/sandbox reasons: multiple packages panic or error when `httptest.NewServer`, `net.Listen`, or `listen tcp 127.0.0.1:0` attempt to bind a localhost port in this sandbox (examples include `internal/cron`, `internal/mcp`, `internal/observationalmemory`, `internal/server`, `cmd/harnesscli`, `cmd/harnesscli/tui`, `cmd/harnessd`, `cmd/cronsd`, and `internal/workspace`).
  - no issue-`#332` failure remained in the harness package after the new tests/refactor landed.

## 2026-03-18 (Ownership And Copy-Semantics Hardening)

- Added an explicit clone contract for mutable exported/state-storing harness types:
  - `internal/harness/types.go`
    - `ToolDefinition.Clone()` now deep-copies schema maps.
    - existing `Message.Clone()` remains the owner of `ToolCalls` copy semantics.
  - `internal/harness/clone.go`
    - centralized deep-copy helpers for payload maps, string slices, and message slices with preserved nil semantics.
- Hardened registry ownership boundaries in `internal/harness/registry.go`:
  - clone tool definitions on registration
  - clone definitions on `Definitions()`, `DefinitionsForRun()`, and `DeferredDefinitions()`
  - deep-copy MCP-discovered tool schemas before storing them
- Normalized remaining runner message snapshot reads onto `copyMessages(...)` in `internal/harness/runner.go` so internal readers stop using ad hoc shallow slice copies.
- Fixed nil/empty conversation semantics in `internal/harness/runner.go`:
  - persisted empty conversations are now distinguishable from missing conversations via store owner lookup
  - `copyMessages(...)` preserves non-nil empty slices instead of collapsing them to `nil`
- Added TDD coverage in `internal/harness/registry_test.go` for:
  - caller mutation after `Register(...)`
  - returned-definition mutation after `Definitions()` / `DefinitionsForRun()`
  - `ToolDefinition.Clone()` nil semantics
- Added the reusable checklist runbook and wired it into the planning flow:
  - `docs/runbooks/ownership-copy-semantics.md`
  - `docs/runbooks/INDEX.md`
  - `docs/plans/PLAN_TEMPLATE.md`
  - `docs/runbooks/worktree-flow.md`
- While running the repo regression gate, fixed two unrelated pre-existing blockers so the gate got further:
  - `cmd/harnesscli/tui/components/statspanel/model.go` plus three golden snapshots now anchor snapshot rendering to the latest fixture date instead of wall-clock time
  - `internal/subagents/manager.go` now synchronizes worktree auto-cleanup so `Get()` no longer races or reports cleanup complete before the filesystem destroy finishes
- Validation:
  - `go test ./internal/harness ./internal/subagents ./cmd/harnesscli/tui/components/statspanel`
  - `go test ./internal/subagents -run 'TestManagerCreateWorktreeSubagent(DestroyOnSuccess|Preserve)' -race`
  - `./scripts/test-regression.sh` executed via `tmux`
- Regression status:
  - repo-wide regression script still exits non-zero because the existing coverage gate reports many zero-coverage functions in unrelated packages (for example `cmd/forensics/main.go:18`, `cmd/harnesscli/main.go:347`, `cmd/harnesscli/tui/api.go:99`, `internal/config/config.go:511`, `internal/provider/openai/client.go:749`, `internal/subagents/manager.go:164`)
  - no new repo-wide behavioral test failure remained after the `statspanel` and `subagents` fixes above

## 2026-03-18 (Runner Concurrency Invariants)

- Made the runner's concurrency/lifecycle invariants explicit in `internal/harness/runner.go`:
  - `emit()` owns canonical event ordering.
  - `state.messages` is the single source of truth for run context.
  - payload ownership must stay isolated across caller/history/subscriber/recorder boundaries.
- Strengthened recorder behavior in `internal/harness/runner.go`:
  - `startRecorderGoroutine()` now buffers out-of-order arrivals and flushes JSONL in `Seq` order.
  - `recorder.drop_detected` markers now carry the dropped event's `Seq`, keeping the ledger position explicit if a drop is surfaced.
- Added invariant-focused regression coverage in `internal/harness/runner_forensics_test.go`:
  - `TestEventLedgerInvariant_JSONLMatchesInMemoryHistory`
- Reframed existing compaction tests in `internal/harness/runner_context_compact_test.go` around the `state.messages` source-of-truth contract.
- Verification:
  - `GOCACHE=/tmp/go-build-cache go test ./internal/harness -run 'TestEventLedgerInvariant_JSONLMatchesInMemoryHistory|TestCompactRunSurvivesConcurrentExecute|TestCompactRunAtStepBoundary|TestMessageExportMutationIsolation|TestAccountingStructPointerFieldIsolation'`
  - `GOCACHE=/tmp/go-build-cache go test -race ./internal/harness -run 'TestEventLedgerInvariant_JSONLMatchesInMemoryHistory|TestCompactRunSurvivesConcurrentExecute|TestCompactRunAtStepBoundary|TestMessageExportMutationIsolation|TestAccountingStructPointerFieldIsolation'`
  - Full repo regression suite not run in this pass.

## 2026-03-18 (Provider/Model Impact Map Guardrail)

- Added a new one-page planning artifact for provider/model flow work:
  - `docs/plans/IMPACT_MAP_TEMPLATE.md`
  - Requires explicit sections for config, server API, TUI state, and regression tests.
  - Makes blank headings an explicit warning; unaffected surfaces must be documented as `None` with rationale.
- Added a focused runbook:
  - `docs/runbooks/provider-model-impact-mapping.md`
  - Defines when the impact map is required and how to use it before implementation starts.
- Updated workflow entry points to surface the requirement early:
  - `AGENTS.md`
  - `docs/context/critical-context.md`
  - `docs/plans/PLAN_TEMPLATE.md`
  - `docs/runbooks/worktree-flow.md`
- Updated planning metadata:
  - `docs/plans/2026-03-18-provider-model-impact-map-guardrail-plan.md`
  - `docs/plans/active-plan.md`
  - `docs/plans/INDEX.md`
  - `docs/runbooks/INDEX.md`
- Verification:
  - Planned as doc cross-reference verification in this pass; no runtime code changed.

## 2026-03-06 (Issue #18 Head-Tail Buffer for Long Command Output)

- Added bounded head-tail output capture in `internal/harness/tools/head_tail_buffer.go`:
  - concurrency-safe writer that stores leading and trailing output bytes
  - explicit middle omission marker: `...[truncated output]...`
- Integrated bounded capture in command execution paths:
  - `internal/harness/tools/bash_manager.go` for foreground `bash` and background jobs (`job_output`)
  - `internal/harness/tools/common_exec.go` so command-backed helper tools also avoid unbounded output buffering
- TDD evidence (failing first, then green):
  - failing first: `GOCACHE=/tmp/go-build-cache go test ./internal/harness/tools -run TestJobManagerOutputHeadTailBuffer` (compile failure before implementation: missing `maxOutputBytes`)
  - passing after implementation:
    - `GOCACHE=/tmp/go-build-cache go test ./internal/harness/tools -run TestJobManagerOutputHeadTailBuffer`
    - `GOCACHE=/tmp/go-build-cache go test ./internal/harness -run TestBashToolOutputUsesHeadTailBuffer`
- Full regression gate:
  - executed via tmux: `GOCACHE=/tmp/go-build-cache ./scripts/test-regression.sh`
  - failed due unrelated pre-existing repo issues:
    - `cmd/harnesscli/main_prompt_test.go` references undefined `httpClient`
    - existing harness test failure: `TestApplyPatchToolAcceptsUnifiedPatchPayload`
- Commit/merge status:
  - blocked by required full regression gate failure (no commit/merge performed).

## 2026-03-05 (Provider Token Streaming)

- Added incremental provider-to-runner streaming contract in `internal/harness/types.go` via `CompletionRequest.Stream` and `CompletionDelta`.
- Updated runner execution to emit live SSE-visible delta events before turn completion:
  - `assistant.message.delta`
  - `tool.call.delta`
- Implemented OpenAI streaming chat completions assembly in `internal/provider/openai/client.go`:
  - sends `stream: true`
  - requests streamed usage via `stream_options.include_usage`
  - assembles assistant text and tool calls from chunked deltas
- Added TDD coverage:
  - streamed assistant/tool-call assembly in `internal/provider/openai/client_test.go`
  - runner delta event emission in `internal/harness/runner_test.go`
- Validation:
  - `go test ./internal/provider/openai` passed
  - targeted runner tests in `go test ./internal/harness -run 'TestRunner(EmitsAssistantMessageDeltaEvents|EmitsToolCallDeltaEventsBeforeExecution|ExecutesToolCallsAndPublishesEvents|FailsWhenProviderErrors|EmitsUsageDeltaAndPersistsTotals|FailedRunIncludesPartialUsageTotals)'` passed
- Note: full `go test ./internal/harness` is currently blocked by an unrelated existing failure in `TestApplyPatchToolAcceptsUnifiedPatchPayload`.

## 2026-03-05

### Architecture Decision: REST over GraphQL

**Decision**: Stick with REST for all API endpoints. Do not adopt GraphQL.

**Rationale**:
- The API is command-and-control for orchestrating agent runs, not a complex query interface
- Current surface is 6 endpoints with clean REST sub-resource patterns (`/runs/{id}/events`, `/runs/{id}/input`)
- SSE for event streaming is REST-native; GraphQL subscriptions (WebSocket-based) would add complexity for no benefit
- New endpoints (`/steer`, `/continue`) are imperative actions, not data mutations — REST verbs express this naturally
- Go stdlib makes REST trivial; GraphQL requires schema/codegen layer (gqlgen etc.) that's overkill here
- No client needs complex field selection, cross-resource queries, or varied data shapes

**When to revisit**: If a dashboard or analytics layer needs to query across many runs with filters, pagination, and field selection — a read-heavy client with varied data needs. That would be a separate read API, not a replacement for the core run orchestration API.

### Issues Created

- [#1](https://github.com/dennisonbertram/go-agent-harness/issues/1) — Stream tool output incrementally during execution
- [#2](https://github.com/dennisonbertram/go-agent-harness/issues/2) — Audit SSE events for completeness and consistency
- [#3](https://github.com/dennisonbertram/go-agent-harness/issues/3) — Make max steps tunable per-run, default to unlimited
- [#4](https://github.com/dennisonbertram/go-agent-harness/issues/4) — Implement deferred (lazy-loaded) tools via ToolSearch meta-tool
- [#5](https://github.com/dennisonbertram/go-agent-harness/issues/5) — Add run continuation for multi-turn conversations
- [#6](https://github.com/dennisonbertram/go-agent-harness/issues/6) — Add mid-run steering for user guidance during execution

### Architecture Direction: Platform Backend (CLI + GUI)

Established that the harness is a **Go backend platform** supporting multiple frontends (CLI, web GUI, desktop app). Must work transparently in both local and remote modes — remote execution should feel like local, and vice versa.

Key architectural pieces identified:
- **Persistence layer** (#7) — foundational, everything else depends on it
- **Workspace abstraction** (#8) — transparent local/remote via `Workspace` interface + optional proxy agent on user's machine
- **Client auth** (#9) — API keys, tenant isolation, scoped permissions
- **Cost/safety controls** (#10) — cost ceilings, idle detection, spending limits (critical once max steps goes unlimited)
- **Multi-provider** (#11) — Anthropic alongside OpenAI, auto-detect from model name, prompt caching

### Codex CLI Architecture Study

Researched OpenAI Codex CLI (Rust, 65+ crates, Apache-2.0) for architectural patterns. Findings in `docs/research/codex-cli-architecture.md`. Created issues for the most impactful patterns:

- [#15](https://github.com/dennisonbertram/go-agent-harness/issues/15) — Two-axis permission model (sandbox × approval policy)
- [#16](https://github.com/dennisonbertram/go-agent-harness/issues/16) — JSONL rollout recorder for replay/fork/audit
- [#17](https://github.com/dennisonbertram/go-agent-harness/issues/17) — Conversation compaction for unlimited-step sessions
- [#18](https://github.com/dennisonbertram/go-agent-harness/issues/18) — Head-tail buffer for long process output
- [#19](https://github.com/dennisonbertram/go-agent-harness/issues/19) — Bidirectional MCP (client + server)
- [#20](https://github.com/dennisonbertram/go-agent-harness/issues/20) — Layered configuration cascade with cloud/team overrides

Skipped creating separate issues for Op/EventMsg protocol (already covered by SSE event audit #2 and the existing architecture) and Codex's skills/memories system (observational memory already covers this).

### Research

- Deferred tools design doc written to `docs/research/deferred-tools-design.md` — covers Claude Code's ToolSearch pattern, Go implementation strategy, token savings analysis (40-60%), and comparison of alternatives (intent filtering, tiered packs, description compression, dynamic pruning). Recommended approach: ToolSearch + tiered packs.

## 2026-03-04

- Initialized repository scaffold.
- Added operating policy (`AGENTS.md`) with strict TDD, worktree-first, and pre-commit testing requirements.
- Created docs structure with indexes, logs, context, plans, and runbooks.
- Added merge helper script: `scripts/verify-and-merge.sh`.
- Refactored `AGENTS.md` into a bootstrap reference map for faster onboarding.
- Added long-term thinking log (`docs/logs/long-term-thinking-log.md`) with command-intent and user-intent precedence.
- Added UX requirements doc (`docs/design/ux-requirements.md`).
- Added completed bootstrap plan/checklist (`docs/plans/2026-03-04-repo-bootstrap-plan.md`).
- Updated merge workflow to auto-push `main` in `scripts/verify-and-merge.sh`.
- Updated worktree runbook and AGENTS guidance to reflect process-guided enforcement (no hard gating yet).
- Added explicit response-clarity policy requiring `Task status: DONE` / `Task status: NOT DONE`.
- Updated agent completion and nightly-task docs to require status-first reporting.

## 2026-03-04 (OpenAI Harness POC)

- Added Go module and executable service entrypoint: `cmd/harnessd/main.go`.
- Implemented core harness runtime in `internal/harness/`:
  - Deterministic run loop with bounded steps.
  - Event history + live subscriber fanout.
  - In-memory run state with status/output/error tracking.
  - Tool registry with schema metadata and execution dispatch.
- Added default proof-of-concept tools:
  - `list_files` (workspace-scoped listing, recursive/non-recursive).
  - `read_file` (workspace-scoped reads with byte limit + truncation flag).
  - `run_go_test` (bounded timeout + restricted package pattern).
- Implemented OpenAI provider adapter in `internal/provider/openai/client.go` against `/v1/chat/completions` with function-tool schema mapping and tool-call parsing.
- Implemented HTTP server in `internal/server/http.go`:
  - `POST /v1/runs`
  - `GET /v1/runs/{runID}`
  - `GET /v1/runs/{runID}/events` (SSE)
  - `GET /healthz`
- Added tests first, then implemented to green:
  - `internal/harness/runner_test.go`
  - `internal/harness/tools_test.go`
  - `internal/provider/openai/client_test.go`
  - `internal/server/http_test.go`
- Updated README with setup, API contract, event taxonomy, and quick-start usage.

## 2026-03-04 (Toolset Update: read/write/edit/bash)

- Replaced default harness tool registrations in `internal/harness/tools_default.go`:
  - Removed `list_files`, `read_file`, `run_go_test`.
  - Added `read`, `write`, `edit`, `bash`.
- Implemented `write` with create/overwrite/append support and parent directory creation.
- Implemented `edit` with single/replace-all text replacement and explicit error when `old_text` is not found.
- Implemented `bash` command execution with timeout, workspace working directory confinement, and deny-list guardrails for dangerous commands.
- Rewrote `internal/harness/tools_test.go` with failing-first assertions for new tools and safety constraints.
- Ran full suite to confirm no behavior regressions outside toolset update.

## 2026-03-04 (Function Coverage Expansion)

- Added `cmd/harnessd/main_test.go` to cover entrypoint logic and env helpers:
  - `main` success/error exit behavior (via test hooks).
  - `run` delegation behavior.
  - `runWithSignals` missing key, provider failure, and graceful shutdown.
  - `getenvOrDefault` and `getenvIntOrDefault`.
- Refactored `cmd/harnessd/main.go` for testability while preserving runtime behavior:
  - Introduced `runMain`, `exitFunc`, and `runWithSignalsFunc` hooks.
  - Converted fatal exits in internal flow to returned errors handled in `main`.
- Expanded `internal/harness/runner_test.go` with failure-path coverage:
  - Provider error run failure path.
  - `failRun(nil)` default error path.
  - `mustJSON` marshal-failure fallback.
- Expanded `internal/server/http_test.go` with handler error/edge coverage:
  - `GET /healthz`.
  - method-not-allowed checks.
  - invalid JSON handling.
  - not-found run and event stream paths.
- Coverage verification:
  - `go test ./... -coverprofile=coverage.out`
  - `go tool cover -func=coverage.out`
  - Total statement coverage now `81.0%`.
  - All functions report non-zero coverage.

## 2026-03-05 (Regression Guardrails Automation)

- Added coverage-gate library and tests:
  - `internal/quality/coveragegate/gate.go`
  - `internal/quality/coveragegate/gate_test.go`
- Added coverage-gate CLI and tests:
  - `cmd/coveragegate/main.go`
  - `cmd/coveragegate/main_test.go`
- Added regression contract test for default tool interface:
  - `internal/harness/tools_contract_test.go` (asserts `bash`, `edit`, `read`, `write` contract).
- Added automated regression script:
  - `scripts/test-regression.sh`
  - Runs `go test`, `go test -race`, coverage profile generation, and coverage gate checks.
- Added CI workflow:
  - `.github/workflows/test-regression.yml`
  - Executes regression script on `pull_request` and `push` to `main`.
- Updated testing and worktree runbooks + README development commands to use regression script as default quality gate.
- Verified full regression suite passes locally with coverage gate result:
  - `coveragegate: PASS (total=81.1%, min=80.0%, zero-functions=0)`.

## 2026-03-05 (Hooks + Baseline Tools Expansion)

- Added hook contracts and runner integration in `internal/harness`:
  - New hook types/interfaces in `types.go` (`PreMessageHook`, `PostMessageHook`, `HookAction`, `HookFailureMode`).
  - Runner hook pipeline in `runner.go`:
    - Pre-message hooks executed before provider call.
    - Post-message hooks executed after provider call.
    - Hook events emitted: `hook.started`, `hook.completed`, `hook.failed`.
    - Blocking and mutation semantics with fail-open/fail-closed modes.
- Added hook-focused tests in `internal/harness/hooks_test.go`:
  - Mutation, blocking, fail-open, and fail-closed behavior for pre and post hooks.
- Expanded default toolset in `internal/harness/tools_default.go`:
  - Added baseline tools:
    - `ls`
    - `glob`
    - `grep`
    - `apply_patch`
    - `git_status`
    - `git_diff`
  - Kept existing tools:
    - `read`, `write`, `edit`, `bash`
- Expanded tool tests in `internal/harness/tools_test.go`:
  - New baseline tool behavior and validation/error branches.
  - Additional branch coverage for helper functions and command execution paths.
- Updated default tool contract test in `internal/harness/tools_contract_test.go`.
- Updated README to document hooks and expanded tool list.
- Validation:
  - `go test ./...` passed.
  - `./scripts/test-regression.sh` passed.
  - Coverage gate after changes: `PASS (total=80.8%, min=80.0%, zero-functions=0)`.
- Live OpenAI verification (local key, `gpt-5-nano`, tmux-hosted harness):
  - Confirmed successful run with `run.completed`.
  - Observed tool calls for `ls`, `write`, `apply_patch`, `grep`, `git_status`, `git_diff` in event stream.

## 2026-03-05 (Sample CLI Test Client)

- Added a new CLI client in `cmd/harnesscli/main.go` to test harness connectivity quickly from terminal.
- Implemented CLI flow:
  - Parse flags (`-base-url`, `-prompt`, `-model`, `-system-prompt`).
  - Create run via `POST /v1/runs`.
  - Stream and print lifecycle events from `GET /v1/runs/{id}/events`.
  - Stop on terminal events (`run.completed`, `run.failed`) with explicit terminal summary output.
- Added full TDD coverage in `cmd/harnesscli/main_test.go`:
  - `main` exit delegation.
  - Create-run payload contract validation.
  - SSE block parsing + event decode + terminal detection.
  - End-to-end CLI success path.
  - Non-2xx create/stream regression paths.
  - Invalid SSE data handling path.
- Validation:
  - `go test ./cmd/harnesscli`
  - `go test ./...`
  - `./scripts/test-regression.sh` (pass, coverage gate pass)
- Live OpenAI verification (local key, `gpt-5-nano`, tmux-hosted harness):
  - Ran CLI end-to-end with prompt to create `demo/live-cli-smoke.html`.
  - Observed real `bash`, `write`, and `ls` tool calls in stream.
  - Completed with `terminal_event=run.completed`.
- Added operator documentation:
  - `docs/runbooks/harnesscli-live-testing.md`
  - Includes tmux commands, variable map, expected outputs, known live-run issues, and troubleshooting.

## Entry Template

- Date:
- Task:
- Change summary:
- Tests added/updated:
- Bugs fixed:
- Regression tests added:
- Docs updated:

## 2026-03-05 (Modular Tooling Migration + Crush-Informed Expansion)

- Refactored tool implementation into modular package: `internal/harness/tools/`.
  - Added catalog-driven registration (`catalog.go`) and common shared utilities (`common_paths.go`, `common_exec.go`, `common_result.go`, `policy.go`).
  - Migrated and modularized existing tools (`read`, `write`, `edit`, `bash`, `ls`, `glob`, `grep`, `apply_patch`, `git_status`, `git_diff`).
- Added Phase 1/2/3 tool contracts and implementations with dependency-gated registration:
  - `job_output`, `job_kill`
  - `fetch`, `download`
  - `todos`
  - `lsp_diagnostics`, `lsp_references`, `lsp_restart`
  - `sourcegraph` (registered when endpoint configured)
  - `list_mcp_resources`, `read_mcp_resource`, dynamic `mcp_<server>_<tool>` (registered when MCP registry provided)
  - `agent`, `agentic_fetch`, `web_search`, `web_fetch` (registered when integrations provided)
- Added approval-mode seam and compatibility wiring:
  - New harness types for `ToolApprovalMode`, `ToolPolicy`, policy input/output.
  - Added `HARNESS_TOOL_APPROVAL_MODE` env wiring in `cmd/harnessd/main.go`.
  - Added `NewDefaultRegistryWithPolicy(...)` while preserving `NewDefaultRegistry(...)` compatibility.
- Updated runner tool execution context to include run ID for run-scoped tools (used by `todos`).
- Expanded test coverage heavily for modular package and compatibility wrappers:
  - `internal/harness/tools/catalog_test.go`
  - `internal/harness/tools/coverage_boost_test.go`
  - `internal/harness/tools/coverage_extra_test.go`
  - `internal/harness/tools_default_test.go`
  - Updated `internal/harness/tools_contract_test.go` expected tool surface.
  - Updated `cmd/harnessd/main_test.go` for approval-mode env parser.
- Fixed live OpenAI schema issue discovered during tmux smoke test:
  - Added explicit `items` schemas for array properties in `apply_patch.edits` and `todos.todos`.
- Validation:
  - `go test ./...` passed.
  - `./scripts/test-regression.sh` passed.
  - Coverage gate after migration: `PASS (total=80.0%, min=80.0%, zero-functions=0)`.
- Live OpenAI verification (tmux-hosted harness + `gpt-5-nano`):
  - Confirmed `run.completed` with real tool usage (`ls`, `write`, `read`) and generated file verification.

## 2026-03-05 (Claude-Compatible AskUserQuestion Tool)

- Added a new first-class `AskUserQuestion` tool in `internal/harness/tools/ask_user_question.go` with Claude-compatible schema and result payload (`questions` + `answers`).
- Added tool-side validation and answer normalization helpers:
  - 1-4 questions, 2-4 options per question.
  - required `question/header/options/multiSelect` fields.
  - unique question text and option labels.
  - multi-select answer normalization to comma-separated labels.
- Added broker interfaces and context helpers in `internal/harness/tools/types.go`:
  - `AskUserQuestionBroker`, `AskUserQuestionRequest`, `AskUserQuestionPending`.
  - `ContextKeyToolCallID` / `ToolCallIDFromContext`.
- Added in-memory broker implementation in `internal/harness/ask_user_broker.go`:
  - one pending question per run.
  - blocking wait in `Ask`.
  - typed timeout error path.
  - submission validation with invalid-input preservation.
- Updated tool catalog/default registry wiring:
  - `AskUserQuestion` now registers in default toolset.
  - new registry options support broker + timeout injection.
- Updated runner behavior:
  - new status `waiting_for_user`.
  - emits `run.waiting_for_user` and `run.resumed` events.
  - fails run immediately on typed AskUserQuestion timeout.
  - adds tool call id into tool execution context.
  - new runner methods for input API: `PendingInput` and `SubmitInput`.
- Updated HTTP server API in `internal/server/http.go`:
  - `GET /v1/runs/{runID}/input`
  - `POST /v1/runs/{runID}/input`
  - error contracts: `404` missing run, `409` no pending input, `400` invalid JSON/request.
- Updated runtime wiring in `cmd/harnessd/main.go`:
  - new env var `HARNESS_ASK_USER_TIMEOUT_SECONDS` (default `300`).
  - shared in-memory broker injected into both registry and runner.
- Added/updated tests:
  - `internal/harness/tools/ask_user_question_test.go`
  - `internal/harness/ask_user_broker_test.go`
  - `internal/harness/runner_test.go` (wait/resume and timeout paths)
  - `internal/server/http_test.go` (input endpoint lifecycle and error semantics)
  - `internal/harness/tools/catalog_test.go` and `internal/harness/tools_contract_test.go` (tool contract update)
  - `cmd/harnessd/main_test.go` (ask-user timeout env parsing)

## 2026-03-05 (Token Counting + Cost Tracking)

- Added additive accounting types in `internal/harness/types.go`:
  - `CompletionUsage` optional detail fields.
  - `CompletionCost`, `UsageStatus`, `CostStatus`.
  - Run-level totals: `RunUsageTotals`, `RunCostTotals`.
- Added pricing module in `internal/provider/pricing/`:
  - file-backed JSON catalog loader.
  - provider/model resolver with alias support.
  - unit tests for load/resolve/validation behavior.
- Extended OpenAI adapter (`internal/provider/openai/client.go`):
  - parses usage + detail fields.
  - normalizes missing usage to zero + `provider_unreported`.
  - computes cost from explicit response cost when present, otherwise resolver-driven pricing.
  - emits `unpriced_model` when pricing is unavailable.
- Extended runner accounting (`internal/harness/runner.go`):
  - per-turn accumulation of usage/cost totals.
  - new `usage.delta` event each model turn.
  - `run.completed` and `run.failed` now include usage/cost totals payloads.
  - run state includes persisted totals exposed by `GET /v1/runs/{id}`.
- Updated runtime context (`internal/systemprompt/runtime_context.go`):
  - replaced phase-1 cost placeholder with live token/cost fields.
  - default `cost_status: pending` before first completion.
- Wired pricing config in server startup (`cmd/harnessd/main.go`):
  - `HARNESS_PRICING_CATALOG_PATH` enables resolver-backed cost computation.
- Updated tests:
  - `internal/provider/openai/client_test.go`
  - `internal/provider/pricing/catalog_test.go`
  - `internal/harness/runner_test.go`
  - `internal/harness/runner_prompt_test.go`
  - `internal/systemprompt/engine_test.go`
  - `internal/server/http_test.go`
- Validation:
  - `go test ./...` passed.
  - `go test ./... -race` passed.
  - `./scripts/test-regression.sh` passed (`coveragegate: PASS`, total `80.1%`, zero-functions `0`).

## 2026-03-05 (Token/Cost Documentation Pass)

- Updated `README.md` to fully document:
  - `GET /v1/runs/{id}` usage/cost totals fields.
  - `usage.delta` payload contract.
  - missing-usage and missing-pricing behavior.
  - pricing catalog JSON format and configuration.
- Updated `docs/runbooks/harnesscli-live-testing.md`:
  - added `HARNESS_PRICING_CATALOG_PATH`.
  - documented expectation that `usage.delta` appears during runs.
- Updated `docs/design/system-prompt-architecture.md` heading/scope text to reflect OpenAI-first implementation status.
- Updated `docs/plans/INDEX.md` to mark token/cost plan as completed.

## 2026-03-05 (Optional Observational Memory: Local-First Foundation)

- Added new subsystem package: `internal/observationalmemory/`.
  - Core manager orchestration and state model (`manager.go`, `types.go`).
  - Model-backed observer + reflector implementations (`observer.go`, `reflector.go`).
  - Local per-scope coordinator (`coordinator.go`).
  - SQLite durable store with migration-safe schema (`store_sqlite.go`, migrations).
  - Postgres compile-ready stub for future activation (`store_postgres.go`).
- Added transcript/runtime context seams in tool layer:
  - `RunMetadata` and read-only `TranscriptReader` in `internal/harness/tools/types.go`.
- Added new tool: `observational_memory` in `internal/harness/tools/observational_memory.go`.
  - Actions: `enable`, `disable`, `status`, `export`, `review`, `reflect_now`.
- Wired tool catalog/default registry to include observational memory manager.
- Updated runner integration in `internal/harness/runner.go`:
  - Stores run transcript snapshots.
  - Injects `<observational-memory>` snippet before model turns when enabled.
  - Calls memory observe flow after each turn/tool cycle.
  - Emits memory lifecycle events (`memory.observe.*`, `memory.reflection.completed`).
  - Passes run metadata + transcript reader into tool execution context.
- Expanded run API metadata fields in `internal/harness/types.go`:
  - `tenant_id`, `conversation_id`, `agent_id` on `RunRequest` and `Run`.
- Updated server bootstrap in `cmd/harnessd/main.go`:
  - Added memory env config parsing and manager creation.
  - Wired shared manager into registry + runner.
- Added/updated tests for new surfaces:
  - `internal/harness/tools/observational_memory_test.go`
  - `internal/harness/runner_test.go` memory snippet/event coverage
  - Tool contract/catalog/default-registry expected tool list updates.
- Added architecture and runbook docs:
  - `docs/design/observational-memory-architecture.md`
  - `docs/runbooks/observational-memory.md`
- Updated roadmap/index/readme docs to include observational memory and configuration.

## 2026-03-05 (Modular System Prompt Subsystem)

- Added new prompt engine module in `internal/systemprompt/`:
  - `catalog.go`: YAML catalog loading/validation and prompt asset indexing.
  - `matcher.go`: deterministic model profile routing with fallback signaling.
  - `engine.go`: static prompt composition for base/intent/model/extensions/custom layers.
  - `runtime_context.go`: per-turn ephemeral runtime context formatter.
  - `types.go`, `errors.go`, `validation.go` for subsystem contracts.
- Added file-driven prompt assets under `prompts/`:
  - `catalog.yaml`
  - `base/main.md`
  - `intents/{general,code_review,frontend_design}.md`
  - `models/{default,openai_gpt5}.md`
  - starter behavior/talent extensions.
- Expanded run request model in `internal/harness/types.go`:
  - `agent_intent`, `task_context`, `prompt_profile`, `prompt_extensions`.
  - reserved `skills` field retained for forward compatibility and ignored in phase 1.
- Updated runner integration in `internal/harness/runner.go`:
  - resolve prompt context at `StartRun`.
  - preserve `system_prompt` override bypass behavior.
  - rebuild provider messages each turn using static prompt + ephemeral runtime context + transcript.
  - emit `prompt.resolved` and `prompt.warning` events.
  - keep runtime context non-persistent in transcript state.
- Updated server bootstrap in `cmd/harnessd/main.go`:
  - startup loads prompt engine from `HARNESS_PROMPTS_DIR` (with default auto-discovery).
  - added `HARNESS_DEFAULT_AGENT_INTENT` config.
  - startup fails fast on invalid prompt catalog/files.
- Updated CLI in `cmd/harnesscli/main.go`:
  - new flags for intent/profile/extensions (`-agent-intent`, `-task-context`, `-prompt-profile`, `-prompt-behavior`, `-prompt-talent`, `-prompt-custom`).
- Added/updated tests:
  - `internal/systemprompt/{catalog,matcher,engine}_test.go`
  - `internal/harness/runner_prompt_test.go`
  - `internal/server/http_prompt_test.go`
  - `cmd/harnesscli/main_prompt_test.go`
- Validation:
  - Focused suites passed: `go test ./internal/systemprompt ./internal/harness ./internal/server ./cmd/harnesscli ./cmd/harnessd`.

## 2026-03-06 (Terminal Bench Periodic Smoke Suite)

- Added a private Terminal Bench integration under `benchmarks/terminal_bench/`.
- Added custom benchmark agent bridge in `benchmarks/terminal_bench/agent.py`:
  - Copies the current repository into each task container.
  - Builds `harnessd` and `harnesscli` inside the container.
  - Starts the harness in tmux and drives tasks through the real HTTP API.
- Added three stable smoke tasks:
  - `go-retry-schedule-fix`
  - `staging-deploy-docs`
  - `incident-summary-shell`
- Added local runner script:
  - `scripts/run-terminal-bench.sh`
  - Uses `tb` when installed or falls back to `uv tool run terminal-bench`.
- Added scheduled workflow:
  - `.github/workflows/terminal-bench-periodic.yml`
  - Runs nightly and on manual dispatch, then uploads benchmark artifacts.
- Added operator documentation:
  - `docs/runbooks/terminal-bench-periodic-suite.md`
- Updated README, nightly tasks, plan tracker, and indexes to reflect the new benchmark path.
- Validation:
  - Not run in this change set.

## 2026-03-25 (HTTP Catalog Route Group Follow-up)

- Continued issue #427 after `origin/main` absorbed the earlier run/conversation extraction, leaving the catalog transport responsibilities inline in `internal/server/http.go`.
- Extracted the remaining catalog/provider/summarize HTTP transport into `internal/server/http_catalog.go` and updated mux wiring to register the catalog route group from one seam.
- Added route-group regression coverage in `internal/server/http_route_groups_test.go` to lock the `/v1/models`, `/v1/providers`, and `/v1/summarize` registration behavior to the extracted helper.
- Validation:
  - `go test ./internal/server -run 'TestRegister(Run|Conversation|Catalog)Routes' -count=1`

## 2026-04-05 (Stages 2-5 Orchestration Runtime)

- Added persistent checkpoint subsystem in `internal/checkpoints/`:
  - SQLite + memory stores
  - waiter/notify service
  - checkpoint-backed approval and ask-user brokers
  - HTTP routes for `GET /v1/checkpoints/{id}` and `POST /v1/checkpoints/{id}/resume`
- Added workflow runtime in `internal/workflows/`:
  - YAML-backed definitions
  - `tool`, `run`, `checkpoint`, and `branch` step execution
  - persisted workflow runs, step states, and workflow event streams
  - HTTP routes for `/v1/workflows*` and `/v1/workflow-runs*`
- Added explicit working memory in `internal/workingmemory/`:
  - SQLite + memory stores
  - core `working_memory` tool
  - runner prompt injection ahead of observational-memory snippets
- Added network compiler/runtime in `internal/networks/`:
  - YAML-backed network definitions
  - workflow-backed sequential role execution
  - HTTP routes for `/v1/networks*`
- Wired `cmd/harnessd` to:
  - open shared SQLite-backed checkpoint/workflow/working-memory stores
  - load workflow and network definitions from `HARNESS_WORKFLOWS_DIR` / `HARNESS_NETWORKS_DIR`
  - use checkpoint-backed approval/input brokers in the live runner
- Added failing-first tests for each new stage plus broader integration coverage in:
  - `internal/checkpoints/service_test.go`
  - `internal/harness/checkpoint_broker_test.go`
  - `internal/workflows/engine_test.go`
  - `internal/workingmemory/store_test.go`
  - `internal/harness/runner_working_memory_test.go`
  - `internal/networks/engine_test.go`
  - `internal/server/http_checkpoints_test.go`
  - `internal/server/http_workflows_test.go`
  - `internal/server/http_networks_test.go`
- Validation:
  - `go test ./internal/checkpoints ./internal/workflows ./internal/networks ./internal/workingmemory ./internal/harness ./internal/harness/tools/core ./internal/server ./cmd/harnessd -count=1`
- Fixed a shutdown bookkeeping race in `internal/symphd/dispatcher.go` where `Shutdown(...)` could return after semaphore drain but before deferred cleanup removed entries from `d.running`.
  - Added `TestDispatcher_ShutdownWaitsForRunningCleanup` in `internal/symphd/dispatcher_test.go` as the failing-first regression for the race.
  - Updated `Shutdown(...)` to:
    - release any partially acquired semaphore slots on context cancellation
    - wait for `d.running` to drain to zero before returning
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/symphd -run 'TestDispatcher_(Shutdown|ShutdownWaitsForRunningCleanup)' -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/symphd -count=1`
- Reduced `-race` test-suite timeouts in API-key-heavy packages without changing production hashing behavior.
  - Added low-cost test-only API-key helpers in:
    - `internal/store/apikey_test_helpers_test.go`
    - `internal/server/apikey_test_helpers_test.go`
  - Swapped the slow `store.GenerateAPIKey(...)` test call sites in:
    - `internal/store/apikeys_test.go`
    - `internal/server/auth_scope_test.go`
    - `internal/server/auth_test.go`
    - `internal/server/http_auth_test.go`
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/store -race -run TestAPIKey_SQLite -count=1 -timeout 2m`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/server -race -count=1 -timeout 5m`
- Replaced the shell-output fixture in `internal/cron/executor_test.go` with a faster `awk` generator so truncation coverage stays stable under heavier regression-suite load.
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/cron -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/cron -race -count=1`

## 2026-04-08 (Repo-Wide Regression Cleanup Follow-up)

- Fixed transcript export default-path selection in `cmd/harnesscli/tui/components/transcriptexport/export.go`.
  - `DefaultOutputDir()` now probes the cache, home, and temp candidates and returns the first writable absolute directory instead of assuming the cache path is usable.
  - Added `TestSelectRuntimeSafeOutputDirSkipsUnwritableCandidates` in `cmd/harnesscli/tui/components/transcriptexport/export_internal_test.go` to lock the fallback behavior when the preferred directory is not writable.
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui/components/transcriptexport -run 'TestTUI059_(ExportDefaultOutputDirCreatesFileOutsideWorkingDirectory|ExportDefaultOutputDir)|TestSelectRuntimeSafeOutputDirSkipsUnwritableCandidates' -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui -run TestExportCommandWritesOutsideWorkingDirectory -count=1`
- Hardened rollout integration timing in `internal/rollout/integration_test.go`.
  - Replaced the fixed post-terminal sleep with polling for a terminal JSONL event so the test matches the recorder's asynchronous flush semantics under full-suite load.
  - Validation:
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/rollout -run TestRunnerRollout_RunProducesJSONL -count=1`
    - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/rollout -count=1`
- Repo-wide verification:
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnessd -run TestMatrix_ConclusionWatcherEnabledWithEvaluator -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./cmd/harnesscli/tui/... -count=1`
  - `TMPDIR=$PWD/.tmp/tmp GOCACHE=$PWD/.tmp/go-build go test ./internal/... ./cmd/... -count=1`
  - `git diff --check`

## 2026-06-26 (Reliability T02 Terminal Event Fanout)

- Moved terminal event store append and subscriber fanout out of the runner mutex while preserving append-before-subscriber-observe ordering.
- Added a subscriber send/close guard for terminal fanout so cancellation cannot race a captured terminal subscriber channel.
- Added `TestTerminalStoreAppendDoesNotBlockRunnerQueries`, which blocks terminal event persistence and verifies unrelated run queries still return.
- Updated the terminal ordering test to exercise the out-of-lock publish path directly.
- Validation:
  - `go test ./internal/harness -run 'TestTerminalStoreAppendDoesNotBlockRunnerQueries|TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification' -count=1`
  - `go test ./internal/harness -race -run 'TestTerminalStoreAppendDoesNotBlockRunnerQueries|TestEventJournalDispatch_TerminalStoreAppendPrecedesSubscriberNotification|TestRunnerPrune|TestRecorderGoroutine_DoneClosedAfterRun' -count=1`

## 2026-06-26 (Reliability T04 Background Bash Shutdown)

- Bound background bash jobs to the tool execution context instead of `context.Background()`, so run cancellation terminates background jobs.
- Added `JobManager.Shutdown(ctx)` to cancel tracked jobs, wait for their `cmd.Wait` goroutines, and clear the jobs map.
- Added registry shutdown hooks and wired default registries to shut down their bash job manager.
- Updated `Runner.Shutdown` to invoke shutdown hooks once for the base registry and any per-run workspace registries after active runs are cancelled or drained.
- Added failing-first coverage for run-context cancellation, job-manager shutdown cleanup, and runner-level registry shutdown invocation.
- Validation:
  - `go test ./internal/harness/tools -run 'TestRunBackgroundCancelsWithRunContext|TestJobManagerShutdownCancelsAndClearsJobs' -count=1`
  - `go test ./internal/harness -run TestRunnerShutdownInvokesToolRegistryShutdownAfterCancellingRuns -count=1`
  - `go test ./internal/harness/tools ./internal/harness -race -run 'TestRunBackgroundCancelsWithRunContext|TestJobManagerShutdownCancelsAndClearsJobs|TestRunnerShutdownInvokesToolRegistryShutdownAfterCancellingRuns|TestRunnerShutdownStopsPoolDispatcher|TestRunnerShutdownIdempotent' -count=1`

## 2026-06-26 (Reliability T05 Scoped MCP Shutdown)

- Added a shutdown sweep that closes scoped per-run MCP registries for all live run states after shutdown cancellation or normal drain.
- Made `closeScopedMCP` atomically detach `state.scopedMCPRegistry` before closing so re-entry is a no-op and closed registries are not retained in memory.
- Added an `execute()` defer safety net so scoped MCP registries are closed even when execution exits outside the normal terminal helpers.
- Added `TestRunnerShutdownClosesWedgedScopedMCPRegistry`, which attaches an already-connected scoped MCP registry to a wedged run and verifies shutdown closes and clears it.
- Validation:
  - `go test ./internal/harness -run TestRunnerShutdownClosesWedgedScopedMCPRegistry -count=1`
  - `go test ./internal/harness -race -run 'TestRunnerShutdownClosesWedgedScopedMCPRegistry|TestRunnerShutdownInvokesToolRegistryShutdownAfterCancellingRuns|TestScopedMCPRegistry_Close|TestRunPreflight_BuildsScopedMCPRegistry|TestStartRun_MCPServers' -count=1`

## 2026-06-26 (Reliability T06 Shared Audit Buckets)

- Added shared audit writer buckets keyed by UTC date so same-day runs append through one runner-owned writer instead of one writer per run.
- Changed terminal audit cleanup to detach run state from the shared writer; buckets are closed once during `Runner.Shutdown`.
- Added `TestAuditTrail_ActiveRunsShareDateBucketWriter`, which keeps two same-day runs active and verifies both point at the same audit writer.
- Preserved existing audit persistence and hash-chain behavior with the writer's internal mutex and file-lock chain resume.
- Validation:
  - `go test ./internal/harness -run 'TestAuditTrail_ActiveRunsShareDateBucketWriter|TestAuditTrail_HashChainValid|TestAuditTrail_RunStarted_WrittenOnEnable|TestAuditTrail_RunCompleted_Written|TestTerminalSealing_AuditWriterWithRolloutDirClosesOnTerminal|TestTerminalSealing_AuditWriterFailedRunClosesOnTerminal' -count=1`
  - `go test ./internal/harness ./internal/forensics/audittrail -race -run 'TestAuditTrail_|TestTerminalSealing_AuditWriter|TestAuditWriter_(ConcurrentWrites|HashChain|HashChainIntegrity|CloseIdempotent|WriteAfterClose)' -count=1`

## 2026-06-26 (Reliability T07 Pool Dispatcher Recovery)

- Wrapped each bounded-pool dispatcher iteration with panic recovery so one bad queued item cannot kill the dispatcher goroutine.
- On dispatcher panic, the runner now releases the acquired worker token, marks the affected queued run failed, decrements its inflight count, logs the panic, and continues dispatching later queued items.
- Added a deterministic `poolDispatchHook` test seam and `TestPoolDispatcherRecoverKeepsDispatchAlive`, which queues work behind a held worker, panics one queued item, and verifies later items still complete and shutdown does not hang.
- Validation:
  - `go test ./internal/harness -run TestPoolDispatcherRecoverKeepsDispatchAlive -count=1`
  - `go test ./internal/harness -race -run 'TestPoolDispatcherRecoverKeepsDispatchAlive|TestRunnerShutdownDrainsBufferedQueue|TestRunnerShutdownStopsPoolDispatcher|TestPanicInProviderEmitsRunFailed|TestPanicInToolHandlerEmitsRunFailed' -count=1`

## 2026-06-26 (Reliability T08 Container Workspace Cleanup)

- Added a small Docker-client interface seam so container lifecycle cleanup can be tested without a live Docker daemon.
- `Provision` now records the workspace path as soon as the directory is created and force-destroys partial resources on create/start/inspect/config-write failures.
- `Destroy` now uses its own bounded background context for stop/remove, force-removes the container, and removes the workspace directory after successful cleanup.
- Added fake-client coverage for start-failure cleanup, workspace directory removal, and destroy behavior when the caller context is already cancelled.
- Validation:
  - `go test ./internal/workspace -run 'TestContainerWorkspace_(ProvisionStartErrorCleansContainerAndWorkspaceDir|DestroyRemovesWorkspaceDir|DestroyUsesForceContextWhenCallerContextCancelled)' -count=1`
  - `go test ./internal/workspace -count=1`
  - `go test ./internal/workspace -race -count=1`

## 2026-06-26 (Reliability T09 VM Post-Create Cleanup)

- `HetznerProvider.Create` now best-effort deletes the created server with a bounded background context when polling, timeout, disappearance, or caller cancellation happens after `Server.Create` succeeds.
- `VMWorkspace.Provision` now stores `vmID` immediately after provider create succeeds and before post-create setup, so caller cleanup can delete the VM if later setup fails.
- Added an HTTP-backed Hetzner regression for delete-after-poll-error and a VMWorkspace regression that simulates post-create failure and verifies `Destroy` deletes the retained VM ID.
- Validation:
  - `go test ./internal/workspace -run 'TestHetznerProvider_CreateDeletesServerAfterPollingError|TestVMWorkspace_ProvisionKeepsVMIDOnPostCreateError' -count=1`
  - `go test ./internal/workspace -count=1`
  - `go test ./internal/workspace -race -count=1`

## 2026-06-26 (Reliability T10 Worktree Serialization)

- Added a `runGitCommand` seam and per-repo mutex so `git worktree add`, `git worktree remove`, `git branch -D`, and `git worktree prune` are serialized by repository path.
- `Destroy` now runs `git worktree prune` even when worktree removal returns an error.
- `Pool.Close` now prunes each distinct worktree repository path once after destroying live workspaces.
- Added focused coverage for same-repo add serialization, prune-after-remove-error, and distinct repo pruning from pool close.
- Validation:
  - `go test ./internal/workspace -run 'TestWorktreeWorkspace_(ProvisionSerializesWorktreeAddPerRepo|DestroyPrunesAfterRemoveError)|TestPoolClosePrunesEachDistinctWorktreeRepoOnce' -count=1`
  - `go test ./internal/workspace -count=1`
  - `go test ./internal/workspace -race -count=1`

## 2026-06-26 (Reliability T11 Bash Streaming Long Lines)

- Replaced the foreground bash streaming `bufio.Scanner` with a draining `bufio.Reader` loop.
- The streamer now caps each emitted line at `defaultMaxStreamLineBytes` while continuing to drain the rest of an overlong line, preventing subprocess pipe blockage.
- Added result metadata for stream truncation: `stream_truncated`, `max_line_bytes`, and `stream_error`.
- Added a regression that streams a 4 MiB single line and verifies the command returns promptly without timing out.
- Validation:
  - `go test ./internal/harness/tools -run TestJobManagerRunForegroundStreamingOverlongLineReturnsPromptly -count=1`
  - `go test ./internal/harness/tools -count=1`
  - `go test ./internal/harness/tools -race -count=1`

## 2026-06-26 (Reliability T12 Cron Tenant Isolation)

- Added `tenant_id` ownership to cron job types, create requests, the HTTP cron client, the embedded cron adapter, and the SQLite cron store.
- `POST /v1/cron/jobs` now stamps jobs from the authenticated tenant context; list/get/update/delete/pause/resume only expose jobs for that tenant and return `404 not_found` on cross-tenant access.
- Cron by-ID handlers now distinguish typed job-not-found errors from backend failures, so real store/client errors return `500 internal_error`.
- Added SQLite migration coverage for legacy `cron_jobs` tables without `tenant_id`, plus persistence coverage for create/get/update/list and missing-delete not-found behavior.
- Updated `TestWorkerPoolLoad` to set `MaxCompletedRetention: totalRuns`; after T01, the default terminal-run retention is 32 and the load test starts 50 runs, so the test must opt into retention for its final GET assertions.
- Validation:
  - `go test ./internal/server -run 'TestCron(GetJob_Returns500ForBackendError|Jobs_AreTenantIsolated)' -count=1` failed before implementation because `tools.CronJob.TenantID` did not exist.
  - `go test ./internal/server ./internal/cron -run 'TestCron(GetJob_Returns500ForBackendError|Jobs_AreTenantIsolated)|Test(CreateJob_PreservesTenantID|Migrate_AddsTenantIDToExistingCronJobs|DeleteJob_NotFound)' -count=1`
  - `go test ./internal/server ./internal/cron ./cmd/harnessd -count=1`
  - `go test ./internal/server ./internal/cron ./cmd/harnessd -race -count=1`

## 2026-06-26 (Reliability T13 Server Hardening)

- Added a top-level server hardening wrapper that applies `http.MaxBytesReader` to request bodies and `http.TimeoutHandler` to non-streaming requests.
- Streaming-style routes whose final path segment is `events`, `stream`, or `wait` bypass the timeout wrapper so SSE and blocking wait endpoints keep their own `r.Context().Done()` behavior.
- `POST /v1/runs` now maps `http.MaxBytesError` to `413 request_too_large` instead of reporting malformed JSON after the body limit is exceeded.
- `buildHTTPRuntime` now constructs the daemon `http.Server` with `ReadTimeout: 60s`, `ReadHeaderTimeout: 10s`, `IdleTimeout: 120s`, and `MaxHeaderBytes: 1 MiB`.
- Added focused coverage for oversized request-body reads, non-streaming timeout behavior, streaming timeout bypass, and daemon server settings.
- Validation:
  - `go test ./internal/server ./cmd/harnessd -run 'Test(PostRunRejectsOversizedBodyWithoutReadingAll|HardenedHandlerTimesOutNonStreamingRequests|HardenedHandlerDoesNotTimeoutSSERequests)|TestBuildHTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer' -count=1`
  - `go test ./internal/server ./cmd/harnessd -count=1`
  - `go test ./internal/server ./cmd/harnessd -race -count=1`

## 2026-06-26 (Reliability T14 Replay Drift Gate)

- Added a small semaphore around `detect_drift:true` replay simulation so drift detection returns `503 replay_busy` instead of constructing additional throwaway replay runners when capacity is saturated.
- Added `ReplayDriftConcurrency` to `ServerOptions`; values <= 0 use the default of 2 concurrent drift detections.
- Added a drift-runner factory seam so tests can prove a saturated gate fails before `runDriftDetection` constructs a replay runner.
- Validation:
  - `go test ./internal/server -run TestHandleRunReplay_DetectDriftReturns503WhenSemaphoreFull -count=1`
  - `go test ./internal/server -run 'TestHandleRunReplay|TestReplaySimulate' -count=1`
  - `go test ./internal/server -count=1`
  - `go test ./internal/server -race -count=1`

## 2026-06-26 (Reliability T15 Registry Hot-Swap Safety)

- Added per-tool in-flight tracking in `Registry.Execute`; hot reloads now wait for old matching handlers to return before replacing tools with the same source tag.
- MCP tools registered via `RegisterMCPTools` now carry an `mcp_server:<name>` tag and retained server metadata.
- `ReplaceByTag` rebuilds `mcpServerTools` from surviving and replacement tools after the swap, so `UnregisterMCPServer` removes the current MCP-owned tools instead of stale names.
- Added regressions for MCP ownership rebuild after replacement and waiting for an in-flight handler before hot-swap completion.
- Validation:
  - `go test ./internal/harness -run 'TestRegistry_ReplaceByTag(RebuildsMCPServerTools|WaitsForInFlightExecution)' -count=1`
  - `go test ./internal/harness -count=1`
  - `go test ./internal/harness -race -count=1`

## 2026-06-27 (TUI Daily Loop, Workflow Recaps, Self-Improvement Command)

- Replaced TUI run-control guidance-only commands with HTTP-backed `/runs`, `/cancel`, `/replay`, and `/resume` actions. `/resume` expands `@path` attachments and emits `RunStartedMsg` so the existing SSE/session path continues the run.
- Added TUI run-list snapshots at 80x24, 120x40, and 200x50, plus focused tests for `/model` issue coverage, command routing, and run-control endpoint behavior.
- Added deterministic workflow recaps to terminal run state and durable run storage. Recaps include goal, changed files, tests run, failure cause, fix pattern, useful commands, and a next continuation prompt.
- Extended `harnesscli search`/`go-code search` to match recap content and `show` to print recap details when present.
- Added `harnesscli improve` and `go-code improve`, exposing the existing autoresearch loop as a first-class command with `--dry-run` planning and `--score-only` repo-native checks.
- Validation:
  - `go test ./cmd/harnesscli/tui ./cmd/harnesscli/tui/components/modelswitcher -run 'TestRunControl_|TestTUI_DailyHarnessCommandsSetGuidance|TestTUI041_BuiltinCommandsRegistered|TestTUI364_RegistryCompleteness|TestTUI573_|TestIssue57|TestModelSearch' -count=1`
  - `go test ./internal/store ./internal/harness ./cmd/harnesscli -run 'TestMemoryStore/UpdateRun_PersistsWorkflowRecap|TestSQLiteStore/UpdateRun_PersistsWorkflowRecap|TestRunnerStore_CompletedRunPersistsWorkflowRecap|TestRunSearch_(FiltersRunMetadata|MatchesWorkflowRecap)' -count=1`
  - `go test ./cmd/harnesscli -run 'TestGoCodeScriptRoutesDailyCommands|TestRunImproveDryRunPrintsSelfImprovementPlan|TestDispatchRoutesImprove' -count=1`

## 2026-06-28 (Go Relay PR #689 Review Repair)

- Resolved PR #689 merge conflicts against current `origin/main` while preserving the Go Relay server option and routes plus main's server-hardening fields.
- Fixed Relay worker HTTP tenant isolation: list/register now derive tenant scope from the authenticated API key, and get/update/delete/heartbeat hide cross-tenant workers as `404`.
- Made placement routing enforce required capability inventory, repo URL, browser, Docker, secret, memory, MCP, tool, and output-surface constraints before scoring workers.
- Wired `HARNESS_RELAY_DB` through `harnessd` persistence/bootstrap/runtime so the daemon can enable `/v1/relay/workers` with a real SQLite worker store.
- Fixed operator run-summary capability redaction by sanitizing with the selected worker's actual location type.
- Validation:
  - `go test ./internal/server -run 'TestRelayWorkersUseAuthenticatedTenant' -count=1` failed before implementation and passes after the tenant fix.
  - `go test ./internal/relay -run 'TestPlacementRequiresCapabilityInventory|TestPlacementRejectsCapabilityRequirementsWithoutCapabilityStore|TestOperatorRunSummaryRedactsNonLocalCapabilityPack' -count=1` failed before implementation and passes after the routing/redaction fixes.
  - `go test ./cmd/harnessd -run 'TestBuild(ServerOptionsForwardsBootstrapRuntime|PersistenceBootstrapInitializesStoresAndCleaner|HTTPRuntimeAssemblesRunnerSubagentsAndHTTPServer)' -count=1` failed before implementation and passes after the runtime wiring fix.
  - `go test ./internal/relay -count=1`
  - `go test ./internal/server -count=1`
  - `go test ./cmd/harnessd -count=1`

## 2026-07-18 (Issue #787 Hybrid Compaction Orphan Tool Messages)

- Symptom: after `compact_history` in `hybrid` mode dropped a large tool result but kept a small one from the same assistant turn, the resulting transcript had `tool` messages with a `tool_call_id` whose parent assistant message carried no `tool_calls` — rejected by OpenAI/Anthropic with a 400 on the next request.
- Cause: `compactHybrid` (both duplicated copies: `internal/harness/tools/compact_history.go`, `internal/harness/tools/core/compact_history.go`) rebuilt an `assistant_tool` turn's assistant message with only `Index/Role/Content`, dropping `ToolCalls`, while keeping small tool results verbatim. Both existing test suites used fixtures without `ToolCalls`, so they encoded the bug.
- Fix: partition each turn's tool results into kept (<=500 estimated tokens) and removed (>500); rebuild the assistant message with `ToolCalls` filtered to exactly the ids whose results survived, emitting it when it has non-empty trimmed content or at least one surviving tool call, followed by the kept results. Orphan tool turns (no assistant parent) fold kept results into the removed set instead of emitting unpairable tool messages. Applied identically in both copies (verified logic-identical modulo `tools.` package prefixes); a later tier dedups these files.
- Regression tests: `TestCompactHistoryTool_HybridModePreservesToolCallPairing` (`internal/harness/tools/compact_history_test.go`) and `TestCompactHistoryTool_Core_HybridModePreservesToolCallPairing` (`internal/harness/tools/core/compact_history_test.go`), enforcing the two-way pairing invariant (every assistant `tool_calls` id has a following tool result; every tool result id appears in a preceding assistant `tool_calls`).
- Validation:
  - Red phase: `go test ./internal/harness/tools/ ./internal/harness/tools/core/ -run 'HybridModePreservesToolCallPairing' -count=1` failed pre-fix (`parent assistant tool_calls ids exactly [call_small], got []`; `orphan tool result: tool_call_id "call_small" has no preceding assistant tool_calls entry`).
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ -run 'HybridModePreservesToolCallPairing' -count=1` (green)
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ -run 'Compact|ParseTurns|FindCompactionBounds|EstimateTextTokens|EstimateTranscriptTokens|TranscriptMsgsToMaps' -count=1` (all pre-existing compact tests stay green; no-ToolCalls fixtures produce identical output)

## 2026-07-18 (Issue #786 Bash Timeout/Kill Orphans Grandchildren)

- Symptom: `bash -lc 'sleep 300 &'` (or any command that backgrounds a child) with a 30s timeout returned only after ~300s, and `job_kill` left the backgrounded grandchildren running.
- Cause: all three spawn sites (`runForeground`/`runBackground` in `internal/harness/tools/bash_manager.go`, `runCommandOnce` in `internal/harness/tools/common_exec.go`) used `exec.CommandContext` with no `SysProcAttr.Setpgid` and no `WaitDelay`, so on timeout/`job_kill` Go SIGKILLed only the direct `bash` child; grandchildren survived and held the stdout/stderr pipes open, so `cmd.Wait()` blocked until they exited.
- Fix: new `configureGroupKill` (`exec_group_unix.go`, `//go:build unix`): `Setpgid` + a `Cancel` override that SIGKILLs the whole process group (ESRCH tolerated) + `WaitDelay = 2s`, matching the proven pattern in `tools/script/loader.go`; `exec_group_other.go` keeps non-unix behavior unchanged. Wired into all three spawn sites. `kill()` needed no change — `job.cancel()` routes through the overridden `Cancel`. Contract preservation: in all three exit-code branches, an error wrapping `exec.ErrWaitDelay` with an exited `ProcessState` recovers the real exit code, so a normally-exiting `bash -lc 'sleep 5 &'` still reports its exit code instead of -1.
- Regression tests (`internal/harness/tools/groupkill_unix_test.go`): `TestRunForegroundTimeoutKillsProcessGroup`, `TestJobKillKillsBackgroundJobGroup`, `TestRunCommandOnceTimeoutKillsProcessGroup` — assert prompt return after timeout/kill and poll `kill(pid, 0)` for ESRCH on the grandchild.
- Validation:
  - Red phase: pre-fix, `go test ./internal/harness/tools/ -run 'TestRunForegroundTimeoutKillsProcessGroup|TestJobKillKillsBackgroundJobGroup|TestRunCommandOnceTimeoutKillsProcessGroup' -count=1` failed — foreground and runCommandOnce each took ~10s instead of ~1s, and the job-kill grandchild was still alive after 3s.
  - `go test ./internal/harness/tools/ -run 'TestRunForegroundTimeoutKillsProcessGroup|TestJobKillKillsBackgroundJobGroup|TestRunCommandOnceTimeoutKillsProcessGroup' -count=1` (green, ~1s each)
  - `go test ./internal/harness/tools/... -count=1` (incl. `TestRunCommand_TimeoutReturnsNilError`, `TestRunCommand_ExternalSignalKillRetriesThenErrors`, streaming tests — all stay green)
  - `go test ./internal/harness/tools/ -race -count=1` (green)

## 2026-07-18 (Issue #785 Linux bwrap Sandbox Shared Host PID/IPC Namespaces)

- Symptom: on Linux, commands run under `SandboxScopeWorkspace`/`SandboxScopeLocal` (bubblewrap) could signal every same-UID host process and read host `/proc/<pid>/environ` (including API keys); darwin's seatbelt profile already restricts signals to self, so this was a cross-platform parity gap.
- Cause: `buildSandboxedCommand` in `internal/harness/tools/sandbox_linux.go` passed only `--unshare-net`; no `--unshare-pid`/`--unshare-ipc`/`--new-session`.
- Fix: insert `--unshare-pid`, `--unshare-ipc`, `--new-session` into the bwrap args right after `--unshare-net`, before the scope branch, so both Workspace and Local scopes get them. `--as-pid-1` intentionally not added (bwrap runs its own minimal PID 1 that reaps zombies); `--die-with-parent` unchanged.
- Regression tests (`internal/harness/tools/sandbox_linux_test.go`, `//go:build linux`): `TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC` (fake `bwrap` on PATH; asserts the argv for both scopes) and `TestSandboxLinuxPIDNamespaceHidesHostProcesses` (OS-level: host canary must be unsignalable and its `/proc/<pid>/environ` unreadable from inside the sandbox; skips when bwrap/user namespaces are unusable).
- Validation:
  - Runtime RED/GREEN requires Linux; this change was authored on macOS, so the linux-tagged files were verified with `GOOS=linux go build ./internal/harness/tools/` and `GOOS=linux go vet ./internal/harness/tools/` (both pass). Pre-fix, the argv assertions fail (flags absent) and the OS-level probe prints `CAN_SIGNAL_HOST`/`ENVIRON_READABLE`; run `go test ./internal/harness/tools/ -run 'TestBuildSandboxedCommandLinuxIsolatesPIDAndIPC|TestSandboxLinuxPIDNamespaceHidesHostProcesses' -count=1 -v` on a Linux host with bwrap for the full red/green cycle.
  - `go test ./internal/harness/tools/... -count=1` (darwin host, green)

## 2026-07-18 (Issue #796 Coverage Gate Red on subagentRunnerHandoff Wrappers)

- Symptom: `./scripts/test-regression.sh` failed at its coverage gate on main: all 8 `subagentRunnerHandoff` methods in `cmd/harnessd/runtime_container.go` (`StartRun`, `GetRun`, `Subscribe`, `CancelRun`, `RunPrompt`, `RunPromptWithAllowedTools`, `SteerRun`, `ParentRunID`) reported 0.0% coverage.
- Cause: PR #795 introduced the handoff (an initialization-cycle breaker that forwards `subagents.RunEngine`/`htools.ConstrainedAgentRunner`/`htools.RunSteerer` calls to a `*harness.Runner` installed later via `setRunner`) without any unit test exercising the wrappers.
- Fix: new `cmd/harnessd/runtime_container_handoff_test.go` builds a real `*harness.Runner` over the exported scriptable `fakeprovider` (single content reply, `ExhaustRepeatLast`), wires it into `&subagentRunnerHandoff{}` via `setRunner` exactly like `buildHTTPRuntime`, and asserts delegation behavior for all 8 methods — not just calls for coverage points: `StartRun` registers the run on the underlying runner; `GetRun` returns the runner's record (ok=false for unknown IDs); `Subscribe` on a completed run replays non-empty history with a live channel and working cancel func (error for unknown IDs); `CancelRun` surfaces `ErrRunNotFound` for unknown runs and is a nil no-op on terminal runs; `RunPrompt`/`RunPromptWithAllowedTools` return the scripted provider content (nil and named-tool filters); `SteerRun` surfaces `ErrRunNotFound`/`ErrRunNotActive`/blank-message validation matching the runner; `ParentRunID` returns ("parent-1", true) for a run spawned with `ParentContextHandoff`, ("", false) for whitespace-only, missing handoff, and unknown runs. Waits poll `GetRun` with a 5s deadline (10ms interval); no sleeps beyond that.
- Validation:
  - `go test ./cmd/harnessd -count=1 -run 'TestSubagentRunnerHandoff' -v` (5 tests green)
  - `go test ./cmd/harnessd -count=1 -race -run 'TestSubagentRunnerHandoff'` (green)
  - `go test ./cmd/harnessd -coverprofile=/tmp/hd-cover.out -count=1 && go tool cover -func=/tmp/hd-cover.out | grep runtime_container.go` — all 8 wrappers (and `setRunner`) at 100.0%, package total 84.5%.

## 2026-07-18 (Issue #788 Recipe Steps Bypass Approval/Policy)

- Symptom: under `ApprovalModePermissions`/`ApprovalModeAll`, one approval of `run_recipe` silently expanded into N unapproved steps — a recipe whose `bash` step was denied by policy executed it anyway (observed: `touch <ws>/pwned` ran with `exit_code:0`).
- Cause: the recipe `HandlerMap` was built by copying raw `Handler` values BEFORE the `ApplyPolicy` wrap loops in both registration paths: `internal/harness/tools_default.go` (recipe block ahead of the wrap loops) and `internal/harness/tools/catalog.go` (`buildHandlerMap(tools)` before the `applyPolicy` loop). `recipe.Executor` then invoked the captured pre-policy handlers. `applyPolicy` reports a denial as marshaled JSON (`permission_denied`) with a nil Go error, so a denied step does not abort the recipe — the fix had to prevent execution, not just surface the denial.
- Fix: moved the recipe registration block after the policy wrap loops in both files so the handler map snapshots post-wrap handlers; wrapped the recipe tool itself individually (`ApplyPolicy(recipeTool.Definition, ..., recipeTool.Handler)` before appending — same pattern as `connect_mcp`/`find_tool`). Side effect: recipe-addressable membership expands to tools registered after the old block position (script/workflow/deploy/deep-git/subagent/goals) — additive only, and all are policy-wrapped.
- Regression tests: `TestRunRecipeTool_PolicyAppliesToSteps` + `TestRunRecipeTool_PolicyAllowsSteps` (`internal/harness/tools/recipe_tool_test.go`; deny-bash policy, allow-all control, direct-bash sanity assertion proving the machinery) and `TestDefaultRegistry_RecipeStepsRespectPolicy` + `TestDefaultRegistry_RecipeStepsAllowedByPolicy` (`internal/harness/tools_default_test.go`; same shape via `NewDefaultRegistryWithOptions`).
- Docs: `internal/harness/tools/descriptions/run_recipe.md` now states each recipe step is subject to the same approval-mode and policy checks as a direct tool invocation, and that a denied step does not execute.
- Validation:
  - Red phase: pre-fix, both deny-policy tests failed — recipe output lacked `permission_denied` (step output showed `exit_code:0`) and the `pwned` marker file existed on disk.
  - `go test ./internal/harness/tools/ -run 'TestRunRecipeTool' -count=1` (green)
  - `go test ./internal/harness/ -run 'TestDefaultRegistry' -count=1` (green)
  - `go test ./internal/harness/... -count=1` (green)

## 2026-07-18 (Issue #789 Git Option Injection via Unvalidated Refs)

- Symptom: user-controlled revision arguments were appended bare to git argv ahead of `--`, so git parsed values like `--output=/abs/path` as options — an arbitrary file write from read-classified tools (`git_diff`, `git_blame_context`, `git_diff_range`). Verified empirically: `git diff --output=<p>` creates `<p>` even in a non-repository directory (exit 129), `git blame --porcelain --output=<p> -- f` creates `<p>`, and `git diff --stat "--output=<p>..HEAD"` creates `<p>..HEAD`.
- Cause: no validation at the four ref-to-argv sites: `internal/harness/tools/git_diff.go` (`args.Target`), `internal/harness/tools/core/git.go` (`args.Target`), `internal/harness/tools/deferred/git_deep.go` (`args.Rev` in blame; `args.From`+`args.To` glued into `from..to` in diff_range). `runCommand` returns a nil Go error for non-zero exits, so the injection surfaced as a normal tool result.
- Fix: new `internal/harness/tools/git_refs.go` exporting `ValidateGitRef` — rejects any ref beginning with `-` (legitimate refs: branches, tags, SHAs, `HEAD~2`, `a..b`/`a...b` ranges never do; git refnames cannot either). Applied after default assignment and before argv append at all four sites (`tools.` prefix in core/deferred; no import cycle since both already import package `tools`). The glued `--since=`/`--grep=` and `-S <query>` sites were left alone (option name is fixed, value position is safe). Rejected alternatives: `git check-ref-format`/`rev-parse --verify` (one exec per call, rejects unresolvable-but-valid refs, no range support) and `--end-of-options` (git >= 2.20, repo pins no minimum, delicate argv placement).
- Regression tests: `TestValidateGitRef_RejectsOptionLikeRefs`/`_AcceptsLegitRefs` (table-driven, `internal/harness/tools/git_refs_test.go`); `TestGitDiffTool_RejectsOptionLikeTarget` in both `internal/harness/tools/git_diff_test.go` and `internal/harness/tools/core/git_test.go` (error contains `must not begin with '-'`, injected file not created; no repo needed — validation precedes exec); `TestGitDiffTool_AcceptsLegitTargets` (`HEAD~1`, branch, `<sha1>..<sha2>` over a 2-commit repo); `TestGitBlameContextTool_RejectsOptionLikeRev`, `TestGitDiffRangeTool_RejectsOptionLikeFrom`, `TestGitDiffRangeTool_RejectsOptionLikeTo`, `TestGitBlameContextTool_AcceptsLegitRev`, `TestGitDiffRangeTool_AcceptsSHARange` (`internal/harness/tools/deferred/git_deep_test.go`).
- Docs: constraint clause added to the relevant args in `descriptions/git_diff.md` (target), `descriptions/git_blame_context.md` (rev), `descriptions/git_diff_range.md` (from/to); line-1 directive phrasing preserved (`TestToolDescriptionsContainBehavioralDirectives` stays green).
- Validation:
  - Red phase: pre-fix, all five reject tests failed with `expected error for option-like ..., got nil` and the injected marker files were created on disk (unit test failed to compile: `undefined: ValidateGitRef`).
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ ./internal/harness/tools/deferred/ -run 'ValidateGitRef|OptionLike|LegitTargets|LegitRev|SHARange' -count=1` (green)
  - `go test ./internal/harness/tools/ ./internal/harness/tools/core/ ./internal/harness/tools/deferred/ ./internal/harness/tools/descriptions/ -count=1` (green)

## 2026-07-18 (Issue #790 Deploy workspace Arg Accepts Any Absolute Path)

- Symptom: the `deploy` tool's `workspace` argument overrode the workspace root with any raw absolute path; `railway up`/`fly deploy` then package and upload that directory — arbitrary host-directory exfiltration under the default FullAuto approval mode. The pre-existing `TestDeployTool_WorkspaceOverride` blessed the behavior (detect against a directory outside the workspace succeeded).
- Cause: `internal/harness/tools/deferred/deploy.go` set `wsDir := args.Workspace` verbatim. `DeployTool` receives no sandbox scope, and the empty default scope makes `ConfineWorkspacePath` a no-op, so the confinement had to be unconditional.
- Fix: replaced the raw override with `tools.ResolveWorkspacePath(workspaceRoot, args.Workspace)` followed by `tools.ConfineWorkspacePath(tools.SandboxScopeWorkspace, workspaceRoot, nil, abs)`, placed before the `detect` branch so all four actions (deploy/status/logs/detect) are covered. Deliberate behavior change: relative `workspace` values now resolve against the workspace root (previously they were used raw, i.e. relative to the process CWD). Absolute paths outside the root fail with `deploy workspace: sandbox violation: path ... escapes the allowed workspace root ...`; `../` traversal fails inside `ResolveWorkspacePath` with `... escapes workspace`.
- Regression tests (`internal/harness/tools/deferred/deploy_test.go`, replacing `TestDeployTool_WorkspaceOverride`): `TestDeployTool_WorkspaceOverride_OutsideRejected` (absolute path outside root rejected with `escapes the allowed workspace root`), `TestDeployTool_WorkspaceOverride_InsideAllowed` (absolute subdir with `railway.json` detects `railway`), `TestDeployTool_WorkspaceOverride_RelativeInsideAllowed` (`workspace: "app"` resolves against root), `TestDeployTool_WorkspaceOverride_TraversalRejected` (`../sibling` rejected).
- Docs: `descriptions/deploy.md` and the JSON-schema description of the `workspace` property now state the path is relative to the workspace root (absolute paths must lie inside it), defaults to the workspace root, and outside paths are rejected.
- Validation:
  - Red phase (verified by stashing the fix and re-running the final tests): `OutsideRejected` failed with `expected error for workspace outside the workspace root, got nil`; `RelativeInsideAllowed` failed with `detect platform: no platform config found in app` (relative used raw); `TraversalRejected` failed with error text `no platform config found in ../sibling` instead of `escapes workspace`.
  - `go test ./internal/harness/tools/deferred/ -run 'TestDeployTool_WorkspaceOverride' -count=1` (green)
  - `go test ./internal/harness/tools/deferred/ ./internal/harness/tools/descriptions/ -count=1` (green)
# 2026-07-19 — Installable plugin bundles (Epic #748)

- Added validated, versioned installable bundles with explicit enabled versus trusted lifecycle state, CLI/TUI management, marketplace indexes, and runtime reuse of the existing skills, profiles, MCP, and hooks paths.
- Remote installs default untrusted; hook and MCP execution are unreachable until explicit trust.

## 2026-07-20 (Issue #846 Subscription-Auth Foundation)

- Added the internal `provider.TokenSource` contract and `StaticToken` adapter, keeping static-key client construction compatible.
- Extended the OpenAI-compatible client with request-time bearer lookup and copied static extra headers at both chat-completions and responses request sites. Authorization is applied after extra headers so an extra-header map cannot override it; errors identify only the credential operation, never its value.
- Added `internal/provider/tokencache`: a provider-neutral mutex-single-flighted refresh cache. It reuses credentials outside a configurable expiry margin; if a refresh within that margin fails while the current credential remains valid, it returns the still-valid cache entry. Refresh transport, OAuth details, and persistence deliberately remain follow-on-provider responsibilities.
- Added registry `SetTokenSource`: token sources satisfy configuration, evict cached clients on replacement, and reach the typed four-argument `catalog.ClientFactory`. `SetClientFactory` continues accepting existing three-argument static factories as a source-compatible bridge.
- TDD validation: provider token-source, OpenAI dynamic-auth/header/static-header regression, token-cache concurrency/failure-policy, and registry propagation/eviction tests were all red before their implementations and green afterward. `go test ./internal/provider/... ./internal/harness/...` passed.

## 2026-07-20 (Epic #849 Live Model Discovery)

- Generalized the catalog's OpenRouter-only cache into provider-agnostic live model discovery.
- OpenRouter, OpenAI, Anthropic, and DeepSeek now have five-minute cached listings when configured; failures retain stale cached results when present and otherwise leave the static catalog untouched.
- Live listings add models while curated catalog metadata remains authoritative on matching IDs.
## 2026-07-20 (Issue #848 Kimi Code Subscription Authentication)

- Added a separate `kimi-subscription` provider that derives its model list from the existing metered `kimi` entry, preserving the metered path unchanged.
- `harnesscli auth kimi login` reads the vendor credential only and stores a `0600` go-code-owned copy; status and logout never print a credential and logout never affects the vendor CLI.
- Refresh uses a 30-second margin for the real 900-second TTL. Fake OAuth/API integration coverage proves a forced near-expiry refresh, rotated persistence, dynamic bearer authorization, and all `X-Kimi-Client-*` headers.
- Live endpoint caveat: a single unauthenticated `OPTIONS https://auth.kimi.com/api/oauth/token` returned `405 Allow: POST`; no authenticated live refresh or completion was performed. The form/body and OpenAI-compatible wire contract are convention-based and must be manually verified.

## 2026-07-20 — Codex ChatGPT-Subscription Authentication (Epic #847)

- Added `internal/provider/codex`: read-only vendor credential import, a `0600` harness-owned credential store, JWT expiry parsing, OAuth refresh, and a `tokencache`-backed token source that persists refreshes only to `~/.harness/subscription-auth/codex.json`.
- Added `codex-subscription` as a structurally mirrored `openai` catalog provider. A token-source-required catalog flag distinguishes this remote subscription route from anonymous local optional-key providers, so absence remains unconfigured and never probes the ChatGPT backend.
- Existing OpenAI-compatible request code now supports the Codex backend's no-`/v1` endpoint path and applies `chatgpt-account-id` with the dynamic bearer credential. `HARNESS_PROVIDER=codex-subscription` selects it deterministically when imported credentials are present.
- Added `harnesscli auth codex login|status|logout`; `/keys` renders the read-only ChatGPT subscription connection state rather than offering API-key entry.
- Coverage includes OAuth request/error sanitization, import permissions/read-only behavior, catalog mirroring, bootstrap wiring, CLI lifecycle, TUI/server status, fake HTTPS request plus forced mid-session expiry refresh, and a grep-based no-token-logging guard.

## 2026-07-19 (Epic #815 Slice 1 Config Reload Field Classification)

- Change: new `internal/config/reload.go` — the single authoritative classification of every `Config` field as hot-swappable (takes effect on live reload for subsequent runs) or restart-only (wired once at startup, reported but never applied), plus the pure `ReloadDiff(old, new Config) ReloadReport` function later slices (runner swap, `POST /v1/config/reload`, SIGHUP, TUI `/reload`) build on.
- Classification rationale (grounded in `cmd/harnessd/main.go` consumption): restart-only is exactly `addr` (listen socket bound once), `memory.db_driver`/`memory.db_dsn`/`memory.sqlite_path` (persistence handles opened once), and `mcp_servers` (server processes and tool registry wired once). Everything else — model, max_steps, cost ceiling, memory toggles/thresholds/LLM knobs, auto_compact, forensics, conclusion_watcher, hooks, cron timing — flows into per-run `RunnerConfig` or runtime policy and is hot-swappable.
- Design: table-driven (`reloadFields` slice with path/class/equality probe per field) so report order is deterministic; `ReloadClassification()` exposes a copy for docs/validation; `ReloadReport` carries `Applied` + `RestartRequired` with `Changed()`/`NeedsRestart()` helpers. No behavior change to `Load`, `Defaults`, or `Resolve`.
- Tests (TDD, written first and verified red as compile errors): model-only change hot-swappable; `addr` restart-only; memory split (`db_driver` restart-only vs `enabled` swappable); identical configs empty report; `mcp_servers` map change restart-only; slice field (`hooks.dirs`) detection; mixed-change determinism; reflection-based exhaustiveness guard failing any future `Config` field added without classification.
- Validation: `go test ./internal/config/... -count=1` (green, 9 new tests); `gofmt`/`go vet` clean; every `reload.go` function at 100% statement coverage.
- Learning: the regression coverage gate rejects any zero-coverage function repo-wide — the first `test-regression.sh` run failed solely on the untested `ReloadClass.String()` helper. Fixed by adding `TestReloadClassString` (including the defensive unknown-class branch) before re-running; the gate failure was self-inflicted, not a baseline issue.

## 2026-07-19 (Epic #810 Slice 1: Theme Token Schema and JSON Loader)

- Change: new `cmd/harnesscli/tui/themes.go` adds a 17-token color schema (`TokenSet`, camelCase JSON keys aligned with kimi-code: primary, accent, text, textStrong, textDim, textMuted, border, borderFocus, success, warning, error, diffAdd, diffRemove, diffHunk, roleUser, shellMode, codeBackground) plus `LoadTheme(dir, name)`, `ListThemes(dir)`, `DefaultThemesDir()` (`~/.config/harnesscli/themes`), and built-in base themes `default-dark`/`default-light`.
- Design: token values are a string (`#rgb`/`#rrggbb` hex or ANSI-256 number, applied to both backgrounds) or an adaptive `{"light","dark"}` object. Resolution overlays each explicitly-set, valid token onto a copy of `DefaultTheme()` — omitted, empty, or unparseable values fall back per token and per side to the base palette (`tokenBaseColors`, pinned to theme.go's current colors by `TestThemesLoad_BasePaletteDerivation`). `theme.go` itself is untouched, so default rendering is byte-identical when no theme file exists; the full `go test ./cmd/harnesscli/tui/...` suite (including golden snapshots) passes unchanged.
- Fallback semantics: missing theme file or built-in name returns `DefaultTheme()` with no error; malformed JSON returns the base palette plus an error (callers keep the current/default theme and can surface the message); invalid token shapes (numbers, arrays) and invalid colors (`"not-a-color"`, 5-digit hex, ANSI > 255) fall back without failing the load. Unsafe names (`../x`, separators, empty) are rejected with an error.
- Mapping: every one of the 24 `Theme` style fields is bound to exactly one token (`applyToken`); `borderFocus`/`shellMode` are parsed and resolved but intentionally unbound — reserved for component-level styling in slice 2. `TestThemesLoad_TokenMappingCoversEveryThemeField` reflects over the struct so adding a `Theme` field without a binding fails the test.
- Validation: strict TDD — 16 behavior tests written first in `cmd/harnesscli/tui/themes_test.go` (red: compile error, undefined `tui.LoadTheme` et al.), then implementation to green. `go test ./cmd/harnesscli/tui/... -count=1` green (25 packages); `gofmt`/`go vet` clean.
- Deferred (later slices of #810, not this commit): threading resolved themes through components (slice 2), `/theme` picker (slice 3), config persistence (slice 4), website docs + example theme file (slice 5).

## 2026-07-19 (Epic #815 Slice 2 Runner ApplyConfig)

- Change: `Runner` can now swap its `RunnerConfig` at runtime via `ApplyConfig(RunnerConfig)` (`internal/harness/runner.go`). Runs started after the swap observe the new config (model, max steps, auto-compact, forensics knobs); in-flight runs keep the snapshot captured at their creation and are completely undisturbed. `NewRunner` signature and behavior unchanged.
- Design: `config` is guarded by a new leaf-level `configMu` (never held while acquiring `r.mu`; `ApplyConfig`/`snapshotConfig` touch no other lock). `runState` gains an immutable `*RunnerConfig` captured in `StartRun`/`ContinueRunWithOptions` (a continuation is a new run and gets a fresh snapshot); nil only for test-constructed runStates, where `configForRun` falls back to the runner's current config — preserving pre-change behavior for the ~19 direct `runState` literals in tests. Every one of the ~198 `r.config.X` read sites across `runner.go`, `runner_step_engine.go`, `runner_event_journal.go`, `plan_mode.go`, `permission_rules.go` now reads a per-function snapshot (`rc := r.configForRun(runID)` for run-scoped code, `rc := r.snapshotConfig()` otherwise); grep verifies zero unsynchronized reads remain. `NewRunner`'s zero-value defaulting was factored into `normalizeRunnerConfig` shared with `ApplyConfig`. Worker-pool sizing stays construction-time only.
- Boundary semantics (documented): snapshot isolation covers everything from run creation onward; a `StartRun` that overlaps an `ApplyConfig` call is itself "starting", so either side of the swap is legitimate for it. Mid-run per-step reads (auto-compact check in the step engine, hook application, emit/redaction path, error-chain, forensics flags) all come from the run's snapshot.
- Bug found and fixed during the slice: the auto-compact/manual-`CompactRun` summarizer resolved its model from the live config mid-run (`summarizeMessagesWithModelAndInstruction`). Added `summarizeWithConfig` + `runnerMessageSummarizer.rc` so run-scoped compaction (`autoCompactMessages`, `CompactRun`) resolves the summarizer model from the run's snapshot; the dead `newMessageSummarizerWithInstruction` constructor was removed (replaced by `newMessageSummarizerWithConfig`). Exported `SummarizeMessages`/`SummarizeMessagesWithModel` keep live-config resolution (no run context).
- Locking subtlety: `eventJournal.prepareLocked` runs under `r.mu`, so it reads `state.config` directly (falling back to `snapshotConfig`) instead of calling `configForRun` (which RLocks `r.mu` and would self-deadlock).
- Tests (TDD, red first as compile errors): `internal/harness/runner_apply_config_test.go` — new runs observe applied model (`CompletionRequest.Model` recorded by a gating provider) and applied `MaxSteps` (run dies at the new limit, provider call count == 2); in-flight isolation (config applied while a run is blocked in its first LLM call: no `auto_compact.started` for the in-flight run on its post-apply steps, original model used for all its calls; a post-apply run compacts and uses the new model); `ApplyConfig` normalization matches `NewRunner` defaults; concurrent apply+start hammer for `-race`.
- Validation: `go test ./internal/harness/ -run TestApplyConfig -count=1 -v` (5/5 green); `go test -race ./internal/harness/... -count=1` green (7 packages); `gofmt`/`go vet` clean; `GOCACHE=/tmp/go-build ./scripts/test-regression.sh` PASS (`coveragegate: PASS (total=84.0%, min=80.0%, zero-functions=0)`).

## 2026-07-19 (Epic #810 Slice 2: Thread Resolved Theme Through Components)

- Change: the resolved `Theme` now actually flows into the five component paths named by the epic. Each component gained a `Styles` struct + `DefaultStyles()` + optional injection (`statusbar.SetStyles`, `diffview.View.Styles`/`Model.Styles`, `spinner.WithStyles` (immutable), `messagebubble` `Styles` on `Model`/`UserBubble`/`AssistantBubble`, `tooluse.DiffStyles` pass-through to its embedded diffview). New `cmd/harnesscli/tui/theme_components.go` derives component styles from a `Theme`; `Model.SetTheme` stores the theme and re-distributes via `applyThemeToComponents` (foundation for slice-3 hot reload), called from `New`, after `WindowSizeMsg` statusbar re-creation, and after spinner re-creation on `RunStartedMsg`. Ephemeral components are styled at the two render funnels: `renderMessageBubble` and `appendToolUseView` (single injection point covers all four tool-card construction sites). The approval overlay (`approval.go`), previously unstyled, now renders chrome in the border-token color, the tool name in primary, and the action line in warning — the one deliberate default-appearance change in this slice.
- Zero-drift mappings (verified by `TestDefaultTheme_ComponentsMatchLegacyRendering` and unchanged component suites): statusbar Dim/Bold/Warn ← DimStyle/BoldStyle/WarningStyle (#FFAF00 == legacy); diffview Add/Remove/Hunk ← diff* styles, dashed border ← DimStyle (consistent with slice 1's SeparatorStyle ← textDim — the rule is a separator, not a box border); spinner ← DimStyle (faint); messagebubble keeps bg 237/fg 252/dot 15 defaults and takes colors only from tokens unset by default (roleUser → user fg, accent → assistant dot, textStrong → title fg), so default rendering is byte-identical.
- Validation: strict TDD — component tests (`styles_theme_test.go` in statusbar/diffview/messagebubble/spinner) and model-level `theme_redistribution_test.go` written first (red: undefined `Styles`/`SetStyles`/`WithStyles`/`SetTheme`), then implementation to green. Color assertions force `termenv.TrueColor` with save/restore (component tests previously only stripped ANSI; default renderer emits no color in non-TTY test runs). One test-expectation fix during green phase: `#E05252` renders as rgb(224,81,81) through termenv, not the hand-computed 224,82,82 — pinned actual output. One test-harness fix: `appendToolUseView` requires `ensureToolStateMaps()` first (nil-map panic in the test, not in product code paths).
- Deferred: `/theme` picker (slice 3), persistence (slice 4), docs + example theme (slice 5); tooluse collapsed/expanded chrome, plan-approval overlay, inputarea, and model.go overlay styles (colors 220/62 etc.) remain hardcoded — outside the epic's named component set.

## 2026-07-19 (Epic #810 Slice 3: /theme Picker With Live Apply and Directory Re-scan)

- Change: new `cmd/harnesscli/tui/components/themepicker/` (modeled on `profilepicker`: value-semantics `New(entries).Open()` state machine, `ThemeSelectedMsg`, rounded-border view with built-in tags and an `(active)` marker). `/theme` registered in `cmd_parser.go` beside `/profiles`; `executeThemeCommand` re-scans `ListThemes(dir)` and rebuilds the picker on every open (sessions-picker pattern), so theme files dropped into `~/.config/harnesscli/themes/` while the TUI runs appear without restart. Model gains `themeName` (default `"default-dark"`), a `themesDir` test seam (empty = `DefaultThemesDir()`), and the `"theme"` overlay kind wired at the same four sites as `"profiles"` (esc close, Enter route, catch-all key route, render switch). Selection resolves via the slice-1 `LoadTheme` and applies live via slice-2 `SetTheme`; loader failure keeps the current theme and sets an error status message (`Theme load failed: ... — keeping <name>`). Persistence deliberately excluded (slice 4).
- Validation: strict TDD — themepicker component tests (navigation wrap, Enter emits selection, Esc closes, SetEntries resets, view lists names/tags/active marker, empty state) and model-level `theme_picker_test.go` (registration, open lists built-ins + files, re-scan picks up a file added after first open, Enter+ThemeSelectedMsg applies live with statusbar restyle proof, malformed theme keeps current + error status, Esc closes without changes) written first (red: undefined `ThemeEntry`/`executeThemeCommand`/`themesDir`), then implementation to green.
- Deferred: config persistence of the selection (slice 4), website docs + example theme (slice 5).

## 2026-07-19 (Epic #815 Slice 3 POST /v1/config/reload)

- Change: `POST /v1/config/reload` (admin scope, same as `PUT /v1/providers/{name}/key`) triggers a live daemon config reload. New `internal/server/http_config.go` (`ConfigReloadFunc`, handler: 501 unwired / 400 with the load error on invalid config / 200 `{applied, restart_required}`), wired in `buildMux` and `ServerOptions.ConfigReload`. `cmd/harnessd/config_reload.go` adds `configReloader`: mutex-serialized load → `config.ReloadDiff` against last-known-good → full `RunnerConfig` reassembly → `Runner.ApplyConfig`; invalid TOML leaves the previous config active.
- Key design decision: reload must reproduce the FULL startup runner-config assembly, not bare `buildRunnerConfig` — `ApplyConfig` replaces hook slices wholesale, so a bare rebuild would silently wipe config-driven hooks, trusted plugin hooks, and the conclusion watcher on every reload. Extracted `assembleRunnerConfig` (buildRunnerConfig + ProfileRunStore + S3 uploader + conclusion watcher + config-driven hooks + trusted plugin hooks) and `loadHarnessConfig` (Load → Resolve → applyProfileDefaults → MaxSteps 0→8 daemon default) — both now shared by startup and reload so the two paths cannot drift. Existing `cmd/harnessd` tests pass unchanged (characterization for the extraction).
- Wiring: `httpRuntimeOptions.configReloader` → `buildHTTPRuntime` binds the created runner (`subagentRunnerHandoff` precedent) → `serverBootstrapOptions` → `ServerOptions.ConfigReload` via `configReloadFunc` adapter. Also added exported `Runner.Config()` (public read counterpart of `ApplyConfig`).
- Scope: memory-manager-resident knobs (`memory.*` LLM/threshold values) live in the `om.Manager` built once at startup and are not rebuilt by this slice (documented limitation; they remain classified hot-swappable in the Slice 1 table for a future manager-rebuild slice). `GET /v1/hooks` keeps serving the startup-computed summary.
- Tests (TDD, red first as undefined `ConfigReload`/`configReloader` compile errors): `internal/server/http_config_test.go` (model edit in temp file → 200 + applied list + next run uses new model; invalid TOML → 400 with error text + last-known-good retained; `addr` edit → restart-only warning; unwired → 501; GET → 405); `auth_scope_test.go` extended (read_only/write → 403, admin → 200); `cmd/harnessd/config_reload_test.go` (apply, invalid-keeps-good, restart-only + advancing diff base, hook slices survive reload without wipe/duplication and reflect `hooks.enabled=false`, concurrent reloads serialize, `buildServerOptions` wiring both branches).
- Validation: `go test ./internal/server/... ./cmd/harnessd/... -count=1` green; `go test ./internal/harness/ -count=1` green; `go test -race ./cmd/harnessd/ -run TestReloader -count=1` green; `gofmt`/`go vet` clean.

## 2026-07-19 (Epic #815 Slice 4 SIGHUP Config Reload)

- Change: `harnessd` HTTP-server mode now registers `syscall.SIGHUP` and routes it to the slice-3 `configReloader.reload` — the identical reload path as `POST /v1/config/reload`. New `awaitServer(sig, serverErr, reloadFn)` loop in `cmd/harnessd/config_reload.go` replaces the one-shot shutdown select: SIGHUP → reload + log `config reloaded (SIGHUP): applied=[...] restart_required=[...]` and keep waiting; reload errors logged, never fatal; SIGINT/SIGTERM return nil into the unchanged graceful-shutdown path; server error propagates. Nil reloadFn tolerates SIGHUP defensively.
- MCP stdio mode deliberately unchanged: its signal goroutine cancels on ANY delivered signal, so registering SIGHUP there would turn a hangup into a shutdown. Registration moved inside the non-MCP branch of `run()`; the MCP branch keeps its original `signal.Notify(sig, os.Interrupt, syscall.SIGTERM)`.
- Tests (TDD, red first as `undefined: awaitServer`): `cmd/harnessd/sighup_test.go` — SIGHUP invokes reload exactly once and keeps waiting; failing reload not fatal + second SIGHUP reloads again; server error returns immediately; SIGINT/SIGTERM return nil without invoking reload; nil reloadFn tolerated.
- Live smoke (acceptance): built `./cmd/harnessd`, ran with `HARNESS_PROVIDER=fake`, edited `~/.harness/config.toml`, `kill -HUP <pid>` → log shows `config reloaded (SIGHUP): applied=[model] restart_required=[]`, daemon kept serving, SIGTERM shut down cleanly. Two debugging notes: (1) first smoke attempt captured the wrapper subshell PID via `$!` (compound command), so the HUP killed the subshell not the daemon — the daemon itself handled SIGHUP correctly on the real PID; (2) `restart_required` was correctly empty despite an `addr` edit because `HARNESS_ADDR` was set — layer-5 env override masks the file value in both startup and reload loads, so no addr diff exists (cascade working as designed).
- Validation: `go test ./cmd/harnessd/... ./internal/config/... -count=1` green; `go test -race ./cmd/harnessd/ -run 'TestAwaitServer|TestReloader' -count=1` green; `gofmt`/`go vet` clean; `GOCACHE=/tmp/go-build ./scripts/test-regression.sh` PASS (`coveragegate: PASS (total=84.3%, min=80.0%, zero-functions=0)`).

## 2026-07-19 (Epic #810 Slice 4: Persist Theme Selection and Apply at Startup)

- Change: `Config` in `cmd/harnesscli/config` gains `Theme string` (`json:"theme,omitempty"`). The `/theme` picker handler persists the selection after a successful live apply (same load-mutate-save pattern as gateway/starring; save errors ignored, consistent with neighbors). `newTUIConfig` (`cmd/harnesscli/main.go`) loads the saved name into `TUIConfig.Theme` — the field is no longer display-only. `tui.New` resolves a non-empty `cfg.Theme` via `applyStartupTheme`: the slice-1 loader against the themes dir (default or the `themesDir` test seam), silently keeping `DefaultTheme()`/`default-dark` on any error (missing file resolves to the base palette with a nil error by slice-1 semantics; malformed JSON errors and is dropped entirely). The config-panel `theme` row now shows `m.themeName` (the active theme) instead of `m.config.Theme`. New `themesDirOrDefault()` helper dedupes the dir-resolution dance across `executeThemeCommand`, the picker handler, and startup.
- Validation: strict TDD — config round-trip test, two `newTUIConfig` tests, and six tui-internal tests (valid saved theme resolves + restyles, missing file renders default silently, malformed keeps `default-dark`, full accept flow: picker select writes config.json and a fresh model on the same HOME starts in that theme, delete-file fallback to default, config-panel row reflects active theme) written first (red: `loaded.Theme undefined`), then implementation to green. All persistence tests redirect `HOME` with `t.Setenv`; nothing touches the real user config.
- Deferred: website docs + example theme (slice 5).

## 2026-07-30 (Issue #1026 Direct GitHub Feedback Publication)

- Change: `/feedback <request>` now snapshots pending TUI image chips with the
  existing request, config, session, transcript, log, and rollout evidence,
  writes a local zip plus `0600` image sidecars, uploads them to the reusable
  `go-code-feedback-assets` GitHub prerelease, and creates the issue directly.
  The issue body links the bundle and renders every attached image inline.
  `--local` opts out, while `--issue` and `--screenshot` remain compatible.
- Ownership: `inputarea.Model` remains the source of truth for pending
  attachments. The command snapshots paths at submission time, and the async
  publisher owns deep-copied slices. Success removes only captured paths;
  failure preserves both the local artifacts and all retryable chips.
- TDD evidence: expected-red tests first showed that plain `/feedback` produced
  only a local status, that selective attachment removal and multi-image input
  did not exist, and that the publisher types were undefined. A later
  ownership test mutated the caller's image slice after command construction
  and reproduced the wrong uploaded filename; cloning at the async boundary
  fixed it.
- Verification: focused feedback/attachment tests, the complete
  `cmd/harnesscli/tui/...` and `cmd/harnesscli/...` suites, the harnesscli race
  suite, `go vet`, and `scripts/test-regression.sh` pass. The regression gate
  reported 85.6% coverage with zero uncovered functions.
- Live proof: a real Ctrl-V image chip in the tmux-hosted TUI created GitHub
  issue #1030. GitHub rendered the release-asset image inline; the downloaded
  image SHA-256 matched the clipboard source, the linked zip contained all nine
  expected evidence members, and the chip disappeared only after publication
  succeeded. The smoke issue was then closed as verification evidence.
- Environment learning: a regression run launched inside tmux timed out only
  in the two real macOS Keychain tests because the `security` subprocess was
  not operating in the logged-in GUI bootstrap context. Running those tests,
  and then the full regression suite, directly in the logged-in context passed.
  This is a test-launch environment distinction, not an accepted failing
  baseline.

## 2026-07-31 (Issue #1003 Remote cronsd Harness Dispatch)

- Root cause: standalone `cmd/cronsd` instantiated `ShellExecutor` directly,
  so `execution_type="harness"` jobs never crossed into `harnessd`.
- Fix: added `internal/cron.RemoteRunStarter` with the typed #1001 scope and
  job/execution correlation contract, bearer authentication, finite connect
  and request timeouts, safe retry-aware `RemoteRunError` values, and
  endpoint-class/job/execution/status/latency observability without prompt or
  credential contents.
- Boundary: added authenticated `POST /v1/cron/runs` in `internal/server`;
  it enforces `runs:write`, derives the effective tenant from auth, starts the
  existing runner, and returns a stable run ID. `DispatchExecutor` now
  validates harness readiness and never falls back to shell.
- Config: `cmd/cronsd` reads `CRONSD_HARNESS_URL`,
  `CRONSD_HARNESS_API_KEY`, `CRONSD_HARNESS_CONNECT_TIMEOUT`, and
  `CRONSD_HARNESS_REQUEST_TIMEOUT`; active harness jobs fail startup/create
  readiness when URL or credentials are absent, while shell-only operation is
  unchanged.
- TDD evidence: the new remote, auth, timeout/cancellation, malformed/non-2xx,
  readiness, and no-shell-fallback tests were added before implementation.
  The initial red run failed on the missing production symbols; targeted
  green and affected-package race tests now pass.
- Full regression: the exact unmodified `./scripts/test-regression.sh` passed
  in foreground execution with package tests, race tests, and coverage gate
  green (`85.6%` total, `zero-functions=0`). Detached tmux was not used for
  this final command because its PTY makes macOS `security(1)` ignore the
  test's piped secret; the foreground result is the authoritative gate.
- Real local canary: built current `harnessd` and `cronsd`, created a harness
  job through cronsd, and observed execution
  `789d3759-ad8d-4670-a6eb-91a4e4c27cd0` succeed with output summary
  `started run run_9b6b2d9c-5922-4330-a09b-fb99eddb538c`. Sanitized logs
  showed HTTP 202 and the same job/execution/correlation key on both daemons.
- No merge is part of issue #1003; this branch is ready only for review.

## 2026-07-31 (Issue #1003 Review Fixes)

- Review findings on PR #1060 identified three boundary gaps: duplicate
  `Idempotency-Key` requests could call `StartRun` twice, typed remote
  `code=timeout` errors were stored as failed executions, and the dedicated
  cron endpoint omitted the authenticated API-key prefix from `RunRequest`.
- Strict red-green fix: added a process-local, per-tenant correlation cache
  with fingerprint conflict detection; duplicate concurrent and sequential
  requests now return the accepted run result without another `StartRun`.
  `Scheduler.isTimeoutError` recognizes `RemoteRunError{Code:"timeout"}`
  and wrapped deadline errors, and `handleCronRun` copies the auth-context
  prefix into audit provenance.
- Focused evidence: the server/cron focused normal and race commands passed,
  as did the broader affected-package normal and race gates, before push.

## 2026-07-31 (Issue #1003 Durable Replay Review Fix)

- Independent review found the process-local cache still allowed a replayed
  accepted POST to call `StartRun` again after harnessd restart. This is a
  server-idempotency defect even though cronsd adds no application retry loop:
  Go's transport can replay a request with `Idempotency-Key` and a replayable
  body when a reused connection loses the response.
- Strict red-green evidence:
  `TestCronRunEndpointDurablyDeduplicatesAfterRestart` reopened the same
  SQLite store with a replacement runner and initially received a second run
  ID. `TestRemoteRunStarterRejectsRedirectWithoutForwardingCredentials`
  initially followed HTTP 307 and accepted the redirect target's response.
- Fix: built-in run stores now atomically reserve a tenant/key/fingerprint and
  server-reserved run ID before `StartRun`, then mark acceptance. In-process
  single-flight covers concurrent deliveries; an accepted durable binding
  returns the original run after restart; an interrupted reservation either
  recovers the persisted run or restarts the same reserved ID. Synchronous
  failures are no longer cached forever.
- Security/reliability hardening: remote starts refuse redirects, identifiers
  are quoted in logs, oversized request bodies remain rejected, tenant
  mismatch and fingerprint conflict are endpoint-tested, and the
  authenticated API-key prefix/timeout classification remain intact.
- Scope: terminal cron execution `run_id` persistence and terminal lifecycle
  remain #1004; #1003 only makes the start boundary replay-safe.
- Rebase: the three PR commits were cleanly rebased from `fedcf607` onto
  `origin/main` `b3afc7ec`.
- Verification before final documentation:
  - focused server/cron/store normal and race tests passed;
  - affected `internal/store`, `internal/harness`, `internal/server`,
    `internal/cron`, and `cmd/cronsd` normal and race suites passed;
  - unchanged foreground non-TTY `./scripts/test-regression.sh` passed with
    85.6% total coverage and zero uncovered functions, including the final
    rerun after adding the concurrent HTTP delivery regression.

## 2026-07-31 (Issue #1003 Fresh-Store API-Key Migration)

- Live review reproduced a production bootstrap gap at exact PR head
  `a27adfeb`: `buildPersistenceBootstrap` migrated the base run schema but
  omitted the separately defined API-key schema.
- Red evidence:
  `TestBuildPersistenceBootstrapMigratesAPIKeysOnFreshRunDB` failed while
  creating the first key with `no such table: api_keys`.
- Fix: harnessd now invokes the existing idempotent `MigrateAPIKeys`
  immediately after the base run-store migration and fails startup if either
  migration fails. The regression validates both key creation and bearer-key
  validation against a genuinely new database.
- Boundary: no cron execution schema or terminal `run_id` linkage changed;
  that remains issue #1004.

## 2026-07-31 (Issue #1003 Final Review Reliability Fixes)

- Exact-head review confirmed three additional production defects:
  reserved-ID starts could dispatch and mark a binding accepted after the
  initial run insert failed; harness starts ignored `job.timeout_seconds`; and
  completed single-flight entries accumulated for every unique execution.
- Strict red:
  - the persistence regression returned HTTP 202 and marked the binding after
    a simulated `CreateRun` failure;
  - the deadline regression observed the 5-second parent deadline instead of
    the persisted 1-second job deadline;
  - the cache regression found one completed entry after the start returned.
- Fix:
  - reserved-ID starts require a configured durable store and synchronously
    create the run record before it enters runner state or dispatch; failures
    wrap `ErrRunPersistence`, map to 503, and leave the binding unaccepted;
  - `HarnessExecutor` derives a job-timeout context, preserving any earlier
    parent deadline/cancellation, while `RemoteRunStarter` still applies the
    earlier daemon transport bound;
  - the process cache closes waiters then evicts every completed entry;
    durable tenant/key/fingerprint storage remains the sequential/restart
    replay source of truth.
- Compatibility: ordinary `StartRun` retains non-fatal persistence behavior.
  No `cron_executions` schema or terminal run linkage changed.
- Central follow-up found the remaining shutdown window after successful
  reserved persistence but before dispatch. A deterministic CreateRun hook
  shut down the first runner at that exact point; before the fix, replacement
  replay returned HTTP 202 but never registered/dispatched the queued run.
  `ResumeRunWithID` now requires an exact queued persisted match for ID,
  prompt, tenant, agent, and conversation, reuses its durable model/timestamps,
  dispatches it in the replacement runner, and only then permits acceptance.
- A second deterministic failure seam made the initial accepted-binding write
  fail after successful dispatch while the run remained queued. The retry now
  detects and reuses that active in-process run before retrying the binding
  write; it does not invoke resume or enqueue a duplicate. A barrier-store
  regression also proved two concurrent direct resumes could pass the early
  lookup together, so insertion now rechecks under the runner lock and permits
  exactly one same-ID dispatch.
- Final execution audit found resume hydrated the visible run model but still
  dispatched the original request with the replacement process's current
  default. The strict red provider capture observed `new-process-default`;
  resume now copies the persisted model/provider into the request and
  re-resolves the prompt before dispatch, so execution observes the durable
  model selected when the queued reservation was created.
- Exact-head review then exposed the accepted variant of the queued shutdown
  window: the binding could be marked accepted before shutdown drained its
  queued worker item. Restart previously returned the accepted ID forever
  without resuming it. Every existing binding is now inspected; an active
  same-process run is reused, otherwise an exact durable queued row is resumed.
- PATCH could persist nonpositive `timeout_seconds`, bypassing the harness
  deadline added in this review. The current-main rebase preserves the merged
  conversational contract: explicit zero or negative values are rejected
  before CAS mutation or dispatch, while valid persisted values bound dispatch.
- A 202 response whose body stalled until timeout/cancellation was mislabeled
  `malformed_response`. Body-decode failures now consult the request context,
  preserve typed timeout/cancel causes and retryability, and log latency after
  the body outcome is known.

## 2026-08-01 (Issue #1003 Persisted Conversation Agent Ownership)

- Review reproduced a restart-time authorization gap: ConversationStore had
  the tenant but no agent field, so agent B in `tenant-shared` was admitted to
  agent A's conversation after reopening both SQLite stores.
- Fix: `checkConversationOwnership` now consults conversation-scoped durable
  run records when configured. All persisted tenant/agent pairs must match;
  read failures fail closed. The companion regression proves agent A still
  resumes after restart. This adds no schema and does not alter #1004 linkage.

## 2026-08-01 (Issue #1003 Atomic First Conversation Ownership)

- Adversarial review found a TOCTOU after the restart repair: two reserved
  starts with different idempotency keys could both read an empty run list and
  then persist/dispatch different agents for one new conversation.
- Strict reds forced both ownership reads to snapshot empty. The in-process
  barrier and two-runner/shared-SQLite variants each observed two successes and
  zero denials.
- Fix: built-in `CreateRun` now couples a normalized tenant/agent claim in
  `conversation_run_owners` with the run insert atomically. MemoryStore uses
  one mutex; SQLite uses one transaction. Conflicts map to
  `ErrConversationAccessDenied` for reserved starts. Contract coverage proves
  same-owner continuation and rollback when the run insert fails.
- Scope correction: this additive schema supersedes the preceding note's
  no-schema statement; #1004 terminal execution linkage remains untouched.

## 2026-08-01 (Issue #1003 Ordinary Start Ownership Ordering)

- Follow-up review proved ordinary `StartRun` still admitted state, ignored an
  atomic owner-conflict error from `storeCreateRun`, and dispatched both agents.
  Deterministic in-memory and two-runner SQLite reds each reported two
  successes and zero denials; SQLite persisted only the winning run while both
  providers were invoked.
- Fix: ordinary `StartRun` now makes exactly one `CreateRun` attempt before
  recorder/state admission and dispatch. A typed owner conflict cleans any
  pre-activation and returns `ErrConversationAccessDenied`. Other CreateRun
  failures are still logged and non-fatal, as proven by the existing store
  outage regression. The later duplicate insertion was removed.

## 2026-08-01 (Issue #1003 Cross-Process Cron Dispatch Lease)

- Final P1 review found that process-local single-flight and runner locking did
  not fence separate harnessd processes. With two SQLite handles, initial
  delivery produced a duplicate reserved-run persistence failure and queued
  recovery dispatched two provider calls.
- Added an atomic expiring dispatch lease to `CronRunStartStore`. SQLite uses a
  conditional update over owner/expiry; MemoryStore applies the same contract
  under its mutex. Only the current owner can mark the binding accepted.
- `getOrStartCronRun` acquires before both initial and resume dispatch. Losers
  wait for an unaccepted owner, return the accepted stable run while its lease
  is live, or recover after expiry. Same-server mark retry renews its stable
  owner and reuses the active run.
- Added deterministic two-server/shared-SQLite initial and expired queued
  recovery tests, lease fencing/expiry store contracts, stale-owner rejection,
  and an upgrade test from the prior `cron_run_starts` schema.
- Focused normal and race suites passed. The first full server run was blocked
  only by sandbox denial of an unrelated IPv6 `httptest` bind; the identical
  command outside that restriction passed both store and server packages.

## 2026-08-01 (Issue #1003 Lease Linearizability and Heartbeat Repair)

- Two independent reviews identified the same remaining defect: SQLite
  acquisition updated and then read without a transaction, accepted caller
  wall-clock expiry, and never renewed a live backlogged owner's lease.
- Deterministic reds observed A report `acquired=true` with owner B, a
  +24-hour-skewed B steal A's lease, one concurrent migration fail with
  duplicate `dispatch_owner`, and B admit A's still-live queued run.
- Acquisition now uses one SQLite-clock `UPDATE ... RETURNING`; only a no-row
  loser performs a current-state read with `acquired=false`. Renewal is a
  distinct owner/live-expiry-qualified `UPDATE ... RETURNING`.
- A server-scoped heartbeat renews queued/running local runs and stops on
  terminal status, local absence, runner shutdown, or ownership loss. The
  shared-SQLite regression drives near-expiry renewal, rejects a skewed B while
  A is worker-backlogged, then stops A, expires the row, and proves exactly one
  B recovery/provider dispatch.
- Concurrent old-schema migration now accepts an ALTER race only after a
  schema recheck proves the other process added the column. Store/server normal
  suites and focused race suites pass.

## 2026-08-02 (Issue #1003 Frontier Pre-Admission Review Repairs)

- Red regression: an owner could acquire a lease, block in reserved-run
  preflight, lose the lease, and still dispatch after a competing owner took
  over because heartbeat renewal began only after runner admission. The repair
  starts a cancellable heartbeat before admission and passes its owner-loss
  context into context-aware `StartRunWithID`/`ResumeRunWithID`; the regression
  proves the blocked owner publishes neither durable nor local run state.
- Duplicate in-process idempotency deliveries now select the request context
  while waiting for the starter. Remote URL/key configuration is canonicalized
  once, queued replays load their persisted model before image/prompt preflight,
  and authenticated DELETE enforces the same 1 MiB mutation-body bound with
  HTTP 413.
- An assembled external integration regression exercises Scheduler,
  HarnessExecutor, RemoteRunStarter, authenticated harnessd, and the scoped
  remote run. The current execution output carries the accepted run identity;
  durable `Execution.RunID` linkage remains owned by #1004.
- Evidence interpretation is now explicit: a successful history record proves
  remote start acceptance, not the terminal harness outcome. The transitional
  #1003 canary reads `started run <id>` from output summary only to inspect
  scope through GET; it does not populate or infer `Execution.RunID`.
# 2026-07-28 — macOS inline loading states

- Added `CollectionLoadState` and a single Reduce-Motion-aware `LoadingPlaceholder` primitive in GoCodeUI's DesignSystem.
- `ProjectSession` now tracks each fetched collection independently, and empty messages are rendered only after the corresponding request succeeds with no entries.
- Replaced model-settings pane replacement and inline status/control swaps with fixed-shape skeletons or fixed slots; added load-state regression coverage.
- Verification note: the Swift toolchain could not create its Xcode module cache under sandboxed `/var/folders`, including when `TMPDIR` was set to `/private/tmp`; build/test execution remains blocked by that environment restriction.
## 2026-08-02 (Issue #1004 merge-review lifecycle repairs)

- Merge review found four correctness gaps in the first candidate: in-memory
  no-overlap did not coordinate separate SQLite scheduler processes; runner
  binding could make restart observation falsely terminal; a transient
  post-StartRun database failure could lose the typed `run_id`; and an equal
  `last_run_at` could regress other tracking clocks.
- `Store.AdmitExecution` is a durable SQLite `BEGIN IMMEDIATE` admission
  predicate over active executions joined to tenant/agent/conversation scope;
  the losing process writes a skipped-overlap history row.
- `ErrRunObservationUnavailable` is nonterminal. Startup and reconciliation
  retain the scope lease until an observer is bound. Run-link persistence
  retries before terminal observation; exhaustion fails closed and preserves
  both local and durable active leases.
- `TouchJobRun` advances an equal run timestamp only when `updated_at` and
  `next_run_at` are nondecreasing. TDD regressions cover all four cases.
- A second review found `Scheduler.Start` synchronously observing restored
  remote runs, holding cronsd/harnessd boot while a conversation remained
  live. The isolated pre-fix `0a00575b` regression timed out in
  `Start -> reconcileExecutions -> RemoteRunStarter.ObserveRun`. Startup now
  restores leases synchronously and observes terminals in a cancellable
  background pass. Embedded runner binding schedules one idempotent retry;
  remote cronsd uses the same background path without a bind callback.

## 2026-08-02 (Issue #1004 shutdown-owned reconciliation)

- Review found asynchronous restart observers were cancelled but not joined by
  `Scheduler.Stop`; an embedded post-bind callback could also start fresh work
  after Stop. A canceled remote poll could then race cron-store teardown and be
  converted to a false terminal failure.
- The scheduler now owns reconciliation context/admission and a dedicated wait
  group. Stop seals admission, cancels, joins observers, then returns; observer
  cancellation is nonterminal and preserves the active durable lease. A bind
  notification after Stop is ignored.
- Exact pre-fix `5583f04d` test-only replay failed Stop/join and post-stop
  no-op regressions. Direct normal tests and race x10 passed for those cases,
  authenticated recovered remote poll cancellation, and prior embedded/remote
  startup paths. The earlier repository-wide `cmd/harnessd` race timeout is
  not waived by this focused evidence.

## 2026-08-02 (Issue #1004 terminal persistence lifecycle fence)

- Review found a TOCTOU after observer return: `finishObservedExecution`
  checked cancellation, then could persist/release after `Stop` won; conversely
  Stop could return while a committed terminal write was still blocked. The
  generic job lookup path also converted cancellation and transient store
  errors into false "job unavailable" terminals.
- Exact `9181311` red tests independently reproduced cancel-wins persistence,
  commit-wins early Stop return, canceled lookup terminalization, and transient
  lookup terminalization.
- Terminal persistence now takes `lifecycleMu` only after observation returns;
  it atomically updates the execution, releases its local lease, and touches
  job tracking relative to Stop. `IsJobNotFound` is the only unavailable
  classification; cancel/transient failures preserve the active row for retry.
- Each new test passed normal and race x20; existing lifecycle normal/race x20
  passed host-local. Sandbox execution cannot bind the real `httptest` IPv6
  listener, but the identical host-local run passed. The full repository race
  gate remains required and unwaived.

## 2026-08-02 (Issue #1004 live observation shutdown ownership)

- New harness executions previously observed terminal state on dispatch's
  background context. A live embedded stream or remote poll could therefore
  make `Scheduler.Stop()` hang forever.
- Live observation now has a scheduler-owned context and wait group. Stop
  seals/cancels observation before joining it, without cancelling StartRun or
  shell dispatch. Observer cancellation, `observed=false`, and all observer
  errors preserve the active execution/run link/lease; a terminal write failure
  also preserves them and skips job tracking.
- Isolated exact-base `6838e25` replays were red for embedded cancellation,
  real remote poll cancellation, stop-wins, and terminal write failure.
  Commit-wins and shell-drain were passing base controls. Expanded direct
  normal and race x20 live tests pass; repository-wide regression remains
  required and unwaived.

## 2026-08-02 (Issue #1004 recovered observer errors are nonterminal)

- Review found recovery diverged from the live harness path: a recovered
  observer 503/stream error was written as failed/timeout, touched job run
  tracking, and released its restored no-overlap lease.
- `reconcileExecutionRows` now terminalizes only when the observer reports
  `observed=true` and nil error. Error/unobserved/canceled results retain the
  row/link/lease and continue to later active rows; explicit observed terminal
  failures still finalize normally.
- Exact `1d699808` test-only replay was red for recovered 503, stream error,
  and mixed error-plus-success rows. Direct focused normal and race x20 pass;
  repository-wide regression remains required and unwaived.

## 2026-08-03 (Issue #1004 embedded terminal replay race)

- Symptom: the composed embedded cron conversation test intermittently retained
  a durable `running` execution before `Stop`, even though most repetitions
  passed.
- Cause: terminal event replay can be returned after the subscriber snapshot
  but before `Runner` commits status/fanout. `cronRunStarter.ObserveRun`
  discarded that replay and could block forever on its silent live channel.
- Fix: replay terminal events trigger authoritative `GetRun` confirmation with
  cancellation-aware bounded polling; the same low-rate polling is the
  fallback for intentionally suppressed terminal events. Terminal result fields
  come only from the committed run, never from event payload. Focused
  normal/race x20 and the composed embedded flow x100 pass.
## 2026-08-03 (Issue #1106 liveness and process-loss recovery repair)

- A deadline-cancelled callback previously persisted `retry_wait` but passed
  `schedule=false` to its own durable state reconciliation. A normal
  single-manager daemon therefore stranded the callback until a later restart.
  The release path now re-arms its retry and a deterministic blocked-renewal
  test proves a second same-ID admission without constructing another manager.
- An expired callback lease is not proof that its process died. The
  filesystem-backed SQLite callback store now owns a sidecar non-blocking
  `flock` for the recovered manager lifetime. A second bootstrap sharing the
  workspace fails before it can turn a live owner's expired dispatch into a
  retry. The kernel releases this authority on process crash.
- This intermediate legacy-`NULL` recovery contract was superseded by the
  final mixed-version fencing repair: only a current private
  `dispatching_fenced` row with its exact bootstrap-observed token may recover
  a `NULL` lease; legacy public `dispatching` remains fail-closed.
## 2026-08-03 (Issue #1106 future-lease and bounded-handoff repair)

- Recovery had only a bootstrap-time expired-lease check. A crash row whose
  persisted lease was still future was armed at that timestamp, but ordinary
  dispatch treated it as live forever. The already-authorized manager now
  re-enters `RecoverExpiredLease` when that timer fires.
- Deadline cancellation now honors the persisted attempt cap and exponential
  retry delay. At the cap it token-fenced terminalizes as `failed`, preventing
  endless same-ID admission loops.
- Durable `Recover` now requires workspace authority for every store; SQLite
  `:memory:` and opaque non-filesystem locations return the typed authority
  requirement instead of silently bypassing fencing. A killed subprocess test
  proves flock release occurs on process death.

## 2026-08-03 (Issue #1132 deterministic compaction-after-wait fixture)

- The compaction/resume regression had polled `PendingInput`, then assumed the
  separate public wait-state publication had completed. That assumption is
  invalid: broker registration deliberately precedes the `waiting_for_user`
  status/event.
- The test now subscribes immediately after `StartRun` and awaits
  `EventRunWaitingForUser` through the shared history/live-stream helper before
  inspecting pending input or state. All existing compaction, event ordering,
  resumed output, and exact message/tool-delta assertions remain unchanged.
- No production code changed. Focused normal and race stress (`-count=100`)
  passed; package and full regression evidence is recorded with this slice.
## 2026-08-03 (Issue #1135 deterministic cron recovery fixture)

- Replaced terminal-notification timing in the embedded post-bind and remote
  asynchronous recovery tests with an explicit terminal `UpdateExecution`
  gate. Each test now proves same-scope admission remains denied before the
  durable terminal call returns, releases it, joins `reconcileWG`, verifies
  the exact scope and recovered lease are absent, then admits the next run.
- No production scheduler code changed. The remote fixture holds its existing
  authenticated terminal response rather than racing a local status mutation.
- Focused normal/race x100, complete cron normal/race, and the full repository
  normal/race/coverage gate pass at 85.5% total coverage with zero uncovered
  functions.
## 2026-08-03 (Issue #1141 callback deadline-release fixture)

- Three callback deadline-release fixtures now use a one-second test lease and causal gates: admitted callback, heartbeat renewal entry, deadline signal, then cancellation-aware starter cancellation before unblocking renewal.
- This is test-only. The strengthened assertions retain the durable safe error, attempt/run identity, retry state, and token/lease clearing contracts. Focused normal x20 (60.593s), race x20 (62.087s), and the full normal/race/coverage gate passed at 85.5% total coverage with zero uncovered functions.
## 2026-08-03 (Issue #1122 native interactive-state ownership)

- `PendingApproval` and `PendingPlan` now retain their originating SSE
  `run_id`. `RunSession` clears approval, plan, and pending input synchronously
  when selection changes, a selected run retires/falls back, or active runs are
  cleared. Chat and ToolWalk pass that captured id to guarded action APIs.
- Deterministic external-run tests cover approval, plan, and input from A being
  displaced by timestamp-newer B; stale captured actions produce no B endpoint
  request. They also cover selected terminal clearing and a foreign terminal
  preserving B's interaction. The expected-red focused Swift build initially
  failed because run IDs and explicit actions did not exist; the focused green
  suite passes after implementation.
- Exact final verification: focused external ownership (11 tests), complete
  Swift package (222 tests / 43 suites), and `scripts/test-regression.sh` all
  pass; the repository coverage gate reports 85.5% total and zero uncovered
  functions.
## 2026-08-04 (Issue #1147 default callback run admission)

- Symptom: a callbacks-enabled default harness persisted a delayed callback and
  retried due-time dispatch three times with `callback admission unavailable`.
  The callback's durable reserved run ID was correctly retained, but no run
  could start or advance the originating conversation.
- Cause: `buildPersistenceBootstrap` created a run store only for explicit
  `HARNESS_RUN_DB`; `callbackRunStarter.StartCallback` correctly calls
  `Runner.EnsureRunWithIDContext`, which correctly refuses a reserved run ID
  without durable persistence. The supported default configuration wired those
  two correct contracts incompatibly.
- Fix: when callbacks are enabled and no explicit run DB is configured,
  bootstrap/migrate workspace `.harness/runs.db` and pass it to the Runner.
  Mark this store internal so server auth remains disabled by default; explicit
  `HARNESS_RUN_DB` retains normal authentication behavior.
- Tests: the initial default-bootstrap regression failed with no run store.
  Bootstrap/auth compatibility and composed durable callback recovery/admission
  tests pass. The acceptance regression posts an unauthenticated HTTP run,
  invokes the agent-visible tool, waits through its five-second due time, and
  asserts a started callback plus the real follow-up assistant marker in the
  same conversation.

## 2026-08-04 (Issue #1153 cron durable dispatch polling coverage)

- `waitForCronRunDispatch` was reachable only when a durable foreign lease was
  unaccepted. Existing multi-server tests usually elected a dispatcher before
  that poll, leaving a genuine 0%-coverage concurrency path.
- A scripted `CronRunStartStore` test wrapper now drives the real
  `getOrStartCronRun` path through an unaccepted foreign lease, then the normal
  lease acquisition. It proves one durable reserved ID, at least two acquire
  attempts, one run admission, and one provider dispatch.
- A separate pre-seeded foreign lease with an already-cancelled context proves
  the long poll does not delay cancellation and makes zero run/provider
  admissions. Production lease, tenant, and API behavior are unchanged.
## 2026-08-04 (Issue #1149 cron execution-history API)

- Added `GET /v1/cron/jobs/{id}/executions`, authorizing the tenant-visible job
  before calling the existing adapter. HTTP regressions cover linked runs,
  pagination, scope denial, foreign/missing 404s, adapter failure, and absent cron.
## 2026-08-04 (Issue #1148 idle scheduled conversation continuation)

- The TUI now maintains a selected-conversation SSE bridge after run terminal,
  reconciles fetched history with replay by event identity, rejects stale
  conversation bridge results, and preserves active-run terminal finalization.
- Independent cheap review found and the final tree covers the local-terminal
  to external-assistant transition plus bounded (4096-event) identity dedupe;
  content text is never used as a replay key. The full
  `./scripts/test-regression.sh` rerun passed normal, race, and coverage at
  85.5% with zero uncovered functions. This is not PTY or native-GUI proof;
  those remain the #1000 convergence matrix.

## 2026-08-04 (Issue #1009 — scheduled-task lifecycle and macOS controls)

- Change: `GET /v1/tasks` now projects optional, server-authored cron and
  callback lifecycle fields: conversation linkage, cron next/last timestamps,
  most-recent execution state/run/error, callback due time, and update time.
  Existing type-specific routes remain the sole mutation authority.
- Native app: `TaskInfo` now has typed forward-compatible kind, state, and
  action values; unknown server values decode without making the Activity page
  unusable. HarnessKit adds scoped pause/resume/delete/cancel requests.
  Activity displays lifecycle detail and accessible controls, asks before cron
  deletion, and always reloads server state after an action succeeds or fails.
- TDD: the first Go test failed because `Task` had no lifecycle fields; the
  first Swift test failed because task values were raw strings and control APIs
  were absent. A full Swift run then caught a global URLProtocol-stub race in
  the new tests; the task tests now use their own isolated protocol class.
- Verification: focused task lifecycle tests, `go test ./internal/server
  -count=1`, `go test ./internal/server -race -count=1`, and the full Swift
  suite (`256` tests) passed. The direct full repository gate, run with its own
  temporary cache and coverage profile after rebase to `f7b6c70`, passed normal,
  race, and coverage phases at `85.5%` total coverage with zero uncovered
  functions. This makes the implementation ready for review; it is not the
  separate #1010 API/TUI/native full-conversation proof.
- Review repair: cron Activity actions now carry optional `expected_updated_at`
  only when the row provides it; server actions preserve empty legacy bodies,
  require active-to-pause and paused-to-resume state, and map stale/invalid
  mutations to 409 without changing the job. Callback `updated_at` is read
  from the durable row on every list/get/returning path and projects into the
  task row. The native "Open linked run" control opens the durable conversation
  and lets only non-terminal run-event reducer evidence establish live control
  ownership; terminal and missing links cannot manufacture controls. Repair
  regressions cover stale/current actions, persisted callback freshness, JSON
  request shape, and active/terminal/missing navigation.
- Repair verification: complete affected server/tool normal and race suites
  passed; the full native suite passed 259 tests. The first full repository
  attempt ran concurrently with another coverage regression and hit two
  unrelated `cmd/harnessd` three-second startup timeouts, so it was not
  accepted. After that load completed, the serial rerun with fresh cache/profile
  passed normal, race, and coverage at 85.5% total with zero uncovered
  functions.

## 2026-08-04 (Issue #1009 review repair — opaque cron task version)

- Cause: HarnessKit decoded task `updated_at` into `Date`, then encoded it
  with a new ISO-8601 formatter for `expected_updated_at`. That conversion can
  discard server-issued nanoseconds, making an otherwise fresh Activity row
  fail its cron CAS action with 409.
- Fix: `TaskInfo.updatedAtVersion` retains the raw optional `updated_at`
  string through ProjectSession and `TaskActionVersion`; standard `Encodable`
  now emits the token unchanged. Missing versions still use the existing empty
  request body for older additive task payloads.
- Regression: Swift asserts a `.123456789Z` task token and the exact JSON
  action field; Go lists a nanosecond cron token, proves `.123Z` returns 409
  without mutation, then proves the exact listed token pauses it.

## 2026-08-04 (Issue #1009 review repair — no-store callback terminals)

- Cause: no-store callback terminal rows stay in `m.callbacks` but leave the
  active `byConv` index. `ListAllCallbacks` incorrectly walked that active
  index, making canceled/fired/shutdown callbacks vanish from `/v1/tasks`.
- Fix: all-state listing snapshots and safely projects `m.callbacks`; legacy
  conversation `List`/`ListCallbacks` remain active-only through `byConv`.
  Legacy cancel, fire, and shutdown cancellation now stamp `UpdatedAt` from
  the manager clock. The durable cancel/list branches are unchanged.
- Regression: deterministic manager coverage proves terminal timestamps and
  all-state retention while agent-facing lists exclude terminals; server
  coverage proves cancel then `GET /v1/tasks` returns one canceled read-only
  row with nonzero `updated_at`.

## 2026-08-05 (Issue #1180 bootstrap staging clone)

- Symptom: direct Go 1.26.4 `-buildvcs=true` compilation in a linked worktree
  produced missing VCS settings despite clean target Git state.
- Cause: Go's VCS discovery requires directory-form `.git`; the linked
  worktree has a Git indirection file, and setting `GIT_DIR`/`GIT_WORK_TREE`
  did not make buildvcs stamp the binary.
- Fix: `scripts/init.sh` validates target state, makes an ephemeral local
  clone, detaches it to the target SHA, checks it clean, builds there with Git
  overrides removed, validates candidates, atomically publishes, and removes
  the owned clone on exit.
- Regression: linked-worktree fake compiler accepts only a directory-form
  `.git`; legacy target-CWD build fails it, while the isolated clone passes.

## 2026-08-05 (Issue #1174 review repair — unbound init start failure)

- Cause: `startRunCmd` emits `RunFailedMsg` without a `RunID` when run creation
  fails before the harness accepts it. The `/init` pending-write fence rejected
  that empty identity but left the unbound state for a later ordinary
  `RunStartedMsg` to claim.
- Fix: a failed message with an empty identity now consumes only an unbound
  `/init` pending state; bound and foreign nonempty run IDs retain exact-ID
  isolation.
- Regression: `/init` then empty-ID start failure followed by an ordinary
  start, assistant output, and completion never creates `AGENTS.md`.

## 2026-08-05 (Issue #1174 review repair — malformed run creation response)

- Cause: a 2xx `/v1/runs` response containing valid JSON but no `run_id`
  decoded successfully and emitted `RunStartedMsg{RunID:""}`. That left an
  unbound `/init` fence vulnerable to a later run binding it.
- Fix: `startRunCmd` now rejects missing or whitespace-only `run_id` values as
  `RunFailedMsg`; existing unbound-start cleanup consumes the pending `/init`
  state.
- Regression: a real 2xx `{}` response during `/init`, followed by close and
  ordinary-run output/completion, never creates `AGENTS.md`.

## 2026-08-05 (Issue #1174 review repair — normalized run identity)

- Cause: the missing-ID guard trimmed only for validation, then returned the
  untrimmed `run_id`; valid responses with surrounding whitespace could fail
  later exact-ID comparisons.
- Fix: `startRunCmd` stores the trimmed value before validation and emits that
  normalized identity in `RunStartedMsg`; omitted and whitespace-only IDs still
  fail closed.
- Regression: a surrounding-whitespace run ID becomes the canonical identity,
  while a whitespace-only JSON value returns `RunFailedMsg`.

## 2026-08-05 (Issue #1190 production MCP HTTP transport ownership)

- Cause: production `dialHTTP` created a nil-transport `http.Client`, so its
  MCP requests used process-global `http.DefaultTransport`. An unrelated
  `httptest.Server.Close` can close that pool and turn an expected 401 into a
  transport cancellation.
- Fix: production and `NewHTTPConnForTest` now share one factory that clones
  the default `*http.Transport`; every `httpConn.Close` atomically marks its
  own connection closed then idempotently closes only that client pool.
- Regression: a nonparallel, gated production auth dial survives unrelated
  global cleanup and returns `ErrUnauthorized`; sibling/local-close and token
  provider precedence coverage retains ownership and strict error contracts.
- Review follow-up: an explicit legacy nil-transport control now holds a
  request in a cleanup-cancelling global transport. `httptest.Server.Close`
  reaches it and deterministically returns a transport error rather than
  `ErrUnauthorized`, proving the historic coupling rather than only asserting
  the fixed client's nonnil field.
# 2026-08-05 (Issue #1186 public cron validation identity)

- Cause: raw cronsd already emitted `400 validation_error`, but its client
  flattened that response and harnessd POST rendered every adapter error as
  500. The embedded adapter likewise returned untyped validation strings.
- Fix: `cron.ValidationError` now retains a caller-safe raw-cronsd validation
  message; both adapters translate it into `tools.ErrCronJobValidation`; the
  public facade renders that sentinel as `400 validation_error` while retaining
  not-found, conflict, and dependency mappings. Explicit zero/negative POST
  timeouts are rejected before any adapter/store write, while omission still
  receives the existing 30-second default.
- Regression: red-first compile tests exposed the missing typed seam, then an
  HTTP regression observed the pre-fix 500. Focused normal/race coverage proves
  remote and embedded create/update validation, no invalid persistence, and
  404/409/5xx preservation. Rebasing on #1190's merged transport fix allowed
  canonical-temp full regression to pass normal, race, coverage, and
  coveragegate at 85.5% total coverage with zero uncovered functions.
# 2026-08-05 (Issue #1194 porcelain blame parsing)

- Cause: `parsePorcelainBlame` accepted arbitrary non-indented long lines with
  three or more fields as headers. Porcelain `previous <hash> <path>` metadata
  therefore overwrote the actual commit identity and coerced the path to line
  zero; nonzero `git show` diagnostics could also populate a commit subject.
- Fix: recognize only exact three/four-field records with a 40/64-hex object
  ID and positive decimal line/group positions. Optional enrichment now accepts
  only a zero-exit, non-timeout command result.
- Regression: literal 40/64-header plus `previous`/long metadata test, a real
  two-commit rewrite tool test, and failed/timed-out enrichment tests preserve
  actual commit identity and never render `fatal` output.

# 2026-08-05 (Issue #1195 git diff-range summary count)

- Cause: `parseStatSummary` tested the second token of a summary clause for
  `changed`. Git uses `1 file changed` and `N files changed`, so the marker is
  the third token and public `files_changed` was always zero while the stat
  string and insertion/deletion values were correct.
- Fix: accept a checked leading integer plus the exact `file/files changed`
  clause before assigning the aggregate. Insertion/deletion parsing, no-diff
  zero behavior, `stat_only`, command execution, and the result schema remain
  unchanged.
- Regression: red-first literal singular/plural/files-only/no-diff parser
  cases and a controlled two-commit tool fixture cover normal and `stat_only`
  output; real fake-provider API/SSE evidence records the corrected structured
  output and a same-conversation second user turn in
  `/private/tmp/gocode-1195-api-artifacts/ACCEPTANCE.md`.

# 2026-08-05 (Issue #1198 isolated harnessd skills directory)

- Cause: harnessd advertised `HARNESS_SKILLS_DIR` in `create_skill` errors but
  independently derived `$HARNESS_GLOBAL_DIR/skills` for loader, registry,
  watcher, and workflow skill inputs, so authored skills escaped isolation.
- Fix: resolve one trimmed absolute override at startup, reject relative input
  before listener acquisition, preserve unset fallback, and thread that exact
  path to all global-skill consumers.
- Regression: red-first catalog, watcher, and invalid-startup tests; a real
  fake-provider multi-message API/SSE daemon test creates, GETs, verifies, and
  follows up in one conversation while proving no legacy/default-global write.
- Verification: canonical-temp `./scripts/test-regression.sh` passed normal,
  race, and coverage (85.6% total; zero uncovered functions).
- Review repair: `cmd/harnesscli/tui/loadTUISkills` had retained a parallel
  `$HARNESS_GLOBAL_DIR/skills` derivation. It now mirrors trimmed
  absolute-only override resolution, loading no global skills for invalid
  input; focused normal/race tests prove override visibility and fail-closed
  local-catalog behavior before final amended regression.
# 2026-08-07 (Issue #1254 trusted child completion lifetime and policy)

- Cause: the private step-engine origin capability remained sufficient after
  its parent became terminal when a tool retained a `context.WithoutCancel`
  copy. Separately, public `RunMetadata.RunID` could select a parent policy
  for `RunForkedSkill`, allowing direct callers to inherit another run's
  system prompt, permissions, or profile.
- Fix: `RunForkedSkill` now trusts a private origin only while that exact
  runner parent remains non-terminal. Child depth, mandatory `task_complete`,
  and inherited policy all derive from that same private origin; untrusted or
  stale contexts use ordinary root defaults.
- Regression: deterministic coverage proves a cancelled parent plus captured
  `WithoutCancel` context cannot offer `task_complete`, public metadata cannot
  inherit privileged policy, and tampered metadata cannot redirect a trusted
  child away from its origin. Focused normal and race harness suites pass.

# 2026-08-07 (Issue #1231 default-registry filesystem/Git API/SSE acceptance)

- Scope: additive acceptance-only coverage for 15 default-registry local
  filesystem/Git tools; no production tool, endpoint, scheduler, GUI, or TUI
  behavior changed.
- Design: one temporary initialized Git repository and one real fake-provider
  harnessd conversation execute ordered write, later inspection, edit/patch,
  and Git verification turns. The test requires live `/v1/tools` availability,
  raw SSE event IDs, exact started/completed tool identity and arguments,
  non-error result shape, terminal run/conversation linkage, persisted
  conversation messages, independent external artifact checks, and fixture
  cleanup.
- Red-first evidence: the new acceptance entrypoint initially failed to compile
  because the driver was undefined; the implemented driver then ran against
  real harnessd. Early failures were corrected test expectations (write and
  patch return metadata; a range diff returns changed lines), not product-tool
  failures. The final focused normal and race acceptance paths pass.
- Review-evidence repair: the driver now binds selected rows to the canonical
  hash compiled from the running `/v1/tools` response (while retaining the raw
  response SHA-256), persists raw SSE/run/store/fixture and independent Git
  probe artifacts under one digest manifest, requires unique tool-call IDs and
  ordered start/completion/result events with terminal `run.completed`, and
  validates the four non-empty persisted assistant replies in order. Tool-call
  turns legitimately create empty assistant store records, so they are retained
  but not misrepresented as textual replies.
- Follow-up evidence repair: artifacts no longer use `t.TempDir`. A unique
  private child under `HARNESS_ISSUE_1231_ARTIFACT_ROOT` (or the private
  system-temp default) remains after test return, and the manifest records its
  path and content digests. The fixture is separately `RemoveAll`ed before
  manifest finalization with retained absence evidence. Call IDs are scoped to
  each `(runID, callID)` pair: duplicates within one run fail, while a later
  run may legitimately reuse an ID; focused regressions cover both cases.

# 2026-08-06 (Issue #1221 ordered fresh-PTY frame evidence repair)

- Cause: the fresh TUI acceptance scenario retained screens after all typed
  actions completed. Its fixed sleeps did not prove that the first reply,
  search, Escape, and second reply were individually observed before the next
  input, so the artifacts could not establish causal conversation ordering.
- Fix: use a Go-owned fixed-size PTY master instead of `script`, whose regular
  output could remain unobservable until teardown. One collector is its sole reader and seals each
  semantic milestone before the sequencer writes the next input. Each seal is
  immutable: VT screen, `[start,end)` prefix offsets, action/input digest,
  prefix/render digest, and durable conversation/run identities. Shutdown
  closes stdin, waits for `script`, drains the file, then seals the final frame.
- Regression: focused collector/launcher tests, full normal `ptyrunner`, the
  real fresh TUI conversation, and full race `ptyrunner` pass. Repository-wide
  regression and post-rebase verification remain required before merge.

# 2026-08-06 (Issue #1228 exact-width PTY frame repair)

- Cause: the VT buffer eagerly wrapped after a printable occupied column 100,
  then processed CRLF as a second advance; live `FIRST_REPLY` bytes were
  therefore absent from the interpreted frame. The collector also omitted its
  own deadline while waiting for a future update.
- Fix: DEC wrap-pending semantics retain the last-column cursor until the next
  printable; CRLF clears pending wrap. Collector waits now select their action
  deadline, without raw fallback or timeout expansion.
- Regression: exact-width, retained-failure-shape, action-deadline, helper
  coverage, repeated real fresh PTY, normal/race package, and serialized full
  regression pass (85.0% total coverage; zero uncovered functions).

# 2026-08-06 (Issue #1229 selective VT wrap-pending transitions)

- Cause: the #1228 exact-width state survived transitions whose terminal
  semantics reset it, while J3/TAB/SGR/combining and alternate-buffer modes
  require selective preservation rather than a blanket clear.
- Fix: cursor/visible erase/BS reset pending wrap; J3, TAB, SGR and combining
  preserve it. `1049` uses cleared alternate state and restores primary; `47`
  switches independent alternate state without global clearing.
- Regression: table-driven transition matrix covers cursor, erase, J3, BS/TAB,
  SGR/combining, and 1049/47 state; package normal/race passed before full gate.

# 2026-08-05 (Issue #1205 native acceptance owner design)

- Cause: the #1089 handoff validated caller-supplied native proof claims and could fetch a caller URL or execute a symlinked driver before ownership was established.
- Design decision before code: `harnessd` has an injected listener only in tests; an inherited-FD production contract is too broad here. The owner will reserve `127.0.0.1:0` only to select a port, release it immediately before spawn, then fail closed unless the recorded child PID owns the exact endpoint. This is not attach/reuse behavior.
- Guardrails: preflight failures have zero spawn/HTTP; every child/root/probe is private and attested; cleanup addresses recorded owned handles only; sentinel app/daemon PID and health must survive all injected failures.

# 2026-08-05 (Issue #1205 public native acceptance ownership)

- Cause: the public command and shell launcher still accepted caller-selected
  daemon URLs, drivers, manifests, and artifact roots, bypassing the owner
  foundation.
- Fix: the only public switch is `-foreground-opt-in`. The owner creates its
  private root, builds a fixed `harnessd` probe, launches a fake-provider
  daemon and an app connected only to that endpoint, and cleans up only those
  recorded children. The shell launcher forwards no caller-controlled runtime
  input.
- Regression: the exact red was an undefined `runLifecycle` selector; green
  coverage rejects `-harness-url` before lifecycle selection and accepts the
  opt-in route. Focused Go and full Swift tests pass. No actual app launch,
  AX/OCR interaction, or TCC proof was run or claimed.
# 2026-08-07 (Issue #1270 TUI replay-boundary fixture causality)

- Cause: `TestResumedConversationReplayBoundarySnapshotIncludesQueuedFuture`
  sent its post-boundary live event immediately after flushing the replay
  marker. Under `-race`, the test driver could receive that live event before
  the model had reduced the marker snapshot, leaving the intended
  snapshot-then-live contract dependent on scheduler timing.
- Fix: the local SSE fixture now waits for a one-shot release that is closed
  only after the rendered model visibly contains both snapshot entries. It
  then sends the same live assistant/terminal frames through the existing
  reducer. The wait also selects on request cancellation, so an earlier failed
  assertion cannot strand `httptest` cleanup. The six-second timeout and all
  exact-once/live assertions remain unchanged; a failed run logs the last
  causal fixture stage.
- Scope: test-only. Harness/API/TUI product source, persistence, protocol,
  configuration, and user-visible behavior are unchanged.
# 2026-08-07 (Issue #1273 native GUI proof root canonicalization)

- Cause: `SealArtifacts` passed lexical `ArtifactRoot` to containment then
  compared it with canonical artifact paths. A safe parent alias (`/var` versus
  `/private/var`) therefore made an owned regular file appear outside root.
- Fix: validate/canonicalize the directory root once at seal time, persist the
  canonical root, and use it for artifact containment and relative paths.
  `canonicalDirectory` still lstat-rejects a final root symlink; artifact
  symlink and regular-file protections remain unchanged.
- Regression: a portable symlinked-parent positive case was red first; focused
  nativegui normal and race suites are green alongside all existing negative
  proof cases.
# 2026-08-07 (Issue #1275 replay-boundary live proof)

- Cause: the acceptance stop predicate used simultaneous historic, queued, and
  live content in `View`, but auto-scroll correctly removes historic content
  in a short viewport. The test could call a correct reducer path a loss.
- Fix: gate fixture release on exact-once snapshot entries in `Transcript`,
  then stop only when decoded `live:3` assistant SSE has passed `Update` and
  rendered live content. Failure diagnostics expose snapshot/decode/reduction
  stages; the 6-second deadline and product source are unchanged.
- Regression: short viewport red captured before the repair; focused normal
  and race repeat evidence is green.
# 2026-08-07 (Issue #1272 fetched bootstrap source provenance)

- Cause: `scripts/init.sh` only fetched/resolved a base for new worktrees and
  used mutable `FETCH_HEAD`; reused task worktrees could therefore rebuild an
  old revision without any current-source record.
- Fix: resolve unqualified and `origin/` refs through freshly fetched
  `refs/remotes/origin/<branch>` commit IDs, validate exact SHA and explicit
  local refs, verify only fresh `git worktree add -b` creation, and write both
  source and actual HEAD revisions to `dev.env`. Reuse deliberately preserves
  task commits rather than resetting them.
- Regression: deterministic stale-local/newer-origin fixture covers reuse
  provenance plus explicit remote, SHA, and local ref source forms; focused
  package test is green before the full gate.

# 2026-08-08 (Issue #1281 API acceptance mapping contract)

- A live fake-provider daemon exposed 67 API tools at inventory hash
  `5230df4123f5c860c4849f5b44ab90f502b2212dec9dd8d1fce5fe2e78fcf1fa`.
  The checked-in manifest binds all 67 to exact ID, owner, resolver condition,
  and a future execution cohort. It is intentionally not an execution record.
- Validation now fails before reporting when any API item lacks a mapping or
  when its owner/condition drifts. The empty `cases` array remains deliberately
  red: the live command reports `mapped=67`, `planned=0`, `missing=67`, then
  exits non-zero. Future scenario cohorts cannot inherit a mapping as proof.

# 2026-08-08 (Issue #1281 daemon provenance binding repair)

- Cause: a hash-bound inventory manifest could still be pointed at an
  arbitrary listener, so it recorded operator intent rather than the actual
  lifecycle-owned daemon source.
- Fix: the manifest now pins `daemon_source_sha`; the API CLI requires the
  existing lifecycle `provenance.json`, validates source SHA, listener address,
  command path, and command SHA-256 before loading/reporting live inventory,
  and serializes accepted daemon identity in the report.
- Regression: a mismatched source SHA fails before mapping/inventory reporting;
  the CLI fixture proves decoding the lifecycle artifact envelope.

# 2026-08-08 (Issue #1281 executable provenance revalidation repair)

- Cause: the first provenance consumer trusted the stored command path and
  SHA-256, so a later artifact edit, symlink path, missing executable, or
  altered binary could evade the lifecycle's launch-time identity check.
- Fix: consumption now rejects relative or noncanonical command paths,
  canonicalizes the absolute path again, reads the canonical executable, and
  requires a freshly recomputed SHA-256 to equal the artifact before inventory
  or a coverage report is reached.
- Regression: explicit relative, symlink/noncanonical, missing-path, and
  digest-mismatch artifacts fail closed; a real file/digest fixture still
  reaches the API inventory gap report.
# 2026-08-08 — Issue #1282 stateful PTY evidence repair

- A manual `script(1)` probe persisted `LANE_B_FIRST_REPLY` but typed later commands before a collector-sealed reply frame, so its absent terminal text cannot diagnose product rendering. The repair is acceptance-only: reuse the existing Go-owned PTY collector and make every action causally wait for a rendered frame.

# 2026-08-08 (Issue #1280 shared same-daemon acceptance lifecycle)

- Cause: API/SSE inventory acceptance and PTY acceptance each owned a separate
  fake daemon, so a future scheduled-continuation proof could not establish a
  shared process and conversation identity.
- Fix: an internal lifecycle validates source SHA before spawn, reserves a TCP
  listener and transfers it as `HARNESS_LISTEN_FD=3`, provisions contained
  workspace/store paths, writes provenance, and derives API/SSE/PTY identity
  from one base URL. Close signals only its recorded child process group.
- Regression: a helper daemon proves health/SSE and matching PTY identity;
  mismatch launches nothing and an occupied unrelated listener stays healthy.
  This is tooling only: no runtime cron/callback or client behavior changes.

# 2026-08-08 (Issue #1280 independent-review repair)

- Cause: the initial lifecycle used a single-value completion channel. When
  readiness consumed an early child exit, cleanup could wait for its timeout.
  It also inherited parent `HARNESS_*`/`CRONSD_*` resource settings and bound
  provenance only to source SHA, not the executable actually launched.
- Fix: record child completion under a mutex then close a broadcast channel;
  remove inherited harness/cronsd settings and reserve lifecycle resource keys
  against caller override; resolve/canonicalize/hash the daemon command and
  serialize its identity with source/config provenance.
- Regression: direct owned-child termination proves two observers and Close do
  not block; `/usr/bin/true` proves bounded pre-ready exit; child environment
  proves parent resource values cannot leak; distinct executable artifacts have
  distinct provenance identities. Product behavior remains unchanged.

# 2026-09-05 (Issue #1379 documentation staleness repair)

- Cause: a read-only audit of README, CLAUDE.md, `docs/runbooks/`, and
  `docs/design/` (attached to epic #1369) found operator-facing docs that send
  an operator to a failing command or describe behavior that never existed:
  `docs/runbooks/mcp.md` described an unmounted `internal/mcpserver` HTTP/SSE
  server instead of the actual `internal/harnessmcp` POST-only `/mcp` (25
  tools); `docs/design/event-catalog.md` documented 23 of 79 events and said
  only two events were terminal (`run.cancelled` is also terminal); several
  runbooks kept the pre-#1328 `:8080` bind default, a reversed config-layering
  order, a nonexistent `profiles_dir`/`tools.allow` config key, and stale
  source paths (`tools/cron.go` vs `tools/deferred/cron.go`, `runner.go:2099`
  vs `:3152`); README/INDEX files linked three nonexistent runbooks and
  omitted two real ones plus four doc folders.
- Fix: corrected every doc:line claim against current code (verified with
  `rg`, not trusted from the audit alone), regenerated
  `docs/design/event-catalog.md` from `AllEventTypes()`/`IsTerminalEvent`,
  rewrote the MCP runbook to describe `/mcp` (`internal/harnessmcp`), the
  separate `harnessd --mcp` stdio tool-catalog server, and the client-role
  `/v1/mcp/servers` endpoints as three distinct surfaces. Documented, as
  current behavior ahead of merge, the three sibling-PR fixes this issue
  depends on: `workspace_path` honored on `POST /v1/runs` (#1372),
  `harnesscli input <run-id> "<question>=<answer>"` to answer a blocked run
  (#1374), and no default step cap (#1376).
- Regression: none required — this is a documentation-only change. Manual
  verification: `git diff --stat` shows only `*.md` files; every corrected
  route/flag/env var/tool/event/path was re-checked with `rg` against the
  code cited next to it; no automated doc link checker is defined in
  `docs/runbooks/documentation-maintenance.md`, so a manual link sweep over
  every touched file's relative doc references found none unresolved.

# 2026-08-08 (Issue #1280 P1 close/reap repair)

- Cause: Close sent SIGTERM before observing the broadcast completion state,
  which could signal a reaped and reused process group. After SIGKILL it read
  a possibly stale wait result and closed the daemon log without confirming
  the child had been reaped.
- Fix: Close first performs a nonblocking completion check; only a live child
  is signaled. Escalation waits a bounded second time for the closed completion
  channel and inspects wait state/closes the log only after confirmed reaping.
- Regression: a pre-reaped child records zero post-exit signals; a TERM-ignoring
  helper requires SIGKILL and proves Close returns only after `done` is closed.

# 2026-09-05 (Issue #1371 stale rewind expected_hash repair)

- Cause: `FinalizeRewindPoint` in `internal/harness/conversation_store_sqlite.go`
  set `expected_hash` only on the rewind point tied to the tool call that just
  ran. Any earlier point in the same conversation snapshotting the same path
  kept its stale hash from before a later agent edit, so `RestoreRewindPoint`
  compared the current (agent-written) content against that stale value and
  refused the older point as "modified outside the agent" even though nothing
  external had touched the file.
- Fix: `FinalizeRewindPoint` now looks up the point's `conversation_id` and
  refreshes `expected_hash` for every `rewind_file_snapshots` row with the same
  path across the whole conversation, not just the current point. A real
  external edit still fails the guard: no finalize runs after it, so the last
  agent-written hash is left in place and diverges from the on-disk content.
- Regression: `TestRestoreRewindPoint_OlderPointAfterAgentEditNotRefused` and
  `TestRestoreRewindPoint_OldestPointAfterEditChainNotRefused` prove an older
  point restores without `force` after one or several agent-only later edits;
  `TestRestoreRewindPoint_StillRefusesExternalEdit` and
  `TestFinalizeRewindPoint_DoesNotCrossContaminateOtherPaths` prove external
  edits are still refused and that the hash refresh stays scoped to the edited
  path, not the whole conversation.

# 2026-09-05 (Issue #1374 blocked-run hint pointed at a dead-end command)

- Cause: `reportRunBlocked` (cmd/harnesscli/main.go) printed the same
  "resume with: harnesscli continue <id> <prompt>" hint for every blocked
  event type. `continue` only works on a completed run; for
  `run.waiting_for_user` the server's `POST /v1/runs/{id}/continue` returns
  409 `run_not_completed` (`internal/server/http_runs.go`
  `handleRunContinue`), so an operator following the CLI's own advice hit a
  dead end and the run stayed blocked until the ask-user deadline expired.
- Fix: `blockedResumeHint` (cmd/harnesscli/exitcodes.go) now depends on the
  blocked event type: `run.waiting_for_user` hints the new
  `harnesscli input <run-id> "<question>=<answer>" [...]` subcommand (POSTs
  to `/v1/runs/{id}/input`); `tool.approval_required` /
  `plan.approval_required` hint the new `harnesscli approve|deny <run-id>`
  subcommands (POST to `/v1/runs/{id}/approve|deny`; these server routes
  already existed, only the CLI commands were missing). `harnesscli continue`
  itself now catches its 409 and looks up the run's live status
  (`continueConflictHint`) to print the correct next command instead of
  repeating the "continue" invocation that just failed.
- Regression: `TestBlockedRunHint_InputCommandActuallyResolvesTheRun` drives
  a real headless `run()` into `run.waiting_for_user`, reads the printed
  hint, and then literally invokes the command it names against the same
  run — proving the hint and the `input` command stay wired together, not
  just that the hint text looks right in isolation.
# 2026-09-05 (Issue #1373 shutdown never cancelled in-flight runs)

- Cause: `cmd/harnessd/main.go`'s shutdown path only ever called
  `httpServer.Shutdown(ctx)` with a 10s drain timeout. Nothing invoked the
  runner's own cancellation machinery, so a run blocked in a long tool call
  (e.g. bash `sleep 30`) stayed `running` in the store after the process
  exited, and the bash child was orphaned — reparented, not killed, since
  process exit does not touch children it spawned. The bash tool's own
  process-group kill-on-cancel (`internal/harness/tools/exec_group_unix.go`'s
  `cmd.Cancel`) already worked correctly; the gap was that shutdown never
  triggered cancellation at all.
- Fix: added `Runner.CancelAllActiveRuns(ctx)`, which cancels every
  non-terminal run via the same path `POST /v1/runs/{id}/cancel` already uses
  (`CancelRun`), then waits up to `ctx`'s deadline for each to reach
  `RunStatusCancelled`. Wired into `cmd/harnessd/main.go`'s shutdown sequence
  with a bounded 3s context, placed before `httpServer.Shutdown` and before
  the run store closes, logging the count of runs cancelled.
- Regression: a runner-level test starts a run blocked in a real bash tool
  call, calls `CancelAllActiveRuns`, and asserts the run reaches
  `RunStatusCancelled` and the bash child's process is actually dead
  (`internal/harness/runner_shutdown_cancel_test.go`). A daemon-level test
  drives the real binary path end to end — POST a run, confirm the bash
  child is running via a pid file, send the shutdown signal, assert the
  child is dead, then restart against the same SQLite run store and confirm
  the run persisted as `cancelled` rather than stuck `running`
  (`cmd/harnessd/shutdown_cancel_regression_test.go`). Verified the daemon
  test fails for the right reason (orphaned child, `still alive after daemon
  shutdown`) when the main.go integration is reverted.
- Out of scope (tracked separately, issue #1356): store-close ordering under
  a live SSE subscription, and handler-context cancellation for requests held
  open past shutdown.
# 2026-09-05 (Issue #1375 durable /events and /summary fallback)

- Cause: `GET /v1/runs/{id}/events` and `GET /v1/runs/{id}/summary` only ever
  asked the in-memory `Runner`, so a daemon restart with the same
  `HARNESS_RUN_DB`/`HARNESS_CONVERSATION_DB` made both 404 "run not found" for
  any historical run, even though `GET /v1/runs/{id}` already fell back to the
  run store (`handleGetRun`) and `GET /v1/conversations/{cid}/events` replayed
  the same run's durable events.
- Fix: both routes now fall back to the persistent store when the runner has
  no live state. `/events` replays the run's durable event log via the
  existing per-run `store.Store.GetEvents(runID, afterSeq)` query (honoring
  `Last-Event-ID` the same way the live path does) and closes the stream after
  replay, since a store-only run is necessarily terminal. `/summary` applies
  the same completed/failed status gate and steps/tool-call scan as
  `Runner.GetRunSummary`, reading usage/cost totals from the run's last
  `usage.delta` event payload (`cumulative_usage`/`cumulative_cost_usd`/
  `cost_status`) rather than `store.Run`'s `UsageTotalsJSON`/`CostTotalsJSON`
  columns, which the writer never actually populates today. Event IDs and wire
  shapes are unchanged.
- Regression: a run completed against one `Runner`+store, then served by a
  second, unrelated `Runner` sharing the same store (simulating a restart),
  proves `/events` replays to `run.completed` and `/summary` matches the live
  totals (steps, prompt/completion tokens, cost, tool calls, cache hit rate).
  Further regressions cover `Last-Event-ID` resumption against the durable
  replay (no repeated events) and the still-running status gate returning 409
  instead of a misleading 200 for a store-only run that has not finished.
# 2026-09-05 (Issue #1377 macOS temp-dir canonicalization + nightly coverage-gate green)

- Cause 1: `TestRunReportsLiveInventoryGapFromManifest` (cmd/acceptance-api-sse)
  built its fixture `commandPath` from `t.TempDir()` without resolving
  symlinks. On macOS `/tmp` is a symlink to `/private/tmp`, so
  `validateProvenanceExecutable` (internal/acceptance/apisserunner/manifest.go)
  rejected the path as non-canonical. Checked sibling tests in
  `cmd/acceptance-*` and `internal/acceptance` for the same shape:
  `apisserunner/runner_test.go` already resolves symlinks on its command path,
  and `scheduledlifecycle` canonicalizes via `executableIdentity`, so only the
  one test needed the fix.
- Fix 1: canonicalize the temp dir with `filepath.EvalSymlinks` before joining
  the fixture command path.
- Cause 2: the nightly `test-regression` coverage gate
  (`go run ./cmd/coveragegate -min-total=80.0`) was red on seven zero-coverage
  functions, all real code with no test caller: `messagebubble/markdown.go:67
  ResolveStyle` (only the unexported `resolveGlamourStyle` had a caller);
  `nativegui/platform_other.go:12/15/18` (the `!darwin || !cgo` stub, which is
  the compiled implementation on the Linux CI runner, had no test under its own
  build tag); `harness_client.go:399/435 SteerRun`/`ListProviders` (simply
  untested, unlike every sibling `HarnessClient` method); and
  `credref.go:44 execSecurityCommand.Run` (only exercised incidentally on
  darwin by a real, un-faked `ResolveCredential(keychain:...)` call, because
  `KeychainAvailable()` is true there — on Linux CI it is false, so the real
  adapter's `Run` never executes).
- Fix 2: added one meaningful test per function rather than lowering the gate
  or adding an exclusion. The `credref.go` fix constructs `execSecurityCommand`
  directly with a portable binary (`echo`/`false`) instead of `"security"`, so
  it is exercised independent of `KeychainAvailable()`/platform. The
  `platform_other.go` fix mirrors the existing `platform_darwin_test.go`
  pattern under the sibling build tag and was verified with `CGO_ENABLED=0` on
  this (darwin) dev machine.
- Regression: all seven functions now report 100.0% in
  `go tool cover -func`; no gate threshold or exclusion changed.
# 2026-09-05 (Issue #1381 docs index and dead-link repair)

- Cause: `docs/investigations/` (193 files) and `docs/implementation/` (40
  files) had no `INDEX.md` despite AGENTS.md:56 requiring one; `docs/testing/`,
  `docs/research/`, and `docs/plans/` indexes listed a small fraction of their
  folders' files; `docs/plans/INDEX.md` linked a nonexistent
  `.context/harness-reliability/tracker.md`; `docs/INDEX.md` omitted
  `implementation/`, `investigations/`, `process/`, `ux-paths/`; three
  investigation files and eight links in `docs/logs/engineering-log.md`
  pointed at machine-specific `/Users/dennisonbertram/...` absolute paths.
- Fix: added `scripts/generate-doc-index.sh` (25 lines) to mechanically list a
  folder's files with each file's first Markdown heading as the title;
  regenerated `docs/testing/INDEX.md`, `docs/research/INDEX.md`,
  `docs/plans/INDEX.md`, and created `docs/investigations/INDEX.md` and
  `docs/implementation/INDEX.md` with it; added the four missing folders to
  `docs/INDEX.md`; rewrote the absolute-path links in the three investigation
  files and this log's own entries to repo-relative links (all targets exist).
  No historical prose content was rewritten and no files were archived or
  deleted — archive candidates from the parent audit (#1369) are listed in the
  PR body for the owner to accept or reject.
- Regression: a `for`-loop link check resolves every `[text](path)` link in
  the touched `INDEX.md` files, the three corrected investigation files, and
  this log against `test -e` (758 links, 0 dead). This is documentation only:
  no runtime, API, or test behavior changed.

# 2026-09-05 (Issue #1370 rewind message truncation and live mirror repair)

- Cause: `RestoreRewindPoint` truncated `conversation_messages` by comparing
  the rewind point's run-local tool-call step (`RewindPoint.Step`, set in
  `runner_step_engine.go`) against `conversation_messages.step`, a
  conversation-wide message index shared across every run on that
  conversation. Rewinding to a point in a conversation's second (or later)
  run deleted the first run's later messages too. Separately, the runner's
  in-memory conversation mirror (populated at each run's completion and
  served by `ConversationMessagesSnapshot`/`GET /messages`) was never
  invalidated by rewind, so the live daemon kept serving pre-rewind history
  and the next run re-persisted it over the truncated DB.
- Fix: `RewindPoint` gained `MessageBoundary`, the conversation-wide index of
  the assistant message carrying the rewound tool call, recorded at capture
  time in the step engine (see the followup entry below for a correction to
  this value). `RestoreRewindPoint` truncates by `step >= MessageBoundary`
  when it is recorded; points captured before this field existed
  (`MessageBoundary == 0`) fall back to the legacy step comparison with a
  logged warning instead of silently over-deleting.
  `Runner.InvalidateConversationHistory` drops the in-memory mirror entry
  for a conversation; the rewind HTTP handler calls it immediately after a
  successful restore.
- Regression: a store-level test proves a two-run conversation's rewind keeps
  run 1's messages plus run 2's user prompt and tool-call message,
  truncating only what follows; an HTTP-level test drives two real runs
  through the handler and proves `GET /messages` reflects the truncation
  immediately; a third test drives a real three-run `Runner` flow and proves
  the run following a rewind does not resurrect the truncated tool result or
  final answer in its LLM request. Related: #1303 describes the same
  resurrection symptom from a different angle (workspace population, TUI
  JSON tags) and remains open.

# 2026-09-05 (Issue #1370 followup: dangling assistant tool_calls after restore)

- Cause: the boundary above (`len(messages)` at capture time) pointed just
  past the assistant message carrying the rewound tool call, so a restore
  kept that assistant message while deleting only its tool result. Real
  providers (OpenAI et al.) reject an assistant message with `tool_calls`
  that isn't immediately followed by matching tool messages; the fake/stub
  providers this repo's tests use do not enforce that, so the bug shipped
  with green tests. Caught in review before merge stabilized.
- Fix: `MessageBoundary` is now the conversation-wide index of the assistant
  message itself (`assistantToolCallIndex`, captured immediately after that
  message is appended in `runner_step_engine.go`), so restore deletes that
  message and everything after it. Parallel tool calls issued in one
  assistant turn all capture the same index, since they share one assistant
  message. `RestoreRewindPoint`'s truncation query and its
  unset-boundary fallback are unchanged; only the captured value changed.
- Regression: a new test drives a real single-turn run with two parallel
  tool calls through the actual capture path and restores using either
  call's point, asserting both points share one boundary, the last
  persisted message is never an assistant message with tool_calls, and no
  tool message lacks a preceding assistant tool_calls entry for its ID.

# 2026-09-05 (Issue #1370 followup: rewind_points prune deleted unrelated older points)

- Cause: found by live verification on main after #1378 merged.
  `RestoreRewindPoint`'s future-point pruning query also compared
  `point.Step`, the same run-local tool-call counter whose misuse in message
  truncation this issue already fixed. Step restarts every run, so run 2's
  edit point (step 1 within run 2) and run 1's write point (also step 1
  within run 1, an unrelated run) collided: restoring the edit point deleted
  the write point too, and a later restore to that still-valid older point
  returned "not found".
- Fix: pruning now uses `MessageBoundary` when recorded -- a point is
  superseded only by a strictly greater boundary, or an equal boundary
  (parallel tool calls sharing one assistant message) captured later
  (`created_at`); the target is always excluded. Falls back to the legacy
  step predicate only when the target has no recorded boundary, matching
  the existing message-truncation fallback.
- Regression: `TestRestoreRewindPoint_PruneKeepsOlderPointsFromEarlierRuns`
  drives two real runs, restores to run 2's edit point, then restores to
  run 1's write point and asserts it still succeeds.

# 2026-09-06 (Issue #1395 trailing system-role message empties DeepSeek responses)

- Cause: the harness appends the per-turn `<runtime_context>` block as a
  second, trailing `system`-role message after the user/tool history
  (`buildTurnMessages` in `internal/harness/clone.go`, content from
  `internal/systemprompt/runtime_context.go`), so the cacheable
  system+tools+history prefix stays stable across turns. `mapMessages` in
  `internal/provider/openai/client.go` forwarded every role verbatim onto
  the OpenAI-compatible chat-completions wire. DeepSeek models reached
  through OpenRouter return an empty assistant message (1-3 completion
  tokens, `finish_reason: stop`) whenever the *last* message in the request
  has role `system`, so every run died within three turns with
  `max_empty_responses`. A logging-proxy replay of the captured request
  confirmed the same body succeeds 3/3 when only the trailing message's
  role is changed to `user`; `gpt-4.1-mini` tolerates either role, which is
  why the fake/OpenAI paths never surfaced this (issue #1395 has the full
  replay table).
- Fix: `mapMessages` now sends any `system`-role message that is not the
  first message (index 0) as role `user` instead, leaving content and
  position unchanged. The leading system message is untouched. This keeps
  the fix scoped to the OpenAI-compatible wire mapper; `buildTurnMessages`
  and the harness's internal message model are unchanged, and the
  Anthropic client (`extractSystem`, which already hoists every system
  message into the top-level `system` parameter regardless of position) is
  unaffected. `prompts/compiled/system_prompt.txt` comments updated to
  describe the wire role split between providers.
- Regression: `TestMapMessagesNonLeadingSystemSentAsUser` and
  `TestCompleteWireBodyTrailingSystemAsUser` cover the mapper contract
  directly (leading system stays `system`, trailing system becomes `user`,
  content unchanged); `TestMapMessagesLeadingOnlySystemUnchanged` guards the
  single-leading-system case already worked. `TestRunnerRuntimeContextReachesOpenAIWireAsUser`
  drives a real `harness.Runner` through the real `openai.Client` end to
  end and asserts the wire body's trailing message is `user` -- confirmed to
  fail with the pre-fix mapper by re-running it against the reverted
  client.go. Live verification against DeepSeek via OpenRouter is a
  follow-up for whoever holds the API key; this PR only proves the
  fake/unit/integration paths.

# 2026-09-06 (Issue #1397 bash sandbox network policy — SECURITY-RELEVANT DEFAULT CHANGE)

- Prior behavior: `SandboxScopeWorkspace` and `SandboxScopeLocal` unconditionally
  denied outbound network from the `bash` tool (`(deny network*)` in the darwin
  seatbelt profile, `--unshare-net` always in the linux bwrap invocation), with
  no way for a caller to opt back in. A run that legitimately needed to install
  a dependency or call an API from a sandboxed `bash` call had no way to do so
  short of dropping to `SandboxScopeUnrestricted`, which also drops filesystem
  confinement.
- Change: `harness.PermissionConfig` gains a third axis, `Network` (json
  `network`, values `""`/`"allow"`/`"deny"`, default `"allow"`), mirrored at
  the tools layer as `tools.NetworkPolicy` with a context key
  (`ContextKeyNetworkPolicy`), `WithNetworkPolicy`, and
  `NetworkPolicyFromContext`, set alongside the existing sandbox-scope context
  value wherever the runner sets up a tool call's execution context
  (`runner_step_engine.go`). **SECURITY-RELEVANT DEFAULT CHANGE:** workspace
  and local sandbox scopes now allow outbound network by default; callers that
  need the old always-deny behavior must set `permissions.network` to
  `"deny"` explicitly. `SandboxScopeUnrestricted` is unaffected (it was never
  network-confined).
- Gotcha found while implementing the darwin side: seatbelt's `(deny default)`
  means every operation — including network — is denied unless explicitly
  allowed. The first implementation attempt just omitted the `(deny network*)`
  line for the allow case and left it there; that still left curl unable to
  resolve DNS (`exit 6`) because omitting a deny rule under `(deny default)`
  does not become an allow. The fix emits an explicit `(allow network*)` line
  for the allow case (and an explicit `(deny network*)` for deny, which is
  redundant with the default but kept for clarity/robustness against a future
  default-policy change). Confirmed directly with `sandbox-exec` against a
  real host (`https://proxy.golang.org`): `http_code=200` under an
  `(allow network*)` profile, `exit=6` ("could not resolve host") under
  `(deny network*)`.
- `SandboxExecResult` gains a `NetworkPolicy` field so the bash tool result map
  carries a `sandbox_network` key reporting the policy actually applied,
  independent of the mechanism (`seatbelt`, `bubblewrap`, `none`, or
  `unavailable`).
- The permissions statement injected into the model's context
  ("Permissions for this run: sandbox=%s, approval=%s, network=%s.") now
  includes the network axis and, when denied, an explicit warning that
  dependency installs will fail and the model should report the blocker
  rather than substitute a design. It is now appended to every turn's wire
  message list (not persisted into conversation history, unlike the existing
  continuation-changed notice) so it reaches the model starting on turn one —
  previously this line only appeared on a continuation whose permissions
  changed from the source run, never on a fresh run's first turn.
- `harnesscli` gains `--sandbox` and `--network` flags on the one-shot run
  path, populating `permissions` on the run-create request only when either
  flag is set (both omitted → server defaults, matching the new default
  above).
- Regression: `TestRedTeam_SandboxNetwork_DefaultPermissionsAllowCurl` drives a
  real bash tool call through the full runner with no explicit `Permissions`
  and asserts curl is not rejected; `TestJobManagerRunForegroundReportsSandboxNetworkInResult`
  asserts the `sandbox_network` result field matches the applied policy for
  both allow and deny under a real OS-level sandbox; a live integration test
  (`TestSandboxWorkspaceScopeNetworkPolicyLiveCurl`) curls
  `https://proxy.golang.org` through the actual seatbelt sandbox and asserts
  success under allow, failure under deny.

# 2026-09-06 (Issue #1399 sandbox toolchain cache dirs)

- Prior behavior: `SandboxScopeWorkspace` confined writes to the workspace
  root alone (plus a handful of device nodes on darwin). Any language
  toolchain invoked via `bash` under workspace scope — `go build`/`go test`,
  `npm install`, `cargo build` — writes to its build cache and a scratch
  directory under the process temp dir or a per-user cache dir, neither of
  which is the workspace, so every such command failed with
  `failed to initialize build cache ... operation not permitted` (darwin
  seatbelt) or the equivalent bwrap "Operation not permitted" (Linux,
  since `/tmp` was only ever read-only bound there).
- Change: `toolchainWritableDirs()` (`internal/harness/tools/toolchain_dirs.go`)
  computes, fresh on every call, the set of per-user temp/cache roots a
  toolchain needs: `os.TempDir()`, `os.UserCacheDir()`, `~/.cache` (created
  if missing), `$GOCACHE` (if set and existing), the Go module cache
  (`$GOMODCACHE`, else `$GOPATH/pkg`, else `~/go/pkg`, created if missing),
  `~/.npm`, `~/.cargo/registry`, `~/.cargo/git`. Every entry is
  symlink-canonicalized (`filepath.EvalSymlinks`) the same way the workspace
  root already is, so it lines up with the kernel-resolved paths darwin's
  seatbelt `(subpath ...)` predicate and Linux's bwrap binds actually match
  against. `$HOME` itself is never opened up — only these specific narrow
  subdirectories, and only the two noted above are created rather than
  merely detected.
- `seatbeltProfile` (darwin) now emits an additional
  `(allow file-write* (subpath ...))` line per directory for
  `SandboxScopeWorkspace` only (`SandboxScopeLocal` already has a blanket
  `(allow file-write*)`, so it does not need these). `buildSandboxedCommand`
  (Linux) now `--bind`s each directory read-write, after the read-only root
  bind and before the workspace bind; `/tmp` is deliberately no longer
  explicitly `--ro-bind`ed there, since `os.TempDir()` is frequently exactly
  `/tmp` (TMPDIR unset) and a read-only bind of the same path would shadow
  the later read-write bind for that directory. `/var/tmp` stays read-only
  unless it happens to be one of the toolchain writable dirs.
- `checkWorkspaceScopeCommand`'s string heuristic (defense-in-depth, not the
  primary OS-level enforcement) now treats an absolute-path token as
  in-scope if it falls under the workspace OR any `toolchainWritableDirs()`
  entry, reusing `canonicalizePathAllowingMissing`/`pathWithinRoot` from
  `common_paths.go` rather than re-deriving symlink-safe containment
  checking. It also now expands a leading `~/` token against the home
  directory before the containment check, so `~/.ssh/id_rsa` is still
  correctly rejected (previously `~`-prefixed tokens were silently skipped
  entirely, since `filepath.IsAbs` does not recognize `~` as absolute).
- `SandboxExecResult` gains a `WritableDirs []string` field so the bash tool
  result map carries a `sandbox_writable_dirs` key (populated only for
  workspace scope, where it's meaningful); the "Permissions for this run:
  ..." notice injected into the model's context (issue #1397's line) now
  appends "For this run, temp and per-user cache directories are writable."
  when `sandbox=workspace`, so the model does not misdiagnose a legitimate
  cache-dir write as a permissions problem it must route around.
- Existing test fallout, both expected consequences of legitimately widening
  what workspace scope permits: `TestSandboxWorkspaceScopeBlocksWriteOutsideWorkspaceAtOSLevel`
  and `TestSandboxWorkspaceScopeEnforcesFilePaths` both used to prove an
  "outside the workspace" write was rejected by writing under `os.TempDir()`
  or a sibling of the workspace's `t.TempDir()` parent (itself under
  `os.TempDir()`) — both are now legitimately writable, so both tests were
  repointed at `/var/tmp` (never a toolchain writable dir) to keep proving a
  real boundary exists. `TestCheckSandboxCommandWorkspaceScope`'s
  cross-platform-ambiguous `"ls /tmp"` case (would flip from rejected to
  accepted on any host where `TMPDIR` is unset, since `os.TempDir()` then
  equals literal `/tmp`) was replaced with `"ls /usr/local/bin/x"`, one of
  the contract's explicit still-rejected examples.
- Regression/integration: `TestSandboxWorkspaceScopeToolchainCanBuildAndTest`
  creates a throwaway Go module inside the workspace and runs
  `go env GOCACHE && go build ./... && go test ./...` through the real
  darwin seatbelt sandbox with no env var overrides — confirmed to fail with
  the pre-fix code (`operation not permitted` creating the build work dir)
  and pass after; `TestSandboxWorkspaceScopeAllowsMktempDir` does the same
  for `mktemp -d`. Both are skipped when no OS-level sandbox mechanism is
  available; the Linux bwrap bind-flag test
  (`TestBuildSandboxedCommandLinuxIncludesToolchainWritableDirs`) cannot
  execute on this darwin worktree and is verified by
  `GOOS=linux go vet ./internal/harness/tools/` for syntax/type correctness
  only — it needs a real Linux CI run to prove behavior.

# 2026-09-06 (Issue #1409 viewport horizontal clamp truncated ANSI-styled transcript lines)

- Symptom: streamed markdown assistant output in the TUI transcript viewport
  sometimes lost trailing visible text — a bullet item like "Created
  `calc.go` with an Add function" could render with "with an Add function"
  (or more) silently missing — but every unit test for the viewport passed.
- Cause: `viewport.Model.View()`
  (`cmd/harnesscli/tui/components/viewport/model.go`) clamped each visible
  line to the viewport width by rune count: `runes := []rune(line); if
  len(runes) > m.width { line = string(runes[:m.width]) }`. Under a real
  color profile, glamour (via `messagebubble.RenderMarkdown`) emits ANSI
  escape sequences — e.g. a truecolor code-span background is ~20 extra
  bytes — and each escape byte counts as a rune even though it occupies zero
  terminal cells. A line with 42 visible cells but 62 runes hit the
  rune-count budget at rune 40, which fell inside the escape sequence itself,
  so the slice cut mid-escape and dropped everything after it. Under the
  `ascii`/`notty` glamour style used when `os.Stdout` is not a real terminal
  (the case for every test process, and for `messagebubble`'s own
  `stdoutIsTerminal` probe), there are no escape bytes, so rune count equals
  cell width and the clamp never mis-cut — which is why the existing test
  suite never caught this.
- Fix: replaced the rune slice with
  `github.com/charmbracelet/x/ansi`'s `Truncate(line, m.width, "")`, which is
  ANSI-aware (it will not cut inside an escape sequence) and is a no-op when
  the line already fits within `m.width`. `github.com/charmbracelet/x/ansi`
  was already an indirect dependency (transitively required by the existing
  charm ecosystem deps); `go mod tidy` after adding the import promoted it —
  along with two other packages already imported directly elsewhere in the
  tree but previously mislabeled indirect (`github.com/creack/pty`,
  `github.com/coder/acp-go-sdk`) — to the direct `require` block in `go.mod`.
  No dependency versions changed (`go.sum` is byte-for-byte unchanged).
- Verification: a new `viewport`-package test
  (`TestTUI1409_ANSIStyledLineNotTruncatedByRuneCount`) feeds the viewport a
  line with a real ANSI escape sequence (42 visible cells, 62 runes) at
  width 40 and asserts the trailing marker word survives and no rendered
  line exceeds width via `lipgloss.Width`; it fails against the pre-fix
  clamp (marker dropped) and passes after. A second, model-level regression
  test in `cmd/harnesscli/tui`
  (`TestTUI1409_ModelViewPreservesANSIStyledTranscriptLine`) drives the same
  fixture through the fully assembled `tui.Model` (constructed via `New` +
  a `tea.WindowSizeMsg`, injected via the model's embedded `m.vp`) and
  asserts the same invariant over the complete `Model.View()` frame
  (header/separators/viewport/input/status bar), not just the isolated
  viewport package — confirmed to fail the same way if the fix in
  `model.go` is reverted. Glamour could not be made to emit real ANSI
  escapes in this test process even with `lipgloss.SetColorProfile
  (termenv.TrueColor)` forced (verified experimentally): `messagebubble`'s
  own `stdoutIsTerminal` probe is a syscall-level check on `os.Stdout`'s fd,
  independent of lipgloss's global color profile, so it always resolves to
  the escape-free `notty` style outside a real terminal. That probe lives in
  `messagebubble`, outside this fix's file scope, so the model-level test
  injects a realistic pre-rendered ANSI-styled line directly rather than
  routing it through glamour. `go test ./cmd/harnesscli/tui/... -race`,
  `go vet ./cmd/harnesscli/tui/...`, and `go build ./cmd/... ./internal/...`
  are all clean.
