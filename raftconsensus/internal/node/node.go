// internal/node/node.go
package node

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"raftconsensus/internal/network" // Import the new network package
	"raftconsensus/internal/rpc"     // Import the new rpc package
)

// Define node states
const (
	Follower  = "Follower"
	Candidate = "Candidate"
	Leader    = "Leader"
)

// Global configuration parameters for node behavior
const (
	minElectionTimeoutMs = 150
	maxElectionTimeoutMs = 300
	heartbeatIntervalMs  = 50
)

// Node represents a single node in the Raft cluster
type Node struct {
	mu          sync.Mutex // Mutex to protect node state
	id          int
	state       string
	currentTerm int
	votedFor    int // Candidate ID that received vote in current term (-1 if none)
	log         []rpc.LogEntry
	commitIndex int // Index of highest log entry known to be committed
	lastApplied int // Index of highest log entry applied to state machine

	// Leader-specific volatile state
	nextIndex  []int // For each server, index of the next log entry to send to that server
	matchIndex []int // For each server, index of the highest log entry known to be replicated on server

	// Channels for communication
	rpcChan          chan interface{} // Channel to receive incoming RPCs
	network          *network.Network // Reference to the simulated network
	peers            []int            // IDs of other nodes in the cluster
	electionTimeout  time.Duration
	heartbeatTimeout time.Duration

	// Timers
	electionTimer  *time.Timer
	heartbeatTimer *time.Timer // Only for leader

	// Quorum management
	votesReceived int
	majority      int

	// Channel for client commands
	commandChan chan string
}

// NewNode creates and initializes a new Raft node
func NewNode(id int, peers []int, net *network.Network, majority int) *Node {
	node := &Node{
		id:               id,
		state:            Follower,
		currentTerm:      0,
		votedFor:         -1,
		log:              []rpc.LogEntry{{Term: 0, Index: 0, Command: "dummy"}}, // Dummy entry at index 0
		commitIndex:      0,
		lastApplied:      0,
		network:          net,
		peers:            peers,
		rpcChan:          net.GetNodeChannel(id), // Get the dedicated channel from the network
		electionTimeout:  randomElectionTimeout(),
		heartbeatTimeout: time.Duration(heartbeatIntervalMs) * time.Millisecond,
		nextIndex:        make([]int, len(peers)+1), // +1 because node IDs are 1-indexed
		matchIndex:       make([]int, len(peers)+1),
		majority:         majority,
		commandChan:      make(chan string, 10), // Buffered channel for client commands
	}
	log.Printf("[Node %d] Initialized as %s, Term %d. Election Timeout: %v\n", node.id, node.state, node.currentTerm, node.electionTimeout)
	return node
}

// Run starts the main loop of the Raft node
func (n *Node) Run() {
	n.electionTimer = time.NewTimer(n.electionTimeout)
	defer n.electionTimer.Stop()

	for {
		n.mu.Lock()
		state := n.state
		n.mu.Unlock()

		switch state {
		case Follower:
			n.runFollower()
		case Candidate:
			n.runCandidate()
		case Leader:
			n.runLeader()
		}
	}
}

// Getters for external access (e.g., by the API server)
func (n *Node) ID() int {
	return n.id
}

func (n *Node) State() string {
	return n.state
}

func (n *Node) CurrentTerm() int {
	return n.currentTerm
}

func (n *Node) Mu() *sync.Mutex {
	return &n.mu
}

// ProposeCommand allows a client to propose a new command to the leader.
func (n *Node) ProposeCommand(command string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return fmt.Errorf("node %d is not the leader", n.id)
	}

	select {
	case n.commandChan <- command:
		log.Printf("[Node %d] [Term %d] %s: Received client command: \"%s\"\n", n.id, n.currentTerm, n.state, command)
		return nil
	default:
		return fmt.Errorf("leader %d command channel is full, try again", n.id)
	}
}

// runFollower implements the logic for a Raft follower
func (n *Node) runFollower() {
	log.Printf("[Node %d] [Term %d] %s: Entering follower state.\n", n.id, n.currentTerm, n.state)
	n.resetElectionTimer() // Start or reset election timer

	for n.state == Follower {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case <-n.electionTimer.C:
			n.mu.Lock()
			if n.state == Follower { // Double check state to avoid race conditions
				log.Printf("[Node %d] [Term %d] %s: Election timeout, becoming Candidate.\n", n.id, n.currentTerm, n.state)
				n.state = Candidate
			}
			n.mu.Unlock()
		}
	}
}

// runCandidate implements the logic for a Raft candidate
func (n *Node) runCandidate() {
	n.mu.Lock()
	n.currentTerm++
	n.votedFor = n.id
	n.votesReceived = 1 // Vote for self
	log.Printf("[Node %d] [Term %d] %s: Started election.\n", n.id, n.currentTerm, n.state)
	n.resetElectionTimer() // Reset election timer for this term
	n.mu.Unlock()

	// Send RequestVote RPCs to all other nodes
	n.sendRequestVotes()

	for n.state == Candidate {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case <-n.electionTimer.C:
			n.mu.Lock()
			if n.state == Candidate { // Election timed out, start new election
				log.Printf("[Node %d] [Term %d] %s: Election timed out, restarting election.\n", n.id, n.currentTerm, n.state)
			}
			n.mu.Unlock()
			return // Exit to re-enter runCandidate and increment term
		}
	}
}

// runLeader implements the logic for a Raft leader
func (n *Node) runLeader() {
	n.mu.Lock()
	log.Printf("[Node %d] [Term %d] %s: Won election, becoming Leader.\n", n.id, n.currentTerm, n.state)
	// Initialize nextIndex and matchIndex for all followers
	for _, peerID := range n.peers {
		n.nextIndex[peerID] = len(n.log) // Next entry to send is one past the last one in leader's log
		n.matchIndex[peerID] = 0
	}
	n.mu.Unlock()

	// Start heartbeat timer
	n.heartbeatTimer = time.NewTimer(0) // Send initial heartbeat immediately
	defer n.heartbeatTimer.Stop()

	for n.state == Leader {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case clientCommand := <-n.commandChan: // Handle client commands
			n.mu.Lock()
			if n.state == Leader {
				newEntry := rpc.LogEntry{
					Term:    n.currentTerm,
					Index:   len(n.log),
					Command: clientCommand,
				}
				n.log = append(n.log, newEntry)
				log.Printf("[Node %d] [Term %d] %s: Appending client command: \"%s\" (Index: %d)\n", n.id, n.currentTerm, n.state, newEntry.Command, newEntry.Index)
				n.sendAppendEntriesToAll()
			}
			n.mu.Unlock()
		case <-n.heartbeatTimer.C:
			n.sendHeartbeats()
			n.heartbeatTimer.Reset(n.heartbeatTimeout)
		}
	}
}

// handleRPC processes an incoming RPC message
func (n *Node) handleRPC(rpcMsg interface{}) {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch r := rpcMsg.(type) {
	case rpc.RequestVoteArgs:
		reply := n.handleRequestVote(r)
		go n.network.SendRPC(n.id, r.CandidateID, reply)
	case rpc.RequestVoteReply:
		n.handleRequestVoteReply(r)
	case rpc.AppendEntriesArgs:
		reply := n.handleAppendEntries(r)
		go n.network.SendRPC(n.id, r.LeaderID, reply)
	case rpc.AppendEntriesReply:
		n.handleAppendEntriesReply(r)
	default:
		log.Printf("[Node %d] [Term %d] %s: Unknown RPC type received: %T\n", n.id, n.currentTerm, n.state, rpcMsg)
	}
}

// handleRequestVote handles an incoming RequestVote RPC
func (n *Node) handleRequestVote(args rpc.RequestVoteArgs) rpc.RequestVoteReply {
	reply := rpc.RequestVoteReply{Term: n.currentTerm, VoteGranted: false}

	log.Printf("[Node %d] [Term %d] %s: Received RequestVote from Node %d (Term %d, LastLogIndex %d, LastLogTerm %d)\n",
		n.id, n.currentTerm, n.state, args.CandidateID, args.Term, args.LastLogIndex, args.LastLogTerm)

	// Rule 1: If args.Term < currentTerm, reply false
	if args.Term < n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Denying vote to Node %d (stale term)\n", n.id, n.currentTerm, n.state, args.CandidateID)
		return reply
	}

	// Rule 2: If args.Term > currentTerm, update term and transition to follower
	if args.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received RequestVote with higher term %d from Node %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, args.Term, args.CandidateID)
		n.currentTerm = args.Term
		n.state = Follower
		n.votedFor = -1 // Reset votedFor for new term
		n.resetElectionTimer()
	}

	// Rule 3: If votedFor is null or candidateId, and candidate's log is at least as up-to-date as receiver's log, grant vote
	if (n.votedFor == -1 || n.votedFor == args.CandidateID) && n.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {
		n.votedFor = args.CandidateID
		reply.VoteGranted = true
		n.resetElectionTimer() // Granting vote resets election timer
		log.Printf("[Node %d] [Term %d] %s: Granted vote to Node %d.\n", n.id, n.currentTerm, n.state, args.CandidateID)
	} else {
		log.Printf("[Node %d] [Term %d] %s: Denying vote to Node %d (already voted or log not up-to-date).\n", n.id, n.currentTerm, n.state, args.CandidateID)
	}

	return reply
}

// isLogUpToDate checks if candidate's log is at least as up-to-date as receiver's log
func (n *Node) isLogUpToDate(candidateLastLogIndex int, candidateLastLogTerm int) bool {
	lastLogIndex := len(n.log) - 1
	lastLogTerm := n.log[lastLogIndex].Term

	if candidateLastLogTerm > lastLogTerm {
		return true
	}
	if candidateLastLogTerm == lastLogTerm && candidateLastLogIndex >= lastLogIndex {
		return true
	}
	return false
}

// handleRequestVoteReply processes a RequestVoteReply from a peer
func (n *Node) handleRequestVoteReply(reply rpc.RequestVoteReply) {
	// Rule 1: If reply.Term > currentTerm, update term and transition to follower
	if reply.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received RequestVoteReply with higher term %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, reply.Term)
		n.currentTerm = reply.Term
		n.state = Follower
		n.votedFor = -1
		n.resetElectionTimer()
		return
	}

	// Only process if still a candidate and reply is for current term
	if n.state == Candidate && reply.Term == n.currentTerm {
		if reply.VoteGranted {
			n.votesReceived++
			log.Printf("[Node %d] [Term %d] %s: Received vote. Total votes: %d/%d\n", n.id, n.currentTerm, n.state, n.votesReceived, n.majority)
			if n.votesReceived >= n.majority {
				n.state = Leader // Won election!
				// Stop the election timer as we are now leader
				if n.electionTimer != nil {
					n.electionTimer.Stop()
				}
			}
		}
	}
}

// handleAppendEntries handles an incoming AppendEntries RPC
func (n *Node) handleAppendEntries(args rpc.AppendEntriesArgs) rpc.AppendEntriesReply {
	reply := rpc.AppendEntriesReply{Term: n.currentTerm, Success: false}

	log.Printf("[Node %d] [Term %d] %s: Received AppendEntries from Node %d (Term %d, PrevLogIndex %d, PrevLogTerm %d, Entries: %d)\n",
		n.id, n.currentTerm, n.state, args.LeaderID, args.Term, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries))

	// Rule 1: If args.Term < currentTerm, reply false
	if args.Term < n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Denying AppendEntries from Node %d (stale term)\n", n.id, n.currentTerm, n.state, args.LeaderID)
		return reply
	}

	// Rule 2: If args.Term >= currentTerm, update term and transition to follower
	if args.Term >= n.currentTerm {
		if args.Term > n.currentTerm || n.state != Follower {
			log.Printf("[Node %d] [Term %d] %s: Received AppendEntries with higher/equal term %d. Stepping down to Follower.\n",
				n.id, n.currentTerm, n.state, args.Term)
			n.currentTerm = args.Term
			n.state = Follower
			n.votedFor = -1 // Reset votedFor for new term
		}
		n.resetElectionTimer() // Always reset election timer on valid AppendEntries
	}

	// Rule 3: If log doesn't contain an entry at prevLogIndex whose term matches prevLogTerm, reply false
	if args.PrevLogIndex >= len(n.log) || n.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		log.Printf("[Node %d] [Term %d] %s: Denying AppendEntries from Node %d (log inconsistency at index %d, term %d)\n",
			n.id, n.currentTerm, n.state, args.LeaderID, args.PrevLogIndex, args.PrevLogTerm)
		return reply
	}

	// Rule 4: If an existing entry conflicts with a new one (same index but different terms), delete the existing entry and all that follow it
	// Then append any new entries not already in the log
	newEntriesStartIndex := 0
	for i, entry := range args.Entries {
		logIndex := args.PrevLogIndex + 1 + i
		if logIndex < len(n.log) {
			if n.log[logIndex].Term != entry.Term {
				// Conflict found, truncate log from this point
				n.log = n.log[:logIndex]
				log.Printf("[Node %d] [Term %d] %s: Log conflict at index %d. Truncating log.\n", n.id, n.currentTerm, n.state, logIndex)
				break
			}
		} else {
			// No conflict, all subsequent entries are new
			newEntriesStartIndex = i
			break
		}
		newEntriesStartIndex = i + 1 // All entries up to this point matched
	}

	// Append new entries
	if newEntriesStartIndex < len(args.Entries) {
		n.log = append(n.log, args.Entries[newEntriesStartIndex:]...)
		log.Printf("[Node %d] [Term %d] %s: Appended %d new entries from leader (Index: %d to %d).\n",
			n.id, n.currentTerm, n.state, len(args.Entries)-newEntriesStartIndex, args.PrevLogIndex+1+newEntriesStartIndex, len(n.log)-1)
	}

	// Rule 5: If leaderCommit > commitIndex, set commitIndex = min(leaderCommit, index of last new entry)
	if args.LeaderCommit > n.commitIndex {
		newCommitIndex := args.LeaderCommit
		if newCommitIndex > len(n.log)-1 {
			newCommitIndex = len(n.log) - 1
		}
		n.commitIndex = newCommitIndex
		log.Printf("[Node %d] [Term %d] %s: Updated commitIndex to %d.\n", n.id, n.currentTerm, n.state, n.commitIndex)
		n.applyCommittedEntries()
	}

	reply.Success = true
	return reply
}

// handleAppendEntriesReply processes an AppendEntriesReply from a peer
func (n *Node) handleAppendEntriesReply(reply rpc.AppendEntriesReply) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Rule 1: If reply.Term > currentTerm, update term and transition to follower
	if reply.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received AppendEntriesReply with higher term %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, reply.Term)
		n.currentTerm = reply.Term
		n.state = Follower
		n.votedFor = -1
		n.resetElectionTimer()
		return
	}

	// Only process if still leader and reply is for current term
	if n.state == Leader && reply.Term == n.currentTerm {
		if reply.Success {
			// In a real Raft, you'd update nextIndex and matchIndex for the specific peer
			// and then check for commitment. This simulation simplifies by just checking for commitment
			// after all replies are received or after a timeout.
			// To properly implement this, the AppendEntriesArgs would need to include the peer ID.
			// For this basic simulation, we'll rely on the periodic checks in sendAppendEntriesToAll
			// to update matchIndex and nextIndex based on the latest successful replication.
		} else {
			// Log inconsistency, decrement nextIndex for this peer and retry
			// (This logic would be more complex in a real Raft, involving sending previous entries)
			if n.nextIndex[peerID] > 1 { // Ensure nextIndex doesn't go below 1 (index of first real entry)
				n.nextIndex[peerID]--
				log.Printf("[Node %d] [Term %d] %s: AppendEntries failed for Node %d. Decrementing nextIndex to %d.\n",
					n.id, n.currentTerm, n.state, peerID, n.nextIndex[peerID])
			}
		}
	}
}

// sendRequestVotes sends RequestVote RPCs to all peers
func (n *Node) sendRequestVotes() {
	lastLogIndex := len(n.log) - 1
	lastLogTerm := n.log[lastLogIndex].Term

	args := rpc.RequestVoteArgs{
		Term:         n.currentTerm,
		CandidateID:  n.id,
		LastLogIndex: lastLogIndex,
		LastLogTerm:  lastLogTerm,
	}

	for _, peerID := range n.peers {
		log.Printf("[Node %d] [Term %d] %s: Sent RequestVote to Node %d\n", n.id, n.currentTerm, n.state, peerID)
		go n.network.SendRPC(n.id, peerID, args)
	}
}

// sendHeartbeats sends empty AppendEntries RPCs to all followers
func (n *Node) sendHeartbeats() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return
	}

	log.Printf("[Node %d] [Term %d] %s: Sending heartbeats.\n", n.id, n.currentTerm, n.state)
	n.sendAppendEntriesToAll() // Heartbeats are just AppendEntries with no new entries
}

// sendAppendEntriesToAll sends AppendEntries RPCs to all followers, including log entries
func (n *Node) sendAppendEntriesToAll() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return
	}

	for _, peerID := range n.peers {
		// Calculate prevLogIndex and prevLogTerm for this specific follower
		prevLogIndex := n.nextIndex[peerID] - 1
		if prevLogIndex < 0 {
			prevLogIndex = 0 // Should not happen with dummy entry at index 0
		}
		prevLogTerm := n.log[prevLogIndex].Term

		// Entries to send to this follower
		entriesToSend := []rpc.LogEntry{}
		if n.nextIndex[peerID] < len(n.log) {
			entriesToSend = n.log[n.nextIndex[peerID]:]
		}

		args := rpc.AppendEntriesArgs{
			Term:         n.currentTerm,
			LeaderID:     n.id,
			PrevLogIndex: prevLogIndex,
			PrevLogTerm:  prevLogTerm,
			Entries:      entriesToSend,
			LeaderCommit: n.commitIndex,
		}

		go func(pID int) {
			reply := n.network.SendRPC(n.id, pID, args)
			n.handleAppendEntriesReplyForPeer(pID, reply) // Handle reply specific to this peer
		}(peerID)
	}
	n.checkCommitIndex() // Check for commitment after sending
}

// handleAppendEntriesReplyForPeer processes an AppendEntriesReply for a specific peer
func (n *Node) handleAppendEntriesReplyForPeer(peerID int, reply interface{}) {
	n.mu.Lock()
	defer n.mu.Unlock()

	aeReply, ok := reply.(rpc.AppendEntriesReply)
	if !ok {
		log.Printf("[Node %d] [Term %d] %s: Received unexpected reply type for AppendEntries from Node %d\n", n.id, n.currentTerm, n.state, peerID)
		return
	}

	// Rule 1: If reply.Term > currentTerm, update term and transition to follower
	if aeReply.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received AppendEntriesReply with higher term %d from Node %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, aeReply.Term, peerID)
		n.currentTerm = aeReply.Term
		n.state = Follower
		n.votedFor = -1
		n.resetElectionTimer()
		return
	}

	// Only process if still leader and reply is for current term
	if n.state == Leader && aeReply.Term == n.currentTerm {
		if aeReply.Success {
			// Update nextIndex and matchIndex for this follower
			// The nextIndex should be the index of the last entry sent + 1
			// The matchIndex should be the index of the last entry successfully replicated
			// This assumes all entries sent were successfully replicated.
			// In a real Raft, you'd track the specific entries sent in the RPC.
			// For this basic simulation, we update based on the current log length.
			newMatchIndex := len(n.log) - 1
			if newMatchIndex > n.matchIndex[peerID] {
				n.matchIndex[peerID] = newMatchIndex
				n.nextIndex[peerID] = newMatchIndex + 1
				log.Printf("[Node %d] [Term %d] %s: Node %d replicated up to index %d. NextIndex for Node %d: %d\n",
					n.id, n.currentTerm, n.state, peerID, n.matchIndex[peerID], peerID, n.nextIndex[peerID])
				n.checkCommitIndex() // Check for commitment after updating matchIndex
			}
		} else {
			// Replication failed, decrement nextIndex for this peer and retry
			// (This logic would be more complex in a real Raft, involving sending previous entries)
			if n.nextIndex[peerID] > 1 { // Ensure nextIndex doesn't go below 1 (index of first real entry)
				n.nextIndex[peerID]--
				log.Printf("[Node %d] [Term %d] %s: AppendEntries failed for Node %d. Decrementing nextIndex to %d.\n",
					n.id, n.currentTerm, n.state, peerID, n.nextIndex[peerID])
			}
		}
	}
}

// checkCommitIndex checks if any log entries can be committed
func (n *Node) checkCommitIndex() {
	// Find the highest N such that N > commitIndex, and a majority of matchIndex[i] >= N
	// and log[N].Term == currentTerm
	for i := len(n.log) - 1; i > n.commitIndex; i-- {
		if n.log[i].Term == n.currentTerm { // Only commit entries from current term
			replicatedCount := 1 // Leader itself has the entry
			for _, peerID := range n.peers {
				if n.matchIndex[peerID] >= i {
					replicatedCount++
				}
			}
			if replicatedCount >= n.majority {
				n.commitIndex = i
				log.Printf("[Node %d] [Term %d] %s: Committed log entry at index %d. Command: \"%s\"\n",
					n.id, n.currentTerm, n.state, n.commitIndex, n.log[n.commitIndex].Command)
				n.applyCommittedEntries()
				return // Only commit one level at a time, highest first
			}
		}
	}
}

// applyCommittedEntries applies committed log entries to the state machine
func (n *Node) applyCommittedEntries() {
	for n.lastApplied < n.commitIndex {
		n.lastApplied++
		entry := n.log[n.lastApplied]
		log.Printf("[Node %d] [Term %d] %s: Applying log entry %d: \"%s\"\n", n.id, n.currentTerm, n.state, entry.Index, entry.Command)
		// In a real system, this is where the command would be executed against the application state.
	}
}

// resetElectionTimer resets the election timer
func (n *Node) resetElectionTimer() {
	if n.electionTimer != nil {
		n.electionTimer.Stop()
	}
	n.electionTimeout = randomElectionTimeout()
	n.electionTimer = time.NewTimer(n.electionTimeout)
}

// randomElectionTimeout generates a random election timeout within the defined range
func randomElectionTimeout() time.Duration {
	return time.Duration(rand.Intn(maxElectionTimeoutMs-minElectionTimeoutMs)+minElectionTimeoutMs) * time.Millisecond
}
