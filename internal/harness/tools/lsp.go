package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go-agent-harness/internal/harness/tools/descriptions"
)

func lspDiagnosticsTool(workspaceRoot string, sandboxScope SandboxScope) Tool {
	def := Definition{
		Name:         "lsp_diagnostics",
		Description:  descriptions.Load("lsp_diagnostics"),
		Action:       ActionRead,
		ParallelSafe: true,
		Tags:         []string{"lsp", "diagnostics", "errors", "compiler", "code"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string"},
			},
		},
	}
	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		if _, err := exec.LookPath("gopls"); err != nil {
			return "", fmt.Errorf("gopls not available")
		}
		args := struct {
			FilePath string `json:"file_path"`
		}{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse lsp_diagnostics args: %w", err)
			}
		}
		target := "./..."
		if strings.TrimSpace(args.FilePath) != "" {
			absPath, err := ResolveWorkspacePathConfined(ctx, workspaceRoot, args.FilePath, sandboxScope)
			if err != nil {
				return "", err
			}
			target = absPath
		}
		output, exitCode, timedOut, err := runCommand(ctx, 30*time.Second, "gopls", "check", target)
		if err != nil {
			return "", err
		}
		return MarshalToolResult(map[string]any{"output": output, "exit_code": exitCode, "timed_out": timedOut})
	}
	return Tool{Definition: def, Handler: handler}
}

func lspReferencesTool(workspaceRoot string, sandboxScope SandboxScope) Tool {
	def := Definition{
		Name:         "lsp_references",
		Description:  descriptions.Load("lsp_references"),
		Action:       ActionRead,
		ParallelSafe: true,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string"},
				"path":   map[string]any{"type": "string"},
			},
			"required": []string{"symbol"},
		},
	}
	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		if _, err := exec.LookPath("gopls"); err != nil {
			return "", fmt.Errorf("gopls not available")
		}
		args := struct {
			Symbol string `json:"symbol"`
			Path   string `json:"path"`
		}{}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse lsp_references args: %w", err)
		}
		if strings.TrimSpace(args.Symbol) == "" {
			return "", fmt.Errorf("symbol is required")
		}
		workDir, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
		if strings.TrimSpace(args.Path) != "" {
			resolved, err := ResolveWorkspacePathConfined(ctx, workspaceRoot, args.Path, sandboxScope)
			if err != nil {
				return "", err
			}
			workDir = filepath.Dir(resolved)
		}
		cmd := exec.CommandContext(ctx, "gopls", "workspace_symbol", args.Symbol)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return MarshalToolResult(map[string]any{"output": strings.TrimSpace(string(out)), "exit_code": 1})
		}
		return MarshalToolResult(map[string]any{"output": strings.TrimSpace(string(out)), "exit_code": 0})
	}
	return Tool{Definition: def, Handler: handler}
}

func lspRestartTool(_ string) Tool {
	def := Definition{
		Name:         "lsp_restart",
		Description:  descriptions.Load("lsp_restart"),
		Action:       ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}
	handler := func(_ context.Context, raw json.RawMessage) (string, error) {
		args := struct {
			Name string `json:"name"`
		}{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("parse lsp_restart args: %w", err)
			}
		}
		if args.Name == "" {
			args.Name = "gopls"
		}
		return MarshalToolResult(map[string]any{"restarted": true, "name": args.Name})
	}
	return Tool{Definition: def, Handler: handler}
}
