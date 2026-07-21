package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider"
	"go-agent-harness/internal/provider/catalog"
)

const testImageB64 = "aGVsbG8=" // "hello" — provider layer passes base64 through verbatim

func imageBlock() harness.ContentBlock {
	return harness.ContentBlock{Type: "image", MediaType: "image/png", Data: testImageB64}
}

// anthropicImageCatalog returns a catalog whose anthropic provider holds a
// text-only model (claude-sonnet-4-6) and an image-capable model
// (claude-vision-1).
func anthropicImageCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadCatalogFromBytes([]byte(`{
		"catalog_version": "1",
		"providers": {
			"anthropic": {
				"display_name": "Anthropic",
				"base_url": "https://api.anthropic.com",
				"api_key_env": "ANTHROPIC_API_KEY",
				"protocol": "anthropic",
				"models": {
					"claude-sonnet-4-6": {"display_name": "Claude Sonnet", "description": "text", "context_window": 200000, "modalities": ["text"], "tool_calling": true, "streaming": true},
					"claude-vision-1": {"display_name": "Claude Vision", "description": "vision", "context_window": 200000, "modalities": ["text", "image"], "tool_calling": true, "streaming": true}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("LoadCatalogFromBytes: %v", err)
	}
	return cat
}

// TestMapMessagesUserTextAndImage asserts the exact block-array shape for a
// user message carrying text plus an image block (epic #818 slice 4).
func TestMapMessagesUserTextAndImage(t *testing.T) {
	t.Parallel()

	out, err := mapMessages([]harness.Message{{
		Role:    "user",
		Content: "describe this",
		Blocks:  []harness.ContentBlock{imageBlock()},
	}})
	if err != nil {
		t.Fatalf("mapMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("role = %q, want user", out[0].Role)
	}

	var blocks []map[string]any
	if err := json.Unmarshal(out[0].Content, &blocks); err != nil {
		t.Fatalf("content must be a block array, got %s: %v", out[0].Content, err)
	}
	want := []map[string]any{
		{"type": "text", "text": "describe this"},
		{"type": "image", "source": map[string]any{
			"type":       "base64",
			"media_type": "image/png",
			"data":       testImageB64,
		}},
	}
	if !reflect.DeepEqual(blocks, want) {
		t.Errorf("blocks = %v, want %v", blocks, want)
	}
}

// TestMapMessagesUserImageOnlyNoText verifies an image-only user message does
// not grow an empty text block.
func TestMapMessagesUserImageOnlyNoText(t *testing.T) {
	t.Parallel()

	out, err := mapMessages([]harness.Message{{
		Role:   "user",
		Blocks: []harness.ContentBlock{imageBlock()},
	}})
	if err != nil {
		t.Fatalf("mapMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	var blocks []map[string]any
	if err := json.Unmarshal(out[0].Content, &blocks); err != nil {
		t.Fatalf("content must be a block array, got %s: %v", out[0].Content, err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks len = %d, want exactly 1 image block (no empty text block)", len(blocks))
	}
	if blocks[0]["type"] != "image" {
		t.Errorf("blocks[0].type = %v, want image", blocks[0]["type"])
	}
}

// TestMapMessagesUserTextOnlyRegression guards the pre-slice-4 contract: a
// plain text user message still serializes content as a JSON string, not a
// block array.
func TestMapMessagesUserTextOnlyRegression(t *testing.T) {
	t.Parallel()

	out, err := mapMessages([]harness.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("mapMessages: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	var s string
	if err := json.Unmarshal(out[0].Content, &s); err != nil || s != "hello" {
		t.Errorf("text-only content must stay the JSON string %q, got %s (err %v)", "hello", out[0].Content, err)
	}
}

// captureServer records request bodies and answers with a valid canned
// Anthropic message response.
func captureServer(t *testing.T, hits *atomic.Int32, bodies *atomic.Pointer[[]byte]) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodies.Store(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_01",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "done"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCompleteRefusesImageForTextOnlyModel covers the defense-in-depth
// refusal: a catalog-known text-only model is rejected with the typed
// sentinel before any HTTP request is made.
func TestCompleteRefusesImageForTextOnlyModel(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureServer(t, &hits, &bodies)

	client := newTestClient(t, srv, func(c *Config) {
		c.ProviderName = "anthropic"
		c.Catalog = anthropicImageCatalog(t)
	})
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []harness.Message{{Role: "user", Content: "look", Blocks: []harness.ContentBlock{imageBlock()}}},
	})
	if err == nil {
		t.Fatal("Complete must refuse an image block for a text-only model")
	}
	if !errors.Is(err, provider.ErrImageModalityUnsupported) {
		t.Errorf("err = %v, want errors.Is ErrImageModalityUnsupported", err)
	}
	if !strings.Contains(err.Error(), "claude-sonnet-4-6") {
		t.Errorf("err must name the model, got %q", err.Error())
	}
	if hits.Load() != 0 {
		t.Errorf("refusal must fire before any HTTP request, server hit %d times", hits.Load())
	}
}

// TestCompleteAllowsImageForImageModel proves the happy path end to end: the
// image block is serialized into the /v1/messages request body.
func TestCompleteAllowsImageForImageModel(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureServer(t, &hits, &bodies)

	client := newTestClient(t, srv, func(c *Config) {
		c.ProviderName = "anthropic"
		c.Catalog = anthropicImageCatalog(t)
	})
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "claude-vision-1",
		Messages: []harness.Message{{Role: "user", Content: "look", Blocks: []harness.ContentBlock{imageBlock()}}},
	})
	if err != nil {
		t.Fatalf("Complete with image-capable model: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
	assertAnthropicWireImage(t, *bodies.Load(), "look")
}

// TestCompleteSkipsModalityCheckWithoutCatalog verifies a client with no
// catalog performs no refusal (unknown data → allow).
func TestCompleteSkipsModalityCheckWithoutCatalog(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureServer(t, &hits, &bodies)

	client := newTestClient(t, srv) // no catalog
	_, err := client.Complete(context.Background(), harness.CompletionRequest{
		Model:    "claude-sonnet-4-6",
		Messages: []harness.Message{{Role: "user", Content: "look", Blocks: []harness.ContentBlock{imageBlock()}}},
	})
	if err != nil {
		t.Fatalf("nil catalog must skip the refusal, got %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
	assertAnthropicWireImage(t, *bodies.Load(), "look")
}

// assertAnthropicWireImage checks the captured /v1/messages body carries the
// text and the base64 image block.
func assertAnthropicWireImage(t *testing.T, body []byte, wantText string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode captured body: %v", err)
	}
	msgs, _ := payload["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("no messages in captured body: %s", body)
	}
	raw, _ := json.Marshal(msgs)
	s := string(raw)
	if !strings.Contains(s, `"type":"image"`) {
		t.Errorf("wire body missing an image block: %s", s)
	}
	if !strings.Contains(s, `"type":"base64"`) || !strings.Contains(s, `"media_type":"image/png"`) || !strings.Contains(s, `"data":"`+testImageB64+`"`) {
		t.Errorf("wire body missing the base64 source: %s", s)
	}
	if !strings.Contains(s, wantText) {
		t.Errorf("wire body missing the prompt text %q: %s", wantText, s)
	}
}

// TestRunnerImageBlockReachesAnthropicWire is the slice-4 acceptance proof
// that the block survives runner → provider translation: a real run through
// harness.Runner lands on the Anthropic wire as an image block.
func TestRunnerImageBlockReachesAnthropicWire(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	var bodies atomic.Pointer[[]byte]
	srv := captureServer(t, &hits, &bodies)

	client := newTestClient(t, srv) // no catalog → no refusal
	runner := harness.NewRunner(client, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "claude-sonnet-4-6",
		MaxSteps:     1,
	})
	run, err := runner.StartRun(harness.RunRequest{
		Prompt:      "what is in this image?",
		Model:       "claude-sonnet-4-6",
		Attachments: []harness.ContentBlock{imageBlock()},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitTerminal(t, runner, run.ID)

	if hits.Load() != 1 {
		t.Fatalf("server hit %d times, want 1", hits.Load())
	}
	assertAnthropicWireImage(t, *bodies.Load(), "what is in this image?")
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
