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
