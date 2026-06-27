package relay

import (
	"testing"
)

func TestValidateWorkerID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr error
	}{
		{"valid id", "worker-1", nil},
		{"valid uuid", "550e8400-e29b-41d4-a716-446655440000", nil},
		{"valid hostname", "laptop-dennison.local", nil},
		{"empty id", "", ErrInvalidWorkerID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorkerID(tt.id)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("ValidateWorkerID(%q) = %v, want %v", tt.id, err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateWorkerID(%q) = %v, want nil", tt.id, err)
				}
			}
		})
	}
}

func TestValidateLocationType(t *testing.T) {
	tests := []struct {
		name    string
		lt      LocationType
		wantErr error
	}{
		{"local", LocationLocal, nil},
		{"worktree", LocationWorktree, nil},
		{"container", LocationContainer, nil},
		{"vm", LocationVM, nil},
		{"sandbox", LocationSandbox, nil},
		{"invalid", LocationType("cloud"), ErrInvalidLocationType},
		{"empty", LocationType(""), ErrInvalidLocationType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLocationType(tt.lt)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("ValidateLocationType(%q) = %v, want %v", tt.lt, err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateLocationType(%q) = %v, want nil", tt.lt, err)
				}
			}
		})
	}
}

func TestValidateTrustTier(t *testing.T) {
	tests := []struct {
		name    string
		tt      TrustTier
		wantErr error
	}{
		{"untrusted", TrustTierUntrusted, nil},
		{"standard", TrustTierStandard, nil},
		{"privileged", TrustTierPrivileged, nil},
		{"invalid", TrustTier("admin"), ErrInvalidTrustTier},
		{"empty", TrustTier(""), ErrInvalidTrustTier},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTrustTier(tt.tt)
			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("ValidateTrustTier(%q) = %v, want %v", tt.tt, err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateTrustTier(%q) = %v, want nil", tt.tt, err)
				}
			}
		})
	}
}

func TestWorkerStatusValues(t *testing.T) {
	// Verify all defined statuses are distinct.
	statuses := []WorkerStatus{
		WorkerStatusOnline,
		WorkerStatusOffline,
		WorkerStatusStale,
		WorkerStatusDraining,
	}
	seen := make(map[WorkerStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
	}
}

func TestStaleDuration(t *testing.T) {
	if StaleDuration <= 0 {
		t.Errorf("StaleDuration must be positive, got %v", StaleDuration)
	}
}
