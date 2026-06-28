package relay

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PlacementRequest is the input to the placement router.
type PlacementRequest struct {
	// RunID identifies the run being placed.
	RunID string

	// TenantID scopes placement to a specific tenant.
	TenantID string

	// RequiredCapabilities lists tools, MCP servers, etc. that must be available.
	RequiredCapabilities CapabilityPack

	// RequiredWorkspaceModes lists workspace types that are acceptable.
	// Empty means any mode is acceptable.
	RequiredWorkspaceModes []string

	// RequiredRepoURL is a repo that must be accessible from the worker.
	RequiredRepoURL string

	// PreferLocal scores local workers higher.
	PreferLocal bool

	// PreferCleanWorkspace scores clean (non-local) workspaces higher.
	PreferCleanWorkspace bool

	// PreferCloudForLongRunning scores cloud workers higher for long-running tasks.
	PreferCloudForLongRunning bool

	// MinimumTrustTier is the lowest trust tier acceptable.
	MinimumTrustTier TrustTier

	// AllowedLocationTypes restricts placement to specific location types.
	// Empty means all types are allowed.
	AllowedLocationTypes []LocationType

	// LocalOnly restricts placement to local workers only.
	LocalOnly bool

	// RequireBrowser requires browser capability.
	RequireBrowser bool

	// RequireDocker requires Docker capability.
	RequireDocker bool
}

// RejectionReason records why a worker was not selected.
type RejectionReason struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
	Category string `json:"category"` // "offline", "tenant", "capability", "trust", "location", "workspace", "repo"
}

// PlacementRecord documents the placement decision for a run.
type PlacementRecord struct {
	RunID           string            `json:"run_id"`
	SelectedWorker  string            `json:"selected_worker,omitempty"`
	EligibleWorkers []string          `json:"eligible_workers"`
	RejectedWorkers []RejectionReason `json:"rejected_workers,omitempty"`
	RoutingReason   string            `json:"routing_reason"`
	SoftScoreDetail map[string]int    `json:"soft_score_detail,omitempty"`
	Timestamp       time.Time         `json:"timestamp"`
}

// PlacementRouter selects the best eligible worker for a run contract.
type PlacementRouter struct {
	workerStore     WorkerStore
	capabilityStore CapabilityStore
}

// NewPlacementRouter creates a new placement router.
func NewPlacementRouter(ws WorkerStore, cs CapabilityStore) *PlacementRouter {
	return &PlacementRouter{
		workerStore:     ws,
		capabilityStore: cs,
	}
}

// Place evaluates all available workers and selects the best one for the request.
// Returns a placement record explaining the decision. If no worker is eligible,
// SelectedWorker is empty and all workers appear in RejectedWorkers.
func (pr *PlacementRouter) Place(ctx context.Context, req PlacementRequest) (*PlacementRecord, error) {
	record := &PlacementRecord{
		RunID:     req.RunID,
		Timestamp: time.Now(),
	}

	// Fetch all online workers for this tenant.
	workers, err := pr.workerStore.ListWorkers(ctx, WorkerFilter{
		TenantID: req.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("relay: list workers for placement: %w", err)
	}

	if len(workers) == 0 {
		record.RoutingReason = "no workers registered for tenant"
		return record, nil
	}

	// Phase 1: Apply hard constraints. Workers that fail any constraint are rejected.
	var eligible []*Worker
	for _, w := range workers {
		rejections, err := pr.checkHardConstraints(ctx, w, req)
		if err != nil {
			return nil, err
		}
		if len(rejections) > 0 {
			record.RejectedWorkers = append(record.RejectedWorkers, rejections...)
			continue
		}
		eligible = append(eligible, w)
	}

	if len(eligible) == 0 {
		record.RoutingReason = "no workers passed hard constraints"
		return record, nil
	}

	for _, w := range eligible {
		record.EligibleWorkers = append(record.EligibleWorkers, w.ID)
	}

	// Phase 2: Score eligible workers with soft preferences.
	scores := pr.scoreWorkers(eligible, req)
	record.SoftScoreDetail = scores

	// Phase 3: Select the highest-scoring worker.
	best := pr.selectBest(eligible, scores)
	record.SelectedWorker = best.ID
	record.RoutingReason = pr.explainDecision(best, scores[best.ID], record)

	return record, nil
}

// checkHardConstraints returns rejection reasons if the worker fails any constraint.
func (pr *PlacementRouter) checkHardConstraints(ctx context.Context, w *Worker, req PlacementRequest) ([]RejectionReason, error) {
	var rejections []RejectionReason

	// Reject offline or stale workers.
	if w.Status != WorkerStatusOnline && w.Status != WorkerStatusDraining {
		rejections = append(rejections, RejectionReason{
			WorkerID: w.ID,
			Reason:   fmt.Sprintf("worker status is %s (must be online or draining)", w.Status),
			Category: "offline",
		})
	}

	// Reject workers below the minimum trust tier.
	if !trustTierMeets(w.TrustTier, req.MinimumTrustTier) {
		rejections = append(rejections, RejectionReason{
			WorkerID: w.ID,
			Reason:   fmt.Sprintf("trust tier %s does not meet minimum %s", w.TrustTier, req.MinimumTrustTier),
			Category: "trust",
		})
	}

	// Reject workers outside allowed location types.
	if len(req.AllowedLocationTypes) > 0 {
		allowed := false
		for _, lt := range req.AllowedLocationTypes {
			if w.LocationType == lt {
				allowed = true
				break
			}
		}
		if !allowed {
			rejections = append(rejections, RejectionReason{
				WorkerID: w.ID,
				Reason:   fmt.Sprintf("location type %s not in allowed list", w.LocationType),
				Category: "location",
			})
		}
	}

	// LocalOnly restricts to local workers.
	if req.LocalOnly && w.LocationType != LocationLocal {
		rejections = append(rejections, RejectionReason{
			WorkerID: w.ID,
			Reason:   "local-only placement requires local worker",
			Category: "location",
		})
	}

	// Check required workspace modes.
	if len(req.RequiredWorkspaceModes) > 0 {
		if !hasAnyWorkspaceMode(w.SupportedWorkspaceModes, req.RequiredWorkspaceModes) {
			rejections = append(rejections, RejectionReason{
				WorkerID: w.ID,
				Reason:   fmt.Sprintf("worker supports %v, need one of %v", w.SupportedWorkspaceModes, req.RequiredWorkspaceModes),
				Category: "workspace",
			})
		}
	}

	capabilityRejections, err := pr.checkCapabilityConstraints(ctx, w, req)
	if err != nil {
		return nil, err
	}
	rejections = append(rejections, capabilityRejections...)

	return rejections, nil
}

func (pr *PlacementRouter) checkCapabilityConstraints(ctx context.Context, w *Worker, req PlacementRequest) ([]RejectionReason, error) {
	if !hasCapabilityRequirements(req) {
		return nil, nil
	}
	if pr.capabilityStore == nil {
		return []RejectionReason{{
			WorkerID: w.ID,
			Reason:   "capability requirements cannot be evaluated without a capability store",
			Category: "capability",
		}}, nil
	}

	inv, err := pr.capabilityStore.GetInventory(ctx, w.ID)
	if err != nil {
		if err == ErrCapabilityNotFound {
			return []RejectionReason{{
				WorkerID: w.ID,
				Reason:   "worker has no capability inventory",
				Category: "capability",
			}}, nil
		}
		return nil, fmt.Errorf("relay: get capability inventory for %s: %w", w.ID, err)
	}

	missing := missingCapabilities(inv, req)
	if len(missing) == 0 {
		return nil, nil
	}
	sort.Strings(missing)
	return []RejectionReason{{
		WorkerID: w.ID,
		Reason:   "missing required capabilities: " + strings.Join(missing, ", "),
		Category: "capability",
	}}, nil
}

func hasCapabilityRequirements(req PlacementRequest) bool {
	cp := req.RequiredCapabilities
	return req.RequiredRepoURL != "" ||
		req.RequireBrowser ||
		req.RequireDocker ||
		len(cp.Tools) > 0 ||
		len(cp.MCPServers) > 0 ||
		len(cp.Memories) > 0 ||
		len(cp.Repos) > 0 ||
		len(cp.Secrets) > 0 ||
		len(cp.OutputSurfaces) > 0 ||
		cp.Browser != nil ||
		cp.Docker != nil
}

func missingCapabilities(inv *CapabilityInventory, req PlacementRequest) []string {
	var missing []string
	cp := req.RequiredCapabilities

	for _, tool := range cp.Tools {
		if !inventoryHasTool(inv, tool.Name) {
			missing = append(missing, "tool:"+tool.Name)
		}
	}
	for _, mcp := range cp.MCPServers {
		if !inventoryHasMCPServer(inv, mcp.Name) {
			missing = append(missing, "mcp_server:"+mcp.Name)
		}
	}
	for _, memory := range cp.Memories {
		if !inventoryHasMemory(inv, memory.Name) {
			missing = append(missing, "memory:"+memory.Name)
		}
	}
	for _, repo := range cp.Repos {
		if !inventoryHasRepo(inv, repo.RepoURL) {
			missing = append(missing, "repo:"+repo.RepoURL)
		}
	}
	if req.RequiredRepoURL != "" && !inventoryHasRepo(inv, req.RequiredRepoURL) {
		missing = append(missing, "repo:"+req.RequiredRepoURL)
	}
	for _, secret := range cp.Secrets {
		if !inventoryHasSecret(inv, secret) {
			if secret.Ref != "" {
				missing = append(missing, "secret:"+secret.Ref)
			} else {
				missing = append(missing, "secret:"+secret.Name)
			}
		}
	}
	for _, surface := range cp.OutputSurfaces {
		if !inventoryHasOutputSurface(inv, surface.Type) {
			missing = append(missing, "output_surface:"+surface.Type)
		}
	}
	if req.RequireBrowser && (inv.Browser == nil || !inv.Browser.Available) {
		missing = append(missing, "browser")
	}
	if cp.Browser != nil && cp.Browser.Available && (inv.Browser == nil || !inv.Browser.Available) {
		missing = append(missing, "browser")
	}
	if req.RequireDocker && (inv.Docker == nil || !inv.Docker.Available) {
		missing = append(missing, "docker")
	}
	if cp.Docker != nil && cp.Docker.Available && (inv.Docker == nil || !inv.Docker.Available) {
		missing = append(missing, "docker")
	}

	return missing
}

// scoreWorkers assigns a soft-preference score to each eligible worker.
// Higher scores are better.
func (pr *PlacementRouter) scoreWorkers(workers []*Worker, req PlacementRequest) map[string]int {
	scores := make(map[string]int, len(workers))

	for _, w := range workers {
		score := 100 // base score

		// Local-first: prefer local workers (default bias).
		if w.LocationType == LocationLocal {
			score += 5
		}

		// Local-first: explicit preference bonus.
		if req.PreferLocal && w.LocationType == LocationLocal {
			score += 25
		}

		// Clean workspace: prefer non-local (clean) workers.
		if req.PreferCleanWorkspace && w.LocationType != LocationLocal {
			score += 25
		}

		// Cloud for long-running: prefer cloud/sandbox workers.
		if req.PreferCloudForLongRunning {
			switch w.LocationType {
			case LocationVM, LocationSandbox, LocationContainer:
				score += 20
			}
		}

		// Prefer lower load.
		if w.Load == 0 {
			score += 10
		} else if w.Load < 3 {
			score += 5
		}

		// Prefer privileged workers for capability-rich tasks.
		if w.TrustTier == TrustTierPrivileged {
			score += 5
		}

		// Small bonus for draining workers to complete remaining work faster.
		if w.Status == WorkerStatusDraining {
			score -= 15
		}

		scores[w.ID] = score
	}

	return scores
}

// selectBest returns the worker with the highest score.
// Ties are broken by worker ID for determinism.
func (pr *PlacementRouter) selectBest(workers []*Worker, scores map[string]int) *Worker {
	if len(workers) == 0 {
		return nil
	}

	best := workers[0]
	bestScore := scores[best.ID]

	for _, w := range workers[1:] {
		s := scores[w.ID]
		if s > bestScore || (s == bestScore && w.ID < best.ID) {
			best = w
			bestScore = s
		}
	}

	return best
}

// explainDecision produces a human-readable explanation of the placement.
func (pr *PlacementRouter) explainDecision(selected *Worker, score int, record *PlacementRecord) string {
	parts := []string{
		fmt.Sprintf("selected worker %s (%s, %s, trust=%s) with score %d",
			selected.ID, selected.Name, selected.LocationType, selected.TrustTier, score),
	}

	if len(record.EligibleWorkers) > 1 {
		parts = append(parts, fmt.Sprintf("from %d eligible workers", len(record.EligibleWorkers)))
	} else {
		parts = append(parts, "only eligible worker")
	}

	if len(record.RejectedWorkers) > 0 {
		// Count rejections by category.
		catCounts := make(map[string]int)
		for _, r := range record.RejectedWorkers {
			catCounts[r.Category]++
		}
		var catParts []string
		for cat, count := range catCounts {
			catParts = append(catParts, fmt.Sprintf("%d %s", count, cat))
		}
		sort.Strings(catParts)
		parts = append(parts, fmt.Sprintf("%d workers rejected (%s)", len(record.RejectedWorkers), strings.Join(catParts, ", ")))
	}

	return strings.Join(parts, "; ")
}

// trustTierMeets returns true if the worker's trust tier meets or exceeds the minimum.
// An empty minimumTier means no trust constraint (always returns true).
func trustTierMeets(workerTier, minimumTier TrustTier) bool {
	if minimumTier == "" {
		return true
	}
	tierRank := map[TrustTier]int{
		TrustTierUntrusted:  0,
		TrustTierStandard:   1,
		TrustTierPrivileged: 2,
	}

	workerRank, ok := tierRank[workerTier]
	if !ok {
		return false
	}
	minRank, ok := tierRank[minimumTier]
	if !ok {
		return false
	}
	return workerRank >= minRank
}

// hasAnyWorkspaceMode returns true if the worker supports any of the required modes.
func hasAnyWorkspaceMode(supported, required []string) bool {
	supportedSet := make(map[string]bool, len(supported))
	for _, m := range supported {
		supportedSet[m] = true
	}
	for _, m := range required {
		if supportedSet[m] {
			return true
		}
	}
	return false
}

func inventoryHasTool(inv *CapabilityInventory, name string) bool {
	for _, tool := range inv.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func inventoryHasMCPServer(inv *CapabilityInventory, name string) bool {
	for _, server := range inv.MCPServers {
		if server.Name == name {
			return true
		}
	}
	return false
}

func inventoryHasMemory(inv *CapabilityInventory, name string) bool {
	for _, memory := range inv.Memories {
		if memory.Name == name {
			return true
		}
	}
	return false
}

func inventoryHasRepo(inv *CapabilityInventory, repoURL string) bool {
	for _, repo := range inv.Repos {
		if repo.RepoURL == repoURL {
			return true
		}
	}
	return false
}

func inventoryHasSecret(inv *CapabilityInventory, required SecretCapability) bool {
	for _, secret := range inv.Secrets {
		if required.Ref != "" && secret.Ref == required.Ref {
			return true
		}
		if required.Ref == "" && required.Name != "" && secret.Name == required.Name {
			return true
		}
	}
	return false
}

func inventoryHasOutputSurface(inv *CapabilityInventory, surfaceType string) bool {
	for _, surface := range inv.OutputSurfaces {
		if surface.Type == surfaceType {
			return true
		}
	}
	return false
}
