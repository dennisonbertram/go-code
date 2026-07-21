package tui

// api_auth_test.go — TASK: fix/sse-bridge-resilience (auth follow-up #2)
//
// A parallel agent is hardening harnessd's server-side auth (removing
// query-param auth, enforcing tenant scoping). Once a user configures an
// API key, every unauthenticated TUI request to harnessd would get a 401.
// This file proves EVERY harnessd-targeting call in cmd/harnesscli/tui
// authenticates via newHarnessRequest (bridge.go's SSEBridgeOptions.APIKey
// covers the SSE stream itself and is tested in sse_bridge_resilience_test.go
// — this file covers everything else in api.go, askuser.go, and approval.go).
//
// Full audit of every request-construction call site in cmd/harnesscli/tui/
// (grep for http.Get/http.Post/http.NewRequest/NewRequestWithContext),
// classified harnessd vs external:
//
//	FILE            FUNCTION                      TARGET      AUTH
//	api.go          startRunCmd                    harnessd    yes (newHarnessRequest)
//	api.go          fetchRunsCmd                    harnessd    yes
//	api.go          cancelRunCmd                    harnessd    yes
//	api.go          compactRunCmd                   harnessd    yes
//	api.go          steerRunCmd                     harnessd    yes
//	api.go          replayRunCmd                    harnessd    yes
//	api.go          continueRunCmd                  harnessd    yes
//	api.go          fetchModelsCmd                  harnessd    yes
//	api.go          fetchOpenRouterModelsFromURL    EXTERNAL    no (provider key only — see below)
//	api.go          loadSubagentsCmd                harnessd    yes
//	api.go          fetchProvidersCmd               harnessd    yes
//	api.go          setProviderKeyCmd               harnessd    yes (providerKey stays in the body)
//	api.go          loadProfilesCmd                 harnessd    yes
//	api.go          fetchSessionRunsCmd             harnessd    yes (currently unwired into any
//	                                                              model.go call site — dead code —
//	                                                              still authenticated for when it is)
//	api.go          fetchConversationMessagesCmd    harnessd    yes
//	bridge.go       runBridge (SSE events stream)   harnessd    yes (SSEBridgeOptions.APIKey,
//	                                                              tested in sse_bridge_resilience_test.go)
//	askuser.go      fetchAskUserPendingCmd          harnessd    yes
//	askuser.go      submitAskUserAnswerCmd          harnessd    yes
//	approval.go     toolApprovalDecisionCmd         harnessd    yes (backs approveToolCmd/denyToolCmd)
//
// No endpoint here is intentionally public (e.g. a health check) — every one
// serves authenticated user/run data, so none are exempted.
import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"
)

// harnessAuthCase describes one harnessd-targeting call: how to invoke it and
// how to read back the Authorization header the mock server observed.
type harnessAuthCase struct {
	name string
	// call issues exactly one HTTP request to ts.URL and returns the
	// resulting tea.Msg, so the table can also sanity-check the happy path
	// still decodes correctly (not just the header).
	call func(ts *httptest.Server, apiKey string) any
}

func harnessAuthCases() []harnessAuthCase {
	return []harnessAuthCase{
		{
			name: "startRunCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return startRunCmd(ts.URL, "hello", "", "gpt-test", "openai", "", "default", "/tmp", apiKey, nil, nil)()
			},
		},
		{
			name: "fetchRunsCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return fetchRunsCmd(ts.URL, apiKey)()
			},
		},
		{
			name: "cancelRunCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return cancelRunCmd(ts.URL, "run-1", apiKey)()
			},
		},
		{
			name: "compactRunCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return compactRunCmd(ts.URL, "run-1", "keep the SQL schema", apiKey)()
			},
		},
		{
			name: "steerRunCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return steerRunCmd(ts.URL, "run-1", "focus on tests", apiKey)()
			},
		},
		{
			name: "replayRunCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return replayRunCmd(ts.URL, "run-1", apiKey)()
			},
		},
		{
			name: "continueRunCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return continueRunCmd(ts.URL, "run-1", "more please", apiKey)()
			},
		},
		{
			name: "fetchModelsCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return fetchModelsCmd(ts.URL, apiKey)()
			},
		},
		{
			name: "loadSubagentsCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return loadSubagentsCmd(ts.URL, apiKey)()
			},
		},
		{
			name: "fetchProvidersCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return fetchProvidersCmd(ts.URL, apiKey)()
			},
		},
		{
			name: "setProviderKeyCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return setProviderKeyCmd(ts.URL, "openai", "provider-secret", apiKey)()
			},
		},
		{
			name: "loadProfilesCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return loadProfilesCmd(ts.URL, apiKey)()
			},
		},
		{
			name: "fetchSessionRunsCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return fetchSessionRunsCmd(ts.URL, "conv-1", apiKey)()
			},
		},
		{
			name: "fetchConversationMessagesCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return fetchConversationMessagesCmd(ts.URL, "conv-1", apiKey)()
			},
		},
		{
			name: "undoConversationCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return undoConversationCmd(ts.URL, "conv-1", 1, apiKey)()
			},
		},
		{
			name: "fetchAskUserPendingCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return fetchAskUserPendingCmd(ts.URL, "run-1", apiKey)()
			},
		},
		{
			name: "submitAskUserAnswerCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return submitAskUserAnswerCmd(ts.URL, "run-1", map[string]string{"q": "a"}, apiKey)()
			},
		},
		{
			name: "approveToolCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return approveToolCmd(ts.URL, "run-1", apiKey)()
			},
		},
		{
			name: "denyToolCmd",
			call: func(ts *httptest.Server, apiKey string) any {
				return denyToolCmd(ts.URL, "run-1", apiKey)()
			},
		},
	}
}

// newAuthEchoServer returns an httptest.Server that records the Authorization
// header of the most recent request and replies with a minimal, valid body
// for whichever endpoint hit it (each Cmd factory decodes a different shape,
// so the handler returns a permissive JSON object that satisfies all of
// them without erroring).
func newAuthEchoServer(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			// A generic, empty-ish JSON object/array-friendly body: every
			// Cmd factory in the table above either ignores decode errors
			// for a "safe empty" fallback or only requires a run_id/models/
			// providers/etc field that is fine to be absent (zero value).
			_, _ = w.Write([]byte(`{"run_id":"run-echo","runs":[],"models":[],"providers":[],"profiles":[],"subagents":[],"messages":[],"questions":[]}`))
		}
	}))
}

// TestAllHarnessdCallsAuthenticate is the table-driven test the coordinator
// asked for: every harnessd-targeting call in cmd/harnesscli/tui sends
// "Authorization: Bearer <key>" when a key is configured, and sends no
// Authorization header at all when it is not — preserving today's
// unauthenticated-local default.
func TestAllHarnessdCallsAuthenticate(t *testing.T) {
	for _, tc := range harnessAuthCases() {
		tc := tc
		t.Run(tc.name+"/with_key", func(t *testing.T) {
			t.Parallel()
			var gotAuth string
			ts := newAuthEchoServer(t, &gotAuth)
			defer ts.Close()

			tc.call(ts, "secret-harness-key")

			if gotAuth != "Bearer secret-harness-key" {
				t.Errorf("%s: Authorization header = %q, want %q", tc.name, gotAuth, "Bearer secret-harness-key")
			}
		})
		t.Run(tc.name+"/without_key", func(t *testing.T) {
			t.Parallel()
			var gotAuth string
			ts := newAuthEchoServer(t, &gotAuth)
			defer ts.Close()

			tc.call(ts, "")

			if gotAuth != "" {
				t.Errorf("%s: Authorization header = %q, want empty (no key configured)", tc.name, gotAuth)
			}
		})
	}
}

// TestOpenRouterCallDoesNotReceiveHarnessKey is the explicit isolation test
// the coordinator asked for: fetchOpenRouterModelsFromURL must send its own
// OpenRouter provider key, and must never be handed (or leak) the harnessd
// auth key. There is no code path that could pass TUIConfig.APIKey into it
// today (fetchOpenRouterModelsCmd only ever takes m.pendingAPIKeys["openrouter"]
// — see model.go's executeModelCommand) — this test pins that contract so a
// future refactor cannot accidentally wire the harnessd key into it.
func TestOpenRouterCallDoesNotReceiveHarnessKey(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer ts.Close()

	// Simulate the harnessd key being configured (as it would be for every
	// other call in this file) alongside a distinct OpenRouter provider key.
	// Only the OpenRouter key may ever appear on this request.
	const harnessKey = "harnessd-secret-must-not-leak"
	const openRouterKey = "or-provider-key"

	msg := fetchOpenRouterModelsFromURL(ts.URL, openRouterKey)()
	if _, ok := msg.(ModelsFetchedMsg); !ok {
		t.Fatalf("expected ModelsFetchedMsg, got %T: %+v", msg, msg)
	}

	if gotAuth != "Bearer "+openRouterKey {
		t.Errorf("OpenRouter request Authorization = %q, want %q (its own provider key)", gotAuth, "Bearer "+openRouterKey)
	}
	if gotAuth == "Bearer "+harnessKey {
		t.Fatal("the harnessd API key leaked to the external OpenRouter request")
	}
}

// ---------------------------------------------------------------------------
// Regression: statically prevents a future endpoint from bypassing
// newHarnessRequest, which is the actual mechanism ("route every harnessd
// call through ONE helper... that is what stops the next endpoint from
// being added without auth") this task was asked to establish. A
// table-driven header test only catches a gap once someone writes a test
// case for it; this test catches the gap the moment a raw http.Get/
// http.Post/http.NewRequest call is added anywhere in these files, with no
// new test case required.
// ---------------------------------------------------------------------------

// rawHTTPCallAllowlist lists the functions permitted to build an HTTP
// request without going through newHarnessRequest, and why:
//   - newHarnessRequest itself is the one place http.NewRequestWithContext
//     is allowed to appear directly.
//   - fetchOpenRouterModelsFromURL targets the external openrouter.ai API,
//     not harnessd, and must keep using its own provider-key header logic.
var rawHTTPCallAllowlist = map[string]bool{
	"newHarnessRequest":            true,
	"fetchOpenRouterModelsFromURL": true,
}

// rawHTTPCallNames are the net/http package-level functions that build (and
// in the Get/Post/Head/PostForm cases, also send) a request without going
// through a caller-supplied *http.Request — exactly what newHarnessRequest
// exists to replace so an Authorization header can be attached.
var rawHTTPCallNames = map[string]bool{
	"Get":        true,
	"Head":       true,
	"Post":       true,
	"PostForm":   true,
	"NewRequest": true, // NewRequestWithContext is fine; bare NewRequest has no ctx and, more importantly, callers historically forgot to attach auth to it too.
}

// TestRegression_AllHarnessdRequestsRouteThroughNewHarnessRequest statically
// scans api.go, askuser.go, and approval.go for any function OTHER than the
// allowlisted ones that calls a raw net/http request-building function. It
// would fail immediately if a future PR added a new harnessd endpoint using
// http.Get/http.Post/http.NewRequest directly instead of newHarnessRequest —
// exactly the class of gap that caused this task (a dozen unauthenticated
// calls that accumulated because nothing enforced the pattern).
func TestRegression_AllHarnessdRequestsRouteThroughNewHarnessRequest(t *testing.T) {
	files := []string{"api.go", "askuser.go", "approval.go"}

	for _, file := range files {
		file := file
		t.Run(file, func(t *testing.T) {
			fset := token.NewFileSet()
			astFile, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}

			for _, decl := range astFile.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if rawHTTPCallAllowlist[fn.Name.Name] {
					continue
				}

				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkgIdent, ok := sel.X.(*ast.Ident)
					if !ok || pkgIdent.Name != "http" {
						return true
					}
					if rawHTTPCallNames[sel.Sel.Name] {
						pos := fset.Position(call.Pos())
						t.Errorf(
							"%s:%d: func %s calls http.%s directly instead of routing through newHarnessRequest — this bypasses harnessd authentication entirely",
							file, pos.Line, fn.Name.Name, sel.Sel.Name,
						)
					}
					return true
				})
			}
		})
	}
}
