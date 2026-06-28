Create a reusable Go-authored workflow and make it available immediately.

The workflow is saved as a bundle containing `workflow.json` and `main.go`.
The Go source must be `package main` and should use:

```go
import sdk "go-agent-harness/pkg/workflowsdk"
```

Call `sdk.Main(func(ctx *sdk.Context) (any, error) { ... })` from `main`.
Use `ctx.Agent`, `ctx.Phase`, `ctx.Log`, `ctx.Feedback`, `ctx.Question`, and
`ctx.Workflow` to coordinate work through the host harness.

Scopes:
- `workspace`: save under the current workspace `.go-harness/workflows`
- `global`: save under the user-global workflows directory
- `skill`: save under a skill directory so the workflow transfers with the skill

The workflow is compiled before activation. If compilation fails, the workflow is
not registered and the compiler diagnostics are returned.
