package tui

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"go-agent-harness/cmd/harnesscli/tui/components/inputarea"
)

// writeTempPNG creates a real temp dir holding a PNG file, like the slice-1
// clipboard reader produces.
func writeTempPNG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "clipboard.png")
	if err := os.WriteFile(path, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 9, 9}, 0o600); err != nil {
		t.Fatalf("write temp png: %v", err)
	}
	return path
}

func TestEncodeImageAttachmentsRoundTrip(t *testing.T) {
	path := writeTempPNG(t)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	atts, err := encodeImageAttachments([]inputarea.Attachment{{Path: path, MediaType: "image/png"}})
	if err != nil {
		t.Fatalf("encodeImageAttachments: %v", err)
	}
	if len(atts) != 1 {
		t.Fatalf("len = %d, want 1", len(atts))
	}
	if atts[0].Type != "image" {
		t.Errorf("Type = %q, want image", atts[0].Type)
	}
	if atts[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", atts[0].MediaType)
	}
	if atts[0].Data != base64.StdEncoding.EncodeToString(want) {
		t.Errorf("Data does not match the base64-encoded file bytes")
	}
}

func TestEncodeImageAttachmentsMissingFile(t *testing.T) {
	_, err := encodeImageAttachments([]inputarea.Attachment{{Path: "/nonexistent-dir/clipboard.png", MediaType: "image/png"}})
	if err == nil {
		t.Fatal("missing file must error")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error must mention the image attachment, got %q", err.Error())
	}
}

// captureRunServer records POST /v1/runs bodies and answers 202.
type captureRunServer struct {
	srv    *httptest.Server
	hits   atomic.Int32
	bodies atomic.Pointer[[]byte]
}

func newCaptureRunServer(t *testing.T) *captureRunServer {
	t.Helper()
	c := &captureRunServer{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" && r.Method == http.MethodPost {
			c.hits.Add(1)
			body, _ := io.ReadAll(r.Body)
			c.bodies.Store(&body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"run_id":"run-test-1","status":"queued"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *captureRunServer) lastBody(t *testing.T) map[string]any {
	t.Helper()
	p := c.bodies.Load()
	if p == nil {
		t.Fatal("server never received a run request")
	}
	var out map[string]any
	if err := json.Unmarshal(*p, &out); err != nil {
		t.Fatalf("decode captured body: %v", err)
	}
	return out
}

// attachChipViaPaste drives the real paste flow with a stubbed clipboard
// reader returning the given temp file.
func attachChipViaPaste(t *testing.T, m Model, pngPath string) Model {
	t.Helper()
	stubClipboardReader(t, ClipboardImage{Path: pngPath, MediaType: "image/png"}, nil)
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	m = m2.(Model)
	m = feedCmdMessages(t, m, cmd)
	if len(m.input.Attachments()) != 1 {
		t.Fatalf("precondition: 1 attachment, got %d", len(m.input.Attachments()))
	}
	return m
}

// TestSubmitConsumesChipsIntoRunRequest proves the slice-3 send path: typing
// a prompt with an image chip attached and pressing Enter base64-encodes the
// image into the POST /v1/runs body and consumes the chip (state + temp
// files).
func TestSubmitConsumesChipsIntoRunRequest(t *testing.T) {
	pngPath := writeTempPNG(t)
	pngBytes, err := os.ReadFile(pngPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	capture := newCaptureRunServer(t)

	m := newSizedModel(t)
	m.config.BaseURL = capture.srv.URL
	m = attachChipViaPaste(t, m, pngPath)
	chipDir := filepath.Dir(pngPath)

	for _, r := range "describe this" {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m3.(Model)
	}
	m4, enterCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m4.(Model)

	// Feed the input's CommandSubmittedMsg back so the parent starts the run,
	// then execute the produced command chain — that is what performs the
	// POST. Produced messages are not fed back (the assertions cover the
	// synchronous state changes and the captured request).
	for _, msg := range collectPlainMsgs(enterCmd) {
		m2, runCmd := m.Update(msg)
		m = m2.(Model)
		_ = collectPlainMsgs(runCmd)
	}

	if capture.hits.Load() != 1 {
		t.Fatalf("server received %d run requests, want 1", capture.hits.Load())
	}
	body := capture.lastBody(t)
	if body["prompt"] != "describe this" {
		t.Errorf("prompt = %v", body["prompt"])
	}
	atts, ok := body["attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("attachments = %v, want one image attachment", body["attachments"])
	}
	a0, _ := atts[0].(map[string]any)
	if a0["type"] != "image" || a0["media_type"] != "image/png" {
		t.Errorf("attachment type/media = %v/%v", a0["type"], a0["media_type"])
	}
	if a0["data"] != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Errorf("attachment data is not the base64 of the chip's PNG file")
	}

	// The chip is consumed: state cleared and temp directory deleted.
	if len(m.input.Attachments()) != 0 {
		t.Errorf("chips must be consumed after submit, have %d", len(m.input.Attachments()))
	}
	if _, err := os.Stat(chipDir); !os.IsNotExist(err) {
		t.Errorf("chip temp dir must be deleted after submit, stat err = %v", err)
	}
}

// TestSubmitWithUnreadableChipAborts proves the failure path: when an
// attached image's temp file cannot be read, the submit is aborted before
// any HTTP request, the text is restored, and the chips are kept so the user
// can fix or remove them.
func TestSubmitWithUnreadableChipAborts(t *testing.T) {
	capture := newCaptureRunServer(t)

	m := newSizedModel(t)
	m.config.BaseURL = capture.srv.URL
	m = attachChipViaPaste(t, m, "/nonexistent-dir-xyz/clipboard.png")

	for _, r := range "hello" {
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m3.(Model)
	}
	m4, enterCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m4.(Model)
	// Feed the submission through the parent. The encode failure aborts
	// synchronously inside Update — the returned command is only the
	// transient status tick, which is deliberately not executed.
	for _, msg := range collectPlainMsgs(enterCmd) {
		m2, _ := m.Update(msg)
		m = m2.(Model)
	}

	if capture.hits.Load() != 0 {
		t.Errorf("no run request may be sent on encode failure, got %d", capture.hits.Load())
	}
	if !strings.Contains(m.statusMsg, "image") {
		t.Errorf("statusMsg must explain the image failure, got %q", m.statusMsg)
	}
	if m.input.Value() != "hello" {
		t.Errorf("text must be restored after abort, got %q", m.input.Value())
	}
	if len(m.input.Attachments()) != 1 {
		t.Errorf("chips must be kept after abort, have %d", len(m.input.Attachments()))
	}
}
