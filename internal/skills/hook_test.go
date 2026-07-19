package skills

import (
	"testing"
)

func TestAutoInvokeHook_ExplicitNoArgs(t *testing.T) {
	reg := NewRegistry()
	reg.skills["deploy"] = &Skill{
		Name:       "deploy",
		Body:       "Deploy to $ARGUMENTS",
		FilePath:   "/skills/deploy/SKILL.md",
		AutoInvoke: true,
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("/deploy")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Name != "deploy" {
		t.Errorf("expected name %q, got %q", "deploy", activation.Name)
	}
	if activation.Content != "Deploy to " {
		t.Errorf("expected content %q, got %q", "Deploy to ", activation.Content)
	}
	if activation.Context != ContextConversation {
		t.Errorf("expected context %q, got %q", ContextConversation, activation.Context)
	}
}

func TestAutoInvokeHook_ExplicitWithArgs(t *testing.T) {
	reg := NewRegistry()
	reg.skills["deploy"] = &Skill{
		Name:       "deploy",
		Body:       "Deploy $1 to $2. Full: $ARGUMENTS",
		FilePath:   "/skills/deploy/SKILL.md",
		AutoInvoke: true,
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("/deploy staging eu-west")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Name != "deploy" {
		t.Errorf("expected name %q, got %q", "deploy", activation.Name)
	}
	want := "Deploy staging to eu-west. Full: staging eu-west"
	if activation.Content != want {
		t.Errorf("expected content %q, got %q", want, activation.Content)
	}
}

func TestAutoInvokeHook_ExplicitUnknownSkill(t *testing.T) {
	reg := NewRegistry()

	hook := AutoInvokeHook(reg)
	activation := hook("/nonexistent some args")

	if activation != nil {
		t.Errorf("expected nil activation, got %+v", activation)
	}
}

func TestAutoInvokeHook_AutoInvokeSingleMatch(t *testing.T) {
	reg := NewRegistry()
	reg.skills["review"] = &Skill{
		Name:       "review",
		Body:       "Review the code: $ARGUMENTS",
		FilePath:   "/skills/review/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"review my code"},
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("please review my code carefully")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Name != "review" {
		t.Errorf("expected name %q, got %q", "review", activation.Name)
	}
	want := "Review the code: please review my code carefully"
	if activation.Content != want {
		t.Errorf("expected content %q, got %q", want, activation.Content)
	}
}

func TestAutoInvokeHook_AutoInvokeMultipleMatchesReturnsNil(t *testing.T) {
	reg := NewRegistry()
	reg.skills["review"] = &Skill{
		Name:       "review",
		Body:       "Review body",
		FilePath:   "/skills/review/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"review code"},
		Context:    ContextConversation,
	}
	reg.skills["lint"] = &Skill{
		Name:       "lint",
		Body:       "Lint body",
		FilePath:   "/skills/lint/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"review code"},
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("review code please")

	if activation != nil {
		t.Errorf("expected nil activation for ambiguous match, got %+v", activation)
	}
}

func TestAutoInvokeHook_SkipsNonAutoInvoke(t *testing.T) {
	reg := NewRegistry()
	reg.skills["secret"] = &Skill{
		Name:       "secret",
		Body:       "Secret body",
		FilePath:   "/skills/secret/SKILL.md",
		AutoInvoke: false,
		Triggers:   []string{"do secret thing"},
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("do secret thing now")

	if activation != nil {
		t.Errorf("expected nil activation for non-auto-invoke skill, got %+v", activation)
	}
}

func TestAutoInvokeHook_EmptyMessage(t *testing.T) {
	reg := NewRegistry()
	reg.skills["test"] = &Skill{
		Name:       "test",
		Body:       "Test body",
		FilePath:   "/skills/test/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"test"},
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("")

	if activation != nil {
		t.Errorf("expected nil activation, got %+v", activation)
	}
}

func TestAutoInvokeHook_WhitespaceOnlyMessage(t *testing.T) {
	reg := NewRegistry()
	hook := AutoInvokeHook(reg)
	activation := hook("   \t\n  ")

	if activation != nil {
		t.Errorf("expected nil activation, got %+v", activation)
	}
}

func TestAutoInvokeHook_SlashCommandFallsThrough(t *testing.T) {
	// /unknown-cmd is not a registered skill, but trigger matching may pick it up
	reg := NewRegistry()
	reg.skills["helper"] = &Skill{
		Name:       "helper",
		Body:       "Helper: $ARGUMENTS",
		FilePath:   "/skills/helper/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"/unknown-cmd"},
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("/unknown-cmd do stuff")

	// "/unknown-cmd" is not a skill, so explicit lookup fails.
	// But the message contains the trigger "/unknown-cmd", so auto-invoke matches.
	if activation == nil {
		t.Fatal("expected non-nil activation from trigger fallthrough")
	}
	if activation.Name != "helper" {
		t.Errorf("expected name %q, got %q", "helper", activation.Name)
	}
	if activation.Content == "" {
		t.Error("expected non-empty content from trigger fallthrough")
	}
}

func TestAutoInvokeHook_ExplicitTakesPrecedenceOverTrigger(t *testing.T) {
	reg := NewRegistry()
	reg.skills["deploy"] = &Skill{
		Name:       "deploy",
		Body:       "Deploy: $ARGUMENTS",
		FilePath:   "/skills/deploy/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"deploy"},
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("/deploy production")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Name != "deploy" {
		t.Errorf("expected name %q, got %q", "deploy", activation.Name)
	}
	// Explicit invocation: args = "production"
	want := "Deploy: production"
	if activation.Content != want {
		t.Errorf("expected content %q, got %q", want, activation.Content)
	}
}

// --- Fork-specific hook tests ---

func TestAutoInvokeHook_ForkSkillActivation(t *testing.T) {
	reg := NewRegistry()
	reg.skills["deep-research"] = &Skill{
		Name:       "deep-research",
		Body:       "Research $ARGUMENTS thoroughly.",
		FilePath:   "/skills/deep-research/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"deep research"},
		Context:    ContextFork,
		Agent:      "Explore",
	}

	hook := AutoInvokeHook(reg)
	activation := hook("/deep-research OAuth2 PKCE")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Context != ContextFork {
		t.Errorf("Context = %q, want %q", activation.Context, ContextFork)
	}
	if activation.Agent != "Explore" {
		t.Errorf("Agent = %q, want %q", activation.Agent, "Explore")
	}
	if activation.Skill == nil {
		t.Fatal("Skill reference should not be nil")
	}
}

func TestAutoInvokeHook_ConversationSkillActivation(t *testing.T) {
	reg := NewRegistry()
	reg.skills["deploy"] = &Skill{
		Name:       "deploy",
		Body:       "Deploy: $ARGUMENTS",
		FilePath:   "/skills/deploy/SKILL.md",
		AutoInvoke: true,
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook("/deploy staging")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Context != ContextConversation {
		t.Errorf("Context = %q, want %q", activation.Context, ContextConversation)
	}
}

func TestAutoInvokeHook_AutoInvokeForkSkill(t *testing.T) {
	reg := NewRegistry()
	reg.skills["research"] = &Skill{
		Name:       "research",
		Body:       "Research: $ARGUMENTS",
		FilePath:   "/skills/research/SKILL.md",
		AutoInvoke: true,
		Triggers:   []string{"research topic"},
		Context:    ContextFork,
		Agent:      "Plan",
	}

	hook := AutoInvokeHook(reg)
	activation := hook("research topic about LLMs")

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	if activation.Context != ContextFork {
		t.Errorf("Context = %q, want %q", activation.Context, ContextFork)
	}
	if activation.Agent != "Plan" {
		t.Errorf("Agent = %q, want %q", activation.Agent, "Plan")
	}
}

func TestBuildVars(t *testing.T) {
	skill := &Skill{
		FilePath: "/home/user/skills/my-skill/SKILL.md",
	}

	vars := buildVars(skill, "alpha beta gamma", "/workspace")

	tests := []struct {
		key  string
		want string
	}{
		{"$ARGUMENTS", "alpha beta gamma"},
		{"$WORKSPACE", "/workspace"},
		{"$SKILL_DIR", "/home/user/skills/my-skill"},
		{"$1", "alpha"},
		{"$2", "beta"},
		{"$3", "gamma"},
		{"$4", ""},
		{"$9", ""},
	}

	for _, tt := range tests {
		got := vars[tt.key]
		if got != tt.want {
			t.Errorf("buildVars[%s] = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestBuildVars_NoArgs(t *testing.T) {
	skill := &Skill{
		FilePath: "/skills/test/SKILL.md",
	}

	vars := buildVars(skill, "", "")

	if vars["$ARGUMENTS"] != "" {
		t.Errorf("expected empty $ARGUMENTS, got %q", vars["$ARGUMENTS"])
	}
	if vars["$WORKSPACE"] != "" {
		t.Errorf("expected empty $WORKSPACE, got %q", vars["$WORKSPACE"])
	}
	if vars["$1"] != "" {
		t.Errorf("expected empty $1, got %q", vars["$1"])
	}
}

func TestBuildVars_QuotedArgs(t *testing.T) {
	skill := &Skill{
		FilePath: "/skills/test/SKILL.md",
	}

	vars := buildVars(skill, `run "hello world" --fast`, "")

	tests := []struct {
		key  string
		want string
	}{
		{"$ARGUMENTS", `run "hello world" --fast`}, // raw args preserved untokenized
		{"$1", "run"},
		{"$2", "hello world"}, // quoted multi-word token stays one token
		{"$3", "--fast"},
		{"$4", ""},
	}

	for _, tt := range tests {
		got := vars[tt.key]
		if got != tt.want {
			t.Errorf("buildVars[%s] = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestAutoInvokeHook_ExplicitWithQuotedArgs(t *testing.T) {
	reg := NewRegistry()
	reg.skills["deploy"] = &Skill{
		Name:       "deploy",
		Body:       "Deploy $1 to $2. Full: $ARGUMENTS",
		FilePath:   "/skills/deploy/SKILL.md",
		AutoInvoke: true,
		Context:    ContextConversation,
	}

	hook := AutoInvokeHook(reg)
	activation := hook(`/deploy "staging eu" fast`)

	if activation == nil {
		t.Fatal("expected non-nil activation")
	}
	want := `Deploy staging eu to fast. Full: "staging eu" fast`
	if activation.Content != want {
		t.Errorf("expected content %q, got %q", want, activation.Content)
	}
}
