// socialagent is a Telegram-facing social agent that delegates work to harnessd
// over HTTP. It never imports internal/ packages; all harness interaction is
// done via the public harnessd REST API.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"go-agent-harness/apps/socialagent/config"
	"go-agent-harness/apps/socialagent/db"
	"go-agent-harness/apps/socialagent/gateway"
	"go-agent-harness/apps/socialagent/harness"
	"go-agent-harness/apps/socialagent/mcpserver"
	"go-agent-harness/apps/socialagent/safety"
	"go-agent-harness/apps/socialagent/summarizer"
	"go-agent-harness/apps/socialagent/telegram"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("socialagent: configuration error: %v", err)
		os.Exit(1)
	}

	// Log config at startup, redacting sensitive values.
	log.Printf("socialagent: starting up")
	log.Printf("  harness_url    = %s", cfg.HarnessURL)
	log.Printf("  listen_addr    = %s", cfg.ListenAddr)
	log.Printf("  database_url   = %s", redact(cfg.DatabaseURL))
	log.Printf("  bot_token      = %s", redact(cfg.TelegramBotToken))
	log.Printf("  telegram_base  = %s", cfg.TelegramBaseURL)
	log.Printf("  mcp_server_url = %s", cfg.MCPServerURL)

	store, err := db.NewStore(cfg.DatabaseURL)
	if err != nil {
		log.Printf("socialagent: db: %v", err)
		os.Exit(1)
	}
	defer store.Close()

	var bot *telegram.Bot
	if cfg.TelegramBaseURL != "" {
		bot = telegram.NewBotWithBaseURL(cfg.TelegramBotToken, cfg.TelegramBaseURL)
	} else {
		bot = telegram.NewBot(cfg.TelegramBotToken)
	}
	harnessClient := harness.NewClient(cfg.HarnessURL)

	// Start MCP server on :8082 in the background (same process).
	mcpSrv := mcpserver.New(store, botDeliverer{bot})
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", mcpSrv.Handler())
	go func() {
		log.Printf("MCP server listening on :8082")
		if err := http.ListenAndServe(":8082", mcpMux); err != nil {
			log.Fatalf("MCP server error: %v", err)
		}
	}()

	// Create summarizer backed by the harness client and the store.
	sum := summarizer.New(harnessClient, store, cfg.HarnessURL)

	// Create safety screener if configured. When SAFETY_SCREENER_URL is empty,
	// the screener is nil and all messages pass through unscreened.
	var scr gateway.Screener
	if cfg.SafetyScreenerURL != "" {
		scr = safety.NewLlamaGuardScreener(cfg.SafetyScreenerURL)
		log.Printf("  safety_screener = %s", cfg.SafetyScreenerURL)
	}

	// Create gateway wiring all dependencies together.
	// store satisfies ProfileFetcher and ActivityLogger in addition to UserStore.
	gw := gateway.NewGateway(
		bot,
		store,
		harnessClient,
		cfg.WebhookSecret,
		store,
		sum,
		store,
		scr,
		cfg.MCPServerURL,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook/telegram", gw.HandleWebhook)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})

	log.Printf("socialagent listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, mux))
}

// botDeliverer adapts telegram.Bot to the mcpserver.MessageDeliverer interface.
type botDeliverer struct {
	bot interface {
		SendMessage(ctx context.Context, chatID int64, text string) error
	}
}

func (d botDeliverer) DeliverMessage(ctx context.Context, recipientTelegramID int64, text string) error {
	return d.bot.SendMessage(ctx, recipientTelegramID, text)
}

// redact replaces all but the first 4 characters of a string with asterisks,
// so tokens and URLs with credentials are not fully exposed in logs.
func redact(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
