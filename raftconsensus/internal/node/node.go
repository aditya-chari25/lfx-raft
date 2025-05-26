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

	electionTimer *time.Timer
	heartbeatTimer *time.Timer

	votesReceived int
	majority      int

	commandChan chan string
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
	}
	log.Printf("[Node %d] Initialized as %s, Term %d. Election Timeout: %v\n", node.id, node.state, node.currentTerm, node.electionTimeout)
	return node
}

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

	n.sendRequestVotes()

	for n.state == Candidate {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case <-n.electionTimer.C:
			n.mu.Lock()
			if n.state == Candidate {
				log.Printf("[Node %d] [Term %d] %s: Election timed out, restarting election.\n", n.id, n.currentTerm, n.state)
			}
			n.mu.Unlock()
			return
		}
	}
}

func (n *Node) runLeader() {
	n.mu.Lock()
	log.Printf("[Node %d] [Term %d] %s: Won election, becoming Leader.\n", n.id, n.currentTerm, n.state)
	for _, peerID := range n.peers {
		n.nextIndex[peerID] = len(n.log)
		n.matchIndex[peerID] = 0
	}
	n.mu.Unlock()

	n.heartbeatTimer = time.NewTimer(0)
	defer n.heartbeatTimer.Stop()

	for n.state == Leader {
		select {
		case rpcMsg := <-n.rpcChan:
			n.handleRPC(rpcMsg)
		case clientCommand := <-n.commandChan:
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
	reply := rpc.RequestVoteReply{Term: n.currentTerm, VoteGranted: false}

	log.Printf("[Node %d] [Term %d] %s: Received RequestVote from Node %d (Term %d, LastLogIndex %d, LastLogTerm %d)\n",
		n.id, n.currentTerm, n.state, args.CandidateID, args.Term, args.LastLogIndex, args.LastLogTerm)

	if args.Term < n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Denying vote to Node %d (stale term)\n", n.id, n.currentTerm, n.state, args.CandidateID)
		return reply
	}

	if args.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received RequestVote with higher term %d from Node %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, args.Term, args.CandidateID)
		n.currentTerm = args.Term
		n.state = Follower
		n.votedFor = -1
		n.resetElectionTimer()
	}

	if (n.votedFor == -1 || n.votedFor == args.CandidateID) && n.isLogUpToDate(args.LastLogIndex, args.LastLogTerm) {
		n.votedFor = args.CandidateID
		reply.VoteGranted = true
		n.resetElectionTimer()
		log.Printf("[Node %d] [Term %d] %s: Granted vote to Node %d.\n", n.id, n.currentTerm, n.state, args.CandidateID)
	} else {
		log.Printf("[Node %d] [Term %d] %s: Denying vote to Node %d (already voted or log not up-to-date).\n", n.id, n.currentTerm, n.state, args.CandidateID)
	}

	return reply
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
	if reply.Term > n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Received RequestVoteReply with higher term %d. Stepping down to Follower.\n",
			n.id, n.currentTerm, n.state, reply.Term)
		n.currentTerm = reply.Term
		n.state = Follower
		n.votedFor = -1
		n.resetElectionTimer()
		return
	}

	if n.state == Candidate && reply.Term == n.currentTerm {
		if reply.VoteGranted {
			n.votesReceived++
			log.Printf("[Node %d] [Term %d] %s: Received vote. Total votes: %d/%d\n", n.id, n.currentTerm, n.state, n.votesReceived, n.majority)
			if n.votesReceived >= n.majority {
				n.state = Leader
				if n.electionTimer != nil {
					n.electionTimer.Stop()
				}
			}
		}
	}
}

func (n *Node) handleAppendEntries(args rpc.AppendEntriesArgs) rpc.AppendEntriesReply {
	reply := rpc.AppendEntriesReply{Term: n.currentTerm, Success: false}

	log.Printf("[Node %d] [Term %d] %s: Received AppendEntries from Node %d (Term %d, PrevLogIndex %d, PrevLogTerm %d, Entries: %d)\n",
		n.id, n.currentTerm, n.state, args.LeaderID, args.Term, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries))

	if args.Term < n.currentTerm {
		log.Printf("[Node %d] [Term %d] %s: Denying AppendEntries from Node %d (stale term)\n", n.id, n.currentTerm, n.state, args.LeaderID)
		return reply
	}

	if args.Term >= n.currentTerm {
		if args.Term > n.currentTerm || n.state != Follower {
			log.Printf("[Node %d] [Term %d] %s: Received AppendEntries with higher/equal term %d. Stepping down to Follower.\n",
				n.id, n.currentTerm, n.state, args.Term)
			n.currentTerm = args.Term
			n.state = Follower
			n.votedFor = -1
		}
		n.resetElectionTimer()
	}

	if args.PrevLogIndex >= len(n.log) || n.log[args.PrevLogIndex].Term != args.PrevLogTerm {
		log.Printf("[Node %d] [Term %d] %s: Denying AppendEntries from Node %d (log inconsistency at index %d, term %d)\n",
			n.id, n.currentTerm, n.state, args.LeaderID, args.PrevLogIndex, args.PrevLogTerm)
		return reply
	}

	newEntriesStartIndex := 0
	for i, entry := range args.Entries {
		logIndex := args.PrevLogIndex + 1 + i
		if logIndex < len(n.log) {
			if n.log[logIndex].Term != entry.Term {
				n.log = n.log[:logIndex]
				log.Printf("[Node %d] [Term %d] %s: Log conflict at index %d. Truncating log.\n", n.id, n.currentTerm, n.state, logIndex)
				break
			}
		} else {
			newEntriesStartIndex = i
			break
		}
		newEntriesStartIndex = i + 1
	}

	if newEntriesStartIndex < len(args.Entries) {
		n.log = append(n.log, args.Entries[newEntriesStartIndex:]...)
		log.Printf("[Node %d] [Term %d] %s: Appended %d new entries from leader (Index: %d to %d).\n",
			n.id, n.currentTerm, n.state, len(args.Entries)-newEntriesStartIndex, args.PrevLogIndex+1+newEntriesStartIndex, len(n.log)-1)
	}

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