package systemprompt

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func makePromptFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFixtureFile(t, root, "catalog.yaml", `version: 1
defaults:
  intent: general
  model_profile: default
intents:
  general: intents/general.md
  code_review: intents/code_review.md
  frontend_design: intents/frontend_design.md
model_profiles:
  - name: openai_gpt5
    match: gpt-5-*
    file: models/openai_gpt5.md
  - name: autoresearch
    match: autoresearch-*
    file: models/autoresearch.md
  - name: default
    match: "*"
    file: models/default.md
extensions:
  behaviors_dir: extensions/behaviors
  talents_dir: extensions/talents
`)

	writeFixtureFile(t, root, "base/main.md", "BASE_PROMPT")
	writeFixtureFile(t, root, "intents/general.md", "INTENT_GENERAL")
	writeFixtureFile(t, root, "intents/code_review.md", "INTENT_CODE_REVIEW")
	writeFixtureFile(t, root, "intents/frontend_design.md", "INTENT_FRONTEND_DESIGN")
	writeFixtureFile(t, root, "models/default.md", "MODEL_DEFAULT")
	writeFixtureFile(t, root, "models/openai_gpt5.md", "MODEL_GPT5")
	writeFixtureFile(t, root, "models/autoresearch.md", "MODEL_AUTORESEARCH")
	writeFixtureFile(t, root, "extensions/behaviors/precise.md", "BEHAVIOR_PRECISE")
	writeFixtureFile(t, root, "extensions/behaviors/safe.md", "BEHAVIOR_SAFE")
	writeFixtureFile(t, root, "extensions/talents/ui.md", "TALENT_UI")
	writeFixtureFile(t, root, "extensions/talents/review.md", "TALENT_REVIEW")

	return root
}
