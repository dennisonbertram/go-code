package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
)

// TestCancelProvider verifies mid-flight cancellation of an in-flight provider
// Complete() call through the HTTP API.
func TestCancelProvider(t *testing.T) {
	t.Parallel()

	// Create a fakeprovider with a single Hang turn so Complete blocks on ctx
	// until cancelled.
	prov := fakeprovider.New(
		[]fakeprovider.Turn{
			{Hang: true},
		},
	)

	// Create runner and HTTP server
	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()
	defer runner.Shutdown(context.Background())

	// Start a run
	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Wait for the provider to be in-flight (poll until prov.Calls() == 1)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if prov.Calls() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if prov.Calls() < 1 {
		t.Fatal("provider never started; prov.Calls() < 1")
	}

	// POST /cancel
	cancelRes, err := http.Post(
		ts.URL+"/v1/runs/"+created.RunID+"/cancel",
		"application/json",
		nil,
	)
	if err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	defer cancelRes.Body.Close()

	if cancelRes.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(cancelRes.Body)
		t.Fatalf("expected cancel 200, got %d: %s", cancelRes.StatusCode, string(body))
	}

	// Verify the run terminates with status "cancelled" within a bounded deadline
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		checkRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		var runState struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(checkRes.Body).Decode(&runState); err != nil {
			checkRes.Body.Close()
			time.Sleep(10 * time.Millisecond)
			continue
		}
		checkRes.Body.Close()

		if runState.Status == "cancelled" {
			// Success!
			prov.Release()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Final check
	checkRes, _ := http.Get(ts.URL + "/v1/runs/" + created.RunID)
	if checkRes != nil {
		var runState struct {
			Status string `json:"status"`
		}
		json.NewDecoder(checkRes.Body).Decode(&runState)
		checkRes.Body.Close()
		if runState.Status != "cancelled" {
			t.Errorf("expected status 'cancelled', got %q", runState.Status)
		}
	} else {
		t.Fatal("final status check failed")
	}

	prov.Release()
}

// TestCancelProvider_Events verifies that the run.cancelled event appears
// and run.completed does not when a provider is cancelled mid-flight.
func TestCancelProvider_Events(t *testing.T) {
	t.Parallel()

	// Create a fakeprovider with a single Hang turn
	prov := fakeprovider.New(
		[]fakeprovider.Turn{
			{Hang: true},
		},
	)

	// Create runner and HTTP server
	runner := harness.NewRunner(prov, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "test-model",
		MaxSteps:     5,
	})

	handler := New(runner)
	ts := httptest.NewServer(handler)
	defer ts.Close()
	defer runner.Shutdown(context.Background())

	// Start a run
	res, err := http.Post(ts.URL+"/v1/runs", "application/json",
		bytes.NewBufferString(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	defer res.Body.Close()

	var created struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Wait for the provider to be genuinely in-flight (prov.Calls() == 1).
	// This mirrors TestCancelProvider and proves the cancel is mid-flight, not
	// a race where we cancel before the provider has been reached.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if prov.Calls() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if prov.Calls() < 1 {
		t.Fatal("provider never went in-flight; prov.Calls() < 1 after 3s deadline")
	}

	// Subscribe to events in a goroutine
	eventsChan := make(chan string, 100)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/v1/runs/"+created.RunID+"/events", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			close(eventsChan)
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		eventsChan <- string(body)
		close(eventsChan)
	}()

	// Cancel the run
	cancelRes, err := http.Post(
		ts.URL+"/v1/runs/"+created.RunID+"/cancel",
		"application/json",
		nil,
	)
	if err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	cancelRes.Body.Close()

	// Wait for events
	var eventBody string
	select {
	case eb := <-eventsChan:
		eventBody = eb
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for events")
	}

	// Assert run.cancelled appears
	if eventBody == "" {
		t.Fatal("no events received")
	}
	if !bytes.Contains([]byte(eventBody), []byte("run.cancelled")) {
		t.Errorf("expected 'run.cancelled' event in stream:\n%s", eventBody)
	}

	// Assert run.completed does NOT appear
	if bytes.Contains([]byte(eventBody), []byte("run.completed")) {
		t.Errorf("expected NO 'run.completed' event when cancelled, got:\n%s", eventBody)
	}

	// Independently confirm the run's final API status via GET /v1/runs/{id}.
	// This verifies that the server's persisted state matches the SSE events and
	// that the test stands alone without relying solely on the event stream.
	statusRes, err := http.Get(ts.URL + "/v1/runs/" + created.RunID)
	if err != nil {
		t.Fatalf("GET /v1/runs/%s: %v", created.RunID, err)
	}
	defer statusRes.Body.Close()
	var finalState struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(statusRes.Body).Decode(&finalState); err != nil {
		t.Fatalf("decode run status: %v", err)
	}
	if finalState.Status != "cancelled" {
		t.Errorf("GET /v1/runs/%s: expected status %q, got %q", created.RunID, "cancelled", finalState.Status)
	}

	prov.Release()
}
