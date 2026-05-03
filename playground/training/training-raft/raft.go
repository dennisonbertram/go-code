package main

import "sync"

// Raft states
const (
	Follower = iota
	Candidate
	Leader
)

type LogEntry struct {
	Term    int
	Command string
}

type RaftNode struct {
	mu          sync.Mutex
	state       int
	log         []LogEntry
	commitIndex int
	currentTerm int
	votedFor    int // -1 means nobody
}

func NewRaftNode() *RaftNode {
	return &RaftNode{
		state:       Follower,
		log:         make([]LogEntry, 0),
		commitIndex: -1,
		currentTerm: 0,
		votedFor:    -1,
	}
}

func (rn *RaftNode) AppendEntries(term int, entries []LogEntry) bool {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if term < rn.currentTerm {
		return false
	}
	if term > rn.currentTerm {
		rn.currentTerm = term
		rn.stepDown()
	}
	rn.state = Follower
	if len(entries) > 0 {
		rn.log = append(rn.log, entries...)
		rn.commitIndex = len(rn.log) - 1
	}
	return true
}

func (rn *RaftNode) RequestVote(term int, candidateID int) bool {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if term < rn.currentTerm {
		return false
	}
	if term > rn.currentTerm {
		rn.currentTerm = term
		rn.votedFor = -1 // can vote again in this new term
		rn.stepDown()
	}
	if rn.votedFor == -1 || rn.votedFor == candidateID {
		rn.votedFor = candidateID
		rn.state = Follower
		return true
	}
	return false
}

func (rn *RaftNode) Propose(cmd string) (int, error) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rn.state != Leader {
		return -1, ErrNotLeader
	}
	entry := LogEntry{
		Term:    rn.currentTerm,
		Command: cmd,
	}
	rn.log = append(rn.log, entry)
	rn.commitIndex = len(rn.log) - 1
	return rn.commitIndex, nil
}

func (rn *RaftNode) BecomeLeader() {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.state = Leader
}

func (rn *RaftNode) stepDown() {
	rn.state = Follower
}

var ErrNotLeader = &NotLeaderError{}
type NotLeaderError struct{}
func (e *NotLeaderError) Error() string { return "not leader" }
