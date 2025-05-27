// internal/server/server.go
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

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

// findLeader returns the current leader node and its term
func (s *Server) findLeader() (*node.Node, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var leader *node.Node
	var leaderTerm int

	for _, n := range s.nodes {
		if n == nil {
			continue
		}
		n.Mu().Lock()
		if n.State() == node.Leader {
			leader = n
			leaderTerm = n.CurrentTerm()
			n.Mu().Unlock()
			break
		}
		n.Mu().Unlock()
	}

	return leader, leaderTerm
}

// getStatus handler provides the current status of the Raft cluster.
func (s *Server) getStatus(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	leader, leaderTerm := s.findLeader()
	leaderID := -1
	if leader != nil {
		leaderID = leader.ID()
	}

	var nodeStates []map[string]interface{}

	for _, n := range s.nodes {
		if n == nil {
			continue
		}
		n.Mu().Lock()
		state := n.State()
		term := n.CurrentTerm()
		id := n.ID()
		nodeStates = append(nodeStates, map[string]interface{}{
			"id":    id,
			"state": state,
			"term":  term,
		})
		n.Mu().Unlock()
	}

	log.Printf("Cluster status - Leader ID: %d, Leader Term: %d, Total Nodes: %d\n",
		leaderID, leaderTerm, len(nodeStates))

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
		log.Printf("Invalid command request: %v\n", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Try to find the leader with multiple attempts
	var leader *node.Node
	var leaderTerm int

	// Try up to 3 times to find a leader with a short delay between attempts
	for i := 0; i < 3; i++ {
		leader, leaderTerm = s.findLeader()
		if leader != nil {
			break
		}
		if i < 2 { // Don't sleep on the last attempt
			time.Sleep(100 * time.Millisecond)
		}
	}

	if leader == nil {
		log.Printf("No leader available. Current node states:\n")
		for _, n := range s.nodes {
			if n == nil {
				continue
			}
			n.Mu().Lock()
			log.Printf("Node %d: State=%s, Term=%d\n", n.ID(), n.State(), n.CurrentTerm())
			n.Mu().Unlock()
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "No leader available. Please try again shortly.",
			"details": "Cluster is currently in leader election or transition.",
		})
		return
	}

	log.Printf("Forwarding command '%s' to leader (Node %d, Term %d)\n",
		request.Command, leader.ID(), leaderTerm)

	err := leader.ProposeCommand(request.Command)
	if err != nil {
		log.Printf("Failed to propose command: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Failed to propose command: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "Command proposed to leader successfully",
		"command":   request.Command,
		"leader_id": leader.ID(),
		"term":      leaderTerm,
	})
}
