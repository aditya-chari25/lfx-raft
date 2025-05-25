Raft-Inspired Consensus Algorithm in Go
This project implements a basic distributed consensus algorithm inspired by Raft, built from scratch using Go's concurrency primitives (goroutines and channels). It simulates a cluster of nodes that achieve agreement on operations, handle leader elections, and demonstrate basic fault tolerance. A RESTful API, powered by the Gin framework, is integrated to allow external interaction with the cluster.

Goal
The primary goal is to provide a clear, runnable example of a consensus algorithm's core mechanics without relying on existing consensus libraries. This simulation focuses on:

Cluster Simulation: Representing 3-5 nodes as Go goroutines.

Leader Election: Implementing a timeout-based leader election mechanism.

Consensus Mechanism: Achieving agreement on log entries (operations).

Fault Tolerance: Handling simulated message drops.

API Interface: Exposing cluster status and command proposal via a REST API.

Architecture
The project is structured into several modules to promote modularity and separation of concerns:

raft-consensus/
├── cmd/
│   └── api/
│       └── main.go           # Main application entry point, initializes Raft cluster and Gin server.
├── internal/
│   ├── network/
│   │   └── network.go        # Simulates network communication between nodes (delays, message loss).
│   ├── node/
│   │   └── node.go           # Core Raft algorithm implementation for a single node (states, timers, RPC handlers).
│   ├── rpc/
│   │   └── rpc.go            # Defines RPC message structures (RequestVote, AppendEntries, LogEntry).
│   └── server/
│       └── server.go         # Implements the Gin-based HTTP API server for cluster interaction.
├── go.mod
├── go.sum
└── README.md

Module Breakdown:

cmd/api/main.go: This is the application's entry point. It orchestrates the setup of the simulated Raft cluster (nodes and network) and then starts the Gin HTTP API server. It also includes logic for graceful shutdown.

internal/node/node.go: Contains the complete logic for a single Raft node. This includes state management (Follower, Candidate, Leader), election timers, heartbeat mechanisms, and RPC handlers for RequestVote and AppendEntries. It also manages the replicated log and the commitment process.

internal/network/network.go: Provides a simplified simulation of a network. It allows nodes to send messages to each other via Go channels and can simulate network delays and message loss to test fault tolerance.

internal/rpc/rpc.go: Defines the data structures for all inter-node communication (RPCs) within the Raft protocol. This includes arguments and replies for vote requests and log append/heartbeat messages, as well as the structure for log entries.

internal/server/server.go: Implements the RESTful API using the Gin web framework. It exposes endpoints to query the Raft cluster's status (leader, node states) and to propose new commands to the cluster's current leader.

How to Run
Follow these steps to set up and run the Raft consensus simulation with the API server.

Prerequisites

Go: Version 1.16 or higher.

Git: For cloning the repository.

Setup Instructions

Clone the Repository (or create the project structure):

# If you're starting from scratch, create the directory and initialize a Go module:
mkdir raft-consensus
cd raft-consensus
go mod init raft-consensus

# Then create the directory structure:
mkdir -p cmd/api
mkdir -p internal/node
mkdir -p internal/network
mkdir -p internal/rpc
mkdir -p internal/server

Populate the Files:
Copy the code provided in the respective immersive documents into the following files:

internal/rpc/rpc.go

internal/network/network.go

internal/node/node.go

internal/server/server.go

cmd/api/main.go

Generate .env file:
Create a file named .env in the project's root directory (raft-consensus/) with the following content:

PORT=8080
APP_ENV=local

Install Dependencies:
Navigate to the project's root directory (raft-consensus/) and install the Gin framework:

go get github.com/gin-gonic/gin

Tidy Go Modules:
Ensure all module dependencies are correctly resolved:

go mod tidy

Running the Application

From the project's root directory (raft-consensus/), you can use the provided Makefile to run the application.

make run

You should see logs indicating the initialization of the Raft nodes, leader elections, heartbeats, and log replication. The API server will start listening on http://localhost:8080.

MakeFile

This project uses a Makefile to streamline common development tasks.

Run build make command with tests

make all

Build the application

make build

Run the application

make run

Live reload the application:

make watch

Run the test suite:

make test

Clean up binary from the last build:

make clean

Interacting with the API

You can use curl or any API client (like Postman or Insomnia) to interact with the running application.

Get Cluster Status:
This endpoint provides information about the current leader and the state (Follower, Candidate, Leader) and term of each node in the cluster.

curl http://localhost:8080/status

Example Response:

{
  "leader_id": 3,
  "leader_term": 5,
  "message": "Raft Cluster Status",
  "node_states": [
    {
      "id": 1,
      "state": "Follower",
      "term": 5
    },
    {
      "id": 2,
      "state": "Follower",
      "term": 5
    },
    {
      "id": 3,
      "state": "Leader",
      "term": 5
    },
    {
      "id": 4,
      "state": "Follower",
      "term": 5
    },
    {
      "id": 5,
      "state": "Follower",
      "term": 5
    }
  ]
}

Propose a Command:
This endpoint allows you to send a command to the Raft leader. The leader will then attempt to replicate this command across the cluster.

curl -X POST -H "Content-Type: application/json" -d '{"command": "set_value_X_to_100"}' http://localhost:8080/command

Example Response (Success):

{
  "command": "set_value_X_to_100",
  "message": "Command proposed to leader successfully"
}

Example Response (No Leader):

{
  "error": "No leader available. Please try again shortly."
}

Interpreting Logs

The console output will show detailed logs from each node, including:

Node state transitions (Follower, Candidate, Leader).

Election timeouts and new election starts.

RequestVote and AppendEntries RPCs sent and received.

Log entry appends and commitments.

Simulated message drops.

These logs are crucial for observing the consensus algorithm in action.

Fault Tolerance
The internal/network/network.go module simulates message loss (default 10%). This allows you to observe how the Raft algorithm handles unreliable network conditions by retransmitting messages and re-initiating elections if heartbeats or RPCs are consistently dropped.

SmartBFT Features:

Byzantine Validation: Raft is a Crash Fault Tolerant (CFT) algorithm, not Byzantine Fault Tolerant (BFT). Implementing BFT would require a different consensus algorithm (e.g., PBFT or a Raft variant with BFT properties).

View Changes: SmartBFT's view change mechanism is distinct from Raft's leader election.

Future Enhancements
Persistence: Store logs on disk so nodes can recover from crashes.

Client Redirection: Implement logic for followers to redirect client requests to the current leader.

Configuration Changes: Allow adding/removing nodes from the cluster dynamically.

Snapshotting: Periodically compact the log to prevent it from growing indefinitely.

More Robust RPC: Implement a more complete RPC system with request/response correlation and timeouts.

Testing: Add unit and integration tests for the Raft logic.

