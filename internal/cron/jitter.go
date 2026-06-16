package cron

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"time"
)

// JitterConfig controls the per-job jitter applied to scheduled task execution times.
type JitterConfig struct {
	// Enabled controls whether jitter is applied. Default: true.
	Enabled bool

	// MinSec is the minimum jitter offset in seconds. Default: 60.
	MinSec int

	// MaxSec is the maximum jitter offset in seconds. Default: 300.
	MaxSec int

	// AvoidMarks lists the minute marks (0-59) to avoid landing on.
	// Default: [0, 30].
	AvoidMarks []int

	// LogJitteredTimes controls whether jittered execution times are logged.
	// Default: true.
	LogJitteredTimes bool
}

// DefaultJitterConfig returns the default jitter configuration.
func DefaultJitterConfig() JitterConfig {
	return JitterConfig{
		Enabled:          true,
		MinSec:           60,
		MaxSec:           300,
		AvoidMarks:       []int{0, 30},
		LogJitteredTimes: true,
	}
}

// computeJitter returns a deterministic base jitter duration for a job. The
// jitter is computed from the job ID and schedule using a hash-based seed so
// the same job always gets the same base jitter offset across scheduler
// restarts.
//
// The returned duration is in [cfg.MinSec, cfg.MaxSec]. Minute-mark avoidance
// is applied separately at fire time via avoidMinuteMarks.
func computeJitter(cfg JitterConfig, jobID, schedule string) time.Duration {
	// Sanity: if disabled or range is zero, return zero.
	if !cfg.Enabled || cfg.MinSec >= cfg.MaxSec {
		return 0
	}

	// Deterministic seed from job ID + schedule.
	h := fnv.New64a()
	h.Write([]byte(jobID))
	h.Write([]byte{0})
	h.Write([]byte(schedule))

	// Use the hash as a seed for a new random source.
	src := rand.NewPCG(h.Sum64(), h.Sum64()^0x5bd1e995)
	rng := rand.New(src)

	// Produce a raw jitter in [MinSec, MaxSec].
	span := cfg.MaxSec - cfg.MinSec
	raw := rng.IntN(span+1) + cfg.MinSec

	return time.Duration(raw) * time.Second
}

// avoidMinuteMarks adjusts the jitter offset so that (fireTime + offset) does
// not land on any minute listed in avoidMarks. It walks forward in 1-second
// increments until a clean minute is found.
func avoidMinuteMarks(offset time.Duration, fireTime time.Time, avoidMarks []int) time.Duration {
	if len(avoidMarks) == 0 {
		return offset
	}

	// Build a quick-lookup set.
	bad := make(map[int]bool, len(avoidMarks))
	for _, m := range avoidMarks {
		bad[m%60] = true
	}

	const maxWalk = 120 // max seconds to walk forward; safety cap

	base := offset
	for i := 0; i < maxWalk; i++ {
		landing := fireTime.Add(offset)
		if !bad[landing.Minute()] {
			return offset
		}
		offset += time.Second
	}
	// Safety fallback: return original offset even if it lands on a bad minute.
	return base
}

// jitterCacheKey returns the cache key for a job's jitter computation.
func jitterCacheKey(jobID, schedule string) string {
	return fmt.Sprintf("%s|%s", jobID, schedule)
}
