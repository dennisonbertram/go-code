// Package kimi implements Kimi Code subscription credentials. It never writes
// the vendor CLI credential file; only the separate harness store is mutable.
package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/provider"
	"go-agent-harness/internal/provider/tokencache"
)

const (
	TokenEndpoint = "https://auth.kimi.com/api/oauth/token"
	// ClientID is the public Kimi Code OAuth client identifier used by the
	// convention-based refresh request. Live body verification remains pending.
	ClientID     = "kimi-code"
	SafetyMargin = 30 * time.Second
)

// Credentials is the minimal compatible vendor credential shape. Values are
// deliberately never formatted into errors or log messages.
type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
}

func DefaultStorePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".harness", "subscription-auth", "kimi.json")
	}
	return filepath.Join(home, ".harness", "subscription-auth", "kimi.json")
}

func VendorCredentialPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".kimi-code", "credentials", "kimi-code.json")
	}
	return filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json")
}

// Load reads a credential without exposing its values in errors.
func Load(path string) (Credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("read Kimi subscription credential: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return Credentials{}, fmt.Errorf("decode Kimi subscription credential: %w", err)
	}
	if creds.AccessToken == "" || creds.RefreshToken == "" || creds.ExpiresAt <= 0 {
		return Credentials{}, fmt.Errorf("Kimi subscription credential is incomplete")
	}
	return creds, nil
}

func save(path string, creds Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Kimi subscription credential directory: %w", err)
	}
	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Kimi subscription credential")
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write Kimi subscription credential: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// Import performs a read-only vendor-file import into a separate harness file.
func Import(vendorPath, storePath string) error {
	creds, err := Load(vendorPath)
	if err != nil {
		return fmt.Errorf("Kimi Code credential unavailable; run kimi-code login then harnesscli auth kimi login")
	}
	return save(storePath, creds)
}

// NewTokenSource persists rotated credentials only to the harness-owned store.
func NewTokenSource(storePath, endpoint string, client *http.Client) (provider.TokenSource, error) {
	creds, err := Load(storePath)
	if err != nil {
		return nil, err
	}
	refresh := RefreshFunc(endpoint, client)
	return tokencache.New(creds.AccessToken, creds.RefreshToken, time.Unix(creds.ExpiresAt, 0), SafetyMargin, func(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
		token, next, expiry, err := refresh(ctx, refreshToken)
		if err != nil {
			return "", "", time.Time{}, err
		}
		if err := save(storePath, Credentials{AccessToken: token, RefreshToken: next, ExpiresAt: expiry.Unix(), ExpiresIn: int64(time.Until(expiry).Seconds())}); err != nil {
			return "", "", time.Time{}, err
		}
		return token, next, expiry, nil
	}), nil
}

// ExtraHeaders identifies go-code to Kimi's subscription API on every request.
// Values are intentionally static and contain no user credentials.
func ExtraHeaders() map[string]string {
	return map[string]string{
		"X-Kimi-Client-Id":      ClientID,
		"X-Kimi-Client-Name":    "go-code",
		"X-Kimi-Client-Version": "1",
		"X-Kimi-Client-Ui-Mode": "cli",
	}
}

// RefreshFunc returns a token-cache compatible OAuth refresh function.
func RefreshFunc(endpoint string, client *http.Client) func(context.Context, string) (string, string, time.Time, error) {
	if endpoint == "" {
		endpoint = TokenEndpoint
	}
	if client == nil {
		client = http.DefaultClient
	}
	return func(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {ClientID}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("create Kimi token refresh request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := client.Do(req)
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("Kimi token refresh request failed")
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return "", "", time.Time{}, fmt.Errorf("Kimi token refresh returned HTTP %d", res.StatusCode)
		}
		var payload struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 1<<20)).Decode(&payload); err != nil {
			return "", "", time.Time{}, fmt.Errorf("decode Kimi token refresh response: %w", err)
		}
		if payload.AccessToken == "" || payload.ExpiresIn <= 0 {
			return "", "", time.Time{}, fmt.Errorf("Kimi token refresh response is missing required fields")
		}
		if payload.RefreshToken == "" {
			payload.RefreshToken = refreshToken
		}
		return payload.AccessToken, payload.RefreshToken, time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second), nil
	}
}
