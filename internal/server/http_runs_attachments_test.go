package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go-agent-harness/internal/harness"
	"go-agent-harness/internal/provider/catalog"
)

var testImagePNGBase64 = base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3})

// newAttachmentTestServer builds a full-stack test server whose runner is
// backed by a catalog holding an image-capable openai model and a text-only
// anthropic model.
func newAttachmentTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cat := attachmentCatalog(t)
	reg := catalog.NewProviderRegistry(cat)
	runner := harness.NewRunner(
		&staticProvider{result: harness.CompletionResult{Content: "done"}},
		harness.NewRegistry(),
		harness.RunnerConfig{
			DefaultModel:     "gpt-4.1",
			MaxSteps:         1,
			ProviderRegistry: reg,
		},
	)
	handler := NewWithOptions(ServerOptions{
		Runner:           runner,
		Catalog:          cat,
		ProviderRegistry: reg,
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func attachmentCatalog(t *testing.T) *catalog.Catalog {
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

func postRun(t *testing.T, ts *httptest.Server, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := http.Post(ts.URL+"/v1/runs", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST /v1/runs: %v", err)
	}
	defer res.Body.Close()
	var resp map[string]any
	_ = json.NewDecoder(res.Body).Decode(&resp)
	return res.StatusCode, resp
}

func validImageAttachment() map[string]any {
	return map[string]any{"type": "image", "media_type": "image/png", "data": testImagePNGBase64}
}

// errorMessage extracts the nested {"error": {"message": ...}} body used by
// the server's writeError helper.
func errorMessage(t *testing.T, resp map[string]any) string {
	t.Helper()
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error object: %v", resp)
	}
	msg, _ := errObj["message"].(string)
	return msg
}

// TestPostRunImageAttachmentAccepted proves the happy path: an image
// attachment to an image-capable model is accepted (202) with a run id.
func TestPostRunImageAttachmentAccepted(t *testing.T) {
	t.Parallel()
	ts := newAttachmentTestServer(t)

	code, resp := postRun(t, ts, map[string]any{
		"prompt":      "what is in this image?",
		"model":       "gpt-4.1",
		"attachments": []map[string]any{validImageAttachment()},
	})
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; resp = %v", code, resp)
	}
	if resp["run_id"] == nil || resp["run_id"] == "" {
		t.Errorf("response must carry run_id, got %v", resp)
	}
}

// TestPostRunImageAttachmentRejectedForTextOnlyModel proves the server-side
// modality gate: image attachments to a text-only model get HTTP 400 with a
// clear, actionable error naming the model.
func TestPostRunImageAttachmentRejectedForTextOnlyModel(t *testing.T) {
	t.Parallel()
	ts := newAttachmentTestServer(t)

	code, resp := postRun(t, ts, map[string]any{
		"prompt":      "what is in this image?",
		"model":       "claude-sonnet-4-6",
		"attachments": []map[string]any{validImageAttachment()},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; resp = %v", code, resp)
	}
	msg := errorMessage(t, resp)
	if !strings.Contains(msg, "claude-sonnet-4-6") {
		t.Errorf("error must name the model, got %q (resp %v)", msg, resp)
	}
	if !strings.Contains(msg, "image") {
		t.Errorf("error must mention image input, got %q", msg)
	}
}

// TestPostRunMalformedAttachmentRejected proves the 400-on-malformed-block
// matrix reaches the HTTP surface.
func TestPostRunMalformedAttachmentRejected(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		attachment map[string]any
	}{
		{"unsupported type", map[string]any{"type": "video", "media_type": "video/mp4", "data": testImagePNGBase64}},
		{"unsupported media type", map[string]any{"type": "image", "media_type": "image/tiff", "data": testImagePNGBase64}},
		{"empty data", map[string]any{"type": "image", "media_type": "image/png", "data": ""}},
		{"invalid base64", map[string]any{"type": "image", "media_type": "image/png", "data": "!!!not-base64!!!"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ts := newAttachmentTestServer(t)
			code, resp := postRun(t, ts, map[string]any{
				"prompt":      "hi",
				"model":       "gpt-4.1",
				"attachments": []map[string]any{tc.attachment},
			})
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; resp = %v", code, resp)
			}
			msg := errorMessage(t, resp)
			if !strings.Contains(msg, "attachment") {
				t.Errorf("error must identify the attachment, got %q", msg)
			}
		})
	}
}

// TestPostRunTextOnlyModelWithoutAttachmentsAccepted proves the gate never
// fires for plain text runs on text-only models.
func TestPostRunTextOnlyModelWithoutAttachmentsAccepted(t *testing.T) {
	t.Parallel()
	ts := newAttachmentTestServer(t)

	code, resp := postRun(t, ts, map[string]any{
		"prompt": "plain text",
		"model":  "claude-sonnet-4-6",
	})
	if code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; resp = %v", code, resp)
	}
}
