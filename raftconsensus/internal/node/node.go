// internal/node/node.go
package node

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"raftconsensus/internal/network"
	"raftconsensus/internal/rpc"
)

const (
	Follower  = "Follower"
	Candidate = "Candidate"
	Leader    = "Leader"
)

const (
	minElectionTimeoutMs = 500
	maxElectionTimeoutMs = 1000
	heartbeatIntervalMs  = 100
)

type Node struct {
	mu          sync.Mutex
	id          int
	state       string
	currentTerm int
	votedFor    int
	log         []rpc.LogEntry
	commitIndex int
	lastApplied int

	nextIndex  []int
	matchIndex []int

	rpcChan          chan interface{}
	network          *network.Network
	peers            []int
	electionTimeout  time.Duration
	heartbeatTimeout time.Duration

	electionTimer  *time.Timer
	heartbeatTimer *time.Timer

	votesReceived int
	majority      int

	commandChan   chan string
	quorumSize    int
	lastHeartbeat time.Time
	partitioned   bool
}

func NewNode(id int, peers []int, net *network.Network, majority int) *Node {
	node := &Node{
		id:               id,
		state:            Follower,
		currentTerm:      0,
		votedFor:         -1,
		log:              []rpc.LogEntry{{Term: 0, Index: 0, Command: "dummy"}},
		commitIndex:      0,
		lastApplied:      0,
		network:          net,
		peers:            peers,
		rpcChan:          net.GetNodeChannel(id),
		electionTimeout:  randomElectionTimeout(),
		heartbeatTimeout: time.Duration(heartbeatIntervalMs) * time.Millisecond,
		nextIndex:        make([]int, len(peers)+1),
		matchIndex:       make([]int, len(peers)+1),
		majority:         majority,
		commandChan:      make(chan string, 10),
		quorumSize:       (len(peers)+1)/2 + 1,
		lastHeartbeat:    time.Now(),
		partitioned:      false,
	}
	log.Printf("[Node %d] Initialized as %s, Term %d. Election Timeout: %v, Quorum Size: %d\n",
		node.id, node.state, node.currentTerm, node.electionTimeout, node.quorumSize)
	return node
}

func (n *Node) Run() {
	// Start election timeout immediately with a random delay
	initialDelay := time.Duration(rand.Intn(300)) * time.Millisecond
	time.Sleep(initialDelay)

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

func (n *Node) runFollower() {
	log.Printf("[Node %d] [Term %d] %s: Entering follower state.\n", n.id, n.currentTerm, n.state)
	n.resetElectionTimer()

	for n.state == Follower {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case <-n.electionTimer.C:
			n.mu.Lock()
			if n.state == Follower {
				log.Printf("[Node %d] [Term %d] %s: Election timeout, becoming Candidate.\n", n.id, n.currentTerm, n.state)
				n.state = Candidate
			}
			n.mu.Unlock()
			return
		}
	}
}

func (n *Node) runCandidate() {
	n.mu.Lock()
	n.currentTerm++
	n.votedFor = n.id
	n.votesReceived = 1
	log.Printf("[Node %d] [Term %d] %s: Started election.\n", n.id, n.currentTerm, n.state)
	n.resetElectionTimer()
	n.mu.Unlock()

	// Send RequestVote RPCs to all peers immediately
	go n.sendRequestVotes()

	for n.state == Candidate {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case <-n.electionTimer.C:
			n.mu.Lock()
			if n.state == Candidate {
				// If election times out, start new election
				log.Printf("[Node %d] [Term %d] %s: Election timed out, starting new election.\n", n.id, n.currentTerm, n.state)
				n.currentTerm++
				n.votedFor = n.id
				n.votesReceived = 1
				n.resetElectionTimer()
				go n.sendRequestVotes()
			}
			n.mu.Unlock()
		}
	}
}

func (n *Node) runLeader() {
	n.mu.Lock()
	n.becomeLeader()
	n.mu.Unlock()

	heartbeatTicker := time.NewTicker(n.heartbeatTimeout)
	defer heartbeatTicker.Stop()

	// Start periodic quorum check
	quorumCheckTicker := time.NewTicker(500 * time.Millisecond)
	defer quorumCheckTicker.Stop()

	for n.state == Leader {
		select {
		case <-quorumCheckTicker.C:
			if !n.checkQuorum() {
				log.Printf("[Node %d] [Term %d] Lost quorum, stepping down\n",
					n.id, n.currentTerm)
				return
			}
		case <-heartbeatTicker.C:
			n.sendHeartbeats()
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case cmd := <-n.commandChan:
			n.mu.Lock()
			if n.state == Leader {
				if !n.checkQuorum() {
					log.Printf("[Node %d] [Term %d] Cannot process command, lost quorum\n",
						n.id, n.currentTerm)
					n.mu.Unlock()
					continue
				}
				newEntry := rpc.LogEntry{
					Term:    n.currentTerm,
					Index:   len(n.log),
					Command: cmd,
				}
				n.log = append(n.log, newEntry)
				log.Printf("[Node %d] [Term %d] Added new entry: %v\n",
					n.id, n.currentTerm, newEntry)
				n.sendAppendEntriesToAll()
			}
			n.mu.Unlock()
		}
	}
}

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
		reply.FollowerID = n.id
		go n.network.SendRPC(n.id, r.LeaderID, reply)
	case rpc.AppendEntriesReply:
		n.handleAppendEntriesReply(r)
	default:
		log.Printf("[Node %d] [Term %d] %s: Unknown RPC type received: %T\n", n.id, n.currentTerm, n.state, rpcMsg)
	}
}

func (n *Node) handleRequestVote(args rpc.RequestVoteArgs) rpc.RequestVoteReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	reply := rpc.RequestVoteReply{
		Term:        n.currentTerm,
		VoteGranted: false,
	}

	// If we're partitioned, don't grant votes
	if n.partitioned {
		log.Printf("[Node %d] Rejecting vote request from Node %d due to partition\n", n.id, args.CandidateID)
		return reply
	}

	// If the candidate's term is lower, reject
	if args.Term < n.currentTerm {
		return reply
	}

	// If we see a higher term, revert to follower
	if args.Term > n.currentTerm {
		n.becomeFollower(args.Term)
	}

	// Grant vote if we haven't voted for anyone else in this term
	// and the candidate's log is at least as up to date as ours
	if (n.votedFor == -1 || n.votedFor == args.CandidateID) &&
		n.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {
		n.votedFor = args.CandidateID
		reply.VoteGranted = true
		n.resetElectionTimer() // Reset election timer when granting vote
	}

	reply.Term = n.currentTerm
	return reply
}

func (n *Node) becomeFollower(term int) {
	if n.state != Follower {
		log.Printf("[Node %d] Converting to follower in term %d\n", n.id, term)
	}
	n.state = Follower
	n.currentTerm = term
	n.votedFor = -1
	n.resetElectionTimer()
}

func (n *Node) checkQuorum() bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return false
	}

	reachableNodes := 1 // Count self
	for _, peerID := range n.peers {
		if !n.network.IsPartitioned(n.id, peerID) {
			reachableNodes++
		}
	}

	hasQuorum := reachableNodes >= n.quorumSize
	if !hasQuorum {
		log.Printf("[Node %d] Lost quorum (reachable nodes: %d, required: %d)\n",
			n.id, reachableNodes, n.quorumSize)
		n.becomeFollower(n.currentTerm)
	}
	return hasQuorum
}

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

func (n *Node) handleRequestVoteReply(reply rpc.RequestVoteReply) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// If we've moved on from candidate state, ignore the reply
	if n.state != Candidate {
		return
	}

	if reply.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] Received vote reply with higher term %d, becoming follower\n",
			n.id, n.currentTerm, reply.Term)
		n.becomeFollower(reply.Term)
		return
	}

	// Only count votes from the current term
	if reply.Term == n.currentTerm && reply.VoteGranted {
		n.votesReceived++
		log.Printf("[Node %d] [Term %d] Received vote. Total votes: %d/%d\n",
			n.id, n.currentTerm, n.votesReceived, n.majority)

		if n.votesReceived >= n.majority {
			log.Printf("[Node %d] [Term %d] Received majority votes (%d/%d), becoming leader\n",
				n.id, n.currentTerm, n.votesReceived, n.majority)
			n.state = Leader
			n.becomeLeader()
		}
	}
}

func (n *Node) becomeLeader() {
	if n.state != Leader {
		return
	}

	// Initialize leader state
	for _, peerID := range n.peers {
		n.nextIndex[peerID] = len(n.log)
		n.matchIndex[peerID] = 0
	}

	// Send initial empty AppendEntries RPCs (heartbeats) to establish authority
	go n.sendHeartbeats()

	log.Printf("[Node %d] [Term %d] Successfully transitioned to Leader state\n",
		n.id, n.currentTerm)
}

func (n *Node) handleAppendEntries(args rpc.AppendEntriesArgs) rpc.AppendEntriesReply {
	n.mu.Lock()
	defer n.mu.Unlock()

	reply := rpc.AppendEntriesReply{
		Term:    n.currentTerm,
		Success: false,
	}

	// If we're partitioned, reject append entries
	if n.partitioned {
		log.Printf("[Node %d] Rejecting append entries from Node %d due to partition\n", n.id, args.LeaderID)
		return reply
	}

	// Update last heartbeat time
	n.lastHeartbeat = time.Now()

	// If leader's term is lower, reject
	if args.Term < n.currentTerm {
		return reply
	}

	// If we see a higher term, revert to follower
	if args.Term > n.currentTerm {
		n.becomeFollower(args.Term)
	}

	// Reset election timer since we got a valid AppendEntries
	n.resetElectionTimer()

	// Reject if log doesn't contain an entry at prevLogIndex with prevLogTerm
	if args.PrevLogIndex >= len(n.log) ||
		(args.PrevLogIndex >= 0 && n.log[args.PrevLogIndex].Term != args.PrevLogTerm) {
		return reply
	}

	// If existing entries conflict with new entries, delete them and all that follow
	newEntries := make([]rpc.LogEntry, 0)
	for i, entry := range args.Entries {
		if args.PrevLogIndex+1+i >= len(n.log) ||
			n.log[args.PrevLogIndex+1+i].Term != entry.Term {
			n.log = n.log[:args.PrevLogIndex+1+i]
			newEntries = args.Entries[i:]
			break
		}
	}

	// Append any new entries not already in the log
	if len(newEntries) > 0 {
		n.log = append(n.log, newEntries...)
	}

	// Update commit index if leader's is higher
	if args.LeaderCommit > n.commitIndex {
		n.commitIndex = min(args.LeaderCommit, len(n.log)-1)
		go n.applyCommittedEntries()
	}

	reply.Success = true
	return reply
}

func (n *Node) handleAppendEntriesReply(reply rpc.AppendEntriesReply) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if reply.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received AppendEntriesReply with higher term %d from Node %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, reply.Term, reply.FollowerID)
		n.currentTerm = reply.Term
		n.state = Follower
		n.votedFor = -1
		n.resetElectionTimer()
		return
	}

	if n.state == Leader && reply.Term == n.currentTerm {
		peerID := reply.FollowerID
		if reply.Success {
			newMatchIndex := len(n.log) - 1
			if newMatchIndex > n.matchIndex[peerID] {
				n.matchIndex[peerID] = newMatchIndex
				n.nextIndex[peerID] = newMatchIndex + 1
				log.Printf("[Node %d] [Term %d] %s: Node %d replicated up to index %d. NextIndex for Node %d: %d\n",
					n.id, n.currentTerm, n.state, peerID, n.matchIndex[peerID], peerID, n.nextIndex[peerID])
				n.checkCommitIndex()
			}
		} else {
			if n.nextIndex[peerID] > 1 {
				n.nextIndex[peerID]--
				log.Printf("[Node %d] [Term %d] %s: AppendEntries failed for Node %d. Decrementing nextIndex to %d.\n",
					n.id, n.currentTerm, n.state, peerID, n.nextIndex[peerID])
			}
		}
	}
}

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

func (n *Node) sendHeartbeats() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return
	}

	log.Printf("[Node %d] [Term %d] %s: Sending heartbeats.\n", n.id, n.currentTerm, n.state)
	n.sendAppendEntriesToAll()
}

func (n *Node) sendAppendEntriesToAll() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return
	}

	for _, peerID := range n.peers {
		prevLogIndex := n.nextIndex[peerID] - 1
		if prevLogIndex < 0 {
			prevLogIndex = 0
		}
		prevLogTerm := n.log[prevLogIndex].Term

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

		go n.network.SendRPC(n.id, peerID, args)
	}
	n.checkCommitIndex()
}

func (n *Node) checkCommitIndex() {
	for i := len(n.log) - 1; i > n.commitIndex; i-- {
		if n.log[i].Term == n.currentTerm {
			replicatedCount := 1
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
				return
			}
		}
	}
}

func (n *Node) applyCommittedEntries() {
	for n.lastApplied < n.commitIndex {
		n.lastApplied++
		entry := n.log[n.lastApplied]
		log.Printf("[Node %d] [Term %d] %s: Applying log entry %d: \"%s\"\n", n.id, n.currentTerm, n.state, entry.Index, entry.Command)
	}
}

func (n *Node) resetElectionTimer() {
	if n.electionTimer != nil {
		n.electionTimer.Stop()
	}
	n.electionTimeout = randomElectionTimeout()
	n.electionTimer = time.NewTimer(n.electionTimeout)
}

func randomElectionTimeout() time.Duration {
	return time.Duration(rand.Intn(maxElectionTimeoutMs-minElectionTimeoutMs)+minElectionTimeoutMs) * time.Millisecond
}
