package relay

import (
	"fmt"
)

// CloudWorkerConfig is the configuration for a cloud/sandbox worker.
type CloudWorkerConfig struct {
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
	if cfg.Provider == "" {
		return fmt.Errorf("cloud worker: provider is required")
	}
	if cfg.MaxConcurrentRuns <= 0 {
		cfg.MaxConcurrentRuns = 5
	}
	return nil
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
