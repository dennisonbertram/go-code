package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Scope constants for API key permissions.
const (
	ScopeRunsRead  = "runs:read"
	ScopeRunsWrite = "runs:write"
	ScopeAdmin     = "admin"
)

// bcryptCost is the cost factor used when hashing API keys.
// Cost 12 provides a good balance of security and performance (~300ms on modern hardware).
const bcryptCost = 12

// keyPrefix is the prefix prepended to all generated API keys.
const keyPrefix = "harness_sk_"

// APIKey represents a stored API key record.
type APIKey struct {
	ID         string
	KeyHash    string // bcrypt hash of the raw key; NEVER log or return this in plaintext
	KeyPrefix  string // first 8 characters after "harness_sk_" for human identification
	TenantID   string
	Name       string   // human-readable label, e.g. "dennison's CLI"
	Scopes     []string // e.g. ["runs:write", "runs:read"]
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// APIKeyStore is the set of persistence operations for API keys.
// It is embedded in the Store interface.
type APIKeyStore interface {
	// CreateAPIKey persists a new API key record.
	// Returns an error if an API key with the same ID already exists.
	CreateAPIKey(ctx context.Context, key APIKey) error

	// ValidateAPIKey checks the raw token against all stored keys, updates
	// last_used_at on success, and returns the matching APIKey.
	// Returns ErrKeyNotFound if no matching key exists.
	// Returns ErrKeyExpired if the key has passed its ExpiresAt time.
	ValidateAPIKey(ctx context.Context, rawToken string) (*APIKey, error)

	// ListAPIKeys returns all API keys for a tenant (excluding key hashes).
	ListAPIKeys(ctx context.Context, tenantID string) ([]APIKey, error)

	// RevokeAPIKey removes an API key by ID.
	// Returns ErrKeyNotFound if the key does not exist.
	RevokeAPIKey(ctx context.Context, id string) error
}

// ErrKeyNotFound is returned when a requested API key does not exist.
type ErrKeyNotFound struct {
	ID string
}

func (e *ErrKeyNotFound) Error() string {
	if e.ID != "" {
		return "store: api key not found: " + e.ID
	}
	return "store: api key not found"
}

// IsKeyNotFound returns true if err is a key-not-found error.
func IsKeyNotFound(err error) bool {
	_, ok := err.(*ErrKeyNotFound)
	return ok
}

// ErrKeyExpired is returned by ValidateAPIKey when a key's ExpiresAt has passed.
var ErrKeyExpired = fmt.Errorf("store: api key expired")

// GenerateAPIKey creates a new random API key with the "harness_sk_" prefix.
// It returns the raw token (shown once, never stored) and an APIKey struct with
// the bcrypt hash already populated.
//
// The caller must persist the returned APIKey via Store.CreateAPIKey; the raw
// token cannot be recovered after this call returns.
func GenerateAPIKey(tenantID, name string, scopes []string) (rawToken string, key APIKey, err error) {
	// Generate 32 cryptographically-random bytes and encode as URL-safe base64.
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", APIKey{}, fmt.Errorf("store: generate key entropy: %w", err)
	}
	suffix := base64.RawURLEncoding.EncodeToString(raw) // 43 URL-safe chars
	rawToken = keyPrefix + suffix

	// Hash with bcrypt at cost 12.
	hash, err := bcrypt.GenerateFromPassword([]byte(rawToken), bcryptCost)
	if err != nil {
		return "", APIKey{}, fmt.Errorf("store: hash api key: %w", err)
	}

	// Key prefix = first 8 chars of the suffix for human display (not the full key).
	prefix := suffix
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}

	// Generate a unique ID for the key record.
	idBytes := make([]byte, 12)
	if _, err = rand.Read(idBytes); err != nil {
		return "", APIKey{}, fmt.Errorf("store: generate key id: %w", err)
	}

	scopesCopy := make([]string, len(scopes))
	copy(scopesCopy, scopes)

	key = APIKey{
		ID:        base64.RawURLEncoding.EncodeToString(idBytes),
		KeyHash:   string(hash),
		KeyPrefix: prefix,
		TenantID:  tenantID,
		Name:      name,
		Scopes:    scopesCopy,
		CreatedAt: time.Now().UTC(),
	}
	return rawToken, key, nil
}
