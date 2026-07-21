package checkpoints

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	store   Store
	now     func() time.Time
	mu      sync.Mutex
	waiters map[string][]chan waitResult
}

type waitResult struct {
	result WaitResult
	err    error
}

func NewService(store Store, now func() time.Time) *Service {
	if store == nil {
		store = NewMemoryStore()
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		store:   store,
		now:     now,
		waiters: make(map[string][]chan waitResult),
	}
}

func (s *Service) Store() Store {
	return s.store
}

func (s *Service) Create(ctx context.Context, req CreateRequest) (Record, error) {
	now := s.now().UTC()
	record := Record{
		ID:             "checkpoint_" + uuid.NewString(),
		Kind:           req.Kind,
		Status:         StatusPending,
		RunID:          req.RunID,
		WorkflowRunID:  req.WorkflowRunID,
		CallID:         req.CallID,
		Tool:           req.Tool,
		Args:           req.Args,
		Questions:      req.Questions,
		SuspendPayload: req.SuspendPayload,
		ResumeSchema:   req.ResumeSchema,
		DeadlineAt:     req.DeadlineAt.UTC(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.Create(ctx, &record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Service) Get(ctx context.Context, id string) (Record, error) {
	record, err := s.store.Get(ctx, id)
	if err != nil {
		return Record{}, err
	}
	return *record, nil
}

func (s *Service) PendingByRun(ctx context.Context, runID string) (Record, bool, error) {
	record, err := s.store.PendingByRun(ctx, runID)
	if err != nil {
		return Record{}, false, err
	}
	if record == nil {
		return Record{}, false, nil
	}
	return *record, true, nil
}

func (s *Service) PendingByWorkflowRun(ctx context.Context, workflowRunID string) (Record, bool, error) {
	record, err := s.store.PendingByWorkflowRun(ctx, workflowRunID)
	if err != nil {
		return Record{}, false, err
	}
	if record == nil {
		return Record{}, false, nil
	}
	return *record, true, nil
}

func (s *Service) Wait(ctx context.Context, id string) (WaitResult, error) {
	record, err := s.store.Get(ctx, id)
	if err != nil {
		return WaitResult{}, err
	}
	if record.Status != StatusPending {
		return waitResultFromRecord(record)
	}

	ch := make(chan waitResult, 1)
	s.mu.Lock()
	s.waiters[id] = append(s.waiters[id], ch)
	s.mu.Unlock()

	record, err = s.store.Get(ctx, id)
	if err != nil {
		s.unregister(id, ch)
		return WaitResult{}, err
	}
	if record.Status != StatusPending {
		s.unregister(id, ch)
		return waitResultFromRecord(record)
	}

	select {
	case outcome := <-ch:
		return outcome.result, outcome.err
	case <-ctx.Done():
		s.unregister(id, ch)
		return WaitResult{}, ctx.Err()
	}
}

func (s *Service) Resume(ctx context.Context, id string, payload map[string]any) error {
	return s.resolve(ctx, id, StatusResumed, payload)
}

func (s *Service) Approve(ctx context.Context, id string) error {
	return s.resolve(ctx, id, StatusApproved, nil)
}

// ApproveWithPayload resolves the checkpoint as approved and records payload
// (e.g. the operator's selected plan approach option) as the resume payload
// returned to the waiter.
func (s *Service) ApproveWithPayload(ctx context.Context, id string, payload map[string]any) error {
	return s.resolve(ctx, id, StatusApproved, payload)
}

func (s *Service) Deny(ctx context.Context, id string) error {
	return s.resolve(ctx, id, StatusDenied, nil)
}

func (s *Service) Expire(ctx context.Context, id string) error {
	return s.resolve(ctx, id, StatusExpired, nil)
}

func (s *Service) resolve(ctx context.Context, id string, status Status, payload map[string]any) error {
	record, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	record.Status = status
	record.UpdatedAt = s.now().UTC()
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal checkpoint payload: %w", err)
		}
		record.ResumePayload = string(raw)
	}
	if err := s.store.Update(ctx, record); err != nil {
		return err
	}
	result, err := waitResultFromRecord(record)
	s.notify(id, waitResult{result: result, err: err})
	return err
}

func waitResultFromRecord(record *Record) (WaitResult, error) {
	result := WaitResult{Status: record.Status}
	if record.ResumePayload == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(record.ResumePayload), &result.Payload); err != nil {
		return WaitResult{}, fmt.Errorf("decode resume payload: %w", err)
	}
	return result, nil
}

func (s *Service) notify(id string, outcome waitResult) {
	s.mu.Lock()
	waiters := append([]chan waitResult(nil), s.waiters[id]...)
	delete(s.waiters, id)
	s.mu.Unlock()
	for _, ch := range waiters {
		ch <- outcome
	}
}

func (s *Service) unregister(id string, target chan waitResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.waiters[id]
	if len(waiters) == 0 {
		return
	}
	filtered := waiters[:0]
	for _, ch := range waiters {
		if ch != target {
			filtered = append(filtered, ch)
		}
	}
	if len(filtered) == 0 {
		delete(s.waiters, id)
		return
	}
	s.waiters[id] = filtered
}
