package harness

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	htools "go-agent-harness/internal/harness/tools"
)

// ErrJobNotFound is returned by JobTracker.Kill when the (possibly
// namespaced) task ID does not identify any tracked background job.
var ErrJobNotFound = errors.New("background job not found")

// TrackedJob pairs a daemon-unique task ID with a job snapshot. TaskID is
// namespaced as "<managerRef>:<shellID>" (e.g. "jm2:job_1") because every
// JobManager numbers its own jobs from job_1 and IDs collide across managers.
type TrackedJob struct {
	TaskID string
	Info   htools.JobInfo
}

// JobTracker is the daemon-level registry of per-registry JobManagers. Each
// tool registry built by NewDefaultRegistryWithOptions owns a JobManager;
// registries created with DefaultRegistryOptions.JobTracker set register
// their manager here so the /v1/tasks union (epic #814) can enumerate and
// kill background bash jobs across the whole daemon — main registry, per-run
// provisioned-workspace registries, and subagent worktree registries alike.
//
// All methods are safe for concurrent use.
type JobTracker struct {
	mu       sync.RWMutex
	nextSeq  uint64
	managers map[string]*htools.JobManager // ref -> manager
	refs     map[*htools.JobManager]string // manager -> ref (idempotent Register)
}

// NewJobTracker returns an empty tracker.
func NewJobTracker() *JobTracker {
	return &JobTracker{
		managers: make(map[string]*htools.JobManager),
		refs:     make(map[*htools.JobManager]string),
	}
}

// Register makes a manager's jobs visible to List/Get/Kill and returns the
// manager's ref. Registering the same manager twice returns the original ref.
func (t *JobTracker) Register(m *htools.JobManager) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ref, ok := t.refs[m]; ok {
		return ref
	}
	t.nextSeq++
	ref := fmt.Sprintf("jm%d", t.nextSeq)
	t.managers[ref] = m
	t.refs[m] = ref
	return ref
}

// Unregister removes the manager with the given ref. Unknown refs are a no-op.
func (t *JobTracker) Unregister(ref string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.managers[ref]
	if !ok {
		return
	}
	delete(t.managers, ref)
	delete(t.refs, m)
}

// List unions job snapshots across every registered manager, namespacing each
// job's ID into a daemon-unique TaskID. Output is sorted by TaskID for
// deterministic responses.
func (t *JobTracker) List() []TrackedJob {
	t.mu.RLock()
	type entry struct {
		ref string
		mgr *htools.JobManager
	}
	entries := make([]entry, 0, len(t.managers))
	for ref, mgr := range t.managers {
		entries = append(entries, entry{ref: ref, mgr: mgr})
	}
	t.mu.RUnlock()

	var out []TrackedJob
	for _, e := range entries {
		for _, info := range e.mgr.List() {
			out = append(out, TrackedJob{
				TaskID: e.ref + ":" + info.ID,
				Info:   info,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// Get returns the tracked job for a namespaced task ID, or false when the
// manager ref, the job, or the ID format itself is unknown.
func (t *JobTracker) Get(taskID string) (TrackedJob, bool) {
	mgr, shellID, ok := t.resolve(taskID)
	if !ok {
		return TrackedJob{}, false
	}
	for _, info := range mgr.List() {
		if info.ID == shellID {
			return TrackedJob{TaskID: taskID, Info: info}, true
		}
	}
	return TrackedJob{}, false
}

// Kill terminates the job identified by a namespaced task ID, reusing the
// owning manager's Kill (same mechanism as the agent-facing job_kill tool).
// Unknown task IDs return ErrJobNotFound.
func (t *JobTracker) Kill(taskID string) error {
	mgr, shellID, ok := t.resolve(taskID)
	if !ok {
		return ErrJobNotFound
	}
	if _, err := mgr.Kill(shellID); err != nil {
		return fmt.Errorf("%w: %v", ErrJobNotFound, err)
	}
	return nil
}

// resolve splits a namespaced task ID and returns the owning manager.
func (t *JobTracker) resolve(taskID string) (*htools.JobManager, string, bool) {
	ref, shellID, found := strings.Cut(taskID, ":")
	if !found || ref == "" || shellID == "" {
		return nil, "", false
	}
	t.mu.RLock()
	mgr, ok := t.managers[ref]
	t.mu.RUnlock()
	if !ok {
		return nil, "", false
	}
	return mgr, shellID, true
}
