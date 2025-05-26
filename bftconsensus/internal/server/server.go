// internal/server/server.go
package server

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"bftconsensus/internal/network"
	"bftconsensus/internal/node"
)

// Server holds the Gin engine and the BFT consensus simulation components.
type Server struct {
	router *gin.Engine
	nodes  []*node.Node
	net    *network.Network
	mu     sync.Mutex // Mutex to protect access to nodes and network if needed by handlers
	nextOpID int // To assign unique IDs to operations
}

// NewServer initializes the Gin server and the BFT consensus simulation.
func NewServer() *http.Server {
	// Disable Gin debug output for cleaner logs
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	numNodes := 5 // Number of nodes in the cluster
	f := 1        // Max faulty nodes (N = 3F + 1 for BFT. So 5 = 3*1 + 2, this setup can tolerate 1 malicious node)
	if numNodes < 3*f+1 {
		log.Fatalf("Server: Number of nodes (%d) must be at least 3F+1 (%d) for BFT.", numNodes, 3*f+1)
	}

	net := network.NewNetwork()
	nodes := make([]*node.Node, numNodes)

	// Initialize and start nodes
	for i := 0; i < numNodes; i++ {
		nodes[i] = node.NewNode(i, numNodes, f, net)
		net.RegisterNode(nodes[i])
		go nodes[i].Start() // Start each node's goroutine
	}

	log.Printf("Server: Initialized %d BFT consensus nodes, tolerating F=%d Byzantine faults.", numNodes, f)

	s := &Server{
		router:   r,
		nodes:    nodes,
		net:      net,
		nextOpID: 0,
	}

	s.setupRoutes()

	// Return an http.Server instance
	return &http.Server{
		Addr:    ":8080", // Listen on port 8080
		Handler: s.router,
	}
}

// setupRoutes configures the API endpoints for the simulation.
func (s *Server) setupRoutes() {
	// Endpoint to submit a new operation to the consensus network
	s.router.POST("/operations", s.submitOperation)

	// Endpoint to set a node as malicious or honest
	s.router.POST("/nodes/:id/malicious", s.setNodeMalicious)

	// Endpoint to get the current status and logs of all nodes
	s.router.GET("/status", s.getNetworkStatus)

	// Endpoint to gracefully shut down all nodes in the simulation
	s.router.POST("/shutdown-nodes", s.shutdownNodes)
}

// submitOperation handles client requests to submit a new operation.
func (s *Server) submitOperation(c *gin.Context) {
	var request struct {
		Payload string `json:"payload" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s.mu.Lock()
	s.nextOpID++
	opID := s.nextOpID
	s.mu.Unlock()

	// Find the current primary node
	// In a real client, this would involve querying the network or retrying.
	// For simplicity, we'll assume node 0 is initially primary and then rely on view changes.
	primaryNode := s.nodes[s.nodes[0].GetPrimaryID(s.nodes[0].CurrentView())]

	log.Printf("API: Client submitting operation '%s' with ID %d to primary node %d", request.Payload, opID, primaryNode.ID)

	requestMsg := network.Message{
		SenderID:    -1, // Client
		View:        primaryNode.CurrentView(),
		SequenceNum: opID,
		Type:        network.MsgTypeRequest,
		Payload:     request.Payload,
	}

	// Send the request to the primary node
	s.net.SendMessage(-1, primaryNode.ID, requestMsg)

	c.JSON(http.StatusOK, gin.H{"message": "Operation submitted", "operation_id": opID, "target_primary_node": primaryNode.ID})
}

// setNodeMalicious handles requests to change a node's malicious status.
func (s *Server) setNodeMalicious(c *gin.Context) {
	nodeIDStr := c.Param("id")
	nodeID, err := strconv.Atoi(nodeIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid node ID"})
		return
	}

	var request struct {
		IsMalicious bool `json:"is_malicious"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if nodeID < 0 || nodeID >= len(s.nodes) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Node ID %d out of bounds (0-%d)", nodeID, len(s.nodes)-1)})
		return
	}

	s.nodes[nodeID].SetMalicious(request.IsMalicious)
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Node %d malicious status set to %t", nodeID, request.IsMalicious)})
}

// getNetworkStatus returns the current status and logs of all nodes.
func (s *Server) getNetworkStatus(c *gin.Context) {
	status := make(map[string]interface{})
	nodeStatuses := make([]map[string]interface{}, len(s.nodes))

	for i, n := range s.nodes {
		nodeStatuses[i] = map[string]interface{}{
			"id":          n.ID,
			"current_view": n.CurrentView(),
			"is_primary":  n.IsPrimary,
			"is_malicious": n.IsMalicious,
			"log":         n.GetLog(), // Get a copy of the log
		}
	}
	status["nodes"] = nodeStatuses
	c.JSON(http.StatusOK, status)
}

// shutdownNodes gracefully shuts down all simulated consensus nodes.
func (s *Server) shutdownNodes(c *gin.Context) {
	log.Println("API: Received request to shut down all consensus nodes.")
	for _, n := range s.nodes {
		n.Shutdown()
	}
	c.JSON(http.StatusOK, gin.H{"message": "All consensus nodes signaled to shut down."})
}

