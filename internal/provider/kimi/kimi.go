// Package kimi implements Kimi Code subscription credentials. It never writes
// the vendor CLI credential file; only the separate harness store is mutable.
package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TokenEndpoint = "https://auth.kimi.com/api/oauth/token"
	// ClientID is the public Kimi Code OAuth client identifier used by the
	// convention-based refresh request. Live body verification remains pending.
	ClientID     = "kimi-code"
	SafetyMargin = 30 * time.Second
)

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
