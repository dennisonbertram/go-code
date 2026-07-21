package harness

import (
	"encoding/base64"
	"strings"
	"testing"

	"go-agent-harness/internal/provider/catalog"
)

// testPNGBase64 is a tiny valid PNG payload used across attachment tests.
var testPNGBase64 = base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3})

func imageAttachment() ContentBlock {
	return ContentBlock{Type: "image", MediaType: "image/png", Data: testPNGBase64}
}

// attachmentTestCatalog returns a catalog with an image-capable openai model
// and a text-only anthropic model.
func attachmentTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadCatalogFromBytes([]byte(`{
		"catalog_version": "1",
		"providers": {
			"openai": {
				"display_name": "OpenAI",
				"base_url": "https://api.openai.com/v1",
				"api_key_env": "OPENAI_API_KEY",
				"protocol": "openai",
				"models": {
					"gpt-4.1": {"display_name": "GPT-4.1", "description": "vision", "context_window": 128000, "modalities": ["text", "image"], "tool_calling": true, "streaming": true}
				}
			},
			"anthropic": {
				"display_name": "Anthropic",
				"base_url": "https://api.anthropic.com",
				"api_key_env": "ANTHROPIC_API_KEY",
				"protocol": "anthropic",
				"models": {
					"claude-sonnet-4-6": {"display_name": "Claude Sonnet", "description": "text", "context_window": 200000, "modalities": ["text"], "tool_calling": true, "streaming": true}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("LoadCatalogFromBytes: %v", err)
	}
	return cat
}

// TestStartRunRejectsMalformedAttachments covers the 400-on-malformed-block
// matrix: unsupported type, unsupported media type, empty data, invalid
// base64.
func TestStartRunRejectsMalformedAttachments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		block   ContentBlock
		wantErr string
	}{
		{"unsupported type", ContentBlock{Type: "video", MediaType: "video/mp4", Data: testPNGBase64}, "type"},
		{"unsupported media type", ContentBlock{Type: "image", MediaType: "image/tiff", Data: testPNGBase64}, "media_type"},
		{"empty data", ContentBlock{Type: "image", MediaType: "image/png", Data: ""}, "data"},
		{"invalid base64", ContentBlock{Type: "image", MediaType: "image/png", Data: "!!!not-base64!!!"}, "base64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{DefaultModel: "gpt-4.1", MaxSteps: 1})
			_, err := runner.StartRun(RunRequest{
				Prompt:      "hi",
				Model:       "gpt-4.1",
				Attachments: []ContentBlock{tc.block},
			})
			if err == nil {
				t.Fatalf("StartRun with %s attachment must fail", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q must mention %q", err.Error(), tc.wantErr)
			}
			if !strings.Contains(err.Error(), "attachment") {
				t.Errorf("error %q must identify the attachment", err.Error())
			}
		})
	}
}

// TestStartRunImageModalityGate covers the server-side modality gate matrix:
// text-only catalog models are rejected, image models and unknown models are
// allowed, and the gate never fires without attachments.
func TestStartRunImageModalityGate(t *testing.T) {
	t.Parallel()

	newRunner := func() *Runner {
		reg := catalog.NewProviderRegistry(attachmentTestCatalog(t))
		return NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{
			DefaultModel:     "gpt-4.1",
			MaxSteps:         1,
			ProviderRegistry: reg,
		})
	}

	t.Run("text-only model rejected", func(t *testing.T) {
		t.Parallel()
		_, err := newRunner().StartRun(RunRequest{
			Prompt:      "look at this",
			Model:       "claude-sonnet-4-6",
			Attachments: []ContentBlock{imageAttachment()},
		})
		if err == nil {
			t.Fatal("attachments to a text-only model must be rejected")
		}
		if !strings.Contains(err.Error(), "claude-sonnet-4-6") {
			t.Errorf("error must name the model, got %q", err.Error())
		}
		if !strings.Contains(err.Error(), "image") {
			t.Errorf("error must mention image input, got %q", err.Error())
		}
	})

	t.Run("image model allowed", func(t *testing.T) {
		t.Parallel()
		r := newRunner()
		run, err := r.StartRun(RunRequest{
			Prompt:      "look at this",
			Model:       "gpt-4.1",
			Attachments: []ContentBlock{imageAttachment()},
		})
		if err != nil {
			t.Fatalf("image-capable model must accept attachments, got %v", err)
		}
		waitForRunCompletion(t, r, run.ID)
	})

	t.Run("unknown model allowed", func(t *testing.T) {
		t.Parallel()
		// A model absent from the catalog (e.g. discovered at runtime) is
		// not gated — the provider call is the source of truth.
		_, err := newRunner().StartRun(RunRequest{
			Prompt:      "look at this",
			Model:       "some-uncatalogued-model",
			Attachments: []ContentBlock{imageAttachment()},
		})
		if err != nil {
			t.Fatalf("unknown model must be allowed, got %v", err)
		}
	})

	t.Run("no attachments no gate", func(t *testing.T) {
		t.Parallel()
		_, err := newRunner().StartRun(RunRequest{
			Prompt: "plain text",
			Model:  "claude-sonnet-4-6",
		})
		if err != nil {
			t.Fatalf("text-only run on a text-only model must be allowed, got %v", err)
		}
	})

	t.Run("nil registry skips gate", func(t *testing.T) {
		t.Parallel()
		runner := NewRunner(&stubProvider{}, NewRegistry(), RunnerConfig{DefaultModel: "gpt-4.1", MaxSteps: 1})
		_, err := runner.StartRun(RunRequest{
			Prompt:      "look at this",
			Model:       "claude-sonnet-4-6",
			Attachments: []ContentBlock{imageAttachment()},
		})
		if err != nil {
			t.Fatalf("nil provider registry must skip the gate, got %v", err)
		}
	})
}

// TestMessageCloneDeepCopiesBlocks guards the ownership contract: mutating a
// cloned message's Blocks must not affect the original (runbook:
// ownership/copy semantics for exported types with mutable fields).
func TestMessageCloneDeepCopiesBlocks(t *testing.T) {
	t.Parallel()

	orig := Message{
		Role:    "user",
		Content: "hi",
		Blocks:  []ContentBlock{imageAttachment()},
	}
	clone := orig.Clone()
	clone.Blocks[0].Data = "mutated"

	if orig.Blocks[0].Data != testPNGBase64 {
		t.Errorf("Clone must deep-copy Blocks: original mutated to %q", orig.Blocks[0].Data)
	}
}
