// cmd/api/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	// "os"
	"os/signal"
	"syscall"
	"time"
	"math/rand"
	"sync"

	"raftconsensus/internal/network"
	"raftconsensus/internal/node"
	"raftconsensus/internal/server" // Import the new server package
)

// Global configuration parameters for the simulation
const (
	numNodes             = 5
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

func main() {
	rand.Seed(time.Now().UnixNano()) // Initialize random seed

	log.SetFlags(log.Lmicroseconds | log.Lshortfile) // Add microseconds and file/line to logs

	// 1. Initialize the Raft Cluster
	net := network.NewNetwork(numNodes, messageLossProbability)
	raftNodes := make([]*node.Node, numNodes+1) // +1 because node IDs are 1-indexed

	majority := (numNodes / 2) + 1

	var wg sync.WaitGroup
	for i := 1; i <= numNodes; i++ {
		currentPeers := make([]int, 0)
		for j := 1; j <= numNodes; j++ {
			if i != j {
				currentPeers = append(currentPeers, j)
			}
		}
		n := node.NewNode(i, currentPeers, net, majority)
		raftNodes[i] = n
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Run() // Start the Raft node's main loop
		}()
	}

	fmt.Printf("Raft cluster simulation started with %d nodes. Majority required: %d\n", numNodes, majority)

	// 2. Initialize the Gin API Server
	apiServer := server.NewServer(raftNodes) // Pass the raftNodes to the API server

	// Create a done channel to signal when the shutdown is complete
	done := make(chan bool, 1)

	// Run graceful shutdown in a separate goroutine
	go gracefulShutdown(apiServer.HttpServer(), done) // Pass the *http.Server instance

	err := apiServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("http server error: %s", err))
	}

	// Wait for the graceful shutdown to complete
	<-done
	log.Println("Graceful shutdown complete.")

	// Optionally wait for all Raft nodes to finish (though they run indefinitely in this sim)
	// wg.Wait()
	log.Println("Raft simulation exiting.")
}
