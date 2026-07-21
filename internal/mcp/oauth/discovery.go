package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// asMetadata is the subset of OAuth authorization-server metadata (RFC 8414)
// the flow needs.
type asMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint,omitempty"`
	CodeChallengeMethods  []string `json:"code_challenge_methods_supported,omitempty"`
}

// protectedResourceMetadata is the subset of OAuth protected-resource
// metadata (RFC 9728) the flow needs.
type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// ParseResourceMetadataURL extracts the resource_metadata parameter from a
// WWW-Authenticate response header (RFC 9728 §5.1). It returns an error when
// the header does not carry the parameter.
func ParseResourceMetadataURL(header string) (string, error) {
	const key = "resource_metadata"
	rest := strings.TrimSpace(header)
	if rest == "" {
		return "", fmt.Errorf("oauth: WWW-Authenticate header is empty")
	}
	// Strip the auth scheme (the first token); the rest is a comma-separated
	// auth-param list.
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		rest = rest[i+1:]
	}
	// Split comma-separated auth-params outside of quoted strings.
	for _, part := range splitAuthParams(rest) {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(name) != key {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		if value == "" {
			break
		}
		return value, nil
	}
	return "", fmt.Errorf("oauth: WWW-Authenticate header has no %s parameter", key)
}

// splitAuthParams splits a WWW-Authenticate value on commas that are not
// inside quoted strings.
func splitAuthParams(s string) []string {
	var parts []string
	var b strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			b.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// discoverASMetadata resolves the authorization-server metadata for an MCP
// resource server:
//
//  1. Probe the resource URL without credentials; when the 401 response
//     carries WWW-Authenticate with a resource_metadata parameter, fetch the
//     protected-resource document from that URL (RFC 9728).
//  2. Otherwise fetch the well-known protected-resource document derived
//     from the resource URL.
//  3. Resolve the issuer from the document's authorization_servers, falling
//     back to the resource origin (co-located AS) when no document exists.
//  4. Fetch the AS metadata document (RFC 8414).
func (f *Flow) discoverASMetadata(ctx context.Context, resourceURL string) (*asMetadata, error) {
	u, err := url.Parse(resourceURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("oauth: invalid resource URL %q", resourceURL)
	}
	origin := u.Scheme + "://" + u.Host

	var issuer string

	// Step 1: probe for a WWW-Authenticate hint.
	prURL, probeErr := f.probeResourceMetadataURL(ctx, resourceURL)
	if probeErr != nil {
		return nil, probeErr
	}

	// Step 2: fetch the protected-resource document, from the header-provided
	// URL when present, otherwise from the well-known location.
	if prURL != "" {
		issuer, err = f.fetchAuthorizationServer(ctx, prURL)
		if err != nil {
			return nil, fmt.Errorf("oauth: protected resource metadata from WWW-Authenticate: %w", err)
		}
	} else {
		issuer, err = f.fetchAuthorizationServer(ctx, wellKnownURL(origin, u.EscapedPath(), "oauth-protected-resource"))
		if err != nil && u.EscapedPath() != "" && u.EscapedPath() != "/" {
			// Some servers only serve the bare origin form; retry it.
			issuer, err = f.fetchAuthorizationServer(ctx, wellKnownURL(origin, "", "oauth-protected-resource"))
		}
		if err != nil {
			// No protected-resource document: assume a co-located AS at the
			// resource origin.
			issuer = origin
		}
	}

	// Step 3: fetch the AS metadata document.
	issuerURL, err := url.Parse(issuer)
	if err != nil || issuerURL.Scheme == "" || issuerURL.Host == "" {
		return nil, fmt.Errorf("oauth: invalid authorization server %q", issuer)
	}
	issuerOrigin := issuerURL.Scheme + "://" + issuerURL.Host
	meta, err := f.fetchASMetadata(ctx, wellKnownURL(issuerOrigin, issuerURL.EscapedPath(), "oauth-authorization-server"))
	if err != nil && issuer != origin {
		// Last resort: a co-located AS at the resource origin.
		meta, err = f.fetchASMetadata(ctx, wellKnownURL(origin, "", "oauth-authorization-server"))
	}
	if err != nil {
		return nil, fmt.Errorf("oauth: discover authorization server metadata for %q: %w", resourceURL, err)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return nil, fmt.Errorf("oauth: authorization server metadata for %q is missing endpoints", resourceURL)
	}
	return meta, nil
}

// probeResourceMetadataURL performs an unauthenticated GET against the
// resource URL and extracts the resource_metadata parameter from a 401
// WWW-Authenticate header. It returns ("", nil) when the response carries no
// usable hint, leaving discovery to the well-known fallback.
func (f *Flow) probeResourceMetadataURL(ctx context.Context, resourceURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return "", fmt.Errorf("oauth: probe resource server: %w", err)
	}
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth: probe resource server %q: %w", resourceURL, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}
	prURL, err := ParseResourceMetadataURL(resp.Header.Get("WWW-Authenticate"))
	if err != nil {
		return "", nil // header without the parameter: fall back to well-known
	}
	return prURL, nil
}

// fetchAuthorizationServer fetches a protected-resource metadata document and
// returns its first authorization server.
func (f *Flow) fetchAuthorizationServer(ctx context.Context, metadataURL string) (string, error) {
	var doc protectedResourceMetadata
	if err := f.getJSON(ctx, metadataURL, &doc); err != nil {
		return "", err
	}
	if len(doc.AuthorizationServers) == 0 || doc.AuthorizationServers[0] == "" {
		return "", fmt.Errorf("oauth: protected resource metadata at %q lists no authorization servers", metadataURL)
	}
	return doc.AuthorizationServers[0], nil
}

// fetchASMetadata fetches and validates an AS metadata document.
func (f *Flow) fetchASMetadata(ctx context.Context, metadataURL string) (*asMetadata, error) {
	var meta asMetadata
	if err := f.getJSON(ctx, metadataURL, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// getJSON performs a GET and decodes a 2xx JSON body.
func (f *Flow) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("oauth: create request for %q: %w", rawURL, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("oauth: fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("oauth: fetch %q: HTTP %d", rawURL, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("oauth: decode %q: %w", rawURL, err)
	}
	return nil
}

// wellKnownURL builds a well-known URI by inserting "/.well-known/<name>"
// between the origin and the path, per RFC 8414 §3.1 / RFC 9728 §3.1. When
// the inserted form fails (some servers only serve the bare form), callers
// retry with the bare origin form; wellKnownURL with an empty path yields the
// bare form directly.
func wellKnownURL(origin, escapedPath, name string) string {
	base := strings.TrimSuffix(origin, "/") + "/.well-known/" + name
	path := strings.TrimPrefix(escapedPath, "/")
	if path != "" {
		base += "/" + path
	}
	return base
}
