package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sentinel errors for the MCP OAuth token store. They are wrapped (%w) in
// returned errors so callers can classify failures with errors.Is.
var (
	// ErrTokenNotFound is returned by TokenStore.Get when no token is stored
	// for the named server.
	ErrTokenNotFound = errors.New("mcp: no stored token for server")
	// ErrTokenExpired is returned by TokenStore.Get when the stored token's
	// expiry has passed. The token is still returned so the caller can use
	// its refresh token instead of failing.
	ErrTokenExpired = errors.New("mcp: stored token is expired")
	// ErrTokenCorrupt is returned by TokenStore.Get when the token file
	// exists but cannot be decoded or fails consistency checks.
	ErrTokenCorrupt = errors.New("mcp: stored token file is corrupt")
)

// Token is an OAuth 2.1 token set issued for one MCP server. The zero Expiry
// means the access token does not expire.
type Token struct {
	// Issuer identifies the authorization server that issued the token. It
	// is stored alongside the token so callers can detect an issuer change
	// (tokens are keyed by server name and issuer).
	Issuer string `json:"issuer,omitempty"`
	// AccessToken is the bearer credential attached to MCP HTTP requests.
	AccessToken string `json:"access_token"`
	// RefreshToken is used to renew the access token when it expires.
	RefreshToken string `json:"refresh_token,omitempty"`
	// TokenType is the token type from the OAuth response, typically "Bearer".
	TokenType string `json:"token_type,omitempty"`
	// Expiry is the access token's expiration time; zero means never.
	Expiry time.Time `json:"expiry,omitempty"`
	// Scopes are the granted OAuth scopes, if any.
	Scopes []string `json:"scopes,omitempty"`
}

// tokenFile is the on-disk JSON envelope for one server's token. The server
// name is recorded so reads can detect a mismatch between the file name and
// its contents.
type tokenFile struct {
	Server string `json:"server"`
	Token  Token  `json:"token"`
}

// tokenFileName maps a server name to a safe, unique file name inside the
// store directory. url.PathEscape is injective and escapes path separators,
// so distinct server names never collide and cannot traverse the directory.
func tokenFileName(server string) string {
	return url.PathEscape(server) + ".json"
}

// TokenStore persists OAuth tokens as one JSON file per server under a
// single directory (by default ~/.harness/mcp). Files are written atomically
// (temp file + rename) with 0600 permissions; the directory is enforced to
// 0700, mirroring the credential-store practice in
// internal/provider/codex/store.go.
//
// TokenStore is safe for concurrent use.
type TokenStore struct {
	dir string

	mu  sync.Mutex
	now func() time.Time
}

// NewTokenStore returns a TokenStore rooted at dir. The directory is created
// (with 0700 permissions) on the first Put.
func NewTokenStore(dir string) *TokenStore {
	return &TokenStore{dir: dir, now: time.Now}
}

// DefaultTokenStore returns a TokenStore under ~/.harness/mcp, falling back
// to a relative .harness/mcp when the home directory cannot be determined —
// the same fallback pattern as codex.DefaultStore.
func DefaultTokenStore() *TokenStore {
	home, err := os.UserHomeDir()
	if err != nil {
		return NewTokenStore(filepath.Join(".harness", "mcp"))
	}
	return NewTokenStore(filepath.Join(home, ".harness", "mcp"))
}

// Dir returns the directory the store persists tokens in.
func (s *TokenStore) Dir() string { return s.dir }

// Get returns the token stored for server.
//
//   - No token stored: zero Token and an error wrapping ErrTokenNotFound.
//   - Token file corrupt or inconsistent: zero Token and an error wrapping
//     ErrTokenCorrupt.
//   - Token expired (non-zero Expiry at or before now): the token and an
//     error wrapping ErrTokenExpired — the caller is expected to refresh
//     using the returned token's refresh token rather than fail.
//   - Otherwise: the token and nil.
func (s *TokenStore) Get(server string) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.pathFor(server))
	if err != nil {
		if os.IsNotExist(err) {
			return Token{}, fmt.Errorf("mcp: token for server %q: %w", server, ErrTokenNotFound)
		}
		return Token{}, fmt.Errorf("mcp: read token for server %q: %w", server, err)
	}

	var f tokenFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Token{}, fmt.Errorf("mcp: decode token for server %q: %v: %w", server, err, ErrTokenCorrupt)
	}
	if f.Server != server || f.Token.AccessToken == "" {
		return Token{}, fmt.Errorf("mcp: token file for server %q is inconsistent: %w", server, ErrTokenCorrupt)
	}

	if !f.Token.Expiry.IsZero() && !s.now().Before(f.Token.Expiry) {
		return f.Token, fmt.Errorf("mcp: token for server %q: %w", server, ErrTokenExpired)
	}
	return f.Token, nil
}

// Put stores tok for server, replacing any previously stored token. The
// server name must be non-empty and the token must carry an access token.
func (s *TokenStore) Put(server string, tok Token) error {
	if server == "" {
		return fmt.Errorf("mcp: server name must not be empty")
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("mcp: token for server %q has no access token", server)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("mcp: create token directory: %w", err)
	}
	// Harden the directory even if it pre-existed with loose permissions.
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return fmt.Errorf("mcp: secure token directory: %w", err)
	}

	raw, err := json.Marshal(tokenFile{Server: server, Token: tok})
	if err != nil {
		return fmt.Errorf("mcp: encode token for server %q: %w", server, err)
	}

	tmp, err := os.CreateTemp(s.dir, ".mcp-token-*")
	if err != nil {
		return fmt.Errorf("mcp: create token file for server %q: %w", server, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("mcp: secure token file for server %q: %w", server, err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("mcp: write token file for server %q: %w", server, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcp: close token file for server %q: %w", server, err)
	}
	if err := os.Rename(tmpName, s.pathFor(server)); err != nil {
		return fmt.Errorf("mcp: save token file for server %q: %w", server, err)
	}
	return nil
}

// Delete removes the token stored for server. Deleting a token that does not
// exist is not an error, so logout flows are idempotent.
func (s *TokenStore) Delete(server string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := os.Remove(s.pathFor(server))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("mcp: remove token for server %q: %w", server, err)
	}
	return nil
}

// pathFor returns the token file path for a server name.
func (s *TokenStore) pathFor(server string) string {
	return filepath.Join(s.dir, tokenFileName(server))
}
