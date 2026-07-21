package tui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/messagebubble"
	"go-agent-harness/cmd/harnesscli/tui/components/transcriptexport"
)

// initAgentsPrompt is the fixed prompt /init sends through the normal run
// path. It instructs the model to inspect the workspace and return only the
// AGENTS.md markdown; the TUI writes that markdown to <workspace>/AGENTS.md
// when the run completes. Keep it deterministic: the completion write path
// (extractAgentsMarkdown) relies on the reply being markdown, optionally
// wrapped in a single code fence.
const initAgentsPrompt = `Analyze this repository and write an AGENTS.md file for it.

AGENTS.md gives coding agents the context they need to work effectively in a repo. Base everything on what you actually find in the workspace — never invent commands, structure, or conventions.

Cover, in this order:
1. Overview: one paragraph on what the project is and does.
2. Layout: the top-level directories and what each contains.
3. Commands: exact build, test, and lint commands verified against the repo's own tooling (go.mod, package.json, Makefile, CI config, etc.).
4. Conventions: code style, testing patterns, and commit/PR rules an agent must follow.

Keep the result under 150 lines. Output ONLY the final markdown content for AGENTS.md — no preamble, no explanation, no trailing commentary.`

// executeInitCommand implements /init: it runs initAgentsPrompt against the
// current workspace via the normal run path and, when the run completes,
// writes the assistant's markdown to <workspace>/AGENTS.md (see
// completeInitAgentsMd, driven by pendingInitAgentsMd). An existing AGENTS.md
// is only overwritten when the user passes the explicit "confirm" token —
// the same approval pattern as /rewind <id> confirm.
func executeInitCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	if len(cmd.Args) > 1 || (len(cmd.Args) == 1 && cmd.Args[0] != "confirm") {
		return []tea.Cmd{m.setStatusMsg("Usage: /init [confirm] — generate AGENTS.md for this workspace")}, false
	}
	confirmed := len(cmd.Args) == 1

	ws := m.initWorkspace()
	if _, err := os.Stat(filepath.Join(ws, "AGENTS.md")); err == nil && !confirmed {
		return []tea.Cmd{m.setStatusMsg("AGENTS.md already exists — run /init confirm to overwrite")}, false
	}
	if m.runActive {
		return []tea.Cmd{m.setStatusMsg("A run is already active — wait for it to finish before /init")}, false
	}

	// Record the generation prompt as the user turn and start the run. The
	// transcript keeps the literal prompt (the truthful record of what was
	// sent); the bubble shows a short label to keep the viewport readable.
	m.pendingInitAgentsMd = true
	m.lastAssistantText = ""
	m.responseStarted = false
	m.activeAssistantLineCount = 0
	m.clearThinkingBar()
	m.pendingLastMsg = "/init — generate AGENTS.md"
	m.transcript = append(m.transcript, transcriptexport.TranscriptEntry{
		Role:      "user",
		Content:   initAgentsPrompt,
		Timestamp: time.Now(),
	})
	m.appendMessageBubble(messagebubble.RoleUser, "Generate AGENTS.md for this workspace (/init)")
	effModel, effProvider := m.effectiveModelAndProvider()
	return []tea.Cmd{
		m.setStatusMsg("Generating AGENTS.md..."),
		startRunCmd(m.config.BaseURL, initAgentsPrompt, m.conversationID, effModel, effProvider, m.selectedReasoningEffort, m.selectedProfile, ws, m.config.APIKey, nil),
	}, false
}

// completeInitAgentsMd writes the markdown produced by a /init run to
// <workspace>/AGENTS.md and reports the outcome via a status message. Called
// from the RunCompletedMsg handler when pendingInitAgentsMd is set.
func (m *Model) completeInitAgentsMd() tea.Cmd {
	content := extractAgentsMarkdown(m.lastAssistantText)
	if content == "" {
		return m.setStatusMsg("AGENTS.md not written — the run produced no markdown")
	}
	path := filepath.Join(m.initWorkspace(), "AGENTS.md")
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return m.setStatusMsg("Could not write AGENTS.md: " + err.Error())
	}
	return m.setStatusMsg("Wrote " + path)
}

// initWorkspace returns the workspace root for /init, defaulting to the
// process working directory when the TUI was started without one (mirrors
// resolveWorkspacePath in cmd/harnesscli).
func (m Model) initWorkspace() string {
	if ws := strings.TrimSpace(m.config.Workspace); ws != "" {
		return ws
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// extractAgentsMarkdown converts a /init run's assistant reply into file
// content: surrounding whitespace is trimmed and a single wrapping code fence
// (```markdown or bare ```) is removed when present. Fences inside the
// document are preserved.
func extractAgentsMarkdown(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	lines = lines[1:] // drop the opening fence line
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "```" {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
