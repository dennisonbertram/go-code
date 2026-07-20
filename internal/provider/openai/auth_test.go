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
)

type tokenSourceFunc func(context.Context) (string, error)

func (f tokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClientDynamicTokenAndExtraHeadersApplyToBothEndpoints(t *testing.T) {
	t.Parallel()

	var tokenCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-dynamic-credential" {
			t.Fatal("request did not use the dynamic bearer credential")
		}
		if r.Header.Get("X-Subscription-Account") != "fake-account-id" {
			t.Fatal("request did not include the configured extra header")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/responses" {
			_, _ = w.Write([]byte(`{"id":"resp_fake","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL,
		TokenSource: tokenSourceFunc(func(context.Context) (string, error) {
			tokenCalls.Add(1)
			return "fake-dynamic-credential", nil
		}),
		ExtraHeaders: map[string]string{"X-Subscription-Account": "fake-account-id"},
		ModelAPILookup: func(_ string, model string) string {
			if model == "responses-model" {
				return "responses"
			}
			return ""
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}

	for _, model := range []string{"chat-model", "responses-model"} {
		if _, err := client.Complete(context.Background(), harness.CompletionRequest{
			Model: model, Messages: []harness.Message{{Role: "user", Content: "hello"}},
		}); err != nil {
			t.Fatalf("Complete(%q) error: %v", model, err)
		}
	}
	if tokenCalls.Load() != 2 {
		t.Fatalf("Token() calls = %d, want 2", tokenCalls.Load())
	}
}

func TestClientStaticAPIKeyHeadersRemainUnchangedWithoutTokenSource(t *testing.T) {
	t.Parallel()

	var captured http.Header
	client, err := NewClient(Config{
		APIKey: "fake-static-credential",
		Client: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			captured = r.Header.Clone()
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
				Request:    r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if _, err := client.Complete(context.Background(), harness.CompletionRequest{Messages: []harness.Message{{Role: "user", Content: "hello"}}}); err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	want := http.Header{"Authorization": {"Bearer fake-static-credential"}, "Content-Type": {"application/json"}}
	if !headersEqual(captured, want) {
		t.Fatal("static API-key request headers changed")
	}
}

func TestClientTokenSourceErrorFailsRequest(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{TokenSource: tokenSourceFunc(func(context.Context) (string, error) {
		return "", errors.New("credential source unavailable")
	})})
	if err != nil {
		t.Fatalf("NewClient() error: %v", err)
	}
	if _, err := client.Complete(context.Background(), harness.CompletionRequest{Messages: []harness.Message{{Role: "user", Content: "hello"}}}); err == nil {
		t.Fatal("Complete() succeeded after token-source failure")
	}
}

func headersEqual(got, want http.Header) bool {
	if len(got) != len(want) {
		return false
	}
	for key, wantValues := range want {
		gotValues, ok := got[key]
		if !ok || len(gotValues) != len(wantValues) {
			return false
		}
		for i := range wantValues {
			if gotValues[i] != wantValues[i] {
				return false
			}
		}
	}
	return true
}
