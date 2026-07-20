package observationalmemory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type modelStub struct {
	out string
}

func (m modelStub) Complete(context.Context, ModelRequest) (string, error) {
	return m.out, nil
}

func TestServiceObserveSnippetAndExport(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc, err := NewService(ServiceOptions{
		Mode:           ModeLocalCoordinator,
		Store:          store,
		Coordinator:    NewLocalCoordinator(),
		Observer:       ModelObserver{Model: modelStub{out: "Observed: user wants concise updates."}},
		Reflector:      ModelReflector{Model: modelStub{out: "Reflection: concise updates preferred."}},
		Estimator:      RuneTokenEstimator{},
		DefaultEnabled: false,
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       500,
			ReflectThresholdTokens: 2,
		},
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}
	status, err := svc.SetEnabled(context.Background(), scope, true, nil, "run_1", "call_1")
	if err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if !status.Enabled {
		t.Fatalf("expected enabled status")
	}

	out, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "run_1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Please keep responses concise and technical."},
			{Index: 1, Role: "assistant", Content: "Acknowledged."},
		},
	})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !out.Observed {
		t.Fatalf("expected observed true")
	}

	snippet, status, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if !strings.Contains(snippet, "<observational-memory>") {
		t.Fatalf("expected snippet tags, got %q", snippet)
	}
	if status.ObservationCount == 0 {
		t.Fatalf("expected observations in status")
	}

	exported, err := svc.Export(context.Background(), scope, "json")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if exported.Bytes == 0 || !strings.Contains(exported.Content, "observations") {
		t.Fatalf("unexpected export payload: %q", exported.Content)
	}
}

func TestDisabledManager(t *testing.T) {
	t.Parallel()

	mgr := NewDisabledManager(ModeOff)
	scope := ScopeKey{TenantID: "default", ConversationID: "run_1", AgentID: "default"}
	status, err := mgr.Status(context.Background(), scope)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Enabled {
		t.Fatalf("expected disabled manager status")
	}
	if _, err := mgr.SetEnabled(context.Background(), scope, true, nil, "run_1", "call_1"); err == nil {
		t.Fatalf("expected set enabled error")
	}
}

func TestServiceStatusModeAndReflectNow(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc, err := NewService(ServiceOptions{
		Mode:        ModeAuto,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    ModelObserver{Model: modelStub{out: "Observed: keep code style stable."}},
		Reflector:   ModelReflector{Model: modelStub{out: "Reflection: keep code style stable."}},
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       256,
			ReflectThresholdTokens: 10_000,
		},
		DefaultEnabled: false,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "tenant", ConversationID: "conversation", AgentID: "agent"}
	if svc.Mode() != ModeLocalCoordinator {
		t.Fatalf("unexpected mode: %q", svc.Mode())
	}

	initial, err := svc.Status(context.Background(), scope)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if initial.Enabled {
		t.Fatalf("expected disabled by default")
	}

	if _, err := svc.SetEnabled(context.Background(), scope, true, nil, "run_1", "call_1"); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "run_1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Please keep responses concise and technical."},
		},
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	status, err := svc.ReflectNow(context.Background(), scope, "run_1", "call_2")
	if err != nil {
		t.Fatalf("reflect now: %v", err)
	}
	if !status.ReflectionPresent {
		t.Fatalf("expected reflection to be present")
	}
}

func TestServiceReflectNowFailsWithoutReflector(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    ModelObserver{Model: modelStub{out: "Observed: stable constraints."}},
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       256,
			ReflectThresholdTokens: 10_000,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "tenant", ConversationID: "conversation", AgentID: "agent"}
	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "run_1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Capture one observation first."},
		},
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	_, err = svc.ReflectNow(context.Background(), scope, "run_1", "call_1")
	if err == nil || !strings.Contains(err.Error(), "reflector is not configured") {
		t.Fatalf("expected reflector missing error, got: %v", err)
	}
}

func TestDisabledManagerAllMethods(t *testing.T) {
	t.Parallel()

	mgr := NewDisabledManager("")
	scope := ScopeKey{TenantID: "tenant", ConversationID: "conversation", AgentID: "agent"}

	if mgr.Mode() != ModeOff {
		t.Fatalf("expected mode off, got %q", mgr.Mode())
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := mgr.Observe(context.Background(), ObserveRequest{Scope: scope}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if snippet, _, err := mgr.Snippet(context.Background(), scope); err != nil || snippet != "" {
		t.Fatalf("snippet: %q err=%v", snippet, err)
	}
	if _, err := mgr.ReflectNow(context.Background(), scope, "run_1", "call_1"); err == nil {
		t.Fatalf("expected reflect now to fail for disabled manager")
	}
	if _, err := mgr.Export(context.Background(), scope, "json"); err == nil {
		t.Fatalf("expected export to fail for disabled manager")
	}
}

// TestSnippetImportanceWeightedSelection verifies that a high-importance old
// chunk beats a low-importance new chunk in Snippet() selection.
func TestSnippetImportanceWeightedSelection(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Observer returns two chunks: old high-importance, new low-importance.
	// The snippet budget is tight — only one chunk fits.
	// High-importance old chunk should win.
	observerCalls := 0
	obs := observerFunc(func(_ context.Context, _ ScopeKey, _ Config, _ []TranscriptMessage, _ []ObservationChunk, _ string) (string, error) {
		observerCalls++
		if observerCalls == 1 {
			// First call: one high-importance chunk (~10 tokens).
			return "IMPORTANCE:0.9\nCritical: never auto-commit on behalf of the user.", nil
		}
		// Second call: one low-importance chunk (~10 tokens).
		return "IMPORTANCE:0.1\nUser is currently looking at the auth module.", nil
	})

	// Budget allows exactly one small chunk but not two.
	// RuneTokenEstimator: runes/4. Each ~50-char chunk ≈ 13 tokens.
	// Budget of 15 fits one chunk but not two (~26 total).
	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    obs,
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       15, // tight: fits one ~13-token chunk but not two
			ReflectThresholdTokens: 100000,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}

	// Observe first batch (produces high-importance chunk).
	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Never auto-commit on my behalf."},
		},
	}); err != nil {
		t.Fatalf("observe 1: %v", err)
	}

	// Observe second batch (produces low-importance chunk).
	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r2",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Never auto-commit on my behalf."},
			{Index: 1, Role: "assistant", Content: "Understood."},
			{Index: 2, Role: "user", Content: "I am looking at the auth module now."},
		},
	}); err != nil {
		t.Fatalf("observe 2: %v", err)
	}

	snippet, _, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if !strings.Contains(snippet, "never auto-commit") && !strings.Contains(snippet, "Critical") && !strings.Contains(snippet, "auto-commit") {
		t.Fatalf("expected high-importance chunk in snippet, got: %q", snippet)
	}
	if strings.Contains(snippet, "auth module") {
		t.Fatalf("expected low-importance chunk to be excluded from tight-budget snippet, got: %q", snippet)
	}
}

// TestSnippetUnscoredChunksTreatedAsNeutral verifies that chunks with
// Importance==0.0 (legacy/unscored) are treated as Importance=0.5 during
// selection and are not penalized relative to other unscored chunks.
func TestSnippetUnscoredChunksTreatedAsNeutral(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Observer returns unscored output (no IMPORTANCE: prefix) — legacy behavior.
	obs := observerFunc(func(_ context.Context, _ ScopeKey, _ Config, _ []TranscriptMessage, _ []ObservationChunk, _ string) (string, error) {
		return "User prefers short responses.", nil
	})

	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    obs,
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       500,
			ReflectThresholdTokens: 100000,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}
	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Please keep responses short."},
		},
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}

	snippet, status, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if status.ObservationCount == 0 {
		t.Fatalf("expected observation to be stored")
	}
	if !strings.Contains(snippet, "User prefers short responses") {
		t.Fatalf("expected unscored chunk to appear in snippet, got: %q", snippet)
	}
}

// TestSnippetTokenBudgetRespectedUnderImportanceWeighting verifies that the
// token budget is respected even when importance-weighted selection is active.
func TestSnippetTokenBudgetRespectedUnderImportanceWeighting(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	callCount := 0
	obs := observerFunc(func(_ context.Context, _ ScopeKey, _ Config, _ []TranscriptMessage, _ []ObservationChunk, _ string) (string, error) {
		callCount++
		switch callCount {
		case 1:
			return "IMPORTANCE:0.9\nChunk one high importance long content aaaaaaaaaaaaaaaaaaaaaaaaaaa.", nil
		case 2:
			return "IMPORTANCE:0.9\nChunk two high importance long content bbbbbbbbbbbbbbbbbbbbbbbbbbb.", nil
		default:
			return "IMPORTANCE:0.9\nChunk three high importance long content cccccccccccccccccccccccccc.", nil
		}
	})

	// Very tight budget: 50 tokens. Each chunk is ~15 tokens so only 3 fit if budget is generous,
	// but we set it very tight so ideally 1-2 fit.
	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    obs,
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       30, // very tight
			ReflectThresholdTokens: 100000,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}
	for i := 0; i < 3; i++ {
		msgs := make([]TranscriptMessage, i+2)
		for j := range msgs {
			msgs[j] = TranscriptMessage{Index: int64(j), Role: "user", Content: "Some content to observe round " + string(rune('A'+i))}
		}
		if _, err := svc.Observe(context.Background(), ObserveRequest{
			Scope:    scope,
			RunID:    "r1",
			Messages: msgs,
		}); err != nil {
			t.Fatalf("observe %d: %v", i, err)
		}
	}

	snippet, _, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}

	// Estimate tokens in snippet — must not exceed budget + small overhead for formatting.
	estimator := RuneTokenEstimator{}
	snippetTokens := estimator.EstimateTextTokens(snippet)
	// Allow some overhead for the XML tags and "Observations:" label.
	maxAllowed := 30 + 20
	if snippetTokens > maxAllowed {
		t.Fatalf("snippet token count %d exceeds budget %d (snippet=%q)", snippetTokens, maxAllowed, snippet)
	}
}

// observerFunc is a function adapter implementing Observer.
type observerFunc func(ctx context.Context, scope ScopeKey, cfg Config, unobserved []TranscriptMessage, existing []ObservationChunk, reflection string) (string, error)

func (f observerFunc) Observe(ctx context.Context, scope ScopeKey, cfg Config, unobserved []TranscriptMessage, existing []ObservationChunk, reflection string) (string, error) {
	return f(ctx, scope, cfg, unobserved, existing, reflection)
}

func TestMergeConfig(t *testing.T) {
	t.Parallel()

	current := Config{
		ObserveMinTokens:       10,
		SnippetMaxTokens:       20,
		ReflectThresholdTokens: 30,
	}
	merged := mergeConfig(current, Config{
		ObserveMinTokens:       15,
		SnippetMaxTokens:       0,
		ReflectThresholdTokens: 45,
	})
	if merged.ObserveMinTokens != 15 {
		t.Fatalf("expected observe_min_tokens 15, got %d", merged.ObserveMinTokens)
	}
	if merged.SnippetMaxTokens != 20 {
		t.Fatalf("expected snippet_max_tokens unchanged at 20, got %d", merged.SnippetMaxTokens)
	}
	if merged.ReflectThresholdTokens != 45 {
		t.Fatalf("expected reflect_threshold_tokens 45, got %d", merged.ReflectThresholdTokens)
	}
}

// TestLegacyReflectionParsesWithoutError verifies that a legacy plain-text
// reflection (no SUMMARY: header) is parsed gracefully as SchemaVersion=0
// and does not cause any errors or panics in Snippet().
func TestLegacyReflectionParsesWithoutError(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Reflector returns legacy plain text (no SUMMARY: header).
	legacyReflection := "User prefers concise updates. Never auto-commit."
	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    ModelObserver{Model: modelStub{out: "IMPORTANCE:0.7\nUser prefers concise updates."}},
		Reflector:   ModelReflector{Model: modelStub{out: legacyReflection}},
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       500,
			ReflectThresholdTokens: 2,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}
	result, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Keep responses concise."},
		},
	})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !result.Reflected {
		t.Fatalf("expected reflection to be triggered")
	}

	snippet, _, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if !strings.Contains(snippet, "<observational-memory>") {
		t.Fatalf("expected snippet tags, got: %q", snippet)
	}
	// Legacy reflection text should appear in the snippet.
	if !strings.Contains(snippet, "User prefers concise updates") && !strings.Contains(snippet, "Never auto-commit") {
		t.Fatalf("expected legacy reflection text in snippet, got: %q", snippet)
	}
	// No warning sections should appear for a legacy (SchemaVersion=0) reflection.
	if strings.Contains(snippet, "Preference changes") || strings.Contains(snippet, "contradictions") {
		t.Fatalf("unexpected warning sections in legacy snippet: %q", snippet)
	}
}

// TestSupersededChunkIsDemotedInSnippet verifies that a chunk identified as
// superseded by the structured reflection is demoted in Snippet() selection.
// When both chunks would normally have equal importance, the superseded one
// gets a lower effective importance (0.1), so the non-superseded chunk wins
// when the token budget allows only one.
func TestSupersededChunkIsDemotedInSnippet(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	observerCalls := 0
	obs := observerFunc(func(_ context.Context, _ ScopeKey, _ Config, _ []TranscriptMessage, _ []ObservationChunk, _ string) (string, error) {
		observerCalls++
		if observerCalls == 1 {
			// Old preference chunk: ~11 tokens via RuneTokenEstimator (44 chars/4).
			return "IMPORTANCE:0.8\nUse tabs.", nil
		}
		// New preference chunk: ~11 tokens.
		return "IMPORTANCE:0.8\nUse spaces.", nil
	})

	// Reflector returns structured output indicating seq:1 is superseded by seq:2.
	// The summary is short to leave room in the token budget for one observation.
	structuredReflection := "SUMMARY:\nSpaces now.\n\nSUPERSESSIONS:\n- [seq:2] replaces [seq:1]: user changed from tabs to spaces\n\nCONTRADICTIONS:\n"

	// Budget: summary "Spaces now." is ~3 tokens. Two chunks ("Use tabs." and
	// "Use spaces.") are each ~3 tokens. The supersession warning adds tokens.
	// We set SnippetMaxTokens to allow the summary + one chunk but not two, so
	// demotion of seq:1 determines which chunk survives.
	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    obs,
		Reflector:   ModelReflector{Model: modelStub{out: structuredReflection}},
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       7, // fits summary (~3) + one 3-token chunk, not two
			ReflectThresholdTokens: 2,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}

	// First observe: produces chunk seq=1 (old preference: "Use tabs.").
	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Use tabs for indentation."},
		},
	}); err != nil {
		t.Fatalf("observe 1: %v", err)
	}

	// Second observe: produces chunk seq=2 (new preference: "Use spaces.") and triggers reflection.
	result, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r2",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Use tabs for indentation."},
			{Index: 1, Role: "user", Content: "Actually, use spaces instead."},
		},
	})
	if err != nil {
		t.Fatalf("observe 2: %v", err)
	}
	if !result.Reflected {
		t.Fatalf("expected reflection to be triggered on second observe")
	}

	snippet, _, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}

	// The supersession warning must appear.
	if !strings.Contains(snippet, "Preference changes") {
		t.Fatalf("expected supersession warning in snippet, got: %q", snippet)
	}

	// The superseded chunk (seq:1, "Use tabs.") must NOT appear in observations
	// when both chunks cannot fit — demotion must deprioritise it.
	if strings.Contains(snippet, "Use tabs") {
		t.Fatalf("expected superseded chunk (tabs) to be excluded from tight-budget snippet, got: %q", snippet)
	}
}

// TestContradictionAppearsAsWarningInSnippet verifies that contradictions
// detected by the structured reflection appear as a warning section in Snippet().
func TestContradictionAppearsAsWarningInSnippet(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	observerCalls := 0
	obs := observerFunc(func(_ context.Context, _ ScopeKey, _ Config, _ []TranscriptMessage, _ []ObservationChunk, _ string) (string, error) {
		observerCalls++
		if observerCalls == 1 {
			return "IMPORTANCE:0.7\nRetry count should be 3.", nil
		}
		return "IMPORTANCE:0.7\nRetry count should be 5.", nil
	})

	// Reflector returns structured output with a contradiction between seq:1 and seq:2.
	structuredReflection := "SUMMARY:\nConflicting guidance on retry count.\n\nSUPERSESSIONS:\n\nCONTRADICTIONS:\n- [seq:1] vs [seq:2]: conflicting retry count (3 vs 5)\n"

	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    obs,
		Reflector:   ModelReflector{Model: modelStub{out: structuredReflection}},
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       500,
			ReflectThresholdTokens: 2,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}

	if _, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Retry count is 3."},
		},
	}); err != nil {
		t.Fatalf("observe 1: %v", err)
	}

	result, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r2",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Retry count is 3."},
			{Index: 1, Role: "user", Content: "Actually retry count should be 5."},
		},
	})
	if err != nil {
		t.Fatalf("observe 2: %v", err)
	}
	if !result.Reflected {
		t.Fatalf("expected reflection to be triggered")
	}

	snippet, _, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}

	if !strings.Contains(snippet, "contradictions") && !strings.Contains(snippet, "Unresolved") {
		t.Fatalf("expected contradiction warning in snippet, got: %q", snippet)
	}
	if !strings.Contains(snippet, "retry") && !strings.Contains(snippet, "conflicting") {
		t.Fatalf("expected contradiction detail in snippet, got: %q", snippet)
	}
}

// TestStructuredReflectionNoSupersessionsNoContradictions verifies that a
// structured reflection with empty SUPERSESSIONS and CONTRADICTIONS sections
// works normally without producing spurious warning blocks in Snippet().
func TestStructuredReflectionNoSupersessionsNoContradictions(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cleanReflection := "SUMMARY:\nUser wants concise responses and prefers Go idioms.\n\nSUPERSESSIONS:\n\nCONTRADICTIONS:\n"

	svc, err := NewService(ServiceOptions{
		Mode:        ModeLocalCoordinator,
		Store:       store,
		Coordinator: NewLocalCoordinator(),
		Observer:    ModelObserver{Model: modelStub{out: "IMPORTANCE:0.8\nUser wants concise responses."}},
		Reflector:   ModelReflector{Model: modelStub{out: cleanReflection}},
		Estimator:   RuneTokenEstimator{},
		DefaultConfig: Config{
			ObserveMinTokens:       1,
			SnippetMaxTokens:       500,
			ReflectThresholdTokens: 2,
		},
		DefaultEnabled: true,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	scope := ScopeKey{TenantID: "t", ConversationID: "c", AgentID: "a"}
	result, err := svc.Observe(context.Background(), ObserveRequest{
		Scope: scope,
		RunID: "r1",
		Messages: []TranscriptMessage{
			{Index: 0, Role: "user", Content: "Keep responses concise."},
		},
	})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !result.Reflected {
		t.Fatalf("expected reflection to be triggered")
	}

	snippet, _, err := svc.Snippet(context.Background(), scope)
	if err != nil {
		t.Fatalf("snippet: %v", err)
	}
	if !strings.Contains(snippet, "<observational-memory>") {
		t.Fatalf("expected snippet tags, got: %q", snippet)
	}
	// Should show the summary from the structured reflection.
	if !strings.Contains(snippet, "concise responses") && !strings.Contains(snippet, "Go idioms") {
		t.Fatalf("expected summary content in snippet, got: %q", snippet)
	}
	// No warning sections should appear.
	if strings.Contains(snippet, "Preference changes") || strings.Contains(snippet, "Unresolved") {
		t.Fatalf("unexpected warning sections in clean-reflection snippet: %q", snippet)
	}
}
