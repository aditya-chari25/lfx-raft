# Raft-Inspired Consensus Algorithm in Go

This project implements a basic distributed consensus algorithm inspired by Raft, built from scratch using Go's concurrency primitives (goroutines and channels). It simulates a cluster of nodes that achieve agreement on operations, handle leader elections, and demonstrate basic fault tolerance. A RESTful API, powered by the Gin framework, is integrated to allow external interaction with the cluster.

---

## Goal

The primary goal is to provide a clear, runnable example of a consensus algorithm's core mechanics without relying on existing consensus libraries. This simulation focuses on:

- **Cluster Simulation**: Representing 3-5 nodes as Go goroutines.
- **Leader Election**: Implementing a timeout-based leader election mechanism.
- **Consensus Mechanism**: Achieving agreement on log entries (operations).
- **Fault Tolerance**: Handling simulated message drops.
- **API Interface**: Exposing cluster status and command proposal via a REST API.

---

## Architecture

```
raft-consensus/
├── cmd/
│   └── api/
│       └── main.go           # Entry point, initializes Raft cluster and Gin server
├── internal/
│   ├── network/
│   │   └── network.go        # Simulates inter-node network (delays, message loss)
│   ├── node/
│   │   └── node.go           # Raft node logic (state transitions, elections, replication)
│   ├── rpc/
│   │   └── rpc.go            # RPC definitions (RequestVote, AppendEntries, LogEntry)
│   └── server/
│       └── server.go         # REST API using Gin
├── go.mod
├── go.sum
└── README.md
```

---

## How to Run

### Prerequisites

- Go: Version 1.16 or higher
- Git

### Setup Instructions

```bash
# Clone the repository or set up project structure
mkdir raft-consensus
cd raft-consensus
go mod init raft-consensus

# Create the directory structure
mkdir -p cmd/api
mkdir -p internal/node
mkdir -p internal/network
mkdir -p internal/rpc
mkdir -p internal/server
```

### Populate the Files

Place the respective source code into:

- `internal/rpc/rpc.go`
- `internal/network/network.go`
- `internal/node/node.go`
- `internal/server/server.go`
- `cmd/api/main.go`

### Generate .env file

```bash
# In the project root (raft-consensus/)
echo -e "PORT=8080\nAPP_ENV=local" > .env
```

### Install Dependencies

```bash
go get github.com/gin-gonic/gin
```

### Tidy Go Modules

```bash
go mod tidy
```

---

## Running the Application

```bash
make run
```

The API server will be accessible at:  
`http://localhost:8080`

---

## Makefile Commands

```bash
# Run build and tests
make all

# Build the application
make build

# Run the application
make run

# Live reload the application
make watch

# Run the test suite
make test

# Clean up binaries
make clean
```

---

## Interacting with the API

### Get Cluster Status

```bash
curl http://localhost:8080/status
```

#### Example Response

```json
{
    "leader_id": -1,
    "leader_term": 0,
    "message": "Raft Cluster Status",
    "node_states": [
        {
            "id": 1,
            "state": "Follower",
            "term": 126
        },
        {
            "id": 2,
            "state": "Follower",
            "term": 126
        },
        {
            "id": 3,
            "state": "Candidate",
            "term": 126
        },
        {
            "id": 4,
            "state": "Follower",
            "term": 126
        },
        {
            "id": 5,
            "state": "Follower",
            "term": 126
        }
    ]
}
```

---

### Network Partitions and Quorum Logic (How it improves the consensus)

### Network Partition Handling

The system implements sophisticated network partition simulation and handling:

```go
type Network struct {
    partitions map[int]map[int]bool  // Tracks partitioned node pairs
    // ... other fields
}
```

#### Key Features:
1. **Partition Creation and Healing**
   - Dynamic creation of network partitions between nodes
   - Ability to heal partitions for recovery testing
   - Partition-aware message delivery system

2. **Message Delivery**
   - Checks partition status before message delivery
   - Simulates realistic network conditions
   - Implements retry mechanism with exponential backoff

### Quorum Logic

The system maintains strict quorum requirements for leader election and operation:

#### 1. Quorum Checking
```go
func (n *Node) checkQuorum() bool {
    reachableNodes := 1 // Count self
    for _, peerID := range n.peers {
        if !n.network.IsPartitioned(n.id, peerID) {
            reachableNodes++
        }
    }
    return reachableNodes >= n.quorumSize
}
```

#### 2. Leader Responsibilities
- Periodic quorum verification (every 500ms)
- Steps down if quorum is lost
- Prevents split-brain scenarios

#### 3. Command Processing
- Verifies quorum before processing commands
- Ensures consistency in partitioned scenarios
- Implements retry logic for failed operations

### Fault Tolerance Mechanisms

1. **Split-Brain Prevention**
   - Active quorum monitoring
   - Partition-aware leader election
   - Automatic leader step-down when isolated

2. **Retry Mechanism**
   - Configurable retry attempts
   - Exponential backoff
   - Failure detection and logging

```go
maxRetries := 3
retryBackoff := 50 * time.Millisecond

for attempt := 0; attempt < maxRetries; attempt++ {
    if attempt > 0 {
        time.Sleep(retryBackoff * time.Duration(attempt))
    }
    // Attempt operation
}
```

3. **Safety Guarantees**
   - No split-brain scenarios
   - Consistent leadership in partitions
   - Safe command processing

### Improvements Over Basic Raft

1. **Enhanced Network Simulation**
   - Partition awareness
   - Message loss probability
   - Network delay simulation

2. **Robust Leader Election**
   - Partition-aware voting
   - Quorum verification
   - Term-based leader legitimacy

3. **Command Processing Safety**
   - Quorum verification before processing
   - Partition awareness
   - Automatic leader step-down

### Node Logs 
<img width="1043" alt="Screenshot 2025-05-26 at 10 05 21 AM" src="https://github.com/user-attachments/assets/27460dae-57e5-4eec-8edf-24b00165cae1" />

### Propose a Command

```bash
curl -X POST -H "Content-Type: application/json" -d '{"command": "set_value_X_to_100"}' http://localhost:8080/command
```

#### Example Response (Success)

```json
{
  "command": "set_value_X_to_100",
  "message": "Command proposed to leader successfully"
}

```

#### Example Response (No Leader)

```json
{
  "error": "No leader available. Please try again shortly."
}
```
---

## Fault Tolerance

The system simulates 10% message loss by default using `internal/network/network.go`. This demonstrates Raft's ability to maintain consensus even under unreliable network conditions.

---
