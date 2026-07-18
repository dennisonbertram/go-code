package subagents

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go-agent-harness/internal/harness"
	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/workspace"
)

type IsolationMode string

const (
	IsolationInline   IsolationMode = "inline"
	IsolationWorktree IsolationMode = "worktree"
)

type CleanupPolicy string

const (
	CleanupPreserve            CleanupPolicy = "preserve"
	CleanupDestroyOnSuccess    CleanupPolicy = "destroy_on_success"
	CleanupDestroyOnCompletion CleanupPolicy = "destroy_on_completion"
)

var (
	ErrNotFound      = errors.New("subagent not found")
	ErrActive        = errors.New("subagent is still active")
	ErrInvalidConfig = errors.New("invalid subagent request")
)

type Request struct {
	TenantID      string `json:"tenant_id,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	Skill         string `json:"skill,omitempty"`
	SkillArgs     string `json:"skill_args,omitempty"`
	Model         string `json:"model,omitempty"`
	ProviderName  string `json:"provider_name,omitempty"`
	AllowFallback bool   `json:"allow_fallback,omitempty"`
	// SystemPrompt overrides the runner's default system prompt for this subagent run.
	// When non-empty, it is forwarded to RunRequest.SystemPrompt.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// AgentIntent selects a named prompt overlay (e.g. "code_review",
	// "autonomous") for this subagent, forwarded to RunRequest.AgentIntent.
	// SystemPrompt (a full override) takes precedence over an intent overlay.
	AgentIntent          string                      `json:"agent_intent,omitempty"`
	MaxSteps             int                         `json:"max_steps,omitempty"`
	MaxCostUSD           float64                     `json:"max_cost_usd,omitempty"`
	ReasoningEffort      string                      `json:"reasoning_effort,omitempty"`
	AllowedTools         []string                    `json:"allowed_tools,omitempty"`
	ProfileName          string                      `json:"profile,omitempty"`
	Permissions          *harness.PermissionConfig   `json:"permissions,omitempty"`
	Isolation            IsolationMode               `json:"isolation,omitempty"`
	CleanupPolicy        CleanupPolicy               `json:"cleanup_policy,omitempty"`
	WorktreeRoot         string                      `json:"worktree_root,omitempty"`
	BaseRef              string                      `json:"base_ref,omitempty"`
	ParentContextHandoff *tools.ParentContextHandoff `json:"parent_context_handoff,omitempty"`
}

type Subagent struct {
	ID               string            `json:"id"`
	TenantID         string            `json:"tenant_id,omitempty"`
	RunID            string            `json:"run_id"`
	Status           harness.RunStatus `json:"status"`
	Isolation        IsolationMode     `json:"isolation"`
	CleanupPolicy    CleanupPolicy     `json:"cleanup_policy"`
	WorkspacePath    string            `json:"workspace_path,omitempty"`
	WorkspaceCleaned bool              `json:"workspace_cleaned"`
	BranchName       string            `json:"branch_name,omitempty"`
	BaseRef          string            `json:"base_ref,omitempty"`
	Output           string            `json:"output,omitempty"`
	Error            string            `json:"error,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type Manager interface {
	Create(ctx context.Context, req Request) (Subagent, error)
	Get(ctx context.Context, id string) (Subagent, error)
	List(ctx context.Context) ([]Subagent, error)
	Delete(ctx context.Context, id string) error
	Cancel(ctx context.Context, id string) error
}

type RunEngine interface {
	StartRun(req harness.RunRequest) (harness.Run, error)
	GetRun(runID string) (harness.Run, bool)
	Subscribe(runID string) ([]harness.Event, <-chan harness.Event, func(), error)
	CancelRun(runID string) error
}

type SkillResolver interface {
	ResolveSkill(ctx context.Context, name, args, workspace string) (string, error)
}

type worktreeWorkspace interface {
	Provision(ctx context.Context, opts workspace.Options) error
	WorkspacePath() string
	Destroy(ctx context.Context) error
	BranchName() string
	BaseRef() string
}

type WorktreeRunnerFactory func(workspaceRoot string) (RunEngine, error)

type WorktreeFactory func(repoPath string) worktreeWorkspace

type managedSubagent struct {
	Subagent
	runner            RunEngine
	workspace         worktreeWorkspace
	cleanupInProgress bool
	cleanupDone       chan struct{}
}

type manager struct {
	inlineRunner          RunEngine
	skillResolver         SkillResolver
	worktreeRunnerFactory WorktreeRunnerFactory
	worktreeFactory       WorktreeFactory
	repoPath              string
	defaultWorktreeRoot   string
	defaultBaseRef        string
	configTOML            string

	mu        sync.RWMutex
	subagents map[string]*managedSubagent
}

type Options struct {
	InlineRunner          RunEngine
	SkillResolver         SkillResolver
	WorktreeRunnerFactory WorktreeRunnerFactory
	WorktreeFactory       WorktreeFactory
	RepoPath              string
	DefaultWorktreeRoot   string
	DefaultBaseRef        string
	ConfigTOML            string
}

func NewManager(opts Options) (Manager, error) {
	if opts.InlineRunner == nil {
		return nil, fmt.Errorf("%w: inline runner is required", ErrInvalidConfig)
	}
	if opts.WorktreeFactory == nil {
		opts.WorktreeFactory = func(repoPath string) worktreeWorkspace {
			return workspace.NewWorktree("", repoPath)
		}
	}
	if opts.DefaultBaseRef == "" {
		opts.DefaultBaseRef = "HEAD"
	}
	if opts.DefaultWorktreeRoot == "" && opts.RepoPath != "" {
		opts.DefaultWorktreeRoot = defaultWorktreeRoot(opts.RepoPath)
	}
	return &manager{
		inlineRunner:          opts.InlineRunner,
		skillResolver:         opts.SkillResolver,
		worktreeRunnerFactory: opts.WorktreeRunnerFactory,
		worktreeFactory:       opts.WorktreeFactory,
		repoPath:              opts.RepoPath,
		defaultWorktreeRoot:   opts.DefaultWorktreeRoot,
		defaultBaseRef:        opts.DefaultBaseRef,
		configTOML:            opts.ConfigTOML,
		subagents:             make(map[string]*managedSubagent),
	}, nil
}

func defaultWorktreeRoot(repoPath string) string {
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		absRepo = repoPath
	}
	base := filepath.Base(absRepo)
	parent := filepath.Dir(absRepo)
	return filepath.Join(parent, base+"-subagents")
}

func (m *manager) Create(ctx context.Context, req Request) (Subagent, error) {
	prompt, err := m.resolvePrompt(ctx, req)
	if err != nil {
		return Subagent{}, err
	}
	isolation := req.Isolation
	if isolation == "" {
		isolation = IsolationInline
	}
	if isolation != IsolationInline && isolation != IsolationWorktree {
		return Subagent{}, fmt.Errorf("%w: unsupported isolation %q", ErrInvalidConfig, isolation)
	}
	cleanupPolicy := req.CleanupPolicy
	if cleanupPolicy == "" {
		cleanupPolicy = CleanupPreserve
	}
	switch cleanupPolicy {
	case CleanupPreserve, CleanupDestroyOnSuccess, CleanupDestroyOnCompletion:
	default:
		return Subagent{}, fmt.Errorf("%w: unsupported cleanup_policy %q", ErrInvalidConfig, cleanupPolicy)
	}

	id := "subagent_" + uuid.NewString()
	now := time.Now().UTC()
	managed := &managedSubagent{
		Subagent: Subagent{
			ID:            id,
			TenantID:      strings.TrimSpace(req.TenantID),
			Isolation:     isolation,
			CleanupPolicy: cleanupPolicy,
			Status:        harness.RunStatusQueued,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}

	runReq := harness.RunRequest{
		Prompt:               prompt,
		Model:                strings.TrimSpace(req.Model),
		ProviderName:         strings.TrimSpace(req.ProviderName),
		AllowFallback:        req.AllowFallback,
		SystemPrompt:         strings.TrimSpace(req.SystemPrompt),
		AgentIntent:          strings.TrimSpace(req.AgentIntent),
		MaxSteps:             req.MaxSteps,
		MaxCostUSD:           req.MaxCostUSD,
		ReasoningEffort:      strings.TrimSpace(req.ReasoningEffort),
		AllowedTools:         append([]string(nil), req.AllowedTools...),
		ProfileName:          strings.TrimSpace(req.ProfileName),
		Permissions:          req.Permissions,
		AgentID:              id,
		ParentContextHandoff: req.ParentContextHandoff,
	}

	switch isolation {
	case IsolationInline:
		managed.runner = m.inlineRunner
	case IsolationWorktree:
		if m.worktreeRunnerFactory == nil {
			return Subagent{}, fmt.Errorf("%w: worktree runner factory is not configured", ErrInvalidConfig)
		}
		if strings.TrimSpace(m.repoPath) == "" {
			return Subagent{}, fmt.Errorf("%w: repo path is required for worktree isolation", ErrInvalidConfig)
		}
		wt := m.worktreeFactory(m.repoPath)
		baseRef := strings.TrimSpace(req.BaseRef)
		if baseRef == "" {
			baseRef = m.defaultBaseRef
		}
		worktreeRoot := strings.TrimSpace(req.WorktreeRoot)
		if worktreeRoot == "" {
			worktreeRoot = m.defaultWorktreeRoot
		}
		provisionOpts := workspace.Options{
			ID:              id,
			RepoPath:        m.repoPath,
			WorktreeRootDir: worktreeRoot,
			WorktreeBaseRef: baseRef,
			ConfigTOML:      m.configTOML,
		}
		if err := wt.Provision(ctx, provisionOpts); err != nil {
			return Subagent{}, err
		}
		managed.workspace = wt
		managed.WorkspacePath = wt.WorkspacePath()
		managed.BranchName = wt.BranchName()
		managed.BaseRef = wt.BaseRef()

		childRunner, err := m.worktreeRunnerFactory(managed.WorkspacePath)
		if err != nil {
			_ = wt.Destroy(context.Background())
			return Subagent{}, err
		}
		managed.runner = childRunner
	}

	run, err := managed.runner.StartRun(runReq)
	if err != nil {
		if managed.workspace != nil {
			_ = managed.workspace.Destroy(context.Background())
			managed.WorkspaceCleaned = true
		}
		return Subagent{}, err
	}

	m.mu.Lock()
	managed.RunID = run.ID
	managed.Status = run.Status
	managed.Output = run.Output
	managed.Error = run.Error
	managed.UpdatedAt = time.Now().UTC()
	m.subagents[id] = managed
	// Snapshot the Subagent value while holding the lock and before the monitor
	// goroutine starts. This prevents a data race between Create returning the
	// snapshot and monitor/refresh writing to the same managedSubagent fields.
	snapshot := managed.Subagent
	m.mu.Unlock()

	go m.monitor(managed)

	return snapshot, nil
}

func (m *manager) Get(_ context.Context, id string) (Subagent, error) {
	managed, err := m.getManaged(id)
	if err != nil {
		return Subagent{}, err
	}
	m.refresh(managed)
	m.applyCleanupPolicy(managed)
	m.waitForCleanupIfNeeded(managed.ID)
	return m.snapshot(managed.ID)
}

func (m *manager) List(_ context.Context) ([]Subagent, error) {
	m.mu.RLock()
	items := make([]*managedSubagent, 0, len(m.subagents))
	for _, item := range m.subagents {
		items = append(items, item)
	}
	m.mu.RUnlock()

	result := make([]Subagent, 0, len(items))
	for _, item := range items {
		m.refresh(item)
		m.applyCleanupPolicy(item)
		m.waitForCleanupIfNeeded(item.ID)
		snap, err := m.snapshot(item.ID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		result = append(result, snap)
	}
	return result, nil
}

func (m *manager) Delete(ctx context.Context, id string) error {
	managed, err := m.getManaged(id)
	if err != nil {
		return err
	}
	m.refresh(managed)
	m.mu.RLock()
	status := managed.Status
	m.mu.RUnlock()
	if status == harness.RunStatusQueued || status == harness.RunStatusRunning || status == harness.RunStatusWaitingForUser {
		return ErrActive
	}
	if err := m.cleanupWorkspace(ctx, managed); err != nil {
		return err
	}
	m.mu.Lock()
	// Delete by the resolved canonical ID, not the raw input id — id may be
	// the RunID (see getManaged's fallback above), which is never a key in
	// m.subagents and would make this a silent no-op.
	delete(m.subagents, managed.ID)
	m.mu.Unlock()
	return nil
}

func (m *manager) Cancel(ctx context.Context, id string) error {
	managed, err := m.getManaged(id)
	if err != nil {
		return err
	}

	m.refresh(managed)
	m.mu.RLock()
	status := managed.Status
	runID := managed.RunID
	m.mu.RUnlock()
	if !isSubagentTerminalStatus(status) {
		if err := managed.runner.CancelRun(runID); err != nil {
			return err
		}
	}

	// Refresh once more so callers get a consistent view of terminal transition.
	m.refresh(managed)
	m.applyCleanupPolicy(managed)
	m.waitForCleanupIfNeeded(managed.ID)
	return nil
}

func (m *manager) resolvePrompt(ctx context.Context, req Request) (string, error) {
	hasPrompt := strings.TrimSpace(req.Prompt) != ""
	hasSkill := strings.TrimSpace(req.Skill) != ""
	if !hasPrompt && !hasSkill {
		return "", fmt.Errorf("%w: either prompt or skill is required", ErrInvalidConfig)
	}
	if hasPrompt && hasSkill {
		return "", fmt.Errorf("%w: prompt and skill are mutually exclusive", ErrInvalidConfig)
	}
	if hasPrompt {
		return strings.TrimSpace(req.Prompt), nil
	}
	if m.skillResolver == nil {
		return "", fmt.Errorf("%w: skill resolver is not configured", ErrInvalidConfig)
	}
	content, err := m.skillResolver.ResolveSkill(ctx, strings.TrimSpace(req.Skill), strings.TrimSpace(req.SkillArgs), "")
	if err != nil {
		return "", err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("%w: resolved skill content is empty", ErrInvalidConfig)
	}
	return content, nil
}

func (m *manager) monitor(managed *managedSubagent) {
	history, stream, cancel, err := managed.runner.Subscribe(managed.RunID)
	if err != nil {
		m.refresh(managed)
		return
	}
	defer cancel()

	for _, ev := range history {
		if harness.IsTerminalEvent(ev.Type) {
			m.refresh(managed)
			m.applyCleanupPolicy(managed)
			return
		}
	}

	for ev := range stream {
		if harness.IsTerminalEvent(ev.Type) {
			m.refresh(managed)
			m.applyCleanupPolicy(managed)
			return
		}
	}
	m.refresh(managed)
	m.applyCleanupPolicy(managed)
}

func (m *manager) applyCleanupPolicy(managed *managedSubagent) {
	m.mu.RLock()
	current, ok := m.subagents[managed.ID]
	if !ok {
		m.mu.RUnlock()
		return
	}
	policy := current.CleanupPolicy
	status := current.Status
	workspaceCleaned := current.WorkspaceCleaned
	hasWorkspace := current.workspace != nil
	m.mu.RUnlock()

	if workspaceCleaned || !hasWorkspace {
		return
	}

	switch policy {
	case CleanupDestroyOnCompletion:
		_ = m.cleanupWorkspace(context.Background(), managed)
	case CleanupDestroyOnSuccess:
		if status == harness.RunStatusCompleted {
			_ = m.cleanupWorkspace(context.Background(), managed)
		}
	}
}

func (m *manager) refresh(managed *managedSubagent) {
	run, ok := managed.runner.GetRun(managed.RunID)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.subagents[managed.ID]
	if !ok {
		return
	}
	current.Status = run.Status
	current.Output = run.Output
	current.Error = run.Error
	current.UpdatedAt = time.Now().UTC()
}

func (m *manager) cleanupWorkspace(ctx context.Context, managed *managedSubagent) error {
	m.mu.Lock()
	current, ok := m.subagents[managed.ID]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if current.cleanupInProgress {
		done := current.cleanupDone
		m.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil
	}
	workspaceRef := current.workspace
	if current.WorkspaceCleaned || workspaceRef == nil {
		m.mu.Unlock()
		return nil
	}
	// Reserve cleanup so concurrent callers do not attempt a second destroy
	// while this one is in flight. We only mark WorkspaceCleaned after the
	// filesystem destroy has actually completed.
	done := make(chan struct{})
	current.cleanupInProgress = true
	current.cleanupDone = done
	current.workspace = nil
	current.UpdatedAt = time.Now().UTC()
	m.mu.Unlock()

	if err := workspaceRef.Destroy(ctx); err != nil {
		m.mu.Lock()
		if latest, ok := m.subagents[managed.ID]; ok {
			latest.cleanupInProgress = false
			latest.cleanupDone = nil
			latest.workspace = workspaceRef
			latest.WorkspaceCleaned = false
			latest.UpdatedAt = time.Now().UTC()
		}
		m.mu.Unlock()
		close(done)
		return err
	}

	m.mu.Lock()
	if latest, ok := m.subagents[managed.ID]; ok {
		latest.cleanupInProgress = false
		latest.cleanupDone = nil
		latest.WorkspaceCleaned = true
		latest.UpdatedAt = time.Now().UTC()
	}
	m.mu.Unlock()
	close(done)
	return nil
}

func (m *manager) getManaged(id string) (*managedSubagent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if managed, ok := m.subagents[id]; ok {
		return managed, nil
	}
	// start_subagent's response carries both `subagent_id` and `run_id` side
	// by side (two similar-looking UUIDs), and models frequently pass the
	// run_id here instead — observed live as a parent repeatedly re-spawning
	// a new subagent after a "not found" error, rather than realizing it just
	// used the wrong id. Fall back to a scan by RunID so every lookup path
	// (get_subagent, wait_subagent, cancel_subagent, message_subagent)
	// resolves correctly either way.
	// ponytail: O(n) fallback scan, only on primary-key miss; upgrade to a
	// secondary runID->id index if a single process ever tracks enough
	// concurrent subagents for this to matter.
	for _, managed := range m.subagents {
		if managed.RunID == id {
			return managed, nil
		}
	}
	return nil, ErrNotFound
}

func (m *manager) snapshot(id string) (Subagent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	managed, ok := m.subagents[id]
	if !ok {
		return Subagent{}, ErrNotFound
	}
	return managed.Subagent, nil
}

func (m *manager) waitForCleanupIfNeeded(id string) {
	for {
		m.mu.RLock()
		managed, ok := m.subagents[id]
		if !ok {
			m.mu.RUnlock()
			return
		}
		status := managed.Status
		policy := managed.CleanupPolicy
		cleaned := managed.WorkspaceCleaned
		inProgress := managed.cleanupInProgress
		done := managed.cleanupDone
		m.mu.RUnlock()

		terminal := isSubagentTerminalStatus(status)
		needsCleanup := policy == CleanupDestroyOnCompletion && terminal
		if policy == CleanupDestroyOnSuccess && status == harness.RunStatusCompleted {
			needsCleanup = true
		}
		if !needsCleanup || cleaned {
			return
		}
		if inProgress && done != nil {
			<-done
			return
		}
		return
	}
}

func isSubagentTerminalStatus(status harness.RunStatus) bool {
	return status == harness.RunStatusCompleted || status == harness.RunStatusFailed || status == harness.RunStatusCancelled
}
