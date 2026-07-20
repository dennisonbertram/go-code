package tui

// Shell-mode local executor (epic #811, slice 2).
//
// A shell-mode command runs in the TUI process via `sh -c`, never on the
// harnessd server and never through the agent's bash tool. Execution is fully
// asynchronous so Update() never blocks: startShellExec spawns the process and
// a pump goroutine; the model polls the executor's message channel with a
// tea.Cmd (same pattern as the SSE bridge's pollSSECmd). The pump emits
// shellExecOutputMsg deltas as output arrives (capped) and exactly one
// shellExecDoneMsg when the process exits, carrying the exit code and a
// bounded head/tail of the combined stdout/stderr.

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	// defaultShellExecTimeout bounds a shell-mode command when no explicit
	// timeout is configured (kimi-code parity: 120s).
	defaultShellExecTimeout = 120 * time.Second

	// shellExecMaxOutputBytes caps the combined stdout/stderr retained for the
	// final card (head+tail), matching the bash-plugin capture budget.
	shellExecMaxOutputBytes = 30 * 1024

	// shellExecMaxStreamBytes caps how much output is forwarded as live deltas.
	// Beyond it the card stops updating live but the final done message still
	// carries the bounded head/tail, so floods like `yes` stay memory-safe.
	shellExecMaxStreamBytes = 30 * 1024

	// shellExecTruncationMarker separates head and tail in over-cap output.
	shellExecTruncationMarker = "\n...[truncated output]...\n"
)

// shellExecOutputMsg carries one streamed chunk of combined stdout/stderr.
type shellExecOutputMsg struct {
	CallID string
	Chunk  string
}

// shellExecDoneMsg is emitted exactly once when the process exits.
type shellExecDoneMsg struct {
	CallID string
	// Output is the bounded head/tail of combined stdout/stderr.
	Output   string
	ExitCode int
	// TimedOut is true when the configured timeout killed the command.
	TimedOut bool
	// Timeout is the configured timeout, set when TimedOut is true.
	Timeout time.Duration
	// Interrupted is true when kill() (Esc/Ctrl-C) stopped the command.
	Interrupted bool
	// Err is set for start/wait failures unrelated to the exit code.
	Err error
}

// shellExec tracks one running shell-mode command.
type shellExec struct {
	callID  string
	command string
	// ch delivers output/done messages to the tea loop. It is buffered so the
	// pump never blocks on a busy loop; deltas are dropped when full (the done
	// message still carries the bounded final output).
	ch chan tea.Msg
	// cancel terminates the process (group) via the command context.
	cancel context.CancelFunc
	// interrupted records that kill() — not a failure or timeout — ended the
	// command. Read by the pump when composing the done message.
	interrupted atomic.Bool
}

// startShellExec launches `sh -c command` in the background. The process runs
// in its own process group so a cancel kills the whole tree. A timeout <= 0
// uses defaultShellExecTimeout.
func startShellExec(callID, command string, timeout time.Duration) (*shellExec, error) {
	if timeout <= 0 {
		timeout = defaultShellExecTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	configureShellGroupKill(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	// stderr flows into the same pipe so the card shows interleaved output.
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	ex := &shellExec{
		callID:  callID,
		command: command,
		ch:      make(chan tea.Msg, 64),
		cancel:  cancel,
	}
	go ex.pump(cmd, stdout, ctx, timeout)
	return ex, nil
}

// kill stops the command and marks the done message as an interruption.
func (ex *shellExec) kill() {
	ex.interrupted.Store(true)
	ex.cancel()
}

// pump reads the combined output until EOF, waits for the process, and emits
// the terminal done message. It runs on its own goroutine for the lifetime of
// the process.
func (ex *shellExec) pump(cmd *exec.Cmd, r io.Reader, ctx context.Context, timeout time.Duration) {
	buf := newShellOutputBuffer(shellExecMaxOutputBytes)
	readBuf := make([]byte, 4096)
	streamed := 0
	for {
		n, err := r.Read(readBuf)
		if n > 0 {
			chunk := readBuf[:n]
			buf.Write(chunk)
			if streamed < shellExecMaxStreamBytes {
				select {
				case ex.ch <- shellExecOutputMsg{CallID: ex.callID, Chunk: string(chunk)}:
					streamed += n
				default:
					// The tea loop is busy; drop this live delta. The bounded
					// final output still arrives with the done message.
				}
			}
		}
		if err != nil {
			break
		}
	}

	waitErr := cmd.Wait()
	done := shellExecDoneMsg{CallID: ex.callID, Output: buf.String()}
	switch {
	case ex.interrupted.Load():
		done.Interrupted = true
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// The context deadline fired and killed the process group.
		done.TimedOut = true
		done.Timeout = timeout
	case waitErr == nil:
		done.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			done.ExitCode = exitErr.ExitCode()
		} else {
			done.Err = waitErr
		}
	}
	// Blocking send: done is the terminal message and must not be dropped.
	ex.ch <- done
}

// waitShellExecMsg returns a tea.Cmd that receives the executor's next
// message. The model re-issues it after every delta; the done message ends the
// chain, so no poll outlives the process.
func waitShellExecMsg(ex *shellExec) tea.Cmd {
	return func() tea.Msg {
		return <-ex.ch
	}
}

// shellOutputBuffer is a fixed-capacity writer keeping the first headCap and
// last tailCap bytes with a truncation marker between them when the total
// exceeds the cap. Mirrors the bash-plugin capture strategy
// (cmd/harnesscli/tui/plugin/execute.go), which is unexported there.
type shellOutputBuffer struct {
	max      int
	headCap  int
	tailCap  int
	total    int
	headData []byte
	tailData []byte
}

func newShellOutputBuffer(max int) *shellOutputBuffer {
	if max <= 0 {
		max = shellExecMaxOutputBytes
	}
	headCap := max / 2
	tailCap := max - headCap
	return &shellOutputBuffer{
		max:      max,
		headCap:  headCap,
		tailCap:  tailCap,
		headData: make([]byte, 0, headCap),
		tailData: make([]byte, 0, tailCap),
	}
}

func (b *shellOutputBuffer) Write(p []byte) (int, error) {
	b.total += len(p)
	remaining := p

	if len(b.headData) < b.headCap {
		n := b.headCap - len(b.headData)
		if n > len(remaining) {
			n = len(remaining)
		}
		b.headData = append(b.headData, remaining[:n]...)
		remaining = remaining[n:]
	}

	if b.tailCap > 0 && len(remaining) > 0 {
		if len(remaining) >= b.tailCap {
			b.tailData = append(b.tailData[:0], remaining[len(remaining)-b.tailCap:]...)
		} else {
			b.tailData = append(b.tailData, remaining...)
			if len(b.tailData) > b.tailCap {
				b.tailData = append([]byte{}, b.tailData[len(b.tailData)-b.tailCap:]...)
			}
		}
	}

	return len(p), nil
}

func (b *shellOutputBuffer) String() string {
	if b.total <= b.max {
		combined := make([]byte, 0, len(b.headData)+len(b.tailData))
		combined = append(combined, b.headData...)
		combined = append(combined, b.tailData...)
		return string(combined)
	}
	combined := make([]byte, 0, len(b.headData)+len(shellExecTruncationMarker)+len(b.tailData))
	combined = append(combined, b.headData...)
	combined = append(combined, []byte(shellExecTruncationMarker)...)
	combined = append(combined, b.tailData...)
	return string(combined)
}
