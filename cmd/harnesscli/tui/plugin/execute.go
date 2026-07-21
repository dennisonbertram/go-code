package plugin

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"go-agent-harness/internal/skills"
)

const (
	// defaultBashTimeout is the default execution timeout for bash plugin commands.
	defaultBashTimeout = 10 * time.Second

	// maxOutputBytes is the maximum number of bytes captured from a bash command.
	maxOutputBytes = 30 * 1024

	truncationMarker = "\n...[truncated output]...\n"
)

// CommandResult carries the outcome of executing a plugin command.
type CommandResult struct {
	Output  string
	IsError bool
}

// ExecuteBash runs a bash plugin's command and returns the result.
// It applies a 10-second timeout and caps combined stdout+stderr at 30KB
// using a head+tail strategy (15KB head, 15KB tail with truncation marker).
func ExecuteBash(def PluginDef, args []string) CommandResult {
	return ExecuteBashWithTimeout(def, args, defaultBashTimeout)
}

// ExecuteBashWithTimeout is like ExecuteBash but accepts an explicit timeout.
// A timeout of 0 uses the defaultBashTimeout. A timeout of 1ms is effectively
// instant and is useful in tests to verify timeout behaviour.
func ExecuteBashWithTimeout(def PluginDef, args []string, timeout time.Duration) CommandResult {
	if timeout <= 0 {
		timeout = defaultBashTimeout
	}

	cmdStr := def.Command
	if len(args) > 0 {
		cmdStr = cmdStr + " " + strings.Join(args, " ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)

	buf := newHeadTailBuffer(maxOutputBytes)
	cmd.Stdout = buf
	cmd.Stderr = buf

	err := cmd.Run()
	output := buf.String()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			msg := fmt.Sprintf("timeout: command exceeded %v", timeout)
			if output != "" {
				msg = msg + "\n" + output
			}
			return CommandResult{Output: msg, IsError: true}
		}
		errMsg := err.Error()
		if output != "" {
			errMsg = output + "\n" + errMsg
		}
		return CommandResult{Output: errMsg, IsError: true}
	}

	return CommandResult{Output: output, IsError: false}
}

// ExecutePrompt expands a prompt plugin's template by substituting {args}
// with the tokenized-then-joined args string and returns the result. The raw
// args string is tokenized with the shared skills.SplitArgs semantics, so
// quoted multi-word arguments group into single tokens (quote syntax is
// grouping, not literal output). It never returns an error.
func ExecutePrompt(def PluginDef, rawArgs string) CommandResult {
	tokens, err := skills.SplitArgs(rawArgs)
	if err != nil {
		// SplitArgs never returns an error today; keep the join deterministic
		// if a future error path appears.
		tokens = nil
	}
	joined := strings.Join(tokens, " ")
	expanded := strings.ReplaceAll(def.PromptTemplate, "{args}", joined)
	return CommandResult{Output: expanded, IsError: false}
}

// headTailBuffer is a fixed-capacity writer that keeps the first headCap bytes
// and the last tailCap bytes of all written data, inserting a truncation marker
// between them when the total exceeds max.
type headTailBuffer struct {
	max      int
	headCap  int
	tailCap  int
	total    int
	headData []byte
	tailData []byte
}

func newHeadTailBuffer(max int) *headTailBuffer {
	if max <= 0 {
		max = maxOutputBytes
	}
	headCap := max / 2
	tailCap := max - headCap
	return &headTailBuffer{
		max:      max,
		headCap:  headCap,
		tailCap:  tailCap,
		headData: make([]byte, 0, headCap),
		tailData: make([]byte, 0, tailCap),
	}
}

func (b *headTailBuffer) Write(p []byte) (int, error) {
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

func (b *headTailBuffer) String() string {
	if b.total <= b.max {
		combined := make([]byte, 0, len(b.headData)+len(b.tailData))
		combined = append(combined, b.headData...)
		combined = append(combined, b.tailData...)
		return string(combined)
	}

	combined := make([]byte, 0, len(b.headData)+len(truncationMarker)+len(b.tailData))
	combined = append(combined, b.headData...)
	combined = append(combined, []byte(truncationMarker)...)
	combined = append(combined, b.tailData...)
	return string(combined)
}
