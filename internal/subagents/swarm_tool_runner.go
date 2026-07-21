package subagents

import (
	"context"

	tools "go-agent-harness/internal/harness/tools"
)

// toolSwarmRunner adapts *Swarm to the tools.SwarmRunner interface so the
// deferred agent_swarm tool can run swarms without the harness packages
// importing this one (import cycle: harness -> deferred -> subagents).
type toolSwarmRunner struct {
	swarm *Swarm
}

// NewToolSwarmRunner wraps a Swarm as a tools.SwarmRunner.
func NewToolSwarmRunner(s *Swarm) tools.SwarmRunner {
	return toolSwarmRunner{swarm: s}
}

func (a toolSwarmRunner) RunSwarm(ctx context.Context, req tools.SwarmRequest) (tools.SwarmReport, error) {
	report, err := a.swarm.Run(ctx, SwarmRequest{
		PromptTemplate:       req.PromptTemplate,
		Items:                append([]string(nil), req.Items...),
		ResumeAgentIDs:       append([]string(nil), req.ResumeAgentIDs...),
		Model:                req.Model,
		SystemPrompt:         req.SystemPrompt,
		MaxSteps:             req.MaxSteps,
		MaxCostUSD:           req.MaxCostUSD,
		ReasoningEffort:      req.ReasoningEffort,
		AllowedTools:         append([]string(nil), req.AllowedTools...),
		ProfileName:          req.ProfileName,
		IsolationMode:        req.IsolationMode,
		CleanupPolicy:        req.CleanupPolicy,
		BaseRef:              req.BaseRef,
		ResultMode:           req.ResultMode,
		ParentContextHandoff: req.ParentContextHandoff,
	})
	if err != nil {
		return tools.SwarmReport{}, err
	}

	out := tools.SwarmReport{
		Total:     report.Total,
		Completed: report.Completed,
		Failed:    report.Failed,
		Cancelled: report.Cancelled,
		Members:   make([]tools.SwarmMemberReport, len(report.Members)),
	}
	for i, m := range report.Members {
		out.Members[i] = tools.SwarmMemberReport{
			ID:      m.ID,
			Item:    m.Item,
			Prompt:  m.Prompt,
			Status:  m.Status,
			Output:  m.Output,
			Error:   m.Error,
			Resumed: m.Resumed,
		}
	}
	return out, nil
}
