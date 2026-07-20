package tools

import "context"

// RunForeground is an exported wrapper for the unexported runForeground method.
// Used by tools/core sub-package.
func (m *JobManager) RunForeground(ctx context.Context, command string, timeoutSeconds int, workingDir string) (map[string]any, error) {
	return m.runForeground(ctx, command, timeoutSeconds, workingDir)
}

// RunBackground is an exported wrapper for the unexported runBackground method.
// Used by tools/core sub-package.
func (m *JobManager) RunBackground(command string, timeoutSeconds int, workingDir string) (map[string]any, error) {
	return m.runBackground(context.Background(), command, timeoutSeconds, workingDir)
}

// RunBackgroundWithContext runs a background job using any sandbox scope set on
// the provided execution context.
func (m *JobManager) RunBackgroundWithContext(ctx context.Context, command string, timeoutSeconds int, workingDir string) (map[string]any, error) {
	return m.runBackground(ctx, command, timeoutSeconds, workingDir)
}

// Output is an exported wrapper for the unexported output method.
// Used by tools/core sub-package.
func (m *JobManager) Output(shellID string, wait bool) (map[string]any, error) {
	return m.output(shellID, wait)
}

// Kill is an exported wrapper for the unexported kill method.
// Used by tools/core sub-package.
func (m *JobManager) Kill(shellID string) (map[string]any, error) {
	return m.kill(shellID)
}

// List is an exported wrapper for the unexported list method, returning a
// snapshot of all tracked background jobs. Used by the daemon-level job
// tracker backing the /v1/tasks union (epic #814).
func (m *JobManager) List() []JobInfo {
	return m.list()
}
