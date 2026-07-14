package modelswitcher_test

// Additional regression coverage for BUG B: guards a detail the primary
// drift test (bugB_catalog_sync_test.go) does not check, since catalog/
// models.json has no dedicated "reasoning effort" field to compare against.
// ModelEntry.ReasoningMode is set by hand in DefaultModels and by
// reasoningModelIDs when enriching server-fetched entries; hand-editing
// DefaultModels to sync display names/new models (as the BUG B fix did) is
// exactly the kind of change that could accidentally drop a ReasoningMode:
// true flag on an existing entry. This test would fail if that happened.

import (
	"testing"

	"go-agent-harness/cmd/harnesscli/tui/components/modelswitcher"
)

// TestBugB_Regression_ReasoningModelsRetainFlagAfterSync verifies that the
// known reasoning-capable models still have ReasoningMode == true in
// DefaultModels after the catalog sync.
func TestBugB_Regression_ReasoningModelsRetainFlagAfterSync(t *testing.T) {
	wantReasoning := map[string]bool{
		"deepseek-reasoner":       true,
		"grok-4-1-fast-reasoning": true,
		"qwen-qwq-32b":            true,
	}

	found := make(map[string]bool)
	for _, dm := range modelswitcher.DefaultModels {
		if want, ok := wantReasoning[dm.ID]; ok {
			found[dm.ID] = true
			if dm.ReasoningMode != want {
				t.Errorf("DefaultModels[%q].ReasoningMode = %v, want %v", dm.ID, dm.ReasoningMode, want)
			}
		} else if dm.ReasoningMode {
			t.Errorf("DefaultModels[%q].ReasoningMode = true unexpectedly (not in the known reasoning-model set)", dm.ID)
		}
	}
	for id := range wantReasoning {
		if !found[id] {
			t.Errorf("expected reasoning model %q not found in DefaultModels at all", id)
		}
	}
}
