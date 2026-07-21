package server_test

// http_config_test.go — POST /v1/config/reload endpoint (epic #815 slice 3).
//
// The endpoint invokes a wired ConfigReloadFunc and reports the reload
// outcome: hot-swappable fields that were applied for subsequent runs and
// restart-only fields that were changed but require a daemon restart.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/server"
)

// configReloadRecordingProvider records the model of every completion request.
type configReloadRecordingProvider struct {
	mu     sync.Mutex
	models []string
}

func (p *configReloadRecordingProvider) Complete(_ context.Context, req harness.CompletionRequest) (harness.CompletionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = append(p.models, req.Model)
	return harness.CompletionResult{Content: "done"}, nil
}

// reloadTestRig wires a runner plus a ConfigReloadFunc that re-runs
// config.Load against a temp config file and applies the result — the same
// mechanism cmd/harnessd uses in production.
type reloadTestRig struct {
	provider *configReloadRecordingProvider
	runner   *harness.Runner
	handler  http.Handler
	cfgPath  string

	mu      sync.Mutex
	current config.Config
}

func newReloadTestRig(t *testing.T, initialTOML string) *reloadTestRig {
	t.Helper()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(initialTOML), 0600); err != nil {
		t.Fatal(err)
	}

	loadOpts := config.LoadOptions{
		UserConfigPath: cfgPath,
		Getenv:         func(string) string { return "" },
	}
	initial, err := config.Load(loadOpts)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	provider := &configReloadRecordingProvider{}
	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: initial.Model,
		MaxSteps:     initial.MaxSteps,
	})

	rig := &reloadTestRig{provider: provider, runner: runner, cfgPath: cfgPath, current: initial}

	reloadFn := func(_ context.Context) (config.ReloadReport, error) {
		rig.mu.Lock()
		defer rig.mu.Unlock()
		next, err := config.Load(loadOpts)
		if err != nil {
			return config.ReloadReport{}, err
		}
		report := config.ReloadDiff(rig.current, next)
		runner.ApplyConfig(harness.RunnerConfig{DefaultModel: next.Model, MaxSteps: next.MaxSteps})
		rig.current = next
		return report, nil
	}

	rig.handler = server.NewWithOptions(server.ServerOptions{
		Runner:       runner,
		ConfigReload: reloadFn,
	})
	return rig
}

func (r *reloadTestRig) writeConfig(t *testing.T, contents string) {
	t.Helper()
	if err := os.WriteFile(r.cfgPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func (r *reloadTestRig) postReload(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/config/reload", nil)
	w := httptest.NewRecorder()
	r.handler.ServeHTTP(w, req)
	return w
}

func (r *reloadTestRig) runOnce(t *testing.T) string {
	t.Helper()
	run, err := r.runner.StartRun(harness.RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, ok := r.runner.GetRun(run.ID)
		if ok && (st.Status == harness.RunStatusCompleted || st.Status == harness.RunStatusFailed) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.provider.mu.Lock()
	defer r.provider.mu.Unlock()
	if len(r.provider.models) == 0 {
		t.Fatal("provider saw no completion requests")
	}
	return r.provider.models[len(r.provider.models)-1]
}

// TestConfigReload_ModelChangeTakesEffect verifies the happy path: a model
// edit in the config file is reported as applied and the next run uses it.
func TestConfigReload_ModelChangeTakesEffect(t *testing.T) {
	t.Parallel()

	rig := newReloadTestRig(t, `model = "model-a"`+"\n")
	if got := rig.runOnce(t); got != "model-a" {
		t.Fatalf("pre-reload run model: got %q, want model-a", got)
	}

	rig.writeConfig(t, `model = "model-b"`+"\n")
	w := rig.postReload(t)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /v1/config/reload: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var body struct {
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("200 body is not valid JSON: %v", err)
	}
	if len(body.Applied) != 1 || body.Applied[0] != "model" {
		t.Errorf("applied: got %v, want [model]", body.Applied)
	}
	if len(body.RestartRequired) != 0 {
		t.Errorf("restart_required: got %v, want empty", body.RestartRequired)
	}

	if got := rig.runOnce(t); got != "model-b" {
		t.Errorf("post-reload run model: got %q, want model-b", got)
	}
}

// TestConfigReload_InvalidConfigReturns400 verifies that a broken config file
// is rejected with the parse error surfaced, and the last-known-good config
// stays active for subsequent runs.
func TestConfigReload_InvalidConfigReturns400(t *testing.T) {
	t.Parallel()

	rig := newReloadTestRig(t, `model = "model-a"`+"\n")
	rig.writeConfig(t, "model = \n this is not toml = [\n")

	w := rig.postReload(t)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST with invalid TOML: got %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "error") {
		t.Errorf("400 body must surface the error, got: %s", w.Body.String())
	}

	if got := rig.runOnce(t); got != "model-a" {
		t.Errorf("after rejected reload, run model: got %q, want model-a (last-known-good)", got)
	}
}

// TestConfigReload_RestartOnlyReported verifies that an addr change is
// reported as requiring a restart and is never silently applied.
func TestConfigReload_RestartOnlyReported(t *testing.T) {
	t.Parallel()

	rig := newReloadTestRig(t, "model = \"model-a\"\naddr = \":8080\"\n")
	rig.writeConfig(t, "model = \"model-a\"\naddr = \":9999\"\n")

	w := rig.postReload(t)
	if w.Code != http.StatusOK {
		t.Fatalf("POST: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var body struct {
		Applied         []string `json:"applied"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("200 body is not valid JSON: %v", err)
	}
	if len(body.RestartRequired) != 1 || body.RestartRequired[0] != "addr" {
		t.Errorf("restart_required: got %v, want [addr]", body.RestartRequired)
	}
	if len(body.Applied) != 0 {
		t.Errorf("applied: got %v, want empty for a restart-only change", body.Applied)
	}
}

// TestConfigReload_NotWiredReturns501 verifies the optional-feature
// convention: without a ConfigReload callback the endpoint returns 501.
func TestConfigReload_NotWiredReturns501(t *testing.T) {
	t.Parallel()

	runner := harness.NewRunner(&configReloadRecordingProvider{}, harness.NewRegistry(), harness.RunnerConfig{DefaultModel: "m"})
	h := server.NewWithOptions(server.ServerOptions{Runner: runner})

	req := httptest.NewRequest(http.MethodPost, "/v1/config/reload", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("unwired POST: got %d, want 501 (body: %s)", w.Code, w.Body.String())
	}
}

// TestConfigReload_MethodNotAllowed verifies non-POST methods are rejected.
func TestConfigReload_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	rig := newReloadTestRig(t, `model = "model-a"`+"\n")
	req := httptest.NewRequest(http.MethodGet, "/v1/config/reload", nil)
	w := httptest.NewRecorder()
	rig.handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: got %d, want 405", w.Code)
	}
}
