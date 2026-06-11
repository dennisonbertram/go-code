package workspace

import (
	"context"
	"errors"
)

// Workspace represents an isolated agent execution environment.
// Implementations are responsible for provisioning and tearing down
// the filesystem, process, and network resources needed to run a
// harnessd agent loop.
type Workspace interface {
	// Provision sets up the workspace using the provided options.
	// It must be called before HarnessURL or WorkspacePath are valid.
	Provision(ctx context.Context, opts Options) error

	// WaitReady blocks until the harnessd instance inside the workspace is
	// actually serving requests (not just running at the OS/container level).
	// It returns nil when HarnessURL()+/healthz responds with 200 OK, or a
	// descriptive error on timeout. For workspaces without an inner harnessd
	// (local, worktree), it is a no-op returning nil.
	WaitReady(ctx context.Context) error

	// HarnessURL returns the HTTP endpoint of the harnessd instance
	// running inside this workspace. Valid only after Provision succeeds.
	HarnessURL() string

	// WorkspacePath returns the filesystem path of the workspace root.
	// Valid only after Provision succeeds.
	WorkspacePath() string

	// Destroy tears down the workspace, releasing all associated resources.
	Destroy(ctx context.Context) error
}

// Options configures workspace provisioning.
type Options struct {
	// ID is a required unique identifier for the workspace (e.g. issue ID).
	ID string

	// RepoURL is an optional git repository to clone into the workspace.
	RepoURL string

	// BaseDir is an optional base directory under which workspace roots are created.
	BaseDir string

	// RepoPath is the git repository path used by worktree-backed workspaces.
	// When empty, implementations may fall back to BaseDir for backward compatibility.
	RepoPath string

	// WorktreeRootDir is the parent directory under which worktree paths are created.
	// Used only by worktree-backed implementations.
	WorktreeRootDir string

	// WorktreeBaseRef is the base git ref/commit used when creating a new worktree branch.
	// Used only by worktree-backed implementations. Empty means implementation default.
	WorktreeBaseRef string

	// Env holds optional additional environment variables for the workspace.
	// For container workspaces, these are passed directly to the container environment.
	// For secrets (e.g. API keys), always use this field rather than ConfigTOML.
	Env map[string]string

	// ConfigTOML is an optional serialized TOML configuration string for the
	// subagent harness instance running in this workspace. When non-empty,
	// each Provision() implementation writes it to harness.toml in the
	// workspace root directory. This enables deterministic config propagation
	// from the parent harness to spawned subagents.
	// NEVER include secrets (API keys, tokens) in this field — use Env instead.
	ConfigTOML string
}

// Factory is a constructor function that returns a new, unprovisioned Workspace.
// The returned Workspace must not be nil.
type Factory func() Workspace

// Sentinel errors returned by Registry operations.
var (
	// ErrNotFound is returned when no implementation is registered under a given name.
	ErrNotFound = errors.New("workspace: implementation not found")

	// ErrAlreadyExists is returned when an implementation with the same name has
	// already been registered.
	ErrAlreadyExists = errors.New("workspace: implementation already registered")

	// ErrInvalidID is returned when Options.ID is empty.
	ErrInvalidID = errors.New("workspace: ID must not be empty")

	// ErrInvalidName is returned when the workspace implementation name is empty.
	ErrInvalidName = errors.New("workspace: name must not be empty")
)
