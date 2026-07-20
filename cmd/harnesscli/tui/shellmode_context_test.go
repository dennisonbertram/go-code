package tui_test

// Tests for shell-command context injection (epic #811, slice 3): after a
// shell-mode command finishes, the next agent prompt carries the command and
// its output as a wrapped <shell-command> block — single-use, agent-visible,
// and never shown in the user's display bubble.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui"
	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

// startPromptRecorder spins up an httptest server that records the prompt of
// every POST /v1/runs request and replies with a fixed run ID.
func startPromptRecorder(t *testing.T) (baseURL string, prompts func() []string) {
	t.Helper()
	var mu sync.Mutex
	var recorded []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" && r.Method == http.MethodPost {
			var body struct {
				Prompt string `json:"prompt"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			recorded = append(recorded, body.Prompt)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"run_id":"run-rec"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), recorded...)
	}
}

func initModelWithBaseURL(t *testing.T, w, h int, baseURL string) tui.Model {
	t.Helper()
	cfg := tui.DefaultTUIConfig()
	cfg.BaseURL = baseURL
	m := tui.New(cfg)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return m2.(tui.Model)
}

// submitPrompt sends a normal (non-shell) user message and executes the
// resulting startRunCmd so the prompt reaches the recording server.
func submitPrompt(t *testing.T, m tui.Model, text string) tui.Model {
	t.Helper()
	m2, cmd := m.Update(inputarea.CommandSubmittedMsg{Value: text})
	m = m2.(tui.Model)
	_ = unwrapSingle(t, cmd) // performs the POST
	return m
}

func TestShellContext_InjectedIntoNextPrompt(t *testing.T) {
	baseURL, prompts := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m, pollCmd := submitShellCommand(t, m, "echo hello")
	m = drainShell(t, m, pollCmd)

	m = submitPrompt(t, m, "what changed?")

	got := prompts()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 run request, got %d", len(got))
	}
	prompt := got[0]
	for _, want := range []string{"<shell-command", `command="echo hello"`, `exit-code="0"`, "<![CDATA[", "hello"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("injected prompt must contain %q, got:\n%s", want, prompt)
		}
	}
	// The user's own text follows the block.
	if !strings.Contains(prompt, "what changed?") {
		t.Errorf("prompt must still carry the user's message, got:\n%s", prompt)
	}
}

func TestShellContext_ConsumedOnce(t *testing.T) {
	baseURL, prompts := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m, pollCmd := submitShellCommand(t, m, "echo hello")
	m = drainShell(t, m, pollCmd)

	m = submitPrompt(t, m, "first")
	m = submitPrompt(t, m, "second")

	got := prompts()
	if len(got) != 2 {
		t.Fatalf("expected 2 run requests, got %d", len(got))
	}
	if !strings.Contains(got[0], "<shell-command") {
		t.Error("first prompt after the shell run must contain the context block")
	}
	if strings.Contains(got[1], "<shell-command") {
		t.Errorf("context block is single-use — second prompt must be clean, got:\n%s", got[1])
	}
}

func TestShellContext_NoBlockWithoutShellRun(t *testing.T) {
	baseURL, prompts := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m = submitPrompt(t, m, "just a question")

	got := prompts()
	if len(got) != 1 {
		t.Fatalf("expected 1 run request, got %d", len(got))
	}
	if strings.Contains(got[0], "<shell-command") {
		t.Errorf("no shell command ran — prompt must not carry a context block, got:\n%s", got[0])
	}
}

func TestShellContext_FailedCommandIncludesExitCode(t *testing.T) {
	baseURL, prompts := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m, pollCmd := submitShellCommand(t, m, "exit 3")
	m = drainShell(t, m, pollCmd)

	m = submitPrompt(t, m, "why did it fail?")

	got := prompts()
	if len(got) != 1 {
		t.Fatalf("expected 1 run request, got %d", len(got))
	}
	if !strings.Contains(got[0], `exit-code="3"`) {
		t.Errorf("failed command must inject its non-zero exit code, got:\n%s", got[0])
	}
}

func TestShellContext_InterruptedCommandNotInjected(t *testing.T) {
	baseURL, prompts := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m, pollCmd := submitShellCommand(t, m, "sleep 999")
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // kill it
	m = drainShell(t, m2.(tui.Model), pollCmd)

	m = submitPrompt(t, m, "next question")

	got := prompts()
	if len(got) != 1 {
		t.Fatalf("expected 1 run request, got %d", len(got))
	}
	if strings.Contains(got[0], "<shell-command") {
		t.Errorf("interrupted command must not be injected (the user killed it deliberately), got:\n%s", got[0])
	}
}

func TestShellContext_DisplayBubbleShowsOriginalText(t *testing.T) {
	baseURL, _ := startPromptRecorder(t)
	m := initModelWithBaseURL(t, 80, 24, baseURL)

	m, pollCmd := submitShellCommand(t, m, "echo hello")
	m = drainShell(t, m, pollCmd)

	m = submitPrompt(t, m, "what changed?")

	view := m.View()
	if !strings.Contains(view, "what changed?") {
		t.Errorf("display bubble must show the user's original text, got:\n%s", view)
	}
	if strings.Contains(view, "<shell-command") {
		t.Errorf("the injected context block must not leak into the display, got:\n%s", view)
	}
}
