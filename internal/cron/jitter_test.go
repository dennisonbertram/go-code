package cron

import (
	"fmt"
	"testing"
	"time"
)

func TestComputeJitter_Deterministic(t *testing.T) {
	cfg := DefaultJitterConfig()

	// Same inputs should produce the same jitter.
	j1 := computeJitter(cfg, "job-1", "*/5 * * * *")
	j2 := computeJitter(cfg, "job-1", "*/5 * * * *")
	if j1 != j2 {
		t.Fatalf("expected deterministic jitter: got %v and %v", j1, j2)
	}

	// Different jobs should (with high probability) produce different jitter.
	j3 := computeJitter(cfg, "job-2", "*/5 * * * *")
	if j1 == j3 {
		t.Logf("note: jitter collision between job-1 and job-2 (unlikely but possible)")
	}

	// Different schedules for same job should produce different jitter.
	j4 := computeJitter(cfg, "job-1", "0 * * * *")
	if j1 == j4 {
		t.Logf("note: jitter collision between different schedules (unlikely but possible)")
	}
}

func TestComputeJitter_Range(t *testing.T) {
	cfg := DefaultJitterConfig()
	cfg.MinSec = 60
	cfg.MaxSec = 300

	for i := 0; i < 100; i++ {
		jobID := fmt.Sprintf("job-range-%d", i)
		jitter := computeJitter(cfg, jobID, "*/5 * * * *")
		jitterSec := int(jitter.Seconds())

		if jitterSec < cfg.MinSec || jitterSec > cfg.MaxSec {
			t.Errorf("job %s: jitter %v (%d sec) outside range [%d, %d]",
				jobID, jitter, jitterSec, cfg.MinSec, cfg.MaxSec)
		}
	}
}

func TestComputeJitter_Disabled(t *testing.T) {
	cfg := DefaultJitterConfig()
	cfg.Enabled = false

	jitter := computeJitter(cfg, "job-1", "*/5 * * * *")
	if jitter != 0 {
		t.Fatalf("expected zero jitter when disabled, got %v", jitter)
	}
}

func TestComputeJitter_ZeroRange(t *testing.T) {
	cfg := DefaultJitterConfig()
	cfg.MinSec = 300
	cfg.MaxSec = 300

	// When MinSec >= MaxSec, should return 0 (sanity check).
	jitter := computeJitter(cfg, "job-1", "*/5 * * * *")
	if jitter != 0 {
		t.Fatalf("expected zero jitter when MinSec >= MaxSec, got %v", jitter)
	}
}

func TestAvoidMinuteMarks_AdjustsAway(t *testing.T) {
	// Fire at exactly 12:29:50. With 10s jitter, we'd land at 12:30:00 (bad minute 30).
	// avoidMinuteMarks should bump it past :30.
	fireTime := time.Date(2025, 1, 1, 12, 29, 50, 0, time.UTC)
	offset := 10 * time.Second

	adjusted := avoidMinuteMarks(offset, fireTime, []int{0, 30})
	landing := fireTime.Add(adjusted)

	if landing.Minute() == 0 || landing.Minute() == 30 {
		t.Fatalf("adjusted jitter still lands on bad minute %d (offset=%v)", landing.Minute(), adjusted)
	}
}

func TestAvoidMinuteMarks_AlreadyClean(t *testing.T) {
	// Fire at 12:15:00. With 60s jitter, we land at 12:16:00 (clean minute).
	// avoidMinuteMarks should return the same offset.
	fireTime := time.Date(2025, 1, 1, 12, 15, 0, 0, time.UTC)
	offset := 60 * time.Second

	adjusted := avoidMinuteMarks(offset, fireTime, []int{0, 30})
	if adjusted != offset {
		t.Fatalf("expected unchanged offset %v, got %v", offset, adjusted)
	}
}

func TestAvoidMinuteMarks_EmptyMarks(t *testing.T) {
	fireTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	offset := 60 * time.Second

	adjusted := avoidMinuteMarks(offset, fireTime, nil)
	if adjusted != offset {
		t.Fatalf("expected unchanged offset with empty marks, got %v", adjusted)
	}
}

func TestJitter_Distribution_NoMinuteMarks(t *testing.T) {
	// Test 10k jittered times: 0% should land on :00 or :30.
	// This verifies that avoidMinuteMarks is effective when applied at fire time.
	cfg := DefaultJitterConfig()
	cfg.MinSec = 60
	cfg.MaxSec = 300

	const samples = 10000
	var badCount int

	for i := 0; i < samples; i++ {
		jobID := fmt.Sprintf("job-dist-%d", i)

		// Use various fire times across the hour.
		minute := (i * 7) % 60
		fireTime := time.Date(2025, 1, 1, 12, minute, 0, 0, time.UTC)

		baseJitter := computeJitter(cfg, jobID, fmt.Sprintf("%d * * * *", minute))
		// Minute-mark avoidance applied at fire time.
		jitter := avoidMinuteMarks(baseJitter, fireTime, cfg.AvoidMarks)
		landing := fireTime.Add(jitter)

		if landing.Minute() == 0 || landing.Minute() == 30 {
			badCount++
		}
	}

	if badCount > 0 {
		t.Fatalf("expected 0 samples landing on :00/:30, got %d out of %d (%.4f%%)",
			badCount, samples, float64(badCount)/float64(samples)*100)
	}
}

func TestJitter_Distribution_UniformCoverage(t *testing.T) {
	// Test that jittered minutes are roughly uniformly distributed across
	// non-avoided minute marks.
	cfg := DefaultJitterConfig()
	cfg.MinSec = 60
	cfg.MaxSec = 300

	const samples = 10000
	minuteHist := make(map[int]int)

	for i := 0; i < samples; i++ {
		jobID := fmt.Sprintf("job-uni-%d", i)
		fireTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
		baseJitter := computeJitter(cfg, jobID, "0 * * * *")
		jitter := avoidMinuteMarks(baseJitter, fireTime, cfg.AvoidMarks)
		landing := fireTime.Add(jitter)
		minuteHist[landing.Minute()]++
	}

	// Check that no avoided marks appear.
	for _, m := range cfg.AvoidMarks {
		if minuteHist[m] > 0 {
			t.Fatalf("avoided minute mark %d has %d samples", m, minuteHist[m])
		}
	}

	// Expect coverage of at least 3 different minutes (since jitter range is 1-5 min).
	covered := len(minuteHist)
	if covered < 3 {
		t.Fatalf("expected at least 3 distinct minutes covered, got %d", covered)
	}
	t.Logf("covered %d distinct minutes across %d samples", covered, samples)
}

func TestJitterCacheKey(t *testing.T) {
	k1 := jitterCacheKey("job-1", "*/5 * * * *")
	k2 := jitterCacheKey("job-1", "*/5 * * * *")
	k3 := jitterCacheKey("job-1", "0 * * * *")
	k4 := jitterCacheKey("job-2", "*/5 * * * *")

	if k1 != k2 {
		t.Fatal("same job ID and schedule should produce same cache key")
	}
	if k1 == k3 {
		t.Fatal("different schedule should produce different cache key")
	}
	if k1 == k4 {
		t.Fatal("different job ID should produce different cache key")
	}
}

func TestDefaultJitterConfig(t *testing.T) {
	cfg := DefaultJitterConfig()

	if !cfg.Enabled {
		t.Error("expected JitterEnabled to be true by default")
	}
	if cfg.MinSec != 60 {
		t.Errorf("expected MinSec 60, got %d", cfg.MinSec)
	}
	if cfg.MaxSec != 300 {
		t.Errorf("expected MaxSec 300, got %d", cfg.MaxSec)
	}
	if len(cfg.AvoidMarks) != 2 || cfg.AvoidMarks[0] != 0 || cfg.AvoidMarks[1] != 30 {
		t.Errorf("expected AvoidMarks [0, 30], got %v", cfg.AvoidMarks)
	}
	if !cfg.LogJitteredTimes {
		t.Error("expected LogJitteredTimes to be true by default")
	}
}
