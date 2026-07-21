package server

import (
	"testing"

	"go-agent-harness/internal/fakeprovider"
	"go-agent-harness/internal/harness"
)

// waitRunnerTerminal subscribes to a run and blocks until a terminal event
// arrives (mirrors the harness-internal wait helper, which this package
// cannot reach).
func waitRunnerTerminal(t *testing.T, runner *harness.Runner, runID string) {
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

// TestRunRequestAttachmentsFlowToCompletionRequest proves the typed image
// blocks travel StartRun → runner step loop → provider CompletionRequest,
// alongside the text prompt (epic #818 slice 3 acceptance, asserted via
// fakeprovider.LastRequest()).
func TestRunRequestAttachmentsFlowToCompletionRequest(t *testing.T) {
	t.Parallel()

	fp := fakeprovider.New([]fakeprovider.Turn{{Content: "done"}})
	runner := harness.NewRunner(fp, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1",
		MaxSteps:     1,
	})

	run, err := runner.StartRun(harness.RunRequest{
		Prompt:      "what is in this image?",
		Model:       "gpt-4.1",
		Attachments: []harness.ContentBlock{{Type: "image", MediaType: "image/png", Data: testImagePNGBase64}},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitRunnerTerminal(t, runner, run.ID)

	req, ok := fp.LastRequest()
	if !ok {
		t.Fatal("fakeprovider never received a completion request")
	}
	// Find the user message carrying the run's prompt — the runner appends
	// later user messages (step-limit nudges), so match on the prompt text.
	var user *harness.Message
	for i := range req.Messages {
		if req.Messages[i].Role == "user" && req.Messages[i].Content == "what is in this image?" {
			user = &req.Messages[i]
		}
	}
	if user == nil {
		t.Fatalf("no user message with the prompt in CompletionRequest: %+v", req.Messages)
	}
	if len(user.Blocks) != 1 {
		t.Fatalf("user message Blocks len = %d, want 1 image block", len(user.Blocks))
	}
	block := user.Blocks[0]
	if block.Type != "image" {
		t.Errorf("block.Type = %q, want image", block.Type)
	}
	if block.MediaType != "image/png" {
		t.Errorf("block.MediaType = %q, want image/png", block.MediaType)
	}
	if block.Data != testImagePNGBase64 {
		t.Errorf("block.Data = %q, want the exact base64 payload", block.Data)
	}
}

// TestRunRequestAttachmentsTextOnlyLeavesBlocksNil guards the regression
// contract: a run without attachments produces messages with no Blocks, so
// text-only serialization is byte-identical to before slice 3.
func TestRunRequestAttachmentsTextOnlyLeavesBlocksNil(t *testing.T) {
	t.Parallel()

	fp := fakeprovider.New([]fakeprovider.Turn{{Content: "done"}})
	runner := harness.NewRunner(fp, harness.NewRegistry(), harness.RunnerConfig{
		DefaultModel: "gpt-4.1",
		MaxSteps:     1,
	})

	run, err := runner.StartRun(harness.RunRequest{Prompt: "hello", Model: "gpt-4.1"})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	waitRunnerTerminal(t, runner, run.ID)

	req, ok := fp.LastRequest()
	if !ok {
		t.Fatal("fakeprovider never received a completion request")
	}
	for _, m := range req.Messages {
		if len(m.Blocks) != 0 {
			t.Errorf("text-only run must not carry blocks, message %+v has %d", m, len(m.Blocks))
		}
	}
}
