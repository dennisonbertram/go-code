package main

import (
	"sync"
	"testing"
)

func TestSingleNodeLeaderElection(t *testing.T) {
	n := NewRaftNode()
	// Candidate requests vote for itself
	ok := n.RequestVote(1, 1)
	if !ok {
		t.Fatalf("Expected node to grant vote to itself in new term")
	}
	if n.votedFor != 1 {
		t.Errorf("Expected votedFor to be 1, got %d", n.votedFor)
	}
	if n.currentTerm != 1 {
		t.Errorf("Expected term to be 1, got %d", n.currentTerm)
	}

	n.BecomeLeader()
	if n.state != Leader {
		t.Errorf("Expected to become Leader, state=%d", n.state)
	}
}

func TestProposeAndAppendEntries(t *testing.T) {
	n := NewRaftNode()
	n.currentTerm = 2
	n.BecomeLeader()
	idx, err := n.Propose("doSomething")
	if err != nil {
		t.Fatalf("Propose failed as leader: %v", err)
	}
	if idx != 0 {
		t.Errorf("Expected log index 0, got %d", idx)
	}
	if len(n.log) != 1 || n.log[0].Command != "doSomething" {
		t.Errorf("Incorrect log content: %+v", n.log)
	}
	// Follower accepts entries if term >= currentTerm
	result := n.AppendEntries(2, []LogEntry{{Term: 2, Command: "otherCmd"}})
	if !result {
		t.Errorf("AppendEntries should succeed for current term")
	}
	if len(n.log) != 2 {
		t.Errorf("Expected log length 2, got %d", len(n.log))
	}
}

func TestRejectStaleTerm(t *testing.T) {
	n := NewRaftNode()
	n.currentTerm = 3
	ok := n.RequestVote(2, 7) // stale term
	if ok {
		t.Errorf("Should reject RequestVote for stale term")
	}
	ok2 := n.AppendEntries(2, nil)
	if ok2 {
		t.Errorf("Should reject AppendEntries for stale term")
	}
}

func TestTermIncrementsAndStepDown(t *testing.T) {
	n := NewRaftNode()
	n.currentTerm = 3
	n.BecomeLeader()
	// Receive higher term
	ok := n.AppendEntries(5, nil)
	if !ok || n.currentTerm != 5 {
		t.Fatalf("AppendEntries with higher term should update term and succeed")
	}
	if n.state != Follower {
		t.Fatalf("Node should step down to Follower on higher term")
	}
}

func TestConcurrency(t *testing.T) {
	n := NewRaftNode()
	n.BecomeLeader()

	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(ix int) {
			defer wg.Done()
			_, _ = n.Propose("cmd")
		}(i)
	}
	wg.Wait()
	if n.commitIndex != len(n.log)-1 {
		t.Errorf("Commit index mismatch under concurrency")
	}
}
