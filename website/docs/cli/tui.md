---
title: "The Interactive TUI"
sidebar_label: "TUI"
sidebar_position: 3
---

import { Callout, Steps, Step, Tabs, TabsList, TabsTrigger, TabsContent, Card, CardHeader, CardTitle, CardContent, Badge } from '@site/src/components/ui';

The **TUI** (Terminal User Interface) is the full-screen, interactive mode of `harnesscli`. Instead of firing a single prompt and exiting, you enter a persistent chat session that keeps a live conversation history, renders tool calls as they execute, and lets you switch models, apply profiles, and manage sessions — all without leaving your terminal.

Think of it as the difference between a shell one-liner and an IDE: the one-shot `--prompt` mode is great for scripting; the TUI is great for thinking out loud with an agent over many turns.

---

## Launching the TUI

Pass the `--tui` flag to `harnesscli`. The TUI requires a real terminal on stdout — it will not start inside a pipe.

```bash
# Simplest launch — connects to harnessd at http://localhost:8080
harnesscli --tui

# Explicit server address
harnesscli --tui --base-url http://127.0.0.1:8080

# Start with a specific model pre-selected
harnesscli --tui --base-url http://127.0.0.1:8080 --model gpt-4.1
```

<Callout type="info">
If you use `go-code` (the shell wrapper), running `go-code` with no arguments launches the TUI automatically after starting `harnessd` if needed. See [The go-code Command](/docs/cli/go-code-wrapper) for details.
</Callout>

If stdout is not a TTY — for example, when you pipe output or run inside CI — the program exits immediately with:

```
--tui requires a terminal; pipe output or use without --tui for streaming mode
```

The TUI always uses the **alternate screen buffer** (`tea.WithAltScreen`) and **truecolor** rendering. These are hardcoded when launched from `harnesscli` and are not user-configurable.

<Callout type="warning">
The `TUIConfig.Theme` field exists in the config struct but is not read by any code in the current release. Theme-based styling has no effect.
</Callout>

---

## Screen layout

The TUI divides your terminal into fixed regions stacked top-to-bottom:

```
┌─────────────────────────────────────────┐
│  Viewport (message bubbles, tool cards) │  ← fills remaining height
├─────────────────────────────────────────┤
│  Thinking bar   (while LLM is working)  │  ← optional
│  Interrupt banner   (Ctrl+C confirm)    │  ← optional
│  Slash-complete dropdown                │  ← optional
│  Input area                             │  ← min 3 lines, max 8
├─────────────────────────────────────────┤
│  Status bar                             │  ← 1 line
└─────────────────────────────────────────┘
```

Six lines are reserved at all times (status bar + two separators + minimum input height). The viewport receives whatever is left, with a floor of three lines. The minimum terminal width is 20 columns.

### Status bar

The status bar (bottom line) shows several segments. When the terminal is narrow, lower-priority segments are dropped first:

| Priority | Segment | Example |
|---|---|---|
| 1 | Model name (bold, up to 24 chars) | `gpt-4.1` |
| 2 | Running indicator | `...` |
| 3 | Cumulative cost | `$0.0042` |
| 4 | Permission mode (hidden when `default`) | `[plan]` |
| 5 | Git branch | `(main)` |
| 6 | Workspace path (up to 20 chars) | `~/projects/myapp` |
| 7 | MCP failure count | `2 MCP fail` |

Transient messages (command confirmations, errors) replace the status bar text for three seconds.

---

## Keybindings

<Callout type="info">
Press `?` or `Ctrl+H` at any time to open the built-in help dialog with the full keybinding list.
</Callout>

| Key | Action |
|---|---|
| `Enter` | Submit message or confirm selection |
| `Shift+Enter` / `Ctrl+J` | Insert a newline in the input |
| `Up` / `Ctrl+P` | Input history up, or scroll viewport |
| `Down` / `Ctrl+N` | Input history down, or scroll viewport |
| `PgUp` | Scroll viewport up half a screen |
| `PgDn` | Scroll viewport down half a screen |
| `/` | Open slash-command autocomplete dropdown |
| `@` | Insert `@` to begin a file-path attachment |
| `Tab` | Complete a slash command or file path |
| `Ctrl+O` | Expand/collapse active tool card, or toggle plan mode when idle |
| `Ctrl+E` | Open `$EDITOR` for multi-line prompt editing |
| `Ctrl+S` | Copy last assistant response to clipboard |
| `Ctrl+G` | Steer the active run: inject the input-box text into the running turn (see below) |
| `Ctrl+U` | Clear the input (when no overlay is open) |
| `Ctrl+C` | Two-stage interrupt (see below) |
| `Esc` | Multi-priority close: dropdown → overlay → cancel run → clear input |
| `?` / `Ctrl+H` | Open help dialog |

### Two-stage interrupt

Accidentally pressing `Ctrl+C` mid-run would be frustrating, so the TUI requires confirmation:

<Steps>
  <Step title="First Ctrl+C">
    An interrupt banner appears: **"⚠  Press Ctrl+C again to stop, or Esc to continue"**. The run keeps going.
  </Step>
  <Step title="Second Ctrl+C">
    The run is cancelled. The status bar shows "Interrupted".
  </Step>
  <Step title="Esc (while banner is showing)">
    The banner is dismissed. The run continues. Status shows "Interrupt cancelled".
  </Step>
</Steps>

Pressing `Esc` when no banner is showing cancels the run directly (without the two-step confirmation).

When idle (no run active), `Ctrl+C` quits the TUI immediately.

### Mid-turn steering

While a run is in flight, type corrective input and press `Ctrl+G` to inject it into the running turn — the run keeps going (it is not cancelled or restarted). The steered text is delivered to the agent as a user message at the **next step boundary**, not instantaneously, so the current tool call or model response finishes first.

- The input box clears on send and the status bar confirms with "Steering sent".
- The transcript shows the steered message with a `steered ⟂` marker once the server confirms it, distinguishing it from a typed prompt.
- Steering is queued server-side with a buffer of 10 pending messages per run; if the buffer is full or the run has already finished, the status bar says so and nothing is dropped silently.
- With no active run or an empty input box, `Ctrl+G` is a no-op with a status hint.
- The same path is available outside the TUI as `harnesscli steer <run-id> <prompt>`.
- `Ctrl+S` (copy) and `Esc` (cancel) are unchanged.

---

## Slash commands

Type `/` to open the autocomplete dropdown. `Tab` completes to the common prefix; `Enter` selects a command. Commands are case-insensitive.

| Command | Description |
|---|---|
| `/model` | Open the model picker |
| `/profiles` | View and select a capability profile |
| `/sessions` | Browse and resume past sessions |
| `/title [text]` | Set or show the current session's title (`/title clear` removes it). Shown in the status bar and the `/sessions` picker, persisted across restarts |
| `/init [confirm]` | Generate an `AGENTS.md` for the current workspace via a harness run. If `AGENTS.md` already exists, run `/init confirm` to overwrite it |
| `/new` | Start a fresh conversation (resets conversation ID) |
| `/search <query>` | Search the current session transcript |
| `/history <query>` | Search across stored session metadata |
| `/export` | Export the conversation to a Markdown file |
| `/stats` | Show cumulative cost and token statistics |
| `/context` | Show context window usage |
| `/keys` | Manage provider API keys |
| `/runs` | List recent harness runs |
| `/cancel [run-id]` | Cancel the active run (or a specific run by ID) |
| `/replay <run-id-or-path>` | Replay a recorded run |
| `/resume <run-id> <prompt>` | Continue a completed run with a new prompt |
| `/subagents` | View active subagent processes |
| `/permissions` | View the current session's tool permissions |
| `/attach` | Attach file context with `@path` tokens |
| `/doctor` | Show local harness diagnostic commands |
| `/help` | Show the help dialog |
| `/clear` | Clear the conversation history and viewport |
| `/quit` | Quit the TUI |

---

## Model picker (`/model`)

The model picker is a two-level browser.

**Level 0 — provider list:**
Anthropic, DeepSeek, Google, Groq, Kimi, OpenAI, Qwen, xAI

**Level 1 — model list for the selected provider.**

Navigation works with `Up`/`Down` (or `K`/`J`). Typing characters filters the list in place. Press `S` to star or unstar a model — starred models persist in `~/.config/harnesscli/config.json` across sessions.

Pressing `Enter` at Level 1 opens a **config panel** where you can choose the gateway (Direct or OpenRouter), enter an API key, and set reasoning effort for models that support it.

### Gateway options

<Tabs>
  <TabsList>
    <TabsTrigger value="direct">Direct</TabsTrigger>
    <TabsTrigger value="openrouter">OpenRouter</TabsTrigger>
  </TabsList>
  <TabsContent value="direct">

**Direct** (gateway `""`) sends requests to each model's native provider endpoint. This is the default. Each provider needs its own API key.

  </TabsContent>
  <TabsContent value="openrouter">

**OpenRouter** (gateway `"openrouter"`) routes all requests through `openrouter.ai`. A single `OPENROUTER_API_KEY` covers all models. When selected, the model list is fetched live from `https://openrouter.ai/api/v1/models` (no auth required for the model list itself).

  </TabsContent>
</Tabs>

### Reasoning effort

For models that support extended thinking (`deepseek-reasoner`, `grok-4-1-fast-reasoning`, `qwen-qwq-32b`), the config panel offers effort levels: blank (provider default), `low`, `medium`, or `high`.

### Default model list

<Card>
  <CardHeader>
    <CardTitle>Models in the built-in picker</CardTitle>
  </CardHeader>
  <CardContent>

| Provider | Models |
|---|---|
| OpenAI | `gpt-4.1`, `gpt-4.1-mini` |
| Anthropic | `claude-sonnet-4-6`, `claude-opus-4-6`, `claude-haiku-4-5-20251001` |
| Google | `gemini-2.5-flash`, `gemini-2.0-flash` |
| DeepSeek | `deepseek-chat`, `deepseek-reasoner` |
| xAI | `grok-3-mini`, `grok-4-1-fast-reasoning` |
| Groq | `llama-3.3-70b-versatile`, `qwen-qwq-32b` |
| Qwen | `qwen-plus`, `qwen-turbo` |
| Kimi | `kimi-k2.5` |

  </CardContent>
</Card>

---

## Profile picker (`/profiles`)

Profiles are named configurations that bundle model settings, tool allowlists, and permission policies. The TUI fetches the profile list from `GET /v1/profiles` and shows them in a picker. Selecting a profile applies it to the **next run** — the current run is not affected.

<Callout type="warning">
The profile name is sent in the `profile` JSON field of the run request, which maps to `RunRequest.ProfileName`. Sending a capability profile name in the `prompt_profile` field will cause the server to reject the request with HTTP 400.
</Callout>

Built-in profiles include `full` (all tools, default), `researcher`, `reviewer`, `file-writer`, `bash-runner`, and `github`. See [Subagents and profiles](/docs/integrations/subagents-and-profiles) for the full schema.

---

## Sessions

The TUI tracks conversations by passing a `conversation_id` in every run request. When you send your first message in a new session, the server auto-assigns the ID. All subsequent messages in that session carry the same ID, so the server links them into a coherent conversation history.

Sessions are persisted to `~/.config/harnesscli/sessions.json`.

| Command | What it does |
|---|---|
| `/sessions` | Opens a picker of saved sessions. Press `D` to delete one. |
| `/new` | Resets the conversation ID and clears the viewport, starting a fresh session. |

---

## Plan mode

<Badge>Ctrl+O when idle</Badge>

Toggling plan mode (when no run is active and no tool card is selected) tells the agent to produce a plan before taking action. The status bar shows `[plan]` when plan mode is on.

<Callout type="warning">
The plan-approval overlay — which would display a proposed plan and let you approve (`Y`) or reject (`N`) it before execution — is wired in the TUI but requires the server to emit `plan.proposed` SSE events. The server does not currently emit these events, so the overlay remains inactive. This is forward-looking UI.
</Callout>

---

## Live event rendering

Every run streams SSE events from `GET /v1/runs/{id}/events`. The TUI translates each event type into a visual element:

| Event | What you see |
|---|---|
| `assistant.message.delta` | Message bubble builds up character by character (Markdown rendered) |
| `assistant.thinking.delta` | Thinking bar shows `"Thinking: <text>..."` |
| `tool.call.started` | A tool card appears with status <Badge>running</Badge> |
| `tool.output.delta` | Tool card result area grows |
| `tool.call.completed` | Tool card status updates to <Badge>completed</Badge> or <Badge>error</Badge> |
| `usage.delta` | Cost counter in status bar updates |
| `run.waiting_for_user` | An interactive overlay appears for `AskUserQuestion` approvals |
| `run.resumed` | The `AskUserQuestion` overlay is dismissed |
| `run.completed` | Run is marked inactive; assistant transcript is saved |
| `run.failed` | Run is marked inactive; error is rendered in the viewport |

### Tool cards

Tool cards show a collapsed summary by default:
- Bash/shell tools: the command (first line, up to 60 characters)
- Write/edit tools: `path (N lines)`
- Read/view tools: just the path

Press `Ctrl+O` while a tool card is active to expand it and see the full parameters and output.

---

## File attachments (`@path`)

Type `@` followed by a file path to attach file content to your message. Tab completion works for file paths after `@`. Before the message is sent to the server, the TUI expands all `@` tokens by reading the referenced files and embedding their content in the prompt.

Example:

```
Explain the bug in @src/api/handler.go
```

---

## External editor (`Ctrl+E`)

Press `Ctrl+E` when no overlay is open to launch your `$EDITOR`. The current input is written to a temporary file. When you save and quit the editor, the content is loaded back into the input field.

If `$EDITOR` is not set, the status bar shows `` `$EDITOR not set` ``.

<Callout type="info">
The editor integration uses `tea.ExecProcess`, which suspends the TUI while the editor is running. GUI editors configured as `$EDITOR` that fork to a window may not work correctly, because the TUI will resume as soon as the terminal process exits, before you have finished editing.
</Callout>

---

## Custom slash-command plugins

You can add your own slash commands by placing `.json` files in `~/.config/harnesscli/plugins/`. The TUI loads them at startup.

Two handler types are supported:

<Tabs>
  <TabsList>
    <TabsTrigger value="bash">bash handler</TabsTrigger>
    <TabsTrigger value="prompt">prompt handler</TabsTrigger>
  </TabsList>
  <TabsContent value="bash">

Runs a shell command when the slash command is invoked:

```json
{
  "name": "show-branch",
  "description": "Print the current git branch",
  "handler": "bash",
  "command": "git branch --show-current"
}
```

  </TabsContent>
  <TabsContent value="prompt">

Injects a templated prompt into the conversation:

```json
{
  "name": "ask-doc",
  "description": "Ask the agent to explain a symbol",
  "handler": "prompt",
  "prompt_template": "Explain {args} in simple terms."
}
```

`{args}` is replaced with everything the user typed after the command name.

  </TabsContent>
</Tabs>

**Plugin name rules:** must match `^[a-z][a-z0-9-]*$`. Plugin load errors appear as a transient status message at startup — they do not prevent the TUI from opening.

---

## Persistent configuration

<Card>
  <CardHeader>
    <CardTitle>~/.config/harnesscli/config.json</CardTitle>
  </CardHeader>
  <CardContent>

```json
{
  "starred_models": ["gpt-4.1"],
  "gateway": "openrouter",
  "api_keys": {"openai": "sk-...", "openrouter": "sk-..."},
  "history_entries": ["last command", "second-to-last command"]
}
```

- **`starred_models`** — models marked with `S` in the picker
- **`gateway`** — `""` for Direct, `"openrouter"` for OpenRouter
- **`api_keys`** — keys stored here are sent to the server at startup via `PUT /v1/providers/{provider}/key`
- **`history_entries`** — command history (newest first, max 100 entries)

</CardContent>
</Card>

Sessions are stored separately in `~/.config/harnesscli/sessions.json`.

### Environment variables

The TUI reads four environment variables:

| Variable | Purpose |
|---|---|
| `OPENAI_API_KEY` | Detected at startup to show OpenAI as available in the picker |
| `ANTHROPIC_API_KEY` | Same, for Anthropic |
| `OPENROUTER_API_KEY` | Same, for OpenRouter; also sent with OpenRouter model-list requests |
| `EDITOR` | External editor launched by `Ctrl+E` |

<Callout type="info">
API keys detected from the environment are used only to mark a provider as "available" in the model picker. They are **not** forwarded to `harnessd`. The server reads its own environment variables independently. Only keys stored in `~/.config/harnesscli/config.json` are actively pushed to the server via `PUT /v1/providers/{provider}/key` on startup.
</Callout>

---

## TUI vs one-shot CLI

| Feature | One-shot (`--prompt`) | TUI (`--tui`) |
|---|---|---|
| Multi-turn conversation | No | Yes |
| Live tool-call cards | No (raw events to stdout) | Yes |
| Model / profile / session switching | No | Yes |
| Interrupt mid-run | No (kill process) | Yes (two-stage Ctrl+C) |
| Transcript export | No | Yes (`/export`) |
| Slash commands | No | Yes (20+ commands) |
| Requires a TTY | No | Yes |
| Machine-parseable output | Yes (JSON lines) | No |
| Piping / scripting | Yes | No |

---

## Next steps

- **Profiles** — learn how to restrict tools and set cost limits for a run: [Subagents and profiles](/docs/integrations/subagents-and-profiles)
- **Events** — understand the full SSE event schema the TUI renders: [Events](/docs/concepts/events)
- **harnesscli flags** — the complete flag reference for one-shot mode and all subcommands: [harnesscli reference](/docs/cli/harnesscli)
