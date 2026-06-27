package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/mcp"
)

func TestRunnerShutdownClosesWedgedScopedMCPRegistry(t *testing.T) {
	hp := newHangingProvider()
	runner := NewRunner(hp, NewRegistry(), RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
	})

	run, err := runner.StartRun(RunRequest{Prompt: "hang"})
	require.NoError(t, err)
	waitForStatus(t, runner, run.ID, RunStatusRunning)

	conn := &shutdownSignalMCPConn{
		closed: make(chan struct{}),
	}
	manager := mcp.NewClientManager()
	require.NoError(t, manager.AddServerWithConn("wedged-mcp", func() (mcp.Conn, error) {
		return conn, nil
	}))
	scoped := NewScopedMCPRegistry(nil, manager, []string{"wedged-mcp"})
	_, err = scoped.ListPerRunTools(context.Background())
	require.NoError(t, err)

	runner.mu.Lock()
	state := runner.runs[run.ID]
	require.NotNil(t, state)
	state.scopedMCPRegistry = scoped
	runner.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = runner.Shutdown(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	select {
	case <-conn.closed:
	case <-time.After(time.Second):
		t.Fatal("scoped MCP registry was not closed during shutdown")
	}

	runner.mu.RLock()
	state = runner.runs[run.ID]
	require.NotNil(t, state)
	require.Nil(t, state.scopedMCPRegistry, "scoped MCP registry should be cleared after shutdown close")
	runner.mu.RUnlock()
	hp.Release()
}

type shutdownSignalMCPConn struct {
	closeOnce sync.Once
	closed    chan struct{}
	id        int64
}

func (c *shutdownSignalMCPConn) Initialize(context.Context) error { return nil }

func (c *shutdownSignalMCPConn) ListTools(context.Context) ([]mcp.ToolDef, error) {
	return []mcp.ToolDef{{Name: "noop", Description: "noop"}}, nil
}

func (c *shutdownSignalMCPConn) CallTool(_ context.Context, name string, _ json.RawMessage) (string, error) {
	return fmt.Sprintf(`{"content":[{"type":"text","text":"%s"}]}`, name), nil
}

func (c *shutdownSignalMCPConn) NextID() int64 {
	c.id++
	return c.id
}

func (c *shutdownSignalMCPConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}
