package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"go-agent-harness/internal/harness"
)

// maxHookOutputBytes caps how much stdout a hook process may produce. Output
// beyond the cap fails the hook call (a runaway hook must not exhaust
// harness memory).
const maxHookOutputBytes = 1 << 20 // 1 MiB

// CommandHook adapts a kind=command HookDef onto the harness hook
// interfaces: it executes the configured argv with the JSON event on stdin
// and reads a JSON decision from stdout.
//
// One CommandHook implements all four hook interfaces; the wiring layer
// registers it in exactly the slice matching its def's event, so only one of
// the four methods is ever called per instance.
//
// Error semantics: non-zero exit, timeout, over-limit output, and
// unparseable stdout all return a non-nil error. The adapter NEVER maps an
// error to a deny decision — failure policy (fail_open/fail_closed) belongs
// to the runner's existing hook loops.
type CommandHook struct {
	def HookDef
	// Logger, when non-nil, receives one structured Error call per failed
	// exec with hook_name, event, tool_name, duration_ms, exit_code, error.
	Logger harness.Logger
}

// Compile-time checks: CommandHook satisfies the harness hook interfaces
// with zero changes to internal/harness.
var (
	_ harness.PreToolUseHook  = (*CommandHook)(nil)
	_ harness.PostToolUseHook = (*CommandHook)(nil)
)

// NewCommandHook returns a CommandHook for def. The def must be kind
// command; invalid kinds are rejected so a misconfigured def can never exec.
func NewCommandHook(def HookDef) *CommandHook {
	return &CommandHook{def: def}
}

// Name implements the harness hook interfaces.
func (h *CommandHook) Name() string { return h.def.Name }

// PreToolUse implements harness.PreToolUseHook. A non-matching tool name
// returns allow (nil result) without executing anything.
func (h *CommandHook) PreToolUse(ctx context.Context, ev harness.PreToolUseEvent) (*harness.PreToolUseResult, error) {
	if !h.def.MatchesTool(ev.ToolName) {
		return nil, nil
	}
	payload := toolUsePayload{
		Event:    EventPreToolUse,
		RunID:    ev.RunID,
		HookName: h.def.Name,
		ToolName: ev.ToolName,
		CallID:   ev.CallID,
		Args:     normalizeArgs(ev.Args),
	}
	stdout, err := h.exec(ctx, EventPreToolUse, ev.ToolName, payload)
	if err != nil {
		return nil, err
	}
	if len(stdout) == 0 {
		return nil, nil // empty stdout: allow, no modification
	}

	var resp preToolUseResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return nil, h.logError(EventPreToolUse, ev.ToolName, -1, 0,
			fmt.Errorf("parse hook stdout as JSON: %w", err))
	}
	switch resp.Decision {
	case "", decisionAllow:
		if len(resp.ModifiedArgs) > 0 {
			return &harness.PreToolUseResult{ModifiedArgs: resp.ModifiedArgs}, nil
		}
		return nil, nil
	case decisionDeny:
		return &harness.PreToolUseResult{Decision: harness.ToolHookDeny, Reason: resp.Reason}, nil
	default:
		return nil, h.logError(EventPreToolUse, ev.ToolName, -1, 0,
			fmt.Errorf("unknown decision %q: must be %q or %q", resp.Decision, decisionAllow, decisionDeny))
	}
}

// PostToolUse implements harness.PostToolUseHook. A non-matching tool name
// returns no modification (nil result) without executing anything.
func (h *CommandHook) PostToolUse(ctx context.Context, ev harness.PostToolUseEvent) (*harness.PostToolUseResult, error) {
	if !h.def.MatchesTool(ev.ToolName) {
		return nil, nil
	}
	errText := ""
	if ev.Error != nil {
		errText = ev.Error.Error()
	}
	payload := toolUsePayload{
		Event:      EventPostToolUse,
		RunID:      ev.RunID,
		HookName:   h.def.Name,
		ToolName:   ev.ToolName,
		CallID:     ev.CallID,
		Args:       normalizeArgs(ev.Args),
		Result:     ev.Result,
		DurationMS: ev.Duration.Milliseconds(),
		Error:      errText,
	}
	stdout, err := h.exec(ctx, EventPostToolUse, ev.ToolName, payload)
	if err != nil {
		return nil, err
	}
	if len(stdout) == 0 {
		return nil, nil
	}

	var resp postToolUseResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return nil, h.logError(EventPostToolUse, ev.ToolName, -1, 0,
			fmt.Errorf("parse hook stdout as JSON: %w", err))
	}
	if resp.ModifiedResult == "" {
		return nil, nil
	}
	return &harness.PostToolUseResult{ModifiedResult: resp.ModifiedResult}, nil
}

// exec runs the hook command with payload JSON on stdin and returns trimmed
// stdout. The process tree is bounded by the def's timeout and killed on
// expiry; stdout is capped at maxHookOutputBytes.
func (h *CommandHook) exec(ctx context.Context, event, toolName string, payload any) ([]byte, error) {
	start := time.Now()
	input, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal hook payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, h.def.Timeout())
	defer cancel()

	if len(h.def.Command) == 0 {
		return nil, fmt.Errorf("hook %q has no command", h.def.Name)
	}
	cmd := exec.CommandContext(ctx, h.def.Command[0], h.def.Command[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &limitWriter{w: &stdout, n: maxHookOutputBytes}
	cmd.Stderr = &limitWriter{w: &stderr, n: maxHookOutputBytes}

	// Kill the whole process group on timeout so background children spawned
	// by the hook cannot survive as orphans.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Bound the post-kill wait so a grandchild holding pipes open cannot hang
	// cmd.Wait forever.
	cmd.WaitDelay = 2 * time.Second

	runErr := cmd.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return nil, h.logError(event, toolName, -1, duration,
			fmt.Errorf("hook timed out after %s", h.def.Timeout()))
	}
	if runErr != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 200 {
			detail = detail[:200] + "..."
		}
		if detail != "" {
			return nil, h.logError(event, toolName, exitCode, duration,
				fmt.Errorf("hook exited with error (%v): %s", runErr, detail))
		}
		return nil, h.logError(event, toolName, exitCode, duration,
			fmt.Errorf("hook exited with error: %w", runErr))
	}
	return bytes.TrimSpace(stdout.Bytes()), nil
}

// logError reports one structured line via the configured Logger (when set)
// and returns the error for the runner's failure-mode handling.
func (h *CommandHook) logError(event, toolName string, exitCode int, duration time.Duration, err error) error {
	if h.Logger != nil {
		h.Logger.Error("config-driven command hook failed",
			"hook_name", h.def.Name,
			"event", event,
			"tool_name", toolName,
			"duration_ms", duration.Milliseconds(),
			"exit_code", exitCode,
			"error", err.Error(),
		)
	}
	return err
}

// limitWriter writes at most n bytes to w, then fails — bounding hook output.
type limitWriter struct {
	w interface{ Write([]byte) (int, error) }
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, fmt.Errorf("hook output exceeds %d byte limit", maxHookOutputBytes)
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= int64(n)
	return n, err
}
