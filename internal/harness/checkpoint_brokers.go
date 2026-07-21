package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go-agent-harness/internal/checkpoints"
	htools "go-agent-harness/internal/harness/tools"
)

type checkpointApprovalBroker struct {
	service *checkpoints.Service
}

func NewCheckpointApprovalBroker(service *checkpoints.Service) ApprovalBroker {
	return &checkpointApprovalBroker{service: service}
}

func (b *checkpointApprovalBroker) Ask(ctx context.Context, req ApprovalRequest) (bool, string, error) {
	if req.Timeout <= 0 {
		req.Timeout = 5 * time.Minute
	}
	// Options presented to the operator (plan approach options) ride in the
	// record's Questions field, which is otherwise unused for KindApproval.
	var options string
	if len(req.Options) > 0 {
		raw, err := json.Marshal(req.Options)
		if err != nil {
			return false, "", fmt.Errorf("marshal approval options: %w", err)
		}
		options = string(raw)
	}
	record, err := b.service.Create(ctx, checkpoints.CreateRequest{
		Kind:       checkpoints.KindApproval,
		RunID:      req.RunID,
		CallID:     req.CallID,
		Tool:       req.Tool,
		Args:       req.Args,
		Questions:  options,
		DeadlineAt: time.Now().UTC().Add(req.Timeout),
	})
	if err != nil {
		return false, "", err
	}

	waitCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	result, err := b.service.Wait(waitCtx, record.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			_ = b.service.Expire(context.Background(), record.ID)
			return false, "", &ApprovalTimeoutError{
				RunID:      req.RunID,
				CallID:     req.CallID,
				DeadlineAt: record.DeadlineAt,
			}
		}
		return false, "", err
	}
	var option string
	if result.Status == checkpoints.StatusApproved {
		option, _ = result.Payload["option"].(string)
	}
	return result.Status == checkpoints.StatusApproved, option, nil
}

func (b *checkpointApprovalBroker) Pending(runID string) (PendingApproval, bool) {
	record, ok, err := b.service.PendingByRun(context.Background(), runID)
	if err != nil || !ok || record.Kind != checkpoints.KindApproval {
		return PendingApproval{}, false
	}
	var options []PlanApproachOption
	if record.Questions != "" {
		if err := json.Unmarshal([]byte(record.Questions), &options); err != nil {
			options = nil
		}
	}
	return PendingApproval{
		RunID:      record.RunID,
		CallID:     record.CallID,
		Tool:       record.Tool,
		Args:       record.Args,
		DeadlineAt: record.DeadlineAt,
		Options:    options,
	}, true
}

func (b *checkpointApprovalBroker) Approve(runID string) error {
	return b.ApproveWithOption(runID, "")
}

func (b *checkpointApprovalBroker) ApproveWithOption(runID, option string) error {
	record, ok, err := b.service.PendingByRun(context.Background(), runID)
	if err != nil {
		return err
	}
	if !ok || record.Kind != checkpoints.KindApproval {
		return ErrNoPendingApproval
	}
	if option == "" {
		return b.service.Approve(context.Background(), record.ID)
	}
	return b.service.ApproveWithPayload(context.Background(), record.ID, map[string]any{"option": option})
}

func (b *checkpointApprovalBroker) Deny(runID string) error {
	record, ok, err := b.service.PendingByRun(context.Background(), runID)
	if err != nil {
		return err
	}
	if !ok || record.Kind != checkpoints.KindApproval {
		return ErrNoPendingApproval
	}
	return b.service.Deny(context.Background(), record.ID)
}

type checkpointAskUserQuestionBroker struct {
	service *checkpoints.Service
	now     func() time.Time
}

func NewCheckpointAskUserQuestionBroker(service *checkpoints.Service, now func() time.Time) htools.AskUserQuestionBroker {
	if now == nil {
		now = time.Now
	}
	return &checkpointAskUserQuestionBroker{service: service, now: now}
}

func (b *checkpointAskUserQuestionBroker) Ask(ctx context.Context, req htools.AskUserQuestionRequest) (map[string]string, time.Time, error) {
	if err := htools.ValidateAskUserQuestions(req.Questions); err != nil {
		return nil, time.Time{}, err
	}
	if req.Timeout <= 0 {
		req.Timeout = 5 * time.Minute
	}
	rawQuestions, err := json.Marshal(req.Questions)
	if err != nil {
		return nil, time.Time{}, err
	}
	record, err := b.service.Create(ctx, checkpoints.CreateRequest{
		Kind:       checkpoints.KindUserInput,
		RunID:      req.RunID,
		CallID:     req.CallID,
		Questions:  string(rawQuestions),
		DeadlineAt: b.now().UTC().Add(req.Timeout),
	})
	if err != nil {
		return nil, time.Time{}, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	result, err := b.service.Wait(waitCtx, record.ID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			_ = b.service.Expire(context.Background(), record.ID)
			return nil, time.Time{}, &htools.AskUserQuestionTimeoutError{
				RunID:      req.RunID,
				CallID:     req.CallID,
				DeadlineAt: record.DeadlineAt,
			}
		}
		return nil, time.Time{}, err
	}
	answers := make(map[string]string, len(result.Payload))
	for key, value := range result.Payload {
		if str, ok := value.(string); ok {
			answers[key] = str
		}
	}
	return answers, b.now().UTC(), nil
}

func (b *checkpointAskUserQuestionBroker) Pending(runID string) (htools.AskUserQuestionPending, bool) {
	record, ok, err := b.service.PendingByRun(context.Background(), runID)
	if err != nil || !ok || record.Kind != checkpoints.KindUserInput {
		return htools.AskUserQuestionPending{}, false
	}
	questions, err := decodeQuestions(record.Questions)
	if err != nil {
		return htools.AskUserQuestionPending{}, false
	}
	return htools.AskUserQuestionPending{
		RunID:      record.RunID,
		CallID:     record.CallID,
		Tool:       htools.AskUserQuestionToolName,
		Questions:  questions,
		DeadlineAt: record.DeadlineAt,
	}, true
}

func (b *checkpointAskUserQuestionBroker) Submit(runID string, answers map[string]string) error {
	record, ok, err := b.service.PendingByRun(context.Background(), runID)
	if err != nil {
		return err
	}
	if !ok || record.Kind != checkpoints.KindUserInput {
		return ErrNoPendingUserQuestion
	}
	questions, err := decodeQuestions(record.Questions)
	if err != nil {
		return err
	}
	normalized, err := htools.NormalizeAskUserAnswers(questions, answers)
	if err != nil {
		return ErrInvalidUserQuestionInput
	}
	payload := make(map[string]any, len(normalized))
	for key, value := range normalized {
		payload[key] = value
	}
	return b.service.Resume(context.Background(), record.ID, payload)
}

func decodeQuestions(raw string) ([]htools.AskUserQuestion, error) {
	var questions []htools.AskUserQuestion
	if err := json.Unmarshal([]byte(raw), &questions); err != nil {
		return nil, err
	}
	return questions, nil
}
