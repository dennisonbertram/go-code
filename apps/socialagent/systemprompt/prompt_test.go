package systemprompt

import (
	"strings"
	"testing"
)

func TestRender_NewUser(t *testing.T) {
	ctx := UserContext{
		DisplayName: "Alice",
		UserID:      "user-abc-123",
		IsNewUser:   true,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	if !strings.Contains(output, "Alice") {
		t.Error("expected output to contain the user's DisplayName 'Alice'")
	}

	// New user path should include welcome context
	if !strings.Contains(output, "new user") {
		t.Error("expected output to contain 'new user' welcome context")
	}

	// Should NOT contain existing-user-only sections (summary/interests/lookingfor)
	if strings.Contains(output, "Here's what you know about them:") {
		t.Error("new user output should not include summary section")
	}
}

func TestRender_ExistingUser(t *testing.T) {
	ctx := UserContext{
		DisplayName: "Bob",
		UserID:      "user-def-456",
		Summary:     "Avid rock climber from Denver who loves craft beer.",
		Interests:   []string{"rock climbing", "craft beer", "photography"},
		LookingFor:  "adventure buddies",
		IsNewUser:   false,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	if !strings.Contains(output, "Bob") {
		t.Error("expected output to contain DisplayName 'Bob'")
	}

	if !strings.Contains(output, "Avid rock climber from Denver who loves craft beer.") {
		t.Error("expected output to contain the user's summary")
	}

	if !strings.Contains(output, "rock climbing") {
		t.Error("expected output to contain interest 'rock climbing'")
	}

	if !strings.Contains(output, "craft beer") {
		t.Error("expected output to contain interest 'craft beer'")
	}

	if !strings.Contains(output, "photography") {
		t.Error("expected output to contain interest 'photography'")
	}

	if !strings.Contains(output, "adventure buddies") {
		t.Error("expected output to contain LookingFor 'adventure buddies'")
	}
}

func TestRender_ExistingUserNoSummary(t *testing.T) {
	ctx := UserContext{
		DisplayName: "Carol",
		UserID:      "user-ghi-789",
		Summary:     "", // no summary yet
		Interests:   []string{"hiking"},
		LookingFor:  "trail partners",
		IsNewUser:   false,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	if !strings.Contains(output, "Carol") {
		t.Error("expected output to contain DisplayName 'Carol'")
	}

	if strings.Contains(output, "Here's what you know about them:") {
		t.Error("output should not include summary section when summary is empty")
	}

	if !strings.Contains(output, "hiking") {
		t.Error("expected output to still contain interests even when summary is empty")
	}

	if !strings.Contains(output, "trail partners") {
		t.Error("expected output to contain LookingFor")
	}
}

func TestRender_ContainsPrivacyRules(t *testing.T) {
	ctx := UserContext{
		DisplayName: "TestUser",
		UserID:      "user-test-001",
		IsNewUser:   false,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	privacyPhrases := []string{
		"NEVER",
		"phone number",
	}

	for _, phrase := range privacyPhrases {
		if !strings.Contains(output, phrase) {
			t.Errorf("expected output to contain privacy phrase %q", phrase)
		}
	}
}

func TestRender_ContainsToolInstructions(t *testing.T) {
	ctx := UserContext{
		DisplayName: "TestUser",
		UserID:      "user-test-002",
		IsNewUser:   false,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	tools := []string{
		"search_users",
		"get_user_profile",
		"get_updates",
		"save_insight",
		"get_my_profile",
	}

	for _, tool := range tools {
		if !strings.Contains(output, tool) {
			t.Errorf("expected output to contain tool name %q", tool)
		}
	}
}

func TestRender_InjectsDisplayNameForToolCalls(t *testing.T) {
	ctx := UserContext{
		DisplayName: "Dave",
		UserID:      "550e8400-e29b-41d4-a716-446655440000",
		IsNewUser:   false,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	// The agent must see its display name so it can pass it to tools like
	// send_message_to_user, save_insight, get_my_profile, and get_my_messages.
	if !strings.Contains(output, "Dave") {
		t.Error("expected output to contain the current user's DisplayName 'Dave'")
	}

	// The prompt must explicitly mention sender_name so the agent knows to use display names.
	if !strings.Contains(output, "sender_name") {
		t.Error("expected output to mention sender_name so agent knows to pass display name")
	}

	// The prompt should mention send_message_to_user in the tool instructions section.
	if !strings.Contains(output, "send_message_to_user") {
		t.Error("expected output to mention send_message_to_user tool")
	}

	// Verify the same holds for a new user.
	ctxNew := UserContext{
		DisplayName: "Eve",
		UserID:      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		IsNewUser:   true,
	}
	outputNew, err := Render(ctxNew)
	if err != nil {
		t.Fatalf("Render (new user) returned unexpected error: %v", err)
	}
	if !strings.Contains(outputNew, "Eve") {
		t.Error("expected new-user output to contain the current user's DisplayName 'Eve'")
	}
}

func TestRender_ContainsPersonality(t *testing.T) {
	ctx := UserContext{
		DisplayName: "TestUser",
		UserID:      "user-test-003",
		IsNewUser:   false,
	}

	output, err := Render(ctx)
	if err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}

	// Verify personality elements are present
	personalityElements := []string{
		"Connector",  // agent name
		"connection", // core purpose word
	}

	for _, elem := range personalityElements {
		if !strings.Contains(output, elem) {
			t.Errorf("expected output to contain personality element %q", elem)
		}
	}
}
