package main

import sdk "go-agent-harness/pkg/workflowsdk"

func main() {
	sdk.Main(func(ctx *sdk.Context) (any, error) {
		_ = ctx.Phase("Workflow UX")
		_ = ctx.Log("workflow ux path running")
		_ = ctx.Feedback("finding", "workflow feedback reached host", map[string]any{
			"path": "api-and-tmux",
		})
		return map[string]any{"ok": true, "args": ctx.Args}, nil
	})
}
