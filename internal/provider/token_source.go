package provider

import "context"

// TokenSource supplies a bearer credential for an outbound provider request.
// Implementations may return a static credential or refresh an expiring one.
// Implementations must never log credential values.
type TokenSource interface {
	Token(context.Context) (string, error)
}

// StaticToken adapts a static credential to TokenSource.
type StaticToken string

// Token returns the configured static credential.
func (t StaticToken) Token(context.Context) (string, error) {
	return string(t), nil
}
