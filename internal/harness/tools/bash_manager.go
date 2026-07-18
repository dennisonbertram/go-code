package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var dangerousBashPatterns = []string{
	`(?i)\brm\s+-rf\s+/`,
	`(?i)\bshutdown\b`,
	`(?i)\breboot\b`,
	`(?i):\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`,
}

const defaultMaxStreamLineBytes = 1 << 20

// SudoRegexp matches sudo invocations. The harness runs as root inside Docker
// containers, so sudo is stripped rather than rejected.
var SudoRegexp = regexp.MustCompile(`(?i)\bsudo\s+(?:-[A-Za-z0-9]+\s+)*`)

// StripSudo removes sudo prefix from a command.
func StripSudo(command string) string {
	return SudoRegexp.ReplaceAllString(command, "")
}

type backgroundJob struct {
	id         string
	command    string
	workingDir string
	startedAt  time.Time

	stdout *headTailBuffer
	stderr *headTailBuffer

	mu       sync.Mutex
	exitCode int
	done     bool
	timedOut bool
	err      error
	cancel   context.CancelFunc
}

type JobManager struct {
	root           string
	nextID         uint64
	mu             sync.RWMutex
	jobs           map[string]*backgroundJob
	closed         bool
	wg             sync.WaitGroup
	maxJobs        int
	ttl            time.Duration
	maxOutputBytes int
	now            func() time.Time
	sandboxScope   SandboxScope // optional sandbox enforcement
}

func NewJobManager(workspaceRoot string, now func() time.Time) *JobManager {
	if now == nil {
		now = time.Now
	}
	return &JobManager{
		root:           workspaceRoot,
		jobs:           make(map[string]*backgroundJob),
		maxJobs:        64,
		ttl:            30 * time.Minute,
		maxOutputBytes: defaultMaxCommandOutputBytes,
		now:            now,
	}
}

// SetSandboxScope configures the sandbox scope enforced for all commands run
// via this JobManager.  It is safe to call before any commands are launched.
func (m *JobManager) SetSandboxScope(scope SandboxScope) {
	m.sandboxScope = scope
}

func (m *JobManager) runForeground(ctx context.Context, command string, timeoutSeconds int, workingDir string) (map[string]any, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	if timeoutSeconds > 300 {
		timeoutSeconds = 300
	}
	scope := m.sandboxScopeForContext(ctx)
	if err := CheckSandboxCommand(scope, m.root, command); err != nil {
		return nil, err
	}
	workDir, err := resolveWorkingDir(m.root, workingDir)
	if err != nil {
		return nil, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	cmd, sandboxCleanup, sbResult, err := buildSandboxedCommand(timeoutCtx, scope, m.root, command)
	if err != nil {
		return nil, err
	}
	defer sandboxCleanup()
	configureGroupKill(cmd)
	cmd.Dir = workDir

	streamer, hasStreamer := OutputStreamerFromContext(ctx)

	stdout := newHeadTailBuffer(m.maxOutputBytes)
	stderr := newHeadTailBuffer(m.maxOutputBytes)
	var streamErr error
	var streamTruncated bool

	if hasStreamer {
		pr, pw := io.Pipe()
		cmd.Stdout = io.MultiWriter(stdout, pw)
		cmd.Stderr = stderr

		var streamDone sync.WaitGroup
		streamDone.Add(1)
		go func() {
			defer streamDone.Done()
			reader := bufio.NewReader(pr)
			for {
				line, truncated, readErr := readStreamLine(reader, defaultMaxStreamLineBytes)
				if line != "" {
					streamer(line)
				}
				if truncated {
					streamTruncated = true
					if streamErr == nil {
						streamErr = fmt.Errorf("stream line exceeded %d bytes", defaultMaxStreamLineBytes)
					}
				}
				if readErr != nil {
					if errors.Is(readErr, io.EOF) {
						return
					}
					streamErr = readErr
					return
				}
			}
		}()

		err = cmd.Run()
		pw.Close()
		streamDone.Wait()
	} else {
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err = cmd.Run()
	}

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			// The process exited normally but a descendant kept the pipes
			// open past WaitDelay; preserve the real exit code (#786).
			exitCode = cmd.ProcessState.ExitCode()
		} else {
			exitCode = -1
		}
	}
	timedOut := errors.Is(timeoutCtx.Err(), context.DeadlineExceeded)
	output := mergeCommandStreams(strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))

	result := map[string]any{
		"command":     command,
		"exit_code":   exitCode,
		"timed_out":   timedOut,
		"output":      output,
		"working_dir": NormalizeRelPath(m.root, workDir),
	}
	if stdout.Truncated() || stderr.Truncated() {
		result["truncated"] = true
		result["max_bytes"] = m.maxOutputBytes
		result["truncation_strategy"] = "head_tail"
		result["hint"] = "[output truncated — use grep/head/tail to narrow results]"
	}
	if streamTruncated {
		result["stream_truncated"] = true
		result["max_line_bytes"] = defaultMaxStreamLineBytes
	}
	if streamErr != nil {
		result["stream_error"] = streamErr.Error()
	}
	if sbResult.Mechanism != "" {
		result["sandbox_mechanism"] = sbResult.Mechanism
	}
	if sbResult.Warning != "" {
		result["sandbox_warning"] = sbResult.Warning
	}
	return result, nil
}

func (m *JobManager) runBackground(ctx context.Context, command string, timeoutSeconds int, workingDir string) (map[string]any, error) {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	if timeoutSeconds > 3600 {
		timeoutSeconds = 3600
	}
	scope := m.sandboxScopeForContext(ctx)
	if err := CheckSandboxCommand(scope, m.root, command); err != nil {
		return nil, err
	}
	workDir, err := resolveWorkingDir(m.root, workingDir)
	if err != nil {
		return nil, err
	}

	m.cleanupExpired()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("job manager is shut down")
	}
	if len(m.jobs) >= m.maxJobs {
		m.mu.Unlock()
		return nil, fmt.Errorf("background job limit reached")
	}
	id := "job_" + strconv.FormatUint(atomic.AddUint64(&m.nextID, 1), 10)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	job := &backgroundJob{
		id:         id,
		command:    command,
		workingDir: workDir,
		startedAt:  m.now(),
		stdout:     newHeadTailBuffer(m.maxOutputBytes),
		stderr:     newHeadTailBuffer(m.maxOutputBytes),
		cancel:     cancel,
		exitCode:   0,
	}
	m.jobs[id] = job
	m.wg.Add(1)
	m.mu.Unlock()

	cmd, sandboxCleanup, sbResult, err := buildSandboxedCommand(ctx, scope, m.root, command)
	if err != nil {
		cancel()
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		m.wg.Done()
		return nil, err
	}
	configureGroupKill(cmd)
	cmd.Dir = workDir
	cmd.Stdout = job.stdout
	cmd.Stderr = job.stderr
	if err := cmd.Start(); err != nil {
		sandboxCleanup()
		cancel()
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		m.wg.Done()
		return nil, fmt.Errorf("start background command: %w", err)
	}

	go func() {
		defer m.wg.Done()
		err := cmd.Wait()
		sandboxCleanup()
		job.mu.Lock()
		defer job.mu.Unlock()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				job.exitCode = exitErr.ExitCode()
			} else if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
				// The process exited normally but a descendant kept the
				// pipes open past WaitDelay; preserve the real exit code (#786).
				job.exitCode = cmd.ProcessState.ExitCode()
			} else {
				job.exitCode = -1
			}
			job.err = err
		}
		job.timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		job.done = true
	}()

	result := map[string]any{
		"shell_id":    id,
		"started":     true,
		"command":     command,
		"working_dir": NormalizeRelPath(m.root, workDir),
	}
	if sbResult.Mechanism != "" {
		result["sandbox_mechanism"] = sbResult.Mechanism
	}
	if sbResult.Warning != "" {
		result["sandbox_warning"] = sbResult.Warning
	}
	return result, nil
}

func readStreamLine(reader *bufio.Reader, maxBytes int) (string, bool, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxStreamLineBytes
	}

	var b strings.Builder
	truncated := false
	for {
		fragment, err := reader.ReadString('\n')
		if fragment != "" {
			remaining := maxBytes - b.Len()
			if remaining > 0 {
				if len(fragment) > remaining {
					b.WriteString(fragment[:remaining])
					truncated = true
				} else {
					b.WriteString(fragment)
				}
			} else {
				truncated = true
			}
		}

		switch {
		case err == nil:
			return b.String(), truncated, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return b.String(), truncated, io.EOF
		default:
			return b.String(), truncated, err
		}
	}
}

// Shutdown cancels every tracked background job, waits for their Wait
// goroutines to return, then clears the job map.
func (m *JobManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	jobs := make([]*backgroundJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, job)
	}
	m.mu.Unlock()

	for _, job := range jobs {
		job.cancel()
	}

	waitDone := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		m.mu.Lock()
		for id := range m.jobs {
			delete(m.jobs, id)
		}
		m.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *JobManager) output(shellID string, wait bool) (map[string]any, error) {
	job := m.get(shellID)
	if job == nil {
		return nil, fmt.Errorf("unknown shell_id %q", shellID)
	}
	if wait {
		deadline := time.Now().Add(5 * time.Second)
		for {
			job.mu.Lock()
			done := job.done
			job.mu.Unlock()
			if done || time.Now().After(deadline) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	job.mu.Lock()
	defer job.mu.Unlock()

	output := mergeCommandStreams(strings.TrimSpace(job.stdout.String()), strings.TrimSpace(job.stderr.String()))
	result := map[string]any{
		"shell_id":   shellID,
		"running":    !job.done,
		"exit_code":  job.exitCode,
		"timed_out":  job.timedOut,
		"output":     output,
		"started_at": job.startedAt,
	}
	if job.stdout.Truncated() || job.stderr.Truncated() {
		result["truncated"] = true
		result["max_bytes"] = m.maxOutputBytes
		result["truncation_strategy"] = "head_tail"
		result["hint"] = "[output truncated — use grep/head/tail to narrow results]"
	}
	return result, nil
}

func (m *JobManager) kill(shellID string) (map[string]any, error) {
	job := m.get(shellID)
	if job == nil {
		return nil, fmt.Errorf("unknown shell_id %q", shellID)
	}
	job.cancel()
	job.mu.Lock()
	job.done = true
	job.mu.Unlock()
	return map[string]any{
		"shell_id": shellID,
		"killed":   true,
	}, nil
}

func (m *JobManager) get(id string) *backgroundJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
}

func (m *JobManager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id, job := range m.jobs {
		job.mu.Lock()
		done := job.done
		started := job.startedAt
		job.mu.Unlock()
		if done && now.Sub(started) > m.ttl {
			delete(m.jobs, id)
		}
	}
}

func (m *JobManager) sandboxScopeForContext(ctx context.Context) SandboxScope {
	if scope, ok := SandboxScopeFromContext(ctx); ok && scope != "" {
		return scope
	}
	return m.sandboxScope
}

func resolveWorkingDir(workspaceRoot, workingDir string) (string, error) {
	if strings.TrimSpace(workingDir) == "" {
		return filepath.Abs(workspaceRoot)
	}
	return ResolveWorkspacePath(workspaceRoot, workingDir)
}
