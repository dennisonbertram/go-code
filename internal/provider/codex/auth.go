// Package codex implements the credential contract for the ChatGPT-backed
// Codex subscription provider. It never reads or writes the vendor CLI store.
package codex

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
	// ClientID is the public OAuth client used by the vendor Codex CLI.
	ClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// OAuthTokenURL exchanges a refresh credential for a replacement pair.
	OAuthTokenURL = "https://auth.openai.com/oauth/token"
)

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// NewRefreshFunc creates the RefreshFunc used with provider/tokencache. The
// endpoint and clock are injectable solely for deterministic tests.
func NewRefreshFunc(client *http.Client, endpoint string, now func() time.Time) func(context.Context, string) (string, string, time.Time, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if endpoint == "" {
		endpoint = OAuthTokenURL
	}
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"client_id":     {ClientID},
			"refresh_token": {refreshToken},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("create Codex OAuth refresh request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			return "", "", time.Time{}, fmt.Errorf("send Codex OAuth refresh request: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return "", "", time.Time{}, fmt.Errorf("Codex OAuth refresh failed with status %d", resp.StatusCode)
		}
		var payload oauthTokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return "", "", time.Time{}, fmt.Errorf("decode Codex OAuth refresh response: %w", err)
		}
		if payload.AccessToken == "" || payload.RefreshToken == "" || payload.ExpiresIn <= 0 {
			return "", "", time.Time{}, fmt.Errorf("Codex OAuth refresh response was incomplete")
		}
		return payload.AccessToken, payload.RefreshToken, now().Add(time.Duration(payload.ExpiresIn) * time.Second), nil
	}
}
