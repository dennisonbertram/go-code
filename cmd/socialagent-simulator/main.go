// socialagent-simulator is a local Telegram API simulator for testing the
// socialagent without sending real Telegram messages.
//
// Usage:
//
//	# Start the mock Telegram API server (runs in foreground):
//	socialagent-simulator serve \
//	    --addr=:8084 \
//	    --bot-token=test-bot-token
//
//	# Send a message as a simulated user (one-shot):
//	socialagent-simulator send \
//	    --webhook-url=http://localhost:8081/webhook/telegram \
//	    --webhook-secret=test-secret \
//	    --user-id=123 \
//	    --chat-id=456 \
//	    --first-name=Alice \
//	    --text="Hello bot!"
//
//	# Send a message and poll for responses:
//	socialagent-simulator send \
//	    --webhook-url=http://localhost:8081/webhook/telegram \
//	    --webhook-secret=test-secret \
//	    --user-id=123 \
//	    --chat-id=456 \
//	    --first-name=Alice \
//	    --text="Hello!" \
//	    --wait \
//	    --poll-url=http://localhost:8084/outbox
//
//	# Interactive terminal UI:
//	socialagent-simulator tui \
//	    --bot-token=test-bot-token \
//	    --addr=:8084 \
//	    --webhook-url=http://localhost:8081/webhook/telegram \
//	    --webhook-secret=test-secret
//
// Typical local development flow:
//
//	# Terminal 1: Start simulator server
//	socialagent-simulator serve --addr=:8084 --bot-token=test-token
//
//	# Terminal 2: Start socialagent (pointed at simulator)
//	TELEGRAM_BASE_URL=http://localhost:8084 \
//	TELEGRAM_BOT_TOKEN=test-token \
//	TELEGRAM_WEBHOOK_SECRET=test-secret \
//	DATABASE_URL=postgres://... \
//	OPENAI_API_KEY=sk-... \
//	socialagent
//
//	# Terminal 3: Send test messages
//	socialagent-simulator send \
//	    --webhook-url=http://localhost:8081/webhook/telegram \
//	    --webhook-secret=test-secret \
//	    --user-id=123 --chat-id=456 --first-name=Alice \
//	    --text="Hey bot, what can you do?" \
//	    --wait --poll-url=http://localhost:8084/outbox
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go-agent-harness/apps/socialagent/telegram/simulator"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		serveCmd()
	case "send":
		sendCmd()
	case "tui":
		tuiCmd()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  socialagent-simulator serve [flags]   Start mock Telegram API server
  socialagent-simulator send [flags]    Send a message as a simulated user
  socialagent-simulator tui [flags]     Interactive terminal UI
`)
}

// --- serve command ---

func serveCmd() {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8084", "listen address for mock Telegram API server")
	botToken := fs.String("bot-token", "", "bot token to match in API URL path")
	_ = fs.Parse(os.Args[2:])

	if *botToken == "" {
		fmt.Fprintln(os.Stderr, "error: --bot-token is required for serve mode")
		fmt.Fprintln(os.Stderr, "(use any string that matches the socialagent's TELEGRAM_BOT_TOKEN)")
		os.Exit(1)
	}

	sim := simulator.New(*addr, *botToken)

	// Graceful shutdown on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("simulator: shutting down...")
		os.Exit(0)
	}()

	log.Printf("Telegram API Simulator starting on %s", *addr)
	log.Printf("  Endpoints:")
	log.Printf("    POST /bot%s/sendMessage  ← socialagent sends replies here", *botToken)
	log.Printf("    POST /bot%s/setWebhook   ← socialagent registers webhook here", *botToken)
	log.Printf("    GET  /outbox             ← read captured outbound messages")
	log.Printf("    GET  /health             ← health check")
	log.Println()

	if err := sim.ListenAndServe(); err != nil {
		log.Fatalf("simulator: %v", err)
	}
}

// --- send command ---

func sendCmd() {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	webhookURL := fs.String("webhook-url", "http://localhost:8081/webhook/telegram", "socialagent webhook URL")
	webhookSecret := fs.String("webhook-secret", "", "webhook secret token (required)")
	userID := fs.Int64("user-id", 0, "simulated Telegram user ID (required)")
	chatID := fs.Int64("chat-id", 0, "simulated Telegram chat ID (defaults to user-id)")
	firstName := fs.String("first-name", "TestUser", "simulated user's first name")
	lastName := fs.String("last-name", "", "simulated user's last name")
	username := fs.String("username", "", "simulated user's username")
	text := fs.String("text", "", "message text to send (required; use '-' to read from stdin)")
	messageID := fs.Int("message-id", 0, "message ID (auto-generated if 0)")
	updateID := fs.Int("update-id", 0, "update ID (auto-generated if 0)")
	wait := fs.Bool("wait", false, "wait and poll for bot responses")
	pollURL := fs.String("poll-url", "http://localhost:8084/outbox", "URL to poll for outbound messages")
	pollTimeout := fs.Duration("poll-timeout", 30*time.Second, "max time to wait for responses")
	_ = fs.Parse(os.Args[2:])

	if *webhookSecret == "" {
		fmt.Fprintln(os.Stderr, "error: --webhook-secret is required")
		os.Exit(1)
	}
	if *userID == 0 {
		fmt.Fprintln(os.Stderr, "error: --user-id is required")
		os.Exit(1)
	}
	if *chatID == 0 {
		*chatID = *userID
	}

	msgText := *text
	if msgText == "-" {
		// Read from stdin.
		scanner := bufio.NewScanner(os.Stdin)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		msgText = strings.Join(lines, "\n")
	}
	if strings.TrimSpace(msgText) == "" {
		fmt.Fprintln(os.Stderr, "error: --text is required (non-empty message)")
		os.Exit(1)
	}

	msg := simulator.Message{
		UserID:    *userID,
		ChatID:    *chatID,
		FirstName: *firstName,
		LastName:  *lastName,
		Username:  *username,
		Text:      msgText,
		MessageID: *messageID,
		UpdateID:  *updateID,
	}

	sim := simulator.New("", "unused") // only used for SendUpdate
	ctx := context.Background()

	fmt.Printf("Sending message from %s (user=%d, chat=%d)...\n", msg.FirstName, msg.UserID, msg.ChatID)
	fmt.Printf("  Text: %s\n", truncateForDisplay(msg.Text, 100))

	status, respBody, err := sim.SendUpdate(ctx, *webhookURL, *webhookSecret, msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: sending message: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Webhook response: HTTP %d\n", status)
	if respBody != "" && len(respBody) < 200 {
		fmt.Printf("  Body: %s\n", respBody)
	}

	if *wait {
		fmt.Println("\nWaiting for bot response...")
		outbox, err := pollForMessages(ctx, *pollURL, *pollTimeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: polling failed: %v\n", err)
		} else if len(outbox) == 0 {
			fmt.Println("No bot responses captured within timeout.")
		} else {
			fmt.Println("\nBot responses:")
			for i, m := range outbox {
				fmt.Printf("  [%d] chat_id=%d: %s\n", i+1, m.ChatID, m.Text)
			}
		}
	}
}

// --- tui command ---

func tuiCmd() {
	fmt.Fprintf(os.Stderr, "TUI mode is not yet implemented. Use 'send' mode for now.\n")
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Example interactive loop with 'send':\n")
	fmt.Fprintf(os.Stderr, "  while true; do\n")
	fmt.Fprintf(os.Stderr, "    read -p 'User> ' input\n")
	fmt.Fprintf(os.Stderr, "    [ -z \"$input\" ] && continue\n")
	fmt.Fprintf(os.Stderr, "    socialagent-simulator send \\\n")
	fmt.Fprintf(os.Stderr, "      --webhook-url=http://localhost:8081/webhook/telegram \\\n")
	fmt.Fprintf(os.Stderr, "      --webhook-secret=test-secret \\\n")
	fmt.Fprintf(os.Stderr, "      --user-id=123 --chat-id=456 --first-name=Alice \\\n")
	fmt.Fprintf(os.Stderr, "      --text=\"$input\" \\\n")
	fmt.Fprintf(os.Stderr, "      --wait --poll-url=http://localhost:8084/outbox\n")
	fmt.Fprintf(os.Stderr, "  done\n")
	os.Exit(0)
}

// --- helpers ---

func pollForMessages(ctx context.Context, pollURL string, timeout time.Duration) ([]simulator.CapturedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout after %v", timeout)
		case <-ticker.C:
			msgs, err := fetchOutbox(ctx, pollURL)
			if err != nil {
				return nil, err
			}
			if len(msgs) > 0 {
				return msgs, nil
			}
		}
	}
}

func fetchOutbox(ctx context.Context, url string) ([]simulator.CapturedMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var msgs []simulator.CapturedMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		// Empty or invalid response — return empty.
		return nil, nil
	}
	return msgs, nil
}

func truncateForDisplay(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
