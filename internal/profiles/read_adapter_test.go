package profiles

import (
	"context"
	"errors"
	"testing"

	"go-agent-harness/internal/store"
)

// fakeAggStore implements store.ProfileRunStoreIface for adapter tests.
type fakeAggStore struct {
	stats store.ProfileStats
	err   error
}

func (f *fakeAggStore) RecordProfileRun(context.Context, store.ProfileRunRecord) error {
	return nil
}
func (f *fakeAggStore) QueryRecentProfileRuns(context.Context, string, int) ([]store.ProfileRunRecord, error) {
	return nil, nil
}
func (f *fakeAggStore) AggregateProfileStats(context.Context, string) (store.ProfileStats, error) {
	return f.stats, f.err
}

func TestEfficiencyReadAdapter_FoundWhenHistoryExists(t *testing.T) {
	adapter := NewEfficiencyReadAdapter(&fakeAggStore{stats: store.ProfileStats{
		ProfileName: "researcher",
		RunCount:    4,
		AvgSteps:    12.5,
		AvgCostUSD:  0.42,
		SuccessRate: 0.75,
	}})
	got, found, err := adapter.AggregateProfileStats(context.Background(), "researcher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true when RunCount>0")
	}
	if got.RunCount != 4 || got.AvgSteps != 12.5 || got.AvgCostUSD != 0.42 || got.SuccessRate != 0.75 {
		t.Errorf("field mismatch: %+v", got)
	}
	if got.ProfileName != "researcher" {
		t.Errorf("profile name mismatch: %q", got.ProfileName)
	}
}

func TestEfficiencyReadAdapter_NotFoundWhenNoHistory(t *testing.T) {
	adapter := NewEfficiencyReadAdapter(&fakeAggStore{stats: store.ProfileStats{ProfileName: "x", RunCount: 0}})
	_, found, err := adapter.AggregateProfileStats(context.Background(), "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected found=false when RunCount==0")
	}
}

func TestEfficiencyReadAdapter_PropagatesError(t *testing.T) {
	adapter := NewEfficiencyReadAdapter(&fakeAggStore{err: errors.New("db down")})
	_, _, err := adapter.AggregateProfileStats(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestNewEfficiencyReadAdapter_NilStoreYieldsNil(t *testing.T) {
	if NewEfficiencyReadAdapter(nil) != nil {
		t.Fatal("expected nil adapter for nil store")
	}
}
