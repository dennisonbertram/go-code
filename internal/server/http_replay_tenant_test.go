package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
	"go-agent-harness/internal/store"
)

// validRolloutJSONL is a minimal, well-formed rollout that LoadFile + Replay accept.
const validRolloutJSONL = `{"ts":"2026-03-12T10:00:00Z","seq":1,"type":"run.started","data":{"step":0,"prompt":"hello"}}
{"ts":"2026-03-12T10:00:01Z","seq":2,"type":"llm.turn.completed","data":{"step":1,"content":"hi"}}
{"ts":"2026-03-12T10:00:02Z","seq":3,"type":"run.completed","data":{"step":2}}`

// replayTenantFixture wires a single auth-enabled server with two tenants and a
// configured rollout dir, so that replay path/tenant scoping can be exercised.
type replayTenantFixture struct {
	ts         *httptest.Server
	rolloutDir string
	tokenA     string
	tenantA    string
	tokenB     string
	tenantB    string
}

func newReplayTenantFixture(t *testing.T) *replayTenantFixture {
	t.Helper()

	ms := store.NewMemoryStore()

	tenantA := "tenant-alpha"
	tokenA, keyA := generateFastAPIKey(t, tenantA, "key A", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyA); err != nil {
		t.Fatalf("CreateAPIKey A: %v", err)
	}

	tenantB := "tenant-bravo"
	tokenB, keyB := generateFastAPIKey(t, tenantB, "key B", []string{
		store.ScopeRunsRead,
		store.ScopeRunsWrite,
	})
	if err := ms.CreateAPIKey(context.Background(), keyB); err != nil {
		t.Fatalf("CreateAPIKey B: %v", err)
	}

	rolloutDir := t.TempDir()

	// The runner records rollouts to the SAME directory the server gates on, so
	// that a real recorded rollout (<RolloutDir>/<date>/<run>.jsonl) can be
	// replayed by its owning tenant. Multiple turns are scripted so the run can
	// drive several steps before completing.
	prov := fakeprovider.New([]fakeprovider.Turn{
		{Content: "first output"},
		{Content: "forked output"},
	})
	runner := harness.NewRunner(
		prov,
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:        "test-model",
			DefaultSystemPrompt: "test",
			MaxSteps:            2,
			Store:               ms,
			RolloutDir:          rolloutDir,
		},
	)

	h := server.NewWithOptions(server.ServerOptions{
		Store:      ms,
		Runner:     runner,
		RolloutDir: rolloutDir,
		// AuthDisabled NOT set -- auth is enabled.
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		runner.Shutdown(context.Background())
	})

	return &replayTenantFixture{
		ts:         ts,
		rolloutDir: rolloutDir,
		tokenA:     tokenA,
		tenantA:    tenantA,
		tokenB:     tokenB,
		tenantB:    tenantB,
	}
}

// writeTenantRollout writes raw rollout content to an arbitrary on-disk path
// (creating parent dirs). Used to plant fixtures for path-shape tests that do
// not need a real recorded run.
func (f *replayTenantFixture) writeTenantRollout(t *testing.T, tenant, name, content string) string {
	t.Helper()
	dir := filepath.Join(f.rolloutDir, "tenants", tenant)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}

// recordRealRollout drives a REAL run as the given tenant through the HTTP API,
// waits for it to complete, and returns the on-disk path of the recorded
// rollout file the recorder produced (<RolloutDir>/<date>/<run>.jsonl).
func (f *replayTenantFixture) recordRealRollout(t *testing.T, token, prompt string) (runID, path string) {
	t.Helper()

	b, _ := json.Marshal(map[string]any{"prompt": prompt})
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/v1/runs", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	var created struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	_ = resp.Body.Close()
	if created.RunID == "" {
		t.Fatalf("POST /v1/runs returned no run_id (status %d)", resp.StatusCode)
	}

	f.waitForRunDone(t, token, created.RunID)

	// Locate the recorded rollout file: <RolloutDir>/<date>/<run_id>.jsonl.
	// The date partition is determined at record time, so search rather than
	// assume a particular date.
	var found string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		_ = filepath.Walk(f.rolloutDir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if filepath.Base(p) == created.RunID+".jsonl" {
				found = p
			}
			return nil
		})
		if found != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if found == "" {
		t.Fatalf("recorded rollout file for run %s not found under %s", created.RunID, f.rolloutDir)
	}
	return created.RunID, found
}

// waitForRunDone polls GET /v1/runs/{id} (with auth) until terminal.
func (f *replayTenantFixture) waitForRunDone(t *testing.T, token, runID string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/v1/runs/"+runID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /v1/runs/%s: %v", runID, err)
		}
		var state struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&state)
		_ = resp.Body.Close()
		if state.Status == "completed" || state.Status == "failed" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for run %s to finish (last status %q)", runID, state.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// replay POSTs /v1/runs/replay as the given tenant token and returns status + body.
func (f *replayTenantFixture) replay(t *testing.T, token string, body map[string]any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/v1/runs/replay", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/runs/replay: %v", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(rb)
}

// TestReplayTenant_SameTenant_RealRolloutSucceeds (T-PFIX-4 regression): with auth
// ON and a RolloutDir configured, a tenant that RECORDED a real rollout (written
// by the recorder to <RolloutDir>/<date>/<run>.jsonl) must be able to replay THAT
// path. Confining to <RolloutDir>/tenants/<tenant>/ (a directory the recorder
// never writes to) would wrongly reject this legitimate same-tenant replay.
func TestReplayTenant_SameTenant_RealRolloutSucceeds(t *testing.T) {
	t.Parallel()

	f := newReplayTenantFixture(t)

	_, pathA := f.recordRealRollout(t, f.tokenA, "real prompt A")

	// Simulate: tenant A replays its own real recorded rollout -> 200.
	if code, body := f.replay(t, f.tokenA, map[string]any{
		"rollout_path": pathA,
		"mode":         "simulate",
	}); code != http.StatusOK {
		t.Fatalf("owner simulate of real rollout: got %d, want 200; body %s", code, body)
	}

	// Fork: tenant A forks its own real recorded rollout -> 202.
	if code, body := f.replay(t, f.tokenA, map[string]any{
		"rollout_path": pathA,
		"mode":         "fork",
		"fork_step":    1,
	}); code != http.StatusAccepted {
		t.Fatalf("owner fork of real rollout: got %d, want 202; body %s", code, body)
	}
}

// TestReplayTenant_CrossTenantDenied (T-PFIX-4): a REAL rollout recorded by tenant A
// must NOT be replayable by tenant B.
func TestReplayTenant_CrossTenantDenied(t *testing.T) {
	t.Parallel()

	f := newReplayTenantFixture(t)

	// Tenant A records a real rollout.
	_, pathA := f.recordRealRollout(t, f.tokenA, "secret prompt A")

	// Sanity: tenant A can replay its own real rollout (simulate).
	if code, body := f.replay(t, f.tokenA, map[string]any{
		"rollout_path": pathA,
		"mode":         "simulate",
	}); code != http.StatusOK {
		t.Fatalf("owner simulate: got %d, want 200; body %s", code, body)
	}

	// Tenant B points at tenant A's real rollout path -> must be rejected (404/400),
	// not replayed.
	code, body := f.replay(t, f.tokenB, map[string]any{
		"rollout_path": pathA,
		"mode":         "simulate",
	})
	if code != http.StatusNotFound && code != http.StatusBadRequest {
		t.Errorf("cross-tenant simulate: got %d, want 404 or 400; body %s", code, body)
	}
	// It must not have actually replayed tenant A's rollout.
	if code == http.StatusOK {
		t.Errorf("cross-tenant simulate leaked tenant A's rollout: body %s", body)
	}

	// Same for fork mode: tenant B must not be able to fork tenant A's rollout.
	code, body = f.replay(t, f.tokenB, map[string]any{
		"rollout_path": pathA,
		"mode":         "fork",
		"fork_step":    1,
	})
	if code != http.StatusNotFound && code != http.StatusBadRequest {
		t.Errorf("cross-tenant fork: got %d, want 404 or 400; body %s", code, body)
	}
}

// TestReplayTenant_PathTraversalDenied (T-PFIX-4): a rollout_path that escapes the
// configured rollout dir (via ../ or an absolute path to a sensitive file) must be
// rejected without reading the file.
func TestReplayTenant_PathTraversalDenied(t *testing.T) {
	t.Parallel()

	f := newReplayTenantFixture(t)

	// A sensitive file OUTSIDE the rollout dir, with valid rollout content so that
	// if the guard were missing, the replay would actually succeed (200) -- proving
	// the file was read. With the guard, it must be rejected.
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.jsonl")
	if err := os.WriteFile(secretPath, []byte(validRolloutJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile secret: %v", err)
	}

	// 1) Absolute path to a file outside the configured rollout dir.
	if code, body := f.replay(t, f.tokenA, map[string]any{
		"rollout_path": secretPath,
		"mode":         "simulate",
	}); code == http.StatusOK {
		t.Errorf("absolute escape was read/replayed (got 200): body %s", body)
	} else if code != http.StatusNotFound && code != http.StatusBadRequest {
		t.Errorf("absolute escape: got %d, want 404 or 400; body %s", code, body)
	}

	// 2) Relative ../ traversal from within the rollout dir that escapes it
	//    entirely and lands on the sensitive file.
	traversal := filepath.Join(f.rolloutDir, "..",
		filepath.Base(outsideDir), "secret.jsonl")
	if code, body := f.replay(t, f.tokenA, map[string]any{
		"rollout_path": traversal,
		"mode":         "simulate",
	}); code == http.StatusOK {
		t.Errorf("relative traversal escape was read/replayed (got 200): body %s", body)
	} else if code != http.StatusNotFound && code != http.StatusBadRequest {
		t.Errorf("relative traversal: got %d, want 404 or 400; body %s", code, body)
	}

	// 3) A common sensitive absolute path (e.g. /etc/hostname) must never be read.
	if _, err := os.Stat("/etc/hostname"); err == nil {
		if code, _ := f.replay(t, f.tokenA, map[string]any{
			"rollout_path": "/etc/hostname",
			"mode":         "simulate",
		}); code == http.StatusOK {
			t.Errorf("/etc/hostname was read/replayed (got 200)")
		}
	}
}

// TestReplayTenant_SymlinkEscapeDenied (T-PFIX-4): a symlink that lives INSIDE the
// rollout dir but points to a target OUTSIDE it must not be a usable bypass.
// Textual path containment passes (the link path is in-bounds) but the resolved
// target escapes, so EvalSymlinks-based containment must reject it.
func TestReplayTenant_SymlinkEscapeDenied(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}

	f := newReplayTenantFixture(t)

	// Plant a valid rollout OUTSIDE the rollout dir.
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.jsonl")
	if err := os.WriteFile(secretPath, []byte(validRolloutJSONL), 0o644); err != nil {
		t.Fatalf("WriteFile secret: %v", err)
	}

	// Create an IN-BOUNDS symlink under the rollout dir that points to the
	// out-of-bounds secret. The textual path of the link is inside the rollout
	// dir, so only EvalSymlinks reveals the escape.
	linkDir := filepath.Join(f.rolloutDir, "tenants", f.tenantA)
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", linkDir, err)
	}
	linkPath := filepath.Join(linkDir, "escape.jsonl")
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if code, body := f.replay(t, f.tokenA, map[string]any{
		"rollout_path": linkPath,
		"mode":         "simulate",
	}); code == http.StatusOK {
		t.Errorf("in-bounds symlink to out-of-bounds target was read/replayed (got 200): body %s", body)
	} else if code != http.StatusNotFound && code != http.StatusBadRequest {
		t.Errorf("symlink escape: got %d, want 404 or 400; body %s", code, body)
	}
}
