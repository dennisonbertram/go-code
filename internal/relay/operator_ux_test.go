package relay_test

import (
	"context"
	"testing"
	"time"

	"go-agent-harness/internal/relay"
)

func TestOperatorRunSummaryRedactsNonLocalCapabilityPack(t *testing.T) {
	ctx := context.Background()
	workerStore := newTestWorkerStore()
	capStore := newTestCapabilityStore()
	events := newTestEventArtifactStore()
	now := time.Now().UTC()

	if err := workerStore.RegisterWorker(ctx, &relay.Worker{
		ID:            "w-container",
		TenantID:      "t1",
		Name:          "Container",
		LocationType:  relay.LocationContainer,
		Status:        relay.WorkerStatusOnline,
		TrustTier:     relay.TrustTierStandard,
		LastHeartbeat: now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	if err := capStore.SetPack(ctx, &relay.CapabilityPack{
		RunID: "run-redact",
		Repos: []relay.RepoCapability{
			{
				RepoURL:   "https://github.com/dennisonbertram/go-code.git",
				RepoPath:  "/Users/dennison/private/go-code",
				SecretRef: "secret/github-token",
			},
		},
	}); err != nil {
		t.Fatalf("SetPack: %v", err)
	}
	if err := events.SavePlacementRecord(ctx, &relay.PlacementRecord{
		RunID:          "run-redact",
		SelectedWorker: "w-container",
		RoutingReason:  "selected container",
		Timestamp:      now,
	}); err != nil {
		t.Fatalf("SavePlacementRecord: %v", err)
	}

	ux := relay.NewOperatorUX(workerStore, capStore, nil, events)
	summary, err := ux.GetRunSummary(ctx, "run-redact")
	if err != nil {
		t.Fatalf("GetRunSummary: %v", err)
	}
	if summary.CapabilityView == nil || len(summary.CapabilityView.Repos) != 1 {
		t.Fatalf("expected one repo capability, got %#v", summary.CapabilityView)
	}
	repo := summary.CapabilityView.Repos[0]
	if repo.RepoPath != "[redacted: non-local worker]" {
		t.Fatalf("repo path: got %q", repo.RepoPath)
	}
	if repo.SecretRef != "[redacted]" {
		t.Fatalf("secret ref: got %q", repo.SecretRef)
	}
}

func TestOperatorUXWorkerViewsAndPlacementExplanation(t *testing.T) {
	ctx := context.Background()
	workerStore := newTestWorkerStore()
	capStore := newTestCapabilityStore()
	events := newTestEventArtifactStore()
	now := time.Now().UTC().Add(-2 * time.Hour)

	if err := workerStore.RegisterWorker(ctx, &relay.Worker{
		ID: "w-local", TenantID: "t1", Name: "Local",
		LocationType: relay.LocationLocal, Status: relay.WorkerStatusOnline,
		TrustTier:               relay.TrustTierStandard,
		SupportedWorkspaceModes: []string{"local"},
		Labels:                  map[string]string{"role": "primary"},
		LastHeartbeat:           time.Now().UTC(), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker local: %v", err)
	}
	if err := workerStore.RegisterWorker(ctx, &relay.Worker{
		ID: "w-offline", TenantID: "t1", Name: "Offline",
		LocationType: relay.LocationVM, Status: relay.WorkerStatusOffline,
		TrustTier:               relay.TrustTierStandard,
		SupportedWorkspaceModes: []string{"vm"},
		LastHeartbeat:           now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("RegisterWorker offline: %v", err)
	}
	if err := capStore.SetInventory(ctx, &relay.CapabilityInventory{
		WorkerID: "w-local",
		Repos: []relay.RepoCapability{{
			RepoURL:  "https://github.com/dennisonbertram/go-code.git",
			RepoPath: "/Users/dennison/go-code",
		}},
	}); err != nil {
		t.Fatalf("SetInventory: %v", err)
	}
	if err := events.SavePlacementRecord(ctx, &relay.PlacementRecord{
		RunID: "run-explain", SelectedWorker: "w-local",
		RoutingReason: "selected local worker", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SavePlacementRecord: %v", err)
	}

	ux := relay.NewOperatorUX(workerStore, capStore, nil, events)
	summaries, err := ux.ListWorkerSummaries(ctx, "t1")
	if err != nil {
		t.Fatalf("ListWorkerSummaries: %v", err)
	}
	if len(summaries) != 2 || summaries[0].ID != "w-local" {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
	if summaries[0].Uptime == "" {
		t.Fatal("expected uptime to be formatted")
	}

	detail, err := ux.GetWorkerDetail(ctx, "w-local")
	if err != nil {
		t.Fatalf("GetWorkerDetail: %v", err)
	}
	if detail.Name != "Local" || detail.Labels["role"] != "primary" {
		t.Fatalf("unexpected detail: %#v", detail)
	}

	explanation, err := ux.GetPlacementExplanation(ctx, "run-explain")
	if err != nil {
		t.Fatalf("GetPlacementExplanation: %v", err)
	}
	if explanation != "selected local worker" {
		t.Fatalf("explanation: got %q", explanation)
	}

	caps, err := ux.GetWorkerCapabilities(ctx, "w-local")
	if err != nil {
		t.Fatalf("GetWorkerCapabilities: %v", err)
	}
	if len(caps.Repos) != 1 || caps.Repos[0].RepoPath == "" {
		t.Fatalf("unexpected capabilities: %#v", caps)
	}
}

type testEventArtifactStore struct {
	placements map[string]*relay.PlacementRecord
	artifacts  map[string][]*relay.Artifact
}

func newTestEventArtifactStore() *testEventArtifactStore {
	return &testEventArtifactStore{
		placements: make(map[string]*relay.PlacementRecord),
		artifacts:  make(map[string][]*relay.Artifact),
	}
}

func (s *testEventArtifactStore) SavePlacementRecord(_ context.Context, record *relay.PlacementRecord) error {
	cp := *record
	s.placements[record.RunID] = &cp
	return nil
}

func (s *testEventArtifactStore) GetPlacementRecord(_ context.Context, runID string) (*relay.PlacementRecord, error) {
	record, ok := s.placements[runID]
	if !ok {
		return nil, relay.ErrArtifactNotFound
	}
	cp := *record
	return &cp, nil
}

func (s *testEventArtifactStore) AppendEvent(_ context.Context, _ *relay.EventRecord) error {
	return nil
}

func (s *testEventArtifactStore) GetEvents(_ context.Context, _ string, _ int) ([]*relay.EventRecord, error) {
	return []*relay.EventRecord{}, nil
}

func (s *testEventArtifactStore) SaveArtifact(_ context.Context, artifact *relay.Artifact) error {
	cp := *artifact
	s.artifacts[artifact.RunID] = append(s.artifacts[artifact.RunID], &cp)
	return nil
}

func (s *testEventArtifactStore) GetArtifact(_ context.Context, id string) (*relay.Artifact, error) {
	for _, artifacts := range s.artifacts {
		for _, artifact := range artifacts {
			if artifact.ID == id {
				cp := *artifact
				return &cp, nil
			}
		}
	}
	return nil, relay.ErrArtifactNotFound
}

func (s *testEventArtifactStore) ListArtifacts(_ context.Context, runID string) ([]*relay.Artifact, error) {
	artifacts := s.artifacts[runID]
	out := make([]*relay.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		cp := *artifact
		out = append(out, &cp)
	}
	return out, nil
}

func (s *testEventArtifactStore) Close() error { return nil }
