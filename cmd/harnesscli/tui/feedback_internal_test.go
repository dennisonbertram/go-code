package tui

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	harnessconfig "go-agent-harness/cmd/harnesscli/config"
)

// ─── zip test helpers ─────────────────────────────────────────────────────────

func zipMemberNames(t *testing.T, path string) []string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer r.Close()
	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

func readZipMember(t *testing.T, path, name string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer r.Close()
	for _, f := range r.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open member %s: %v", name, err)
			}
			defer rc.Close()
			buf := new(strings.Builder)
			if _, err := io.Copy(buf, rc); err != nil {
				t.Fatalf("read member %s: %v", name, err)
			}
			return buf.String()
		}
	}
	t.Fatalf("member %s not found in %s", name, path)
	return ""
}

func writeRolloutFile(t *testing.T, dir, date, name, content string, mod time.Time) string {
	t.Helper()
	dateDir := filepath.Join(dir, date)
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dateDir, err)
	}
	p := filepath.Join(dateDir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
	return p
}

// ─── Bundle members ───────────────────────────────────────────────────────────

// TestBuildFeedbackBundle_ContainsExpectedMembers verifies the bundle holds
// version.json, config.json, and the newest rollout files under rollouts/,
// capped at five.
func TestBuildFeedbackBundle_ContainsExpectedMembers(t *testing.T) {
	t.Parallel()

	rolloutDir := t.TempDir()
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	// Seven files with strictly increasing modtimes; the two oldest (run-1,
	// run-2) must be excluded by the newest-5 cap.
	for i := 1; i <= 7; i++ {
		date := "2026-07-01"
		if i > 4 {
			date = "2026-07-02"
		}
		writeRolloutFile(t, rolloutDir, date, "run-"+string(rune('0'+i))+".jsonl", `{"event":"run.started"}`+"\n", base.Add(time.Duration(i)*time.Hour))
	}

	out := filepath.Join(t.TempDir(), "bundle.zip")
	err := buildFeedbackBundle(out, feedbackInput{
		CLIConfig:  &harnessconfig.Config{StarredModels: []string{"gpt-4o"}},
		RolloutDir: rolloutDir,
		BaseURL:    "http://localhost:8080",
		Model:      "gpt-4o",
	})
	if err != nil {
		t.Fatalf("buildFeedbackBundle: %v", err)
	}

	names := zipMemberNames(t, out)
	has := func(want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}
	if !has("version.json") {
		t.Errorf("bundle missing version.json: %v", names)
	}
	if !has("config.json") {
		t.Errorf("bundle missing config.json: %v", names)
	}
	rolloutCount := 0
	for _, n := range names {
		if strings.HasPrefix(n, "rollouts/") && strings.HasSuffix(n, ".jsonl") {
			rolloutCount++
		}
	}
	if rolloutCount != 5 {
		t.Errorf("bundle must contain the newest 5 rollout files, got %d: %v", rolloutCount, names)
	}
	if has("rollouts/2026-07-01/run-1.jsonl") || has("rollouts/2026-07-01/run-2.jsonl") {
		t.Errorf("oldest rollout files must be excluded by the cap: %v", names)
	}

	// version.json content.
	var info struct {
		HarnesscliVersion string   `json:"harnesscli_version"`
		GoVersion         string   `json:"go_version"`
		GOOS              string   `json:"goos"`
		GOARCH            string   `json:"goarch"`
		BaseURL           string   `json:"base_url"`
		Model             string   `json:"model"`
		Notes             []string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(readZipMember(t, out, "version.json")), &info); err != nil {
		t.Fatalf("version.json does not parse: %v", err)
	}
	if info.HarnesscliVersion != "unstamped" {
		t.Errorf("harnesscli_version = %q, want unstamped (no version stamp landed yet)", info.HarnesscliVersion)
	}
	if info.GoVersion != runtime.Version() || info.GOOS != runtime.GOOS || info.GOARCH != runtime.GOARCH {
		t.Errorf("runtime info wrong: %+v", info)
	}
	if info.BaseURL != "http://localhost:8080" || info.Model != "gpt-4o" {
		t.Errorf("base_url/model not carried through: %+v", info)
	}

	// config.json content.
	var cfg map[string]any
	if err := json.Unmarshal([]byte(readZipMember(t, out, "config.json")), &cfg); err != nil {
		t.Fatalf("config.json does not parse: %v", err)
	}
	if !strings.Contains(readZipMember(t, out, "config.json"), "gpt-4o") {
		t.Errorf("config.json should carry non-secret fields: %v", cfg)
	}
}

// ─── Redaction canaries ───────────────────────────────────────────────────────

// TestBuildFeedbackBundle_RedactsConfigSecrets is the canary table: no secret
// placed anywhere in the CLI config may survive into the bundled config.json.
func TestBuildFeedbackBundle_RedactsConfigSecrets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cfg    *harnessconfig.Config
		canary string
	}{
		{
			name:   "sk- API key in api_keys",
			cfg:    &harnessconfig.Config{APIKeys: map[string]string{"openai": "sk-testcanary1234567890abcdef"}},
			canary: "sk-testcanary1234567890abcdef",
		},
		{
			name:   "short non-pattern key caught by exact-value replace",
			cfg:    &harnessconfig.Config{APIKeys: map[string]string{"weird": "sh0rt"}},
			canary: "sh0rt",
		},
		{
			name:   "JWT in api_keys",
			cfg:    &harnessconfig.Config{APIKeys: map[string]string{"svc": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJVadQssw5c"}},
			canary: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJVadQssw5c",
		},
		{
			name:   "AWS access key id in history",
			cfg:    &harnessconfig.Config{HistoryEntries: []string{"aws configure set AKIAIOSFODNN7EXAMPLE"}},
			canary: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:   "postgres connection string in history",
			cfg:    &harnessconfig.Config{HistoryEntries: []string{"psql postgres://admin:hunter2@db.internal:5432/prod"}},
			canary: "postgres://admin:hunter2@db.internal:5432/prod",
		},
		{
			name:   "sk- key pasted into history",
			cfg:    &harnessconfig.Config{HistoryEntries: []string{"/keys openai sk-pastedcanary1234567890"}},
			canary: "sk-pastedcanary1234567890",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := filepath.Join(t.TempDir(), "bundle.zip")
			if err := buildFeedbackBundle(out, feedbackInput{CLIConfig: tc.cfg}); err != nil {
				t.Fatalf("buildFeedbackBundle: %v", err)
			}
			configJSON := readZipMember(t, out, "config.json")
			if strings.Contains(configJSON, tc.canary) {
				t.Errorf("canary secret survived into bundled config.json: %q\n%s", tc.canary, configJSON)
			}
			if !strings.Contains(configJSON, "[REDACTED") {
				t.Errorf("expected a redaction marker in bundled config.json: %s", configJSON)
			}
		})
	}
}

// TestBuildFeedbackBundle_RedactsRolloutContent verifies rollout files are
// redacted before bundling too.
func TestBuildFeedbackBundle_RedactsRolloutContent(t *testing.T) {
	t.Parallel()

	rolloutDir := t.TempDir()
	writeRolloutFile(t, rolloutDir, "2026-07-19", "run-x.jsonl",
		`{"event":"tool.result","output":"key was sk-rolloutcanary1234567890 ok"}`+"\n", time.Now())

	out := filepath.Join(t.TempDir(), "bundle.zip")
	if err := buildFeedbackBundle(out, feedbackInput{RolloutDir: rolloutDir}); err != nil {
		t.Fatalf("buildFeedbackBundle: %v", err)
	}
	bundled := readZipMember(t, out, "rollouts/2026-07-19/run-x.jsonl")
	if strings.Contains(bundled, "sk-rolloutcanary1234567890") {
		t.Errorf("rollout secret survived into the bundle:\n%s", bundled)
	}
}

// ─── Rollout dir absence ──────────────────────────────────────────────────────

// TestBuildFeedbackBundle_RolloutDirUnset verifies the bundle still builds
// when no rollout dir is configured, noting the absence.
func TestBuildFeedbackBundle_RolloutDirUnset(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "bundle.zip")
	if err := buildFeedbackBundle(out, feedbackInput{RolloutDir: ""}); err != nil {
		t.Fatalf("buildFeedbackBundle with unset rollout dir: %v", err)
	}

	names := zipMemberNames(t, out)
	foundMarker := false
	for _, n := range names {
		if strings.HasPrefix(n, "rollouts/") {
			foundMarker = true
		}
	}
	if !foundMarker {
		t.Fatalf("bundle must note the absent rollouts (rollouts/ marker member), got %v", names)
	}
	marker := readZipMember(t, out, "rollouts/NOT_PRESENT.txt")
	if !strings.Contains(marker, "rollout") {
		t.Errorf("absence marker should explain the missing rollouts, got: %q", marker)
	}

	var info struct {
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(readZipMember(t, out, "version.json")), &info); err != nil {
		t.Fatalf("version.json does not parse: %v", err)
	}
	joined := strings.Join(info.Notes, " ")
	if !strings.Contains(joined, "rollout") {
		t.Errorf("version.json notes should mention the rollout dir absence, got %v", info.Notes)
	}
}

// TestBuildFeedbackBundle_RolloutDirMissing verifies a configured-but-missing
// rollout dir degrades the same way.
func TestBuildFeedbackBundle_RolloutDirMissing(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "bundle.zip")
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	if err := buildFeedbackBundle(out, feedbackInput{RolloutDir: missing}); err != nil {
		t.Fatalf("buildFeedbackBundle with missing rollout dir: %v", err)
	}
	marker := readZipMember(t, out, "rollouts/NOT_PRESENT.txt")
	if !strings.Contains(marker, "rollout") {
		t.Errorf("absence marker should explain the missing rollouts, got: %q", marker)
	}
}
