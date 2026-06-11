package config_test

import (
	"os"
	"testing"

	"go-agent-harness/apps/socialagent/config"
)

// clearEnv removes all socialagent-relevant env vars so each test starts clean.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TELEGRAM_BOT_TOKEN",
		"TELEGRAM_BASE_URL",
		"TELEGRAM_WEBHOOK_SECRET",
		"HARNESS_URL",
		"DATABASE_URL",
		"LISTEN_ADDR",
		"SOCIALAGENT_SYSTEM_PROMPT",
		"MCP_SERVER_URL",
	} {
		t.Setenv(key, "") // register for cleanup
		os.Unsetenv(key)
	}
}

// TestLoad_MissingTelegramBotToken ensures Load returns an error when
// TELEGRAM_BOT_TOKEN is absent.
func TestLoad_MissingTelegramBotToken(t *testing.T) {
	clearEnv(t)
	os.Setenv("DATABASE_URL", "postgres://localhost/testdb")
	os.Setenv("TELEGRAM_WEBHOOK_SECRET", "test-secret")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when TELEGRAM_BOT_TOKEN is missing, got nil")
	}
}

// TestLoad_MissingDatabaseURL ensures Load returns an error when DATABASE_URL
// is absent.
func TestLoad_MissingDatabaseURL(t *testing.T) {
	clearEnv(t)
	os.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	os.Setenv("TELEGRAM_WEBHOOK_SECRET", "test-secret")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing, got nil")
	}
}

// TestLoad_MissingBothRequired ensures Load returns an error when both required
// fields are absent.
func TestLoad_MissingBothRequired(t *testing.T) {
	clearEnv(t)

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when both required env vars are missing, got nil")
	}
}

// TestLoad_Defaults verifies that optional fields get their default values when
// the corresponding env vars are not set.
func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	os.Setenv("TELEGRAM_BOT_TOKEN", "tok-abc")
	os.Setenv("DATABASE_URL", "postgres://localhost/db")
	os.Setenv("TELEGRAM_WEBHOOK_SECRET", "test-secret")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.HarnessURL != "http://localhost:8080" {
		t.Errorf("HarnessURL default: got %q, want %q", cfg.HarnessURL, "http://localhost:8080")
	}
	if cfg.ListenAddr != ":8081" {
		t.Errorf("ListenAddr default: got %q, want %q", cfg.ListenAddr, ":8081")
	}
	if cfg.SystemPrompt == "" {
		t.Error("SystemPrompt default must not be empty")
	}
	if cfg.MCPServerURL != "http://localhost:8082/mcp" {
		t.Errorf("MCPServerURL default: got %q, want %q", cfg.MCPServerURL, "http://localhost:8082/mcp")
	}
	if cfg.TelegramBaseURL != "" {
		t.Errorf("TelegramBaseURL default: got %q, want empty string", cfg.TelegramBaseURL)
	}
}

// TestLoad_AllEnvVars verifies that every env var is read and stored correctly.
func TestLoad_AllEnvVars(t *testing.T) {
	clearEnv(t)

	want := config.Config{
		TelegramBotToken: "tok-xyz",
		TelegramBaseURL:  "http://localhost:8084",
		WebhookSecret:    "my-webhook-secret",
		HarnessURL:       "http://harness:9090",
		DatabaseURL:      "postgres://user:pass@db/socialagent",
		ListenAddr:       ":9000",
		SystemPrompt:     "you are a custom agent",
		MCPServerURL:     "http://mcp:8082/mcp",
	}

	os.Setenv("TELEGRAM_BOT_TOKEN", want.TelegramBotToken)
	os.Setenv("TELEGRAM_BASE_URL", want.TelegramBaseURL)
	os.Setenv("TELEGRAM_WEBHOOK_SECRET", want.WebhookSecret)
	os.Setenv("HARNESS_URL", want.HarnessURL)
	os.Setenv("DATABASE_URL", want.DatabaseURL)
	os.Setenv("LISTEN_ADDR", want.ListenAddr)
	os.Setenv("SOCIALAGENT_SYSTEM_PROMPT", want.SystemPrompt)
	os.Setenv("MCP_SERVER_URL", want.MCPServerURL)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.TelegramBotToken != want.TelegramBotToken {
		t.Errorf("TelegramBotToken: got %q, want %q", cfg.TelegramBotToken, want.TelegramBotToken)
	}
	if cfg.TelegramBaseURL != want.TelegramBaseURL {
		t.Errorf("TelegramBaseURL: got %q, want %q", cfg.TelegramBaseURL, want.TelegramBaseURL)
	}
	if cfg.WebhookSecret != want.WebhookSecret {
		t.Errorf("WebhookSecret: got %q, want %q", cfg.WebhookSecret, want.WebhookSecret)
	}
	if cfg.HarnessURL != want.HarnessURL {
		t.Errorf("HarnessURL: got %q, want %q", cfg.HarnessURL, want.HarnessURL)
	}
	if cfg.DatabaseURL != want.DatabaseURL {
		t.Errorf("DatabaseURL: got %q, want %q", cfg.DatabaseURL, want.DatabaseURL)
	}
	if cfg.ListenAddr != want.ListenAddr {
		t.Errorf("ListenAddr: got %q, want %q", cfg.ListenAddr, want.ListenAddr)
	}
	if cfg.SystemPrompt != want.SystemPrompt {
		t.Errorf("SystemPrompt: got %q, want %q", cfg.SystemPrompt, want.SystemPrompt)
	}
	if cfg.MCPServerURL != want.MCPServerURL {
		t.Errorf("MCPServerURL: got %q, want %q", cfg.MCPServerURL, want.MCPServerURL)
	}
}

// TestLoad_MissingWebhookSecret ensures Load returns an error when
// TELEGRAM_WEBHOOK_SECRET is absent.
func TestLoad_MissingWebhookSecret(t *testing.T) {
	clearEnv(t)
	os.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	os.Setenv("DATABASE_URL", "postgres://localhost/testdb")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when TELEGRAM_WEBHOOK_SECRET is missing, got nil")
	}
}
