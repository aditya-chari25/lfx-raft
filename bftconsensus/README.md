# **BFT Consensus Simulation: Core Logic and API Routes**

This document provides a concise overview of the Byzantine Fault Tolerant (BFT) consensus simulation's core mechanics and the functionalities offered by its API.

## **1\. BFT Consensus Logic (SmartBFT Inspired)**

The simulation models a network of nodes working to agree on a sequence of operations, even when some nodes are faulty or malicious. It uses a three-phase commit protocol for agreement and a view-change mechanism for leader election.

**Consensus Phases (First 3):**

1. **Request (Client to Primary):**  
   * A client (simulated via the API) sends an operation request to the current primary node.  
   * The primary assigns a unique sequence number and calculates a cryptographic digest for the operation.  
2. **Pre-Prepare (Primary to Replicas):**  
   * The primary broadcasts a `PRE-PREPARE` message to all other replicas, containing the operation's view, sequence number, payload, and digest.  
   * *Malicious Primary Behavior:* A malicious primary might intentionally send incorrect digests in `PRE-PREPARE`messages to disrupt consensus.  
3. **Prepare (Replicas to All):**  
   * Upon receiving a `PRE-PREPARE`, each replica validates it (checking the primary, view, and crucially, verifying the digest against the payload).  
   * If the `PRE-PREPARE` is valid, the replica broadcasts a `PREPARE` message to all other nodes. This indicates the replica is ready to commit the operation.  
   * *Quorum for (Prepared) State:* A node moves to the "(Prepared)" state for an operation once it has received 2F+1 matching `PREPARE` messages (including its own) for the same operation.

### **Leader Election (View Change Mechanism)**

* **Liveness Detection:** Replicas continuously monitor the primary node's activity. If a replica does not receive messages from the primary within a specified timeout period, it suspects the primary is unresponsive or faulty.  
* **`VIEW-CHANGE` Message:** A replica that suspects the primary initiates a view change by broadcasting a `VIEW-CHANGE`message, proposing a new, incremented view number.  
* **New Primary Selection:** When any node collects F+1 matching `VIEW-CHANGE` messages for the same new view, it determines the new primary for that view. The new primary is typically calculated deterministically (e.g., `newView % N`, where `N` is the total number of nodes).  
* **`NEW-VIEW` Message:** The newly designated primary for the new view then broadcasts a `NEW-VIEW` message to the network, signaling all nodes to transition to this new view. This message also helps ensure consistency of previously prepared operations across the view change.

## **2\. API Routes**

The Gin server provides HTTP API endpoints for interacting with and observing the BFT consensus simulation.

### **`POST /operations`**

* **Purpose:** Submits a new operation (client request) to the BFT consensus network for agreement.  
* **Method:** `POST`

### **`POST /nodes/:id/malicious`**

* **Purpose:** Toggles the "malicious" (Byzantine) status of a specific node in the simulation.  
* **Method:** `POST`

### **`GET /status`**

* **Purpose:** Retrieves the current operational state and internal logs of all nodes within the consensus network.  
* **Method:** `GET`

### **`POST /shutdown-nodes`**

* **Purpose:** Gracefully signals all simulated consensus nodes to shut down their internal processing goroutines. This does not stop the API server itself.  
* **Method:** `POST`

