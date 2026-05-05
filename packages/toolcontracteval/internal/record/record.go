package record

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go-agent-harness/packages/toolcontracteval/internal/schema"
)

type Manifest struct {
	RunID              string    `json:"run_id"`
	SuiteID            string    `json:"suite_id"`
	Model              string    `json:"model"`
	Provider           string    `json:"provider"`
	Mode               string    `json:"mode"`
	SystemPromptLabel  string    `json:"system_prompt_label,omitempty"`
	SystemPromptPath   string    `json:"system_prompt_path,omitempty"`
	SystemPromptSHA256 string    `json:"system_prompt_sha256,omitempty"`
	SystemPromptChars  int       `json:"system_prompt_chars,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
}

type ToolCall struct {
	RunID        string `json:"run_id"`
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	Scenario     string `json:"scenario"`
	Turn         int    `json:"turn"`
	Tool         string `json:"tool"`
	CallID       string `json:"call_id"`
	ArgumentsRaw string `json:"arguments_raw"`
	Valid        bool   `json:"valid"`
}

type ValidationFailure struct {
	RunID        string       `json:"run_id"`
	Model        string       `json:"model"`
	Provider     string       `json:"provider"`
	Scenario     string       `json:"scenario"`
	Turn         int          `json:"turn"`
	Tool         string       `json:"tool"`
	CallID       string       `json:"call_id"`
	ArgumentsRaw string       `json:"arguments_raw"`
	Issue        schema.Issue `json:"issue"`
}

type RetryMessage struct {
	RunID    string `json:"run_id"`
	Scenario string `json:"scenario"`
	Turn     int    `json:"turn"`
	Tool     string `json:"tool"`
	CallID   string `json:"call_id"`
	Message  string `json:"message"`
}

type ToolResult struct {
	RunID    string `json:"run_id"`
	Scenario string `json:"scenario"`
	Turn     int    `json:"turn"`
	Tool     string `json:"tool"`
	CallID   string `json:"call_id"`
	Content  string `json:"content"`
	Error    string `json:"error,omitempty"`
}

type ScenarioResult struct {
	RunID          string `json:"run_id"`
	Scenario       string `json:"scenario"`
	ToolCalls      int    `json:"tool_calls"`
	InvalidCalls   int    `json:"invalid_calls"`
	ValidationHits int    `json:"validation_hits"`
	Completed      bool   `json:"completed"`
	Error          string `json:"error,omitempty"`
}

type APIEvent struct {
	RunID    string         `json:"run_id"`
	Scenario string         `json:"scenario"`
	EventID  string         `json:"event_id,omitempty"`
	Type     string         `json:"type"`
	Payload  map[string]any `json:"payload,omitempty"`
	Raw      string         `json:"raw,omitempty"`
}

type Writer struct {
	dir string
}

func NewWriter(dir string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Writer{dir: dir}, nil
}

func (w *Writer) Dir() string {
	return w.dir
}

func (w *Writer) WriteJSON(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(w.dir, name), data, 0o644)
}

func (w *Writer) AppendJSONL(name string, v any) error {
	return AppendJSONL(filepath.Join(w.dir, name), v)
}

func AppendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("append jsonl %s: %w", path, err)
	}
	return nil
}

func ReadJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var v T
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
