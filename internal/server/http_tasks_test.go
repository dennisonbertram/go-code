package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/store"
	"go-agent-harness/internal/subagents"
)

// mockCallbackLister is a test double for the CallbackLister option.
type mockCallbackLister struct {
	callbacks []tools.CallbackInfo
}

func (m mockCallbackLister) ListAll() []tools.CallbackInfo { return m.callbacks }

// listTasks performs GET /v1/tasks and decodes the response body.
func listTasks(t *testing.T, ts *httptest.Server, token string) (int, []Task) {
	t.Helper()
	code, body := doSubagentRequest(t, ts, http.MethodGet, token, "/v1/tasks", nil)
	var decoded struct {
		Tasks []Task `json:"tasks"`
	}
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("decode /v1/tasks response: %v (body %s)", err, body)
		}
	}
	return code, decoded.Tasks
}

// TestTasksEndpoint_EmptyUnion verifies that a server with none of the task
// sources configured still returns 200 with an empty (non-null) task list.
func TestTasksEndpoint_EmptyUnion(t *testing.T) {
	t.Parallel()

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t)})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, tasks := listTasks(t, ts, "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/tasks: status %d, want 200", code)
	}
	if tasks == nil {
		t.Fatal("GET /v1/tasks: tasks field is null, want []")
	}
	if len(tasks) != 0 {
		t.Fatalf("GET /v1/tasks: got %d tasks, want 0: %+v", len(tasks), tasks)
	}
}

// TestTasksEndpoint_UnionsAllSources verifies that subagents, cron jobs, and
// pending callbacks each contribute correctly typed entries to the union.
func TestTasksEndpoint_UnionsAllSources(t *testing.T) {
	t.Parallel()

	started := time.Now().UTC().Add(-2 * time.Minute)
	subMgr := &mockSubagentManager{
		listFn: func(context.Context) ([]subagents.Subagent, error) {
			return []subagents.Subagent{{
				ID:         "sub-1",
				RunID:      "run-1",
				Status:     harness.RunStatusRunning,
				BranchName: "workspace-sub-1",
				CreatedAt:  started,
				UpdatedAt:  started,
			}}, nil
		},
	}
	cronClient := newMockCronClient()
	if _, err := cronClient.CreateJob(context.Background(), tools.CronCreateJobRequest{
		Name:     "nightly-sync",
		Schedule: "0 3 * * *",
	}); err != nil {
		t.Fatalf("seed cron job: %v", err)
	}
	callbacks := mockCallbackLister{callbacks: []tools.CallbackInfo{{
		ID:             "cb-1",
		ConversationID: "conv-1",
		Prompt:         "check the deploy",
		State:          tools.CallbackStatePending,
		CreatedAt:      started,
		FiresAt:        started.Add(5 * time.Minute),
	}}}

	handler := NewWithOptions(ServerOptions{
		Runner:          testRunnerForAgents(t),
		SubagentManager: subMgr,
		CronClient:      cronClient,
		CallbackLister:  callbacks,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, tasks := listTasks(t, ts, "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/tasks: status %d, want 200", code)
	}
	if len(tasks) != 3 {
		t.Fatalf("GET /v1/tasks: got %d tasks, want 3: %+v", len(tasks), tasks)
	}

	byType := make(map[string]Task, len(tasks))
	for _, task := range tasks {
		byType[task.Type] = task
		if task.StartedAt.IsZero() {
			t.Errorf("task %s (%s): started_at is zero", task.ID, task.Type)
		}
		if task.AgeSeconds < 0 {
			t.Errorf("task %s (%s): negative age_seconds %d", task.ID, task.Type, task.AgeSeconds)
		}
	}

	sub, ok := byType["subagent"]
	if !ok {
		t.Fatalf("no subagent task in union: %+v", tasks)
	}
	if sub.ID != "sub-1" || sub.Status != "running" || sub.Label == "" {
		t.Errorf("subagent task = %+v, want id sub-1 status running non-empty label", sub)
	}
	if len(sub.Actions) != 1 || sub.Actions[0] != "cancel" {
		t.Errorf("running subagent actions = %v, want [cancel]", sub.Actions)
	}

	cron, ok := byType["cron"]
	if !ok {
		t.Fatalf("no cron task in union: %+v", tasks)
	}
	if cron.Status != "active" || cron.Label != "nightly-sync" {
		t.Errorf("cron task = %+v, want status active label nightly-sync", cron)
	}

	cb, ok := byType["callback"]
	if !ok {
		t.Fatalf("no callback task in union: %+v", tasks)
	}
	if cb.ID != "cb-1" || cb.Status != "pending" || cb.Label != "check the deploy" {
		t.Errorf("callback task = %+v, want id cb-1 status pending label 'check the deploy'", cb)
	}
	if len(cb.Actions) != 1 || cb.Actions[0] != "cancel" {
		t.Errorf("pending callback actions = %v, want [cancel]", cb.Actions)
	}
}

// TestTasksEndpoint_SkipsUnconfiguredSources verifies the union degrades
// gracefully: with only a cron client configured, only cron entries appear.
func TestTasksEndpoint_SkipsUnconfiguredSources(t *testing.T) {
	t.Parallel()

	cronClient := newMockCronClient()
	if _, err := cronClient.CreateJob(context.Background(), tools.CronCreateJobRequest{
		Name:     "only-cron",
		Schedule: "* * * * *",
	}); err != nil {
		t.Fatalf("seed cron job: %v", err)
	}

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), CronClient: cronClient})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, tasks := listTasks(t, ts, "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/tasks: status %d, want 200", code)
	}
	if len(tasks) != 1 || tasks[0].Type != "cron" {
		t.Fatalf("got %+v, want exactly one cron task", tasks)
	}
}

// TestTasksEndpoint_SubagentListError verifies a failing source surfaces an
// error rather than silently dropping that source from the union.
func TestTasksEndpoint_SubagentListError(t *testing.T) {
	t.Parallel()

	subMgr := &mockSubagentManager{
		listFn: func(context.Context) ([]subagents.Subagent, error) {
			return nil, context.DeadlineExceeded
		},
	}
	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), SubagentManager: subMgr})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, _ := listTasks(t, ts, "")
	if code != http.StatusInternalServerError {
		t.Fatalf("GET /v1/tasks with failing subagent source: status %d, want 500", code)
	}
}

// TestTasksEndpoint_TenantFiltering verifies that with auth enabled, each
// caller only sees their own tenant's subagents, cron jobs, and callbacks —
// matching the scoping of /v1/subagents and /v1/cron/jobs.
func TestTasksEndpoint_TenantFiltering(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	tokenA, keyA := newSubagentTenantAPIKey(t, "tenant-alpha", "key A")
	tokenB, keyB := newSubagentTenantAPIKey(t, "tenant-bravo", "key B")
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	subMgr := &mockSubagentManager{
		listFn: func(context.Context) ([]subagents.Subagent, error) {
			return []subagents.Subagent{
				{ID: "sub-a", TenantID: "tenant-alpha", RunID: "run-a", Status: harness.RunStatusRunning, CreatedAt: time.Now().UTC()},
				{ID: "sub-b", TenantID: "tenant-bravo", RunID: "run-b", Status: harness.RunStatusRunning, CreatedAt: time.Now().UTC()},
			}, nil
		},
	}
	cronClient := newMockCronClient()
	if _, err := cronClient.CreateJob(context.Background(), tools.CronCreateJobRequest{Name: "job-a", Schedule: "* * * * *", TenantID: "tenant-alpha"}); err != nil {
		t.Fatalf("seed cron A: %v", err)
	}
	if _, err := cronClient.CreateJob(context.Background(), tools.CronCreateJobRequest{Name: "job-b", Schedule: "* * * * *", TenantID: "tenant-bravo"}); err != nil {
		t.Fatalf("seed cron B: %v", err)
	}
	callbacks := mockCallbackLister{callbacks: []tools.CallbackInfo{
		{ID: "cb-a", TenantID: "tenant-alpha", Prompt: "a", State: tools.CallbackStatePending, CreatedAt: time.Now().UTC()},
		{ID: "cb-b", TenantID: "tenant-bravo", Prompt: "b", State: tools.CallbackStatePending, CreatedAt: time.Now().UTC()},
	}}

	handler := NewWithOptions(ServerOptions{
		Runner:          testRunnerForAgents(t),
		Store:           ms,
		SubagentManager: subMgr,
		CronClient:      cronClient,
		CallbackLister:  callbacks,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, tasksA := listTasks(t, ts, tokenA)
	if code != http.StatusOK {
		t.Fatalf("list as A: status %d, want 200", code)
	}
	if len(tasksA) != 3 {
		t.Fatalf("tenant A sees %d tasks, want 3: %+v", len(tasksA), tasksA)
	}
	for _, task := range tasksA {
		switch task.ID {
		case "sub-a", "cb-a":
		case "job-1": // first mock cron job (tenant-alpha)
		default:
			t.Errorf("tenant A sees unexpected task %s (%s)", task.ID, task.Type)
		}
	}

	code, tasksB := listTasks(t, ts, tokenB)
	if code != http.StatusOK {
		t.Fatalf("list as B: status %d, want 200", code)
	}
	if len(tasksB) != 3 {
		t.Fatalf("tenant B sees %d tasks, want 3: %+v", len(tasksB), tasksB)
	}
	for _, task := range tasksB {
		switch task.ID {
		case "sub-b", "cb-b", "job-2":
		default:
			t.Errorf("tenant B sees unexpected task %s (%s)", task.ID, task.Type)
		}
	}
}

// TestTasksEndpoint_AuthEnforced verifies /v1/tasks requires authentication
// and the runs:read scope when auth is enabled.
func TestTasksEndpoint_AuthEnforced(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	rawNoScope, keyNoScope, err := store.GenerateAPIKey("tenant-alpha", "no-scope", []string{})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	keyNoScope = minCostRehash(t, rawNoScope, keyNoScope)
	if err := ms.CreateAPIKey(context.Background(), keyNoScope); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	tokenRead, keyRead := newSubagentTenantAPIKey(t, "tenant-alpha", "reader")
	if err := ms.CreateAPIKey(context.Background(), keyRead); err != nil {
		t.Fatalf("CreateAPIKey reader: %v", err)
	}

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), Store: ms})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Unauthenticated: 401.
	if code, _ := listTasks(t, ts, ""); code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/tasks without token: status %d, want 401", code)
	}
	// Authenticated but missing runs:read: 403.
	if code, _ := listTasks(t, ts, rawNoScope); code != http.StatusForbidden {
		t.Fatalf("GET /v1/tasks without runs:read: status %d, want 403", code)
	}
	// runs:read token: 200.
	if code, _ := listTasks(t, ts, tokenRead); code != http.StatusOK {
		t.Fatalf("GET /v1/tasks with runs:read: status %d, want 200", code)
	}
}

// TestTasksEndpoint_MethodNotAllowed verifies non-GET methods are rejected.
func TestTasksEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t)})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, _ := doSubagentRequest(t, ts, http.MethodPost, "", "/v1/tasks", []byte(`{}`))
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/tasks: status %d, want 405", code)
	}
}

// --- bash_job union + kill endpoint (epic #814 slice 2) ---

// newTrackedJobManager returns a JobManager registered on a tracker, plus the
// tracker. The manager is shut down with test cleanup.
func newTrackedJobManager(t *testing.T) (*harness.JobTracker, *tools.JobManager) {
	t.Helper()
	tracker := harness.NewJobTracker()
	mgr := tools.NewJobManager(t.TempDir(), nil)
	tracker.Register(mgr)
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	return tracker, mgr
}

func startBashJob(t *testing.T, mgr *tools.JobManager, ctx context.Context, command string) string {
	t.Helper()
	result, err := mgr.RunBackgroundWithContext(ctx, command, 60, "")
	if err != nil {
		t.Fatalf("RunBackgroundWithContext(%q): %v", command, err)
	}
	shellID, _ := result["shell_id"].(string)
	if shellID == "" {
		t.Fatalf("RunBackgroundWithContext(%q) returned no shell_id: %v", command, result)
	}
	return shellID
}

func tenantJobCtx(tenantID string) context.Context {
	return context.WithValue(context.Background(), tools.ContextKeyRunMetadata, tools.RunMetadata{
		RunID:    "run-1",
		TenantID: tenantID,
	})
}

// TestTasksEndpoint_UnionsBashJobs verifies background bash jobs appear in the
// union as bash_job entries with running/exited statuses and a cancel action
// only while running.
func TestTasksEndpoint_UnionsBashJobs(t *testing.T) {
	t.Parallel()

	tracker, mgr := newTrackedJobManager(t)
	runningID := startBashJob(t, mgr, context.Background(), "sleep 30")
	exitedID := startBashJob(t, mgr, context.Background(), "true")
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := mgr.Output(exitedID, false)
		if err != nil {
			t.Fatalf("Output(%s): %v", exitedID, err)
		}
		if running, _ := out["running"].(bool); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exited job did not finish in time")
		}
		time.Sleep(20 * time.Millisecond)
	}

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, tasks := listTasks(t, ts, "")
	if code != http.StatusOK {
		t.Fatalf("GET /v1/tasks: status %d, want 200", code)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks, want 2 bash jobs: %+v", len(tasks), tasks)
	}
	byLabel := make(map[string]Task, 2)
	for _, task := range tasks {
		if task.Type != "bash_job" {
			t.Errorf("task %s type = %q, want bash_job", task.ID, task.Type)
		}
		byLabel[task.Label] = task
	}
	running, ok := byLabel["sleep 30"]
	if !ok {
		t.Fatalf("no task labelled 'sleep 30': %+v", tasks)
	}
	if running.Status != "running" {
		t.Errorf("running job status = %q, want running", running.Status)
	}
	if len(running.Actions) != 1 || running.Actions[0] != "cancel" {
		t.Errorf("running job actions = %v, want [cancel]", running.Actions)
	}
	exited, ok := byLabel["true"]
	if !ok {
		t.Fatalf("no task labelled 'true': %+v", tasks)
	}
	if exited.Status != "exited" {
		t.Errorf("finished job status = %q, want exited", exited.Status)
	}
	if len(exited.Actions) != 0 {
		t.Errorf("finished job actions = %v, want []", exited.Actions)
	}
	_ = runningID
}

// TestTasksEndpoint_BashJobTenantFiltering verifies bash jobs are scoped to
// the caller's tenant like the other task sources.
func TestTasksEndpoint_BashJobTenantFiltering(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	tokenA, keyA := newSubagentTenantAPIKey(t, "tenant-alpha", "key A")
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	tracker, mgr := newTrackedJobManager(t)
	startBashJob(t, mgr, tenantJobCtx("tenant-alpha"), "sleep 30")
	startBashJob(t, mgr, tenantJobCtx("tenant-bravo"), "sleep 31")

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), Store: ms, JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, tasks := listTasks(t, ts, tokenA)
	if code != http.StatusOK {
		t.Fatalf("GET /v1/tasks as A: status %d, want 200", code)
	}
	if len(tasks) != 1 || tasks[0].Label != "sleep 30" {
		t.Fatalf("tenant A sees %+v, want exactly the tenant-alpha job", tasks)
	}
}

// TestJobKillEndpoint verifies the full slice-2 acceptance flow at the HTTP
// layer: a running bash job listed in /v1/tasks is killed via
// POST /v1/jobs/{id}/kill, after which job_output reports it terminated.
func TestJobKillEndpoint(t *testing.T) {
	t.Parallel()

	tracker, mgr := newTrackedJobManager(t)
	shellID := startBashJob(t, mgr, context.Background(), "sleep 30")

	var taskID string
	for id := range func() map[string]bool {
		out := map[string]bool{}
		for _, tj := range tracker.List() {
			out[tj.TaskID] = true
		}
		return out
	}() {
		taskID = id
	}
	if taskID == "" {
		t.Fatal("tracker has no jobs")
	}

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, body := doSubagentRequest(t, ts, http.MethodPost, "", "/v1/jobs/"+taskID+"/kill", nil)
	if code != http.StatusOK {
		t.Fatalf("POST /v1/jobs/%s/kill: status %d, body %s; want 200", taskID, code, body)
	}

	// job_output must reflect termination.
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err := mgr.Output(shellID, false)
		if err != nil {
			t.Fatalf("Output(%s): %v", shellID, err)
		}
		if running, _ := out["running"].(bool); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still running after kill endpoint", shellID)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The union now reports the job as exited, no longer running.
	_, tasks := listTasks(t, ts, "")
	if len(tasks) != 1 || tasks[0].Status != "exited" {
		t.Fatalf("after kill, tasks = %+v, want one exited bash_job", tasks)
	}
}

// TestJobKillEndpoint_NotFound verifies unknown task IDs return 404.
func TestJobKillEndpoint_NotFound(t *testing.T) {
	t.Parallel()

	tracker, _ := newTrackedJobManager(t)
	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	for _, id := range []string{"jm999:job_1", "jm1:job_999", "no-separator"} {
		code, _ := doSubagentRequest(t, ts, http.MethodPost, "", "/v1/jobs/"+id+"/kill", nil)
		if code != http.StatusNotFound {
			t.Errorf("POST /v1/jobs/%s/kill: status %d, want 404", id, code)
		}
	}
}

// TestJobKillEndpoint_CrossTenant verifies a caller cannot kill another
// tenant's bash job (404, not 403, matching subagent cancel semantics).
func TestJobKillEndpoint_CrossTenant(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	_, keyA := newSubagentTenantAPIKey(t, "tenant-alpha", "key A")
	tokenB, keyB := newSubagentTenantAPIKey(t, "tenant-bravo", "key B")
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	tracker, mgr := newTrackedJobManager(t)
	shellID := startBashJob(t, mgr, tenantJobCtx("tenant-alpha"), "sleep 30")
	var taskID string
	for _, tj := range tracker.List() {
		taskID = tj.TaskID
	}

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), Store: ms, JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, body := doSubagentRequest(t, ts, http.MethodPost, tokenB, "/v1/jobs/"+taskID+"/kill", nil)
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant kill: status %d, body %s; want 404", code, body)
	}
	out, err := mgr.Output(shellID, false)
	if err != nil {
		t.Fatalf("Output(%s): %v", shellID, err)
	}
	if running, _ := out["running"].(bool); !running {
		t.Fatal("cross-tenant kill terminated the job")
	}
}

// TestJobKillEndpoint_AuthEnforced verifies the kill endpoint requires
// authentication and runs:write when auth is enabled.
func TestJobKillEndpoint_AuthEnforced(t *testing.T) {
	t.Parallel()

	ms := store.NewMemoryStore()
	rawRead, keyRead, err := store.GenerateAPIKey("tenant-alpha", "reader", []string{store.ScopeRunsRead})
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	keyRead = minCostRehash(t, rawRead, keyRead)
	if err := ms.CreateAPIKey(context.Background(), keyRead); err != nil {
		t.Fatalf("CreateAPIKey reader: %v", err)
	}
	tokenWrite, keyWrite := newSubagentTenantAPIKey(t, "tenant-alpha", "writer")
	if err := ms.CreateAPIKey(context.Background(), keyWrite); err != nil {
		t.Fatalf("CreateAPIKey writer: %v", err)
	}

	tracker, mgr := newTrackedJobManager(t)
	startBashJob(t, mgr, tenantJobCtx("tenant-alpha"), "sleep 30")
	var taskID string
	for _, tj := range tracker.List() {
		taskID = tj.TaskID
	}

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), Store: ms, JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	if code, _ := doSubagentRequest(t, ts, http.MethodPost, "", "/v1/jobs/"+taskID+"/kill", nil); code != http.StatusUnauthorized {
		t.Fatalf("kill without token: status %d, want 401", code)
	}
	if code, _ := doSubagentRequest(t, ts, http.MethodPost, rawRead, "/v1/jobs/"+taskID+"/kill", nil); code != http.StatusForbidden {
		t.Fatalf("kill with read-only token: status %d, want 403", code)
	}
	if code, _ := doSubagentRequest(t, ts, http.MethodPost, tokenWrite, "/v1/jobs/"+taskID+"/kill", nil); code != http.StatusOK {
		t.Fatalf("kill with runs:write token: status %d, want 200", code)
	}
}

// TestJobKillEndpoint_MethodNotAllowed verifies non-POST methods are rejected.
func TestJobKillEndpoint_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	tracker, _ := newTrackedJobManager(t)
	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t), JobTracker: tracker})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, _ := doSubagentRequest(t, ts, http.MethodGet, "", "/v1/jobs/jm1:job_1/kill", nil)
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /v1/jobs/jm1:job_1/kill: status %d, want 405", code)
	}
}

// TestJobKillEndpoint_NotConfigured verifies 501 when no tracker is wired.
func TestJobKillEndpoint_NotConfigured(t *testing.T) {
	t.Parallel()

	handler := NewWithOptions(ServerOptions{Runner: testRunnerForAgents(t)})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	code, _ := doSubagentRequest(t, ts, http.MethodPost, "", "/v1/jobs/jm1:job_1/kill", nil)
	if code != http.StatusNotImplemented {
		t.Fatalf("kill with unconfigured tracker: status %d, want 501", code)
	}
}
