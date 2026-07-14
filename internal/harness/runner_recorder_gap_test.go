package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-agent-harness/internal/rollout"
)

// TestStartRecorderGoroutine_GapSkipForwardProgress verifies that when a Seq
// is dropped (simulated by never sending Seq 2), later events still get
// recorded instead of stalling the recorder forever.
func TestStartRecorderGoroutine_GapSkipForwardProgress(t *testing.T) {
	t.Parallel()

	runID := "gap-test"
	rolloutDir := t.TempDir()
	now := time.Now().UTC()

	rec, err := rollout.NewRecorderAt(rollout.RecorderConfig{Dir: rolloutDir, RunID: runID}, now)
	if err != nil {
		t.Fatalf("NewRecorderAt: %v", err)
	}

	state := &runState{}
	startRecorderGoroutine(state, rec)

	// Send Seq 0, 1, then deliberately skip Seq 2 (drop), then send 3 and 4.
	for _, seq := range []uint64{0, 1, 3, 4} {
		ev := rollout.RecordableEvent{
			ID:        fmt.Sprintf("%s:%d", runID, seq),
			RunID:     runID,
			Type:      "test.gap",
			Timestamp: now,
			Seq:       seq,
		}
		state.recorderCh <- ev
	}

	state.closeRecorderOnce()

	select {
	case <-state.recorderDone:
	case <-time.After(5 * time.Second):
		t.Fatal("recorderDone did not close after closeRecorderOnce()")
	}

	jsonlPath := filepath.Join(rolloutDir, now.Format("2006-01-02"), runID+".jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("reading JSONL file: %v", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	var seqs []uint64
	for {
		var e struct {
			Seq uint64 `json:"seq"`
		}
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode JSONL line: %v", err)
		}
		seqs = append(seqs, e.Seq)
	}

	want := []uint64{0, 1, 3, 4}
	if len(seqs) != len(want) {
		t.Fatalf("recorded %d events, want %d: got %v, want %v", len(seqs), len(want), seqs, want)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Fatalf("recorded seq mismatch at index %d: got %v, want %v", i, seqs, want)
		}
	}
}
