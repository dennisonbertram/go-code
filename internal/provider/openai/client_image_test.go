package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider"
)

const testImageB64 = "aGVsbG8=" // "hello" — provider layer passes base64 through verbatim

func imageBlock() harness.ContentBlock {
	return harness.ContentBlock{Type: "image", MediaType: "image/png", Data: testImageB64}
}

func userWithImage(text string) harness.Message {
	return harness.Message{Role: "user", Content: text, Blocks: []harness.ContentBlock{imageBlock()}}
}

// TestMapMessagesChatUserTextAndImage asserts the chat-completions content
// parts shape for a user message with text + image (epic #818 slice 4).
func TestMapMessagesChatUserTextAndImage(t *testing.T) {
	t.Parallel()

	out := mapMessages([]harness.Message{userWithImage("describe this")}, false)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	parts, ok := out[0].Content.([]chatContentPart)
	if !ok {
		t.Fatalf("content must be []chatContentPart for a message with blocks, got %T", out[0].Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2 (text + image)", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "describe this" {
		t.Errorf("parts[0] = %+v, want the text part", parts[0])
	}
	if parts[1].Type != "image_url" {
		t.Errorf("parts[1].Type = %q, want image_url", parts[1].Type)
	}
	if parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,"+testImageB64 {
		t.Errorf("parts[1].ImageURL = %+v, want the data URL", parts[1].ImageURL)
	}
}

// TestMapMessagesChatTextOnlyRegression guards the pre-slice-4 contract: a
// plain text user message keeps string content.
func TestMapMessagesChatTextOnlyRegression(t *testing.T) {
	t.Parallel()

	out := mapMessages([]harness.Message{{Role: "user", Content: "hello"}}, false)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if s, ok := out[0].Content.(string); !ok || s != "hello" {
		t.Errorf("text-only content must stay the string %q, got %v (%T)", "hello", out[0].Content, out[0].Content)
	}
}

// TestMapToResponsesRequestUserTextAndImage asserts the responses-API input
// parts shape (input_text + input_image with a data URL).
func TestMapToResponsesRequestUserTextAndImage(t *testing.T) {
	t.Parallel()

	rr := mapToResponsesRequest(harness.CompletionRequest{
		Messages: []harness.Message{userWithImage("describe this")},
	}, "computer-use-preview")

	var userItem *responsesInputItem
	for i := range rr.Input {
		if rr.Input[i].Type == "message" && rr.Input[i].Role == "user" {
			userItem = &rr.Input[i]
		}
	}
	if userItem == nil {
		t.Fatalf("no user message item in input: %+v", rr.Input)
	}
	parts, ok := userItem.Content.([]responsesInputContent)
	if !ok {
		t.Fatalf("content must be []responsesInputContent for a message with blocks, got %T", userItem.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2 (input_text + input_image)", len(parts))
	}
	if parts[0].Type != "input_text" || parts[0].Text != "describe this" {
		t.Errorf("parts[0] = %+v, want the input_text part", parts[0])
	}
	if parts[1].Type != "input_image" || parts[1].ImageURL != "data:image/png;base64,"+testImageB64 {
		t.Errorf("parts[1] = %+v, want the input_image data URL", parts[1])
	}
}

// TestMapToResponsesRequestTextOnlyRegression guards the pre-slice-4
// contract: a plain text user message keeps string content in the responses
// request.
func TestMapToResponsesRequestTextOnlyRegression(t *testing.T) {
	t.Parallel()

	rr := mapToResponsesRequest(harness.CompletionRequest{
		Messages: []harness.Message{{Role: "user", Content: "hello"}},
	}, "computer-use-preview")

	var userItem *responsesInputItem
	for i := range rr.Input {
		if rr.Input[i].Type == "message" && rr.Input[i].Role == "user" {
			userItem = &rr.Input[i]
		}
	}
	if userItem == nil {
		t.Fatalf("no user message item in input: %+v", rr.Input)
	}
	if s, ok := userItem.Content.(string); !ok || s != "hello" {
		t.Errorf("text-only content must stay the string %q, got %v (%T)", "hello", userItem.Content, userItem.Content)
	}
}

// captureChatServer records hits/bodies for both endpoints and answers with
// valid canned responses.
func captureChatServer(t *testing.T, hits *atomic.Int32, bodies *atomic.Pointer[[]byte]) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodies.Store(&body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = w.Write([]byte(`{
				"id":"resp_test",
				"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}],
				"usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func modalityLookup(mods ...string) ModelModalityLookupFn {
	return func(_, _ string) []string { return mods }
}

// TestCompleteRefusesImageForTextOnlyModelChat covers the refusal on the
// chat-completions path: typed error, zero HTTP calls.
func TestCompleteRefusesImageForTextOnlyModelChat(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureChatServer(t, &hits, &bodies)

	client, err := NewClient(Config{
		APIKey:              "test-key",
		BaseURL:             srv.URL,
		ProviderName:        "openai",
		ModelModalityLookup: modalityLookup("text"),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-4.1-mini",
		Messages: []harness.Message{userWithImage("look")},
	})
	if err == nil {
		t.Fatal("Complete must refuse an image block for a text-only model")
	}
	if !errors.Is(err, provider.ErrImageModalityUnsupported) {
		t.Errorf("err = %v, want errors.Is ErrImageModalityUnsupported", err)
	}
	if !strings.Contains(err.Error(), "gpt-4.1-mini") {
		t.Errorf("err must name the model, got %q", err.Error())
	}
	if hits.Load() != 0 {
		t.Errorf("refusal must fire before any HTTP request, server hit %d times", hits.Load())
	}
}

// TestCompleteRefusesImageForTextOnlyModelResponses covers the refusal on
// the responses-API path.
func TestCompleteRefusesImageForTextOnlyModelResponses(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureChatServer(t, &hits, &bodies)

	client, err := NewClient(Config{
		APIKey:              "test-key",
		BaseURL:             srv.URL,
		ProviderName:        "openai",
		ModelAPILookup:      func(_, _ string) string { return "responses" },
		ModelModalityLookup: modalityLookup("text"),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "computer-use-preview",
		Messages: []harness.Message{userWithImage("look")},
	})
	if !errors.Is(err, provider.ErrImageModalityUnsupported) {
		t.Errorf("err = %v, want errors.Is ErrImageModalityUnsupported", err)
	}
	if hits.Load() != 0 {
		t.Errorf("refusal must fire before any HTTP request, server hit %d times", hits.Load())
	}
}

// TestCompleteAllowsImageForImageModelChat proves the chat-completions happy
// path: image part lands in the wire body.
func TestCompleteAllowsImageForImageModelChat(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureChatServer(t, &hits, &bodies)

	client, err := NewClient(Config{
		APIKey:              "test-key",
		BaseURL:             srv.URL,
		ProviderName:        "openai",
		ModelModalityLookup: modalityLookup("text", "image"),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-4.1",
		Messages: []harness.Message{userWithImage("look")},
	})
	if err != nil {
		t.Fatalf("Complete with image-capable model: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
	body := string(*bodies.Load())
	if !strings.Contains(body, `"type":"image_url"`) || !strings.Contains(body, "data:image/png;base64,"+testImageB64) {
		t.Errorf("chat wire body missing the image_url data URL: %s", body)
	}
	if !strings.Contains(body, "look") {
		t.Errorf("chat wire body missing the prompt text: %s", body)
	}
}

// TestCompleteAllowsImageForImageModelResponses proves the responses-API
// happy path: input_image part lands in the wire body.
func TestCompleteAllowsImageForImageModelResponses(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureChatServer(t, &hits, &bodies)

	client, err := NewClient(Config{
		APIKey:              "test-key",
		BaseURL:             srv.URL,
		ProviderName:        "openai",
		ModelAPILookup:      func(_, _ string) string { return "responses" },
		ModelModalityLookup: modalityLookup("text", "image"),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "computer-use-preview",
		Messages: []harness.Message{userWithImage("look")},
	})
	if err != nil {
		t.Fatalf("Complete with image-capable model: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
	body := string(*bodies.Load())
	if !strings.Contains(body, `"type":"input_image"`) || !strings.Contains(body, "data:image/png;base64,"+testImageB64) {
		t.Errorf("responses wire body missing the input_image data URL: %s", body)
	}
}

// TestCompleteSkipsRefusalWithoutLookup verifies a nil ModelModalityLookup
// disables the refusal (unknown data → allow).
func TestCompleteSkipsRefusalWithoutLookup(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureChatServer(t, &hits, &bodies)

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: srv.URL, ProviderName: "openai"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "gpt-4.1-mini",
		Messages: []harness.Message{userWithImage("look")},
	})
	if err != nil {
		t.Fatalf("nil lookup must skip the refusal, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
}

// TestRunnerImageBlockReachesOpenAIWire is the slice-4 acceptance proof that
// the block survives runner → provider translation on the chat-completions
// wire.
func TestRunnerImageBlockReachesOpenAIWire(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureChatServer(t, &hits, &bodies)

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: srv.URL, ProviderName: "openai"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	runner := harness.NewRunner(client, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1",
		MaxSteps:     1,
	})
	run, err := runner.StartRun(harness.RunRequest{
		Prompt:      "what is in this image?",
		Model:       "gpt-4.1",
		Attachments: []harness.ContentBlock{imageBlock()},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitTerminal(t, runner, run.ID)

	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
	body := string(*bodies.Load())
	if !strings.Contains(body, `"type":"image_url"`) || !strings.Contains(body, "data:image/png;base64,"+testImageB64) {
		t.Errorf("wire body missing the image block: %s", body)
	}
	if !strings.Contains(body, "what is in this image?") {
		t.Errorf("wire body missing the prompt text: %s", body)
	}
}

// waitTerminal blocks until the run emits a terminal event.
func waitTerminal(t *testing.T, runner *harness.Runner, runID string) {
	t.Helper()
	history, stream, cancel, err := runner.Subscribe(runID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	for _, ev := range history {
		if harness.IsTerminalEvent(ev.Type) {
			return
		}
	}
	for ev := range stream {
		if harness.IsTerminalEvent(ev.Type) {
			return
		}
	}
}
