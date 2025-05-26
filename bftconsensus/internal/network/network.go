package network

import (
	"log"
	"math/rand"
	"sync"
	"time"
)

// Define message types for the consensus protocol
const (
	MsgTypeRequest    = "REQUEST"
	MsgTypePrePrepare = "PRE_PREPARE"
	MsgTypePrepare    = "PREPARE"
	MsgTypeCommit     = "COMMIT"
	MsgTypeReply      = "REPLY"
	MsgTypeViewChange = "VIEW_CHANGE"
	MsgTypeNewView    = "NEW_VIEW"
)

// Message represents a message exchanged between nodes
type Message struct {
	SenderID    int         // ID of the node sending the message
	View        int         // Current view number
	SequenceNum int         // Sequence number of the operation
	Type        string      // Type of message (e.g., PRE_PREPARE, PREPARE)
	Payload     string      // The actual operation/data
	Digest      string      // Hash of (View, SequenceNum, Payload) for integrity
	Signature   string      // Mock signature for BFT validation (simplified)
	ExtraData   interface{} // For VIEW_CHANGE and NEW_VIEW messages
}

// NodeInterface defines the methods a node must implement to interact with the network
type NodeInterface interface {
	GetInbox() chan Message
	GetID() int // Added GetID for network to identify nodes
}

// Network simulates the communication layer between nodes
type Network struct {
	nodes map[int]NodeInterface
	mu    sync.Mutex
}

// NewNetwork creates a new simulated network
func NewNetwork() *Network {
	return &Network{
		nodes: make(map[int]NodeInterface),
	}
}

// RegisterNode adds a node to the network
func (n *Network) RegisterNode(node NodeInterface) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodes[node.GetID()] = node // Use GetID method
}

// SendMessage sends a message from sender to receiver
func (n *Network) SendMessage(senderID, receiverID int, msg Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if node, ok := n.nodes[receiverID]; ok {
		// Simulate network delay and potential message drop
		time.Sleep(time.Duration(rand.Intn(50)+10) * time.Millisecond) // 10-60ms delay
		if rand.Float32() < 0.05 { // 5% chance to drop message
			log.Printf("Network: Dropping message from %d to %d (Type: %s)", senderID, receiverID, msg.Type)
			return
		}
		select {
		case node.GetInbox() <- msg:
			// Message sent successfully
		default:
			log.Printf("Network: Node %d inbox full, dropping message from %d (Type: %s)", receiverID, senderID, msg.Type)
		}
	} else {
		log.Printf("Network: Node %d not found.", receiverID)
	}
}

// BroadcastMessage sends a message to all nodes except the sender
func (n *Network) BroadcastMessage(senderID int, msg Message) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for id := range n.nodes {
		if id != senderID {
			go n.SendMessage(senderID, id, msg) // Send concurrently
		}
	}
}