package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// GenerateCodeVerifier returns a PKCE code verifier: 32 cryptographically
// random bytes, base64url-encoded without padding (43 characters, within the
// RFC 7636 §4.1 range of 43–128).
func GenerateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: generate code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CodeChallengeS256 derives the PKCE S256 code challenge for a verifier:
// base64url-no-padding of the SHA-256 digest (RFC 7636 §4.2).
func CodeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// generateState returns a random anti-CSRF state value for the authorization
// request: 16 random bytes, base64url-encoded without padding.
func generateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
