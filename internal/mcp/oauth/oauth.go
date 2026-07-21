// Package oauth implements the OAuth 2.1 authorization-code flow with PKCE
// that go-agent-harness uses to log in to remote MCP servers: metadata
// discovery (RFC 9728 / RFC 8414), a localhost loopback redirect listener,
// optional dynamic client registration (RFC 7591), token exchange, and
// silent refresh. Tokens are persisted through the mcp.TokenStore from
// epic #809 slice 2.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go-agent-harness/internal/mcp"
)

// ErrReauthRequired marks failures that only a fresh interactive login can
// fix — the AS rejected the refresh token (invalid_grant) or none is stored.
var ErrReauthRequired = errors.New("oauth: re-authentication required")

// Flow drives OAuth login and refresh for MCP servers. The zero value is
// usable except that Store is required.
type Flow struct {
	// HTTPClient is used for metadata, registration, and token endpoint
	// calls. Nil selects a default client with a 30s timeout.
	HTTPClient *http.Client
	// Store persists issued tokens. Required.
	Store *mcp.TokenStore
	// OpenURL opens the authorization URL in the user's browser. Nil selects
	// the platform default (open / xdg-open / rundll32). Tests inject an
	// in-process driver.
	OpenURL func(string) error
	// ClientName is advertised during dynamic client registration.
	// Empty selects "go-agent-harness".
	ClientName string
}

// LoginOptions configures one Login call.
type LoginOptions struct {
	// ServerName is the MCP server's configured name; the issued token is
	// stored under this key. Required.
	ServerName string
	// ResourceURL is the MCP server's HTTP endpoint URL. Required.
	ResourceURL string
	// ClientID is a pre-registered OAuth client ID. When empty and the
	// authorization server advertises a registration endpoint, a client is
	// registered dynamically (RFC 7591).
	ClientID string
	// Scopes optionally limits the requested scopes.
	Scopes []string
}

func (f *Flow) httpClient() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (f *Flow) clientName() string {
	if f.ClientName != "" {
		return f.ClientName
	}
	return "go-agent-harness"
}

// Login runs the full authorization-code + PKCE flow against the resource
// server in opts: discover the authorization server, open the browser at the
// authorization URL, wait for the loopback redirect, exchange the code, and
// persist the token in the store. The context bounds the whole operation —
// cancelling it (e.g. the user aborts) unblocks the wait for the browser.
func (f *Flow) Login(ctx context.Context, opts LoginOptions) (mcp.Token, error) {
	if f.Store == nil {
		return mcp.Token{}, fmt.Errorf("oauth: token store is required")
	}
	if strings.TrimSpace(opts.ServerName) == "" {
		return mcp.Token{}, fmt.Errorf("oauth: server name is required")
	}
	if strings.TrimSpace(opts.ResourceURL) == "" {
		return mcp.Token{}, fmt.Errorf("oauth: resource URL is required")
	}

	meta, err := f.discoverASMetadata(ctx, opts.ResourceURL)
	if err != nil {
		return mcp.Token{}, err
	}
	if len(meta.CodeChallengeMethods) > 0 && !slicesContains(meta.CodeChallengeMethods, "S256") {
		return mcp.Token{}, fmt.Errorf("oauth: authorization server %q does not support PKCE S256", meta.Issuer)
	}

	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return mcp.Token{}, err
	}
	state, err := generateState()
	if err != nil {
		return mcp.Token{}, err
	}

	// Start the loopback listener before registration so the redirect URI is
	// known when registering the client.
	cb, err := startCallbackListener()
	if err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: start loopback listener: %w", err)
	}
	defer cb.close()

	clientID := strings.TrimSpace(opts.ClientID)
	if clientID == "" {
		if meta.RegistrationEndpoint == "" {
			return mcp.Token{}, fmt.Errorf("oauth: no client ID configured for server %q and the authorization server does not support dynamic client registration; configure one in the server's headers/auth settings", opts.ServerName)
		}
		clientID, err = f.registerDynamicClient(ctx, meta.RegistrationEndpoint, cb.redirectURI)
		if err != nil {
			return mcp.Token{}, err
		}
	}

	authURL, err := buildAuthorizationURL(meta.AuthorizationEndpoint, authorizeParams{
		ClientID:      clientID,
		RedirectURI:   cb.redirectURI,
		State:         state,
		Scope:         strings.Join(opts.Scopes, " "),
		Resource:      opts.ResourceURL,
		CodeChallenge: CodeChallengeS256(verifier),
	})
	if err != nil {
		return mcp.Token{}, err
	}

	openURL := f.OpenURL
	if openURL == nil {
		openURL = openBrowser
	}
	if err := openURL(authURL); err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: could not open the browser: %w (open this URL manually: %s)", err, authURL)
	}

	res, err := cb.wait(ctx)
	if err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: login for server %q aborted: %w", opts.ServerName, err)
	}
	if res.errCode != "" {
		return mcp.Token{}, fmt.Errorf("oauth: authorization failed for server %q: %s (%s)", opts.ServerName, res.errCode, res.errDesc)
	}
	if res.state != state {
		return mcp.Token{}, fmt.Errorf("oauth: state mismatch for server %q: the callback does not belong to this login attempt", opts.ServerName)
	}
	if res.code == "" {
		return mcp.Token{}, fmt.Errorf("oauth: authorization response for server %q carried no code", opts.ServerName)
	}

	tr, err := f.postTokenRequest(ctx, meta.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {res.code},
		"redirect_uri":  {cb.redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: token exchange for server %q: %w", opts.ServerName, err)
	}

	tok := buildToken(meta.Issuer, clientID, opts.Scopes, mcp.Token{}, tr)
	if err := f.Store.Put(opts.ServerName, tok); err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: store token for server %q: %w", opts.ServerName, err)
	}
	return tok, nil
}

// Refresh silently renews the stored token for serverName using its refresh
// token and writes the result back to the store. When the AS does not rotate
// the refresh token, the previous one is preserved. An invalid_grant
// response maps to ErrReauthRequired and leaves the stored token untouched.
func (f *Flow) Refresh(ctx context.Context, serverName string) (mcp.Token, error) {
	if f.Store == nil {
		return mcp.Token{}, fmt.Errorf("oauth: token store is required")
	}

	tok, err := f.Store.Get(serverName)
	if err != nil {
		if errors.Is(err, mcp.ErrTokenExpired) {
			// An expired access token is exactly what Refresh is for; the
			// returned token still carries the refresh token.
		} else {
			return mcp.Token{}, fmt.Errorf("oauth: load token for server %q: %w", serverName, err)
		}
	}
	if tok.RefreshToken == "" {
		return mcp.Token{}, fmt.Errorf("oauth: no refresh token stored for server %q: %w", serverName, ErrReauthRequired)
	}
	if tok.Issuer == "" {
		return mcp.Token{}, fmt.Errorf("oauth: stored token for server %q has no issuer; cannot rediscover the authorization server: %w", serverName, ErrReauthRequired)
	}

	meta, err := f.discoverFromIssuer(ctx, tok.Issuer)
	if err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: rediscover authorization server for %q: %w", serverName, err)
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
	}
	if tok.ClientID != "" {
		form.Set("client_id", tok.ClientID)
	}
	tr, err := f.postTokenRequest(ctx, meta.TokenEndpoint, form)
	if err != nil {
		var oe *oauthError
		if errors.As(err, &oe) && oe.Code == "invalid_grant" {
			return mcp.Token{}, fmt.Errorf("oauth: refresh token for server %q was rejected; log in again: %w", serverName, ErrReauthRequired)
		}
		return mcp.Token{}, fmt.Errorf("oauth: refresh token for server %q: %w", serverName, err)
	}

	newTok := buildToken(tok.Issuer, tok.ClientID, tok.Scopes, tok, tr)
	if err := f.Store.Put(serverName, newTok); err != nil {
		return mcp.Token{}, fmt.Errorf("oauth: store refreshed token for server %q: %w", serverName, err)
	}
	return newTok, nil
}

// discoverFromIssuer fetches AS metadata starting from a known issuer.
func (f *Flow) discoverFromIssuer(ctx context.Context, issuer string) (*asMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("oauth: invalid issuer %q", issuer)
	}
	origin := u.Scheme + "://" + u.Host
	meta, err := f.fetchASMetadata(ctx, wellKnownURL(origin, u.EscapedPath(), "oauth-authorization-server"))
	if err != nil && u.EscapedPath() != "" && u.EscapedPath() != "/" {
		meta, err = f.fetchASMetadata(ctx, wellKnownURL(origin, "", "oauth-authorization-server"))
	}
	if err != nil {
		return nil, err
	}
	if meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("oauth: authorization server metadata for issuer %q has no token endpoint", issuer)
	}
	return meta, nil
}

// authorizeParams are the query parameters of an authorization request.
type authorizeParams struct {
	ClientID      string
	RedirectURI   string
	State         string
	Scope         string
	Resource      string
	CodeChallenge string
}

// buildAuthorizationURL assembles the authorization URL the browser opens.
func buildAuthorizationURL(endpoint string, p authorizeParams) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("oauth: invalid authorization endpoint %q", endpoint)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("resource", p.Resource) // RFC 8707
	if p.Scope != "" {
		q.Set("scope", p.Scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// registerDynamicClient registers a public client per RFC 7591 and returns
// the issued client ID.
func (f *Flow) registerDynamicClient(ctx context.Context, endpoint, redirectURI string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"client_name":                f.clientName(),
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", fmt.Errorf("oauth: encode registration request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("oauth: create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := f.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth: register client: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("oauth: register client: HTTP %d", resp.StatusCode)
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("oauth: decode registration response: %w", err)
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("oauth: registration response carried no client_id")
	}
	return out.ClientID, nil
}

// tokenResponse is the success body of a token endpoint response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// oauthError is an OAuth error response body (RFC 6749 §5.2).
type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// postTokenRequest submits a form to the token endpoint and decodes the
// success response; OAuth error bodies are returned as *oauthError.
func (f *Flow) postTokenRequest(ctx context.Context, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := f.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: call token endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var oe oauthError
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&oe); err == nil && oe.Code != "" {
			return nil, &oe
		}
		return nil, fmt.Errorf("oauth: token endpoint returned HTTP %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return nil, fmt.Errorf("oauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("oauth: token response carried no access_token")
	}
	return &tr, nil
}

// buildToken merges a token endpoint response into an mcp.Token. prev carries
// the values to preserve (refresh token, scopes) when the response omits them.
func buildToken(issuer, clientID string, requestedScopes []string, prev mcp.Token, tr *tokenResponse) mcp.Token {
	tok := mcp.Token{
		Issuer:       issuer,
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		ClientID:     clientID,
	}
	if tok.TokenType == "" {
		tok.TokenType = "Bearer"
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = prev.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	switch {
	case tr.Scope != "":
		tok.Scopes = strings.Fields(tr.Scope)
	case len(prev.Scopes) > 0:
		tok.Scopes = prev.Scopes
	default:
		tok.Scopes = requestedScopes
	}
	return tok
}

// callbackResult is the parsed loopback redirect.
type callbackResult struct {
	code    string
	state   string
	errCode string
	errDesc string
}

// callbackListener is the loopback HTTP server that receives the redirect.
type callbackListener struct {
	srv         *http.Server
	redirectURI string
	results     chan callbackResult
}

// startCallbackListener binds an ephemeral loopback port and serves the
// redirect handler until closed. The first /callback request wins.
func startCallbackListener() (*callbackListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	cb := &callbackListener{
		redirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port),
		results:     make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		res := callbackResult{
			code:    q.Get("code"),
			state:   q.Get("state"),
			errCode: q.Get("error"),
			errDesc: q.Get("error_description"),
		}
		select {
		case cb.results <- res:
		default:
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>go-code login</title><p>Login complete — you can close this tab and return to the terminal.</p>")
	})
	cb.srv = &http.Server{Handler: mux}
	go func() { _ = cb.srv.Serve(ln) }()
	return cb, nil
}

// wait blocks until the first callback arrives or ctx is done.
func (cb *callbackListener) wait(ctx context.Context) (callbackResult, error) {
	select {
	case res := <-cb.results:
		return res, nil
	case <-ctx.Done():
		return callbackResult{}, ctx.Err()
	}
}

// close shuts the listener down.
func (cb *callbackListener) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cb.srv.Shutdown(ctx)
}

func slicesContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
