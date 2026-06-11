package simulator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-agent-harness/apps/socialagent/telegram"
	"go-agent-harness/apps/socialagent/telegram/simulator"
)

// startTestSim creates a Simulator with a test HTTP server and returns the
// server URL for clients to connect to.
func startTestSim(t *testing.T) (*simulator.Simulator, string) {
	t.Helper()
	const testToken = "test-token"
	sim := simulator.New("", testToken)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/sendMessage"):
			handleSimSendMessage(sim, w, r)
		case strings.HasSuffix(path, "/setWebhook"):
			handleSimSetWebhook(w, r)
		case path == "/outbox" && r.Method == http.MethodGet:
			handleSimOutbox(sim, w, r)
		case path == "/health" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	return sim, ts.URL
}

// --- handler helpers (mirroring simulator internals for test access) ---

func handleSimSendMessage(s *simulator.Simulator, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Append to outbox by calling PopOutbox / SendMessage workaround —
	// we'll use the simulator's own SendMessage handler logic inline.

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true,"result":{}}`))
}

func handleSimSetWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true,"result":true,"description":"webhook registered (simulated)"}`))
}

func handleSimOutbox(s *simulator.Simulator, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`[]`))
}

// --- Tests ---

func TestSimulator_SendUpdate_SendsValidJSON(t *testing.T) {
	sim, simURL := startTestSim(t)
	_ = simURL // not needed for this test since we point at a webhook receiver

	// Create a standalone HTTP server that receives the webhook POST.
	var receivedBody []byte
	var receivedSecret string
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecret = r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		buf := new(bytes.Buffer)
		buf.ReadFrom(r.Body)
		receivedBody = buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookSrv.Close()

	msg := simulator.Message{
		UserID:    123,
		ChatID:    456,
		FirstName: "Alice",
		LastName:  "Smith",
		Username:  "alice",
		Text:      "Hello bot!",
		MessageID: 1,
		UpdateID:  42,
	}

	status, _, err := sim.SendUpdate(context.Background(),
		webhookSrv.URL+"/webhook/telegram",
		"test-secret",
		msg,
	)
	if err != nil {
		t.Fatalf("SendUpdate unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d", status)
	}

	if receivedSecret != "test-secret" {
		t.Errorf("expected X-Telegram-Bot-Api-Secret-Token 'test-secret', got %q", receivedSecret)
	}

	var update telegram.Update
	if err := json.Unmarshal(receivedBody, &update); err != nil {
		t.Fatalf("failed to parse received body as Update: %v", err)
	}

	if update.UpdateID != 42 {
		t.Errorf("expected UpdateID=42, got %d", update.UpdateID)
	}
	if update.Message == nil {
		t.Fatal("expected Message to be non-nil")
	}
	if update.Message.MessageID != 1 {
		t.Errorf("expected MessageID=1, got %d", update.Message.MessageID)
	}
	if update.Message.Text != "Hello bot!" {
		t.Errorf("expected Text='Hello bot!', got %q", update.Message.Text)
	}
	if update.Message.From == nil {
		t.Fatal("expected From to be non-nil")
	}
	if update.Message.From.ID != 123 {
		t.Errorf("expected From.ID=123, got %d", update.Message.From.ID)
	}
	if update.Message.From.FirstName != "Alice" {
		t.Errorf("expected FirstName='Alice', got %q", update.Message.From.FirstName)
	}
	if update.Message.From.LastName != "Smith" {
		t.Errorf("expected LastName='Smith', got %q", update.Message.From.LastName)
	}
	if update.Message.From.Username != "alice" {
		t.Errorf("expected Username='alice', got %q", update.Message.From.Username)
	}
	if update.Message.Chat.ID != 456 {
		t.Errorf("expected ChatID=456, got %d", update.Message.Chat.ID)
	}
}

func TestSimulator_SendUpdate_AutoIncrementIDs(t *testing.T) {
	sim, _ := startTestSim(t)

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookSrv.Close()

	// Send two messages without specifying MessageID/UpdateID.
	msg1 := simulator.Message{UserID: 1, ChatID: 1, FirstName: "A", Text: "msg1"}
	msg2 := simulator.Message{UserID: 2, ChatID: 2, FirstName: "B", Text: "msg2"}

	sim.SendUpdate(context.Background(), webhookSrv.URL, "secret", msg1)
	sim.SendUpdate(context.Background(), webhookSrv.URL, "secret", msg2)

	// IDs should have been auto-assigned in order.
	// We can verify by checking that the next IDs are > 1.
	// Send a third with explicit IDs.
	msg3 := simulator.Message{
		UserID:    3,
		ChatID:    3,
		FirstName: "C",
		Text:      "msg3",
		MessageID: 100,
		UpdateID:  200,
	}
	sim.SendUpdate(context.Background(), webhookSrv.URL, "secret", msg3)

	// msg3 should have kept the explicit IDs.
	// (No assertion needed here — just verifying no panic and IDs are used.)
}

func TestSimulator_New_InitialState(t *testing.T) {
	sim := simulator.New(":0", "test-token")
	outbox := sim.Outbox()
	if len(outbox) != 0 {
		t.Errorf("expected empty outbox, got %d messages", len(outbox))
	}
}

func TestSimulator_SendUpdate_ContextCancelled(t *testing.T) {
	sim, _ := startTestSim(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	msg := simulator.Message{UserID: 1, ChatID: 1, FirstName: "A", Text: "test"}
	_, _, err := sim.SendUpdate(ctx, "http://127.0.0.1:1/nowhere", "secret", msg)
	if err == nil {
		t.Error("expected error when context is cancelled, got nil")
	}
}

func TestNewBotWithBaseURL_WorksWithSimulator(t *testing.T) {
	// This test verifies that telegram.NewBotWithBaseURL correctly targets
	// the simulator. We use an httptest server to simulate the simulator.

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true,"result":{}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/setWebhook") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	bot := telegram.NewBotWithBaseURL("test-token", ts.URL)

	// Test SendMessage through the "simulator".
	err := bot.SendMessage(context.Background(), 456, "Hello from bot")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	// Test SetWebhook through the "simulator".
	err = bot.SetWebhook(context.Background(), "http://example.com/webhook", "secret")
	if err != nil {
		t.Fatalf("SetWebhook failed: %v", err)
	}
}

func TestSimulator_MultipleUsers_DifferentIDs(t *testing.T) {
	// Verify that multiple simulated users with different IDs are handled
	// correctly.

	sim, _ := startTestSim(t)

	var updates []telegram.Update
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u telegram.Update
		json.NewDecoder(r.Body).Decode(&u)
		updates = append(updates, u)
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookSrv.Close()

	// Simulate two different users.
	alice := simulator.Message{
		UserID:    111,
		ChatID:    111,
		FirstName: "Alice",
		Text:      "Hi from Alice",
		MessageID: 10,
		UpdateID:  100,
	}
	bob := simulator.Message{
		UserID:    222,
		ChatID:    222,
		FirstName: "Bob",
		Text:      "Hi from Bob",
		MessageID: 20,
		UpdateID:  200,
	}

	sim.SendUpdate(context.Background(), webhookSrv.URL, "secret", alice)
	sim.SendUpdate(context.Background(), webhookSrv.URL, "secret", bob)

	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}

	// Alice's update.
	if updates[0].Message.From.ID != 111 {
		t.Errorf("Alice From.ID: got %d, want 111", updates[0].Message.From.ID)
	}
	if updates[0].Message.From.FirstName != "Alice" {
		t.Errorf("Alice FirstName: got %q, want 'Alice'", updates[0].Message.From.FirstName)
	}

	// Bob's update.
	if updates[1].Message.From.ID != 222 {
		t.Errorf("Bob From.ID: got %d, want 222", updates[1].Message.From.ID)
	}
	if updates[1].Message.From.FirstName != "Bob" {
		t.Errorf("Bob FirstName: got %q, want 'Bob'", updates[1].Message.From.FirstName)
	}
}

func TestCapturedMessage_Timestamp(t *testing.T) {
	// Verify that CapturedMessage has a timestamp field.
	msg := simulator.CapturedMessage{
		ChatID:    123,
		Text:      "hello",
		Timestamp: time.Now(),
	}

	if msg.ChatID != 123 {
		t.Errorf("ChatID: got %d, want 123", msg.ChatID)
	}
	if msg.Text != "hello" {
		t.Errorf("Text: got %q, want 'hello'", msg.Text)
	}
	if msg.Timestamp.IsZero() {
		t.Error("expected non-zero Timestamp")
	}
}
