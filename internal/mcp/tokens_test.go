package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixedClock returns a clock function pinned at t0.
func fixedClock(t0 time.Time) func() time.Time { return func() time.Time { return t0 } }

func fullToken(issuer string, expiry time.Time) Token {
	return Token{
		Issuer:       issuer,
		AccessToken:  "access-" + issuer,
		RefreshToken: "refresh-" + issuer,
		TokenType:    "Bearer",
		Expiry:       expiry,
		Scopes:       []string{"mcp", "offline_access"},
	}
}

// TestTokenStore_PutGet_RoundTrip verifies that a token written with Put is
// returned by Get with every field intact, and that the on-disk file lives
// under the store directory.
func TestTokenStore_PutGet_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(dir)

	want := fullToken("https://as.example.com", time.Now().Add(time.Hour).UTC().Truncate(time.Second))
	if err := store.Put("demo", want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get("demo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Issuer != want.Issuer {
		t.Errorf("Issuer = %q, want %q", got.Issuer, want.Issuer)
	}
	if got.AccessToken != want.AccessToken {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, want.AccessToken)
	}
	if got.RefreshToken != want.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, want.RefreshToken)
	}
	if got.TokenType != want.TokenType {
		t.Errorf("TokenType = %q, want %q", got.TokenType, want.TokenType)
	}
	if !got.Expiry.Equal(want.Expiry) {
		t.Errorf("Expiry = %v, want %v", got.Expiry, want.Expiry)
	}
	if len(got.Scopes) != len(want.Scopes) || got.Scopes[0] != "mcp" || got.Scopes[1] != "offline_access" {
		t.Errorf("Scopes = %v, want %v", got.Scopes, want.Scopes)
	}

	// The file must exist inside the injected directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 token file in %s, got %d", dir, len(entries))
	}

	// The on-disk JSON must carry the server name and token fields.
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var onDisk struct {
		Server string `json:"server"`
		Token  Token  `json:"token"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("on-disk file is not valid JSON: %v", err)
	}
	if onDisk.Server != "demo" {
		t.Errorf("on-disk server = %q, want %q", onDisk.Server, "demo")
	}
	if onDisk.Token.AccessToken != want.AccessToken {
		t.Errorf("on-disk access token = %q, want %q", onDisk.Token.AccessToken, want.AccessToken)
	}
}

// TestTokenStore_Get_NotFound verifies the distinct not-found error.
func TestTokenStore_Get_NotFound(t *testing.T) {
	store := NewTokenStore(t.TempDir())

	tok, err := store.Get("missing")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Get error = %v, want errors.Is ErrTokenNotFound", err)
	}
	if tok.AccessToken != "" {
		t.Errorf("expected zero token on not-found, got %+v", tok)
	}
}

// TestTokenStore_Get_CorruptFile verifies that unreadable or inconsistent
// files are reported distinctly from a missing token.
func TestTokenStore_Get_CorruptFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"not JSON at all", `{"server":`},
		{"valid JSON wrong shape", `["an","array"]`},
		{"missing access token", `{"server":"demo","token":{"issuer":"https://as.example.com"}}`},
		{"server name mismatch", `{"server":"other","token":{"access_token":"x"}}`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store := NewTokenStore(dir)

			path := filepath.Join(dir, tokenFileName("demo"))
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := store.Get("demo")
			if !errors.Is(err, ErrTokenCorrupt) {
				t.Fatalf("Get error = %v, want errors.Is ErrTokenCorrupt", err)
			}
			if errors.Is(err, ErrTokenNotFound) {
				t.Fatalf("corrupt file must not be reported as not-found: %v", err)
			}
		})
	}
}

// TestTokenStore_Permissions verifies 0700 on the directory and 0600 on the
// token file, including when the directory pre-existed with loose perms.
func TestTokenStore_Permissions(t *testing.T) {
	t.Run("fresh dir", func(t *testing.T) {
		base := t.TempDir()
		dir := filepath.Join(base, "mcp")
		store := NewTokenStore(dir)

		if err := store.Put("demo", fullToken("https://as.example.com", time.Time{})); err != nil {
			t.Fatalf("Put: %v", err)
		}

		di, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Errorf("dir perms = %o, want 700", got)
		}

		fi, err := os.Stat(filepath.Join(dir, tokenFileName("demo")))
		if err != nil {
			t.Fatalf("stat file: %v", err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("file perms = %o, want 600", got)
		}
	})

	t.Run("pre-existing loose dir is hardened", func(t *testing.T) {
		dir := t.TempDir() // t.TempDir creates 0700 already; loosen it.
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		store := NewTokenStore(dir)

		if err := store.Put("demo", fullToken("https://as.example.com", time.Time{})); err != nil {
			t.Fatalf("Put: %v", err)
		}

		di, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if got := di.Mode().Perm(); got != 0o700 {
			t.Errorf("dir perms = %o, want 700 after Put", got)
		}
	})

	t.Run("overwrite keeps 0600", func(t *testing.T) {
		dir := t.TempDir()
		store := NewTokenStore(dir)
		if err := store.Put("demo", fullToken("https://as1.example.com", time.Time{})); err != nil {
			t.Fatalf("Put 1: %v", err)
		}
		if err := store.Put("demo", fullToken("https://as2.example.com", time.Time{})); err != nil {
			t.Fatalf("Put 2: %v", err)
		}
		fi, err := os.Stat(filepath.Join(dir, tokenFileName("demo")))
		if err != nil {
			t.Fatalf("stat file: %v", err)
		}
		if got := fi.Mode().Perm(); got != 0o600 {
			t.Errorf("file perms after overwrite = %o, want 600", got)
		}
	})
}

// TestTokenStore_Get_ExpiryClassification verifies the distinct expiry
// behavior: expired tokens are returned (so the caller can use the refresh
// token) together with an ErrTokenExpired-wrapping error; tokens without an
// expiry never expire; a token whose expiry equals now is expired.
func TestTokenStore_Get_ExpiryClassification(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		expiry    time.Time
		wantErr   error // nil means valid token
		wantToken bool
	}{
		{"expired in the past", now.Add(-time.Minute), ErrTokenExpired, true},
		{"expiry equals now", now, ErrTokenExpired, true},
		{"valid in the future", now.Add(time.Hour), nil, true},
		{"zero expiry never expires", time.Time{}, nil, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := NewTokenStore(t.TempDir())
			store.now = fixedClock(now)

			tok := fullToken("https://as.example.com", tc.expiry)
			if err := store.Put("demo", tok); err != nil {
				t.Fatalf("Put: %v", err)
			}

			got, err := store.Get("demo")
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Get error = %v, want nil", err)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Get error = %v, want errors.Is %v", err, tc.wantErr)
				}
			}
			if tc.wantToken {
				if got.AccessToken != tok.AccessToken {
					t.Errorf("returned token AccessToken = %q, want %q", got.AccessToken, tok.AccessToken)
				}
				if got.RefreshToken != tok.RefreshToken {
					t.Errorf("expired path must still return the refresh token: got %q, want %q", got.RefreshToken, tok.RefreshToken)
				}
			}
		})
	}
}

// TestTokenStore_Delete verifies delete removes the token and is idempotent.
func TestTokenStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(dir)

	if err := store.Put("demo", fullToken("https://as.example.com", time.Time{})); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Delete("demo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get("demo"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Get after Delete error = %v, want errors.Is ErrTokenNotFound", err)
	}

	// Deleting a missing token is not an error (logout is idempotent).
	if err := store.Delete("demo"); err != nil {
		t.Fatalf("Delete of missing token: %v", err)
	}
	// Deleting from a store whose directory does not exist yet is also fine.
	fresh := NewTokenStore(filepath.Join(t.TempDir(), "not-created-yet"))
	if err := fresh.Delete("ghost"); err != nil {
		t.Fatalf("Delete with absent store dir: %v", err)
	}
}

// TestTokenStore_PutOverwrite verifies a second Put replaces the stored token
// (e.g. re-login against a different issuer).
func TestTokenStore_PutOverwrite(t *testing.T) {
	store := NewTokenStore(t.TempDir())

	if err := store.Put("demo", fullToken("https://old.example.com", time.Time{})); err != nil {
		t.Fatalf("Put old: %v", err)
	}
	want := fullToken("https://new.example.com", time.Time{})
	if err := store.Put("demo", want); err != nil {
		t.Fatalf("Put new: %v", err)
	}

	got, err := store.Get("demo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Issuer != want.Issuer || got.AccessToken != want.AccessToken {
		t.Errorf("Get = %+v, want issuer/access from %q", got, want.Issuer)
	}
}

// TestTokenStore_PutValidation verifies invalid inputs are rejected without
// writing anything.
func TestTokenStore_PutValidation(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(dir)

	if err := store.Put("", fullToken("https://as.example.com", time.Time{})); err == nil {
		t.Error("Put with empty server name: expected error, got nil")
	}
	if err := store.Put("demo", Token{Issuer: "https://as.example.com"}); err == nil {
		t.Error("Put with empty access token: expected error, got nil")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("invalid Puts must not write files, found %d entries", len(entries))
	}
}

// TestTokenStore_ServerNameSanitization verifies that server names containing
// path separators or other special characters are stored inside the store
// directory (no traversal) and round-trip correctly.
func TestTokenStore_ServerNameSanitization(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(dir)

	names := []string{"org/server", "a b", "dots..here", `back\slash`, "percent%20"}
	for i, name := range names {
		tok := fullToken(fmt.Sprintf("https://as%d.example.com", i), time.Time{})
		if err := store.Put(name, tok); err != nil {
			t.Fatalf("Put(%q): %v", name, err)
		}
	}
	for i, name := range names {
		got, err := store.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if want := fmt.Sprintf("https://as%d.example.com", i); got.Issuer != want {
			t.Errorf("Get(%q).Issuer = %q, want %q", name, got.Issuer, want)
		}
	}

	// Every file must be directly inside the store directory — no escapes.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(names) {
		t.Fatalf("expected %d files in store dir, got %d", len(names), len(entries))
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("unexpected subdirectory created by sanitization: %q", e.Name())
		}
		if strings.Contains(e.Name(), "/") || strings.Contains(e.Name(), `\`) {
			t.Errorf("file name contains a path separator: %q", e.Name())
		}
	}
}

// TestTokenStore_ConcurrentAccess hammers the store from many goroutines and
// verifies consistency. Run with -race for the race-detector guarantee.
func TestTokenStore_ConcurrentAccess(t *testing.T) {
	store := NewTokenStore(t.TempDir())

	const workers = 16
	const opsPerWorker = 25

	var wg sync.WaitGroup
	errs := make(chan error, workers*opsPerWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			server := fmt.Sprintf("server-%d", w%4) // force contention on shared keys
			for i := 0; i < opsPerWorker; i++ {
				tok := fullToken(fmt.Sprintf("https://as-%d-%d.example.com", w, i), time.Time{})
				if err := store.Put(server, tok); err != nil {
					errs <- fmt.Errorf("Put(%q): %w", server, err)
					return
				}
				if _, err := store.Get(server); err != nil && !errors.Is(err, ErrTokenNotFound) && !errors.Is(err, ErrTokenExpired) {
					errs <- fmt.Errorf("Get(%q): %w", server, err)
					return
				}
				if i%5 == 0 {
					if err := store.Delete(server); err != nil {
						errs <- fmt.Errorf("Delete(%q): %w", server, err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Final write must be readable and complete — a torn write would fail to
	// decode and surface as ErrTokenCorrupt.
	final := fullToken("https://final.example.com", time.Time{})
	if err := store.Put("server-0", final); err != nil {
		t.Fatalf("final Put: %v", err)
	}
	got, err := store.Get("server-0")
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if got.AccessToken != final.AccessToken || got.Issuer != final.Issuer {
		t.Errorf("final Get = %+v, want token from %q", got, final.Issuer)
	}
}

// TestDefaultTokenStore_Path verifies the default store resolves under
// ~/.harness/mcp using the process home, without writing anything.
func TestDefaultTokenStore_Path(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store := DefaultTokenStore()
	want := filepath.Join(home, ".harness", "mcp")
	if store.Dir() != want {
		t.Errorf("DefaultTokenStore dir = %q, want %q", store.Dir(), want)
	}
}
