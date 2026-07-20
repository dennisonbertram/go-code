package statspanel_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/cmd/harnesscli/tui/components/statspanel"
)

// ── helpers ────────────────────────────────────────────────────────────────────

// sampleData returns 30 data points ending today with realistic counts and costs.
func sampleData() []statspanel.DataPoint {
	now := time.Now()
	pts := make([]statspanel.DataPoint, 30)
	for i := 0; i < 30; i++ {
		day := now.AddDate(0, 0, -(29 - i))
		count := (i*7 + 3) % 50
		cost := float64(count) * 0.0175
		pts[i] = statspanel.DataPoint{
			Date:  day,
			Count: count,
			Cost:  cost,
		}
	}
	return pts
}

// highData returns data where all counts are at the maximum (100).
func highData(n int) []statspanel.DataPoint {
	now := time.Now()
	pts := make([]statspanel.DataPoint, n)
	for i := 0; i < n; i++ {
		pts[i] = statspanel.DataPoint{
			Date:  now.AddDate(0, 0, -(n - 1 - i)),
			Count: 100,
			Cost:  1.75,
		}
	}
	return pts
}

// ── snapshot helpers ──────────────────────────────────────────────────────────

func writeSnapshot(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join("testdata", "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating snapshot dir: %v", err)
	}
	path := filepath.Join(dir, name+".txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing snapshot %s: %v", path, err)
	}
}

func readSnapshot(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "snapshots", name+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func assertSnapshot(t *testing.T, name, actual string) {
	t.Helper()
	expected := readSnapshot(t, name)
	if expected == "" {
		writeSnapshot(t, name, actual)
		t.Logf("created snapshot %s", name)
		return
	}
	if expected != actual {
		t.Errorf("snapshot mismatch for %s\ngot:\n%s\nwant:\n%s", name, actual, expected)
	}
}

// ── TUI-045 tests ─────────────────────────────────────────────────────────────

// TestTUI045_StatsHeatmapRendersLegendAndBars verifies View() contains "Activity".
func TestTUI045_StatsHeatmapRendersLegendAndBars(t *testing.T) {
	m := statspanel.New(sampleData()).SetWidth(80)
	out := m.View()
	if !strings.Contains(out, "Activity") {
		t.Errorf("expected 'Activity' in output\noutput:\n%s", out)
	}
}

// TestTUI045_StatsHeatmapPeriodToggle verifies TogglePeriod() cycles through all 3 periods.
func TestTUI045_StatsHeatmapPeriodToggle(t *testing.T) {
	m := statspanel.New(sampleData()).SetWidth(80)
	// Start at Week (0).
	if m.ActivePeriod() != statspanel.PeriodWeek {
		t.Errorf("expected initial period PeriodWeek, got %v", m.ActivePeriod())
	}
	// Toggle to Month.
	m = m.TogglePeriod()
	if m.ActivePeriod() != statspanel.PeriodMonth {
		t.Errorf("expected PeriodMonth after first toggle, got %v", m.ActivePeriod())
	}
	// Toggle to Year.
	m = m.TogglePeriod()
	if m.ActivePeriod() != statspanel.PeriodYear {
		t.Errorf("expected PeriodYear after second toggle, got %v", m.ActivePeriod())
	}
	// Toggle back to Week.
	m = m.TogglePeriod()
	if m.ActivePeriod() != statspanel.PeriodWeek {
		t.Errorf("expected PeriodWeek after third toggle (wrap), got %v", m.ActivePeriod())
	}
}

// TestTUI045_HeatmapIntensityMapping verifies high count maps to █ and zero maps to ░.
func TestTUI045_HeatmapIntensityMapping(t *testing.T) {
	// Single point at maximum count — should produce █.
	now := time.Now()
	highPt := []statspanel.DataPoint{
		{Date: now, Count: 1000, Cost: 10.0},
	}
	m := statspanel.New(highPt).SetWidth(80)
	out := m.View()
	if !strings.Contains(out, "█") {
		t.Errorf("expected '█' in output for high count data\noutput:\n%s", out)
	}

	// Zero count data — all cells should be ░.
	zeroPt := []statspanel.DataPoint{
		{Date: now, Count: 0, Cost: 0.0},
	}
	m2 := statspanel.New(zeroPt).SetWidth(80)
	out2 := m2.View()
	if strings.Contains(out2, "█") {
		t.Errorf("expected no '█' in output for zero count data\noutput:\n%s", out2)
	}
}

// TestTUI045_EmptyDataShowsEmptyGrid verifies zero data points renders safely.
func TestTUI045_EmptyDataShowsEmptyGrid(t *testing.T) {
	m := statspanel.New(nil).SetWidth(80)
	// Should not panic.
	out := m.View()
	if out == "" {
		t.Error("View() returned empty string for nil data")
	}
	// Should contain no █.
	if strings.Contains(out, "█") {
		t.Errorf("expected no '█' for empty data\noutput:\n%s", out)
	}
}

// TestTUI045_AllMaxDataFilledGrid verifies all-max data renders █ everywhere.
func TestTUI045_AllMaxDataFilledGrid(t *testing.T) {
	m := statspanel.New(highData(7)).SetWidth(80).SetPeriod(statspanel.PeriodWeek)
	out := m.View()
	if !strings.Contains(out, "█") {
		t.Errorf("expected '█' in output for all-max data\noutput:\n%s", out)
	}
}

// TestTUI045_MalformedTimestamps verifies zero-time DataPoints handled gracefully.
func TestTUI045_MalformedTimestamps(t *testing.T) {
	pts := []statspanel.DataPoint{
		{Date: time.Time{}, Count: 5, Cost: 0.1}, // zero time
		{Date: time.Time{}, Count: 0, Cost: 0.0},
	}
	m := statspanel.New(pts).SetWidth(80)
	// Should not panic.
	out := m.View()
	if out == "" {
		t.Error("View() returned empty string for malformed timestamps")
	}
}

// TestTUI045_ConcurrentStats verifies 10 goroutines each with own Model, no race.
func TestTUI045_ConcurrentStats(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pts := []statspanel.DataPoint{
				{Date: time.Now(), Count: i * 10, Cost: float64(i) * 0.5},
			}
			m := statspanel.New(pts).SetWidth(80)
			out := m.View()
			if out == "" {
				errs <- "View() returned empty string"
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestTUI045_PeriodLabels verifies period labels appear in View() output.
func TestTUI045_PeriodLabels(t *testing.T) {
	data := sampleData()

	cases := []struct {
		period statspanel.Period
		want   string
	}{
		{statspanel.PeriodWeek, "last 7 days"},
		{statspanel.PeriodMonth, "last 30 days"},
		{statspanel.PeriodYear, "last 365 days"},
	}
	for _, tc := range cases {
		m := statspanel.New(data).SetWidth(80).SetPeriod(tc.period)
		out := m.View()
		if !strings.Contains(out, tc.want) {
			t.Errorf("period %v: expected %q in output\noutput:\n%s", tc.period, tc.want, out)
		}
	}
}

// TestTUI045_TotalLinePresent verifies "Total runs:" and "Total cost:" are in View().
func TestTUI045_TotalLinePresent(t *testing.T) {
	m := statspanel.New(sampleData()).SetWidth(80)
	out := m.View()
	if !strings.Contains(out, "Total runs:") {
		t.Errorf("expected 'Total runs:' in output\noutput:\n%s", out)
	}
	if !strings.Contains(out, "Total cost:") {
		t.Errorf("expected 'Total cost:' in output\noutput:\n%s", out)
	}
}

// TestTUI045_BoundaryWidths verifies width=40, 80, 200 do not panic.
func TestTUI045_BoundaryWidths(t *testing.T) {
	data := sampleData()
	for _, w := range []int{40, 80, 200} {
		m := statspanel.New(data).SetWidth(w)
		out := m.View()
		if out == "" {
			t.Errorf("View() returned empty string for width=%d", w)
		}
	}
}

// ── visual snapshots ──────────────────────────────────────────────────────────

func snapshotData() []statspanel.DataPoint {
	// Use a fixed date to make snapshots deterministic.
	base := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	counts := []int{0, 5, 12, 3, 45, 8, 0, 22, 31, 7, 15, 50, 0, 3, 8, 0, 42, 18, 6, 0,
		33, 27, 0, 9, 14, 1, 0, 60, 100, 5}
	pts := make([]statspanel.DataPoint, len(counts))
	for i, c := range counts {
		pts[i] = statspanel.DataPoint{
			Date:  base.AddDate(0, 0, -(len(counts) - 1 - i)),
			Count: c,
			Cost:  float64(c) * 0.0175,
		}
	}
	return pts
}

// TestTUI045_VisualSnapshot_80x24 generates/checks a golden snapshot at width 80.
func TestTUI045_VisualSnapshot_80x24(t *testing.T) {
	m := statspanel.New(snapshotData()).SetWidth(80).SetPeriod(statspanel.PeriodMonth)
	out := m.View()
	assertSnapshot(t, "TUI-045-stats-80x24", out)
}

// TestTUI045_VisualSnapshot_120x40 generates/checks a golden snapshot at width 120.
func TestTUI045_VisualSnapshot_120x40(t *testing.T) {
	m := statspanel.New(snapshotData()).SetWidth(120).SetPeriod(statspanel.PeriodMonth)
	out := m.View()
	assertSnapshot(t, "TUI-045-stats-120x40", out)
}

// TestTUI045_VisualSnapshot_200x50 generates/checks a golden snapshot at width 200.
func TestTUI045_VisualSnapshot_200x50(t *testing.T) {
	m := statspanel.New(snapshotData()).SetWidth(200).SetPeriod(statspanel.PeriodMonth)
	out := m.View()
	assertSnapshot(t, "TUI-045-stats-200x50", out)
}
