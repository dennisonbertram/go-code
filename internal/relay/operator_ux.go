package relay

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// OperatorWorkerSummary is a safe, operator-facing view of a worker.
type OperatorWorkerSummary struct {
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	TenantID                string            `json:"tenant_id"`
	LocationType            LocationType      `json:"location_type"`
	Status                  WorkerStatus      `json:"status"`
	TrustTier               TrustTier         `json:"trust_tier"`
	Load                    int               `json:"load"`
	SupportedWorkspaceModes []string          `json:"supported_workspace_modes,omitempty"`
	Labels                  map[string]string `json:"labels,omitempty"`
	LastHeartbeat           time.Time         `json:"last_heartbeat"`
	Uptime                  string            `json:"uptime,omitempty"`
}

// OperatorRunSummary is an operator-facing view of a relayed run.
type OperatorRunSummary struct {
	RunID            string          `json:"run_id"`
	Status           string          `json:"status"`
	SelectedWorker   string          `json:"selected_worker,omitempty"`
	PlacementReason  string          `json:"placement_reason,omitempty"`
	CapabilityView   *CapabilityPack `json:"capability_view,omitempty"`
	ArtifactRefs     []ArtifactRef   `json:"artifact_refs,omitempty"`
	Mobility         MobilityClass   `json:"mobility"`
	HandoffStatus    string          `json:"handoff_status,omitempty"`
	PendingApprovals []string        `json:"pending_approvals,omitempty"`
	Source           string          `json:"source"`
	CreatedAt        time.Time       `json:"created_at"`
}

// ArtifactRef is a lightweight reference to an artifact for UX display.
type ArtifactRef struct {
	ID       string       `json:"id"`
	Type     ArtifactType `json:"type"`
	URL      string       `json:"url,omitempty"`
	Redacted bool         `json:"redacted"`
}

// OperatorUX provides the operator-facing visibility surface.
type OperatorUX struct {
	workerStore        WorkerStore
	capabilityStore    CapabilityStore
	transportMgr       *TransportManager
	eventArtifactStore EventAndArtifactStore
}

// NewOperatorUX creates a new operator UX service.
func NewOperatorUX(
	ws WorkerStore,
	cs CapabilityStore,
	tm *TransportManager,
	eas EventAndArtifactStore,
) *OperatorUX {
	return &OperatorUX{
		workerStore:        ws,
		capabilityStore:    cs,
		transportMgr:       tm,
		eventArtifactStore: eas,
	}
}

// ListWorkerSummaries returns operator-safe summaries of all workers.
func (ux *OperatorUX) ListWorkerSummaries(ctx context.Context, tenantID string) ([]OperatorWorkerSummary, error) {
	workers, err := ux.workerStore.ListWorkers(ctx, WorkerFilter{TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("operator ux: list workers: %w", err)
	}

	summaries := make([]OperatorWorkerSummary, 0, len(workers))
	for _, w := range workers {
		summary := OperatorWorkerSummary{
			ID:                      w.ID,
			Name:                    w.Name,
			TenantID:                w.TenantID,
			LocationType:            w.LocationType,
			Status:                  w.Status,
			TrustTier:               w.TrustTier,
			Load:                    w.Load,
			SupportedWorkspaceModes: w.SupportedWorkspaceModes,
			Labels:                  w.Labels,
			LastHeartbeat:           w.LastHeartbeat,
			Uptime:                  formatDuration(time.Since(w.CreatedAt)),
		}
		summaries = append(summaries, summary)
	}

	// Sort by status (online first), then by name.
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Status != summaries[j].Status {
			return summaries[i].Status == WorkerStatusOnline
		}
		return summaries[i].Name < summaries[j].Name
	})

	return summaries, nil
}

// GetWorkerDetail returns the full operator-safe worker detail.
func (ux *OperatorUX) GetWorkerDetail(ctx context.Context, workerID string) (*OperatorWorkerSummary, error) {
	w, err := ux.workerStore.GetWorker(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("operator ux: get worker: %w", err)
	}

	summary := &OperatorWorkerSummary{
		ID:                      w.ID,
		Name:                    w.Name,
		TenantID:                w.TenantID,
		LocationType:            w.LocationType,
		Status:                  w.Status,
		TrustTier:               w.TrustTier,
		Load:                    w.Load,
		SupportedWorkspaceModes: w.SupportedWorkspaceModes,
		Labels:                  w.Labels,
		LastHeartbeat:           w.LastHeartbeat,
		Uptime:                  formatDuration(time.Since(w.CreatedAt)),
	}
	return summary, nil
}

// GetRunSummary returns the operator-facing view of a run.
func (ux *OperatorUX) GetRunSummary(ctx context.Context, runID string) (*OperatorRunSummary, error) {
	summary := &OperatorRunSummary{
		RunID:  runID,
		Status: "unknown",
	}
	locationType := LocationLocal

	// Get placement record.
	if ux.eventArtifactStore != nil {
		record, err := ux.eventArtifactStore.GetPlacementRecord(ctx, runID)
		if err == nil {
			summary.SelectedWorker = record.SelectedWorker
			summary.PlacementReason = record.RoutingReason
			if ux.workerStore != nil && record.SelectedWorker != "" {
				if worker, err := ux.workerStore.GetWorker(ctx, record.SelectedWorker); err == nil {
					locationType = worker.LocationType
				}
			}
		}
	}

	// Get capability pack.
	if ux.capabilityStore != nil {
		pack, err := ux.capabilityStore.GetPack(ctx, runID)
		if err == nil {
			summary.CapabilityView = SanitizePackForDisplay(pack, locationType)
		}
	}

	// Get artifact refs.
	if ux.eventArtifactStore != nil {
		artifacts, err := ux.eventArtifactStore.ListArtifacts(ctx, runID)
		if err == nil {
			for _, a := range artifacts {
				summary.ArtifactRefs = append(summary.ArtifactRefs, ArtifactRef{
					ID:       a.ID,
					Type:     a.Type,
					URL:      a.URL,
					Redacted: a.Redacted,
				})
			}
		}
	}

	return summary, nil
}

// GetPlacementExplanation returns a human-readable placement explanation.
func (ux *OperatorUX) GetPlacementExplanation(ctx context.Context, runID string) (string, error) {
	if ux.eventArtifactStore == nil {
		return "placement records not available", nil
	}
	record, err := ux.eventArtifactStore.GetPlacementRecord(ctx, runID)
	if err != nil {
		return "no placement record found", nil
	}
	return record.RoutingReason, nil
}

// GetWorkerCapabilities returns the sanitized capability inventory for a worker.
func (ux *OperatorUX) GetWorkerCapabilities(ctx context.Context, workerID string) (*CapabilityInventory, error) {
	if ux.capabilityStore == nil {
		return nil, fmt.Errorf("operator ux: capability store not configured")
	}

	w, err := ux.workerStore.GetWorker(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("operator ux: get worker: %w", err)
	}

	inv, err := ux.capabilityStore.GetInventory(ctx, workerID)
	if err != nil {
		return nil, fmt.Errorf("operator ux: get inventory: %w", err)
	}

	return SanitizeInventoryForDisplay(inv, w.LocationType), nil
}

// formatDuration formats a duration into a human-readable string.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	if hours > 24 {
		days := hours / 24
		hours = hours % 24
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}
