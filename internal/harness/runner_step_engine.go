package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go-agent-harness/internal/forensics/audittrail"
	"go-agent-harness/internal/forensics/causalgraph"
	"go-agent-harness/internal/forensics/contextwindow"
	"go-agent-harness/internal/forensics/costanomaly"
	"go-agent-harness/internal/forensics/requestenvelope"
	"go-agent-harness/internal/forensics/tooldecision"
	htools "go-agent-harness/internal/harness/tools"
	om "go-agent-harness/internal/observationalmemory"
	"go-agent-harness/internal/systemprompt"
)

// messageMapToMessage converts a generic message map (as produced by
// transcriptMsgsToMaps and consumed by the message replacer) into a harness
// Message. It is the decode side of the compact_history map encoding.
func messageMapToMessage(m map[string]any) Message {
	msg := Message{}
	if v, ok := m["role"].(string); ok {
		msg.Role = v
	}
	if v, ok := m["content"].(string); ok {
		msg.Content = v
	}
	if v, ok := m["name"].(string); ok {
		msg.Name = v
		if v == "compact_summary" {
			msg.IsCompactSummary = true
		}
	}
	if v, ok := m["tool_call_id"].(string); ok {
		msg.ToolCallID = v
	}
	if tcs, ok := m["tool_calls"].([]map[string]any); ok {
		msg.ToolCalls = make([]ToolCall, 0, len(tcs))
		for _, tc := range tcs {
			id, _ := tc["id"].(string)
			name, _ := tc["name"].(string)
			args, _ := tc["arguments"].(string)
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: id, Name: name, Arguments: args})
		}
	}
	return msg
}

type stepEngine struct {
	runner                  *Runner
	ctx                     context.Context
	runID                   string
	req                     RunRequest
	preflight               *runPreflightResult
	effectiveMaxSteps       int
	effectiveMaxTurns       int
	runForkDepth            int
	effectiveApprovalPolicy ApprovalPolicy
	effectiveSandboxScope   htools.SandboxScope
}

func newStepEngine(r *Runner, ctx context.Context, runID string, req RunRequest, preflight *runPreflightResult, effectiveMaxSteps int, effectiveMaxTurns int, runForkDepth int, effectiveApprovalPolicy ApprovalPolicy, effectiveSandboxScope htools.SandboxScope) *stepEngine {
	return &stepEngine{
		runner:                  r,
		ctx:                     ctx,
		runID:                   runID,
		req:                     req,
		preflight:               preflight,
		effectiveMaxSteps:       effectiveMaxSteps,
		effectiveMaxTurns:       effectiveMaxTurns,
		runForkDepth:            runForkDepth,
		effectiveApprovalPolicy: effectiveApprovalPolicy,
		effectiveSandboxScope:   effectiveSandboxScope,
	}
}

func (se *stepEngine) run() {
	r := se.runner
	ctx := se.ctx
	runID := se.runID
	req := se.req
	preflight := se.preflight
	effectiveMaxSteps := se.effectiveMaxSteps
	effectiveMaxTurns := se.effectiveMaxTurns
	runForkDepth := se.runForkDepth
	effectiveApprovalPolicy := se.effectiveApprovalPolicy
	effectiveSandboxScope := se.effectiveSandboxScope

	// rc is this run's config snapshot, captured at run creation. It is
	// immutable for the run's lifetime, so per-step reads stay stable even
	// if ApplyConfig swaps the runner's live config mid-run.
	rc := r.configForRun(runID)

	// runTools is the tool registry for this run. When workspace_type
	// provisioning created a per-run workspace, this points to a registry
	// rooted at the provisioned path so that file/shell tools see the right
	// filesystem. Otherwise it falls back to the global Runner.tools.
	runTools := r.toolsForRun(runID)

	model := preflight.model
	primaryModel := preflight.primaryModel
	activeProvider := preflight.activeProvider
	providerCandidates := preflight.providerCandidates
	systemPrompt := preflight.systemPrompt
	resolvedPrompt := preflight.resolvedPrompt
	runStartedAt := preflight.runStartedAt
	messages := preflight.messages

	callSeq := 0
	var antiPatternCounts map[string]int
	if rc.DetectAntiPatterns {
		antiPatternCounts = make(map[string]int)
	}
	var alreadyAlerted map[string]bool
	if rc.DetectAntiPatterns {
		alreadyAlerted = make(map[string]bool)
	}
	var costAnomalyDetector *costanomaly.Detector
	if rc.CostAnomalyDetectionEnabled {
		multiplier := rc.CostAnomalyStepMultiplier
		if multiplier <= 0 {
			multiplier = 2.0
		}
		costAnomalyDetector = costanomaly.NewDetector(multiplier)
	}
	var causalBuilder *causalgraph.Builder
	if rc.CausalGraphEnabled {
		causalBuilder = causalgraph.NewBuilder()
	}
	consecutiveEmptyResponses := 0

	emitCausalGraph := func(lastStep int) {
		if causalBuilder == nil {
			return
		}
		graph := causalBuilder.Build()
		graphJSON, err := json.Marshal(graph)
		if err != nil {
			return
		}
		var graphMap any
		json.Unmarshal(graphJSON, &graphMap)
		r.emit(runID, EventCausalGraphSnapshot, map[string]any{
			"step":  lastStep,
			"graph": graphMap,
		})
	}

	var step int
	for step = 1; (effectiveMaxSteps == 0 || step <= effectiveMaxSteps) && (effectiveMaxTurns == 0 || step <= effectiveMaxTurns); step++ {
		if ctx.Err() != nil {
			r.cancelledRun(runID)
			return
		}

		r.mu.Lock()
		if s, ok := r.runs[runID]; ok {
			s.currentStep = step
		}
		r.mu.Unlock()

		{
			r.mu.RLock()
			st, ok := r.runs[runID]
			r.mu.RUnlock()
			if !ok {
				return
			}
			messages = r.messagesForStep(st)
		}

		stepStartTime := time.Now()
		r.emit(runID, EventRunStepStarted, map[string]any{
			"step":          step,
			"step_start_ms": stepStartTime.UnixMilli(),
		})
		r.drainSteering(runID, &messages)

		// Step budget pressure: fire when any turn budget (MaxSteps or MaxTurns) is set,
		// not just for subagents. Bifurcate the message: subagents get task_complete
		// guidance, root agents get a generic wrap-up message.
		turnBudget := effectiveMaxSteps
		if effectiveMaxTurns > 0 && (effectiveMaxSteps == 0 || effectiveMaxTurns < effectiveMaxSteps) {
			turnBudget = effectiveMaxTurns
		}
		if turnBudget > 0 {
			stepsRemaining := turnBudget - step + 1
			var pressureMsg string
			switch stepsRemaining {
			case 3:
				if runForkDepth > 0 {
					pressureMsg = fmt.Sprintf("SYSTEM: You have %d steps remaining in your step budget. You should be wrapping up your task. Call task_complete soon with what you have completed.", stepsRemaining)
				} else {
					pressureMsg = fmt.Sprintf("SYSTEM: You have %d steps remaining in your step budget. Please wrap up your work and provide a final response.", stepsRemaining)
				}
			case 1:
				if runForkDepth > 0 {
					pressureMsg = "SYSTEM: You have 1 step remaining. You MUST call task_complete now with what you have accomplished. Do not use any other tools."
				} else {
					pressureMsg = "SYSTEM: You have 1 step remaining. You MUST provide your final answer now. Do not use any other tools."
				}
			}
			if pressureMsg != "" {
				messages = append(messages, Message{Role: "user", Content: pressureMsg, IsMeta: true})
				r.stepSetMessages(runID, messages)
				r.emit(runID, EventStepBudgetPressure, map[string]any{
					"step":            step,
					"steps_remaining": stepsRemaining,
					"depth":           runForkDepth,
				})
			}
		}

		r.emit(runID, EventLLMTurnRequested, map[string]any{"step": step})

		var injectedRuleContent strings.Builder
		r.evaluateDynamicRules(runID, step, messages, &injectedRuleContent)

		var memorySnippetForSnapshot string
		var workingMemorySnippet string
		if rc.WorkingMemoryStore != nil {
			snippet, err := rc.WorkingMemoryStore.Snippet(context.Background(), r.scopeKey(runID))
			if err == nil && strings.TrimSpace(snippet) != "" {
				workingMemorySnippet = snippet
			}
		}
		if rc.MemoryManager != nil && rc.MemoryManager.Mode() != om.ModeOff {
			snippet, _, err := rc.MemoryManager.Snippet(context.Background(), r.scopeKey(runID))
			if err != nil {
				r.emit(runID, EventMemoryObserveFailed, map[string]any{"step": step, "error": err.Error()})
			} else if strings.TrimSpace(snippet) != "" {
				memorySnippetForSnapshot = snippet
			}
		}
		var runtimeContext string
		if resolvedPrompt != nil && rc.PromptEngine != nil {
			usageTotals, costTotals := r.accountingTotals(runID)

			estimatedCtxTokens := 0
			for _, m := range messages {
				runes := utf8.RuneCountInString(m.Content)
				if runes > 0 {
					estimatedCtxTokens += (runes + 3) / 4
				}
			}

			envInfo := r.envInfo
			envInfo.Model = model
			if r.providerRegistry != nil {
				providerName, found := r.providerRegistry.ResolveProvider(model)
				if found {
					cat := r.providerRegistry.Catalog()
					if cat != nil {
						if result, ok := cat.ModelInfo(providerName, model); ok && result.Model.Pricing != nil {
							envInfo.InputCostPerMToken = result.Model.Pricing.InputPer1MTokensUSD
							envInfo.OutputCostPerMToken = result.Model.Pricing.OutputPer1MTokensUSD
						}
					}
				}
			}
			runtimeContext = strings.TrimSpace(rc.PromptEngine.RuntimeContext(systemprompt.RuntimeContextInput{
				RunStartedAt:           runStartedAt,
				Now:                    time.Now().UTC(),
				Step:                   step,
				PromptTokensTotal:      usageTotals.PromptTokensTotal,
				CompletionTokensTotal:  usageTotals.CompletionTokensTotal,
				TotalTokens:            usageTotals.TotalTokens,
				LastTurnTokens:         usageTotals.LastTurnTokens,
				CostUSDTotal:           costTotals.CostUSDTotal,
				LastTurnCostUSD:        costTotals.LastTurnCostUSD,
				CostStatus:             string(costTotals.CostStatus),
				EstimatedContextTokens: estimatedCtxTokens,
				MessageCount:           len(messages),
				Environment:            envInfo,
			}))
		}
		planModeGuidance := r.planModePromptBlock(runID)
		turnMessages := r.buildTurnMessages(systemPrompt, messages, workingMemorySnippet, memorySnippetForSnapshot, injectedRuleContent.String(), planModeGuidance, runtimeContext)

		if rc.AutoCompactEnabled && rc.ModelContextWindow > 0 {
			estimated := 0
			for _, m := range turnMessages {
				runes := utf8.RuneCountInString(m.Content)
				if runes > 0 {
					estimated += (runes + 3) / 4
				}
			}
			ratio := float64(estimated) / float64(rc.ModelContextWindow)
			if ratio > rc.AutoCompactThreshold {
				r.emit(runID, EventAutoCompactStarted, map[string]any{
					"estimated_tokens": estimated,
					"context_window":   rc.ModelContextWindow,
					"threshold":        rc.AutoCompactThreshold,
					"ratio":            ratio,
					"mode":             rc.AutoCompactMode,
				})
				compactedMsgs, compactErr := r.autoCompactMessages(ctx, runID, messages)
				if compactErr == nil && compactedMsgs != nil {
					afterTokens := 0
					for _, m := range compactedMsgs {
						runes := utf8.RuneCountInString(m.Content)
						if runes > 0 {
							afterTokens += (runes + 3) / 4
						}
					}
					messages = compactedMsgs
					r.stepSetMessages(runID, messages)
					turnMessages = r.buildTurnMessages(systemPrompt, messages, workingMemorySnippet, memorySnippetForSnapshot, injectedRuleContent.String(), planModeGuidance, runtimeContext)
					r.emit(runID, EventAutoCompactCompleted, map[string]any{
						"before_tokens": estimated,
						"after_tokens":  afterTokens,
						"mode":          rc.AutoCompactMode,
					})
				} else if compactErr != nil {
					r.emit(runID, EventAutoCompactCompleted, map[string]any{
						"before_tokens": estimated,
						"after_tokens":  estimated,
						"mode":          rc.AutoCompactMode,
						"error":         compactErr.Error(),
					})
				}
			}
		}

		// deltaEmitted tracks whether any streaming delta has been emitted to the
		// client during this turn.  It is set atomically by the Stream closure and
		// read by the fallback loop.  When a delta has already been delivered we
		// must NOT fall back to another provider — the client has already received
		// partial output and switching would produce inconsistent results.
		var deltaEmitted atomic.Bool

		completionReq := CompletionRequest{
			Model:           primaryModel,
			Messages:        turnMessages,
			Tools:           r.filteredToolsForRun(runID),
			ReasoningEffort: req.ReasoningEffort,
			Stream: func(delta CompletionDelta) {
				deltaEmitted.Store(true)
				r.emitCompletionDelta(runID, step, delta)
			},
		}

		completionReq, blocked, err := r.applyPreHooks(ctx, runID, step, completionReq)
		if err != nil {
			r.failRun(runID, err)
			return
		}
		if blocked != nil {
			reason := blocked.reason
			if reason == "" {
				reason = "blocked"
			}
			r.failRun(runID, fmt.Errorf("blocked by pre-message hook %s: %s", blocked.hookName, reason))
			return
		}

		if rc.CaptureRequestEnvelope {
			var promptBuilder strings.Builder
			for _, m := range completionReq.Messages {
				promptBuilder.WriteString(m.Content)
				for _, tc := range m.ToolCalls {
					promptBuilder.WriteString(tc.Arguments)
				}
			}
			toolNames := make([]string, 0, len(completionReq.Tools))
			for _, td := range completionReq.Tools {
				toolNames = append(toolNames, td.Name)
			}
			snapshotPayload := map[string]any{
				"step":        step,
				"prompt_hash": requestenvelope.HashPrompt(promptBuilder.String()),
				"tool_names":  toolNames,
			}
			if rc.SnapshotMemorySnippet && memorySnippetForSnapshot != "" {
				snapshotPayload["memory_snippet"] = memorySnippetForSnapshot
			}
			r.emit(runID, EventLLMRequestSnapshot, snapshotPayload)
		}

		// --- Provider fallback loop ---
		// Iterate over the ordered candidate list.  On a fallback-eligible error
		// (429/5xx) from an earlier candidate, emit a prompt.warning and retry
		// the same CompletionRequest with the next candidate.  If a streaming
		// delta has already been emitted, fall back is disallowed (streaming-safety
		// rule) and we fail the run immediately.
		//
		// When AllowFallback is false (or there is only one candidate), the slice
		// contains exactly one entry so this degenerates to the original behaviour.
		//
		// The active candidate is tracked so post-loop code that updates
		// state.run.ProviderName uses the correct name.
		if len(providerCandidates) == 0 {
			// Defensive: preflight always populates at least one entry; fall back to
			// the pre-existing activeProvider if somehow the slice is empty.
			providerCandidates = []providerCandidate{{Provider: activeProvider, Name: preflight.providerName}}
		}

		llmCallStart := time.Now()
		var result CompletionResult
		var activeCandidateName string
		{
			candidateIdx := 0
			for {
				candidate := providerCandidates[candidateIdx]
				activeCandidateName = candidate.Name

				// Reset the delta flag before each attempt so mid-stream detection
				// is scoped to this particular provider call.
				deltaEmitted.Store(false)

				result, err = candidate.Provider.Complete(ctx, completionReq)
				if err == nil {
					// Success — update the running activeProvider so that rest of
					// the step loop (and future turns) still have the right reference
					// when they need it.
					activeProvider = candidate.Provider
					break
				}

				// Context cancelled: hard stop, do not attempt any fallback.
				if ctx.Err() != nil {
					r.cancelledRun(runID)
					return
				}

				// Streaming-safety: if any delta was delivered to the client,
				// switching providers would produce an inconsistent partial response.
				// Fail the run immediately.
				if deltaEmitted.Load() {
					r.failRun(runID, fmt.Errorf("provider completion failed: %w", err))
					return
				}

				// Check whether the error is fallback-eligible and there is a next
				// candidate available.
				nextIdx := candidateIdx + 1
				if isFallbackEligible(err) && req.AllowFallback && nextIdx < len(providerCandidates) {
					next := providerCandidates[nextIdx]
					r.emit(runID, EventPromptWarning, map[string]any{
						"code":          "provider_fallback",
						"step":          step,
						"from_provider": candidate.Name,
						"to_provider":   next.Name,
						"reason":        err.Error(),
						"message": fmt.Sprintf(
							"provider %q failed (step %d), falling back to %q: %s",
							candidate.Name, step, next.Name, err.Error(),
						),
					})
					// Optionally update run state so observability sees the new provider.
					r.mu.Lock()
					if st, ok := r.runs[runID]; ok {
						st.run.ProviderName = next.Name
					}
					r.mu.Unlock()
					r.emit(runID, EventProviderResolved, map[string]any{
						"model":    primaryModel,
						"provider": next.Name,
					})
					candidateIdx = nextIdx
					continue
				}

				// Non-eligible error or no more candidates: fail the run.
				r.failRun(runID, fmt.Errorf("provider completion failed: %w", err))
				return
			}
		}
		llmTotalDurationMs := result.TotalDurationMs
		if llmTotalDurationMs == 0 {
			llmTotalDurationMs = time.Since(llmCallStart).Milliseconds()
		}

		if rc.CaptureRequestEnvelope {
			r.emit(runID, EventLLMResponseMeta, map[string]any{
				"step":          step,
				"latency_ms":    llmTotalDurationMs,
				"model_version": result.ModelVersion,
			})
		}

		result, blocked, err = r.applyPostHooks(ctx, runID, step, completionReq, result)
		if err != nil {
			r.failRun(runID, err)
			return
		}
		if blocked != nil {
			reason := blocked.reason
			if reason == "" {
				reason = "blocked"
			}
			r.failRun(runID, fmt.Errorf("blocked by post-message hook %s: %s", blocked.hookName, reason))
			return
		}

		accountingPayload := r.recordAccounting(runID, result, step)
		r.emit(runID, EventUsageDelta, accountingPayload)
		r.emit(runID, EventLLMTurnCompleted, map[string]any{
			"step":              step,
			"tool_calls":        len(result.ToolCalls),
			"total_duration_ms": llmTotalDurationMs,
			"ttft_ms":           result.TTFTMs,
			"provider":          activeCandidateName,
		})

		if causalBuilder != nil {
			turnID := fmt.Sprintf("turn-%d", step)
			var contextIDs []string
			for _, m := range turnMessages {
				if m.ToolCallID != "" {
					contextIDs = append(contextIDs, m.ToolCallID)
				} else if m.CorrelationID != "" {
					contextIDs = append(contextIDs, m.CorrelationID)
				} else if m.MessageID != "" {
					contextIDs = append(contextIDs, m.MessageID)
				}
			}
			causalBuilder.RecordTurn(step, turnID, contextIDs)
		}

		if costAnomalyDetector != nil {
			var stepCost float64
			if v, ok := accountingPayload["turn_cost_usd"].(float64); ok {
				stepCost = v
			}
			if alert := costAnomalyDetector.Record(step, stepCost); alert != nil {
				r.emit(runID, EventCostAnomaly, map[string]any{
					"step":                 alert.Step,
					"anomaly_type":         string(alert.AnomalyType),
					"step_cost_usd":        alert.StepCostUSD,
					"avg_cost_usd":         alert.AvgCostUSD,
					"threshold_multiplier": alert.ThresholdMultiplier,
				})
			}
		}

		if rc.ContextWindowSnapshotEnabled {
			r.emitContextWindowSnapshot(runID, step, model, systemPrompt, turnMessages, result)
		}

		// Reasoning capture has two purposes:
		//   1. Functional — providers with the reasoning_content_passback quirk
		//      (DeepSeek V4, OpenRouter routing to a reasoning model) require
		//      the prior assistant turn's reasoning to be replayed on follow-up
		//      turns or they reject the request. This must happen regardless
		//      of forensics config.
		//   2. Observational — emitting an EventReasoningComplete for
		//      transcripts/forensics is gated on rc.CaptureReasoning.
		// Always carry reasoning on the assistant Message so (1) works; only
		// emit the event when the operator opts in via (2).
		capturedReasoning := result.ReasoningText
		if rc.CaptureReasoning && capturedReasoning != "" {
			if rc.RedactionPipeline != nil {
				redacted, keep := rc.RedactionPipeline.Apply(
					string(EventReasoningComplete),
					map[string]any{"text": capturedReasoning},
				)
				if keep {
					if t, ok := redacted["text"].(string); ok {
						capturedReasoning = t
					}
				} else {
					capturedReasoning = ""
				}
			}
			if capturedReasoning != "" {
				r.emit(runID, EventReasoningComplete, map[string]any{
					"text":   capturedReasoning,
					"tokens": result.ReasoningTokens,
					"step":   step,
				})
			}
		}

		if r.exceedsCostCeiling(runID) {
			_, costTotals := r.accountingTotals(runID)
			r.mu.RLock()
			maxCost := r.runs[runID].maxCostUSD
			r.mu.RUnlock()
			r.emit(runID, EventRunCostLimitReached, map[string]any{
				"step":                step,
				"max_cost_usd":        maxCost,
				"cumulative_cost_usd": costTotals.CostUSDTotal,
			})
			r.observeMemory(runID, step, messages)
			r.emit(runID, EventRunStepCompleted, map[string]any{
				"step":        step,
				"tool_calls":  0,
				"duration_ms": time.Since(stepStartTime).Milliseconds(),
			})
			emitCausalGraph(step)
			approved, selectedOption, approvalErr := r.awaitPlanApproval(ctx, runID, result.Content)
			if approvalErr != nil {
				r.failRun(runID, approvalErr)
				return
			}
			if !approved {
				messages = append(messages, Message{Role: "user", Content: r.planModeDenialFeedback(runID)})
				r.stepSetMessages(runID, messages)
				continue
			}
			if selectedOption.ID != "" {
				messages = append(messages, Message{Role: "user", Content: fmt.Sprintf("The operator approved the plan and selected approach %q (%s). Follow that approach.", selectedOption.Label, selectedOption.ID)})
				r.stepSetMessages(runID, messages)
			}
			r.completeRun(runID, result.Content)
			return
		}

		r.mu.RLock()
		stepState, stepOk := r.runs[runID]
		r.mu.RUnlock()
		if !stepOk {
			return
		}
		messages = r.messagesForStep(stepState)

		if len(result.ToolCalls) == 0 {
			if strings.TrimSpace(result.Content) == "" {
				consecutiveEmptyResponses++
				if consecutiveEmptyResponses < maxEmptyRetries {
					r.emit(runID, EventEmptyResponseRetry, map[string]any{
						"step":        step,
						"retry":       consecutiveEmptyResponses,
						"max_retries": maxEmptyRetries,
					})
					messages = append(messages,
						Message{Role: "assistant", Content: ""},
						Message{
							Role:    "user",
							Content: "Your previous response was empty — no text and no tool calls. Please use the available tools to make progress on the task. What do you need to do next?",
						},
					)
					r.stepSetMessages(runID, messages)
					r.emit(runID, EventRunStepCompleted, map[string]any{
						"step":        step,
						"tool_calls":  0,
						"duration_ms": time.Since(stepStartTime).Milliseconds(),
					})
					emitCausalGraph(step)
					// Empty-response retries are retry attempts for the current
					// assistant turn, not progress through the user's step budget.
					step--
					continue
				}
				r.emit(runID, EventRunStepCompleted, map[string]any{
					"step":        step,
					"tool_calls":  0,
					"duration_ms": time.Since(stepStartTime).Milliseconds(),
				})
				emitCausalGraph(step)
				r.failRun(runID, fmt.Errorf("max_empty_responses: max consecutive empty responses reached"))
				return
			} else {
				consecutiveEmptyResponses = 0
			}

			if result.Content != "" {
				messages = append(messages, Message{
					Role:      "assistant",
					Content:   result.Content,
					Reasoning: capturedReasoning,
				})
			}
			r.stepSetMessages(runID, messages)
			if result.Content != "" {
				r.snapshotRecordMessage(runID, "assistant", result.Content)
				r.emit(runID, EventAssistantMessage, map[string]any{"content": result.Content})
			}
			r.observeMemory(runID, step, messages)
			r.emit(runID, EventRunStepCompleted, map[string]any{
				"step":        step,
				"tool_calls":  0,
				"duration_ms": time.Since(stepStartTime).Milliseconds(),
			})
			emitCausalGraph(step)
			approved, selectedOption, approvalErr := r.awaitPlanApproval(ctx, runID, result.Content)
			if approvalErr != nil {
				r.failRun(runID, approvalErr)
				return
			}
			if !approved {
				messages = append(messages, Message{Role: "user", Content: r.planModeDenialFeedback(runID)})
				r.stepSetMessages(runID, messages)
				continue
			}
			if selectedOption.ID != "" {
				messages = append(messages, Message{Role: "user", Content: fmt.Sprintf("The operator approved the plan and selected approach %q (%s). Follow that approach.", selectedOption.Label, selectedOption.ID)})
				r.stepSetMessages(runID, messages)
			}
			r.completeRun(runID, result.Content)
			return
		}

		consecutiveEmptyResponses = 0

		messages = append(messages, Message{
			Role:      "assistant",
			Content:   result.Content,
			ToolCalls: append([]ToolCall(nil), result.ToolCalls...),
			Reasoning: capturedReasoning,
		})
		r.stepSetMessages(runID, messages)
		r.snapshotRecordMessage(runID, "assistant", result.Content)

		if rc.TraceToolDecisions && len(result.ToolCalls) > 0 {
			callSeq++
			availableTools := make([]string, 0, len(completionReq.Tools))
			for _, td := range completionReq.Tools {
				availableTools = append(availableTools, td.Name)
			}
			selectedTools := make([]string, 0, len(result.ToolCalls))
			for _, tc := range result.ToolCalls {
				selectedTools = append(selectedTools, tc.Name)
			}
			snap := tooldecision.ToolDecisionSnapshot{
				Step:           step,
				CallSequence:   callSeq,
				AvailableTools: availableTools,
				SelectedTools:  selectedTools,
			}
			r.emit(runID, EventToolDecision, map[string]any{
				"step":             snap.Step,
				"call_sequence":    snap.CallSequence,
				"call_sequence_id": snap.CallSequenceID(),
				"available_tools":  snap.AvailableTools,
				"selected_tools":   snap.SelectedTools,
			})
		}

		type pendingToolExec struct {
			origIdx        int
			call           ToolCall
			callArgs       json.RawMessage
			toolCtx        context.Context
			waitingForUser bool
			rewindPointID  string
		}

		type toolExecResult struct {
			output       string
			err          error
			metaMessages []htools.MetaMessage
			duration     time.Duration
		}

		pendingExecs := make([]pendingToolExec, 0, len(result.ToolCalls))

		for _, call := range result.ToolCalls {
			r.emit(runID, EventToolCallStarted, map[string]any{
				"call_id":   call.ID,
				"tool":      call.Name,
				"arguments": call.Arguments,
			})

			if causalBuilder != nil {
				causalBuilder.RecordToolCall(step, call.ID, call.Name, call.Arguments)
			}

			if rc.AuditTrailEnabled && audittrail.IsStateModifying(call.Name) {
				auditPayload := map[string]any{
					"tool":      call.Name,
					"call_id":   call.ID,
					"arguments": call.Arguments,
					"step":      step,
				}
				r.emit(runID, EventAuditAction, auditPayload)
				r.writeAudit(runID, audittrail.AuditRecord{
					RunID:     runID,
					EventType: string(EventAuditAction),
					Payload:   auditPayload,
				})
			}

			if rc.DetectAntiPatterns {
				apKey := call.Name + "\x00" + call.Arguments
				antiPatternCounts[apKey]++
				count := antiPatternCounts[apKey]
				if count >= 3 && !alreadyAlerted[apKey] {
					alreadyAlerted[apKey] = true
					alert := tooldecision.AntiPatternAlert{
						Type:      tooldecision.AntiPatternRetryLoop,
						ToolName:  call.Name,
						CallCount: count,
						Step:      step,
					}
					r.emit(runID, EventToolAntiPattern, map[string]any{
						"type":       string(alert.Type),
						"tool":       alert.ToolName,
						"call_count": alert.CallCount,
						"step":       alert.Step,
					})
				}
			}

			if !r.skillConstraints.IsToolAllowed(runID, call.Name) {
				constraint, _ := r.skillConstraints.Active(runID)
				constraintSkillName := ""
				var constraintAllowed []string
				if constraint != nil {
					constraintSkillName = constraint.SkillName
					constraintAllowed = constraint.AllowedTools
				}
				toolOutput := mustJSON(map[string]any{
					"error":         fmt.Sprintf("tool %q is not allowed while skill %q is active", call.Name, constraintSkillName),
					"allowed_tools": constraintAllowed,
				})
				r.emit(runID, EventToolCallBlocked, map[string]any{
					"call_id": call.ID,
					"tool":    call.Name,
					"skill":   constraintSkillName,
					"reason":  "not_in_allowed_tools",
				})
				messages = append(messages, Message{
					Role:       "tool",
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    toolOutput,
				})
				r.stepSetMessages(runID, messages)
				continue
			}

			waitingForUser := false
			if call.Name == htools.AskUserQuestionToolName {
				questions, err := htools.ParseAskUserQuestionArgs(json.RawMessage(call.Arguments))
				if err == nil {
					waitingForUser = true
					deadlineAt := time.Now().UTC().Add(rc.AskUserTimeout)
					r.setStatus(runID, RunStatusWaitingForUser, "", "")
					r.emit(runID, EventRunWaitingForUser, map[string]any{
						"call_id":     call.ID,
						"tool":        call.Name,
						"questions":   questions,
						"deadline_at": deadlineAt,
					})
				}
			}

			callArgs := json.RawMessage(call.Arguments)
			if denied, denialOutput := r.applyPreToolUseHooks(ctx, runID, call, &callArgs); denied {
				messages = append(messages, Message{
					Role:       "tool",
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    denialOutput,
				})
				r.stepSetMessages(runID, messages)
				continue
			}

			ruleEffect, ruleErr := r.permissionRuleDecision(runID, call.Name, callArgs)
			if ruleErr != nil {
				ruleEffect = PermissionEffectDeny
			}
			if ruleEffect == PermissionEffectDeny {
				deniedOutput := mustJSON(map[string]any{
					"error": map[string]any{
						"code":    "permission_denied",
						"message": "tool call denied by permission rule",
						"reason":  "fine-grained permission rule denied the call",
					},
				})
				r.emit(runID, EventToolCallBlocked, map[string]any{
					"call_id": call.ID,
					"tool":    call.Name,
					"reason":  "permission_rule_denied",
				})
				messages = append(messages, Message{
					Role:       "tool",
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    deniedOutput,
				})
				r.stepSetMessages(runID, messages)
				continue
			}

			needsApproval := ruleEffect == PermissionEffectAsk
			if !needsApproval && rc.ApprovalBroker != nil && effectiveApprovalPolicy != ApprovalPolicyNone && effectiveApprovalPolicy != "" {
				switch effectiveApprovalPolicy {
				case ApprovalPolicyAll:
					needsApproval = true
				case ApprovalPolicyDestructive:
					needsApproval = runTools.IsMutating(call.Name)
				}
			}
			if rc.ApprovalBroker != nil {
				if needsApproval {
					deadlineAt := time.Now().UTC().Add(rc.AskUserTimeout)
					r.setStatus(runID, RunStatusWaitingForApproval, "", "")
					r.emit(runID, EventToolApprovalRequired, map[string]any{
						"call_id":     call.ID,
						"tool":        call.Name,
						"arguments":   call.Arguments,
						"deadline_at": deadlineAt.Format(time.RFC3339),
					})
					approved, _, approvalErr := rc.ApprovalBroker.Ask(ctx, ApprovalRequest{
						RunID:   runID,
						CallID:  call.ID,
						Tool:    call.Name,
						Args:    call.Arguments,
						Timeout: rc.AskUserTimeout,
					})
					if approvalErr != nil {
						if ctx.Err() != nil {
							r.cancelledRun(runID)
							return
						}
						r.setStatus(runID, RunStatusRunning, "", "")
						r.emit(runID, EventToolApprovalDenied, map[string]any{
							"call_id": call.ID,
							"tool":    call.Name,
							"reason":  approvalErr.Error(),
						})
						deniedOutput := mustJSON(map[string]any{
							"error": map[string]any{
								"code":    "approval_timeout",
								"message": approvalErr.Error(),
							},
						})
						r.emit(runID, EventToolCallCompleted, map[string]any{
							"call_id":     call.ID,
							"tool":        call.Name,
							"output":      deniedOutput,
							"duration_ms": int64(0),
						})
						messages = append(messages, Message{
							Role:       "tool",
							Name:       call.Name,
							ToolCallID: call.ID,
							Content:    deniedOutput,
						})
						r.stepSetMessages(runID, messages)
						continue
					}
					r.setStatus(runID, RunStatusRunning, "", "")
					if !approved {
						r.emit(runID, EventToolApprovalDenied, map[string]any{
							"call_id": call.ID,
							"tool":    call.Name,
						})
						deniedOutput := mustJSON(map[string]any{
							"error": map[string]any{
								"code":    "permission_denied",
								"message": "tool call denied by operator",
							},
						})
						r.emit(runID, EventToolCallCompleted, map[string]any{
							"call_id":     call.ID,
							"tool":        call.Name,
							"output":      deniedOutput,
							"duration_ms": int64(0),
						})
						messages = append(messages, Message{
							Role:       "tool",
							Name:       call.Name,
							ToolCallID: call.ID,
							Content:    deniedOutput,
						})
						r.stepSetMessages(runID, messages)
						continue
					}
					r.emit(runID, EventToolApprovalGranted, map[string]any{
						"call_id": call.ID,
						"tool":    call.Name,
					})
				}
			} else if needsApproval {
				deniedOutput := mustJSON(map[string]any{
					"error": map[string]any{
						"code":    "permission_denied",
						"message": "tool call requires approval but no approval broker is configured",
						"reason":  "fine-grained permission rule requested approval",
					},
				})
				r.emit(runID, EventToolCallBlocked, map[string]any{
					"call_id": call.ID,
					"tool":    call.Name,
					"reason":  "permission_rule_approval_unavailable",
				})
				messages = append(messages, Message{
					Role:       "tool",
					Name:       call.Name,
					ToolCallID: call.ID,
					Content:    deniedOutput,
				})
				r.stepSetMessages(runID, messages)
				continue
			}

			meta := r.runMetadata(runID)
			toolCtx := context.WithValue(ctx, htools.ContextKeyRunID, runID)
			toolCtx = context.WithValue(toolCtx, htools.ContextKeyPlanModeGate, runPlanModeGate{runner: r, runID: runID})
			toolCtx = context.WithValue(toolCtx, htools.ContextKeyToolCallID, call.ID)
			toolCtx = context.WithValue(toolCtx, htools.ContextKeyRunMetadata, meta)
			toolCtx = htools.WithSandboxScope(toolCtx, effectiveSandboxScope)
			toolCtx = context.WithValue(toolCtx, htools.ContextKeyTranscriptReader, runTranscriptReader{runner: r, runID: runID})
			toolCtx = htools.WithForkDepth(toolCtx, runForkDepth)
			callID := call.ID
			callName := call.Name
			var streamIndex atomic.Int64
			toolStep := step
			outputStreamer := func(chunk string) {
				idx := int(streamIndex.Add(1) - 1)
				r.emit(runID, EventToolOutputDelta, map[string]any{
					"step":         toolStep,
					"call_id":      callID,
					"tool":         callName,
					"stream_index": idx,
					"content":      chunk,
				})
			}
			toolCtx = context.WithValue(toolCtx, htools.ContextKeyOutputStreamer, outputStreamer)
			preCompactMessages := messages
			messageReplacer := func(replacedMaps []map[string]any) {
				compactStart := time.Now()
				replaced := make([]Message, 0, len(replacedMaps))
				for _, m := range replacedMaps {
					replaced = append(replaced, messageMapToMessage(m))
				}
				messages = replaced
				r.stepSetMessages(runID, messages)
				compactPayload := map[string]any{
					"message_count": len(replaced),
					"duration_ms":   time.Since(compactStart).Milliseconds(),
				}
				if rc.ContextWindowSnapshotEnabled {
					var beforeTokens, afterTokens int
					for _, m := range preCompactMessages {
						beforeTokens += contextwindow.EstimateTokens(m.Content)
					}
					for _, m := range replaced {
						afterTokens += contextwindow.EstimateTokens(m.Content)
					}
					compactPayload["before_tokens"] = beforeTokens
					compactPayload["after_tokens"] = afterTokens
					compactPayload["tokens_estimated"] = true
				}
				r.emit(runID, EventCompactHistoryCompleted, compactPayload)
			}
			toolCtx = context.WithValue(toolCtx, htools.ContextKeyMessageReplacer, messageReplacer)

			pendingExecs = append(pendingExecs, pendingToolExec{
				origIdx:        len(pendingExecs),
				call:           call,
				callArgs:       callArgs,
				toolCtx:        toolCtx,
				waitingForUser: waitingForUser,
			})
		}

		execResults := make([]toolExecResult, len(pendingExecs))

		i := 0
		for i < len(pendingExecs) {
			pe := pendingExecs[i]
			isSafe := runTools.IsParallelSafe(pe.call.Name) && !pe.waitingForUser

			if !isSafe {
				if rewind, ok := rc.ConversationStore.(RewindStore); ok && runTools.IsMutating(pe.call.Name) {
					meta := r.runMetadata(runID)
					workspace := rc.WorkspaceBaseOptions.RepoPath
					if workspace != "" {
						point := RewindPoint{ID: fmt.Sprintf("%s-%d-%s", runID, step, pe.call.ID), ConversationID: meta.ConversationID, Step: step, Tool: pe.call.Name}
						if err := CaptureRewindPreImage(pe.toolCtx, rewind, point, workspace, pe.callArgs); err != nil {
							r.emit(runID, EventToolCallCompleted, map[string]any{"call_id": pe.call.ID, "tool": pe.call.Name, "rewind_warning": err.Error()})
						}
						if paths := ExtractRewindPaths(pe.call.Name, pe.callArgs); len(paths) > 0 {
							pe.rewindPointID = point.ID
						}
					}
				}
				start := time.Now()
				out, err := runTools.Execute(pe.toolCtx, pe.call.Name, pe.callArgs)
				if err == nil && pe.rewindPointID != "" {
					if rewind, ok := rc.ConversationStore.(RewindStore); ok {
						_ = FinalizeRewindPoint(pe.toolCtx, rewind, pe.rewindPointID, rc.WorkspaceBaseOptions.RepoPath)
					}
				}
				execResults[pe.origIdx] = toolExecResult{
					output:   out,
					err:      err,
					duration: time.Since(start),
				}
				if err == nil {
					if tr, ok := htools.UnwrapToolResult(execResults[pe.origIdx].output); ok {
						execResults[pe.origIdx].output = tr.Output
						execResults[pe.origIdx].metaMessages = tr.MetaMessages
					}
				}
				i++
				continue
			}

			j := i + 1
			for j < len(pendingExecs) {
				next := pendingExecs[j]
				if !runTools.IsParallelSafe(next.call.Name) || next.waitingForUser {
					break
				}
				j++
			}
			batch := pendingExecs[i:j]

			if len(batch) == 1 {
				bpe := batch[0]
				start := time.Now()
				out, err := runTools.Execute(bpe.toolCtx, bpe.call.Name, bpe.callArgs)
				execResults[bpe.origIdx] = toolExecResult{
					output:   out,
					err:      err,
					duration: time.Since(start),
				}
				if err == nil {
					if tr, ok := htools.UnwrapToolResult(execResults[bpe.origIdx].output); ok {
						execResults[bpe.origIdx].output = tr.Output
						execResults[bpe.origIdx].metaMessages = tr.MetaMessages
					}
				}
			} else {
				var wg sync.WaitGroup
				for _, bpe := range batch {
					bpe := bpe
					wg.Add(1)
					go func() {
						defer wg.Done()
						start := time.Now()
						out, err := runTools.Execute(bpe.toolCtx, bpe.call.Name, bpe.callArgs)
						res := toolExecResult{
							output:   out,
							err:      err,
							duration: time.Since(start),
						}
						if err == nil {
							if tr, ok := htools.UnwrapToolResult(res.output); ok {
								res.output = tr.Output
								res.metaMessages = tr.MetaMessages
							}
						}
						execResults[bpe.origIdx] = res
					}()
				}
				wg.Wait()
			}
			i = j
		}

		for _, pe := range pendingExecs {
			call := pe.call
			callArgs := pe.callArgs
			res := execResults[pe.origIdx]
			toolOutput := res.output
			toolErr := res.err
			toolDuration := res.duration
			metaMessages := res.metaMessages
			waitingForUser := pe.waitingForUser

			hookOutput := r.applyPostToolUseHooks(ctx, runID, call, callArgs, toolOutput, toolDuration, toolErr)

			if toolErr != nil {
				if ctx.Err() != nil {
					r.cancelledRun(runID)
					return
				}
				if hookOutput != "" {
					toolOutput = hookOutput
				} else {
					toolOutput = mustJSON(map[string]any{"error": toolErr.Error()})
				}
				r.emit(runID, EventToolCallCompleted, map[string]any{
					"call_id":     call.ID,
					"tool":        call.Name,
					"error":       toolErr.Error(),
					"output":      toolOutput,
					"duration_ms": toolDuration.Milliseconds(),
				})
				if waitingForUser && htools.IsAskUserQuestionTimeout(toolErr) {
					r.failRun(runID, toolErr)
					return
				}
				if waitingForUser {
					r.setStatus(runID, RunStatusRunning, "", "")
				}
			} else {
				toolOutput = hookOutput
				r.emit(runID, EventToolCallCompleted, map[string]any{
					"call_id":     call.ID,
					"tool":        call.Name,
					"output":      toolOutput,
					"duration_ms": toolDuration.Milliseconds(),
				})
				if waitingForUser {
					r.setStatus(runID, RunStatusRunning, "", "")
					r.emit(runID, EventRunResumed, map[string]any{
						"call_id":     call.ID,
						"tool":        call.Name,
						"answered_at": time.Now().UTC(),
					})
				}
			}

			if call.Name == "skill" && toolErr == nil {
				r.maybeActivateSkillConstraint(runID, toolOutput)
			}

			if toolErr == nil {
				if persist, isReset := htools.IsResetContextResult(call.Name, toolOutput); isReset {
					r.mu.Lock()
					var resetIdx int
					if state, ok := r.runs[runID]; ok {
						resetIdx = state.resetIndex
						state.resetIndex++
					}
					r.mu.Unlock()

					if rc.MemoryManager != nil && rc.MemoryManager.Mode() != om.ModeOff {
						persistContent := string(persist)
						if persistContent == "" || persistContent == "null" {
							persistContent = "{}"
						}
						memMsg := om.TranscriptMessage{
							Index:   int64(step),
							Role:    "system",
							Name:    "context_reset",
							Content: "[context_reset] persist: " + persistContent,
						}
						_, _ = rc.MemoryManager.Observe(context.Background(), om.ObserveRequest{
							Scope:      r.scopeKey(runID),
							RunID:      runID,
							ToolCallID: call.ID,
							Messages:   []om.TranscriptMessage{memMsg},
						})
					}

					if rc.ContextResetStore != nil {
						_ = rc.ContextResetStore.RecordContextReset(context.Background(), runID, resetIdx, step, persist)
					}

					r.emit(runID, EventContextReset, map[string]any{
						"reset_index": resetIdx,
						"at_step":     step,
						"persist":     persist,
					})

					var persistPretty string
					if formatted, err := json.MarshalIndent(persist, "", "  "); err == nil {
						persistPretty = string(formatted)
					} else {
						persistPretty = string(persist)
					}
					openingContent := fmt.Sprintf(
						"[Context Reset — Segment %d of this run]\n\nYou previously reset your context. Here is what you carried forward:\n\n%s\n\nContinue from here.",
						resetIdx+1,
						persistPretty,
					)
					resetMessages := []Message{
						{Role: "user", Content: openingContent},
					}
					messages = resetMessages
					r.stepSetMessages(runID, messages)
					break
				}
			}

			r.mu.RLock()
			latestState, ok := r.runs[runID]
			r.mu.RUnlock()
			if !ok {
				return
			}
			messages = r.messagesForStep(latestState)

			{
				errMsg := ""
				if toolErr != nil {
					errMsg = toolErr.Error()
				}
				r.snapshotRecordToolCall(runID, call.Name, call.ID, call.Arguments, errMsg)
			}

			if causalBuilder != nil {
				causalBuilder.RecordToolResult(step, call.ID, toolOutput)
			}

			messages = append(messages, Message{
				Role:       "tool",
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolOutput,
			})

			for _, metaMsg := range metaMessages {
				messages = append(messages, Message{
					Role:    "system",
					Content: metaMsg.Content,
					IsMeta:  true,
				})
			}

			r.stepSetMessages(runID, messages)

			for _, metaMsg := range metaMessages {
				r.emit(runID, EventMetaMessageInjected, map[string]any{
					"call_id": call.ID,
					"tool":    call.Name,
					"length":  len(metaMsg.Content),
				})
			}
		}
		r.observeMemory(runID, step, messages)
		r.emit(runID, EventRunStepCompleted, map[string]any{
			"step":        step,
			"tool_calls":  len(result.ToolCalls),
			"duration_ms": time.Since(stepStartTime).Milliseconds(),
		})
	}

	// Determine which budget was exhausted and emit the appropriate event.
	if effectiveMaxTurns > 0 {
		r.emit(runID, EventMaxTurnsExhausted, map[string]any{
			"run_id":     runID,
			"step":       step,
			"turn_count": step - 1,
			"max_turns":  effectiveMaxTurns,
		})
	}
	if effectiveMaxSteps > 0 {
		emitCausalGraph(effectiveMaxSteps)
	}
	if effectiveMaxTurns > 0 {
		r.failRunMaxTurns(runID, effectiveMaxTurns)
	} else {
		r.failRunMaxSteps(runID, effectiveMaxSteps)
	}
}
