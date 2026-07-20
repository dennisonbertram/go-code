package mcpserver_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"go-agent-harness/apps/socialagent/db"
	"go-agent-harness/apps/socialagent/mcpserver"
)

// mockStore implements UserStore for testing.
type mockStore struct {
	profiles             []db.UserProfile
	user                 *db.User
	profile              *db.UserProfile
	activity             []db.ActivityEntry
	insights             []db.UserInsight
	communityStats       *db.CommunityStats
	savedMessage         *db.Message
	pendingMessages      []db.Message
	saveInsightErr       error
	searchErr            error
	getUserErr           error
	getProfileErr        error
	getActivityErr       error
	getInsightsErr       error
	getCommunityStatsErr error
	saveMessageErr       error
	getPendingMsgsErr    error
	markDeliveredErr     error
}

func (m *mockStore) SearchProfiles(_ context.Context, _ string, _ int) ([]db.UserProfile, error) {
	return m.profiles, m.searchErr
}

func (m *mockStore) GetProfile(_ context.Context, _ string) (*db.UserProfile, error) {
	return m.profile, m.getProfileErr
}

func (m *mockStore) GetUserByDisplayName(_ context.Context, _ string) (*db.User, error) {
	return m.user, m.getUserErr
}

func (m *mockStore) GetUserByID(_ context.Context, _ string) (*db.User, error) {
	return m.user, m.getUserErr
}

func (m *mockStore) GetRecentActivity(_ context.Context, _ int, _ string) ([]db.ActivityEntry, error) {
	return m.activity, m.getActivityErr
}

func (m *mockStore) SaveInsight(_ context.Context, _, _, _ string) error {
	return m.saveInsightErr
}

func (m *mockStore) GetInsights(_ context.Context, _ string) ([]db.UserInsight, error) {
	return m.insights, m.getInsightsErr
}

func (m *mockStore) GetAllProfiles(_ context.Context, _ string, _ int) ([]db.UserProfile, error) {
	return m.profiles, m.searchErr
}

func (m *mockStore) GetCommunityStats(_ context.Context) (*db.CommunityStats, error) {
	return m.communityStats, m.getCommunityStatsErr
}

func (m *mockStore) SaveMessage(_ context.Context, _, _, _ string) (*db.Message, error) {
	return m.savedMessage, m.saveMessageErr
}

func (m *mockStore) GetPendingMessages(_ context.Context, _ string) ([]db.Message, error) {
	return m.pendingMessages, m.getPendingMsgsErr
}

func (m *mockStore) MarkMessageDelivered(_ context.Context, _ string) error {
	return m.markDeliveredErr
}

// mockDeliverer implements MessageDeliverer for testing.
type mockDeliverer struct {
	delivered []deliveredMsg
	err       error
}

type deliveredMsg struct {
	telegramID int64
	text       string
}

func (d *mockDeliverer) DeliverMessage(_ context.Context, recipientTelegramID int64, text string) error {
	d.delivered = append(d.delivered, deliveredMsg{telegramID: recipientTelegramID, text: text})
	return d.err
}

// callTool invokes a named tool on the server and returns the text result.
func callTool(t *testing.T, s *mcpserver.Server, toolName string, args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args
	result, err := s.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool(%s) returned error: %v", toolName, err)
	}
	if result == nil {
		t.Fatalf("CallTool(%s) returned nil result", toolName)
	}
	// Extract text from first content item.
	for _, c := range result.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			return tc.Text
		}
	}
	return ""
}

// --- search_users ---

func TestSearchUsers_ReturnsResults(t *testing.T) {
	store := &mockStore{
		profiles: []db.UserProfile{
			{
				UserID:     "uid-1",
				Summary:    "Loves hiking and photography",
				Interests:  []string{"hiking", "photography"},
				LookingFor: "adventure buddies",
			},
			{
				UserID:     "uid-2",
				Summary:    "Software engineer interested in open source",
				Interests:  []string{"programming", "open source"},
				LookingFor: "collaborators",
			},
		},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "search_users", map[string]any{"query": "hiking", "limit": float64(10)})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "uid-1") && !contains(out, "Loves hiking") {
		t.Errorf("expected output to contain first profile info, got: %s", out)
	}
	if !contains(out, "uid-2") && !contains(out, "Software engineer") {
		t.Errorf("expected output to contain second profile info, got: %s", out)
	}
}

func TestSearchUsers_NoResults(t *testing.T) {
	store := &mockStore{profiles: []db.UserProfile{}}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "search_users", map[string]any{"query": "nobody", "limit": float64(10)})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "No users found") && !contains(out, "no users") && !contains(out, "0 users") {
		t.Errorf("expected helpful no-results message, got: %s", out)
	}
}

// --- get_user_profile ---

func TestGetUserProfile_Found(t *testing.T) {
	store := &mockStore{
		user: &db.User{
			ID:          "uid-alice",
			DisplayName: "Alice",
		},
		profile: &db.UserProfile{
			UserID:     "uid-alice",
			Summary:    "Alice loves baking and hiking",
			Interests:  []string{"baking", "hiking"},
			LookingFor: "friends",
		},
		insights: []db.UserInsight{
			{ID: "ins-1", UserID: "uid-alice", Insight: "Prefers morning chats", Source: "agent", CreatedAt: time.Now()},
		},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_user_profile", map[string]any{"name": "Alice"})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "Alice") {
		t.Errorf("expected output to contain user name, got: %s", out)
	}
	if !contains(out, "baking") && !contains(out, "hiking") {
		t.Errorf("expected output to contain interests, got: %s", out)
	}
}

func TestGetUserProfile_NotFound(t *testing.T) {
	store := &mockStore{user: nil}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_user_profile", map[string]any{"name": "Ghost"})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "not found") && !contains(out, "No user") && !contains(out, "couldn't find") {
		t.Errorf("expected error message about user not found, got: %s", out)
	}
}

// --- get_updates ---

func TestGetUpdates_ReturnsActivity(t *testing.T) {
	now := time.Now()
	store := &mockStore{
		activity: []db.ActivityEntry{
			{ID: "act-1", UserID: "uid-bob", DisplayName: "Bob", ActivityType: "message", Content: "Hello world!", CreatedAt: now},
			{ID: "act-2", UserID: "uid-carol", DisplayName: "Carol", ActivityType: "profile_update", Content: "Updated interests", CreatedAt: now},
		},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_updates", map[string]any{"limit": float64(10), "exclude_user_id": "uid-current"})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "Bob") {
		t.Errorf("expected output to contain Bob, got: %s", out)
	}
	if !contains(out, "Carol") {
		t.Errorf("expected output to contain Carol, got: %s", out)
	}
}

// --- save_insight ---

func TestSaveInsight_Success(t *testing.T) {
	store := &mockStore{
		user: &db.User{ID: "uid-123", DisplayName: "Alice"},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "save_insight", map[string]any{
		"user_name": "Alice",
		"insight":   "Loves hiking on weekends",
	})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "saved") && !contains(out, "Saved") && !contains(out, "noted") && !contains(out, "Noted") {
		t.Errorf("expected confirmation message, got: %s", out)
	}
}

// --- get_my_profile ---

func TestGetMyProfile_Found(t *testing.T) {
	store := &mockStore{
		user: &db.User{ID: "uid-me", DisplayName: "Me"},
		profile: &db.UserProfile{
			UserID:     "uid-me",
			Summary:    "I enjoy coding and coffee",
			Interests:  []string{"coding", "coffee"},
			LookingFor: "fellow hackers",
		},
		insights: []db.UserInsight{
			{ID: "ins-1", UserID: "uid-me", Insight: "Night owl", Source: "agent", CreatedAt: time.Now()},
		},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_my_profile", map[string]any{"user_name": "Me"})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "coding") && !contains(out, "coffee") {
		t.Errorf("expected profile content, got: %s", out)
	}
}

func TestGetMyProfile_NewUser(t *testing.T) {
	store := &mockStore{profile: nil, insights: nil, user: nil}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_my_profile", map[string]any{"user_name": "NewUser"})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "don't know") && !contains(out, "Don't know") && !contains(out, "not much") && !contains(out, "new") {
		t.Errorf("expected new-user message, got: %s", out)
	}
}

// --- get_community_stats ---

func TestGetCommunityStats_ReturnsStats(t *testing.T) {
	store := &mockStore{
		communityStats: &db.CommunityStats{
			TotalUsers:        42,
			UsersWithProfiles: 30,
			TotalActivities:   150,
		},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_community_stats", map[string]any{})

	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "42") {
		t.Errorf("expected output to contain total_users=42, got: %s", out)
	}
	if !contains(out, "30") {
		t.Errorf("expected output to contain users_with_profiles=30, got: %s", out)
	}
	if !contains(out, "150") {
		t.Errorf("expected output to contain total_activities=150, got: %s", out)
	}
}

func TestGetCommunityStats_StoreError(t *testing.T) {
	store := &mockStore{
		getCommunityStatsErr: fmt.Errorf("db connection lost"),
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_community_stats", map[string]any{})

	if out == "" {
		t.Fatal("expected non-empty error output")
	}
	if !contains(out, "failed") && !contains(out, "error") && !contains(out, "db connection lost") {
		t.Errorf("expected error message in output, got: %s", out)
	}
}

// --- send_message_to_user ---

func TestSendMessageToUser_Success(t *testing.T) {
	recipient := &db.User{ID: "uid-recipient", TelegramID: 9001, DisplayName: "Bob"}
	sender := &db.User{ID: "uid-sender", TelegramID: 9002, DisplayName: "Alice"}
	savedMsg := &db.Message{ID: "msg-1", SenderID: "uid-sender", RecipientID: "uid-recipient", Content: "Hello Bob!"}
	deliverer := &mockDeliverer{}

	// Both sender and recipient are looked up by display name.
	// Use a custom store that maps names to users.
	customStore := &namedUserStore{
		users: map[string]*db.User{
			"Alice": sender,
			"Bob":   recipient,
		},
		savedMessage: savedMsg,
	}
	s := mcpserver.New(customStore, deliverer)

	out := callTool(t, s, "send_message_to_user", map[string]any{
		"sender_name": "Alice",
		"recipient":   "Bob",
		"message":     "Hello Bob!",
	})

	if !contains(out, "delivered") && !contains(out, "Bob") {
		t.Errorf("expected success confirmation, got: %s", out)
	}
	if len(deliverer.delivered) != 1 {
		t.Errorf("expected 1 delivery call, got %d", len(deliverer.delivered))
	}
	if deliverer.delivered[0].telegramID != 9001 {
		t.Errorf("expected delivery to telegram_id 9001, got %d", deliverer.delivered[0].telegramID)
	}
	if !contains(deliverer.delivered[0].text, "Alice") || !contains(deliverer.delivered[0].text, "Hello Bob!") {
		t.Errorf("unexpected delivery text: %s", deliverer.delivered[0].text)
	}
}

func TestSendMessageToUser_RecipientNotFound(t *testing.T) {
	// Sender is found; recipient is not.
	sender := &db.User{ID: "uid-sender", TelegramID: 9002, DisplayName: "Alice"}
	customStore := &namedUserStore{
		users: map[string]*db.User{
			"Alice": sender,
			// "Ghost" intentionally absent
		},
	}
	s := mcpserver.New(customStore, nil)

	out := callTool(t, s, "send_message_to_user", map[string]any{
		"sender_name": "Alice",
		"recipient":   "Ghost",
		"message":     "Hello?",
	})

	if !contains(out, "not found") && !contains(out, "Ghost") {
		t.Errorf("expected not-found error, got: %s", out)
	}
}

// --- get_my_messages ---

func TestGetMyMessages_ReturnsPending(t *testing.T) {
	now := time.Now()
	store := &mockStore{
		user: &db.User{ID: "uid-me", DisplayName: "Me"},
		pendingMessages: []db.Message{
			{ID: "msg-1", SenderID: "uid-a", SenderName: "Alice", RecipientID: "uid-me", RecipientName: "Me", Content: "Hey there!", CreatedAt: now},
			{ID: "msg-2", SenderID: "uid-b", SenderName: "Bob", RecipientID: "uid-me", RecipientName: "Me", Content: "Want to chat?", CreatedAt: now},
		},
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_my_messages", map[string]any{"user_name": "Me"})

	if !contains(out, "Alice") {
		t.Errorf("expected output to contain sender Alice, got: %s", out)
	}
	if !contains(out, "Hey there!") {
		t.Errorf("expected output to contain message content, got: %s", out)
	}
	if !contains(out, "Bob") {
		t.Errorf("expected output to contain sender Bob, got: %s", out)
	}
}

func TestGetMyMessages_NoPending(t *testing.T) {
	store := &mockStore{
		user:            &db.User{ID: "uid-me", DisplayName: "Me"},
		pendingMessages: nil,
	}
	s := mcpserver.New(store, nil)

	out := callTool(t, s, "get_my_messages", map[string]any{"user_name": "Me"})

	if !contains(out, "No new messages") && !contains(out, "no messages") && !contains(out, "0 message") {
		t.Errorf("expected empty-mailbox message, got: %s", out)
	}
}

// namedUserStore is a specialized mock that returns different users based on
// display name lookup, needed for send_message_to_user tests where sender and
// recipient are distinct users.
type namedUserStore struct {
	users        map[string]*db.User
	savedMessage *db.Message
}

func (n *namedUserStore) SearchProfiles(_ context.Context, _ string, _ int) ([]db.UserProfile, error) {
	return nil, nil
}
func (n *namedUserStore) GetProfile(_ context.Context, _ string) (*db.UserProfile, error) {
	return nil, nil
}
func (n *namedUserStore) GetUserByDisplayName(_ context.Context, name string) (*db.User, error) {
	return n.users[name], nil
}
func (n *namedUserStore) GetUserByID(_ context.Context, _ string) (*db.User, error) {
	return nil, nil
}
func (n *namedUserStore) GetRecentActivity(_ context.Context, _ int, _ string) ([]db.ActivityEntry, error) {
	return nil, nil
}
func (n *namedUserStore) SaveInsight(_ context.Context, _, _, _ string) error { return nil }
func (n *namedUserStore) GetInsights(_ context.Context, _ string) ([]db.UserInsight, error) {
	return nil, nil
}
func (n *namedUserStore) GetAllProfiles(_ context.Context, _ string, _ int) ([]db.UserProfile, error) {
	return nil, nil
}
func (n *namedUserStore) GetCommunityStats(_ context.Context) (*db.CommunityStats, error) {
	return nil, nil
}
func (n *namedUserStore) SaveMessage(_ context.Context, _, _, _ string) (*db.Message, error) {
	return n.savedMessage, nil
}
func (n *namedUserStore) GetPendingMessages(_ context.Context, _ string) ([]db.Message, error) {
	return nil, nil
}
func (n *namedUserStore) MarkMessageDelivered(_ context.Context, _ string) error { return nil }

// contains is a helper to check substring presence.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
