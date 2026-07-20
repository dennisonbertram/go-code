package provider

import (
	"context"
	"testing"
)

func TestStaticTokenReturnsConfiguredValue(t *testing.T) {
	t.Parallel()

	const credential = "fake-static-credential"
	got, err := StaticToken(credential).Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if got != credential {
		t.Fatal("Token() did not return the configured static credential")
	}
}
