// cmd/api/main.go
package main

import (
	"context"
	"log"
	"net/http"

	// "os"
	"math/rand"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"raftconsensus/internal/network"
	"raftconsensus/internal/node"
	"raftconsensus/internal/server" // Import the new server package
)

// Global configuration parameters for the simulation
const (
	numNodes               = 5
	messageLossProbability = 0.1 // 10% chance of message loss
)

func gracefulShutdown(apiServer *http.Server, done chan bool) {
	// Create context that listens for the interrupt signal from the OS.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Listen for the interrupt signal.
	<-ctx.Done()

	log.Println("shutting down gracefully, press Ctrl+C again to force")
	stop() // Allow Ctrl+C to force shutdown

	// The context is used to inform the server it has 5 seconds to finish
	// the request it is currently handling
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := apiServer.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown with error: %v", err)
	}

	log.Println("Server exiting")

	// Notify the main goroutine that the shutdown is complete
	done <- true
}

func waitForLeader(nodes []*node.Node, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, n := range nodes {
			if n == nil {
				continue
			}
			n.Mu().Lock()
			if n.State() == node.Leader {
				n.Mu().Unlock()
				log.Printf("Leader elected: Node %d\n", n.ID())
				return true
			}
			n.Mu().Unlock()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func main() {
	rand.Seed(time.Now().UnixNano()) // Initialize random seed

	log.SetFlags(log.Lmicroseconds | log.Lshortfile) // Add microseconds and file/line to logs

	// Initialize the Raft Cluster
	net := network.NewNetwork(numNodes, messageLossProbability)
	raftNodes := make([]*node.Node, numNodes+1)
	majority := (numNodes / 2) + 1

	log.Printf("Initializing Raft cluster with %d nodes (majority: %d)\n", numNodes, majority)

	// Initialize all nodes first
	for i := 1; i <= numNodes; i++ {
		currentPeers := make([]int, 0)
		for j := 1; j <= numNodes; j++ {
			if i != j {
				currentPeers = append(currentPeers, j)
			}
		}
		raftNodes[i] = node.NewNode(i, currentPeers, net, majority)
	}

	// Start all nodes
	var wg sync.WaitGroup
	for i := 1; i <= numNodes; i++ {
		wg.Add(1)
		go func(n *node.Node) {
			defer wg.Done()
			log.Printf("Starting node %d\n", n.ID())
			n.Run()
		}(raftNodes[i])
	}

	// Wait for leader election (up to 5 seconds)
	log.Println("Waiting for leader election...")
	if !waitForLeader(raftNodes, 5*time.Second) {
		log.Println("Warning: No leader elected within timeout period")
	}

	// Initialize and start the API server
	log.Println("Starting API server...")
	apiServer := server.NewServer(raftNodes)

	// Create a done channel to signal when the shutdown is complete
	done := make(chan bool, 1)

	// Run graceful shutdown in a separate goroutine
	go gracefulShutdown(apiServer.HttpServer(), done) // Pass the *http.Server instance

	log.Printf("API server listening on %s\n", apiServer.HttpServer().Addr)
	err := apiServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}

	// Wait for the graceful shutdown to complete
	<-done
	log.Println("Graceful shutdown complete.")

	// Optionally wait for all Raft nodes to finish (though they run indefinitely in this sim)
	// wg.Wait()
	log.Println("Raft simulation exiting.")
}
