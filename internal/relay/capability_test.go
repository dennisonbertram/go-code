package relay_test

import (
	"context"
	"testing"

	"go-agent-harness/internal/relay"
)

func TestValidateCapabilityType(t *testing.T) {
	tests := []struct {
		name    string
		ct      relay.CapabilityType
		wantErr error
	}{
		{"tool", relay.CapabilityTool, nil},
		{"mcp_server", relay.CapabilityMCPServer, nil},
		{"memory", relay.CapabilityMemory, nil},
		{"repo", relay.CapabilityRepo, nil},
		{"workspace_mode", relay.CapabilityWorkspaceMode, nil},
		{"secret", relay.CapabilitySecret, nil},
		{"output_surface", relay.CapabilityOutputSurface, nil},
		{"browser", relay.CapabilityBrowser, nil},
		{"docker", relay.CapabilityDocker, nil},
		{"invalid", relay.CapabilityType("invalid"), relay.ErrInvalidCapabilityType},
		{"empty", relay.CapabilityType(""), relay.ErrInvalidCapabilityType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := relay.ValidateCapabilityType(tt.ct)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("ValidateCapabilityType(%q) = %v, want %v", tt.ct, err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateCapabilityType(%q) = %v, want nil", tt.ct, err)
				}
			}
		})
	}
}

func TestCapabilityPackHasTool(t *testing.T) {
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Tools: []relay.ToolCapability{
			{Name: "bash"},
			{Name: "read"},
			{Name: "write"},
		},
	}

	if !pack.HasTool("bash") {
		t.Error("HasTool(bash) should be true")
	}
	if pack.HasTool("edit") {
		t.Error("HasTool(edit) should be false")
	}
}

func TestCapabilityPackHasSecret(t *testing.T) {
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Secrets: []relay.SecretCapability{
			{Name: "github-token", Ref: "refs/secrets/github"},
			{Name: "slack-token", Ref: "refs/secrets/slack"},
		},
	}

	if !pack.HasSecret("refs/secrets/github") {
		t.Error("HasSecret(github) should be true")
	}
	if pack.HasSecret("refs/secrets/unknown") {
		t.Error("HasSecret(unknown) should be false")
	}
}

func TestCapabilityPackHasMCPServer(t *testing.T) {
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		MCPServers: []relay.MCPServerCapability{
			{Name: "context7"},
			{Name: "posthog"},
		},
	}

	if !pack.HasMCPServer("context7") {
		t.Error("HasMCPServer(context7) should be true")
	}
	if pack.HasMCPServer("unknown") {
		t.Error("HasMCPServer(unknown) should be false")
	}
}

func TestCapabilityPackJSON(t *testing.T) {
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Tools: []relay.ToolCapability{
			{Name: "bash", DisplayName: "Bash Shell"},
		},
		Secrets: []relay.SecretCapability{
			{Name: "gh-token", Ref: "refs/gh", Scope: "repo"},
		},
	}

	data, err := pack.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	restored, err := relay.CapabilityPackFromJSON(data)
	if err != nil {
		t.Fatalf("CapabilityPackFromJSON: %v", err)
	}
	if restored.RunID != pack.RunID {
		t.Errorf("RunID: got %q, want %q", restored.RunID, pack.RunID)
	}
	if !restored.HasTool("bash") {
		t.Error("restored pack should have bash")
	}
	if !restored.HasSecret("refs/gh") {
		t.Error("restored pack should have gh secret ref")
	}
}

func TestCapabilityPackFromJSONInvalid(t *testing.T) {
	_, err := relay.CapabilityPackFromJSON([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSanitizeInventoryForDisplay(t *testing.T) {
	inv := &relay.CapabilityInventory{
		WorkerID: "w-1",
		Repos: []relay.RepoCapability{
			{RepoURL: "https://github.com/org/repo.git", RepoPath: "/home/user/repos/repo", SecretRef: "refs/gh"},
		},
		Secrets: []relay.SecretCapability{
			{Name: "token", Ref: "refs/gh", Scope: "repo"},
			{Name: "bad", Ref: "this-is-a-very-long-ref-that-exceeds-128-chars-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", Scope: "repo"},
		},
	}

	// Local worker: paths are preserved, secrets are checked.
	sanitized := relay.SanitizeInventoryForDisplay(inv, relay.LocationLocal)
	if sanitized.Repos[0].RepoPath != "/home/user/repos/repo" {
		t.Errorf("local worker: repo path should be preserved, got %q", sanitized.Repos[0].RepoPath)
	}
	if sanitized.Secrets[1].Ref != "[invalid: ref too long]" {
		t.Errorf("long secret ref should be marked invalid, got %q", sanitized.Secrets[1].Ref)
	}

	// Non-local worker: paths are redacted.
	sanitized2 := relay.SanitizeInventoryForDisplay(inv, relay.LocationVM)
	if sanitized2.Repos[0].RepoPath != "[redacted: non-local worker]" {
		t.Errorf("non-local worker: repo path should be redacted, got %q", sanitized2.Repos[0].RepoPath)
	}
	if sanitized2.Repos[0].SecretRef != "[redacted]" {
		t.Errorf("non-local worker: secret ref should be redacted, got %q", sanitized2.Repos[0].SecretRef)
	}
}

func TestSanitizeInventoryForDisplayNil(t *testing.T) {
	if relay.SanitizeInventoryForDisplay(nil, relay.LocationLocal) != nil {
		t.Error("SanitizeInventoryForDisplay(nil) should return nil")
	}
}

func TestSanitizePackForDisplay(t *testing.T) {
	pack := &relay.CapabilityPack{
		RunID: "run-1",
		Repos: []relay.RepoCapability{
			{RepoURL: "https://github.com/org/repo.git", RepoPath: "/home/user/repos/repo"},
		},
	}

	sanitized := relay.SanitizePackForDisplay(pack, relay.LocationContainer)
	if sanitized.Repos[0].RepoPath != "[redacted: non-local worker]" {
		t.Errorf("container worker: repo path should be redacted, got %q", sanitized.Repos[0].RepoPath)
	}
}

func TestSanitizePackForDisplayNil(t *testing.T) {
	if relay.SanitizePackForDisplay(nil, relay.LocationLocal) != nil {
		t.Error("SanitizePackForDisplay(nil) should return nil")
	}
}

// capabilityStoreFactory creates a fresh CapabilityStore for testing.
type capabilityStoreFactory func(t *testing.T) relay.CapabilityStore

func runCapabilityStoreContractTests(t *testing.T, factory capabilityStoreFactory) {
	t.Helper()

	t.Run("SetAndGetInventory", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		inv := &relay.CapabilityInventory{
			WorkerID: "w-1",
			Tools: []relay.ToolCapability{
				{Name: "bash", DisplayName: "Bash"},
			},
			Secrets: []relay.SecretCapability{
				{Name: "gh", Ref: "refs/gh", Scope: "repo"},
			},
		}

		if err := s.SetInventory(ctx, inv); err != nil {
			t.Fatalf("SetInventory: %v", err)
		}

		got, err := s.GetInventory(ctx, "w-1")
		if err != nil {
			t.Fatalf("GetInventory: %v", err)
		}
		if got.WorkerID != "w-1" {
			t.Errorf("WorkerID: got %q, want w-1", got.WorkerID)
		}
		if len(got.Tools) != 1 {
			t.Errorf("Tools: got %d, want 1", len(got.Tools))
		}
		if len(got.Secrets) != 1 {
			t.Errorf("Secrets: got %d, want 1", len(got.Secrets))
		}
	})

	t.Run("GetInventoryNotFound", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		_, err := s.GetInventory(ctx, "nonexistent")
		if err != relay.ErrCapabilityNotFound {
			t.Errorf("GetInventory: got %v, want ErrCapabilityNotFound", err)
		}
	})

	t.Run("DeleteInventory", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		inv := &relay.CapabilityInventory{WorkerID: "w-del"}
		if err := s.SetInventory(ctx, inv); err != nil {
			t.Fatalf("SetInventory: %v", err)
		}
		if err := s.DeleteInventory(ctx, "w-del"); err != nil {
			t.Fatalf("DeleteInventory: %v", err)
		}
		_, err := s.GetInventory(ctx, "w-del")
		if err != relay.ErrCapabilityNotFound {
			t.Errorf("GetInventory after delete: got %v, want ErrCapabilityNotFound", err)
		}
	})

	t.Run("DeleteInventoryNotFound", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		err := s.DeleteInventory(ctx, "nonexistent")
		if err != relay.ErrCapabilityNotFound {
			t.Errorf("DeleteInventory: got %v, want ErrCapabilityNotFound", err)
		}
	})

	t.Run("SetAndGetPack", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		pack := &relay.CapabilityPack{
			RunID: "run-1",
			Tools: []relay.ToolCapability{
				{Name: "bash"},
				{Name: "read", DisplayName: "Read File"},
			},
			MCPServers: []relay.MCPServerCapability{
				{Name: "context7", Transport: "http"},
			},
			Secrets: []relay.SecretCapability{
				{Name: "gh", Ref: "refs/gh", Scope: "repo"},
			},
		}

		if err := s.SetPack(ctx, pack); err != nil {
			t.Fatalf("SetPack: %v", err)
		}

		got, err := s.GetPack(ctx, "run-1")
		if err != nil {
			t.Fatalf("GetPack: %v", err)
		}
		if got.RunID != "run-1" {
			t.Errorf("RunID: got %q, want run-1", got.RunID)
		}
		if !got.HasTool("bash") {
			t.Error("pack should have bash")
		}
		if !got.HasMCPServer("context7") {
			t.Error("pack should have context7")
		}
		if !got.HasSecret("refs/gh") {
			t.Error("pack should have gh secret ref")
		}
	})

	t.Run("GetPackNotFound", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		_, err := s.GetPack(ctx, "nonexistent")
		if err != relay.ErrCapabilityNotFound {
			t.Errorf("GetPack: got %v, want ErrCapabilityNotFound", err)
		}
	})

	t.Run("DeletePack", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		pack := &relay.CapabilityPack{RunID: "run-del"}
		if err := s.SetPack(ctx, pack); err != nil {
			t.Fatalf("SetPack: %v", err)
		}
		if err := s.DeletePack(ctx, "run-del"); err != nil {
			t.Fatalf("DeletePack: %v", err)
		}
		_, err := s.GetPack(ctx, "run-del")
		if err != relay.ErrCapabilityNotFound {
			t.Errorf("GetPack after delete: got %v, want ErrCapabilityNotFound", err)
		}
	})

	t.Run("SetInventoryUpdatesExisting", func(t *testing.T) {
		s := factory(t)
		ctx := context.Background()

		inv1 := &relay.CapabilityInventory{
			WorkerID: "w-update",
			Tools:    []relay.ToolCapability{{Name: "bash"}},
		}
		if err := s.SetInventory(ctx, inv1); err != nil {
			t.Fatalf("SetInventory 1: %v", err)
		}

		inv2 := &relay.CapabilityInventory{
			WorkerID: "w-update",
			Tools:    []relay.ToolCapability{{Name: "bash"}, {Name: "read"}},
		}
		if err := s.SetInventory(ctx, inv2); err != nil {
			t.Fatalf("SetInventory 2: %v", err)
		}

		got, err := s.GetInventory(ctx, "w-update")
		if err != nil {
			t.Fatalf("GetInventory: %v", err)
		}
		if len(got.Tools) != 2 {
			t.Errorf("Tools after update: got %d, want 2", len(got.Tools))
		}
	})
}
