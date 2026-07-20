package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// stubClipboardReader replaces the clipboard-image read seam and counts calls.
func stubClipboardReader(t *testing.T, img ClipboardImage, err error) *int {
	t.Helper()
	calls := new(int)
	old := readClipboardImage
	readClipboardImage = func() (ClipboardImage, error) {
		*calls++
		return img, err
	}
	t.Cleanup(func() { readClipboardImage = old })
	return calls
}

func newSizedModel(t *testing.T) Model {
	t.Helper()
	m := New(DefaultTUIConfig())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m2.(Model)
}

// feedCmdMessages executes cmd (handling batches) and feeds any
// clipboardImageReadMsg back into the model, like the bubbletea runtime would.
func feedCmdMessages(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	var feed func(msg tea.Msg) Model
	feed = func(msg tea.Msg) Model {
		switch v := msg.(type) {
		case tea.BatchMsg:
			for _, c := range v {
				if c == nil {
					continue
				}
				m = feed(c())
			}
			return m
		case clipboardImageReadMsg:
			m2, _ := m.Update(v)
			return m2.(Model)
		default:
			return m
		}
	}
	return feed(cmd())
}

func TestImageModalityError_AllowsImageCapableModel(t *testing.T) {
	m := Model{
		selectedModel:    "gpt-4.1",
		selectedProvider: "openai",
		serverModels: []modelswitcher.ServerModelEntry{
			{ID: "gpt-4.1", Provider: "openai", Modalities: []string{"text", "image"}},
		},
	}
	if err := m.imageModalityError(); err != nil {
		t.Errorf("image-capable model must be allowed, got %v", err)
	}
}

func TestImageModalityError_RejectsTextOnlyModel(t *testing.T) {
	m := Model{
		selectedModel:    "claude-sonnet-4-6",
		selectedProvider: "anthropic",
		serverModels: []modelswitcher.ServerModelEntry{
			{ID: "claude-sonnet-4-6", Provider: "anthropic", Modalities: []string{"text"}},
		},
	}
	err := m.imageModalityError()
	if err == nil {
		t.Fatal("text-only model must be rejected")
	}
	if !strings.Contains(err.Error(), "claude-sonnet-4-6") {
		t.Errorf("error must name the model, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error must mention image input, got %q", err.Error())
	}
}

func TestImageModalityError_AllowsUnknownModel(t *testing.T) {
	// The selected model is absent from the fetched list (offline fetch,
	// stale catalog, or OpenRouter-sourced list without modalities): the
	// pre-flight must allow the paste; slice 3's server gate enforces at
	// send time.
	m := Model{
		selectedModel:    "some-local-model",
		selectedProvider: "ollama",
		serverModels: []modelswitcher.ServerModelEntry{
			{ID: "gpt-4.1", Provider: "openai", Modalities: []string{"text", "image"}},
		},
	}
	if err := m.imageModalityError(); err != nil {
		t.Errorf("unknown model must be allowed (best-effort pre-flight), got %v", err)
	}
}

func TestImageModalityError_AllowsWhenNoModelsFetched(t *testing.T) {
	m := Model{selectedModel: "gpt-4.1", selectedProvider: "openai"}
	if err := m.imageModalityError(); err != nil {
		t.Errorf("no fetched models must be allowed, got %v", err)
	}
}

func TestImageModalityError_AllowsEntryWithoutModalities(t *testing.T) {
	// A pre-slice-2 server (or OpenRouter source) returns entries with no
	// modalities field — treated as unknown, allowed.
	m := Model{
		selectedModel:    "gpt-4.1",
		selectedProvider: "openai",
		serverModels:     []modelswitcher.ServerModelEntry{{ID: "gpt-4.1", Provider: "openai"}},
	}
	if err := m.imageModalityError(); err != nil {
		t.Errorf("entry without modalities must be allowed, got %v", err)
	}
}

func TestPasteImageHappyPathAttachesChip(t *testing.T) {
	calls := stubClipboardReader(t, ClipboardImage{Path: "/tmp/fake/clipboard.png", MediaType: "image/png"}, nil)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	if cmd == nil {
		t.Fatal("ctrl+v must issue the clipboard read command")
	}
	m = feedCmdMessages(t, m, cmd)

	if *calls != 1 {
		t.Errorf("clipboard read called %d times, want 1", *calls)
	}
	atts := m.input.Attachments()
	if len(atts) != 1 {
		t.Fatalf("input must hold 1 attachment after paste, got %d", len(atts))
	}
	if atts[0].Path != "/tmp/fake/clipboard.png" || atts[0].MediaType != "image/png" {
		t.Errorf("attachment = %+v", atts[0])
	}
	if !strings.Contains(m.View(), "[image #1]") {
		t.Errorf("View() must render the chip, got:\n%s", m.View())
	}
	if m.statusMsg == "" {
		t.Error("a status hint must confirm the attach")
	}
}

func TestPasteImageNoImageShowsStatusHint(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{}, ErrClipboardNoImage)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)

	if len(m.input.Attachments()) != 0 {
		t.Errorf("no chip may be attached on failure, got %d", len(m.input.Attachments()))
	}
	if !strings.Contains(m.statusMsg, "no image on the clipboard") {
		t.Errorf("statusMsg = %q, want the no-image hint", m.statusMsg)
	}
}

func TestPasteImageHeadlessShowsStatusHint(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{}, ErrClipboardHeadless)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)

	if !strings.Contains(m.statusMsg, "headless") {
		t.Errorf("statusMsg = %q, want the headless hint", m.statusMsg)
	}
}

func TestPasteImageUnsupportedShowsStatusHint(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{}, ErrClipboardUnsupported)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)

	if !strings.Contains(m.statusMsg, "unsupported") {
		t.Errorf("statusMsg = %q, want the unsupported-platform hint", m.statusMsg)
	}
}

func TestPasteImageGenericErrorShowsStatusHint(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{}, errors.New("boom"))
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)

	if !strings.Contains(m.statusMsg, "boom") {
		t.Errorf("statusMsg = %q, want the underlying error surfaced", m.statusMsg)
	}
	if len(m.input.Attachments()) != 0 {
		t.Errorf("no chip may be attached on failure, got %d", len(m.input.Attachments()))
	}
}

func TestPasteImageGateRejectsBeforeReadingClipboard(t *testing.T) {
	calls := stubClipboardReader(t, ClipboardImage{Path: "/tmp/fake/clipboard.png", MediaType: "image/png"}, nil)
	m := newSizedModel(t)
	m.serverModels = []modelswitcher.ServerModelEntry{
		{ID: "claude-sonnet-4-6", Provider: "anthropic", Modalities: []string{"text"}},
	}
	m.selectedModel = "claude-sonnet-4-6"
	m.selectedProvider = "anthropic"

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)

	if *calls != 0 {
		t.Errorf("clipboard read must not run when the gate rejects, called %d times", *calls)
	}
	if !strings.Contains(m.statusMsg, "claude-sonnet-4-6") || !strings.Contains(m.statusMsg, "image") {
		t.Errorf("statusMsg = %q, want a modality rejection naming the model", m.statusMsg)
	}
	if len(m.input.Attachments()) != 0 {
		t.Errorf("no chip may be attached on gate rejection, got %d", len(m.input.Attachments()))
	}
}

func TestPasteImageNoOpWhenOverlayActive(t *testing.T) {
	calls := stubClipboardReader(t, ClipboardImage{Path: "/tmp/fake/clipboard.png", MediaType: "image/png"}, nil)
	m := newSizedModel(t)
	m2, _ := m.Update(OverlayOpenMsg{Kind: "help"})
	m = m2.(Model)
	if !m.OverlayActive() {
		t.Fatal("precondition: overlay must be active")
	}

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m3.(Model)

	if *calls != 0 {
		t.Errorf("ctrl+v with an active overlay must not read the clipboard, called %d times", *calls)
	}
	if len(m.input.Attachments()) != 0 {
		t.Errorf("no chip may be attached while an overlay is active, got %d", len(m.input.Attachments()))
	}
}

// TestPasteImageChipSurvivesTextSubmit locks the slice-2 acceptance flow
// "re-paste, send with a text prompt": Enter submits the text normally while
// the chip stays pending (slice 3 will consume attachments into the run
// request).
func TestPasteImageChipSurvivesTextSubmit(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{Path: "/tmp/fake/clipboard.png", MediaType: "image/png"}, nil)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)
	if len(m.input.Attachments()) != 1 {
		t.Fatalf("precondition: 1 attachment, got %d", len(m.input.Attachments()))
	}

	for _, r := range "hello" {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m3.(Model)
	}
	m4, enterCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m4.(Model)

	// Enter must emit the text submission.
	if enterCmd == nil {
		t.Fatal("Enter with text must emit a command")
	}
	var submitted *inputarea.CommandSubmittedMsg
	for _, msg := range collectPlainMsgs(enterCmd) {
		if v, ok := msg.(inputarea.CommandSubmittedMsg); ok {
			vv := v
			submitted = &vv
		}
	}
	if submitted == nil {
		t.Fatal("Enter must produce CommandSubmittedMsg")
	}
	if submitted.Value != "hello" {
		t.Errorf("submitted value = %q, want %q", submitted.Value, "hello")
	}
	if m.input.Value() != "" {
		t.Errorf("input text must clear after submit, got %q", m.input.Value())
	}
	// The chip stays pending — it is NOT consumed by a text-only submit in
	// this slice.
	if len(m.input.Attachments()) != 1 {
		t.Errorf("attachment chip must survive text submit, got %d", len(m.input.Attachments()))
	}
	if !strings.Contains(m.input.View(), "[image #1]") {
		t.Errorf("chip must still render after text submit, got:\n%s", m.input.View())
	}
}

// collectPlainMsgs executes a tea.Cmd, flattening batches, and returns the
// produced messages without feeding them back into the model.
func collectPlainMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collectPlainMsgs(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// TestPasteImageChipsSurviveWindowResize is a regression test: the parent
// re-creates the input component on every WindowSizeMsg, which used to drop
// pending attachment chips (and leak their temp files). Attachments must be
// preserved across resizes exactly like history and shell mode are.
func TestPasteImageChipsSurviveWindowResize(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{Path: "/tmp/fake/clipboard.png", MediaType: "image/png"}, nil)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)
	if len(m.input.Attachments()) != 1 {
		t.Fatalf("precondition: 1 attachment, got %d", len(m.input.Attachments()))
	}

	m3, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = m3.(Model)

	atts := m.input.Attachments()
	if len(atts) != 1 {
		t.Fatalf("attachments must survive window resize, got %d", len(atts))
	}
	if atts[0].Path != "/tmp/fake/clipboard.png" || atts[0].MediaType != "image/png" {
		t.Errorf("attachment = %+v after resize", atts[0])
	}
	if !strings.Contains(m.input.View(), "[image #1]") {
		t.Errorf("chip must still render after resize, got:\n%s", m.input.View())
	}
}

func TestPasteImageThenBackspaceRemovesChip(t *testing.T) {
	stubClipboardReader(t, ClipboardImage{Path: "/tmp/fake/clipboard.png", MediaType: "image/png"}, nil)
	m := newSizedModel(t)

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)
	if len(m.input.Attachments()) != 1 {
		t.Fatalf("precondition: 1 attachment, got %d", len(m.input.Attachments()))
	}

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m3.(Model)
	if len(m.input.Attachments()) != 0 {
		t.Errorf("Backspace on empty input must remove the chip, have %d", len(m.input.Attachments()))
	}
	if strings.Contains(m.input.View(), "[image #") {
		t.Errorf("chip must disappear from the input area, got:\n%s", m.input.View())
	}
}
