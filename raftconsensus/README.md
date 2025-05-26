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
