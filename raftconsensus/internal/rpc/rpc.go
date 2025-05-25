// internal/rpc/rpc.go
package rpc

// LogEntry represents an entry in the replicated log
type LogEntry struct {
	Term    int
	Index   int
	Command string
}

// RequestVoteArgs is the arguments for RequestVote RPC
type RequestVoteArgs struct {
	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
}

// RequestVoteReply is the reply for RequestVote RPC
type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

// AppendEntriesArgs is the arguments for AppendEntries RPC (heartbeats and log replication)
type AppendEntriesArgs struct {
	Term         int
	LeaderID     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

// AppendEntriesReply is the reply for AppendEntries RPC
type AppendEntriesReply struct {
	Term    int
	Success bool
	// Added FollowerID to identify which follower sent this reply
	FollowerID int
}
