package tui

import (
	"testing"
	"time"
)

func TestFormatSwarmPanelLinesMultiStatus(t *testing.T) {
	t.Parallel()

	p := swarmPanel{
		Active: true,
		Members: []swarmPanelMember{
			{Item: "alpha", ID: "sub-1", Status: "completed"},
			{Item: "beta", ID: "sub-2", Status: "running"},
			{Item: "gamma", Status: "pending"},
			{Item: "delta", ID: "sub-4", Status: "failed"},
		},
	}
	lines := formatSwarmPanelLines(p)
	want := []string{
		"Swarm: 3 launched, 1/4 completed (cap 128)",
		"  [completed] alpha (sub-1)",
		"  [running  ] beta (sub-2)",
		"  [pending  ] gamma",
		"  [failed   ] delta (sub-4)",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (all: %v)", i, lines[i], want[i], lines)
		}
	}
}

func TestResolveSwarmPanelMatchesByCreationWindow(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	tr := &swarmTracker{
		active:    true,
		items:     []string{"a", "b", "c"},
		startedAt: start,
	}
	remote := []RemoteSubagent{
		// Created before the swarm started: not a member.
		{ID: "old", Status: "completed", CreatedAt: start.Add(-time.Minute)},
		// Members, deliberately out of order to prove CreatedAt sorting.
		{ID: "s2", Status: "completed", CreatedAt: start.Add(2 * time.Second)},
		{ID: "s1", Status: "running", CreatedAt: start.Add(time.Second)},
	}
	p := tr.resolvePanel(remote)
	if !p.Active {
		t.Fatal("panel Active = false, want true")
	}
	if len(p.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(p.Members))
	}
	want := []swarmPanelMember{
		{Item: "a", ID: "s1", Status: "running"},
		{Item: "b", ID: "s2", Status: "completed"},
		{Item: "c", ID: "", Status: "pending"},
	}
	for i, w := range want {
		if p.Members[i] != w {
			t.Errorf("member %d = %+v, want %+v", i, p.Members[i], w)
		}
	}
}

func TestResolveSwarmPanelResumeEntries(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	tr := &swarmTracker{
		active:      true,
		items:       []string{"a", "b"},
		resumeCount: 1,
		startedAt:   start,
	}
	remote := []RemoteSubagent{
		{ID: "s1", Status: "running", CreatedAt: start.Add(time.Second)},
	}
	p := tr.resolvePanel(remote)
	if len(p.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(p.Members))
	}
	if p.Members[0].Status != "resumed" || p.Members[0].ID != "" {
		t.Errorf("resume entry = %+v, want status resumed with no id pre-report", p.Members[0])
	}
	if p.Members[1].ID != "s1" || p.Members[1].Status != "running" {
		t.Errorf("new entry = %+v, want s1 running", p.Members[1])
	}
}

func TestResolveSwarmPanelReportSetUsesExactMembers(t *testing.T) {
	t.Parallel()

	tr := &swarmTracker{
		items: []string{"a"},
		report: []swarmPanelMember{
			{Item: "a", ID: "sub-9", Status: "failed"},
		},
		reportSet: true,
	}
	p := tr.resolvePanel(nil)
	if len(p.Members) != 1 || p.Members[0].ID != "sub-9" || p.Members[0].Status != "failed" {
		t.Fatalf("members = %+v, want exact report member sub-9 failed", p.Members)
	}
}

func TestFormatSubagentsLinesNoSwarmRegression(t *testing.T) {
	t.Parallel()

	if got := formatSubagentsLines(nil, nil); len(got) != 1 || got[0] != "No managed subagents." {
		t.Fatalf("empty listing = %v, want [No managed subagents.]", got)
	}

	items := []RemoteSubagent{{ID: "sub-1", Status: "running", Isolation: "inline", CleanupPolicy: "preserve"}}
	got := formatSubagentsLines(items, nil)
	if len(got) != 1 || got[0] != "sub-1 [running] inline (preserve)" {
		t.Fatalf("listing = %v, want legacy single-line format", got)
	}
}

func TestFormatSubagentsLinesWithSwarmGroup(t *testing.T) {
	t.Parallel()

	panel := &swarmPanel{Members: []swarmPanelMember{
		{Item: "alpha", ID: "sub-1", Status: "completed"},
		{Item: "beta", Status: "pending"},
	}}
	items := []RemoteSubagent{{ID: "other", Status: "queued", Isolation: "inline", CleanupPolicy: "preserve"}}
	got := formatSubagentsLines(items, panel)

	if len(got) < 5 {
		t.Fatalf("listing too short: %v", got)
	}
	if got[0] != "Swarm: 1 launched, 1/2 completed (cap 128)" {
		t.Errorf("summary line = %q", got[0])
	}
	if got[1] != "  [completed] alpha (sub-1)" || got[2] != "  [pending  ] beta" {
		t.Errorf("member rows = %q, %q", got[1], got[2])
	}
	// The regular listing follows the swarm group.
	last := got[len(got)-1]
	if last != "other [queued] inline (preserve)" {
		t.Errorf("last line = %q, want regular subagent entry", last)
	}
}
