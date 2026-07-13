package profiles

import (
	"context"

	"go-agent-harness/internal/store"
)

// EfficiencyReadAdapter adapts a store.ProfileRunStoreIface (the persistence
// interface implemented by store.SQLiteProfileRunStore) to the read-only
// interface expected by the get_efficiency_report tool
// (deferred.ProfileRunStoreIface).
//
// It bridges two concerns the tool and the store keep deliberately separate:
//   - the tool speaks in profiles.ProfileStats and needs an explicit
//     found bool ("does any history exist for this profile?");
//   - the store speaks in store.ProfileStats and signals "no history" with
//     RunCount == 0 rather than a bool.
//
// The adapter satisfies deferred.ProfileRunStoreIface structurally, so it can
// be assigned to DefaultRegistryOptions.ProfileRunStore without profiles
// importing the deferred package (which would create an import cycle, since
// deferred already imports profiles).
type EfficiencyReadAdapter struct {
	store store.ProfileRunStoreIface
}

// NewEfficiencyReadAdapter wraps a profile run store for read access by the
// efficiency report tool. Passing a nil store yields a nil adapter so callers
// can treat "no store configured" uniformly.
func NewEfficiencyReadAdapter(s store.ProfileRunStoreIface) *EfficiencyReadAdapter {
	if s == nil {
		return nil
	}
	return &EfficiencyReadAdapter{store: s}
}

// AggregateProfileStats returns aggregate statistics for the named profile as a
// profiles.ProfileStats, plus a found bool that is false when no run history
// exists for the profile (store RunCount == 0).
//
// TopTools and MaxSteps are left zero-valued: the store's aggregate query does
// not compute cross-run tool frequency or the profile's configured max_steps,
// and the efficiency report treats both as optional.
func (a *EfficiencyReadAdapter) AggregateProfileStats(ctx context.Context, profileName string) (ProfileStats, bool, error) {
	st, err := a.store.AggregateProfileStats(ctx, profileName)
	if err != nil {
		return ProfileStats{}, false, err
	}
	if st.RunCount == 0 {
		return ProfileStats{}, false, nil
	}
	return ProfileStats{
		ProfileName: st.ProfileName,
		RunCount:    st.RunCount,
		AvgSteps:    st.AvgSteps,
		AvgCostUSD:  st.AvgCostUSD,
		SuccessRate: st.SuccessRate,
	}, true, nil
}
