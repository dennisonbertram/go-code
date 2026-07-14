package tools

// These tests are written as ATTACKS against the WebFetcher-backed tools
// (GAP-2 / BUG-2 follow-up): web_fetch, web_search, and agentic_fetch are all
// backed by a WebFetcher whose Fetch(url) argument is chosen directly by the
// LLM. Before this fix, BuildCatalog (catalog.go) and
// NewDefaultRegistryWithOptions (tools_default.go) handed whatever WebFetcher
// implementation the caller supplied straight to the tool constructors with
// no guard at all, so a prompt-injected agent could reach loopback,
// link-local (169.254.169.254 cloud metadata), and RFC1918/ULA-private
// destinations through web_fetch/agentic_fetch even though the exact same
// class of destination is already refused by the `fetch`/`download` tools'
// guard (ssrf_guard.go).
//
// realHTTPWebFetcher below stands in for what an actual (currently
// nonexistent in this repo) production WebFetcher implementation would look
// like: it performs a real, UNGUARDED http.Get. It is deliberately NOT
// guarded itself — proving the guard must be applied at the wiring point
// (BuildCatalog / NewDefaultRegistryWithOptions), not by every future
// implementation remembering to guard itself.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// realHTTPWebFetcher is a WebFetcher that performs actual, unguarded HTTP
// requests — the shape any real (currently absent) production
// implementation would take.
type realHTTPWebFetcher struct {
	client *http.Client
}

func newRealHTTPWebFetcher() *realHTTPWebFetcher {
	return &realHTTPWebFetcher{client: &http.Client{Timeout: 5 * time.Second}}
}

func (f *realHTTPWebFetcher) Fetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	res, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (f *realHTTPWebFetcher) Search(_ context.Context, query string, maxResults int) ([]map[string]any, error) {
	return []map[string]any{{"query": query, "max": maxResults}}, nil
}

// TestBuildCatalog_WebFetchTool_RefusesLoopbackDestination proves web_fetch
// (built by BuildCatalog) refuses a loopback destination by default, even
// though the underlying WebFetcher implementation would happily perform the
// request itself (realHTTPWebFetcher has no guard of its own).
func TestBuildCatalog_WebFetchTool_RefusesLoopbackDestination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal secret"))
	}))
	defer srv.Close()

	list, err := BuildCatalog(BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableAgent:   true,
		AgentRunner:   &fakeRunner{},
		EnableWebOps:  true,
		WebFetcher:    newRealHTTPWebFetcher(),
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	webFetch := findToolByName(t, list, "web_fetch")

	args, _ := json.Marshal(map[string]any{"url": srv.URL})
	out, err := webFetch.Handler(context.Background(), args)
	if err == nil && !strings.Contains(out, "\"error\"") {
		t.Fatalf("expected web_fetch to refuse a loopback destination, but it succeeded: err=%v out=%s", err, out)
	}
}

// TestBuildCatalog_AgenticFetchTool_RefusesLoopbackDestination proves
// agentic_fetch (which also calls WebFetcher.Fetch with an agent-supplied
// URL) is guarded the same way as web_fetch.
func TestBuildCatalog_AgenticFetchTool_RefusesLoopbackDestination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal secret"))
	}))
	defer srv.Close()

	list, err := BuildCatalog(BuildOptions{
		WorkspaceRoot: t.TempDir(),
		EnableAgent:   true,
		AgentRunner:   &fakeRunner{},
		EnableWebOps:  true,
		WebFetcher:    newRealHTTPWebFetcher(),
	})
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	agenticFetch := findToolByName(t, list, "agentic_fetch")

	args, _ := json.Marshal(map[string]any{"url": srv.URL, "prompt": "summarize"})
	out, err := agenticFetch.Handler(context.Background(), args)
	if err == nil && !strings.Contains(out, "\"error\"") {
		t.Fatalf("expected agentic_fetch to refuse a loopback destination, but it succeeded: err=%v out=%s", err, out)
	}
}
