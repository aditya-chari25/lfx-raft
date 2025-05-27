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
	partitions             map[int]map[int]bool // Maps node pairs that are partitioned
	maxRetries             int
	retryBackoff           time.Duration
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
		partitions:             make(map[int]map[int]bool),
		maxRetries:             3,
		retryBackoff:           50 * time.Millisecond,
	}
}

// CreatePartition simulates a network partition between two nodes
func (n *Network) CreatePartition(node1, node2 int) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.partitions[node1]; !exists {
		n.partitions[node1] = make(map[int]bool)
	}
	if _, exists := n.partitions[node2]; !exists {
		n.partitions[node2] = make(map[int]bool)
	}

	n.partitions[node1][node2] = true
	n.partitions[node2][node1] = true
	log.Printf("[Network] Created partition between Node %d and Node %d\n", node1, node2)
}

// HealPartition removes a network partition between two nodes
func (n *Network) HealPartition(node1, node2 int) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if partition1, exists := n.partitions[node1]; exists {
		delete(partition1, node2)
	}
	if partition2, exists := n.partitions[node2]; exists {
		delete(partition2, node1)
	}
	log.Printf("[Network] Healed partition between Node %d and Node %d\n", node1, node2)
}

// IsPartitioned checks if two nodes are partitioned
func (n *Network) IsPartitioned(node1, node2 int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	if partition, exists := n.partitions[node1]; exists {
		return partition[node2]
	}
	return false
}

// SendRPC simulates sending an RPC from senderID to receiverID with retries
func (n *Network) SendRPC(senderID, receiverID int, rpc interface{}) interface{} {
	for attempt := 0; attempt < n.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(n.retryBackoff * time.Duration(attempt))
		}

		n.mu.Lock()
		// Check for partition
		if n.IsPartitioned(senderID, receiverID) {
			n.mu.Unlock()
			log.Printf("[Network] Nodes %d and %d are partitioned, message dropped\n", senderID, receiverID)
			continue
		}

		// Simulate message loss
		if rand.Float64() < n.messageLossProbability {
			n.mu.Unlock()
			log.Printf("[Network] Attempt %d: Dropping RPC from Node %d to Node %d\n", attempt+1, senderID, receiverID)
			continue
		}

		// Simulate network delay
		time.Sleep(time.Duration(rand.Intn(10)+1) * time.Millisecond)

		receiverChan, ok := n.nodeChans[receiverID]
		if !ok {
			n.mu.Unlock()
			log.Printf("[Network] Error: Receiver Node %d not found.\n", receiverID)
			return nil
		}

		// Send the RPC to the receiver's channel
		select {
		case receiverChan <- rpc:
			n.mu.Unlock()
			return nil // Successfully sent
		default:
			n.mu.Unlock()
			log.Printf("[Network] Attempt %d: Node %d's channel is full, retrying...\n", attempt+1, receiverID)
			continue
		}
	}

	log.Printf("[Network] Failed to send RPC from Node %d to Node %d after %d attempts\n", senderID, receiverID, n.maxRetries)
	return nil
}

// GetNodeChannel returns the channel for a specific node
func (n *Network) GetNodeChannel(nodeID int) chan interface{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nodeChans[nodeID]
}
