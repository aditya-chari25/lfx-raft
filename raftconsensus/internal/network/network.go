// internal/network/network.go
package network

import (
	"log"
	"math/rand"
	"sync"
	"time"
)

// Network simulates the communication between nodes
type Network struct {
	mu                     sync.Mutex
	nodeChans              map[int]chan interface{}
	messageLossProbability float64
}

// NewNetwork creates a new simulated network
func NewNetwork(numNodes int, messageLossProbability float64) *Network {
	nodeChans := make(map[int]chan interface{})
	for i := 1; i <= numNodes; i++ {
		nodeChans[i] = make(chan interface{}, 100) // Buffered channel for each node
	}
	return &Network{
		nodeChans:              nodeChans,
		messageLossProbability: messageLossProbability,
	}
}

// SendRPC simulates sending an RPC from senderID to receiverID
// It returns nil in this simulation, as replies are handled asynchronously by the receiver's RPC handler.
func (n *Network) SendRPC(senderID, receiverID int, rpc interface{}) interface{} {
	n.mu.Lock()
	defer n.mu.Unlock()

	// Simulate message loss
	if rand.Float64() < n.messageLossProbability {
		log.Printf("[Network] Dropping RPC from Node %d to Node %d\n", senderID, receiverID)
		return nil // Simulate lost message
	}

	// Simulate network delay
	time.Sleep(time.Duration(rand.Intn(10)+1) * time.Millisecond) // 1-10ms delay

	receiverChan, ok := n.nodeChans[receiverID]
	if !ok {
		log.Printf("[Network] Error: Receiver Node %d not found.\n", receiverID)
		return nil
	}

	// Send the RPC to the receiver's channel
	select {
	case receiverChan <- rpc:
		// RPC sent successfully
	default:
		log.Printf("[Network] Warning: Node %d's channel is full, dropping RPC from Node %d.\n", receiverID, senderID)
		return nil // Simulate channel full / dropped message
	}

	return nil // Placeholder, actual replies are handled by the RPC handlers
}

// GetNodeChannel returns the channel for a specific node
func (n *Network) GetNodeChannel(nodeID int) chan interface{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nodeChans[nodeID]
}
