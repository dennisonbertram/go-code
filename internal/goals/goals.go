// Package goals provides persistent, multi-session workflow goals that survive
// restarts, compaction, and span multiple sessions. Equivalent to OpenAI Codex's
// Goals system and Factory AI's mission/task delegation model.
//
// A Goal represents a bounded, verifiable unit of work with:
//   - A clear definition of done (verification criteria)
//   - Progress tracking across sessions
//   - Dependency chains (blocks/blockedBy)
//   - Status lifecycle: pending → running → verifying → completed | failed
//   - Persistent storage (survives harness restarts)
package goals

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Status represents the lifecycle state of a goal.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusVerifying Status = "verifying"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Goal represents a persistent, verifiable unit of work.
type Goal struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Status         Status            `json:"status"`
	Progress       Progress          `json:"progress"`
	DependsOn      []string          `json:"depends_on,omitempty"` // IDs of goals that must complete first
	Blocks         []string          `json:"blocks,omitempty"`     // IDs of goals this one blocks
	VerifyCriteria string            `json:"verify_criteria"`      // How to verify completion (e.g., "all tests pass", "PR merged")
	Metadata       map[string]string `json:"metadata,omitempty"`
	Result         string            `json:"result,omitempty"` // Output/result on completion
	Error          string            `json:"error,omitempty"`  // Error message on failure
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
}

// Progress tracks completion progress for a goal.
type Progress struct {
	Total     int `json:"total"`     // Total sub-tasks/items
	Completed int `json:"completed"` // Completed count
	Percent   int `json:"percent"`   // 0-100 percentage
}

// GoalManager provides CRUD operations for persistent goals.
type GoalManager interface {
	Create(ctx context.Context, goal Goal) (*Goal, error)
	Get(ctx context.Context, id string) (*Goal, error)
	Update(ctx context.Context, goal Goal) (*Goal, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter GoalFilter) ([]Goal, error)
	// Ready returns goals whose dependencies are all satisfied.
	Ready(ctx context.Context) ([]Goal, error)
}

// GoalFilter filters goal queries.
type GoalFilter struct {
	Status   Status `json:"status,omitempty"`    // Filter by status
	Limit    int    `json:"limit,omitempty"`     // Max results (0 = unlimited)
	Offset   int    `json:"offset,omitempty"`    // Pagination offset
	SortBy   string `json:"sort_by,omitempty"`   // Field to sort by
	SortDesc bool   `json:"sort_desc,omitempty"` // Sort descending
}

// Store is the persistence interface for goals.
type Store interface {
	Create(ctx context.Context, goal *Goal) error
	Get(ctx context.Context, id string) (*Goal, error)
	Update(ctx context.Context, goal *Goal) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, filter GoalFilter) ([]Goal, error)
}

// Manager implements GoalManager with an in-memory store by default.
type Manager struct {
	store Store
	mu    sync.RWMutex
	now   func() time.Time
}

// NewManager creates a new GoalManager. If store is nil, uses an in-memory store.
func NewManager(store Store) *Manager {
	if store == nil {
		store = NewMemoryStore()
	}
	return &Manager{
		store: store,
		now:   time.Now,
	}
}

func (m *Manager) Create(ctx context.Context, goal Goal) (*Goal, error) {
	goal.CreatedAt = m.now().UTC()
	goal.UpdatedAt = m.now().UTC()
	if goal.Status == "" {
		goal.Status = StatusPending
	}
	if goal.Progress.Total > 0 {
		goal.Progress.Percent = (goal.Progress.Completed * 100) / goal.Progress.Total
	}
	if err := m.store.Create(ctx, &goal); err != nil {
		return nil, fmt.Errorf("create goal: %w", err)
	}
	return &goal, nil
}

func (m *Manager) Get(ctx context.Context, id string) (*Goal, error) {
	return m.store.Get(ctx, id)
}

func (m *Manager) Update(ctx context.Context, goal Goal) (*Goal, error) {
	goal.UpdatedAt = m.now().UTC()
	if goal.Progress.Total > 0 {
		goal.Progress.Percent = (goal.Progress.Completed * 100) / goal.Progress.Total
	}
	if goal.Status == StatusCompleted || goal.Status == StatusFailed || goal.Status == StatusCancelled {
		now := m.now().UTC()
		goal.CompletedAt = &now
	}
	if err := m.store.Update(ctx, &goal); err != nil {
		return nil, fmt.Errorf("update goal: %w", err)
	}
	return &goal, nil
}

func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.store.Delete(ctx, id)
}

func (m *Manager) List(ctx context.Context, filter GoalFilter) ([]Goal, error) {
	goals, err := m.store.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	// Apply sorting
	if filter.SortBy == "created_at" {
		sort.Slice(goals, func(i, j int) bool {
			if filter.SortDesc {
				return goals[i].CreatedAt.After(goals[j].CreatedAt)
			}
			return goals[i].CreatedAt.Before(goals[j].CreatedAt)
		})
	} else if filter.SortBy == "updated_at" {
		sort.Slice(goals, func(i, j int) bool {
			if filter.SortDesc {
				return goals[i].UpdatedAt.After(goals[j].UpdatedAt)
			}
			return goals[i].UpdatedAt.Before(goals[j].UpdatedAt)
		})
	}
	// Apply pagination
	if filter.Offset > 0 && filter.Offset < len(goals) {
		goals = goals[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(goals) {
		goals = goals[:filter.Limit]
	}
	return goals, nil
}

// Ready returns goals that are pending and have all dependencies satisfied.
func (m *Manager) Ready(ctx context.Context) ([]Goal, error) {
	all, err := m.store.List(ctx, GoalFilter{Status: StatusPending})
	if err != nil {
		return nil, err
	}
	var ready []Goal
	for _, g := range all {
		if m.dependenciesSatisfied(ctx, g) {
			ready = append(ready, g)
		}
	}
	return ready, nil
}

func (m *Manager) dependenciesSatisfied(ctx context.Context, goal Goal) bool {
	for _, depID := range goal.DependsOn {
		dep, err := m.store.Get(ctx, depID)
		if err != nil || dep == nil {
			return false
		}
		if dep.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// =============================================================================
// In-Memory Store
// =============================================================================

// MemoryStore is an in-memory implementation of Store for testing.
type MemoryStore struct {
	mu    sync.RWMutex
	goals map[string]*Goal
}

// NewMemoryStore creates a new in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{goals: make(map[string]*Goal)}
}

func (s *MemoryStore) Create(_ context.Context, goal *Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *goal
	s.goals[goal.ID] = &cp
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Goal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	g, ok := s.goals[id]
	if !ok {
		return nil, fmt.Errorf("goal %q not found", id)
	}
	cp := *g
	return &cp, nil
}

func (s *MemoryStore) Update(_ context.Context, goal *Goal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goals[goal.ID]; !ok {
		return fmt.Errorf("goal %q not found", goal.ID)
	}
	cp := *goal
	s.goals[goal.ID] = &cp
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goals[id]; !ok {
		return fmt.Errorf("goal %q not found", id)
	}
	delete(s.goals, id)
	return nil
}

func (s *MemoryStore) List(_ context.Context, filter GoalFilter) ([]Goal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Goal
	for _, g := range s.goals {
		if filter.Status != "" && g.Status != filter.Status {
			continue
		}
		result = append(result, *g)
	}
	return result, nil
}
