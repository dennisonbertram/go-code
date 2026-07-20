package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func jwtWithExpiry(expiresAt time.Time) string {
	payload, _ := json.Marshal(map[string]int64{"exp": expiresAt.Unix()})
	return "test." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestStoreImportCopiesVendorCredentialWithoutModifyingIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	vendorPath := filepath.Join(dir, "vendor-auth.json")
	vendor := `{"auth_mode":"chatgpt","tokens":{"access_token":"` + jwtWithExpiry(time.Now().Add(time.Hour)) + `","refresh_token":"test-import-refresh","id_token":"test-import-id","account_id":"acct-test"}}`
	if err := os.WriteFile(vendorPath, []byte(vendor), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(vendorPath)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(dir, "harness", "codex.json"))
	credential, err := store.Import(vendorPath)
	if err != nil {
		t.Fatalf("Import() error: %v", err)
	}
	if credential.AccountID != "acct-test" {
		t.Fatalf("AccountID = %q", credential.AccountID)
	}
	after, err := os.ReadFile(vendorPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("Import modified vendor credential file")
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("credential mode = %o, want 0600", info.Mode().Perm())
	}
	if parent, err := os.Stat(filepath.Dir(store.Path())); err != nil || parent.Mode().Perm() != 0o700 {
		t.Errorf("credential directory must be 0700, info=%v err=%v", parent, err)
	}
}

func TestStoreMissingCredentialGivesCodexRemediation(t *testing.T) {
	t.Parallel()

	_, err := NewStore(filepath.Join(t.TempDir(), "missing.json")).Load()
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Load() error = %v, want ErrNotConfigured", err)
	}
	if !strings.Contains(err.Error(), "codex login") || !strings.Contains(err.Error(), "harnesscli auth codex login") {
		t.Fatalf("missing credential error lacks remediation: %q", err)
	}
}

func TestTokenSourceRefreshPersistsOnlyHarnessCredential(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "harness", "codex.json"))
	if err := store.Save(Credential{AccessToken: "test-expired-access", RefreshToken: "test-old-refresh", AccountID: "acct-test", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	source, err := NewTokenSource(store, func(context.Context, string) (string, string, time.Time, error) {
		return "test-fresh-access", "test-fresh-refresh", time.Now().Add(time.Hour), nil
	})
	if err != nil {
		t.Fatalf("NewTokenSource() error: %v", err)
	}
	got, err := source.Token(context.Background())
	if err != nil || got != "test-fresh-access" {
		t.Fatalf("Token() = %q, %v", got, err)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RefreshToken != "test-fresh-refresh" || persisted.AccessToken != "test-fresh-access" {
		t.Fatal("refresh replacement pair was not persisted")
	}
	if source.AccountID() != "acct-test" {
		t.Fatalf("AccountID() = %q", source.AccountID())
	}
}
