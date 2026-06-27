package workflow

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	sourceManifestName       = "workflow.json"
	defaultWorkflowTimeout   = 5 * time.Minute
	maxWorkflowProtocolBytes = 1024 * 1024
	maxWorkflowStderrBytes   = 32 * 1024
)

var sourceWorkflowNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// SourceManager discovers, builds, registers, and runs Go-authored workflow
// bundles without requiring the harness process to restart.
type SourceManager struct {
	engine *Engine

	workflowDirs []string
	skillDirs    []string
	cacheDir     string
	moduleRoot   string
	goBinary     string

	mu      sync.RWMutex
	bundles map[string]*SourceBundle
}

type SourceService interface {
	List() []Meta
	Start(ctx context.Context, name string, args any) (*Run, error)
	Resume(ctx context.Context, runID string, args any) (*Run, error)
	GetRun(runID string) (*Run, error)
	Subscribe(runID string) ([]Event, <-chan Event, func(), error)
	Wait(ctx context.Context, runID string) (*Run, []Event, error)
	CreateWorkflow(ctx context.Context, req CreateWorkflowRequest) (*SourceBundle, error)
}

type SourceManagerOptions struct {
	Engine       *Engine
	WorkflowDirs []string
	SkillDirs    []string
	CacheDir     string
	ModuleRoot   string
	GoBinary     string
}

type SourceBundleManifest struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Version        int            `json:"version"`
	Language       string         `json:"language"`
	Entrypoint     string         `json:"entrypoint"`
	WhenToUse      string         `json:"when_to_use,omitempty"`
	ArgsSchema     map[string]any `json:"args_schema,omitempty"`
	Skill          string         `json:"skill,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
}

type SourceBundle struct {
	Manifest SourceBundleManifest `json:"manifest"`
	Dir      string               `json:"dir"`
	Hash     string               `json:"hash"`
	Binary   string               `json:"binary"`
	Scope    string               `json:"scope"`
}

type CreateWorkflowRequest struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	WhenToUse      string         `json:"when_to_use,omitempty"`
	Source         string         `json:"source"`
	ArgsSchema     map[string]any `json:"args_schema,omitempty"`
	Scope          string         `json:"scope,omitempty"`
	Skill          string         `json:"skill,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
	Overwrite      bool           `json:"overwrite,omitempty"`
}

func NewSourceManager(opts SourceManagerOptions) (*SourceManager, error) {
	if opts.Engine == nil {
		return nil, fmt.Errorf("workflow source manager requires an engine")
	}
	if opts.GoBinary == "" {
		opts.GoBinary = "go"
	}
	if opts.CacheDir == "" {
		opts.CacheDir = filepath.Join(os.TempDir(), "go-agent-harness-workflows")
	}
	moduleRoot := strings.TrimSpace(opts.ModuleRoot)
	if moduleRoot == "" {
		moduleRoot = findModuleRoot()
	}
	return &SourceManager{
		engine:       opts.Engine,
		workflowDirs: cleanDirs(opts.WorkflowDirs),
		skillDirs:    cleanDirs(opts.SkillDirs),
		cacheDir:     opts.CacheDir,
		moduleRoot:   moduleRoot,
		goBinary:     opts.GoBinary,
		bundles:      make(map[string]*SourceBundle),
	}, nil
}

func cleanDirs(dirs []string) []string {
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if strings.TrimSpace(dir) != "" {
			out = append(out, dir)
		}
	}
	return out
}

func (m *SourceManager) Load(ctx context.Context) error {
	bundles, err := m.discover(ctx)
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		if err := m.build(ctx, bundle); err != nil {
			continue
		}
		m.register(bundle)
	}
	return nil
}

func (m *SourceManager) List() []Meta {
	return m.engine.List()
}

func (m *SourceManager) Start(ctx context.Context, name string, args any) (*Run, error) {
	return m.engine.Start(ctx, name, args)
}

func (m *SourceManager) Resume(ctx context.Context, runID string, args any) (*Run, error) {
	return m.engine.Resume(ctx, runID, args)
}

func (m *SourceManager) GetRun(runID string) (*Run, error) {
	return m.engine.GetRun(runID)
}

func (m *SourceManager) Subscribe(runID string) ([]Event, <-chan Event, func(), error) {
	return m.engine.Subscribe(runID)
}

func (m *SourceManager) Wait(ctx context.Context, runID string) (*Run, []Event, error) {
	history, stream, cancel, err := m.Subscribe(runID)
	if err != nil {
		return nil, nil, err
	}
	defer cancel()
	events := append([]Event(nil), history...)
	if run, err := m.GetRun(runID); err == nil && isTerminalRun(run.Status) {
		return run, events, nil
	}
	for {
		select {
		case <-ctx.Done():
			run, _ := m.GetRun(runID)
			return run, events, ctx.Err()
		case ev, ok := <-stream:
			if !ok {
				run, _ := m.GetRun(runID)
				return run, events, nil
			}
			events = append(events, ev)
			if ev.Type == EventWorkflowCompleted || ev.Type == EventWorkflowFailed {
				run, _ := m.GetRun(runID)
				return run, events, nil
			}
		}
	}
}

func isTerminalRun(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed
}

func (m *SourceManager) CreateWorkflow(ctx context.Context, req CreateWorkflowRequest) (*SourceBundle, error) {
	name := strings.TrimSpace(req.Name)
	if !sourceWorkflowNameRe.MatchString(name) {
		return nil, fmt.Errorf("workflow name %q must be kebab-case", req.Name)
	}
	if strings.TrimSpace(req.Description) == "" {
		return nil, fmt.Errorf("description is required")
	}
	if strings.TrimSpace(req.Source) == "" {
		return nil, fmt.Errorf("source is required")
	}
	dir, scope, err := m.createDir(req)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(dir); statErr == nil && !req.Overwrite {
		return nil, fmt.Errorf("workflow %q already exists at %s", name, dir)
	}
	if req.Overwrite {
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	manifest := SourceBundleManifest{
		Name:           name,
		Description:    strings.TrimSpace(req.Description),
		Version:        1,
		Language:       "go",
		Entrypoint:     "main.go",
		WhenToUse:      strings.TrimSpace(req.WhenToUse),
		ArgsSchema:     req.ArgsSchema,
		Skill:          strings.TrimSpace(req.Skill),
		TimeoutSeconds: req.TimeoutSeconds,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, sourceManifestName), append(raw, '\n'), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.Entrypoint), []byte(strings.TrimSpace(req.Source)+"\n"), 0o644); err != nil {
		return nil, err
	}
	bundle, err := m.loadBundle(dir, scope)
	if err != nil {
		return nil, err
	}
	if err := m.build(ctx, bundle); err != nil {
		return nil, err
	}
	m.register(bundle)
	return bundle, nil
}

func (m *SourceManager) createDir(req CreateWorkflowRequest) (string, string, error) {
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "workspace"
	}
	switch scope {
	case "workspace":
		if len(m.workflowDirs) == 0 {
			return "", "", fmt.Errorf("workspace workflow directory is not configured")
		}
		return filepath.Join(m.workflowDirs[len(m.workflowDirs)-1], req.Name), scope, nil
	case "global":
		if len(m.workflowDirs) == 0 {
			return "", "", fmt.Errorf("global workflow directory is not configured")
		}
		return filepath.Join(m.workflowDirs[0], req.Name), scope, nil
	case "skill":
		skill := strings.TrimSpace(req.Skill)
		if skill == "" {
			return "", "", fmt.Errorf("skill is required when scope=skill")
		}
		for i := len(m.skillDirs) - 1; i >= 0; i-- {
			root := m.skillDirs[i]
			if root == "" {
				continue
			}
			return filepath.Join(root, skill, "workflows", req.Name), scope, nil
		}
		return "", "", fmt.Errorf("skill workflow directory is not configured")
	default:
		return "", "", fmt.Errorf("unsupported workflow scope %q", scope)
	}
}

func (m *SourceManager) discover(ctx context.Context) ([]*SourceBundle, error) {
	var out []*SourceBundle
	for _, root := range m.workflowDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bundles, err := discoverDirectBundles(root, "workflow")
		if err != nil {
			return nil, err
		}
		out = append(out, bundles...)
	}
	for _, skillRoot := range m.skillDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bundles, err := discoverSkillBundles(skillRoot)
		if err != nil {
			return nil, err
		}
		out = append(out, bundles...)
	}
	return out, nil
}

func discoverDirectBundles(root, scope string) ([]*SourceBundle, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*SourceBundle
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bundle, err := loadSourceBundle(filepath.Join(root, entry.Name()), scope)
		if err != nil {
			continue
		}
		if bundle != nil {
			out = append(out, bundle)
		}
	}
	return out, nil
}

func discoverSkillBundles(skillRoot string) ([]*SourceBundle, error) {
	entries, err := os.ReadDir(skillRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*SourceBundle
	for _, skill := range entries {
		if !skill.IsDir() {
			continue
		}
		root := filepath.Join(skillRoot, skill.Name(), "workflows")
		bundles, err := discoverDirectBundles(root, "skill")
		if err != nil {
			return nil, err
		}
		for _, bundle := range bundles {
			if bundle.Manifest.Skill == "" {
				bundle.Manifest.Skill = skill.Name()
			}
			out = append(out, bundle)
		}
	}
	return out, nil
}

func (m *SourceManager) loadBundle(dir, scope string) (*SourceBundle, error) {
	return loadSourceBundle(dir, scope)
}

func loadSourceBundle(dir, scope string) (*SourceBundle, error) {
	manifestPath := filepath.Join(dir, sourceManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var manifest SourceBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
	}
	if err := validateSourceManifest(manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	return &SourceBundle{Manifest: manifest, Dir: dir, Scope: scope}, nil
}

func validateSourceManifest(manifest SourceBundleManifest) error {
	if !sourceWorkflowNameRe.MatchString(manifest.Name) {
		return fmt.Errorf("name %q must be kebab-case", manifest.Name)
	}
	if strings.TrimSpace(manifest.Description) == "" {
		return fmt.Errorf("description is required")
	}
	if manifest.Version != 1 {
		return fmt.Errorf("version must be 1")
	}
	if manifest.Language != "go" {
		return fmt.Errorf("language must be go")
	}
	if strings.TrimSpace(manifest.Entrypoint) == "" {
		return fmt.Errorf("entrypoint is required")
	}
	return nil
}

func (m *SourceManager) register(bundle *SourceBundle) {
	b := *bundle
	m.mu.Lock()
	m.bundles[b.Manifest.Name] = &b
	m.mu.Unlock()
	m.engine.RegisterWithMeta(Meta{
		Name:        b.Manifest.Name,
		Description: b.Manifest.Description,
		WhenToUse:   b.Manifest.WhenToUse,
	}, func(ctx *Context) (any, error) {
		return m.runSourceWorkflow(ctx, &b)
	})
}

func (m *SourceManager) build(ctx context.Context, bundle *SourceBundle) error {
	hash, err := hashBundle(bundle.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(m.cacheDir, "bin"), 0o755); err != nil {
		return err
	}
	binary := filepath.Join(m.cacheDir, "bin", bundle.Manifest.Name+"-"+hash)
	bundle.Hash = hash
	bundle.Binary = binary
	if _, err := os.Stat(binary); err == nil {
		return nil
	}
	if strings.TrimSpace(m.moduleRoot) == "" {
		return fmt.Errorf("harness module root is not configured; set HARNESS_SOURCE_ROOT")
	}
	buildDir := filepath.Join(m.cacheDir, "src", bundle.Manifest.Name+"-"+hash)
	if err := os.RemoveAll(buildDir); err != nil {
		return err
	}
	if err := copyTree(bundle.Dir, buildDir); err != nil {
		return err
	}
	mod := fmt.Sprintf("module workflow.local/%s\n\ngo 1.25.0\n\nrequire go-agent-harness v0.0.0\n\nreplace go-agent-harness => %s\n",
		bundle.Manifest.Name, filepath.ToSlash(m.moduleRoot))
	if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte(mod), 0o644); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, m.goBinary, "build", "-o", binary, ".")
	cmd.Dir = buildDir
	cmd.Env = minimalGoEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build workflow %q: %w: %s", bundle.Manifest.Name, err, boundedString(stderr.String(), maxWorkflowStderrBytes))
	}
	return nil
}

func (m *SourceManager) runSourceWorkflow(ctx *Context, bundle *SourceBundle) (any, error) {
	if err := m.build(ctx.ctx, bundle); err != nil {
		return nil, err
	}
	timeout := defaultWorkflowTimeout
	if bundle.Manifest.TimeoutSeconds > 0 {
		timeout = time.Duration(bundle.Manifest.TimeoutSeconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx.ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bundle.Binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = minimalChildEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, max: maxWorkflowStderrBytes}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	enc := json.NewEncoder(stdin)
	if err := enc.Encode(protocolResponse{Type: "start", Result: mustRaw(ctx.Args)}); err != nil {
		_ = killProcessGroup(cmd)
		return nil, err
	}

	result, protocolErr := m.serveProtocol(runCtx, ctx, stdout, enc)
	if protocolErr != nil {
		_ = killProcessGroup(cmd)
	}
	closeErr := stdin.Close()
	waitErr := cmd.Wait()
	if runCtx.Err() == context.DeadlineExceeded {
		_ = killProcessGroup(cmd)
		return nil, fmt.Errorf("workflow %q timed out after %s", bundle.Manifest.Name, timeout)
	}
	if protocolErr != nil {
		_ = killProcessGroup(cmd)
		return nil, protocolErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("workflow %q exited: %w: %s", bundle.Manifest.Name, waitErr, boundedString(stderr.String(), maxWorkflowStderrBytes))
	}
	if result == nil {
		return nil, fmt.Errorf("workflow %q exited without a result", bundle.Manifest.Name)
	}
	return result, nil
}

type protocolMessage struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	Args   json.RawMessage `json:"args,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type protocolResponse struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (m *SourceManager) serveProtocol(runCtx context.Context, ctx *Context, stdout io.Reader, enc *json.Encoder) (any, error) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxWorkflowProtocolBytes)
	var result any
	terminal := false
	for scanner.Scan() {
		var msg protocolMessage
		line := scanner.Bytes()
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("workflow protocol error: invalid json: %w", err)
		}
		if terminal {
			return nil, fmt.Errorf("workflow protocol error: message after terminal result")
		}
		switch msg.Type {
		case "result":
			if result != nil {
				return nil, fmt.Errorf("workflow protocol error: duplicate result")
			}
			terminal = true
			if len(msg.Result) > 0 {
				if err := json.Unmarshal(msg.Result, &result); err != nil {
					return nil, err
				}
			} else {
				result = map[string]any{}
			}
		case "error":
			if msg.Error == "" {
				msg.Error = "workflow child returned an error"
			}
			return nil, fmt.Errorf("%s", msg.Error)
		case "phase":
			var args struct {
				Title string `json:"title"`
			}
			if err := decodeArgs(msg.Args, &args); err != nil {
				return nil, err
			}
			ctx.Phase(args.Title)
			if err := enc.Encode(protocolResponse{ID: msg.ID, Result: mustRaw(true)}); err != nil {
				return nil, err
			}
		case "log":
			var args struct {
				Message string `json:"message"`
			}
			if err := decodeArgs(msg.Args, &args); err != nil {
				return nil, err
			}
			ctx.Log(args.Message)
			if err := enc.Encode(protocolResponse{ID: msg.ID, Result: mustRaw(true)}); err != nil {
				return nil, err
			}
		case "feedback":
			var args struct {
				Kind    string         `json:"kind"`
				Message string         `json:"message"`
				Data    map[string]any `json:"data"`
			}
			if err := decodeArgs(msg.Args, &args); err != nil {
				return nil, err
			}
			ctx.Feedback(args.Kind, args.Message, args.Data)
			if err := enc.Encode(protocolResponse{ID: msg.ID, Result: mustRaw(true)}); err != nil {
				return nil, err
			}
		case "agent":
			resp, err := m.handleAgent(runCtx, ctx, msg.Args)
			if err := writeProtocolResponse(enc, msg.ID, resp, err); err != nil {
				return nil, err
			}
		case "workflow":
			resp, err := m.handleNestedWorkflow(ctx, msg.Args)
			if err := writeProtocolResponse(enc, msg.ID, resp, err); err != nil {
				return nil, err
			}
		case "question":
			resp, err := m.handleQuestion(ctx, msg.Args)
			if err := writeProtocolResponse(enc, msg.ID, resp, err); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("workflow protocol error: unknown message type %q", msg.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *SourceManager) handleAgent(_ context.Context, ctx *Context, raw json.RawMessage) (*AgentResult, error) {
	var args struct {
		Prompt string     `json:"prompt"`
		Opts   *AgentOpts `json:"opts"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	return ctx.Agent(args.Prompt, args.Opts)
}

func (m *SourceManager) handleNestedWorkflow(ctx *Context, raw json.RawMessage) (any, error) {
	var args struct {
		Name string `json:"name"`
		Args any    `json:"args"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	return ctx.Workflow(args.Name, args.Args)
}

func (m *SourceManager) handleQuestion(ctx *Context, raw json.RawMessage) (any, error) {
	var args struct {
		Prompt  string           `json:"prompt"`
		Choices []QuestionOption `json:"choices"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	return ctx.Question(args.Prompt, args.Choices)
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode workflow protocol args: %w", err)
	}
	return nil
}

func writeProtocolResponse(enc *json.Encoder, id string, result any, err error) error {
	resp := protocolResponse{ID: id}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Result = mustRaw(result)
	}
	return enc.Encode(resp)
}

func mustRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}

func hashBundle(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		h.Write([]byte(rel))
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write(raw)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode())
	})
}

func minimalGoEnv() []string {
	env := minimalChildEnv()
	env = append(env, "GOWORK=off")
	return env
}

func minimalChildEnv() []string {
	return []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func boundedString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:max]) + "...[truncated]"
}

type limitedWriter struct {
	w   io.Writer
	max int
	n   int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.max <= 0 || w.n >= w.max {
		return len(p), nil
	}
	remaining := w.max - w.n
	write := p
	if len(write) > remaining {
		write = write[:remaining]
	}
	n, err := w.w.Write(write)
	w.n += n
	if err != nil {
		return n, err
	}
	return len(p), nil
}

func findModuleRoot() string {
	candidates := []string{os.Getenv("HARNESS_SOURCE_ROOT")}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	for _, start := range candidates {
		if root := findModuleRootFrom(start); root != "" {
			return root
		}
	}
	return ""
}

func findModuleRootFrom(start string) string {
	if strings.TrimSpace(start) == "" {
		return ""
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	info, err := os.Stat(current)
	if err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		mod := filepath.Join(current, "go.mod")
		raw, err := os.ReadFile(mod)
		if err == nil && strings.Contains(string(raw), "module go-agent-harness") {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
