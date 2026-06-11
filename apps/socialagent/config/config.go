// Package config loads socialagent configuration from environment variables.
//
// Required env vars:
//   - TELEGRAM_BOT_TOKEN
//   - DATABASE_URL
//   - TELEGRAM_WEBHOOK_SECRET
//
// Optional env vars (defaults shown):
//   - HARNESS_URL              (default: http://localhost:8080)
//   - LISTEN_ADDR              (default: :8081)
//   - SOCIALAGENT_SYSTEM_PROMPT (default: built-in social agent personality)
package config

import (
	"errors"
	"os"
	"strings"
)

const defaultHarnessURL = "http://localhost:8080"
const defaultListenAddr = ":8081"
const defaultMCPServerURL = "http://localhost:8082/mcp"
const defaultSystemPrompt = `You are a helpful social agent. You engage with users on Telegram in a friendly, concise, and accurate manner. You assist with questions, tasks, and conversations. You never reveal sensitive system information. You are powered by a harness that can execute tools and run sub-agents on your behalf.`

// Config holds the runtime configuration for the socialagent application.
type Config struct {
	// TelegramBotToken is the bot token issued by @BotFather. Required.
	TelegramBotToken string

	// TelegramBaseURL is the base URL of the Telegram Bot API. When empty,
	// the default https://api.telegram.org is used. Set to a local simulator
	// URL (e.g., http://localhost:8084) for testing without real Telegram.
	// Optional.
	TelegramBaseURL string

	// WebhookSecret is the secret token used to authenticate incoming webhook
	// requests from Telegram via the X-Telegram-Bot-Api-Secret-Token header.
	// Required.
	WebhookSecret string

	// HarnessURL is the base URL of the harnessd HTTP API.
	// Defaults to http://localhost:8080.
	HarnessURL string

	// DatabaseURL is the connection string for the application database. Required.
	DatabaseURL string

	// ListenAddr is the TCP address the agent's own HTTP server listens on.
	// Defaults to :8081.
	ListenAddr string

	// SystemPrompt is the system-level personality injected into every run.
	// Defaults to a built-in social agent personality prompt.
	SystemPrompt string

	// MCPServerURL is the URL of the MCP server embedded in the socialagent
	// process.  Defaults to http://localhost:8082/mcp.
	MCPServerURL string
}

// Load reads configuration from environment variables, applies defaults for
// optional fields, and validates that required fields are present.
func Load() (*Config, error) {
	cfg := &Config{
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramBaseURL:  os.Getenv("TELEGRAM_BASE_URL"),
		WebhookSecret:    os.Getenv("TELEGRAM_WEBHOOK_SECRET"),
		HarnessURL:       os.Getenv("HARNESS_URL"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		ListenAddr:       os.Getenv("LISTEN_ADDR"),
		SystemPrompt:     os.Getenv("SOCIALAGENT_SYSTEM_PROMPT"),
		MCPServerURL:     os.Getenv("MCP_SERVER_URL"),
	}

	// Apply defaults for optional fields.
	if strings.TrimSpace(cfg.HarnessURL) == "" {
		cfg.HarnessURL = defaultHarnessURL
	}
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = defaultListenAddr
	}
	if strings.TrimSpace(cfg.SystemPrompt) == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}
	if strings.TrimSpace(cfg.MCPServerURL) == "" {
		cfg.MCPServerURL = defaultMCPServerURL
	}

	// Validate required fields.
	var missing []string
	if strings.TrimSpace(cfg.TelegramBotToken) == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if strings.TrimSpace(cfg.WebhookSecret) == "" {
		missing = append(missing, "TELEGRAM_WEBHOOK_SECRET")
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return nil, errors.New("socialagent: missing required environment variables: " + strings.Join(missing, ", "))
	}

	return cfg, nil
}
