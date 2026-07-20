package tui

import "testing"

func TestKimiSubscriptionAppearsInKeysEnvironmentMap(t *testing.T) {
	t.Setenv("KIMI_SUBSCRIPTION_AUTH", "configured")
	m := New(TUIConfig{})
	if !m.providerKeyConfigured("kimi-subscription") {
		t.Fatal("Kimi subscription status should be available to /keys without a token")
	}
}
