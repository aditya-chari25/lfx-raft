// internal/server/server.go
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"raftconsensus/internal/node" // Import the node package

	"github.com/gin-gonic/gin"
)

// Server holds the Gin engine and a reference to the Raft nodes.
type Server struct {
	router     *gin.Engine
	httpServer *http.Server
	nodes      []*node.Node // Reference to the Raft nodes
	mu         sync.RWMutex // Mutex for accessing nodes (especially for finding leader)
}

// NewServer creates and configures a new HTTP server with Gin.
// It takes the slice of Raft nodes to interact with the cluster.
func NewServer(raftNodes []*node.Node) *Server {
	r := gin.Default()

	s := &Server{
		router: r,
		nodes:  raftNodes,
	}

	// Define API routes
	r.GET("/status", s.getStatus)
	r.POST("/command", s.postCommand)

	s.httpServer = &http.Server{
		Addr:    ":8080", // Listen on port 8080
		Handler: r,
	}

	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	log.Printf("API Server listening on %s\n", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// HttpServer returns the underlying *http.Server instance.
func (s *Server) HttpServer() *http.Server {
	return s.httpServer
}

// getStatus handler provides the current status of the Raft cluster.
func (s *Server) getStatus(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	leaderID := -1
	var leaderTerm int
	var nodeStates []map[string]interface{}

	for _, n := range s.nodes {
		if n == nil {
			continue // Skip nil nodes if slice is 1-indexed and some indices are empty
		}
		// Access node's internal state (requires Node to expose a safe way to read state)
		// For now, we'll assume direct access for simplicity in this simulation.
		// In a real app, Node would have methods like GetState(), GetCurrentTerm(), IsLeader()
		n.Mu().Lock() // Lock the node's mutex to read its state safely
		state := n.State()
		term := n.CurrentTerm()
		id := n.ID()
		if state == node.Leader {
			leaderID = id
			leaderTerm = term
		}
		nodeStates = append(nodeStates, map[string]interface{}{
			"id":    id,
			"state": state,
			"term":  term,
		})
		n.Mu().Unlock()
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Raft Cluster Status",
		"leader_id":   leaderID,
		"leader_term": leaderTerm,
		"node_states": nodeStates,
	})
}

// postCommand handler allows clients to propose a new command to the Raft leader.
func (s *Server) postCommand(c *gin.Context) {
	var request struct {
		Command string `json:"command" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Find the current leader
	var currentLeader *node.Node
	for _, n := range s.nodes {
		if n == nil {
			continue
		}
		n.Mu().Lock() // Lock the node's mutex to read its state safely
		if n.State() == node.Leader {
			currentLeader = n
		}
		n.Mu().Unlock()
		if currentLeader != nil {
			break
		}
	}

	if currentLeader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "No leader available. Please try again shortly."})
		return
	}

	// Simulate sending the command to the leader.
	// In a real Raft, this would involve the leader appending to its log and replicating.
	// For this simulation, we'll call a method on the leader node.
	err := currentLeader.ProposeCommand(request.Command)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to propose command: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Command proposed to leader successfully", "command": request.Command})
}
