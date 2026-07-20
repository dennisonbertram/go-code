// Package s3backup provides JSONL event backup streaming to S3 on run completion.
package s3backup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/store"
	"go-agent-harness/internal/store/s3backup"
)

// --- helpers ---

func makeRun(runID, convID string) *store.Run {
	return &store.Run{
		ID:             runID,
		ConversationID: convID,
		TenantID:       "tenant-1",
		AgentID:        "agent-1",
		Model:          "gpt-4",
		Status:         store.RunStatusCompleted,
		Output:         "done",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}

func populateStore(t *testing.T, st store.Store, runID, convID string) {
	t.Helper()
	ctx := context.Background()
	run := makeRun(runID, convID)
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	msgs := []*store.Message{
		{Seq: 0, RunID: runID, Role: "user", Content: "hello"},
		{Seq: 1, RunID: runID, Role: "assistant", Content: "world"},
	}
	for _, m := range msgs {
		if err := st.AppendMessage(ctx, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	events := []*store.Event{
		{Seq: 0, RunID: runID, EventID: runID + ":0", EventType: "run.started", Payload: `{"status":"running"}`, Timestamp: time.Now().UTC()},
		{Seq: 1, RunID: runID, EventID: runID + ":1", EventType: "run.completed", Payload: `{"output":"done"}`, Timestamp: time.Now().UTC()},
	}
	for _, e := range events {
		if err := st.AppendEvent(ctx, e); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
}

// --- ConfigFromEnv ---

func TestConfigFromEnv_AllPresent(t *testing.T) {
	getenv := func(k string) string {
		m := map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIA123",
			"AWS_SECRET_ACCESS_KEY": "secret",
			"AWS_REGION":            "us-east-1",
			"S3_BUCKET":             "my-bucket",
			"S3_KEY_PREFIX":         "harness/runs",
		}
		return m[k]
	}
	cfg, ok := s3backup.ConfigFromEnv(getenv)
	if !ok {
		t.Fatal("expected ok=true when all env vars are set")
	}
	if cfg.Bucket != "my-bucket" {
		t.Errorf("Bucket: got %q, want my-bucket", cfg.Bucket)
	}
	if cfg.KeyPrefix != "harness/runs" {
		t.Errorf("KeyPrefix: got %q, want harness/runs", cfg.KeyPrefix)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("Region: got %q, want us-east-1", cfg.Region)
	}
	if cfg.AccessKeyID != "AKIA123" {
		t.Errorf("AccessKeyID: got %q, want AKIA123", cfg.AccessKeyID)
	}
	if cfg.SecretAccessKey != "secret" {
		t.Errorf("SecretAccessKey: got %q, want secret", cfg.SecretAccessKey)
	}
}

func TestConfigFromEnv_MissingBucket(t *testing.T) {
	getenv := func(k string) string {
		m := map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIA123",
			"AWS_SECRET_ACCESS_KEY": "secret",
			"AWS_REGION":            "us-east-1",
			// S3_BUCKET intentionally missing
		}
		return m[k]
	}
	_, ok := s3backup.ConfigFromEnv(getenv)
	if ok {
		t.Fatal("expected ok=false when S3_BUCKET is missing")
	}
}

func TestConfigFromEnv_MissingCredentials(t *testing.T) {
	getenv := func(k string) string {
		m := map[string]string{
			// credentials intentionally missing
			"S3_BUCKET":  "my-bucket",
			"AWS_REGION": "us-east-1",
		}
		return m[k]
	}
	_, ok := s3backup.ConfigFromEnv(getenv)
	if ok {
		t.Fatal("expected ok=false when credentials are missing")
	}
}

func TestConfigFromEnv_EmptyPrefix(t *testing.T) {
	// S3_KEY_PREFIX is optional — empty string is valid.
	getenv := func(k string) string {
		m := map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIA123",
			"AWS_SECRET_ACCESS_KEY": "secret",
			"AWS_REGION":            "us-east-1",
			"S3_BUCKET":             "my-bucket",
			// S3_KEY_PREFIX not set
		}
		return m[k]
	}
	cfg, ok := s3backup.ConfigFromEnv(getenv)
	if !ok {
		t.Fatal("expected ok=true when S3_KEY_PREFIX is absent")
	}
	if cfg.KeyPrefix != "" {
		t.Errorf("KeyPrefix: got %q, want empty", cfg.KeyPrefix)
	}
}

// --- ObjectKey ---

func TestObjectKey_WithPrefix(t *testing.T) {
	cfg := s3backup.Config{KeyPrefix: "harness/runs"}
	key := cfg.ObjectKey("conv-123", "run-456")
	want := "harness/runs/conv-123/run-456.jsonl"
	if key != want {
		t.Errorf("ObjectKey: got %q, want %q", key, want)
	}
}

func TestObjectKey_NoPrefix(t *testing.T) {
	cfg := s3backup.Config{KeyPrefix: ""}
	key := cfg.ObjectKey("conv-123", "run-456")
	want := "conv-123/run-456.jsonl"
	if key != want {
		t.Errorf("ObjectKey: got %q, want %q", key, want)
	}
}

// --- BuildJSONL ---

func TestBuildJSONL_ContainsRunAndEvents(t *testing.T) {
	st := store.NewMemoryStore()
	populateStore(t, st, "run-1", "conv-1")

	body, err := s3backup.BuildJSONL(context.Background(), st, "run-1")
	if err != nil {
		t.Fatalf("BuildJSONL: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) < 1 {
		t.Fatal("expected at least 1 JSONL line")
	}

	// First line should be the run record.
	var runObj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &runObj); err != nil {
		t.Fatalf("unmarshal line 0: %v", err)
	}
	if runObj["type"] != "run" {
		t.Errorf("line 0 type: got %q, want run", runObj["type"])
	}
	if runObj["run_id"] != "run-1" {
		t.Errorf("line 0 run_id: got %v", runObj["run_id"])
	}

	// Subsequent lines should be events.
	for i := 1; i < len(lines); i++ {
		var evtObj map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &evtObj); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if evtObj["type"] == nil {
			t.Errorf("line %d missing type field", i)
		}
	}
}

func TestBuildJSONL_RunNotFound(t *testing.T) {
	st := store.NewMemoryStore()
	_, err := s3backup.BuildJSONL(context.Background(), st, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

// --- Uploader with fake HTTP server ---

// capturedRequest holds the last S3 PUT request received by the test server.
type capturedRequest struct {
	method      string
	path        string
	body        []byte
	contentType string
}

func makeFakeS3Server(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		captured.body = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, captured
}

func TestUploader_UploadRun_Success(t *testing.T) {
	srv, captured := makeFakeS3Server(t)

	st := store.NewMemoryStore()
	populateStore(t, st, "run-abc", "conv-xyz")

	cfg := s3backup.Config{
		Bucket:          "test-bucket",
		KeyPrefix:       "prefix",
		Region:          "us-east-1",
		AccessKeyID:     "AKIATEST",
		SecretAccessKey: "testsecret",
		// Override endpoint for testing
		EndpointURL: srv.URL,
	}

	uploader := s3backup.NewUploader(cfg)
	err := uploader.UploadRun(context.Background(), st, "conv-xyz", "run-abc")
	if err != nil {
		t.Fatalf("UploadRun: %v", err)
	}

	if captured.method != http.MethodPut {
		t.Errorf("method: got %q, want PUT", captured.method)
	}

	expectedPath := "/test-bucket/prefix/conv-xyz/run-abc.jsonl"
	if captured.path != expectedPath {
		t.Errorf("path: got %q, want %q", captured.path, expectedPath)
	}

	if len(captured.body) == 0 {
		t.Error("body: expected non-empty JSONL body")
	}

	// Verify body is valid JSONL.
	lines := strings.Split(strings.TrimSpace(string(captured.body)), "\n")
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestUploader_UploadRun_S3Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Access Denied", http.StatusForbidden)
	}))
	defer srv.Close()

	st := store.NewMemoryStore()
	populateStore(t, st, "run-err", "conv-err")

	cfg := s3backup.Config{
		Bucket:          "bucket",
		KeyPrefix:       "",
		Region:          "us-east-1",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		EndpointURL:     srv.URL,
	}
	uploader := s3backup.NewUploader(cfg)
	err := uploader.UploadRun(context.Background(), st, "conv-err", "run-err")
	if err == nil {
		t.Fatal("expected error on S3 403, got nil")
	}
}

func TestUploader_UploadRun_RunNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := store.NewMemoryStore()

	cfg := s3backup.Config{
		Bucket:          "bucket",
		Region:          "us-east-1",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		EndpointURL:     srv.URL,
	}
	uploader := s3backup.NewUploader(cfg)
	err := uploader.UploadRun(context.Background(), st, "conv-x", "run-nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent run")
	}
}

// --- BackupHook (no-op when config missing) ---

func TestBackupHook_NoopWhenNotConfigured(t *testing.T) {
	// When S3 config is absent, UploadRun must be a no-op (returns nil).
	hook := s3backup.NewNoOpUploader()
	st := store.NewMemoryStore()
	err := hook.UploadRun(context.Background(), st, "conv-1", "run-1")
	if err != nil {
		t.Errorf("NoOpUploader.UploadRun: expected nil error, got %v", err)
	}
}

// --- JSONL line ordering ---

func TestBuildJSONL_EventsOrdered(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	run := makeRun("run-order", "conv-order")
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		e := &store.Event{
			Seq:       i,
			RunID:     "run-order",
			EventID:   "run-order:" + strings.Repeat("x", i),
			EventType: "run.step",
			Payload:   `{"step":` + string(rune('0'+i)) + `}`,
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
		if err := st.AppendEvent(ctx, e); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}

	body, err := s3backup.BuildJSONL(context.Background(), st, "run-order")
	if err != nil {
		t.Fatalf("BuildJSONL: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	// Line 0 is the run header; lines 1+ are events in seq order.
	if len(lines) < 6 {
		t.Fatalf("expected 6 lines (1 run + 5 events), got %d", len(lines))
	}

	var prevSeq float64 = -1
	for i := 1; i < len(lines); i++ {
		var obj map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &obj); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		seq, ok := obj["seq"].(float64)
		if !ok {
			t.Fatalf("line %d: missing seq field", i)
		}
		if seq <= prevSeq {
			t.Errorf("events not in order: seq %v after seq %v", seq, prevSeq)
		}
		prevSeq = seq
	}
}

// --- Content-Type header ---

func TestUploader_ContentType(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := store.NewMemoryStore()
	populateStore(t, st, "run-ct", "conv-ct")

	cfg := s3backup.Config{
		Bucket:          "bucket",
		Region:          "us-east-1",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		EndpointURL:     srv.URL,
	}
	uploader := s3backup.NewUploader(cfg)
	if err := uploader.UploadRun(context.Background(), st, "conv-ct", "run-ct"); err != nil {
		t.Fatalf("UploadRun: %v", err)
	}

	if !strings.Contains(gotContentType, "application/x-ndjson") &&
		!strings.Contains(gotContentType, "application/jsonl") &&
		!strings.Contains(gotContentType, "application/octet-stream") {
		t.Errorf("Content-Type: got %q, want jsonl/ndjson/octet-stream", gotContentType)
	}
}

// --- Upload body correctness (run header fields) ---

func TestBuildJSONL_RunHeaderFields(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	run := &store.Run{
		ID:             "run-hdr",
		ConversationID: "conv-hdr",
		TenantID:       "t1",
		AgentID:        "a1",
		Model:          "gpt-4",
		Status:         store.RunStatusCompleted,
		Output:         "done!",
		CreatedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
	}
	if err := st.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	body, err := s3backup.BuildJSONL(ctx, st, "run-hdr")
	if err != nil {
		t.Fatalf("BuildJSONL: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) == 0 {
		t.Fatal("empty JSONL body")
	}

	var hdr map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}

	checks := map[string]any{
		"type":            "run",
		"run_id":          "run-hdr",
		"conversation_id": "conv-hdr",
		"tenant_id":       "t1",
		"agent_id":        "a1",
		"model":           "gpt-4",
		"status":          "completed",
	}
	for field, want := range checks {
		got := hdr[field]
		if got != want {
			t.Errorf("header.%s: got %v, want %v", field, got, want)
		}
	}
}

// --- RunUploader interface satisfied by both Uploader and NoOpUploader ---

func TestUploaderInterface(_ *testing.T) {
	// Compile-time check that both types implement RunUploader.
	var _ s3backup.RunUploader = (*s3backup.Uploader)(nil)
	var _ s3backup.RunUploader = (*s3backup.NoOpUploader)(nil)
}

// --- Concurrent upload safety ---

func TestUploader_ConcurrentUploads(t *testing.T) {
	var mu bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = mu // just to reference it
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := store.NewMemoryStore()
	ctx := context.Background()

	// Create 5 runs.
	for i := 0; i < 5; i++ {
		run := makeRun("run-conc-"+string(rune('a'+i)), "conv-conc")
		if err := st.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun %d: %v", i, err)
		}
	}

	cfg := s3backup.Config{
		Bucket:          "bucket",
		Region:          "us-east-1",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		EndpointURL:     srv.URL,
	}
	uploader := s3backup.NewUploader(cfg)

	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		runID := "run-conc-" + string(rune('a'+i))
		go func(id string) {
			errs <- uploader.UploadRun(ctx, st, "conv-conc", id)
		}(runID)
	}

	for i := 0; i < 5; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent UploadRun: %v", err)
		}
	}
}
