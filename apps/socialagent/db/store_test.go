package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"go-agent-harness/apps/socialagent/db"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	return url
}

func TestGetOrCreateUser_CreatesOnFirstCall(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(100001)

	// Clean up before test.
	_ = store.DeleteUserByTelegramID(ctx, telegramID)

	user, err := store.GetOrCreateUser(ctx, telegramID, "Alice")
	if err != nil {
		t.Fatalf("GetOrCreateUser: %v", err)
	}

	if user == nil {
		t.Fatal("expected non-nil user")
	}
	if user.ID == "" {
		t.Error("expected non-empty ID")
	}
	if user.TelegramID != telegramID {
		t.Errorf("TelegramID: got %d, want %d", user.TelegramID, telegramID)
	}
	if user.ConversationID == "" {
		t.Error("expected non-empty ConversationID")
	}
	if user.DisplayName != "Alice" {
		t.Errorf("DisplayName: got %q, want %q", user.DisplayName, "Alice")
	}
	if user.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if user.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestGetOrCreateUser_IdempotentOnSecondCall(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(100002)

	_ = store.DeleteUserByTelegramID(ctx, telegramID)

	first, err := store.GetOrCreateUser(ctx, telegramID, "Bob")
	if err != nil {
		t.Fatalf("first GetOrCreateUser: %v", err)
	}

	// Small sleep so that if updated_at were to change, we'd catch it.
	time.Sleep(10 * time.Millisecond)

	second, err := store.GetOrCreateUser(ctx, telegramID, "Bob Updated")
	if err != nil {
		t.Fatalf("second GetOrCreateUser: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("ID mismatch: first=%s, second=%s", first.ID, second.ID)
	}
	if first.ConversationID != second.ConversationID {
		t.Errorf("ConversationID changed between calls: first=%s, second=%s", first.ConversationID, second.ConversationID)
	}
}

func TestGetOrCreateUser_DifferentTelegramIDsGetDifferentUsers(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	idA := int64(100003)
	idB := int64(100004)

	_ = store.DeleteUserByTelegramID(ctx, idA)
	_ = store.DeleteUserByTelegramID(ctx, idB)

	userA, err := store.GetOrCreateUser(ctx, idA, "Charlie")
	if err != nil {
		t.Fatalf("GetOrCreateUser A: %v", err)
	}
	userB, err := store.GetOrCreateUser(ctx, idB, "Diana")
	if err != nil {
		t.Fatalf("GetOrCreateUser B: %v", err)
	}

	if userA.ID == userB.ID {
		t.Error("different telegram IDs should produce different user IDs")
	}
	if userA.ConversationID == userB.ConversationID {
		t.Error("different users should have different conversation IDs")
	}
}

func TestGetUser_ReturnsNilForNonExistentUser(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	// Use a telegram ID very unlikely to exist.
	telegramID := int64(-999999)

	user, err := store.GetUser(ctx, telegramID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user != nil {
		t.Errorf("expected nil for non-existent user, got %+v", user)
	}
}

func TestUpdateDisplayName(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(100005)

	_ = store.DeleteUserByTelegramID(ctx, telegramID)

	user, err := store.GetOrCreateUser(ctx, telegramID, "Eve")
	if err != nil {
		t.Fatalf("GetOrCreateUser: %v", err)
	}

	err = store.UpdateDisplayName(ctx, user.ID, "Eve Updated")
	if err != nil {
		t.Fatalf("UpdateDisplayName: %v", err)
	}

	updated, err := store.GetUser(ctx, telegramID)
	if err != nil {
		t.Fatalf("GetUser after update: %v", err)
	}
	if updated == nil {
		t.Fatal("expected non-nil user after update")
	}
	if updated.DisplayName != "Eve Updated" {
		t.Errorf("DisplayName: got %q, want %q", updated.DisplayName, "Eve Updated")
	}
}

// ---------------------------------------------------------------------------
// Profile tests
// ---------------------------------------------------------------------------

func TestUpsertProfile(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(200001)

	_ = store.DeleteUserByTelegramID(ctx, telegramID)

	user, err := store.GetOrCreateUser(ctx, telegramID, "ProfileUser")
	if err != nil {
		t.Fatalf("GetOrCreateUser: %v", err)
	}

	// Create.
	if err := store.UpsertProfile(ctx, user.ID, "loves hiking", []string{"hiking", "camping"}, "friends"); err != nil {
		t.Fatalf("UpsertProfile create: %v", err)
	}

	p, err := store.GetProfile(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetProfile after create: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil profile after create")
	}
	if p.Summary != "loves hiking" {
		t.Errorf("Summary: got %q, want %q", p.Summary, "loves hiking")
	}
	if len(p.Interests) != 2 {
		t.Errorf("Interests: got %v, want 2 items", p.Interests)
	}
	if p.LookingFor != "friends" {
		t.Errorf("LookingFor: got %q, want %q", p.LookingFor, "friends")
	}

	// Update.
	if err := store.UpsertProfile(ctx, user.ID, "loves mountains", []string{"climbing"}, "partner"); err != nil {
		t.Fatalf("UpsertProfile update: %v", err)
	}

	p2, err := store.GetProfile(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetProfile after update: %v", err)
	}
	if p2.Summary != "loves mountains" {
		t.Errorf("Summary after update: got %q, want %q", p2.Summary, "loves mountains")
	}
	if p2.LookingFor != "partner" {
		t.Errorf("LookingFor after update: got %q, want %q", p2.LookingFor, "partner")
	}
	if len(p2.Interests) != 1 || p2.Interests[0] != "climbing" {
		t.Errorf("Interests after update: got %v, want [climbing]", p2.Interests)
	}
}

func TestGetProfile_NotFound(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	// Use a UUID that almost certainly does not exist.
	p, err := store.GetProfile(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil for missing profile, got %+v", p)
	}
}

func TestSearchProfiles(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create three users with distinct profiles.
	type fixture struct {
		telegramID int64
		name       string
		summary    string
		interests  []string
		lookingFor string
	}

	fixtures := []fixture{
		{201001, "SearchAlpha", "enjoys cycling and yoga", []string{"cycling", "yoga"}, "friends"},
		{201002, "SearchBeta", "passionate about cooking", []string{"cooking", "baking"}, "partner"},
		{201003, "SearchGamma", "avid reader and chess player", []string{"reading", "chess"}, "friends"},
	}

	var userIDs []string
	for _, f := range fixtures {
		_ = store.DeleteUserByTelegramID(ctx, f.telegramID)
		u, err := store.GetOrCreateUser(ctx, f.telegramID, f.name)
		if err != nil {
			t.Fatalf("GetOrCreateUser %s: %v", f.name, err)
		}
		if err := store.UpsertProfile(ctx, u.ID, f.summary, f.interests, f.lookingFor); err != nil {
			t.Fatalf("UpsertProfile %s: %v", f.name, err)
		}
		userIDs = append(userIDs, u.ID)
	}

	// Search for "cycling" — should match SearchAlpha only.
	results, err := store.SearchProfiles(ctx, "cycling", 10)
	if err != nil {
		t.Fatalf("SearchProfiles: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("SearchProfiles 'cycling': got %d results, want 1", len(results))
	} else if results[0].UserID != userIDs[0] {
		t.Errorf("SearchProfiles 'cycling': got userID %s, want %s", results[0].UserID, userIDs[0])
	}

	// Search for "friends" in looking_for — note the query only checks summary+interests.
	// Search "reading" — should match SearchGamma.
	results2, err := store.SearchProfiles(ctx, "reading", 10)
	if err != nil {
		t.Fatalf("SearchProfiles 'reading': %v", err)
	}
	if len(results2) != 1 {
		t.Errorf("SearchProfiles 'reading': got %d results, want 1", len(results2))
	}
}

// ---------------------------------------------------------------------------
// Activity log tests
// ---------------------------------------------------------------------------

func TestLogActivity(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(202001)

	_ = store.DeleteUserByTelegramID(ctx, telegramID)
	user, err := store.GetOrCreateUser(ctx, telegramID, "ActivityUser")
	if err != nil {
		t.Fatalf("GetOrCreateUser: %v", err)
	}

	if err := store.LogActivity(ctx, user.ID, user.DisplayName, "message", "hello world"); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
	if err := store.LogActivity(ctx, user.ID, user.DisplayName, "joined", "joined the channel"); err != nil {
		t.Fatalf("LogActivity second: %v", err)
	}

	// Retrieve with a different excludeUserID so our entries are included.
	entries, err := store.GetRecentActivity(ctx, 10, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetRecentActivity: %v", err)
	}

	// Find our entries.
	var found int
	for _, e := range entries {
		if e.UserID == user.ID {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected 2 activity entries for user, got %d", found)
	}

	// Verify ordering: most recent first.
	for i := 1; i < len(entries); i++ {
		if entries[i].CreatedAt.After(entries[i-1].CreatedAt) {
			t.Errorf("activity entries not in DESC order at index %d", i)
		}
	}
}

func TestGetRecentActivity_ExcludesUser(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create two users.
	telegramA := int64(202101)
	telegramB := int64(202102)

	_ = store.DeleteUserByTelegramID(ctx, telegramA)
	_ = store.DeleteUserByTelegramID(ctx, telegramB)

	userA, err := store.GetOrCreateUser(ctx, telegramA, "ExcludeA")
	if err != nil {
		t.Fatalf("GetOrCreateUser A: %v", err)
	}
	userB, err := store.GetOrCreateUser(ctx, telegramB, "ExcludeB")
	if err != nil {
		t.Fatalf("GetOrCreateUser B: %v", err)
	}

	_ = store.LogActivity(ctx, userA.ID, "ExcludeA", "message", "from A")
	_ = store.LogActivity(ctx, userB.ID, "ExcludeB", "message", "from B")

	// Exclude userA — should only see userB's entries (at minimum).
	entries, err := store.GetRecentActivity(ctx, 100, userA.ID)
	if err != nil {
		t.Fatalf("GetRecentActivity: %v", err)
	}

	for _, e := range entries {
		if e.UserID == userA.ID {
			t.Errorf("GetRecentActivity returned entry for excluded user %s", userA.ID)
		}
	}

	// Verify userB's entry is present.
	var foundB bool
	for _, e := range entries {
		if e.UserID == userB.ID && e.Content == "from B" {
			foundB = true
		}
	}
	if !foundB {
		t.Error("expected to find userB activity entry")
	}
}

// ---------------------------------------------------------------------------
// Insight tests
// ---------------------------------------------------------------------------

func TestSaveInsight(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(203001)

	_ = store.DeleteUserByTelegramID(ctx, telegramID)
	user, err := store.GetOrCreateUser(ctx, telegramID, "InsightUser")
	if err != nil {
		t.Fatalf("GetOrCreateUser: %v", err)
	}

	if err := store.SaveInsight(ctx, user.ID, "likes hiking", "agent"); err != nil {
		t.Fatalf("SaveInsight: %v", err)
	}
	if err := store.SaveInsight(ctx, user.ID, "reads science fiction", "self-reported"); err != nil {
		t.Fatalf("SaveInsight second: %v", err)
	}

	insights, err := store.GetInsights(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetInsights: %v", err)
	}
	if len(insights) != 2 {
		t.Fatalf("GetInsights: got %d, want 2", len(insights))
	}
	if insights[0].Insight != "likes hiking" {
		t.Errorf("insight[0]: got %q, want %q", insights[0].Insight, "likes hiking")
	}
	if insights[0].Source != "agent" {
		t.Errorf("insight[0].Source: got %q, want %q", insights[0].Source, "agent")
	}
	if insights[1].Source != "self-reported" {
		t.Errorf("insight[1].Source: got %q, want %q", insights[1].Source, "self-reported")
	}
}

// ---------------------------------------------------------------------------
// User enrichment tests
// ---------------------------------------------------------------------------

func TestGetUserByDisplayName(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	telegramID := int64(204001)

	_ = store.DeleteUserByTelegramID(ctx, telegramID)
	user, err := store.GetOrCreateUser(ctx, telegramID, "CaseSensitiveUser")
	if err != nil {
		t.Fatalf("GetOrCreateUser: %v", err)
	}

	// Exact match.
	found, err := store.GetUserByDisplayName(ctx, "CaseSensitiveUser")
	if err != nil {
		t.Fatalf("GetUserByDisplayName exact: %v", err)
	}
	if found == nil {
		t.Fatal("expected non-nil user for exact display name")
	}
	if found.ID != user.ID {
		t.Errorf("ID mismatch: got %s, want %s", found.ID, user.ID)
	}

	// Case-insensitive match (all lower).
	foundLower, err := store.GetUserByDisplayName(ctx, "casesensitiveuser")
	if err != nil {
		t.Fatalf("GetUserByDisplayName lower: %v", err)
	}
	if foundLower == nil {
		t.Fatal("expected non-nil user for lower-cased display name")
	}
	if foundLower.ID != user.ID {
		t.Errorf("ID mismatch (lower): got %s, want %s", foundLower.ID, user.ID)
	}

	// Not found.
	notFound, err := store.GetUserByDisplayName(ctx, "nobody_at_all_xyz")
	if err != nil {
		t.Fatalf("GetUserByDisplayName not found: %v", err)
	}
	if notFound != nil {
		t.Errorf("expected nil for unknown display name, got %+v", notFound)
	}
}

// ---------------------------------------------------------------------------
// Message forwarding tests
// ---------------------------------------------------------------------------

func TestSaveMessage(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Create two users.
	_ = store.DeleteUserByTelegramID(ctx, 205001)
	_ = store.DeleteUserByTelegramID(ctx, 205002)

	sender, err := store.GetOrCreateUser(ctx, 205001, "MsgSender")
	if err != nil {
		t.Fatalf("GetOrCreateUser sender: %v", err)
	}
	recipient, err := store.GetOrCreateUser(ctx, 205002, "MsgRecipient")
	if err != nil {
		t.Fatalf("GetOrCreateUser recipient: %v", err)
	}

	msg, err := store.SaveMessage(ctx, sender.ID, recipient.ID, "Hello there!")
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.ID == "" {
		t.Error("expected non-empty message ID")
	}
	if msg.SenderID != sender.ID {
		t.Errorf("SenderID: got %s, want %s", msg.SenderID, sender.ID)
	}
	if msg.RecipientID != recipient.ID {
		t.Errorf("RecipientID: got %s, want %s", msg.RecipientID, recipient.ID)
	}
	if msg.Content != "Hello there!" {
		t.Errorf("Content: got %q, want %q", msg.Content, "Hello there!")
	}
	if msg.DeliveredAt != nil {
		t.Error("expected DeliveredAt to be nil on creation")
	}
	if msg.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestGetPendingMessages(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	_ = store.DeleteUserByTelegramID(ctx, 205003)
	_ = store.DeleteUserByTelegramID(ctx, 205004)

	sender, err := store.GetOrCreateUser(ctx, 205003, "PendingSender")
	if err != nil {
		t.Fatalf("GetOrCreateUser sender: %v", err)
	}
	recipient, err := store.GetOrCreateUser(ctx, 205004, "PendingRecipient")
	if err != nil {
		t.Fatalf("GetOrCreateUser recipient: %v", err)
	}

	// Save two messages.
	_, err = store.SaveMessage(ctx, sender.ID, recipient.ID, "First message")
	if err != nil {
		t.Fatalf("SaveMessage 1: %v", err)
	}
	_, err = store.SaveMessage(ctx, sender.ID, recipient.ID, "Second message")
	if err != nil {
		t.Fatalf("SaveMessage 2: %v", err)
	}

	msgs, err := store.GetPendingMessages(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetPendingMessages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 pending messages, got %d", len(msgs))
	}

	// Verify sender name is joined.
	for _, m := range msgs {
		if m.SenderName != "PendingSender" {
			t.Errorf("SenderName: got %q, want %q", m.SenderName, "PendingSender")
		}
		if m.RecipientName != "PendingRecipient" {
			t.Errorf("RecipientName: got %q, want %q", m.RecipientName, "PendingRecipient")
		}
	}
}

func TestMarkMessageDelivered(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	_ = store.DeleteUserByTelegramID(ctx, 205005)
	_ = store.DeleteUserByTelegramID(ctx, 205006)

	sender, err := store.GetOrCreateUser(ctx, 205005, "DelivSender")
	if err != nil {
		t.Fatalf("GetOrCreateUser sender: %v", err)
	}
	recipient, err := store.GetOrCreateUser(ctx, 205006, "DelivRecipient")
	if err != nil {
		t.Fatalf("GetOrCreateUser recipient: %v", err)
	}

	msg, err := store.SaveMessage(ctx, sender.ID, recipient.ID, "Deliver me")
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Pending before marking.
	pending, err := store.GetPendingMessages(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetPendingMessages before: %v", err)
	}
	var found bool
	for _, m := range pending {
		if m.ID == msg.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected message to be in pending list before delivery")
	}

	// Mark as delivered.
	if err := store.MarkMessageDelivered(ctx, msg.ID); err != nil {
		t.Fatalf("MarkMessageDelivered: %v", err)
	}

	// Should no longer be pending.
	pending2, err := store.GetPendingMessages(ctx, recipient.ID)
	if err != nil {
		t.Fatalf("GetPendingMessages after: %v", err)
	}
	for _, m := range pending2 {
		if m.ID == msg.ID {
			t.Error("expected message to NOT be in pending list after delivery")
		}
	}
}

func TestGetCommunityStats_ReturnsNonNegativeCounts(t *testing.T) {
	dbURL := testDatabaseURL(t)

	store, err := db.NewStore(dbURL)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	stats, err := store.GetCommunityStats(ctx)
	if err != nil {
		t.Fatalf("GetCommunityStats: %v", err)
	}
	if stats == nil {
		t.Fatal("expected non-nil CommunityStats")
	}
	if stats.TotalUsers < 0 {
		t.Errorf("TotalUsers should be non-negative, got %d", stats.TotalUsers)
	}
	if stats.UsersWithProfiles < 0 {
		t.Errorf("UsersWithProfiles should be non-negative, got %d", stats.UsersWithProfiles)
	}
	if stats.TotalActivities < 0 {
		t.Errorf("TotalActivities should be non-negative, got %d", stats.TotalActivities)
	}
}
