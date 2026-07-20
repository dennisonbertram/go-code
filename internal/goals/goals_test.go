package goals_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go-agent-harness/internal/goals"
)

func setupManager(t *testing.T) *goals.Manager {
	t.Helper()
	return goals.NewManager(nil) // in-memory store
}

// =============================================================================
// POC 1: Create, read, update, delete goals
// =============================================================================

func TestPOC1_GoalCRUD(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	// Create
	g, err := mgr.Create(ctx, goals.Goal{
		ID:             "goal-001",
		Name:           "Add user authentication",
		Description:    "Implement OAuth2 login flow",
		VerifyCriteria: "All auth tests pass, login page works e2e",
	})
	require.NoError(t, err)
	assert.Equal(t, goals.StatusPending, g.Status)
	assert.NotEmpty(t, g.CreatedAt)

	// Read
	retrieved, err := mgr.Get(ctx, "goal-001")
	require.NoError(t, err)
	assert.Equal(t, "Add user authentication", retrieved.Name)

	// Update progress
	retrieved.Status = goals.StatusRunning
	retrieved.Progress = goals.Progress{Total: 5, Completed: 3}
	updated, err := mgr.Update(ctx, *retrieved)
	require.NoError(t, err)
	assert.Equal(t, goals.StatusRunning, updated.Status)
	assert.Equal(t, 60, updated.Progress.Percent) // 3/5 = 60%

	// Complete
	updated.Status = goals.StatusCompleted
	updated.Progress = goals.Progress{Total: 5, Completed: 5}
	final, err := mgr.Update(ctx, *updated)
	require.NoError(t, err)
	assert.Equal(t, goals.StatusCompleted, final.Status)
	assert.NotNil(t, final.CompletedAt)

	// Delete
	err = mgr.Delete(ctx, "goal-001")
	require.NoError(t, err)
	_, err = mgr.Get(ctx, "goal-001")
	assert.Error(t, err)
}

// =============================================================================
// POC 2: Dependency chains (blocks/blockedBy)
// =============================================================================

func TestPOC2_DependencyChains(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	// Create goals with dependencies
	_, err := mgr.Create(ctx, goals.Goal{
		ID:   "setup-db",
		Name: "Set up database schema",
	})
	require.NoError(t, err)

	_, err = mgr.Create(ctx, goals.Goal{
		ID:        "build-api",
		Name:      "Build REST API",
		DependsOn: []string{"setup-db"},
	})
	require.NoError(t, err)

	_, err = mgr.Create(ctx, goals.Goal{
		ID:        "build-ui",
		Name:      "Build frontend",
		DependsOn: []string{"build-api"},
	})
	require.NoError(t, err)

	// build-api should NOT be ready (depends on setup-db which is pending)
	ready, err := mgr.Ready(ctx)
	require.NoError(t, err)
	readyIDs := goalIDs(ready)
	assert.NotContains(t, readyIDs, "build-api", "build-api depends on setup-db")
	assert.NotContains(t, readyIDs, "build-ui", "build-ui depends on build-api")

	// Complete setup-db
	db, _ := mgr.Get(ctx, "setup-db")
	db.Status = goals.StatusCompleted
	mgr.Update(ctx, *db)

	// Now build-api should be ready
	ready, err = mgr.Ready(ctx)
	require.NoError(t, err)
	readyIDs = goalIDs(ready)
	assert.Contains(t, readyIDs, "build-api")

	// Complete build-api
	api, _ := mgr.Get(ctx, "build-api")
	api.Status = goals.StatusCompleted
	mgr.Update(ctx, *api)

	// Now build-ui should be ready
	ready, err = mgr.Ready(ctx)
	require.NoError(t, err)
	readyIDs = goalIDs(ready)
	assert.Contains(t, readyIDs, "build-ui")
}

func goalIDs(goals []goals.Goal) []string {
	ids := make([]string, len(goals))
	for i, g := range goals {
		ids[i] = g.ID
	}
	return ids
}

// =============================================================================
// POC 3: Progress tracking and percentage calculation
// =============================================================================

func TestPOC3_ProgressTracking(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	g, _ := mgr.Create(ctx, goals.Goal{
		ID:             "migrate",
		Name:           "Migrate 100 endpoints",
		VerifyCriteria: "All 100 endpoints return 200",
		Progress:       goals.Progress{Total: 100, Completed: 0},
	})

	// Simulate progress updates
	steps := []struct {
		completed int
		expected  int
	}{
		{10, 10}, {25, 25}, {50, 50}, {75, 75}, {100, 100},
	}
	for _, step := range steps {
		g.Progress.Completed = step.completed
		g.Status = goals.StatusRunning
		updated, err := mgr.Update(ctx, *g)
		require.NoError(t, err)
		assert.Equal(t, step.expected, updated.Progress.Percent)
		g = updated
	}

	// Mark complete
	g.Status = goals.StatusCompleted
	final, _ := mgr.Update(ctx, *g)
	assert.Equal(t, 100, final.Progress.Percent)
	assert.NotNil(t, final.CompletedAt)
}

// =============================================================================
// POC 4: Filtering and listing
// =============================================================================

func TestPOC4_FilteringAndListing(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	// Create a mix of goals with different statuses
	statuses := []goals.Status{
		goals.StatusPending, goals.StatusPending,
		goals.StatusRunning, goals.StatusRunning, goals.StatusRunning,
		goals.StatusCompleted,
		goals.StatusFailed, goals.StatusFailed,
	}
	for i, s := range statuses {
		mgr.Create(ctx, goals.Goal{
			ID:          fmt.Sprintf("g-%02d", i),
			Name:        fmt.Sprintf("goal-%d", i),
			Status:      s,
			Description: fmt.Sprintf("goal with status %s", s),
		})
	}

	// Filter by pending
	pending, err := mgr.List(ctx, goals.GoalFilter{Status: goals.StatusPending})
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	// Filter by running
	running, err := mgr.List(ctx, goals.GoalFilter{Status: goals.StatusRunning})
	require.NoError(t, err)
	assert.Len(t, running, 3)

	// Filter by completed
	completed, err := mgr.List(ctx, goals.GoalFilter{Status: goals.StatusCompleted})
	require.NoError(t, err)
	assert.Len(t, completed, 1)

	// Filter by failed
	failed, err := mgr.List(ctx, goals.GoalFilter{Status: goals.StatusFailed})
	require.NoError(t, err)
	assert.Len(t, failed, 2)

	// Pagination
	all, err := mgr.List(ctx, goals.GoalFilter{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

// =============================================================================
// POC 5: Concurrent goal creation and updates
// =============================================================================

func TestPOC5_ConcurrentGoalOperations(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// Concurrent creates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := mgr.Create(ctx, goals.Goal{
				ID:   fmt.Sprintf("concurrent-%d", idx),
				Name: fmt.Sprintf("concurrent goal %d", idx),
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}

	// Concurrent updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond) // stagger
			g, err := mgr.Get(ctx, fmt.Sprintf("concurrent-%d", idx))
			if err != nil {
				errs <- err
				return
			}
			g.Status = goals.StatusRunning
			_, err = mgr.Update(ctx, *g)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}

	// Verify all 10 created
	all, err := mgr.List(ctx, goals.GoalFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 10)
}

// =============================================================================
// POC 6: Real-world migration workflow
// =============================================================================

func TestPOC6_MigrationWorkflow(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	// Simulate a multi-step migration workflow
	type step struct {
		id             string
		name           string
		dependsOn      []string
		verifyCriteria string
	}
	steps := []step{
		{"analyze", "Analyze codebase", nil, "Report generated with all endpoints listed"},
		{"plan", "Create migration plan", []string{"analyze"}, "Plan document approved"},
		{"migrate-auth", "Migrate auth module", []string{"plan"}, "Auth tests pass"},
		{"migrate-api", "Migrate API module", []string{"plan"}, "API tests pass"},
		{"migrate-ui", "Migrate UI module", []string{"plan"}, "UI tests pass"},
		{"verify", "Verify full migration", []string{"migrate-auth", "migrate-api", "migrate-ui"}, "All tests pass, no regressions"},
		{"deploy", "Deploy to production", []string{"verify"}, "Deployment successful, smoke tests pass"},
	}

	for _, s := range steps {
		_, err := mgr.Create(ctx, goals.Goal{
			ID:             s.id,
			Name:           s.name,
			DependsOn:      s.dependsOn,
			VerifyCriteria: s.verifyCriteria,
		})
		require.NoError(t, err)
	}

	// Initially, only "analyze" should be ready
	ready, _ := mgr.Ready(ctx)
	readyIDs := goalIDs(ready)
	assert.Len(t, readyIDs, 1)
	assert.Equal(t, "analyze", readyIDs[0])

	// Execute step by step (simulating an agent loop)
	completed := make(map[string]bool)
	for _, s := range steps {
		// Check dependencies satisfied
		ready, _ := mgr.Ready(ctx)
		readySet := goalIDs(ready)
		assert.Contains(t, readySet, s.id, "step %s should be ready", s.id)

		// Execute: pending → running → completed
		g, _ := mgr.Get(ctx, s.id)
		g.Status = goals.StatusRunning
		mgr.Update(ctx, *g)

		g.Status = goals.StatusCompleted
		g.Result = fmt.Sprintf("completed %s", s.name)
		mgr.Update(ctx, *g)
		completed[s.id] = true
	}

	// All should be completed
	all, _ := mgr.List(ctx, goals.GoalFilter{Status: goals.StatusCompleted})
	assert.Len(t, all, len(steps))
}

// =============================================================================
// POC 7: Error handling and edge cases
// =============================================================================

func TestPOC7_ErrorHandling(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	// Get non-existent
	_, err := mgr.Get(ctx, "nonexistent")
	assert.Error(t, err)

	// Update non-existent
	_, err = mgr.Update(ctx, goals.Goal{ID: "nonexistent", Name: "x"})
	assert.Error(t, err)

	// Delete non-existent
	err = mgr.Delete(ctx, "nonexistent")
	assert.Error(t, err)

	// Create with auto-assigned status
	g, err := mgr.Create(ctx, goals.Goal{
		ID:   "auto-status",
		Name: "Should default to pending",
	})
	require.NoError(t, err)
	assert.Equal(t, goals.StatusPending, g.Status)

	// Progress percentage with zero total
	g.Progress = goals.Progress{Total: 0, Completed: 5}
	updated, _ := mgr.Update(ctx, *g)
	assert.Equal(t, 0, updated.Progress.Percent, "percent should be 0 when total is 0")
}

// =============================================================================
// POC 8: Metadata propagation
// =============================================================================

func TestPOC8_MetadataPropagation(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	_, _ = mgr.Create(ctx, goals.Goal{
		ID:   "meta-test",
		Name: "Goal with metadata",
		Metadata: map[string]string{
			"priority":   "high",
			"owner":      "team-alpha",
			"sprint":     "S42",
			"issue_ref":  "GH-1234",
			"deploy_env": "staging",
		},
	})

	retrieved, _ := mgr.Get(ctx, "meta-test")
	assert.Equal(t, "high", retrieved.Metadata["priority"])
	assert.Equal(t, "team-alpha", retrieved.Metadata["owner"])
	assert.Equal(t, "S42", retrieved.Metadata["sprint"])

	// Update metadata
	retrieved.Metadata["status_note"] = "blocked on API team"
	updated, _ := mgr.Update(ctx, *retrieved)
	assert.Equal(t, "blocked on API team", updated.Metadata["status_note"])
}

// =============================================================================
// POC 9: Status lifecycle validation
// =============================================================================

func TestPOC9_StatusLifecycle(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	g, _ := mgr.Create(ctx, goals.Goal{
		ID:             "lifecycle",
		Name:           "Lifecycle test",
		VerifyCriteria: "All checks pass",
	})

	// Normal path: pending → running → verifying → completed
	statuses := []goals.Status{
		goals.StatusRunning,
		goals.StatusVerifying,
		goals.StatusCompleted,
	}
	for _, s := range statuses {
		g.Status = s
		updated, err := mgr.Update(ctx, *g)
		require.NoError(t, err)
		assert.Equal(t, s, updated.Status)
		g = updated
	}

	// Verify completed_at is set
	assert.NotNil(t, g.CompletedAt)
	assert.WithinDuration(t, time.Now().UTC(), *g.CompletedAt, 2*time.Second)

	// Create another and fail it
	g2, _ := mgr.Create(ctx, goals.Goal{ID: "failing", Name: "Will fail"})
	g2.Status = goals.StatusFailed
	g2.Error = "dependency timeout"
	failed, _ := mgr.Update(ctx, *g2)
	assert.Equal(t, goals.StatusFailed, failed.Status)
	assert.Equal(t, "dependency timeout", failed.Error)
	assert.NotNil(t, failed.CompletedAt)

	// Create another and cancel it
	g3, _ := mgr.Create(ctx, goals.Goal{ID: "cancelled", Name: "Will cancel"})
	g3.Status = goals.StatusCancelled
	cancelled, _ := mgr.Update(ctx, *g3)
	assert.Equal(t, goals.StatusCancelled, cancelled.Status)
	assert.NotNil(t, cancelled.CompletedAt)
}

// =============================================================================
// POC 10: Bulk ready-check for fan-out
// =============================================================================

func TestPOC10_BulkReadyForFanOut(t *testing.T) {
	mgr := setupManager(t)
	ctx := context.Background()

	// Create a common dependency
	mgr.Create(ctx, goals.Goal{ID: "common-dep", Name: "Common setup", Status: goals.StatusPending})

	// Create 20 goals that all depend on common-dep
	for i := 0; i < 20; i++ {
		mgr.Create(ctx, goals.Goal{
			ID:        fmt.Sprintf("worker-%d", i),
			Name:      fmt.Sprintf("Worker task %d", i),
			DependsOn: []string{"common-dep"},
		})
	}

	// common-dep should be ready (no dependencies)
	ready, _ := mgr.Ready(ctx)
	assert.Len(t, ready, 1, "common-dep has no deps so it's ready")

	// Complete the common dependency
	dep, _ := mgr.Get(ctx, "common-dep")
	dep.Status = goals.StatusCompleted
	mgr.Update(ctx, *dep)

	// All 20 should now be ready (fan-out)
	ready, _ = mgr.Ready(ctx)
	assert.Len(t, ready, 20, "all 20 workers should be ready after common dep completes")
}
