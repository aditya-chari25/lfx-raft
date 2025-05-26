// internal/node/node.go
package node

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"bftconsensus/internal/network" // Import the network package
)

// Node represents a single participant in the consensus cluster
type Node struct {
	ID            int               // Unique identifier for the node
	NumNodes      int               // Total number of nodes in the cluster
	F             int               // Maximum number of faulty (Byzantine) nodes tolerated (N = 3F + 1)
	currentView   int               // Current view number (private)
	IsPrimary     bool              // True if this node is the primary (leader)
	IsMalicious   bool              // True if this node is exhibiting Byzantine behavior
	Log           []string          // Log of agreed-upon operations
	mu            sync.Mutex        // Mutex to protect node state
	inbox         chan network.Message      // Channel for incoming messages
	network       *network.Network  // Reference to the simulated network
	requestSeqNum int               // Next sequence number for client requests
	lastHeartbeat time.Time         // Timestamp of the last message from the primary (for replicas)

	// State for consensus phases
	prePrepareMsgs map[int]network.Message                    // Stores the PrePrepare message for a sequence number
	prepareMsgs    map[int]map[int]network.Message            // Stores Prepare messages: [sequenceNum][senderID]Message
	commitMsgs     map[int]map[int]network.Message            // Stores Commit messages: [sequenceNum][senderID]Message
	prepared       map[int]bool                       // True if (pre-prepared, prepared) for a sequence number
	committed      map[int]bool                       // True if (prepared, committed) for a sequence number
	viewChangeMsgs map[int]map[int]network.Message            // Stores ViewChange messages: [newView][senderID]Message
	newViewMsgs    map[int]map[int]network.Message            // Stores NewView messages: [newView][senderID]Message
	shutdownChan   chan struct{}                      // Channel to signal shutdown
}

// NewNode creates and initializes a new Node
func NewNode(id, numNodes, f int, net *network.Network) *Node {
	return &Node{
		ID:             id,
		NumNodes:       numNodes,
		F:              f,
		currentView:    0, // Start in view 0
		IsPrimary:      (id == 0), // Node 0 is initial primary
		IsMalicious:    false,
		Log:            []string{},
		inbox:          make(chan network.Message, 100), // Buffered channel for messages
		network:        net,
		requestSeqNum:  0,
		lastHeartbeat:  time.Now(),
		prePrepareMsgs: make(map[int]network.Message),
		prepareMsgs:    make(map[int]map[int]network.Message),
		commitMsgs:     make(map[int]map[int]network.Message),
		prepared:       make(map[int]bool),
		committed:      make(map[int]bool),
		viewChangeMsgs: make(map[int]map[int]network.Message),
		newViewMsgs:    make(map[int]map[int]network.Message),
		shutdownChan:   make(chan struct{}),
	}
}

// Start runs the main event loop for the node
func (n *Node) Start() {
	log.Printf("Node %d: Starting. Initial Primary: %t, View: %d", n.ID, n.IsPrimary, n.CurrentView())

	ticker := time.NewTicker(500 * time.Millisecond) // For primary heartbeats / replica timeouts
	defer ticker.Stop()

	for {
		select {
		case msg := <-n.inbox:
			n.handleMessage(msg)
		case <-ticker.C:
			n.checkPrimaryLiveness()
		case <-n.shutdownChan:
			log.Printf("Node %d: Shutting down.", n.ID)
			return
		}
	}
}

// Shutdown signals the node to stop its operation
func (n *Node) Shutdown() {
	close(n.shutdownChan)
}

// SetMalicious sets the node's malicious status
func (n *Node) SetMalicious(status bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.IsMalicious = status
	if status {
		log.Printf("Node %d: *** HAS BECOME MALICIOUS! ***\n", n.ID)
	} else {
		log.Printf("Node %d: *** IS NOW HONEST! ***\n", n.ID)
	}
}

// GetInbox returns the node's inbox channel. This is used by the Network to send messages.
func (n *Node) GetInbox() chan network.Message {
	return n.inbox
}

// GetID returns the ID of the node.
func (n *Node) GetID() int {
	return n.ID
}

// CurrentView returns the current view number of the node.
func (n *Node) CurrentView() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.currentView
}

// GetPrimaryID returns the ID of the current primary node based on the view
func (n *Node) GetPrimaryID(view int) int {
	return view % n.NumNodes
}

// IsCurrentPrimary checks if this node is the primary for the current view
func (n *Node) IsCurrentPrimary() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ID == n.GetPrimaryID(n.currentView)
}

// GetLog returns a copy of the node's log.
func (n *Node) GetLog() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	logCopy := make([]string, len(n.Log))
	copy(logCopy, n.Log)
	return logCopy
}

// generateDigest creates a SHA256 hash of the relevant message fields
func generateDigest(view, seqNum int, payload string) string {
	data := fmt.Sprintf("%d-%d-%s", view, seqNum, payload)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// handleMessage processes an incoming message
func (n *Node) handleMessage(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// If message is from an older view, ignore it unless it's a view change request
	if msg.View < n.currentView && msg.Type != network.MsgTypeViewChange {
		log.Printf("Node %d: Ignoring old message from view %d (current view %d) Type: %s", n.ID, msg.View, n.currentView, msg.Type)
		return
	}

	// Update last heartbeat from primary if message is from current primary in current view
	if msg.SenderID == n.GetPrimaryID(n.currentView) && msg.View == n.currentView {
		n.lastHeartbeat = time.Now()
	}

	// Malicious behavior injection (simplified)
	if n.IsMalicious {
		// A malicious node might drop messages, send invalid ones, or delay them.
		// For simplicity, let's say a malicious node sometimes sends invalid digests or just drops messages.
		if rand.Float32() < 0.3 { // 30% chance to drop a message
			log.Printf("Node %d (MALICIOUS): Dropping message from %d (Type: %s)", n.ID, msg.SenderID, msg.Type)
			return
		}
		if msg.Type == network.MsgTypePrePrepare && n.IsCurrentPrimary() {
			// Malicious primary sends incorrect digest to some replicas
			if rand.Float32() < 0.5 {
				log.Printf("Node %d (MALICIOUS Primary): Sending incorrect digest for seq %d", n.ID, msg.SequenceNum)
				msg.Digest = "INVALID_DIGEST_" + msg.Digest
			}
		}
	}

	log.Printf("Node %d: Received %s from %d (View: %d, Seq: %d, Payload: %s)",
		n.ID, msg.Type, msg.SenderID, msg.View, msg.SequenceNum, msg.Payload)

	switch msg.Type {
	case network.MsgTypeRequest:
		if n.IsCurrentPrimary() {
			n.handleRequest(msg)
		} else {
			log.Printf("Node %d: Not primary, forwarding REQUEST to primary %d", n.ID, n.GetPrimaryID(n.currentView))
			n.network.SendMessage(n.ID, n.GetPrimaryID(n.currentView), msg)
		}
	case network.MsgTypePrePrepare:
		n.handlePrePrepare(msg)
	case network.MsgTypePrepare:
		n.handlePrepare(msg)
	case network.MsgTypeCommit:
		n.handleCommit(msg)
	case network.MsgTypeViewChange:
		n.handleViewChange(msg)
	case network.MsgTypeNewView:
		n.handleNewView(msg)
	}
}

// handleRequest processes a client request (only by primary)
func (n *Node) handleRequest(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.IsCurrentPrimary() {
		log.Printf("Node %d: Error - received REQUEST but not primary.", n.ID)
		return
	}

	n.requestSeqNum++
	seqNum := n.requestSeqNum
	digest := generateDigest(n.currentView, seqNum, msg.Payload)

	// Store PrePrepare message
	n.prePrepareMsgs[seqNum] = network.Message{
		SenderID:    n.ID,
		View:        n.currentView,
		SequenceNum: seqNum,
		Type:        network.MsgTypePrePrepare,
		Payload:     msg.Payload,
		Digest:      digest,
		Signature:   "mock_sig_preprepare", // Mock signature
	}

	log.Printf("Node %d (Primary): Broadcasting PRE_PREPARE for seq %d, payload '%s'", n.ID, seqNum, msg.Payload)
	n.network.BroadcastMessage(n.ID, n.prePrepareMsgs[seqNum])
}

// handlePrePrepare processes a PrePrepare message (by replicas)
func (n *Node) handlePrePrepare(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check if from current primary and correct view
	if msg.SenderID != n.GetPrimaryID(msg.View) || msg.View != n.currentView {
		log.Printf("Node %d: Invalid PRE_PREPARE (sender %d, view %d). Expected primary %d, view %d",
			n.ID, msg.SenderID, msg.View, n.GetPrimaryID(n.currentView), n.currentView)
		return
	}

	// Validate digest
	expectedDigest := generateDigest(msg.View, msg.SequenceNum, msg.Payload)
	if msg.Digest != expectedDigest {
		log.Printf("Node %d: Invalid PRE_PREPARE digest for seq %d. Expected %s, Got %s. Triggering ViewChange.",
			n.ID, msg.SequenceNum, expectedDigest, msg.Digest)
		n.startViewChange(n.currentView + 1)
		return
	}

	if _, ok := n.prePrepareMsgs[msg.SequenceNum]; ok {
		log.Printf("Node %d: Already received PRE_PREPARE for seq %d. Ignoring.", n.ID, msg.SequenceNum)
		return
	}

	n.prePrepareMsgs[msg.SequenceNum] = msg
	log.Printf("Node %d: Accepted PRE_PREPARE for seq %d. Broadcasting PREPARE.", n.ID, msg.SequenceNum)

	// Broadcast PREPARE
	prepareMsg := network.Message{
		SenderID:    n.ID,
		View:        n.currentView,
		SequenceNum: msg.SequenceNum,
		Type:        network.MsgTypePrepare,
		Digest:      msg.Digest,
		Signature:   "mock_sig_prepare", // Mock signature
	}
	n.network.BroadcastMessage(n.ID, prepareMsg)
}

// handlePrepare processes a Prepare message
func (n *Node) handlePrepare(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check if from current view
	if msg.View != n.currentView {
		log.Printf("Node %d: Ignoring PREPARE from old view %d (current %d)", n.ID, msg.View, n.currentView)
		return
	}

	// Ensure we have a PrePrepare for this sequence number and digest
	if _, ok := n.prePrepareMsgs[msg.SequenceNum]; !ok || n.prePrepareMsgs[msg.SequenceNum].Digest != msg.Digest {
		log.Printf("Node %d: Received PREPARE for unknown/mismatched seq %d or digest %s. Ignoring.", n.ID, msg.SequenceNum, msg.Digest)
		return
	}

	if _, ok := n.prepareMsgs[msg.SequenceNum]; !ok {
		n.prepareMsgs[msg.SequenceNum] = make(map[int]network.Message)
	}
	n.prepareMsgs[msg.SequenceNum][msg.SenderID] = msg


	quorumSize := 2*n.F + 1 // Number of agreeing messages needed for (prepared)
	if len(n.prepareMsgs[msg.SequenceNum]) >= quorumSize && !n.prepared[msg.SequenceNum] {
		n.prepared[msg.SequenceNum] = true
		log.Printf("Node %d: (Prepared) for seq %d. Broadcasting COMMIT.", n.ID, msg.SequenceNum)

		// Broadcast COMMIT
		commitMsg := network.Message{
			SenderID:    n.ID,
			View:        n.currentView,
			SequenceNum: msg.SequenceNum,
			Type:        network.MsgTypeCommit,
			Digest:      msg.Digest,
			Signature:   "mock_sig_commit", // Mock signature
		}
		n.network.BroadcastMessage(n.ID, commitMsg)
	}
}

// handleCommit processes a Commit message
func (n *Node) handleCommit(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Check if from current view
	if msg.View != n.currentView {
		log.Printf("Node %d: Ignoring COMMIT from old view %d (current %d)", n.ID, msg.View, n.currentView)
		return
	}

	// Ensure we have a PrePrepare for this sequence number and digest
	if _, ok := n.prePrepareMsgs[msg.SequenceNum]; !ok || n.prePrepareMsgs[msg.SequenceNum].Digest != msg.Digest {
		log.Printf("Node %d: Received COMMIT for unknown/mismatched seq %d or digest %s. Ignoring.", n.ID, msg.SequenceNum, msg.Digest)
		return
	}

	if _, ok := n.commitMsgs[msg.SequenceNum]; !ok {
		n.commitMsgs[msg.SequenceNum] = make(map[int]network.Message)
	}
	n.commitMsgs[msg.SequenceNum][msg.SenderID] = msg

	// Check for (committed-local) condition: 2F+1 matching COMMIT messages
	quorumSize := 2*n.F + 1 // Number of agreeing messages needed for (committed-local)
	if len(n.commitMsgs[msg.SequenceNum]) >= quorumSize && !n.committed[msg.SequenceNum] {
		n.committed[msg.SequenceNum] = true
		log.Printf("Node %d: (Committed-local) for seq %d, payload '%s'. Appending to log.",
			n.ID, msg.SequenceNum, n.prePrepareMsgs[msg.SequenceNum].Payload)

		// Append to log and reply to client (simplified)
		n.Log = append(n.Log, n.prePrepareMsgs[msg.SequenceNum].Payload)
		log.Printf("Node %d: Current Log: %v", n.ID, n.Log)

		// Send REPLY (simplified, not to actual client)
		replyMsg := network.Message{
			SenderID:    n.ID,
			View:        n.currentView,
			SequenceNum: msg.SequenceNum,
			Type:        network.MsgTypeReply,
			Payload:     n.prePrepareMsgs[msg.SequenceNum].Payload,
			Digest:      msg.Digest,
		}
		log.Printf("Node %d: Sending REPLY for seq %d.", n.ID, msg.SequenceNum)
		// In a real system, reply to the client that sent the initial request
		// For simulation, we just log it.
		n.network.SendMessage(n.ID, 0, replyMsg) // Send to node 0 as a mock client
	}
}

// checkPrimaryLiveness checks if the primary is alive (for replicas) or sends heartbeats (for primary)
func (n *Node) checkPrimaryLiveness() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.IsCurrentPrimary() {
		return
	}

	// Replica checks primary liveness
	if time.Since(n.lastHeartbeat) > 3*time.Second { // If no message from primary for 3 seconds
		log.Printf("Node %d: Primary %d in view %d appears unresponsive. Initiating VIEW_CHANGE.",
			n.ID, n.GetPrimaryID(n.currentView), n.currentView)
		n.startViewChange(n.currentView + 1)
	}
}

// startViewChange initiates a view change process
func (n *Node) startViewChange(newView int) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if newView <= n.currentView {
		log.Printf("Node %d: Already in view %d or higher. Not starting view change to %d.", n.ID, n.currentView, newView)
		return
	}

	log.Printf("Node %d: Starting VIEW_CHANGE to new view %d", n.ID, newView)
	n.currentView = newView // Immediately move to the new view locally

	// Reset consensus state for the new view
	n.prePrepareMsgs = make(map[int]network.Message)
	n.prepareMsgs = make(map[int]map[int]network.Message)
	n.commitMsgs = make(map[int]map[int]network.Message)
	n.prepared = make(map[int]bool)
	n.committed = make(map[int]bool)

	// Collect messages for the new view (simplified: just send current state)
	// In a real BFT, this would include the set of (prepared) messages from the previous view
	// to ensure consistency in the new view.
	viewChangeMsg := network.Message{
		SenderID:    n.ID,
		View:        newView,
		Type:        network.MsgTypeViewChange,
		ExtraData:   "Proof of liveness/prepared messages from previous view", // Mocked
	}
	n.network.BroadcastMessage(n.ID, viewChangeMsg)
}

// handleViewChange processes a ViewChange message
func (n *Node) handleViewChange(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if msg.View <= n.currentView {
		log.Printf("Node %d: Ignoring VIEW_CHANGE for old/current view %d from %d", n.ID, msg.View, msg.SenderID)
		return
	}

	if _, ok := n.viewChangeMsgs[msg.View]; !ok {
		n.viewChangeMsgs[msg.View] = make(map[int]network.Message)
	}
	n.viewChangeMsgs[msg.View][msg.SenderID] = msg

	// Check for quorum of ViewChange messages for the new view
	// Need F+1 view-change messages to trigger new view
	quorumSize := n.F + 1
	if len(n.viewChangeMsgs[msg.View]) >= quorumSize {
		log.Printf("Node %d: Received %d VIEW_CHANGE messages for view %d. Quorum reached.",
			n.ID, len(n.viewChangeMsgs[msg.View]), msg.View)

		// If this node is the new primary, it initiates NEW_VIEW
		if n.ID == n.GetPrimaryID(msg.View) {
			n.currentView = msg.View
			n.IsPrimary = true
			log.Printf("Node %d (New Primary): Initiating NEW_VIEW for view %d", n.ID, n.currentView)

			// In a real BFT, the new primary would construct a NewView message
			// based on the collected ViewChange messages, ensuring consistency.
			// Here, we just send a mock NewView.
			newViewMsg := network.Message{
				SenderID:    n.ID,
				View:        n.currentView,
				Type:        network.MsgTypeNewView,
				ExtraData:   "Summary of prepared messages from previous view", // Mocked
			}
			n.network.BroadcastMessage(n.ID, newViewMsg)
		}
	}
}

// handleNewView processes a NewView message
func (n *Node) handleNewView(msg network.Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if msg.View < n.currentView {
		log.Printf("Node %d: Ignoring NEW_VIEW for old view %d from %d", n.ID, msg.View, msg.SenderID)
		return
	}

	// Check if from the new primary
	if msg.SenderID != n.GetPrimaryID(msg.View) {
		log.Printf("Node %d: Invalid NEW_VIEW from %d. Expected primary %d for view %d",
			n.ID, msg.SenderID, n.GetPrimaryID(msg.View), msg.View)
		return
	}

	// Accept the new view
	n.currentView = msg.View
	n.IsPrimary = (n.ID == n.GetPrimaryID(n.currentView))
	n.lastHeartbeat = time.Now() // Reset heartbeat timer

	log.Printf("Node %d: Accepted NEW_VIEW for view %d. I am Primary: %t", n.ID, n.currentView, n.IsPrimary)

	// Clear view change messages for this view once new view is established
	delete(n.viewChangeMsgs, n.currentView)
}