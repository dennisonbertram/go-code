package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// LspDiagnosticsTool returns a deferred tool for getting LSP diagnostics.
func LspDiagnosticsTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "lsp_diagnostics",
		Description:  descriptions.Load("lsp_diagnostics"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"lsp", "diagnostics", "code-analysis"},
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
			absPath, err := tools.ResolveWorkspacePathConfined(ctx, opts.WorkspaceRoot, args.FilePath, opts.SandboxScope)
			if err != nil {
				return "", err
			}
			target = absPath
		}
		output, exitCode, timedOut, err := tools.RunCommand(ctx, 30*time.Second, "gopls", "check", target)
		if err != nil {
			return "", err
		}
		return tools.MarshalToolResult(map[string]any{"output": output, "exit_code": exitCode, "timed_out": timedOut})
	}
	return tools.Tool{Definition: def, Handler: handler}
}

// LspReferencesTool returns a deferred tool for finding symbol references via LSP.
func LspReferencesTool(opts tools.BuildOptions) tools.Tool {
	def := tools.Definition{
		Name:         "lsp_references",
		Description:  descriptions.Load("lsp_references"),
		Action:       tools.ActionRead,
		ParallelSafe: true,
		Tier:         tools.TierDeferred,
		Tags:         []string{"lsp", "references", "symbol", "go", "code-analysis", "semantic-search"},
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
		workDir, err := filepath.Abs(opts.WorkspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve workspace root: %w", err)
		}
		if strings.TrimSpace(args.Path) != "" {
			resolved, err := tools.ResolveWorkspacePathConfined(ctx, opts.WorkspaceRoot, args.Path, opts.SandboxScope)
			if err != nil {
				return "", err
			}
			workDir = filepath.Dir(resolved)
		}
		cmd := exec.CommandContext(ctx, "gopls", "workspace_symbol", args.Symbol)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return tools.MarshalToolResult(map[string]any{"output": strings.TrimSpace(string(out)), "exit_code": 1})
		}
		return tools.MarshalToolResult(map[string]any{"output": strings.TrimSpace(string(out)), "exit_code": 0})
	}
	return tools.Tool{Definition: def, Handler: handler}
}

// LspRestartTool returns a deferred tool for restarting a language server.
func LspRestartTool() tools.Tool {
	def := tools.Definition{
		Name:         "lsp_restart",
		Description:  descriptions.Load("lsp_restart"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"lsp", "diagnostics", "code-analysis"},
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
		return tools.MarshalToolResult(map[string]any{"restarted": true, "name": args.Name})
	}
	return tools.Tool{Definition: def, Handler: handler}
}
