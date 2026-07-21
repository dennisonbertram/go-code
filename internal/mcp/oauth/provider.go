package oauth

import (
	"context"
	"errors"

	"go-agent-harness/internal/mcp"
)

// TokenProvider adapts the flow's token store into an mcp.TokenProviderFunc
// suitable for ServerConfig.TokenProvider or ClientManager.SetTokenProvider:
// valid stored tokens are returned as-is, expired tokens are silently
// refreshed through Refresh, and a missing token yields ("", nil) so the
// transport sends the request unauthenticated and the server's own 401
// guides the user to `harnesscli mcp login`. Refresh failures (including
// ErrReauthRequired) are surfaced to the caller.
func (f *Flow) TokenProvider() mcp.TokenProviderFunc {
	return func(ctx context.Context, serverName string) (string, error) {
		tok, err := f.Store.Get(serverName)
		switch {
		case errors.Is(err, mcp.ErrTokenNotFound):
			return "", nil
		case errors.Is(err, mcp.ErrTokenExpired):
			refreshed, rerr := f.Refresh(ctx, serverName)
			if rerr != nil {
				return "", rerr
			}
			return refreshed.AccessToken, nil
		case err != nil:
			return "", err
		default:
			return tok.AccessToken, nil
		}
	}
}
