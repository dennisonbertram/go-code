package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validHookJSON = `{"name":"h","event":"pre_tool_use","kind":"command","command":["/bin/true"]}`

// newStorePath returns a trust-store path inside a temp "home" directory.
func newStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".harness", "hooks-trust.json")
}

func TestTrustStore_UntrustedFileSkipped(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	hookPath := writeHookFile(t, projectDir, "evil.json", validHookJSON)
	store, err := LoadTrustStore(newStorePath(t))
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}

	defs, skips := LoadWithOptions(LoadOptions{TrustStore: store}, projectDir)
	if len(defs) != 0 {
		t.Fatalf("untrusted project hook loaded: %+v", defs)
	}
	if len(skips) != 1 || skips[0].File != hookPath || skips[0].Reason != SkipReasonUntrusted {
		t.Fatalf("skips: %+v, want one %q skip for %s", skips, SkipReasonUntrusted, hookPath)
	}
}

func TestTrustStore_TrustThenLoad(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeHookFile(t, projectDir, "ok.json", validHookJSON)
	storePath := newStorePath(t)
	store, err := LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}

	hookPath := filepath.Join(projectDir, "ok.json")
	if err := store.Trust(hookPath); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	defs, skips := LoadWithOptions(LoadOptions{TrustStore: store}, projectDir)
	if len(skips) != 0 {
		t.Fatalf("trusted hook skipped: %+v", skips)
	}
	if len(defs) != 1 || defs[0].Name != "h" {
		t.Fatalf("defs: %+v", defs)
	}

	// Trust must persist: a freshly loaded store trusts the file too.
	store2, err := LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("reload trust store: %v", err)
	}
	if reason := store2.CheckFile(hookPath); reason != "" {
		t.Fatalf("reloaded store does not trust file: %q", reason)
	}
}

func TestTrustStore_EditAfterTrustUntrusts(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	hookPath := writeHookFile(t, projectDir, "hook.json", validHookJSON)
	store, err := LoadTrustStore(newStorePath(t))
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := store.Trust(hookPath); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	// One byte changed → content hash differs → modified_since_trusted.
	if err := os.WriteFile(hookPath, []byte(validHookJSON+" "), 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := store.CheckFile(hookPath); reason != SkipReasonModifiedSinceTrusted {
		t.Fatalf("CheckFile after edit: got %q, want %q", reason, SkipReasonModifiedSinceTrusted)
	}

	defs, skips := LoadWithOptions(LoadOptions{TrustStore: store}, projectDir)
	if len(defs) != 0 || len(skips) != 1 || skips[0].Reason != SkipReasonModifiedSinceTrusted {
		t.Fatalf("defs=%+v skips=%+v", defs, skips)
	}
}

func TestTrustStore_Revoke(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	hookPath := writeHookFile(t, projectDir, "hook.json", validHookJSON)
	store, err := LoadTrustStore(newStorePath(t))
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := store.Trust(hookPath); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if err := store.Revoke(hookPath); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if reason := store.CheckFile(hookPath); reason != SkipReasonUntrusted {
		t.Fatalf("after revoke: got %q, want %q", reason, SkipReasonUntrusted)
	}
	// Revoke of an unknown path is a no-op, not an error.
	if err := store.Revoke(hookPath); err != nil {
		t.Fatalf("second Revoke should be a no-op: %v", err)
	}
}

func TestTrustStore_UserGlobalBypassesTrust(t *testing.T) {
	t.Parallel()
	userDir := t.TempDir()
	writeHookFile(t, userDir, "mine.json", validHookJSON)
	store, err := LoadTrustStore(newStorePath(t)) // empty store, no records
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}

	defs, skips := LoadWithOptions(LoadOptions{UserDir: userDir, TrustStore: store}, userDir)
	if len(skips) != 0 {
		t.Fatalf("user-global hook skipped: %+v", skips)
	}
	if len(defs) != 1 || defs[0].Source != SourceUser {
		t.Fatalf("defs: %+v", defs)
	}
}

func TestTrustStore_MissingStoreFileIsEmpty(t *testing.T) {
	t.Parallel()
	store, err := LoadTrustStore(filepath.Join(t.TempDir(), "never-existed.json"))
	if err != nil {
		t.Fatalf("missing store file must not be fatal: %v", err)
	}
	if reason := store.CheckFile("/any/path.json"); reason != SkipReasonUntrusted {
		t.Fatalf("empty store: got %q, want %q", reason, SkipReasonUntrusted)
	}
}

func TestTrustStore_CorruptStoreFileIsEmpty(t *testing.T) {
	t.Parallel()
	storePath := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(storePath, []byte("{{{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("corrupt store file must not be fatal: %v", err)
	}
	if reason := store.CheckFile("/any/path.json"); reason != SkipReasonUntrusted {
		t.Fatalf("corrupt store treated as empty: got %q, want %q", reason, SkipReasonUntrusted)
	}
}

func TestTrustStore_AtomicWriteLeavesNoTempFiles(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	hookPath := writeHookFile(t, projectDir, "hook.json", validHookJSON)
	storePath := newStorePath(t)
	store, err := LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := store.Trust(hookPath); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	// The store directory must contain exactly the store file — the
	// write-temp-then-rename pattern must clean up after itself.
	entries, err := os.ReadDir(filepath.Dir(storePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(storePath) {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("store dir contains %v, want exactly %s", names, filepath.Base(storePath))
	}
	// And the file must be valid, complete JSON (no partial write).
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sha256"`) {
		t.Fatalf("store file missing sha256 record: %s", data)
	}
}

func TestTrustStore_StoreNeverWrittenToProjectTree(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	hookPath := writeHookFile(t, projectDir, "hook.json", validHookJSON)
	storePath := newStorePath(t) // under a separate "home" temp dir
	store, err := LoadTrustStore(storePath)
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := store.Trust(hookPath); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	// Only the hook file itself may exist in the project tree.
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "trust") {
			t.Fatalf("trust state leaked into project tree: %s", e.Name())
		}
	}
}

func TestTrustStore_TrustMissingFileFails(t *testing.T) {
	t.Parallel()
	store, err := LoadTrustStore(newStorePath(t))
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	if err := store.Trust(filepath.Join(t.TempDir(), "ghost.json")); err == nil {
		t.Fatal("Trust of a nonexistent file must fail")
	}
}

// TestLoad_TrustIntegration mixes one user-global and one untrusted project
// def: exactly one def loads and one skip record carries the trust reason.
func TestLoad_TrustIntegration(t *testing.T) {
	t.Parallel()
	userDir := t.TempDir()
	projectDir := t.TempDir()
	writeHookFile(t, userDir, "global.json", `{"name":"g","event":"pre_message","kind":"command","command":["/bin/true"]}`)
	projectHook := writeHookFile(t, projectDir, "proj.json", validHookJSON)

	store, err := LoadTrustStore(newStorePath(t))
	if err != nil {
		t.Fatalf("LoadTrustStore: %v", err)
	}
	defs, skips := LoadWithOptions(LoadOptions{UserDir: userDir, TrustStore: store}, userDir, projectDir)
	if len(defs) != 1 || defs[0].Name != "g" {
		t.Fatalf("defs: %+v, want only the user-global def", defs)
	}
	if len(skips) != 1 || skips[0].File != projectHook || skips[0].Reason != SkipReasonUntrusted {
		t.Fatalf("skips: %+v", skips)
	}
}

// TestEmptyTrustStore_FailsClosed covers the in-memory fail-closed store used
// when the on-disk store is unreadable: everything reports untrusted, and
// mutation attempts fail because there is no backing file.
func TestEmptyTrustStore_FailsClosed(t *testing.T) {
	t.Parallel()
	store := EmptyTrustStore()

	projectDir := t.TempDir()
	hookPath := writeHookFile(t, projectDir, "hook.json", validHookJSON)

	if reason := store.CheckFile(hookPath); reason != SkipReasonUntrusted {
		t.Fatalf("empty store: got %q, want %q", reason, SkipReasonUntrusted)
	}
	defs, skips := LoadWithOptions(LoadOptions{TrustStore: store}, projectDir)
	if len(defs) != 0 || len(skips) != 1 || skips[0].Reason != SkipReasonUntrusted {
		t.Fatalf("defs=%+v skips=%+v, want load blocked", defs, skips)
	}

	if err := store.Trust(hookPath); err == nil {
		t.Fatal("Trust on a backing-file-less store must fail")
	}
	if err := store.Revoke(hookPath); err == nil {
		t.Fatal("Revoke on a backing-file-less store must fail")
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("List: got %+v, want empty", got)
	}
}
