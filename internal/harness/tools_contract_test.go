package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go-agent-harness/internal/goals"
	htools "go-agent-harness/internal/harness/tools"
)

// TestGoalsToolRegistersWithManager verifies the goals tool is present exactly
// when a GoalManager is wired: absent by default, registered when provided.
func TestGoalsToolRegistersWithManager(t *testing.T) {
	t.Parallel()

	has := func(reg *Registry) bool {
		for _, def := range reg.Definitions() {
			if def.Name == "goals" {
				return true
			}
		}
		return false
	}

	if has(NewDefaultRegistry(t.TempDir())) {
		t.Error("goals tool should not be registered without a GoalManager")
	}

	withMgr := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		GoalManager: goals.NewManager(nil),
	})
	if !has(withMgr) {
		t.Error("goals tool should be registered when a GoalManager is provided")
	}
}

func TestDefaultRegistryToolContract(t *testing.T) {
	t.Parallel()

	registry := NewDefaultRegistry(t.TempDir())
	defs := registry.Definitions()

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
		if def.Parameters == nil {
			t.Fatalf("tool %q missing parameters schema", def.Name)
		}
	}

	expected := []string{
		"AskUserQuestion",
		"apply_patch",
		"bash",
		"compact_history",
		"context_status",
		"create_prompt_extension",
		"deploy",
		"download",
		"edit",
		"file_inspect",
		"find_tool",
		"get_efficiency_report",
		"get_profile",
		"get_profile_manifest",
		"git_blame_context",
		"git_contributor_context",
		"git_diff_range",
		"git_file_history",
		"git_log_search",
		"job_kill",
		"job_output",
		"list_profiles",
		"observational_memory",
		"read",
		"recommend_profile",
		"todos",
		"validate_profile",
		"working_memory",
		"write",
	}
	if len(names) != len(expected) {
		t.Fatalf("expected %d tools, got %d (%v)", len(expected), len(names), names)
	}
	for i := range expected {
		if names[i] != expected[i] {
			t.Fatalf("unexpected tools order/value. got=%v want=%v", names, expected)
		}
	}
}

// ---------- mock SkillLister for contract tests ----------

type contractMockSkillLister struct {
	skills map[string]htools.SkillInfo
}

func (m *contractMockSkillLister) GetSkill(name string) (htools.SkillInfo, bool) {
	s, ok := m.skills[name]
	return s, ok
}

func (m *contractMockSkillLister) ListSkills() []htools.SkillInfo {
	result := make([]htools.SkillInfo, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	return result
}

func (m *contractMockSkillLister) ResolveSkill(_ context.Context, name, args, workspace string) (string, error) {
	if _, ok := m.skills[name]; !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	return "instructions for " + name, nil
}

func TestDefaultRegistryToolContractWithSkills(t *testing.T) {
	t.Parallel()

	lister := &contractMockSkillLister{
		skills: map[string]htools.SkillInfo{
			"deploy": {
				Name:        "deploy",
				Description: "Deploy to production",
				Source:      "project",
			},
		},
	}

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
		SkillLister:  lister,
	})
	defs := registry.Definitions()

	// With skills enabled, the skill tool should appear as a core tool
	found := false
	for _, def := range defs {
		if def.Name == "skill" {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(defs))
		for _, def := range defs {
			names = append(names, def.Name)
		}
		t.Fatalf("expected 'skill' tool in registry with skills enabled, got: %v", names)
	}
}

func TestDefaultRegistryToolContractWithSkills_ZeroSkills(t *testing.T) {
	t.Parallel()

	lister := &contractMockSkillLister{
		skills: map[string]htools.SkillInfo{},
	}

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
		SkillLister:  lister,
	})
	defs := registry.Definitions()

	// With zero skills, the skill tool should NOT appear
	for _, def := range defs {
		if def.Name == "skill" {
			t.Fatal("skill tool should not be in registry when lister returns zero skills")
		}
	}
}

// ---------- mock ConversationStore for contract tests ----------

type contractMockConversationStore struct{}

func (m *contractMockConversationStore) Migrate(_ context.Context) error { return nil }
func (m *contractMockConversationStore) Close() error                    { return nil }
func (m *contractMockConversationStore) SaveConversation(_ context.Context, _ string, _ []Message) error {
	return nil
}
func (m *contractMockConversationStore) SaveConversationWithCost(_ context.Context, _ string, _ []Message, _ ConversationTokenCost) error {
	return nil
}
func (m *contractMockConversationStore) LoadMessages(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}
func (m *contractMockConversationStore) ListConversations(_ context.Context, _ ConversationFilter, _, _ int) ([]Conversation, error) {
	return []Conversation{
		{ID: "test-id", Title: "Test", CreatedAt: time.Now(), UpdatedAt: time.Now(), MsgCount: 1},
	}, nil
}
func (m *contractMockConversationStore) DeleteConversation(_ context.Context, _ string) error {
	return nil
}
func (m *contractMockConversationStore) UpdateConversationMeta(_ context.Context, _, _, _ string) error {
	return nil
}
func (m *contractMockConversationStore) SearchMessages(_ context.Context, _ string, _ string, _ int) ([]MessageSearchResult, error) {
	return []MessageSearchResult{}, nil
}
func (m *contractMockConversationStore) DeleteOldConversations(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
func (m *contractMockConversationStore) PinConversation(_ context.Context, _ string, _ bool) error {
	return nil
}
func (m *contractMockConversationStore) CompactConversation(_ context.Context, _ string, _ int, _ Message) error {
	return nil
}
func (m *contractMockConversationStore) UndoPrompts(_ context.Context, _ string, _ int) (int, error) {
	return 0, nil
}
func (m *contractMockConversationStore) GetConversationOwner(_ context.Context, _ string) (*Conversation, error) {
	return nil, nil
}
func (m *contractMockConversationStore) ForkConversation(_ context.Context, _, _ string) (*Conversation, error) {
	return nil, nil
}

// TestDefaultRegistryToolContractWithConversations verifies conversation tools appear when store is configured.
func TestDefaultRegistryToolContractWithConversations(t *testing.T) {
	t.Parallel()

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode:      ToolApprovalModeFullAuto,
		ConversationStore: &contractMockConversationStore{},
	})
	defs := registry.Definitions()

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}

	foundList := false
	foundSearch := false
	for _, name := range names {
		if name == "list_conversations" {
			foundList = true
		}
		if name == "search_conversations" {
			foundSearch = true
		}
	}
	if !foundList {
		t.Fatalf("expected 'list_conversations' tool in registry, got: %v", names)
	}
	if !foundSearch {
		t.Fatalf("expected 'search_conversations' tool in registry, got: %v", names)
	}
}

// TestDefaultRegistryToolContractWithoutConversations verifies conversation tools are absent when store is nil.
func TestDefaultRegistryToolContractWithoutConversations(t *testing.T) {
	t.Parallel()

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
	})
	defs := registry.Definitions()

	for _, def := range defs {
		if def.Name == "list_conversations" {
			t.Fatal("list_conversations should not appear when ConversationStore is nil")
		}
		if def.Name == "search_conversations" {
			t.Fatal("search_conversations should not appear when ConversationStore is nil")
		}
	}
}

// TestDefaultRegistryNoLSPToolsInVisibleSet pins the policy that LSP/code-intel tools
// are not included in the default visible registry. They require a running language
// server and are intentionally excluded from the default configuration. This test
// prevents accidental re-introduction of LSP tools into the visible set.
func TestDefaultRegistryNoLSPToolsInVisibleSet(t *testing.T) {
	t.Parallel()

	lspToolNames := []string{"lsp_diagnostics", "lsp_references", "lsp_restart"}

	registry := NewDefaultRegistryWithOptions(t.TempDir(), DefaultRegistryOptions{
		ApprovalMode: ToolApprovalModeFullAuto,
	})

	// Check visible (core) tools.
	for _, def := range registry.Definitions() {
		for _, lsp := range lspToolNames {
			if def.Name == lsp {
				t.Errorf("LSP tool %q must not appear in the default visible registry (core tier); "+
					"it requires a running language server and is not supported in the default config", lsp)
			}
		}
	}

	// Check deferred tools as well — they should not be wired either.
	for _, def := range registry.DeferredDefinitions() {
		for _, lsp := range lspToolNames {
			if def.Name == lsp {
				t.Errorf("LSP tool %q must not appear in the default deferred registry; "+
					"it requires a running language server and is not supported in the default config", lsp)
			}
		}
	}
}
