package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/store"
)

// This file contains regression tests for the server hardening batch
// (S1-S5, C1-C4). Each test covers a DIFFERENT angle than the item's
// original attack test — an edge case, a boundary condition, or an
// interaction with a mode the fix must NOT affect — so that a future revert
// or refactor of the fix is caught even if it technically still passes the
// original attack test.

// TestRegression_C1_InRangeLastEventID_StillReplaysOnlyUnseenEvents guards
// against a regression where the C1 bounds-check fix (fall back to full
// replay for OUT-of-range sequences) is accidentally broadened to apply to
// every Last-Event-ID, which would silently break the legitimate SSE
// reconnect case (the whole point of Last-Event-ID: skip events the client
// has already seen).
func TestRegression_C1_InRangeLastEventID_StillReplaysOnlyUnseenEvents(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&staticProvider{result: harness.CompletionResult{Content: "done"}}, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     2,
	})
	handler := New(runner)

	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewBufferString(`{"prompt":"hello"}`))
	handler.ServeHTTP(createRec, createReq)
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	fullRec := httptest.NewRecorder()
	fullReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.RunID+"/events", nil)
	handler.ServeHTTP(fullRec, fullReq)
	fullCount := countSSEEvents(fullRec.Body.String())
	if fullCount < 2 {
		t.Fatalf("expected at least 2 events in full replay, got %d", fullCount)
	}

	// Reconnect claiming to have seen only the FIRST event (seq=0): a valid,
	// in-range Last-Event-ID. Must get fewer events than the full replay —
	// NOT the full replay again (which is only for out-of-range/unparseable
	// values).
	partialRec := httptest.NewRecorder()
	partialReq := httptest.NewRequest(http.MethodGet, "/v1/runs/"+created.RunID+"/events", nil)
	partialReq.Header.Set("Last-Event-ID", created.RunID+":0")
	handler.ServeHTTP(partialRec, partialReq)
	partialCount := countSSEEvents(partialRec.Body.String())

	if partialCount >= fullCount {
		t.Fatalf("in-range Last-Event-ID must skip already-seen events: got %d events, full replay has %d", partialCount, fullCount)
	}
	if partialCount == 0 {
		t.Fatalf("in-range Last-Event-ID must still replay the remaining unseen events, got 0")
	}
}

func countSSEEvents(body string) int {
	count := 0
	for i := 0; i+len("event: ") <= len(body); i++ {
		if body[i:i+len("event: ")] == "event: " {
			count++
		}
	}
	return count
}

// TestRegression_C3_SingleTurnFork_NoConversationStoreRequired guards against
// a regression where the C3 fix (persist reconstructed history via a
// ConversationStore) is accidentally broadened to require a ConversationStore
// for EVERY fork, even the common case where there is no prior history to
// restore (the fork point IS the first turn). That would make forking
// unusable in any deployment without conversation persistence configured,
// which is a real, supported configuration today.
func TestRegression_C3_SingleTurnFork_NoConversationStoreRequired(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Single-turn rollout: only one user prompt, nothing before it.
	content := `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"only question"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"llm.turn.completed","data":{"step":1,"content":"only answer"}}
{"ts":"2026-03-12T10:00:02Z","seq":3,"type":"run.completed","data":{"step":2}}`
	rolloutPath := writeTestRollout(t, dir, "single-turn.jsonl", content)

	// Deliberately NO ConversationStore configured.
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "forked output"}},
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:        "gpt-4.1-mini",
			DefaultSystemPrompt: "test",
			MaxSteps:            2,
			RolloutDir:          dir,
		},
	)
	handler := NewWithOptions(ServerOptions{
		Runner:       runner,
		AuthDisabled: true,
		RolloutDir:   dir,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"rollout_path": rolloutPath,
		"mode":         "fork",
		"fork_step":    1,
	})
	resp, err := http.Post(ts.URL+"/v1/runs/replay", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		t.Fatalf("single-turn fork with no ConversationStore must still succeed (202), got %d: %v", resp.StatusCode, errBody)
	}
}

// TestRegression_S3_ScriptWorkflowTenantGate_DisabledWhenAuthDisabled guards
// against an overly-aggressive future change to the S3 tenant gate that
// starts blocking script-workflow run access even when auth is disabled
// (the common local/dev/no-persistence configuration), where there is no
// authenticated tenant to compare against.
func TestRegression_S3_ScriptWorkflowTenantGate_DisabledWhenAuthDisabled(t *testing.T) {
	t.Parallel()

	mgr := newMockScriptWorkflowMgr()
	h := NewWithOptions(ServerOptions{
		ScriptWorkflows: mgr,
		AuthDisabled:    true,
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	startRes, err := http.Post(ts.URL+"/v1/script-workflows/test-workflow/runs", "application/json", nil)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer startRes.Body.Close()
	if startRes.StatusCode != http.StatusAccepted {
		t.Fatalf("start run: expected 202, got %d", startRes.StatusCode)
	}
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A DIFFERENT, un-authenticated request (no Authorization header at all,
	// matching AuthDisabled's contract) must still be able to read the run.
	getRes, err := http.Get(ts.URL + "/v1/script-workflow-runs/" + created.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	defer getRes.Body.Close()
	if getRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 when auth is disabled (no tenant gate should apply), got %d", getRes.StatusCode)
	}
}

// TestRegression_S5_DedupCacheIsPerServerInstance guards against a future
// "simplification" that turns DeliveryDedupCache into a package-level
// singleton, which would let one Server's replayed-delivery rejection leak
// into a completely different Server instance (e.g. two test servers, or
// two harnessd processes sharing a binary in some embedding scenario).
func TestRegression_S5_DedupCacheIsPerServerInstance(t *testing.T) {
	t.Parallel()

	const secret = "shared-secret-two-servers"
	reg := makeGitHubRegistry(secret)

	provider1 := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	ms1 := store.NewMemoryStore()
	runner1 := harness.NewRunner(provider1, harness.NewRegistry(), harness.RunnerConfig{DefaultModel: "test-model", MaxSteps: 4, Store: ms1})
	h1 := NewWithOptions(ServerOptions{Runner: runner1, Store: ms1, AuthDisabled: true, Validators: reg})
	ts1 := httptest.NewServer(h1)
	defer ts1.Close()

	provider2 := &staticProvider{result: harness.CompletionResult{Content: "done"}}
	ms2 := store.NewMemoryStore()
	runner2 := harness.NewRunner(provider2, harness.NewRegistry(), harness.RunnerConfig{DefaultModel: "test-model", MaxSteps: 4, Store: ms2})
	h2 := NewWithOptions(ServerOptions{Runner: runner2, Store: ms2, AuthDisabled: true, Validators: reg})
	ts2 := httptest.NewServer(h2)
	defer ts2.Close()

	body, sig := buildTriggerRequest(t, "github", secret, "start", "same delivery, two servers", "PR#cross-server", map[string]string{
		"source_id": "delivery-cross-server-001",
	})

	res1 := sendTrigger(t, ts1, body, sig)
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusAccepted {
		t.Fatalf("server 1: expected 202, got %d", res1.StatusCode)
	}

	// The SAME delivery ID on a DIFFERENT server instance must be treated as
	// fresh — dedup state must not be shared across servers.
	res2 := sendTrigger(t, ts2, body, sig)
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusAccepted {
		t.Fatalf("server 2: expected 202 (independent dedup cache), got %d", res2.StatusCode)
	}
}
