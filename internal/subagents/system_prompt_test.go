package subagents

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
)

// captureRunEngine is a RunEngine that captures the last RunRequest.
type captureRunEngine struct {
	lastReq harness.RunRequest
}

func (c *captureRunEngine) StartRun(req harness.RunRequest) (harness.Run, error) {
	c.lastReq = req
	return harness.Run{
		ID:     "test-run-id",
		Status: harness.RunStatusQueued,
	}, nil
}

func (c *captureRunEngine) GetRun(runID string) (harness.Run, bool) {
	return harness.Run{
		ID:     runID,
		Status: harness.RunStatusCompleted,
		Output: "done",
	}, true
}

func (c *captureRunEngine) Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error) {
	ch := make(chan harness.Event)
	close(ch)
	return []harness.Event{
		{Type: harness.EventRunCompleted},
	}, ch, func() {}, nil
}

func (c *captureRunEngine) CancelRun(runID string) error {
	return nil
}

type statefulRunEngine struct {
	mu              sync.Mutex
	lastReq         harness.RunRequest
	status          harness.RunStatus
	output          string
	cancelled       bool
	cancelCallCount int
	cancelledRunID  string
}

func (s *statefulRunEngine) StartRun(req harness.RunRequest) (harness.Run, error) {
	s.mu.Lock()
	s.lastReq = req
	s.mu.Unlock()
	return harness.Run{
		ID:     "stateful-run-id",
		Status: harness.RunStatusQueued,
	}, nil
}

func (s *statefulRunEngine) GetRun(runID string) (harness.Run, bool) {
	s.mu.Lock()
	output := s.output
	if s.cancelled && output == "" {
		output = "cancelled"
	}
	status := s.status
	if s.cancelled {
		status = harness.RunStatusCancelled
	}
	s.mu.Unlock()
	return harness.Run{
		ID:     runID,
		Status: status,
		Output: output,
	}, true
}

func (s *statefulRunEngine) Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error) {
	ch := make(chan harness.Event)
	close(ch)
	return nil, ch, func() {}, nil
}

func (s *statefulRunEngine) CancelRun(runID string) error {
	s.mu.Lock()
	s.cancelCallCount++
	s.cancelled = true
	s.cancelledRunID = runID
	s.mu.Unlock()
	return nil
}

func TestRequestSystemPromptForwarded(t *testing.T) {
	engine := &captureRunEngine{}

	mgr, err := NewManager(Options{
		InlineRunner: engine,
	})
	require.NoError(t, err)

	req := Request{
		Prompt:       "Do something",
		SystemPrompt: "Be a helpful specialist.",
		Isolation:    IsolationInline,
	}

	_, err = mgr.Create(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "Be a helpful specialist.", engine.lastReq.SystemPrompt,
		"SystemPrompt should be forwarded to RunRequest")
}

func TestRequestAgentIntentForwarded(t *testing.T) {
	engine := &captureRunEngine{}

	mgr, err := NewManager(Options{
		InlineRunner: engine,
	})
	require.NoError(t, err)

	req := Request{
		Prompt:      "Review the diff",
		AgentIntent: "code_review",
		Isolation:   IsolationInline,
	}

	_, err = mgr.Create(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "code_review", engine.lastReq.AgentIntent,
		"AgentIntent should be forwarded to RunRequest so a subagent can select a named overlay")
}

func TestRequestSystemPromptEmpty(t *testing.T) {
	engine := &captureRunEngine{}

	mgr, err := NewManager(Options{
		InlineRunner: engine,
	})
	require.NoError(t, err)

	req := Request{
		Prompt:    "Do something",
		Isolation: IsolationInline,
	}

	_, err = mgr.Create(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "", engine.lastReq.SystemPrompt,
		"Empty SystemPrompt should forward as empty string")
}

func TestRequestSystemPromptTrimsSpace(t *testing.T) {
	engine := &captureRunEngine{}

	mgr, err := NewManager(Options{
		InlineRunner: engine,
	})
	require.NoError(t, err)

	req := Request{
		Prompt:       "Do something",
		SystemPrompt: "  Leading and trailing spaces.  ",
		Isolation:    IsolationInline,
	}

	_, err = mgr.Create(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "Leading and trailing spaces.", engine.lastReq.SystemPrompt)
}

// TestNewInlineManager verifies that NewInlineManager wraps a Manager correctly.
func TestNewInlineManager(t *testing.T) {
	engine := &captureRunEngine{}
	mgr, err := NewManager(Options{InlineRunner: engine})
	require.NoError(t, err)

	im := NewInlineManager(mgr)
	require.NotNil(t, im)
}

// TestInlineManagerCreateAndWait verifies that CreateAndWait creates a subagent
// and returns when it reaches a terminal status.
func TestInlineManagerCreateAndWait(t *testing.T) {
	engine := &captureRunEngine{}
	mgr, err := NewManager(Options{InlineRunner: engine})
	require.NoError(t, err)

	im := NewInlineManager(mgr)

	req := tools.SubagentRequest{
		Prompt:   "Do a thing",
		Model:    "gpt-4.1-mini",
		MaxSteps: 5,
	}

	result, err := im.CreateAndWait(context.Background(), req)
	require.NoError(t, err)
	assert.NotEmpty(t, result.ID)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, "done", result.Output)
}

// TestInlineManagerCreateAndWaitSystemPrompt verifies that the SystemPrompt is
// forwarded through CreateAndWait to the underlying RunRequest.
func TestInlineManagerCreateAndWaitSystemPrompt(t *testing.T) {
	engine := &captureRunEngine{}
	mgr, err := NewManager(Options{InlineRunner: engine})
	require.NoError(t, err)

	im := NewInlineManager(mgr)

	req := tools.SubagentRequest{
		Prompt:       "Do a thing",
		SystemPrompt: "Be specialized.",
	}

	_, err = im.CreateAndWait(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "Be specialized.", engine.lastReq.SystemPrompt)
}

func TestInlineManagerCreateAndWaitForwardsParentContextHandoff(t *testing.T) {
	engine := &captureRunEngine{}
	mgr, err := NewManager(Options{InlineRunner: engine})
	require.NoError(t, err)

	im := NewInlineManager(mgr)
	handoff := &tools.ParentContextHandoff{
		ParentRunID: "run_parent",
		Messages: []tools.ParentContextMessage{{
			Index:   1,
			Role:    "user",
			Content: "Important parent context",
		}},
	}

	_, err = im.CreateAndWait(context.Background(), tools.SubagentRequest{
		Prompt:               "Do a thing",
		ParentContextHandoff: handoff,
	})
	require.NoError(t, err)

	require.NotNil(t, engine.lastReq.ParentContextHandoff)
	assert.Equal(t, "run_parent", engine.lastReq.ParentContextHandoff.ParentRunID)
}

func TestInlineManagerGet(t *testing.T) {
	engine := &statefulRunEngine{
		status: harness.RunStatusCompleted,
		output: "done",
	}
	mgr, err := NewManager(Options{InlineRunner: engine})
	require.NoError(t, err)

	im := NewInlineManager(mgr)
	created, err := im.Start(context.Background(), tools.SubagentRequest{Prompt: "Do a thing"})
	require.NoError(t, err)

	result, err := im.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, result.ID)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, "done", result.Output)
}

func TestInlineManagerCancel(t *testing.T) {
	engine := &statefulRunEngine{
		status: harness.RunStatusRunning,
	}
	mgr, err := NewManager(Options{InlineRunner: engine})
	require.NoError(t, err)

	im := NewInlineManager(mgr)
	created, err := im.Start(context.Background(), tools.SubagentRequest{Prompt: "Cancel this"})
	require.NoError(t, err)

	err = im.Cancel(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, engine.cancelCallCount)
	assert.Equal(t, created.RunID, engine.cancelledRunID)

	result, err := im.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", result.Status)
}
