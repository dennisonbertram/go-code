package relay

import (
	"context"
	"fmt"
	"time"
)

// CloudWorkerConfig is the configuration for a cloud/sandbox worker.
type CloudWorkerConfig struct {
	// WorkerID is the stable worker identity for this cloud/sandbox worker.
	WorkerID string `json:"worker_id"`

	// TenantID scopes the worker when it is registered in the worker store.
	TenantID string `json:"tenant_id,omitempty"`

	// Name is the display name used when the worker is registered.
	Name string `json:"name,omitempty"`

	// LocationType is the non-local location type for this worker.
	LocationType LocationType `json:"location_type"`

	// TrustTier is the trust tier assigned to this worker.
	TrustTier TrustTier `json:"trust_tier,omitempty"`

	// Provider identifies the cloud provider (e.g. "hetzner", "aws", "gcp", "sandbox").
	Provider string `json:"provider"`

	// Region is the cloud region the worker runs in.
	Region string `json:"region,omitempty"`

	// InstanceType is the VM/container instance type.
	InstanceType string `json:"instance_type,omitempty"`

	// MaxConcurrentRuns limits how many runs this worker can handle.
	MaxConcurrentRuns int `json:"max_concurrent_runs"`

	// CostHint is an operator-visible cost estimate (e.g. "$0.05/hr").
	CostHint string `json:"cost_hint,omitempty"`

	// RiskHint describes the trust/security risk of this worker type.
	RiskHint string `json:"risk_hint,omitempty"`

	// AutoProvision indicates this worker is provisioned on demand by the relay.
	AutoProvision bool `json:"auto_provision"`

	// ProvisionTimeoutSeconds is the max time to wait for provisioning.
	ProvisionTimeoutSeconds int `json:"provision_timeout_seconds,omitempty"`

	// TeardownOnIdle tears down the worker after it becomes idle.
	TeardownOnIdle bool `json:"teardown_on_idle"`

	// IdleTimeoutSeconds is the max idle time before teardown.
	IdleTimeoutSeconds int `json:"idle_timeout_seconds,omitempty"`
}

// CloudWorkerPool manages a pool of cloud/sandbox workers.
type CloudWorkerPool struct {
	workerStore WorkerStore
	configs     map[string]CloudWorkerConfig // worker ID → config
}

// NewCloudWorkerPool creates a cloud worker pool manager.
func NewCloudWorkerPool(ws WorkerStore) *CloudWorkerPool {
	return &CloudWorkerPool{
		workerStore: ws,
		configs:     make(map[string]CloudWorkerConfig),
	}
}

// RegisterCloudWorker registers a worker with cloud-specific configuration.
// It validates that the worker has a non-local location type and stores the config.
func (p *CloudWorkerPool) RegisterCloudWorker(cfg CloudWorkerConfig) error {
	if cfg.WorkerID == "" {
		return fmt.Errorf("cloud worker: worker ID is required")
	}
	if cfg.Provider == "" {
		return fmt.Errorf("cloud worker: provider is required")
	}
	if !IsCloudWorker(cfg.LocationType) {
		return fmt.Errorf("cloud worker: location type %q is not a cloud worker type", cfg.LocationType)
	}
	if cfg.MaxConcurrentRuns <= 0 {
		cfg.MaxConcurrentRuns = 5
	}
	if cfg.Name == "" {
		cfg.Name = cfg.WorkerID
	}
	if cfg.TrustTier == "" {
		cfg.TrustTier = TrustTierStandard
	}
	if err := ValidateTrustTier(cfg.TrustTier); err != nil {
		return err
	}

	p.configs[cfg.WorkerID] = cfg
	if p.workerStore != nil {
		now := time.Now().UTC()
		if err := p.workerStore.RegisterWorker(context.Background(), &Worker{
			ID: cfg.WorkerID, TenantID: cfg.TenantID, Name: cfg.Name,
			LocationType: cfg.LocationType, Status: WorkerStatusOnline,
			TrustTier: cfg.TrustTier, Load: 0,
			SupportedWorkspaceModes: []string{WorkspaceModeForLocation(cfg.LocationType)},
			LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			delete(p.configs, cfg.WorkerID)
			return fmt.Errorf("cloud worker: register worker: %w", err)
		}
	}
	return nil
}

// Config returns the stored cloud configuration for a worker.
func (p *CloudWorkerPool) Config(workerID string) (CloudWorkerConfig, bool) {
	cfg, ok := p.configs[workerID]
	return cfg, ok
}

// IsCloudWorker returns true if the location type is a cloud/sandbox type.
func IsCloudWorker(lt LocationType) bool {
	switch lt {
	case LocationVM, LocationContainer, LocationSandbox:
		return true
	default:
		return false
	}
}

// IsLocalWorker returns true if the location type is a local type.
func IsLocalWorkerType(lt LocationType) bool {
	return lt == LocationLocal || lt == LocationWorktree
}

// ValidateCloudPlacement checks if a run can be placed on a cloud worker.
// Returns nil if placement is valid, or an error explaining why not.
func ValidateCloudPlacement(localOnly bool, requiredLocationTypes []LocationType, workerLocation LocationType) error {
	if localOnly {
		return fmt.Errorf("cloud placement: run is local-only, cannot place on %s worker", workerLocation)
	}

	if len(requiredLocationTypes) > 0 {
		allowed := false
		for _, lt := range requiredLocationTypes {
			if lt == workerLocation {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("cloud placement: %s not in allowed location types", workerLocation)
		}
	}

	return nil
}

// WorkspaceModeForLocation maps a location type to a workspace mode string.
func WorkspaceModeForLocation(lt LocationType) string {
	switch lt {
	case LocationLocal:
		return "local"
	case LocationWorktree:
		return "worktree"
	case LocationContainer:
		return "container"
	case LocationVM:
		return "vm"
	case LocationSandbox:
		return "sandbox"
	default:
		return "local"
	}
}

// CostRiskSummary returns a human-readable summary of cost and risk for a location type.
func CostRiskSummary(lt LocationType) (cost, risk string) {
	switch lt {
	case LocationLocal:
		return "free (your machine)", "low (your environment)"
	case LocationWorktree:
		return "free (local git)", "low (isolated worktree)"
	case LocationContainer:
		return "low (~$0.02/hr)", "medium (container isolation)"
	case LocationVM:
		return "medium (~$0.05/hr)", "medium (VM isolation)"
	case LocationSandbox:
		return "low (~$0.01/hr)", "high (restricted sandbox)"
	default:
		return "unknown", "unknown"
	}
}
