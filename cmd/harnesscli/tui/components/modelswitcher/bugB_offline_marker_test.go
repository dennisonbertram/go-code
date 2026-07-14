package modelswitcher_test

// Second half of BUG B (P2): when the live GET /v1/models fetch fails,
// ModelsFetchErrorMsg only set a loadError string. At browseLevel==0 and in
// the flat search view, an error replaces the entire list — that already
// makes the failure obvious. But at browseLevel==1 (drilled into a specific
// provider) view.go's viewModelsForProvider never looked at loadError at
// all, so a user who had already drilled in (or drills in after the error
// arrives) kept browsing the stale DefaultModels list with zero indication
// it wasn't live data.

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// TestBugB_Level1ShowsOfflineMarkerOnLoadError verifies that once a fetch
// error is recorded, browsing a provider's model list (level 1) still works
// (the fallback list remains usable) but is visibly marked as offline/stale
// so it can't be mistaken for freshly fetched data.
func TestBugB_Level1ShowsOfflineMarkerOnLoadError(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()

	provs := m.Providers()
	found := false
	for i := range provs {
		if provs[i].Label == "OpenAI" {
			for m.ProviderCursorIndex() != i {
				m = m.ProviderDown()
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("test setup: OpenAI provider not found")
	}
	m = m.DrillIntoProvider()
	m = m.SetLoadError("Error loading models: connection refused")

	v := m.View(80)

	// The fallback list must still be usable/browsable.
	if !strings.Contains(v, "GPT-4.1") {
		t.Errorf("level-1 view should still show the fallback model list when a background fetch fails:\n%s", v)
	}
	// ...but must be visibly marked as offline/stale so it is never
	// indistinguishable from a successful live fetch.
	if !strings.Contains(v, "offline") {
		t.Errorf("level-1 view should visibly mark the list as an offline/stale fallback on fetch error:\n%s", v)
	}
}

// TestBugB_Level1NoOfflineMarkerWhenNoError is the inverse regression check:
// when there is no load error, the offline marker must not appear (it would
// be misleading noise on a successful live fetch).
func TestBugB_Level1NoOfflineMarkerWhenNoError(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	provs := m.Providers()
	for i := range provs {
		if provs[i].Label == "OpenAI" {
			for m.ProviderCursorIndex() != i {
				m = m.ProviderDown()
			}
			break
		}
	}
	m = m.DrillIntoProvider()

	v := m.View(80)
	if strings.Contains(v, "offline") {
		t.Errorf("level-1 view should not show an offline marker when there is no load error:\n%s", v)
	}
}
