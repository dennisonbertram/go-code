package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/provider"
	"go-agent-harness/internal/provider/tokencache"
)

// ErrNotConfigured intentionally includes the complete operator remediation.
// It never reveals whether any individual credential field was present.
var ErrNotConfigured = errors.New("Codex subscription is not configured; run `codex login`, then `harnesscli auth codex login`")

// Credential is the harness-owned copy of a ChatGPT Codex credential.
type Credential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	AccountID    string    `json:"account_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type vendorAuthFile struct {
	AuthMode string `json:"auth_mode"`
	Tokens   struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

// Store owns only the harness copy of the subscription credential.
type Store struct{ path string }

func NewStore(path string) *Store { return &Store{path: path} }

func DefaultStore() *Store {
	home, err := os.UserHomeDir()
	if err != nil {
		return NewStore(filepath.Join(".harness", "subscription-auth", "codex.json"))
	}
	return NewStore(filepath.Join(home, ".harness", "subscription-auth", "codex.json"))
}

func DefaultVendorAuthPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".codex", "auth.json")
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func (s *Store) Path() string { return s.path }

// Import reads but never changes the vendor Codex auth file.
func (s *Store) Import(vendorPath string) (Credential, error) {
	raw, err := os.ReadFile(vendorPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Credential{}, ErrNotConfigured
		}
		return Credential{}, fmt.Errorf("read Codex credential import source: %w", err)
	}
	var vendor vendorAuthFile
	if err := json.Unmarshal(raw, &vendor); err != nil {
		return Credential{}, fmt.Errorf("decode Codex credential import source: %w", err)
	}
	if vendor.AuthMode != "chatgpt" {
		return Credential{}, ErrNotConfigured
	}
	expiresAt, err := jwtExpiry(vendor.Tokens.AccessToken)
	if err != nil || vendor.Tokens.RefreshToken == "" || vendor.Tokens.AccountID == "" {
		return Credential{}, ErrNotConfigured
	}
	credential := Credential{AccessToken: vendor.Tokens.AccessToken, RefreshToken: vendor.Tokens.RefreshToken, IDToken: vendor.Tokens.IDToken, AccountID: vendor.Tokens.AccountID, ExpiresAt: expiresAt}
	if err := s.Save(credential); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

func (s *Store) Load() (Credential, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Credential{}, ErrNotConfigured
		}
		return Credential{}, fmt.Errorf("read Codex subscription credential: %w", err)
	}
	var credential Credential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return Credential{}, fmt.Errorf("decode Codex subscription credential: %w", err)
	}
	if credential.AccessToken == "" || credential.RefreshToken == "" || credential.AccountID == "" || credential.ExpiresAt.IsZero() {
		return Credential{}, ErrNotConfigured
	}
	return credential, nil
}

func (s *Store) Save(credential Credential) error {
	if credential.AccessToken == "" || credential.RefreshToken == "" || credential.AccountID == "" || credential.ExpiresAt.IsZero() {
		return ErrNotConfigured
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create Codex subscription credential directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("secure Codex subscription credential directory: %w", err)
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode Codex subscription credential: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".codex-credential-*")
	if err != nil {
		return fmt.Errorf("create Codex subscription credential file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure Codex subscription credential file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write Codex subscription credential: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close Codex subscription credential: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("save Codex subscription credential: %w", err)
	}
	return nil
}

func (s *Store) Remove() error {
	err := os.Remove(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove Codex subscription credential: %w", err)
	}
	return nil
}

// Source provides a refreshable bearer credential while preserving refreshes
// exclusively in the harness-owned store.
type Source struct {
	cache     *tokencache.Cache
	accountID string
}

var _ provider.TokenSource = (*Source)(nil)

func NewTokenSource(store *Store, refresh tokencache.RefreshFunc) (*Source, error) {
	return NewTokenSourceWithSafetyMargin(store, refresh, 30*time.Second)
}

// NewTokenSourceWithSafetyMargin is equivalent to NewTokenSource with a
// caller-supplied refresh margin. It exists for deterministic integration
// coverage; production callers should use NewTokenSource.
func NewTokenSourceWithSafetyMargin(store *Store, refresh tokencache.RefreshFunc, safetyMargin time.Duration) (*Source, error) {
	credential, err := store.Load()
	if err != nil {
		return nil, err
	}
	cache := tokencache.New(credential.AccessToken, credential.RefreshToken, credential.ExpiresAt, safetyMargin, func(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
		token, nextRefresh, expiresAt, err := refresh(ctx, refreshToken)
		if err != nil {
			return "", "", time.Time{}, err
		}
		if err := store.Save(Credential{AccessToken: token, RefreshToken: nextRefresh, IDToken: credential.IDToken, AccountID: credential.AccountID, ExpiresAt: expiresAt}); err != nil {
			return "", "", time.Time{}, fmt.Errorf("persist refreshed Codex credential: %w", err)
		}
		return token, nextRefresh, expiresAt, nil
	})
	return &Source{cache: cache, accountID: credential.AccountID}, nil
}

func (s *Source) Token(ctx context.Context) (string, error) { return s.cache.Token(ctx) }
func (s *Source) AccountID() string                         { return s.accountID }

func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, errors.New("JWT payload is absent")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	var claims struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.ExpiresAt <= 0 {
		return time.Time{}, errors.New("JWT expiry is absent")
	}
	return time.Unix(claims.ExpiresAt, 0), nil
}
