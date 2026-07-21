package oauth

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestParseResourceMetadataURL covers WWW-Authenticate parsing for the
// resource_metadata parameter (RFC 9728).
func TestParseResourceMetadataURL(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{
			name:   "quoted value",
			header: `Bearer resource_metadata="https://rs.example.com/.well-known/oauth-protected-resource"`,
			want:   "https://rs.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:   "with realm before",
			header: `Bearer realm="mcp", resource_metadata="https://rs.example.com/pr"`,
			want:   "https://rs.example.com/pr",
		},
		{
			name:   "with realm after",
			header: `Bearer resource_metadata="https://rs.example.com/pr", realm="mcp"`,
			want:   "https://rs.example.com/pr",
		},
		{
			name:   "unquoted token value",
			header: `Bearer resource_metadata=https://rs.example.com/pr`,
			want:   "https://rs.example.com/pr",
		},
		{
			name:   "extra whitespace",
			header: `Bearer   resource_metadata = "https://rs.example.com/pr" `,
			want:   "https://rs.example.com/pr",
		},
		{name: "no bearer scheme", header: `Basic realm="x"`, wantErr: true},
		{name: "no resource_metadata param", header: `Bearer realm="mcp"`, wantErr: true},
		{name: "empty", header: ``, wantErr: true},
		{name: "empty value", header: `Bearer resource_metadata=""`, wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseResourceMetadataURL(tc.header)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseResourceMetadataURL(%q): expected error, got %q", tc.header, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseResourceMetadataURL(%q): %v", tc.header, err)
			}
			if got != tc.want {
				t.Errorf("ParseResourceMetadataURL(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// TestPKCE_KnownVector checks the S256 challenge against the RFC 7636
// appendix B example.
func TestPKCE_KnownVector(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := CodeChallengeS256(verifier); got != want {
		t.Errorf("CodeChallengeS256(%q) = %q, want RFC 7636 vector %q", verifier, got, want)
	}
}

// TestPKCE_VerifierProperties verifies verifier shape and uniqueness.
func TestPKCE_VerifierProperties(t *testing.T) {
	v1, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	v2, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	// 32 bytes base64url-encoded without padding = 43 characters.
	if len(v1) != 43 {
		t.Errorf("verifier length = %d, want 43", len(v1))
	}
	if strings.ContainsAny(v1, "+/=") {
		t.Errorf("verifier %q is not base64url (no padding)", v1)
	}
	if v1 == v2 {
		t.Error("two verifiers should not be equal")
	}
}

// TestDiscover_CoLocatedASFallback verifies that when the resource server has
// no protected-resource document at all, discovery falls back to AS metadata
// at the resource origin (a co-located authorization server).
func TestDiscover_CoLocatedASFallback(t *testing.T) {
	// The mock AS serves its metadata at its origin; pointing the "resource"
	// at the same origin exercises the co-located fallback.
	as := newMockAuthorizationServer(t, nil)

	flow := &Flow{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	meta, err := flow.discoverASMetadata(ctx, as.url()+"/mcp")
	if err != nil {
		t.Fatalf("discoverASMetadata: %v", err)
	}
	if meta.AuthorizationEndpoint != as.url()+"/authorize" {
		t.Errorf("AuthorizationEndpoint = %q, want %q", meta.AuthorizationEndpoint, as.url()+"/authorize")
	}
	if meta.TokenEndpoint != as.url()+"/token" {
		t.Errorf("TokenEndpoint = %q, want %q", meta.TokenEndpoint, as.url()+"/token")
	}
	if meta.Issuer != as.url() {
		t.Errorf("Issuer = %q, want %q", meta.Issuer, as.url())
	}
}

// TestDiscover_Unreachable verifies a clear error when the resource server
// cannot be reached.
func TestDiscover_Unreachable(t *testing.T) {
	flow := &Flow{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Port 1 on loopback is closed.
	_, err := flow.discoverASMetadata(ctx, "http://127.0.0.1:1/mcp")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}
