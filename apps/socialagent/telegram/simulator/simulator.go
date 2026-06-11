// Package simulator provides a local Telegram Bot API simulator for testing
// the socialagent without sending real Telegram messages.
//
// The simulator serves two roles:
//  1. It impersonates the Telegram Bot API (api.telegram.org) so that the
//     socialagent's SendMessage calls are intercepted and captured locally.
//  2. It provides a Sender that POSTs properly-formatted Update JSON to the
//     socialagent's /webhook/telegram endpoint, simulating real users typing
//     messages in the Telegram app.
//
// Usage with socialagent:
//
//	sim := simulator.New(":8084", "test-token")
//	go sim.ListenAndServe()
//
//	// Point socialagent at the simulator via TELEGRAM_BASE_URL env var.
//	// The socialagent will call POST /bottest-token/sendMessage on the
//	// simulator instead of the real Telegram API.
//
//	// Send a message as a simulated user:
//	msg := simulator.Message{
//	    UserID:    123,
//	    ChatID:    456,
//	    FirstName: "Alice",
//	    Text:      "Hello bot!",
//	}
//	resp, err := sim.SendUpdate(ctx,
//	    "http://localhost:8081/webhook/telegram",
//	    "test-webhook-secret",
//	    msg)
//
//	// Read captured outbound messages:
//	outbox := sim.Outbox()
package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// CapturedMessage represents a sendMessage call that was intercepted by the
// simulator.
type CapturedMessage struct {
	ChatID    int64     `json:"chat_id"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Message represents a simulated incoming user message.
type Message struct {
	UserID    int64  `json:"user_id"`
	ChatID    int64  `json:"chat_id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
	Text      string `json:"text"`
	MessageID int    `json:"message_id"`   // auto-incremented if 0
	UpdateID  int    `json:"update_id"`    // auto-incremented if 0
}

// Simulator is a local mock of the Telegram Bot API.
type Simulator struct {
	listenAddr string
	botToken   string

	mu      sync.Mutex
	outbox  []CapturedMessage
	updateN int
	msgN    int
}

// New creates a new Simulator. listenAddr is the TCP address the mock HTTP
// server binds to (e.g., ":8084"). botToken is the token the socialagent
// uses; the simulator responds to requests matching /bot{token}/...
func New(listenAddr, botToken string) *Simulator {
	return &Simulator{
		listenAddr: listenAddr,
		botToken:   botToken,
	}
}

// ListenAndServe starts the HTTP server and blocks until the server exits.
func (s *Simulator) ListenAndServe() error {
	mux := http.NewServeMux()
	s.registerHandlers(mux)
	server := &http.Server{
		Addr:         s.listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	log.Printf("simulator: listening on %s (bot token prefix: %s)", s.listenAddr, redact(s.botToken))
	return server.ListenAndServe()
}

// registerHandlers adds all the mock Telegram API routes to mux.
func (s *Simulator) registerHandlers(mux *http.ServeMux) {
	prefix := "/bot" + s.botToken + "/"

	// sendMessage — capture the message and return a Telegram-like success.
	mux.HandleFunc(prefix+"sendMessage", s.handleSendMessage)

	// setWebhook — accept the webhook registration; no-op.
	mux.HandleFunc(prefix+"setWebhook", s.handleSetWebhook)

	// Outbox returns captured outbound messages (GET /outbox).
	mux.HandleFunc("GET /outbox", s.handleOutbox)

	// Health check.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})
}

// Outbox returns a copy of all captured outbound messages.
func (s *Simulator) Outbox() []CapturedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CapturedMessage, len(s.outbox))
	copy(out, s.outbox)
	return out
}

// PopOutbox returns and clears all captured outbound messages.
func (s *Simulator) PopOutbox() []CapturedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.outbox
	s.outbox = nil
	return out
}

// NextMessageID returns an auto-incremented message ID number.
func (s *Simulator) nextMessageID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgN++
	return s.msgN
}

// nextUpdateID returns an auto-incremented update ID number.
func (s *Simulator) nextUpdateID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateN++
	return s.updateN
}

// SendUpdate formats msg as a Telegram Update JSON payload and POSTs it to
// the socialagent's webhookURL with the given webhookSecret in the
// X-Telegram-Bot-Api-Secret-Token header. It returns the HTTP status code
// and response body.
func (s *Simulator) SendUpdate(ctx context.Context, webhookURL, webhookSecret string, msg Message) (int, string, error) {
	if msg.MessageID == 0 {
		msg.MessageID = s.nextMessageID()
	}
	if msg.UpdateID == 0 {
		msg.UpdateID = s.nextUpdateID()
	}

	update := map[string]interface{}{
		"update_id": msg.UpdateID,
		"message": map[string]interface{}{
			"message_id": msg.MessageID,
			"from": map[string]interface{}{
				"id":         msg.UserID,
				"first_name": msg.FirstName,
				"last_name":  msg.LastName,
				"username":   msg.Username,
			},
			"chat": map[string]interface{}{
				"id": msg.ChatID,
			},
			"text": msg.Text,
			"date": time.Now().Unix(),
		},
	}

	body, err := json.Marshal(update)
	if err != nil {
		return 0, "", fmt.Errorf("simulator: marshal update: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return 0, "", fmt.Errorf("simulator: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", webhookSecret)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("simulator: POST webhook: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("simulator: sent update_id=%d to %s → HTTP %d", msg.UpdateID, webhookURL, resp.StatusCode)
	return resp.StatusCode, string(respBody), nil
}

// --- HTTP handlers ---

func (s *Simulator) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read body: "+err.Error())
		return
	}

	var req struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	s.mu.Lock()
	s.outbox = append(s.outbox, CapturedMessage{
		ChatID:    req.ChatID,
		Text:      req.Text,
		Timestamp: time.Now(),
	})
	s.mu.Unlock()

	log.Printf("simulator: captured sendMessage → chat_id=%d text=%q", req.ChatID, truncate(req.Text, 80))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true,"result":{"message_id":1,"from":{"id":999,"is_bot":true,"first_name":"Bot","username":"test_bot"},"chat":{"id":` + fmt.Sprintf("%d", req.ChatID) + `,"first_name":"User","type":"private"},"date":` + fmt.Sprintf("%d", time.Now().Unix()) + `,"text":"` + escapeJSON(req.Text) + `"}}`)) //nolint:errcheck
}

func (s *Simulator) handleSetWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "only POST")
		return
	}

	var req struct {
		URL         string `json:"url"`
		SecretToken string `json:"secret_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Body may be empty for deregistration — still return ok.
	}
	_ = req // accepted, no-op in simulator

	log.Printf("simulator: setWebhook accepted (simulated)")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true,"result":true,"description":"webhook registered (simulated)"}`)) //nolint:errcheck
}

func (s *Simulator) handleOutbox(w http.ResponseWriter, r *http.Request) {
	msgs := s.PopOutbox()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(msgs); err != nil {
		log.Printf("simulator: encode outbox: %v", err)
	}
}

// --- helpers ---

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"ok":false,"description":%q}`, msg) //nolint:errcheck
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// escapeJSON escapes a string for embedding in JSON. This is a minimal
// implementation that handles double quotes and backslashes.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

func redact(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
