package main

import (
	"sync"
	"testing"
)

func TestLeaderElectionSingleNode(t *testing.T) {
	node := NewRaftNode()
	term := 1
	voted := node.RequestVote(term, 0)
	if !voted {
		t.Fatalf("Single node should vote for self")
	}
	node.state = Candidate
	voted = node.RequestVote(term, 0)
	if !voted {
		t.Fatalf("Node should vote for self again if not already voted")
	}
}

func TestLogAppending(t *testing.T) {
	node := NewRaftNode()
	node.state = Leader
	node.currentTerm = 1
	idx, err := node.Propose("cmd1")
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}
	if idx != 0 || node.log[0].Command != "cmd1" {
		t.Fatalf("Unexpected log append result")
	}
}

func TestTermIncrements(t *testing.T) {
	node := NewRaftNode()
	accepted := node.AppendEntries(1, []LogEntry{{Term: 1, Command: "cmd"}})
	if !accepted || node.currentTerm != 1 {
		t.Fatalf("AppendEntries should accept equal/higher term")
	}
	accepted = node.AppendEntries(2, nil)
	if !accepted || node.currentTerm != 2 {
		t.Fatalf("AppendEntries should update term to 2")
	}
}

func TestStaleTermRejection(t *testing.T) {
	node := NewRaftNode()
	node.currentTerm = 3
	accepted := node.AppendEntries(2, nil)
	if accepted {
		t.Fatalf("AppendEntries should reject stale term")
	}
	voted := node.RequestVote(2, 1)
	if voted {
		t.Fatalf("RequestVote should reject stale term")
	}
}

func TestStepDown(t *testing.T) {
	node := NewRaftNode()
	node.state = Leader
	node.stepDown()
	if node.state != Follower {
		t.Fatalf("stepDown should set state to Follower")
	}
}

func TestNoDataRaces(t *testing.T) {
	node := NewRaftNode()
	node.state = Leader
	node.currentTerm = 1
	wg := sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = node.Propose("cmd")
			node.stepDown()
			node.RequestVote(1, 0)
			node.AppendEntries(1, nil)
		}(i)
	}
	wg.Wait()
}
