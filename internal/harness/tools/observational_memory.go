package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"
	om "go-agent-harness/internal/observationalmemory"
)

type observationalMemoryArgs struct {
	Action string `json:"action"`
	Config *struct {
		ObserveMinTokens       int `json:"observe_min_tokens"`
		SnippetMaxTokens       int `json:"snippet_max_tokens"`
		ReflectThresholdTokens int `json:"reflect_threshold_tokens"`
	} `json:"config,omitempty"`
	Export *struct {
		Format string `json:"format,omitempty"`
		Path   string `json:"path,omitempty"`
	} `json:"export,omitempty"`
	Review *struct {
		Prompt string `json:"prompt,omitempty"`
	} `json:"review,omitempty"`
}

func observationalMemoryTool(workspaceRoot string, manager om.Manager, runner AgentRunner, sandboxScope SandboxScope) Tool {
	def := Definition{
		Name:         "observational_memory",
		Description:  descriptions.Load("observational_memory"),
		Action:       ActionWrite,
		Mutating:     true,
		ParallelSafe: false,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum": []string{"enable", "disable", "status", "export", "review", "reflect_now"},
				},
				"config": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"observe_min_tokens":       map[string]any{"type": "integer", "minimum": 1},
						"snippet_max_tokens":       map[string]any{"type": "integer", "minimum": 1},
						"reflect_threshold_tokens": map[string]any{"type": "integer", "minimum": 1},
					},
				},
				"export": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"format": map[string]any{"type": "string", "enum": []string{"json", "markdown"}},
						"path":   map[string]any{"type": "string"},
					},
				},
				"review": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"prompt": map[string]any{"type": "string"},
					},
				},
			},
			"required": []string{"action"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		args := observationalMemoryArgs{}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse observational_memory args: %w", err)
		}
		action := strings.TrimSpace(strings.ToLower(args.Action))
		if action == "" {
			return "", fmt.Errorf("action is required")
		}

		runID := RunIDFromContext(ctx)
		meta, _ := RunMetadataFromContext(ctx)
		scope := memoryScopeFromMetadata(runID, meta)
		if scope.ConversationID == "" {
			return "", fmt.Errorf("run context is required")
		}

		warnings := make([]string, 0)
		toolCallID := ToolCallIDFromContext(ctx)
		if manager == nil {
			warnings = append(warnings, "observational memory manager is not configured")
			status := om.Status{
				Mode:                     om.ModeOff,
				MemoryID:                 scope.MemoryID(),
				Scope:                    scope,
				Enabled:                  false,
				LastObservedMessageIndex: -1,
				UpdatedAt:                time.Now().UTC(),
			}
			return MarshalToolResult(map[string]any{"status": status, "warnings": warnings})
		}

		result := map[string]any{}
		var status om.Status
		var err error

		switch action {
		case "status":
			status, err = manager.Status(ctx, scope)
		case "enable":
			status, err = manager.SetEnabled(ctx, scope, true, configFromArgs(args.Config), runID, toolCallID)
		case "disable":
			status, err = manager.SetEnabled(ctx, scope, false, nil, runID, toolCallID)
		case "reflect_now":
			status, err = manager.ReflectNow(ctx, scope, runID, toolCallID)
		case "export":
			exportFormat := "json"
			if args.Export != nil && strings.TrimSpace(args.Export.Format) != "" {
				exportFormat = strings.TrimSpace(args.Export.Format)
			}
			exported, exportErr := manager.Export(ctx, scope, exportFormat)
			if exportErr != nil {
				return "", exportErr
			}
			status = exported.Status
			exportPath := ""
			if args.Export != nil {
				exportPath = strings.TrimSpace(args.Export.Path)
			}
			if exportPath == "" {
				ext := "json"
				if exported.Format == "markdown" {
					ext = "md"
				}
				exportPath = filepath.ToSlash(filepath.Join(
					".harness",
					"observational-memory",
					sanitizePathPart(scope.TenantID),
					sanitizePathPart(scope.ConversationID),
					sanitizePathPart(scope.AgentID),
					fmt.Sprintf("memory-%s.%s", time.Now().UTC().Format("20060102-150405"), ext),
				))
			}
			absPath, pathErr := ResolveWorkspacePathConfined(ctx, workspaceRoot, exportPath, sandboxScope)
			if pathErr != nil {
				return "", pathErr
			}
			if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
				return "", fmt.Errorf("create export directory: %w", err)
			}
			if err := os.WriteFile(absPath, []byte(exported.Content), 0o644); err != nil {
				return "", fmt.Errorf("write export file: %w", err)
			}
			result["export"] = map[string]any{
				"path":   NormalizeRelPath(workspaceRoot, absPath),
				"format": exported.Format,
				"bytes":  exported.Bytes,
			}
		case "review":
			exported, exportErr := manager.Export(ctx, scope, "markdown")
			if exportErr != nil {
				return "", exportErr
			}
			status = exported.Status
			reviewPrompt := "Review this observational memory snapshot. Focus on contradictions, stale assumptions, and missing durable constraints."
			if args.Review != nil && strings.TrimSpace(args.Review.Prompt) != "" {
				reviewPrompt = strings.TrimSpace(args.Review.Prompt)
			}
			if runner == nil {
				warnings = append(warnings, "agent runner is not configured, review was skipped")
				break
			}
			analysis, runErr := runner.RunPrompt(ctx, reviewPrompt+"\n\n"+exported.Content)
			if runErr != nil {
				return "", runErr
			}
			result["review"] = map[string]any{
				"analysis":  analysis,
				"model":     "delegated-agent",
				"timestamp": time.Now().UTC(),
			}
		default:
			return "", fmt.Errorf("unsupported action %q", action)
		}
		if err != nil {
			return "", err
		}
		if status.MemoryID == "" {
			status, err = manager.Status(ctx, scope)
			if err != nil {
				return "", err
			}
		}
		result["status"] = status
		if len(warnings) > 0 {
			result["warnings"] = warnings
		}
		return MarshalToolResult(result)
	}

	return Tool{Definition: def, Handler: handler}
}

func configFromArgs(v *struct {
	ObserveMinTokens       int `json:"observe_min_tokens"`
	SnippetMaxTokens       int `json:"snippet_max_tokens"`
	ReflectThresholdTokens int `json:"reflect_threshold_tokens"`
}) *om.Config {
	if v == nil {
		return nil
	}
	cfg := om.Config{
		ObserveMinTokens:       v.ObserveMinTokens,
		SnippetMaxTokens:       v.SnippetMaxTokens,
		ReflectThresholdTokens: v.ReflectThresholdTokens,
	}
	return &cfg
}

func memoryScopeFromMetadata(runID string, meta RunMetadata) om.ScopeKey {
	tenantID := strings.TrimSpace(meta.TenantID)
	if tenantID == "" {
		tenantID = "default"
	}
	agentID := strings.TrimSpace(meta.AgentID)
	if agentID == "" {
		agentID = "default"
	}
	conversationID := strings.TrimSpace(meta.ConversationID)
	if conversationID == "" {
		conversationID = strings.TrimSpace(runID)
	}
	return om.ScopeKey{
		TenantID:       tenantID,
		ConversationID: conversationID,
		AgentID:        agentID,
	}
}

func sanitizePathPart(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	v = strings.ReplaceAll(v, string(filepath.Separator), "-")
	v = strings.ReplaceAll(v, "..", "-")
	v = strings.ReplaceAll(v, " ", "-")
	return v
}
