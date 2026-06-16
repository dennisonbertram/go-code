package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/apps/socialagent/db"
	"go-agent-harness/apps/socialagent/gateway"
	"go-agent-harness/apps/socialagent/harness"
	"go-agent-harness/apps/socialagent/safety"
	"go-agent-harness/apps/socialagent/telegram"
)

// --- fake implementations ---

type fakeStore struct {
	mu    sync.Mutex
	users map[int64]*db.User
	calls []int64 // telegramIDs passed to GetOrCreateUser
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: make(map[int64]*db.User)}
}

func (f *fakeStore) GetOrCreateUser(ctx context.Context, telegramID int64, displayName string) (*db.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, telegramID)
	u, ok := f.users[telegramID]
	if !ok {
		u = &db.User{
			ID:             fmt.Sprintf("uuid-%d", telegramID),
			TelegramID:     telegramID,
			ConversationID: fmt.Sprintf("conv-%d", telegramID),
			DisplayName:    displayName,
		}
		f.users[telegramID] = u
	}
	return u, nil
}

// fakeHarness records calls and optionally delays or returns errors.
type fakeHarness struct {
	mu       sync.Mutex
	requests []harness.RunRequest
	result   *harness.RunResult
	err      error
	delay    time.Duration
}

func (f *fakeHarness) SendAndWait(ctx context.Context, req harness.RunRequest) (*harness.RunResult, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &harness.RunResult{Output: "default output", RunID: "run-1"}, nil
}

// fakeBot captures sent messages and can be configured to fail ParseUpdate.
type fakeBot struct {
	mu       sync.Mutex
	messages []sentMessage
	parseErr error
}

type sentMessage struct {
	chatID int64
	text   string
}

func (f *fakeBot) ParseUpdate(r *http.Request) (*telegram.Update, error) {
	if f.parseErr != nil {
		return nil, f.parseErr
	}
	var update telegram.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		return nil, err
	}
	if update.Message == nil || update.Message.Text == "" {
		return nil, errors.New("no text message")
	}
	return &update, nil
}

func (f *fakeBot) SendMessage(ctx context.Context, chatID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, sentMessage{chatID: chatID, text: text})
	return nil
}

func (f *fakeBot) DisplayName(u *telegram.User) string {
	if u == nil {
		return "Unknown"
	}
	return u.FirstName
}

// recordingHarness tracks concurrent active calls to verify serialization.
type recordingHarness struct {
	delay       time.Duration
	activeCalls *int32
	maxActive   *int32
}

func (r *recordingHarness) SendAndWait(ctx context.Context, req harness.RunRequest) (*harness.RunResult, error) {
	current := atomic.AddInt32(r.activeCalls, 1)
	defer atomic.AddInt32(r.activeCalls, -1)

	// Update max if needed (CAS loop).
	for {
		max := atomic.LoadInt32(r.maxActive)
		if current <= max {
			break
		}
		if atomic.CompareAndSwapInt32(r.maxActive, max, current) {
			break
		}
	}

	if r.delay > 0 {
		time.Sleep(r.delay)
	}

	return &harness.RunResult{Output: "ok", RunID: "run-x"}, nil
}

// fakeProfileFetcher is a no-op ProfileFetcher.
type fakeProfileFetcher struct {
	mu      sync.Mutex
	profile *db.UserProfile
	err     error
	calls   []string // userIDs requested
}

func (f *fakeProfileFetcher) GetProfile(ctx context.Context, userID string) (*db.UserProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, userID)
	if f.err != nil {
		return nil, f.err
	}
	return f.profile, nil
}

// fakeSummarizer records UpdateProfile calls.
type fakeSummarizer struct {
	mu    sync.Mutex
	calls []summaryCall
	err   error
}

type summaryCall struct {
	userID         string
	conversationID string
	displayName    string
}

func (f *fakeSummarizer) UpdateProfile(ctx context.Context, userID, conversationID, displayName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, summaryCall{userID: userID, conversationID: conversationID, displayName: displayName})
	return f.err
}

// fakeActivityLogger records LogActivity calls.
type fakeActivityLogger struct {
	mu    sync.Mutex
	calls []activityCall
}

type activityCall struct {
	userID       string
	displayName  string
	activityType string
	content      string
}

func (f *fakeActivityLogger) LogActivity(ctx context.Context, userID, displayName, activityType, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, activityCall{userID: userID, displayName: displayName, activityType: activityType, content: content})
	return nil
}

// fakeScreener implements gateway.Screener for tests. It can be configured
// to mark messages as safe or unsafe, or to return an error (fail-open).
type fakeScreener struct {
	mu      sync.Mutex
	result  *safety.Result
	err     error
	calls   []string // texts passed to Screen
}

func (f *fakeScreener) Screen(ctx context.Context, text string) (*safety.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, text)
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &safety.Result{Safe: true}, nil
}

// --- helpers ---

const testWebhookSecret = "test-secret"

func makeWebhookRequest(t *testing.T, update telegram.Update) *http.Request {
	t.Helper()
	return makeWebhookRequestWithSecret(t, update, testWebhookSecret)
}

func makeWebhookRequestWithSecret(t *testing.T, update telegram.Update, secret string) *http.Request {
	t.Helper()
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook/telegram", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	return req
}

func makeUpdate(userID, chatID int64, text string) telegram.Update {
	return telegram.Update{
		UpdateID: 1,
		Message: &telegram.Message{
			MessageID: 1,
			From:      &telegram.User{ID: userID, FirstName: "Alice"},
			Chat:      telegram.Chat{ID: chatID},
			Text:      text,
		},
	}
}

func makeUpdateWithID(updateID int, userID, chatID int64, text string) telegram.Update {
	return telegram.Update{
		UpdateID: updateID,
		Message: &telegram.Message{
			MessageID: updateID,
			From:      &telegram.User{ID: userID, FirstName: "Alice"},
			Chat:      telegram.Chat{ID: chatID},
			Text:      text,
		},
	}
}

// newTestGateway is a helper that creates a Gateway with nil optional dependencies.
func newTestGateway(bot gateway.MessageSender, store gateway.UserStore, h gateway.HarnessRunner, webhookSecret string) *gateway.Gateway {
	return gateway.NewGateway(bot, store, h, webhookSecret, nil, nil, nil, nil, "")
}

// --- tests ---

// TestHappyPath verifies: valid webhook → handler returns 200 immediately →
// background goroutine creates user → calls harness with correct fields →
// sends response back to correct chat_id.
func TestHappyPath(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "hello from agent", RunID: "run-42"}}
	bot := &fakeBot{}
	profiles := &fakeProfileFetcher{profile: &db.UserProfile{
		UserID:  "uuid-123",
		Summary: "A test user",
	}}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, profiles, nil, nil, nil, "")

	update := makeUpdateWithID(100, 123, 456, "What is 2+2?")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	// Handler must return 200 immediately, before background work completes.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Wait for background goroutine to finish.
	gw.Wait()

	// Verify store was called with correct telegramID.
	store.mu.Lock()
	if len(store.calls) != 1 || store.calls[0] != 123 {
		t.Errorf("expected store call with telegramID=123, got %v", store.calls)
	}
	store.mu.Unlock()

	// Verify harness was called with correct fields.
	h.mu.Lock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request, got %d", len(h.requests))
	}
	r := h.requests[0]
	h.mu.Unlock()

	if r.Prompt != "What is 2+2?" {
		t.Errorf("expected prompt 'What is 2+2?', got %q", r.Prompt)
	}
	if r.ConversationID != "conv-123" {
		t.Errorf("expected conversation_id 'conv-123', got %q", r.ConversationID)
	}
	if r.TenantID != "uuid-123" {
		t.Errorf("expected tenant_id 'uuid-123', got %q", r.TenantID)
	}

	// Verify the response was sent to the correct chat.
	bot.mu.Lock()
	if len(bot.messages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(bot.messages))
	}
	msg := bot.messages[0]
	bot.mu.Unlock()

	if msg.chatID != 456 {
		t.Errorf("expected chatID=456, got %d", msg.chatID)
	}
	if msg.text != "hello from agent" {
		t.Errorf("expected text 'hello from agent', got %q", msg.text)
	}
}

// TestHarnessError verifies: when harness returns an error, handler returns 200
// immediately, and the background goroutine sends an error message to the user.
func TestHarnessError(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{err: errors.New("harness unavailable")}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	update := makeUpdateWithID(200, 111, 222, "help me")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	// Handler returns 200 immediately.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Wait for background goroutine to finish.
	gw.Wait()

	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.messages) != 1 {
		t.Fatalf("expected 1 error message sent, got %d", len(bot.messages))
	}
	if bot.messages[0].chatID != 222 {
		t.Errorf("expected chatID=222, got %d", bot.messages[0].chatID)
	}
	if bot.messages[0].text != "Sorry, something went wrong. Please try again." {
		t.Errorf("unexpected error text: %q", bot.messages[0].text)
	}
}

// TestInvalidWebhook verifies: when ParseUpdate returns an error (no text),
// the handler returns 200 and does NOT dispatch a background goroutine or call harness.
func TestInvalidWebhook(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{}
	bot := &fakeBot{parseErr: errors.New("no text")}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	req := httptest.NewRequest(http.MethodPost, "/webhook/telegram", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	// No background goroutine was dispatched, so Wait returns immediately.
	gw.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 0 {
		t.Errorf("expected no harness calls, got %d", len(h.requests))
	}
}

// TestPerUserMutex verifies: two concurrent requests for the same user are
// serialized — max concurrent harness calls for that user is 1.
func TestPerUserMutex(t *testing.T) {
	store := newFakeStore()
	bot := &fakeBot{}

	var activeCalls int32
	var maxConcurrent int32

	h := &recordingHarness{
		delay:       50 * time.Millisecond,
		activeCalls: &activeCalls,
		maxActive:   &maxConcurrent,
	}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	// Use distinct update IDs to avoid deduplication.
	for i := 0; i < 2; i++ {
		update := makeUpdateWithID(300+i, 999, 999, "concurrent message")
		req := makeWebhookRequest(t, update)
		rec := httptest.NewRecorder()
		gw.HandleWebhook(rec, req)
	}

	// Wait for all background goroutines to complete.
	gw.Wait()

	if maxConcurrent > 1 {
		t.Errorf("per-user mutex violated: max concurrent calls for same user = %d (expected ≤1)", maxConcurrent)
	}
}

// TestDifferentUsersConcurrent verifies: two concurrent requests for different
// users both proceed simultaneously — they are NOT blocked by each other.
func TestDifferentUsersConcurrent(t *testing.T) {
	store := newFakeStore()
	bot := &fakeBot{}

	var activeCalls int32
	var maxConcurrent int32

	h := &recordingHarness{
		delay:       50 * time.Millisecond,
		activeCalls: &activeCalls,
		maxActive:   &maxConcurrent,
	}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	// Use distinct update IDs for each user.
	update1 := makeUpdateWithID(400, 1001, 1001, "message from user 1")
	update2 := makeUpdateWithID(401, 1002, 1002, "message from user 2")

	req1 := makeWebhookRequest(t, update1)
	req2 := makeWebhookRequest(t, update2)
	rec1 := httptest.NewRecorder()
	rec2 := httptest.NewRecorder()

	// Both handlers return immediately; background goroutines run concurrently.
	gw.HandleWebhook(rec1, req1)
	gw.HandleWebhook(rec2, req2)

	// Wait for both background goroutines to finish.
	gw.Wait()

	if maxConcurrent < 2 {
		t.Errorf("expected both users to run concurrently, maxConcurrent=%d", maxConcurrent)
	}
}

// TestDuplicateUpdateID verifies: sending the same update_id twice results in
// harness being called only once (Telegram retry deduplication).
func TestDuplicateUpdateID(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "hello", RunID: "run-1"}}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	// Send the same update twice with the same update_id.
	for i := 0; i < 2; i++ {
		update := makeUpdateWithID(500, 777, 777, "duplicate message")
		req := makeWebhookRequest(t, update)
		rec := httptest.NewRecorder()
		gw.HandleWebhook(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rec.Code)
		}
	}

	// Wait for any background goroutines to complete.
	gw.Wait()

	// Harness should have been called exactly once.
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Errorf("expected harness called once for duplicate update_id, got %d calls", len(h.requests))
	}
}

// TestWebhookAuth_ValidSecret verifies: a request with the correct secret
// proceeds normally — harness is called and a response is sent.
func TestWebhookAuth_ValidSecret(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "auth ok", RunID: "run-auth-1"}}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	update := makeUpdateWithID(600, 111, 111, "authenticated message")
	req := makeWebhookRequestWithSecret(t, update, testWebhookSecret)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Errorf("expected harness called once for valid secret, got %d calls", len(h.requests))
	}
}

// TestWebhookAuth_InvalidSecret verifies: a request with a wrong secret
// returns 200 but does NOT call the harness (spoofed request is silently dropped).
func TestWebhookAuth_InvalidSecret(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	update := makeUpdateWithID(700, 222, 222, "spoofed message")
	req := makeWebhookRequestWithSecret(t, update, "wrong-secret")
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 0 {
		t.Errorf("expected no harness calls for invalid secret, got %d", len(h.requests))
	}
}

// TestWebhookAuth_MissingSecret verifies: a request with no
// X-Telegram-Bot-Api-Secret-Token header returns 200 but does NOT call the harness.
func TestWebhookAuth_MissingSecret(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	update := makeUpdateWithID(800, 333, 333, "no secret message")
	// Use makeWebhookRequestWithSecret with empty string so no header is set.
	req := makeWebhookRequestWithSecret(t, update, "")
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 0 {
		t.Errorf("expected no harness calls for missing secret, got %d", len(h.requests))
	}
}

// TestProcessMessage_RendersSystemPrompt verifies that the harness receives a
// rendered system prompt (not an empty string) when a profile is available.
func TestProcessMessage_RendersSystemPrompt(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "hi", RunID: "run-sp-1"}}
	bot := &fakeBot{}
	profiles := &fakeProfileFetcher{
		profile: &db.UserProfile{
			UserID:     "uuid-901",
			Summary:    "Loves hiking and photography",
			Interests:  []string{"hiking", "photography"},
			LookingFor: "adventure partners",
		},
	}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, profiles, nil, nil, nil, "")

	update := makeUpdateWithID(900, 901, 901, "Hello!")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)
	gw.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request, got %d", len(h.requests))
	}

	sp := h.requests[0].SystemPrompt
	if sp == "" {
		t.Error("expected non-empty system prompt, got empty string")
	}
	// The rendered prompt should contain "The Connector" from the template.
	if !strings.Contains(sp, "The Connector") {
		t.Errorf("system prompt does not contain 'The Connector': %q", sp[:min(len(sp), 200)])
	}
}

// TestProcessMessage_IncludesMCPServer verifies that the RunRequest includes
// MCP server config when mcpServerURL is set.
func TestProcessMessage_IncludesMCPServer(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "ok", RunID: "run-mcp-1"}}
	bot := &fakeBot{}

	const mcpURL = "http://localhost:8082/mcp"
	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, nil, nil, nil, mcpURL)

	update := makeUpdateWithID(1000, 1001, 1001, "find me someone who likes hiking")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)
	gw.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request, got %d", len(h.requests))
	}

	servers := h.requests[0].MCPServers
	if len(servers) != 1 {
		t.Fatalf("expected 1 MCP server in request, got %d", len(servers))
	}
	if servers[0].Name != "social" {
		t.Errorf("expected MCP server name 'social', got %q", servers[0].Name)
	}
	if servers[0].URL != mcpURL {
		t.Errorf("expected MCP server URL %q, got %q", mcpURL, servers[0].URL)
	}
}

// TestProcessMessage_NoMCPServer verifies that the RunRequest does NOT include
// MCP server config when mcpServerURL is empty.
func TestProcessMessage_NoMCPServer(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "ok", RunID: "run-nomcp-1"}}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	update := makeUpdateWithID(1100, 1101, 1101, "hello")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)
	gw.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request, got %d", len(h.requests))
	}
	if len(h.requests[0].MCPServers) != 0 {
		t.Errorf("expected no MCP servers when URL is empty, got %v", h.requests[0].MCPServers)
	}
}

// TestProcessMessage_SetsAllowedTools verifies that the RunRequest sent to
// harness always includes the expected AllowedTools restriction list, ensuring
// the Telegram-facing agent cannot call bash, file I/O, or other dangerous
// built-in harness tools.
func TestProcessMessage_SetsAllowedTools(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "ok", RunID: "run-at-1"}}
	bot := &fakeBot{}

	gw := newTestGateway(bot, store, h, testWebhookSecret)

	update := makeUpdateWithID(1150, 1151, 1151, "who is online?")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)
	gw.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request, got %d", len(h.requests))
	}

	allowed := h.requests[0].AllowedTools
	if len(allowed) == 0 {
		t.Fatal("expected AllowedTools to be non-empty, got empty slice")
	}

	// Verify core harness tools are present.
	wantPresent := []string{"compact_history", "context_status"}
	for _, tool := range wantPresent {
		found := false
		for _, a := range allowed {
			if a == tool {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in AllowedTools, got %v", tool, allowed)
		}
	}

	// Verify MCP social tools are present.
	wantMCP := []string{"mcp_social_search_users", "mcp_social_get_user_profile", "mcp_social_get_updates", "mcp_social_save_insight", "mcp_social_get_my_profile", "mcp_social_get_community_stats", "mcp_social_send_message_to_user", "mcp_social_get_my_messages"}
	for _, tool := range wantMCP {
		found := false
		for _, a := range allowed {
			if a == tool {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected MCP tool %q in AllowedTools, got %v", tool, allowed)
		}
	}

	// Verify dangerous tools are NOT listed.
	wantAbsent := []string{"bash", "read_file", "write_file", "list_dir"}
	for _, tool := range wantAbsent {
		for _, a := range allowed {
			if a == tool {
				t.Errorf("dangerous tool %q should NOT be in AllowedTools", tool)
			}
		}
	}
}

// TestProcessMessage_TriggersSummary verifies that summarizer.UpdateProfile is
// called after the agent responds successfully.
func TestProcessMessage_TriggersSummary(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "great conversation!", RunID: "run-sum-1"}}
	bot := &fakeBot{}
	sum := &fakeSummarizer{}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, sum, nil, nil, "")

	update := makeUpdateWithID(1200, 1201, 1201, "I love hiking!")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)
	gw.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	sum.mu.Lock()
	defer sum.mu.Unlock()
	if len(sum.calls) != 1 {
		t.Fatalf("expected 1 summarizer call, got %d", len(sum.calls))
	}
	call := sum.calls[0]
	if call.userID != "uuid-1201" {
		t.Errorf("expected userID 'uuid-1201', got %q", call.userID)
	}
	if call.conversationID != "conv-1201" {
		t.Errorf("expected conversationID 'conv-1201', got %q", call.conversationID)
	}
}

// TestProcessMessage_SummaryNotCalledOnError verifies that summarizer is NOT
// called when harness returns an error.
func TestProcessMessage_SummaryNotCalledOnError(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{err: errors.New("harness down")}
	bot := &fakeBot{}
	sum := &fakeSummarizer{}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, sum, nil, nil, "")

	update := makeUpdateWithID(1300, 1301, 1301, "hello")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)
	gw.Wait()

	sum.mu.Lock()
	defer sum.mu.Unlock()
	if len(sum.calls) != 0 {
		t.Errorf("expected no summarizer calls on error, got %d", len(sum.calls))
	}
}

// TestSafetyScreen_BlockedMessage verifies that when the screener marks a
// message as unsafe, the agent harness is NOT called and a polite refusal
// message is sent to the user instead.
func TestSafetyScreen_BlockedMessage(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{}
	bot := &fakeBot{}
	screener := &fakeScreener{
		result: &safety.Result{
			Safe:     false,
			Category: "S3",
			Reason:   "Harmful content detected",
		},
	}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, nil, nil, screener, "")

	update := makeUpdateWithID(1400, 1401, 1401, "bad content")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	// Screener should have been called with the message text.
	screener.mu.Lock()
	if len(screener.calls) != 1 || screener.calls[0] != "bad content" {
		t.Errorf("expected screener called with 'bad content', got %v", screener.calls)
	}
	screener.mu.Unlock()

	// Harness must NOT have been called.
	h.mu.Lock()
	if len(h.requests) != 0 {
		t.Errorf("expected no harness calls for blocked message, got %d", len(h.requests))
	}
	h.mu.Unlock()

	// A polite refusal message must have been sent to the user.
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.messages) != 1 {
		t.Fatalf("expected 1 refusal message sent, got %d", len(bot.messages))
	}
	msg := bot.messages[0]
	if msg.chatID != 1401 {
		t.Errorf("expected chatID=1401, got %d", msg.chatID)
	}
	if !strings.Contains(msg.text, "not able to help") {
		t.Errorf("expected refusal message, got %q", msg.text)
	}
	if !strings.Contains(msg.text, "Harmful content") {
		t.Errorf("expected refusal message to include reason, got %q", msg.text)
	}
}

// TestSafetyScreen_SafeMessagePassesThrough verifies that when the screener
// marks a message as safe, normal processing continues: the harness is called
// and the agent response is sent to the user.
func TestSafetyScreen_SafeMessagePassesThrough(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "hello from agent", RunID: "run-safe-1"}}
	bot := &fakeBot{}
	screener := &fakeScreener{
		result: &safety.Result{Safe: true},
	}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, nil, nil, screener, "")

	update := makeUpdateWithID(1500, 1501, 1501, "Hello!")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	// Screener should have been called.
	screener.mu.Lock()
	if len(screener.calls) != 1 || screener.calls[0] != "Hello!" {
		t.Errorf("expected screener called with 'Hello!', got %v", screener.calls)
	}
	screener.mu.Unlock()

	// Harness should have been called.
	h.mu.Lock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request for safe message, got %d", len(h.requests))
	}
	if h.requests[0].Prompt != "Hello!" {
		t.Errorf("expected prompt 'Hello!', got %q", h.requests[0].Prompt)
	}
	h.mu.Unlock()

	// Agent response should have been sent to the user.
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.messages) != 1 {
		t.Fatalf("expected 1 message sent, got %d", len(bot.messages))
	}
	if bot.messages[0].text != "hello from agent" {
		t.Errorf("expected 'hello from agent', got %q", bot.messages[0].text)
	}
}

// TestSafetyScreen_ScreenerErrorFallthrough verifies fail-open behavior: when
// the screener returns an error, the message is still processed normally
// (harness is called and no refusal is sent).
func TestSafetyScreen_ScreenerErrorFallthrough(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "processed anyway", RunID: "run-fo-1"}}
	bot := &fakeBot{}
	screener := &fakeScreener{
		err: errors.New("screener unreachable"),
	}

	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, nil, nil, screener, "")

	update := makeUpdateWithID(1600, 1601, 1601, "normal message")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	// Screener should have been called and errored.
	screener.mu.Lock()
	if len(screener.calls) != 1 {
		t.Errorf("expected 1 screener call, got %d", len(screener.calls))
	}
	screener.mu.Unlock()

	// Harness MUST be called (fail-open).
	h.mu.Lock()
	if len(h.requests) != 1 {
		t.Fatalf("expected harness call on screener error (fail-open), got %d", len(h.requests))
	}
	h.mu.Unlock()

	// Agent response should be sent normally.
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.messages) != 1 {
		t.Fatalf("expected 1 message on fail-open, got %d", len(bot.messages))
	}
	if bot.messages[0].text != "processed anyway" {
		t.Errorf("expected 'processed anyway', got %q", bot.messages[0].text)
	}
}

// TestSafetyScreen_NoScreenerBackwardCompat verifies that when no screener is
// configured (nil), all messages pass through unscreened — backward compatible
// with existing deployments that do not set SAFETY_SCREENER_URL.
func TestSafetyScreen_NoScreenerBackwardCompat(t *testing.T) {
	store := newFakeStore()
	h := &fakeHarness{result: &harness.RunResult{Output: "normal response", RunID: "run-bc-1"}}
	bot := &fakeBot{}

	// Pass nil screener — this is the default for existing deployments.
	gw := gateway.NewGateway(bot, store, h, testWebhookSecret, nil, nil, nil, nil, "")

	update := makeUpdateWithID(1700, 1701, 1701, "any message at all")
	req := makeWebhookRequest(t, update)
	rec := httptest.NewRecorder()

	gw.HandleWebhook(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	gw.Wait()

	// Harness should be called normally with nil screener.
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) != 1 {
		t.Fatalf("expected 1 harness request, got %d (nil screener should not block)", len(h.requests))
	}
}

// min is a local helper since we can't import slices in go1.20.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
