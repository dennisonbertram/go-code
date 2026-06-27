package relay_test

import (
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

func TestRunContractValidate(t *testing.T) {
	tests := []struct {
		name    string
		rc      *relay.RunContract
		wantErr bool
	}{
		{
			name: "valid contract",
			rc: &relay.RunContract{
				ID:     "rc-1",
				Prompt: "Fix the bug",
				Source: relay.TriggerSource{Type: "api", TriggerID: "t1"},
				Workspace: relay.WorkspaceTarget{Mode: "local"},
				Mobility: relay.MobilityResumable,
				Metadata: relay.RunMetadata{
					TenantID:  "t1",
					CreatedBy: "user:alice",
					CreatedAt: time.Now(),
				},
			},
			wantErr: false,
		},
		{
			name:    "missing id",
			rc:      &relay.RunContract{Prompt: "test", Metadata: relay.RunMetadata{TenantID: "t1"}},
			wantErr: true,
		},
		{
			name:    "missing prompt",
			rc:      &relay.RunContract{ID: "rc-1", Metadata: relay.RunMetadata{TenantID: "t1"}},
			wantErr: true,
		},
		{
			name:    "missing tenant",
			rc:      &relay.RunContract{ID: "rc-1", Prompt: "test"},
			wantErr: true,
		},
		{
			name: "defaults mobility",
			rc: &relay.RunContract{
				ID:     "rc-1",
				Prompt: "test",
				Metadata: relay.RunMetadata{
					TenantID: "t1",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid mobility class",
			rc: &relay.RunContract{
				ID:       "rc-1",
				Prompt:   "test",
				Mobility: relay.MobilityClass("teleport"),
				Metadata: relay.RunMetadata{TenantID: "t1"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rc.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunContractDefaults(t *testing.T) {
	rc := &relay.RunContract{
		ID:     "rc-1",
		Prompt: "test",
		Metadata: relay.RunMetadata{
			TenantID: "t1",
		},
	}
	if err := rc.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if rc.Mobility != relay.MobilityResumable {
		t.Errorf("default Mobility: got %q, want resumable", rc.Mobility)
	}
	if rc.Metadata.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set when zero")
	}
}

func TestRunContractJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rc := &relay.RunContract{
		ID:     "rc-json",
		Prompt: "Fix the race condition",
		Source: relay.TriggerSource{
			Type:      "github",
			TriggerID: "gh-123",
			ThreadID:  "github:org/repo:42",
		},
		Workspace: relay.WorkspaceTarget{
			Mode:    "worktree",
			RepoURL: "https://github.com/org/repo.git",
			Clean:   true,
		},
		Capabilities: relay.CapabilityPack{
			Tools: []relay.ToolCapability{{Name: "bash"}, {Name: "write"}},
			MCPServers: []relay.MCPServerCapability{{Name: "context7"}},
		},
		Outputs: []relay.OutputExpectation{
			{Type: "patch", Format: "diff"},
			{Type: "github:pr", Target: "github:pr"},
		},
		Mobility: relay.MobilityResumable,
		Metadata: relay.RunMetadata{
			TenantID:  "t1",
			CreatedBy: "user:alice",
			CreatedAt: now,
			Tags:      []string{"bugfix"},
		},
	}

	data, err := rc.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	restored, err := relay.RunContractFromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if restored.ID != rc.ID {
		t.Errorf("ID: got %q, want %q", restored.ID, rc.ID)
	}
	if restored.Prompt != rc.Prompt {
		t.Errorf("Prompt: got %q, want %q", restored.Prompt, rc.Prompt)
	}
	if restored.Source.Type != "github" {
		t.Errorf("Source.Type: got %q, want github", restored.Source.Type)
	}
	if restored.Workspace.Mode != "worktree" {
		t.Errorf("Workspace.Mode: got %q, want worktree", restored.Workspace.Mode)
	}
	if len(restored.Capabilities.Tools) != 2 {
		t.Errorf("Tools: got %d, want 2", len(restored.Capabilities.Tools))
	}
	if len(restored.Outputs) != 2 {
		t.Errorf("Outputs: got %d, want 2", len(restored.Outputs))
	}
}

func TestRunContractFromJSONInvalid(t *testing.T) {
	_, err := relay.RunContractFromJSON([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestComposerComposeValid(t *testing.T) {
	c := relay.NewComposer()

	req := relay.ComposeRequest{
		Prompt:   "Fix the race condition in pool.go",
		TenantID: "t1",
		Source: relay.TriggerSource{
			Type:      "github",
			TriggerID: "gh-123",
			ThreadID:  "github:org/repo:42",
		},
		RequestedTools:          []string{"bash", "read", "write"},
		RequestedMCPServers:     []string{"context7"},
		RequestedOutputSurfaces: []string{"github:pr", "github:comment"},
		RepoURL:                 "https://github.com/org/repo.git",
		WorkspaceMode:           "worktree",
		CreatedBy:               "user:alice",
		Tags:                    []string{"bugfix", "high-priority"},
	}

	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if contract.ID == "" {
		t.Error("contract ID should not be empty")
	}
	if contract.Prompt != req.Prompt {
		t.Errorf("Prompt: got %q, want %q", contract.Prompt, req.Prompt)
	}
	if contract.Source.Type != "github" {
		t.Errorf("Source.Type: got %q, want github", contract.Source.Type)
	}
	if contract.Workspace.Mode != "worktree" {
		t.Errorf("Workspace.Mode: got %q, want worktree", contract.Workspace.Mode)
	}
	if !contract.Capabilities.HasTool("bash") {
		t.Error("capabilities should include bash")
	}
	if !contract.Capabilities.HasMCPServer("context7") {
		t.Error("capabilities should include context7")
	}
	if len(contract.Outputs) != 2 {
		t.Errorf("Outputs: got %d, want 2", len(contract.Outputs))
	}
}

func TestComposerComposeMissingPrompt(t *testing.T) {
	c := relay.NewComposer()
	req := relay.ComposeRequest{
		Prompt:   "",
		TenantID: "t1",
	}
	_, err := c.Compose(req)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestComposerComposeDefaultTenant(t *testing.T) {
	c := relay.NewComposer()
	req := relay.ComposeRequest{
		Prompt: "test",
		// TenantID intentionally empty.
	}
	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if contract.Metadata.TenantID != "default" {
		t.Errorf("default TenantID: got %q, want default", contract.Metadata.TenantID)
	}
}

func TestComposerComposeDefaults(t *testing.T) {
	c := relay.NewComposer()
	req := relay.ComposeRequest{
		Prompt:    "test",
		TenantID:  "t1",
		CreatedBy: "user:alice",
	}
	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if contract.Mobility != relay.MobilityResumable {
		t.Errorf("default Mobility: got %q, want resumable", contract.Mobility)
	}
	if contract.Workspace.Mode != "local" {
		t.Errorf("default WorkspaceMode: got %q, want local", contract.Workspace.Mode)
	}
}

func TestComposerContextHintTruncation(t *testing.T) {
	c := relay.NewComposer()
	c.MaxContextHintBytes = 100

	longHint := ""
	for i := 0; i < 200; i++ {
		longHint += "x"
	}

	req := relay.ComposeRequest{
		Prompt:      "test",
		TenantID:    "t1",
		ContextHint: longHint,
	}
	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(contract.ContextHint) > c.MaxContextHintBytes {
		t.Errorf("context hint: got %d bytes, want <= %d", len(contract.ContextHint), c.MaxContextHintBytes)
	}
	if !stringsContains(contract.ContextHint, "truncated") {
		t.Error("truncated context hint should have truncation marker")
	}
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestComposerLocalOnlyApprovals(t *testing.T) {
	c := relay.NewComposer()

	req := relay.ComposeRequest{
		Prompt:    "test",
		TenantID:  "t1",
		LocalOnly: true,
	}
	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(contract.Permissions.ApprovalRequired) != 0 {
		t.Errorf("local-only: expected no approval requirements, got %v", contract.Permissions.ApprovalRequired)
	}

	req2 := relay.ComposeRequest{
		Prompt:    "test",
		TenantID:  "t1",
		LocalOnly: false,
	}
	contract2, err := c.Compose(req2)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(contract2.Permissions.ApprovalRequired) == 0 {
		t.Error("non-local: expected some approval requirements")
	}
}

func TestComposerAPIDefaultOutput(t *testing.T) {
	c := relay.NewComposer()
	req := relay.ComposeRequest{
		Prompt:   "test",
		TenantID: "t1",
		Source: relay.TriggerSource{
			Type: "api",
		},
	}
	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(contract.Outputs) != 1 {
		t.Errorf("API request: expected 1 default output, got %d", len(contract.Outputs))
	}
	if len(contract.Outputs) > 0 && contract.Outputs[0].Type != "summary" {
		t.Errorf("API request: expected summary output, got %s", contract.Outputs[0].Type)
	}
}

func TestComposerComposePreservesContextHint(t *testing.T) {
	c := relay.NewComposer()
	req := relay.ComposeRequest{
		Prompt:      "test",
		TenantID:    "t1",
		ContextHint: "This is important context from the issue body.",
	}
	contract, err := c.Compose(req)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if contract.ContextHint != req.ContextHint {
		t.Errorf("ContextHint: got %q, want %q", contract.ContextHint, req.ContextHint)
	}
}
