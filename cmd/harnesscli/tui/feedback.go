package tui

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	harnessconfig "go-agent-harness/cmd/harnesscli/config"
	"go-agent-harness/internal/forensics/redaction"
)

// maxFeedbackRollouts caps how many of the newest rollout JSONL files go into
// a feedback bundle.
const maxFeedbackRollouts = 5

// feedbackInput carries everything buildFeedbackBundle needs; it is a plain
// value so the write path is testable without a TUI model.
type feedbackInput struct {
	// CLIConfig is the persistent harnesscli config (nil tolerated).
	CLIConfig *harnessconfig.Config
	// RolloutDir is the harness rollout directory ("" means not configured).
	RolloutDir string
	BaseURL    string
	Model      string
	// Version is the harnesscli build version stamp; "" means unstamped.
	Version string
	// Notes are extra human-readable caveats recorded in version.json.
	Notes []string
	// Now overrides the timestamp (zero → time.Now()).
	Now time.Time
}

// executeFeedbackCommand implements /feedback: it bundles recent rollout
// JSONL, the redacted CLI config, and version/runtime info into a zip under
// <config-dir>/feedback/ and prints the path. The bundle is local-only —
// nothing is uploaded; the user attaches it to a bug report manually.
func executeFeedbackCommand(m *Model, _ Command) ([]tea.Cmd, bool) {
	var notes []string
	cfg, err := harnessconfig.Load()
	if err != nil {
		cfg = nil
		notes = append(notes, "cli config unreadable: "+err.Error())
	}
	rolloutDir := strings.TrimSpace(os.Getenv("HARNESS_ROLLOUT_DIR"))

	outDir := filepath.Join(defaultSessionConfigDir(), "feedback")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return []tea.Cmd{m.setStatusMsg("Could not create feedback dir: " + err.Error())}, false
	}
	outPath := filepath.Join(outDir, "go-code-feedback-"+time.Now().Format("20060102-150405")+".zip")

	err = buildFeedbackBundle(outPath, feedbackInput{
		CLIConfig:  cfg,
		RolloutDir: rolloutDir,
		BaseURL:    m.config.BaseURL,
		Model:      m.selectedModel,
		Notes:      notes,
	})
	if err != nil {
		return []tea.Cmd{m.setStatusMsg("Could not write feedback bundle: " + err.Error())}, false
	}
	return []tea.Cmd{m.setStatusMsg("Feedback bundle written to " + outPath)}, false
}

// buildFeedbackBundle writes the diagnostics zip to outPath. The bundle never
// contains secrets: the CLI config passes through exact-value replacement of
// every stored api_keys value plus the forensics redaction patterns, and
// rollout files pass through the same redactor.
func buildFeedbackBundle(outPath string, in feedbackInput) error {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	redactor := redaction.NewRedactor(nil)
	notes := append([]string{}, in.Notes...)

	zf, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	zw := zip.NewWriter(zf)

	// version.json — version/runtime info plus caveats.
	version := in.Version
	if version == "" {
		version = "unstamped"
	}
	rolloutFiles, rolloutNote := collectFeedbackRollouts(in.RolloutDir, maxFeedbackRollouts)
	if rolloutNote != "" {
		notes = append(notes, rolloutNote)
	}
	info := map[string]any{
		"harnesscli_version": version,
		"go_version":         runtime.Version(),
		"goos":               runtime.GOOS,
		"goarch":             runtime.GOARCH,
		"base_url":           in.BaseURL,
		"model":              in.Model,
		"generated_at":       now.UTC().Format(time.RFC3339),
		"notes":              notes,
	}
	infoJSON, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		zw.Close()
		zf.Close()
		return fmt.Errorf("marshal version.json: %w", err)
	}
	if err := writeZipMember(zw, "version.json", infoJSON); err != nil {
		zw.Close()
		zf.Close()
		return err
	}

	// config.json — redacted CLI config.
	if err := writeZipMember(zw, "config.json", redactCLIConfigJSON(in.CLIConfig, redactor)); err != nil {
		zw.Close()
		zf.Close()
		return err
	}

	// rollouts/ — newest rollout files, redacted; absence marker otherwise.
	if len(rolloutFiles) == 0 {
		marker := "no rollout files included: " + rolloutNote + "\n"
		if rolloutNote == "" {
			marker = "no rollout files included\n"
		}
		if err := writeZipMember(zw, "rollouts/NOT_PRESENT.txt", []byte(marker)); err != nil {
			zw.Close()
			zf.Close()
			return err
		}
	}
	for _, rf := range rolloutFiles {
		if err := writeZipMember(zw, rf.member, redactFileBytes(rf.absPath, redactor)); err != nil {
			zw.Close()
			zf.Close()
			return err
		}
	}

	if err := zw.Close(); err != nil {
		zf.Close()
		return fmt.Errorf("finalize bundle: %w", err)
	}
	return zf.Close()
}

// rolloutFile pairs an on-disk rollout JSONL path with its intended zip
// member name.
type rolloutFile struct {
	absPath string
	member  string
}

// collectFeedbackRollouts returns the newest max .jsonl files under dir
// (layout <dir>/<YYYY-MM-DD>/<run_id>.jsonl) as zip members preserving the
// dated subdirectory. The second return value explains an empty result:
// "" when files were found, otherwise a human-readable reason.
func collectFeedbackRollouts(dir string, max int) ([]rolloutFile, string) {
	if strings.TrimSpace(dir) == "" {
		return nil, "rollout dir not configured (HARNESS_ROLLOUT_DIR unset)"
	}
	fi, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "rollout dir " + dir + " does not exist"
		}
		return nil, "rollout dir " + dir + " is not accessible: " + err.Error()
	}
	if !fi.IsDir() {
		return nil, "rollout dir " + dir + " is not a directory"
	}

	type candidate struct {
		absPath string
		member  string
		modTime time.Time
	}
	var found []candidate
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		found = append(found, candidate{absPath: path, member: "rollouts/" + filepath.ToSlash(rel), modTime: info.ModTime()})
		return nil
	})
	if len(found) == 0 {
		return nil, "no rollout files found under " + dir
	}

	// Newest first, then cap.
	sort.Slice(found, func(i, j int) bool { return found[i].modTime.After(found[j].modTime) })
	if len(found) > max {
		found = found[:max]
	}
	out := make([]rolloutFile, len(found))
	for i, c := range found {
		out[i] = rolloutFile{absPath: c.absPath, member: c.member}
	}
	return out, ""
}

// redactCLIConfigJSON marshals cfg and scrubs it: every stored api_keys value
// is replaced exactly (format-agnostic), then the forensics redaction
// patterns run over the whole document (catches secrets pasted into history).
func redactCLIConfigJSON(cfg *harnessconfig.Config, r *redaction.Redactor) []byte {
	if cfg == nil {
		cfg = &harnessconfig.Config{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		data = []byte("{}")
	}
	text := string(data)
	for _, v := range cfg.APIKeys {
		if v != "" {
			text = strings.ReplaceAll(text, v, "[REDACTED:api_key]")
		}
	}
	return []byte(r.Redact(text))
}

// redactFileBytes reads path and returns its content with the redaction
// patterns applied. Unreadable files yield an explanatory placeholder rather
// than failing the whole bundle.
func redactFileBytes(path string, r *redaction.Redactor) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return []byte("could not read file: " + err.Error())
	}
	return []byte(r.Redact(string(data)))
}

func writeZipMember(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create member %s: %w", name, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write member %s: %w", name, err)
	}
	return nil
}
