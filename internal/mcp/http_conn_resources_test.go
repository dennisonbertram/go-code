package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go-agent-harness/internal/mcp"
)

func newHTTPResourceServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			Method  string          `json:"method"`
			ID      json.RawMessage `json:"id"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var result any
		switch req.Method {
		case "resources/list":
			result = map[string]any{
				"resources": []map[string]any{
					{
						"uri":         "file:///a.txt",
						"name":        "a",
						"description": "file a",
						"mimeType":    "text/plain",
					},
					{
						"uri":         "file:///b.txt",
						"name":        "b",
						"description": "file b",
						"mimeType":    "text/plain",
					},
				},
			}
		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(req.Params, &params)
			result = map[string]any{
				"contents": []map[string]any{
					{
						"uri":      params.URI,
						"mimeType": "text/plain",
						"text":     "hello from " + params.URI,
					},
				},
			}
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestHTTPConn_ListResources(t *testing.T) {
	t.Parallel()

	srv := newHTTPResourceServer(t)
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test-server", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resources, err := conn.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if resources[0].URI != "file:///a.txt" || resources[0].Name != "a" {
		t.Errorf("unexpected resource[0]: %+v", resources[0])
	}
	if resources[1].URI != "file:///b.txt" || resources[1].Name != "b" {
		t.Errorf("unexpected resource[1]: %+v", resources[1])
	}
}

func TestHTTPConn_ReadResource(t *testing.T) {
	t.Parallel()

	srv := newHTTPResourceServer(t)
	defer srv.Close()

	conn := mcp.NewHTTPConnForTest("test-server", srv.URL)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	content, err := conn.ReadResource(ctx, "file:///a.txt")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	want := fmt.Sprintf("hello from %s", "file:///a.txt")
	if content != want {
		t.Errorf("ReadResource content = %q, want %q", content, want)
	}
}
