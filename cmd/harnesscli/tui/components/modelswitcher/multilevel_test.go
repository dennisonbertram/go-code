package modelswitcher_test

import (
	"strings"
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// TestProviders_ReturnsUniqueProviders verifies providers() returns unique, alphabetically
// sorted providers from the model list.
func TestProviders_ReturnsUniqueProviders(t *testing.T) {
	m := modelswitcher.New("gpt-4.1")
	provs := m.Providers()

	if len(provs) == 0 {
		t.Fatal("Providers() should return non-empty list")
	}

	// Verify uniqueness.
	seen := make(map[string]bool)
	for _, p := range provs {
		if seen[p.Label] {
			t.Errorf("Duplicate provider label %q in Providers()", p.Label)
		}
		seen[p.Label] = true
	}

	// Verify alphabetical order.
	for i := 1; i < len(provs); i++ {
		if provs[i].Label < provs[i-1].Label {
			t.Errorf("Providers() not sorted: %q before %q", provs[i-1].Label, provs[i].Label)
		}
	}

	// Verify known providers are present.
	wantLabels := []string{"Anthropic", "DeepSeek", "Google", "Groq", "Kimi", "OpenAI", "Qwen", "xAI"}
	for _, want := range wantLabels {
		if !seen[want] {
			t.Errorf("Providers() missing expected label %q", want)
		}
	}
}

// TestProviders_CountsCorrect verifies the Count field matches the number of models
// for each provider.
func TestProviders_CountsCorrect(t *testing.T) {
	m := modelswitcher.New("gpt-4.1")
	provs := m.Providers()

	// Build expected counts from DefaultModels.
	expected := make(map[string]int)
	for _, dm := range modelswitcher.DefaultModels {
		label := dm.ProviderLabel
		if label == "" {
			label = dm.Provider
		}
		expected[label]++
	}

	for _, p := range provs {
		want, ok := expected[p.Label]
		if !ok {
			t.Errorf("Providers() has unexpected label %q", p.Label)
			continue
		}
		if p.Count != want {
			t.Errorf("Provider %q Count = %d, want %d", p.Label, p.Count, want)
		}
	}
}

// TestProviders_HasCurrentFlagged verifies HasCurrent is true for the current model's provider.
func TestProviders_HasCurrentFlagged(t *testing.T) {
	// gpt-4.1 belongs to OpenAI.
	m := modelswitcher.New("gpt-4.1")
	provs := m.Providers()

	foundOpenAI := false
	for _, p := range provs {
		if p.Label == "OpenAI" {
			foundOpenAI = true
			if !p.HasCurrent {
				t.Error("OpenAI provider should have HasCurrent=true when gpt-4.1 is current")
			}
		} else {
			if p.HasCurrent {
				t.Errorf("Provider %q should not have HasCurrent=true when gpt-4.1 is current", p.Label)
			}
		}
	}
	if !foundOpenAI {
		t.Fatal("OpenAI provider not found in Providers()")
	}
}

// TestDrillIntoProvider_SetsBrowseLevel1 verifies DrillIntoProvider sets browseLevel=1
// and activeProvider to the selected provider's label.
func TestDrillIntoProvider_SetsBrowseLevel1(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	// Navigate to OpenAI provider.
	provs := m.Providers()
	for i := range provs {
		if provs[i].Label == "OpenAI" {
			for m.ProviderCursorIndex() != i {
				m = m.ProviderDown()
			}
			break
		}
	}
	m2 := m.DrillIntoProvider()

	if m2.BrowseLevel() != 1 {
		t.Errorf("DrillIntoProvider() should set BrowseLevel=1, got %d", m2.BrowseLevel())
	}
	if m2.ActiveProvider() != "OpenAI" {
		t.Errorf("DrillIntoProvider() should set ActiveProvider='OpenAI', got %q", m2.ActiveProvider())
	}
}

// TestDrillIntoProvider_FiltersModels verifies that after DrillIntoProvider, visibleModels
// (accessed via Accept/SelectDown) only returns models for that provider.
func TestDrillIntoProvider_FiltersModels(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	// Navigate to OpenAI and drill in.
	provs := m.Providers()
	for i := range provs {
		if provs[i].Label == "OpenAI" {
			for m.ProviderCursorIndex() != i {
				m = m.ProviderDown()
			}
			break
		}
	}
	m2 := m.DrillIntoProvider()

	// Navigate through all visible entries and verify they belong to OpenAI.
	entry, _ := m2.Accept()
	if entry.ProviderLabel != "OpenAI" {
		t.Errorf("After drill into OpenAI, Accept() returned provider %q, want OpenAI", entry.ProviderLabel)
	}

	// Iterate through all entries.
	current := m2
	for i := 0; i < 20; i++ {
		e, _ := current.Accept()
		if e.ProviderLabel != "OpenAI" {
			t.Errorf("At index %d after drill into OpenAI, Accept() provider=%q, want OpenAI", i, e.ProviderLabel)
		}
		next := current.SelectDown()
		// If we've wrapped back to start, stop.
		ne, _ := next.Accept()
		if ne.ID == entry.ID && i > 0 {
			break
		}
		current = next
	}
}

// TestExitToProviderList_ResetsBrowseLevel verifies ExitToProviderList sets browseLevel=0.
func TestExitToProviderList_ResetsBrowseLevel(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	// Drill into OpenAI.
	provs := m.Providers()
	for i := range provs {
		if provs[i].Label == "OpenAI" {
			for m.ProviderCursorIndex() != i {
				m = m.ProviderDown()
			}
			break
		}
	}
	m2 := m.DrillIntoProvider()

	if m2.BrowseLevel() != 1 {
		t.Fatal("Should be at level 1 after DrillIntoProvider")
	}

	m3 := m2.ExitToProviderList()
	if m3.BrowseLevel() != 0 {
		t.Errorf("ExitToProviderList() should set BrowseLevel=0, got %d", m3.BrowseLevel())
	}
	if m3.ActiveProvider() != "" {
		t.Errorf("ExitToProviderList() should clear ActiveProvider, got %q", m3.ActiveProvider())
	}
}

// TestSearchBypassesLevel0 verifies that when searchQuery != "", visibleModels returns
// cross-provider results (not filtered to a single provider).
func TestSearchBypassesLevel0(t *testing.T) {
	// At level 0 with a search query, the flat cross-provider list should show.
	m := modelswitcher.New("gpt-4.1").Open().SetSearch("e")

	// With search active, Accept() should return models from multiple providers.
	// "e" matches models from many providers (Claude, DeepSeek, Google, etc.)
	providers := make(map[string]bool)
	current := m
	for i := 0; i < 20; i++ {
		entry, _ := current.Accept()
		if entry.ID == "" {
			break
		}
		if entry.ProviderLabel != "" {
			providers[entry.ProviderLabel] = true
		}
		next := current.SelectDown()
		// Stop if we've looped back.
		ne, _ := next.Accept()
		e, _ := current.Accept()
		if ne.ID == e.ID {
			break
		}
		current = next
	}

	// We expect results from more than one provider (cross-provider search).
	if len(providers) <= 1 {
		t.Errorf("Search should return cross-provider results, got providers: %v", providers)
	}
}

// TestViewProviderList_RendersProviderNames verifies View() at browseLevel=0 contains
// all provider labels.
func TestViewProviderList_RendersProviderNames(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	v := m.View(80)

	provs := m.Providers()
	for _, p := range provs {
		if !strings.Contains(v, p.Label) {
			t.Errorf("View() at level 0 should contain provider label %q:\n%s", p.Label, v)
		}
	}
}

// TestViewProviderList_RendersModelCounts verifies View() at level 0 shows "(N)" counts.
func TestViewProviderList_RendersModelCounts(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	v := m.View(80)

	// Each provider should have a count shown in the view.
	// We just verify the format "(N)" appears for some reasonable count values.
	provs := m.Providers()
	for _, p := range provs {
		countStr := "(" + itoa(p.Count) + ")"
		if !strings.Contains(v, countStr) {
			t.Errorf("View() at level 0 should contain count %q for provider %q:\n%s", countStr, p.Label, v)
		}
	}
}

// TestViewModelList_ShowsBreadcrumb verifies View() at browseLevel=1 contains "< Back".
func TestViewModelList_ShowsBreadcrumb(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	// Drill into OpenAI.
	provs := m.Providers()
	for i := range provs {
		if provs[i].Label == "OpenAI" {
			for m.ProviderCursorIndex() != i {
				m = m.ProviderDown()
			}
			break
		}
	}
	m2 := m.DrillIntoProvider()
	v := m2.View(80)

	if !strings.Contains(v, "< Back") {
		t.Errorf("View() at level 1 should contain '< Back':\n%s", v)
	}
	if !strings.Contains(v, "OpenAI") {
		t.Errorf("View() at level 1 should contain provider name 'OpenAI' in breadcrumb:\n%s", v)
	}
}

// TestProviderUp_WrapsAround verifies ProviderUp() at top wraps to bottom.
func TestProviderUp_WrapsAround(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	// Start at cursor 0 (top).
	if m.ProviderCursorIndex() != 0 {
		// Move to top.
		provs := m.Providers()
		// Navigate down to bottom first, then up should wrap to top.
		_ = provs
	}
	// Move cursor to 0 explicitly by calling ProviderUp from 0 → should go to last.
	m2 := m
	// Ensure cursor is at 0.
	for m2.ProviderCursorIndex() != 0 {
		// Navigate up until we reach 0 or we've gone around.
		m2 = m2.ProviderUp()
		// Safety: if we've looped, stop.
		if m2.ProviderCursorIndex() == 0 {
			break
		}
	}
	// Now at index 0 — ProviderUp should wrap to last.
	provs := m2.Providers()
	lastIdx := len(provs) - 1
	m3 := m2.ProviderUp()
	if m3.ProviderCursorIndex() != lastIdx {
		t.Errorf("ProviderUp() at top (0) should wrap to last (%d), got %d", lastIdx, m3.ProviderCursorIndex())
	}
}

// TestProviderDown_WrapsAround verifies ProviderDown() at bottom wraps to top.
func TestProviderDown_WrapsAround(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	provs := m.Providers()
	lastIdx := len(provs) - 1

	// Navigate to last provider.
	m2 := m
	for m2.ProviderCursorIndex() != lastIdx {
		m2 = m2.ProviderDown()
		if m2.ProviderCursorIndex() == lastIdx {
			break
		}
	}
	// At last index — ProviderDown should wrap to 0.
	m3 := m2.ProviderDown()
	if m3.ProviderCursorIndex() != 0 {
		t.Errorf("ProviderDown() at bottom (%d) should wrap to 0, got %d", lastIdx, m3.ProviderCursorIndex())
	}
}

// TestViewProviderList_CountOnSameLineAsLabel verifies that provider model counts
// do not wrap onto separate lines at reasonable widths. Regression test for issue #571.
func TestViewProviderList_CountOnSameLineAsLabel(t *testing.T) {
	m := modelswitcher.New("gpt-4.1").Open()
	for _, w := range []int{30, 35, 40, 60, 80} {
		v := m.View(w)
		lines := strings.Split(v, "\n")
		for i, line := range lines {
			// A line whose visible content is solely a count "(N)" (with optional
			// indicator " ●"/" ○") indicates the count has wrapped onto its own line.
			// Strip ANSI escape sequences (reuse package-local stripANSI from availability_test.go).
			trimmed := stripANSI(strings.TrimSpace(line))
			// Detect lines that are just a count in parentheses, e.g., "(3)" or "(10) ●".
			if len(trimmed) > 0 && len(trimmed) <= 7 && trimmed[0] == '(' {
				t.Errorf("width=%d: line %d has provider count on its own line (wrapping bug): %q\nFull output:\n%s",
					w, i, trimmed, v)
			}
		}
	}
}

// itoa is a local helper to convert int to decimal string (same as the one in view.go).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
