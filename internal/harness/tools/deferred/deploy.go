package deferred

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go-agent-harness/internal/deploy"
	tools "go-agent-harness/internal/harness/tools"
	"go-agent-harness/internal/harness/tools/descriptions"
)

// DeployPlatformRegistry maps platform name to Platform implementation.
type DeployPlatformRegistry map[string]deploy.Platform

// DefaultDeployPlatformRegistry returns a registry with the built-in adapters.
func DefaultDeployPlatformRegistry() DeployPlatformRegistry {
	return DeployPlatformRegistry{
		"railway": deploy.NewRailwayAdapter(nil),
		"flyio":   deploy.NewFlyAdapter(nil),
	}
}

// DeployTool returns a deferred tool for deploying to cloud platforms.
func DeployTool(registry DeployPlatformRegistry, workspaceRoot string) tools.Tool {
	def := tools.Definition{
		Name:         "deploy",
		Description:  descriptions.Load("deploy"),
		Action:       tools.ActionExecute,
		Mutating:     true,
		ParallelSafe: false,
		Tier:         tools.TierDeferred,
		Tags:         []string{"deploy", "cloud", "infrastructure", "railway", "fly"},
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"platform": map[string]any{
					"type":        "string",
					"description": "Platform adapter: 'railway' or 'flyio'. If omitted, auto-detected from workspace.",
				},
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"deploy", "status", "logs", "detect"},
					"description": "Action to perform.",
				},
				"workspace": map[string]any{
					"type":        "string",
					"description": "Absolute path to the project directory. Defaults to the agent workspace root.",
				},
				"environment": map[string]any{
					"type":        "string",
					"description": "Target environment: 'staging' or 'production'. Default: 'production'.",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "Preview the deploy command without executing. Default: false.",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Skip pre-deploy checks. Default: false.",
				},
			},
			"required": []string{"action"},
		},
	}

	handler := func(ctx context.Context, raw json.RawMessage) (string, error) {
		var args struct {
			Platform    string `json:"platform"`
			Action      string `json:"action"`
			Workspace   string `json:"workspace"`
			Environment string `json:"environment"`
			DryRun      bool   `json:"dry_run"`
			Force       bool   `json:"force"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("parse deploy args: %w", err)
		}
		if strings.TrimSpace(args.Action) == "" {
			return "", fmt.Errorf("action is required")
		}

		// Resolve workspace directory.
		wsDir := args.Workspace
		if wsDir == "" {
			wsDir = workspaceRoot
		}

		// Handle detect action — no platform needed.
		if args.Action == "detect" {
			name, err := deploy.DetectPlatform(wsDir)
			if err != nil {
				return "", fmt.Errorf("detect platform: %w", err)
			}
			all := deploy.DetectAll(wsDir)
			// Distinguish what we can DETECT from what we can actually DEPLOY:
			// detection recognises more project types (e.g. vercel, cloudflare)
			// than there are deploy adapters for. Report the deployable subset so
			// the caller does not assume a detected platform can be deployed here.
			deployable := make([]string, 0, len(all))
			for _, p := range all {
				if _, ok := registry[p]; ok {
					deployable = append(deployable, p)
				}
			}
			return tools.MarshalToolResult(map[string]any{
				"platform":   name,
				"all":        all,
				"deployable": deployable,
			})
		}

		// Resolve platform.
		platformName := strings.TrimSpace(args.Platform)
		if platformName == "" {
			// Auto-detect from workspace.
			detected, err := deploy.DetectPlatform(wsDir)
			if err != nil {
				return "", fmt.Errorf("auto-detect platform: %w; specify platform explicitly", err)
			}
			platformName = detected
		}

		platform, ok := registry[platformName]
		if !ok {
			return "", fmt.Errorf("unknown platform %q; supported: %s", platformName, supportedPlatforms(registry))
		}

		switch args.Action {
		case "deploy":
			env := args.Environment
			if env == "" {
				env = "production"
			}
			result, err := platform.Deploy(ctx, wsDir, deploy.DeployOpts{
				Environment: env,
				DryRun:      args.DryRun,
				Force:       args.Force,
			})
			if err != nil {
				return "", fmt.Errorf("deploy failed: %w", err)
			}
			return tools.MarshalToolResult(result)

		case "status":
			status, err := platform.Status(ctx, wsDir)
			if err != nil {
				return "", fmt.Errorf("status failed: %w", err)
			}
			return tools.MarshalToolResult(status)

		case "logs":
			reader, err := platform.Logs(ctx, wsDir, false)
			if err != nil {
				return "", fmt.Errorf("logs failed: %w", err)
			}
			var buf strings.Builder
			tmp := make([]byte, 4096)
			for {
				n, err := reader.Read(tmp)
				if n > 0 {
					buf.Write(tmp[:n])
				}
				if err != nil {
					break
				}
			}
			return tools.MarshalToolResult(map[string]any{
				"platform": platformName,
				"logs":     buf.String(),
			})

		default:
			return "", fmt.Errorf("unknown action %q; valid: deploy, status, logs, detect", args.Action)
		}
	}

	return tools.Tool{Definition: def, Handler: handler}
}

// supportedPlatforms returns a comma-separated list of registered platform names.
func supportedPlatforms(registry DeployPlatformRegistry) string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
