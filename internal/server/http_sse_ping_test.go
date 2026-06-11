package server

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

	"go-agent-harness/internal/harness"
)

// delayProvider returns its result after a configurable delay, creating idle
// time in the event stream for testing SSE keep-alive pings.
type delayProvider struct {
	result harness.CompletionResult
	delay  time.Duration
}

func (d *delayProvider) Complete(_ context.Context, _ harness.CompletionRequest) (harness.CompletionResult, error) {
	time.Sleep(d.delay)
	return d.result, nil
}

func TestHandleRunEvents_SSEPingEmittedWhenIdle(t *testing.T) {
	pingInterval := 20 * time.Millisecond

	provider := &delayProvider{
		result: harness.CompletionResult{Content: "done"},
		delay:  80 * time.Millisecond, // longer than ping interval
	}

	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	handler := NewWithOptions(ServerOptions{
		Runner:          runner,
		AuthDisabled:    true,
		SSEPingInterval: pingInterval,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"Hi"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	eventsRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer eventsRes.Body.Close()

	body, _ := io.ReadAll(eventsRes.Body)
	bodyStr := string(body)

	if !strings.Contains(bodyStr, ": ping") {
		t.Errorf("expected ': ping' SSE keep-alive comment in event stream, got:\n%s", bodyStr)
	}
}

func TestHandleRunEvents_NoPingWhenDisabled(t *testing.T) {
	provider := &delayProvider{
		result: harness.CompletionResult{Content: "done"},
		delay:  30 * time.Millisecond,
	}

	runner := harness.NewRunner(provider, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1-mini",
		MaxSteps:     1,
	})

	handler := NewWithOptions(ServerOptions{
		Runner:          runner,
		AuthDisabled:    true,
		SSEPingInterval: 0, // disabled
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"Hi"}`))
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	eventsRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID + "/events")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer eventsRes.Body.Close()

	body, _ := io.ReadAll(eventsRes.Body)
	bodyStr := string(body)

	if strings.Contains(bodyStr, ": ping") {
		t.Errorf("expected no ': ping' when SSEPingInterval=0, got:\n%s", bodyStr)
	}
}

func TestWriteSSEPing_Format(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeSSEPing(rec)
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if body != ": ping\n\n" {
		t.Errorf("expected ': ping\\n\\n' in SSE ping, got: %q", body)
	}
}
