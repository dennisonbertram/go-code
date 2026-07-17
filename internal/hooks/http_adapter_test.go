package hooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/harness"
)

func httpDef(url string) HookDef {
	return HookDef{Name: "http-hook", Event: EventPreToolUse, Kind: KindHTTP, URL: url}
}

// captureServer records the last request body and Content-Type, and responds
// with the given status/body.
type captureServer struct {
	*httptest.Server
	body        atomic.Value // []byte
	contentType atomic.Value // string
	status      int
	respBody    string
}

func newCaptureServer(t *testing.T, status int, respBody string) *captureServer {
	t.Helper()
	cs := &captureServer{status: status, respBody: respBody}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf strings.Builder
		_, _ = io.Copy(&buf, r.Body)
		cs.body.Store([]byte(buf.String()))
		ct, _ := r.Header["Content-Type"]
		if len(ct) > 0 {
			cs.contentType.Store(ct[0])
		}
		w.WriteHeader(cs.status)
		_, _ = w.Write([]byte(cs.respBody))
	}))
	t.Cleanup(cs.Server.Close)
	return cs
}

func TestHTTPHook_PreToolUse(t *testing.T) {
	t.Parallel()

	t.Run("allow returns nil result", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"decision":"allow"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.PreToolUse(context.Background(), preEvent())
		if err != nil || result != nil {
			t.Fatalf("got result=%+v err=%v, want nil/nil", result, err)
		}
	})

	t.Run("deny with reason", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"decision":"deny","reason":"endpoint says no"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.PreToolUse(context.Background(), preEvent())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.Decision != harness.ToolHookDeny || result.Reason != "endpoint says no" {
			t.Fatalf("got %+v, want deny/endpoint says no", result)
		}
	})

	t.Run("modified args pass through", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"decision":"allow","modified_args":{"command":"ls -la"}}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.PreToolUse(context.Background(), preEvent())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || !strings.Contains(string(result.ModifiedArgs), "ls -la") {
			t.Fatalf("ModifiedArgs: got %+v", result)
		}
	})

	t.Run("empty body is allow no-op", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, ``)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.PreToolUse(context.Background(), preEvent())
		if err != nil || result != nil {
			t.Fatalf("got result=%+v err=%v, want nil/nil", result, err)
		}
	})

	t.Run("non-2xx is an error not a decision", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusInternalServerError, `{"decision":"deny"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.PreToolUse(context.Background(), preEvent())
		if err == nil {
			t.Fatalf("expected error for 500, got result=%+v", result)
		}
		if !strings.Contains(err.Error(), "500") {
			t.Fatalf("error should name the status code, got: %v", err)
		}
	})

	t.Run("garbage body is an error", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `not json at all`)
		hook := NewHTTPHook(httpDef(cs.URL))
		if _, err := hook.PreToolUse(context.Background(), preEvent()); err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("connection refused is an error", func(t *testing.T) {
		t.Parallel()
		// Port 1 is never listening.
		hook := NewHTTPHook(httpDef("http://127.0.0.1:1/hook"))
		if _, err := hook.PreToolUse(context.Background(), preEvent()); err == nil {
			t.Fatal("expected connection error")
		}
	})

	t.Run("timeout is an error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Consume the body first: the Go server only detects client
			// disconnect (and cancels r.Context()) once the body is read.
			// Then block until the client gives up so the test does not wait
			// for the server side.
			_, _ = io.Copy(io.Discard, r.Body)
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
		}))
		t.Cleanup(srv.Close)
		def := httpDef(srv.URL)
		def.TimeoutSeconds = 1
		hook := NewHTTPHook(def)
		start := time.Now()
		_, err := hook.PreToolUse(context.Background(), preEvent())
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if time.Since(start) > 5*time.Second {
			t.Fatalf("request not bounded by hook timeout: %v", time.Since(start))
		}
	})

	t.Run("matcher skip performs no HTTP call", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
		}))
		t.Cleanup(srv.Close)
		def := httpDef(srv.URL)
		def.Matcher = "write_*"
		hook := NewHTTPHook(def)
		result, err := hook.PreToolUse(context.Background(), preEvent()) // tool is "bash"
		if err != nil || result != nil {
			t.Fatalf("got result=%+v err=%v", result, err)
		}
		if calls.Load() != 0 {
			t.Fatal("HTTP call made despite non-matching tool name")
		}
	})
}

// TestHTTPHook_PostsGoldenFields pins the HTTP wire contract: JSON body with
// documented fields and application/json content type.
func TestHTTPHook_PostsGoldenFields(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, http.StatusOK, `{"decision":"allow"}`)
	hook := NewHTTPHook(httpDef(cs.URL))
	if _, err := hook.PreToolUse(context.Background(), preEvent()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ct, _ := cs.contentType.Load().(string)
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}
	body, _ := cs.body.Load().([]byte)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("POST body is not JSON: %v (%s)", err, body)
	}
	for _, field := range []string{"event", "run_id", "hook_name", "tool_name", "call_id", "args"} {
		if _, ok := payload[field]; !ok {
			t.Errorf("POST body missing documented field %q (got %s)", field, body)
		}
	}
	if payload["event"] != "pre_tool_use" {
		t.Errorf("event: got %v", payload["event"])
	}
}

func TestHTTPHook_PostToolUse(t *testing.T) {
	t.Parallel()

	t.Run("modified result", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"modified_result":"audited"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.PostToolUse(context.Background(), harness.PostToolUseEvent{
			ToolName: "bash", Result: "raw", Duration: time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result == nil || result.ModifiedResult != "audited" {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("non-2xx is an error", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusBadGateway, ``)
		hook := NewHTTPHook(httpDef(cs.URL))
		if _, err := hook.PostToolUse(context.Background(), harness.PostToolUseEvent{ToolName: "bash"}); err == nil {
			t.Fatal("expected error for 502")
		}
	})
}

func TestHTTPHook_MessageEvents(t *testing.T) {
	t.Parallel()

	t.Run("pre_message block", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"action":"block","reason":"policy violation"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			RunID: "r1", Step: 3,
			Request: harness.CompletionRequest{Model: "gpt-test", Messages: []harness.Message{{Role: "user", Content: "hi"}}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != harness.HookActionBlock || result.Reason != "policy violation" {
			t.Fatalf("got %+v, want block/policy violation", result)
		}
	})

	t.Run("pre_message continue", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"action":"continue"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			Request: harness.CompletionRequest{Model: "m"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != "" && result.Action != harness.HookActionContinue {
			t.Fatalf("got %+v, want continue", result)
		}
	})

	t.Run("post_message block with golden fields", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"action":"block","reason":"bad answer"}`)
		hook := NewHTTPHook(httpDef(cs.URL))
		result, err := hook.AfterMessage(context.Background(), harness.PostMessageHookInput{
			RunID: "r7", Step: 2,
			Request:   harness.CompletionRequest{Model: "gpt-x", Messages: []harness.Message{{Role: "user", Content: "q"}}},
			Response:  harness.CompletionResult{Content: "the answer"},
			ToolCalls: []harness.ToolCall{{ID: "c1", Name: "bash"}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Action != harness.HookActionBlock || result.Reason != "bad answer" {
			t.Fatalf("got %+v", result)
		}

		body, _ := cs.body.Load().([]byte)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("body not JSON: %s", body)
		}
		if payload["event"] != "post_message" {
			t.Errorf("event: got %v", payload["event"])
		}
		if payload["step"] != float64(2) {
			t.Errorf("step: got %v", payload["step"])
		}
		if payload["model"] != "gpt-x" {
			t.Errorf("model: got %v", payload["model"])
		}
		if payload["response_text"] != "the answer" {
			t.Errorf("response_text: got %v", payload["response_text"])
		}
		if payload["tool_call_count"] != float64(1) {
			t.Errorf("tool_call_count: got %v", payload["tool_call_count"])
		}
		// include_messages not set: full messages must NOT be sent.
		if _, ok := payload["messages"]; ok {
			t.Errorf("messages must be omitted unless include_messages is set (got %v)", payload["messages"])
		}
		if payload["message_count"] != float64(1) {
			t.Errorf("message_count: got %v", payload["message_count"])
		}
	})

	t.Run("include_messages sends full messages", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusOK, `{"action":"continue"}`)
		def := httpDef(cs.URL)
		def.IncludeMessages = true
		hook := NewHTTPHook(def)
		_, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			Request: harness.CompletionRequest{Model: "m", Messages: []harness.Message{{Role: "user", Content: "secret?"}}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		body, _ := cs.body.Load().([]byte)
		if !strings.Contains(string(body), "secret?") {
			t.Errorf("include_messages=true should send message contents, got %s", body)
		}
	})

	t.Run("message endpoint error surfaces as error", func(t *testing.T) {
		t.Parallel()
		cs := newCaptureServer(t, http.StatusServiceUnavailable, ``)
		hook := NewHTTPHook(httpDef(cs.URL))
		if _, err := hook.BeforeMessage(context.Background(), harness.PreMessageHookInput{
			Request: harness.CompletionRequest{Model: "m"},
		}); err == nil {
			t.Fatal("expected error for 503")
		}
	})
}
