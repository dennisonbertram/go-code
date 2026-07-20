package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/provider/codex"
)

func TestSubscriptionStoresAppearInKeysStartupHints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_SUBSCRIPTION_AUTH", "")

	codexStore := codex.DefaultStore()
	if err := codexStore.Save(codex.Credential{
		AccessToken: "header.payload.signature", RefreshToken: "refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save Codex store: %v", err)
	}
	kimiPath := filepath.Join(home, ".harness", "subscription-auth", "kimi.json")
	if err := os.MkdirAll(filepath.Dir(kimiPath), 0o700); err != nil {
		t.Fatalf("mkdir Kimi store: %v", err)
	}
	if err := os.WriteFile(kimiPath, []byte(`{"access_token":"access","refresh_token":"refresh","expires_at":4102444800}`), 0o600); err != nil {
		t.Fatalf("write Kimi store: %v", err)
	}

	m := New(TUIConfig{})
	if !m.providerKeyConfigured("codex-subscription") {
		t.Fatal("Codex subscription status should be available from its local credential store")
	}
	if !m.providerKeyConfigured("kimi-subscription") {
		t.Fatal("Kimi subscription status should be available from its local credential store")
	}
}
