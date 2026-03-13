# Distributed Coordination System

A Go-based distributed coordination system implementing four fundamental distributed systems algorithms - **Bully Leader Election**, **Ricart-Agrawala Mutual Exclusion**, **Wait-For Graph Deadlock Detection**, and **Simple Majority-Vote Consensus** - applied to a distributed ticket booking system. All inter-node communication uses Go's native **`net/rpc`** over TCP.

---

## Algorithms Implemented

### 1. Bully Leader Election
**File:** `internal/election/election.go`

| Concept | Implementation |
|---------|---------------|
| Trigger | Heartbeat timeout detection |
| Election | Node sends ELECTION to all higher-ID nodes |
| Answer | Higher-ID node responds with ANSWER, starts own election |
| Victory | If no ANSWER received, node broadcasts COORDINATOR |
| Winner | The highest-ID alive node always becomes leader |

```
Node 0 (low ID)       Node 1 (mid ID)       Node 3 (high ID)
    |                      |                      |
    +---ELECTION---------->|                      |
    +---ELECTION--------------------------------->|
    |                      |                      |
    |<--ANSWER-------------|                      |
    |                      +---ELECTION---------->|
    |                      |                      |
    |                      |<--ANSWER-------------|
    |                      |                      |
    |<--COORDINATOR--------------------------------|
    |                      |<--COORDINATOR--------|
    |                      |                      |
    |            Node 3 is the LEADER             |
```

### 2. Ricart-Agrawala Mutual Exclusion
**File:** `internal/lock/lock.go`

| Concept | Implementation |
|---------|---------------|
| Request | Broadcast REQUEST(timestamp, nodeID) to all peers |
| Grant | Wait for REPLY from every peer before entering CS |
| Priority | Lower timestamp wins; ties broken by lower nodeID |
| Deferred | Replies deferred while node is in CS or has higher priority |
| Release | On CS exit, send all deferred REPLY messages |

```
Node 0 (wants CS)     Node 1 (idle)     Node 2 (idle)
    |                     |                 |
    +---REQUEST---------->|                 |
    +---REQUEST--------------------------->|
    |                     |                 |
    |<--REPLY-------------|                 |
    |<--REPLY-------------------------------|
    |                     |                 |
    | [ALL REPLIES: ENTER CS]               |
    |                     |                 |
    | [EXIT CS: send deferred replies]      |
```

### 3. Deadlock Detection (Wait-For Graph)
**File:** `internal/deadlock/deadlock.go`

| Concept | Implementation |
|---------|---------------|
| Graph | Wait-For Graph: edge A-->B means A waits for resource held by B |
| Detection | DFS-based cycle detection on the WFG |
| Collection | Leader sends PROBE to all; each responds with REPORT |
| Resolution | Lowest-ID node in cycle is selected as victim |
| Simulation | `simulate_deadlock` command creates artificial circular wait |

```
Leader probes all nodes:
    Leader --PROBE--> Node 0, Node 1, Node 2, Node 3

Each node reports its wait state:
    Node 0 --REPORT--> Leader  (holds R0, waits for R1)
    Node 1 --REPORT--> Leader  (holds R1, waits for R2)
    Node 2 --REPORT--> Leader  (holds R2, waits for R0)

Leader builds WFG:
    Node 0 --> Node 1 --> Node 2 --> Node 0  (CYCLE = DEADLOCK!)

Resolution: Force Node 0 (lowest ID) to release
```

### 4. Simple Majority-Vote Consensus
**File:** `internal/consensus/consensus.go`

| Concept | Implementation |
|---------|---------------|
| Propose | Leader broadcasts PROPOSE to all followers |
| Vote | Each follower sends ACCEPT or REJECT |
| Commit | If majority accepts, leader broadcasts COMMIT |
| Abort | If not enough votes, leader broadcasts ABORT |
| Apply | On COMMIT, all nodes apply the change to local state |

```
Leader               Follower 1        Follower 2       Follower 3
  |                      |                  |                |
  +---PROPOSE----------->|                  |                |
  +---PROPOSE------------------------------>|                |
  +---PROPOSE----------------------------------------------->|
  |                      |                  |                |
  |<--ACCEPT-------------|                  |                |
  |<--ACCEPT--------------------------------|                |
  |<--ACCEPT---------------------------------------------|
  |                      |                  |                |
  |  (majority = 3/4 --> COMMITTED)         |                |
  |                      |                  |                |
  +---COMMIT------------>|                  |                |
  +---COMMIT------------------------------>|                |
  +---COMMIT----------------------------------------------->|
```

---

## Project Structure

```
+-- go.mod
+-- README.md
+-- internal/
|   +-- types/types.go                  # Shared types & message definitions
|   +-- transport/transport.go          # net/rpc service, server & client
|   +-- election/election.go            # Algorithm 1: Bully Leader Election
|   +-- lock/lock.go                    # Algorithm 2: Ricart-Agrawala Mutual Exclusion
|   +-- deadlock/deadlock.go            # Algorithm 3: Wait-For Graph Deadlock Detection
|   +-- consensus/consensus.go          # Algorithm 4: Simple Majority-Vote Consensus
|   +-- coordinator/coordinator.go      # Coordination ensemble node
|   +-- booking/booking.go              # Seat booking database
+-- cmd/
|   +-- coordinator/main.go             # Coordinator CLI
+-- scripts/
    +-- launch_cluster.sh               # Launch 4-node cluster instructions
```

---

## Quick Start

### Prerequisites
- **Go 1.21+** installed (https://go.dev/dl/)

### Build
```bash
go build ./...
```

---

## Running on 4 Machines (Multi-Machine Setup)

### Network Layout

| Node | Role | IP Address | Port |
|------|------|-----------|------|
| Node 0 | Server (You) | 10.207.4.131 | 9000 |
| Node 1 | Member 2 | 10.207.4.156 | 9001 |
| Node 2 | Member 3 | 10.207.4.197 | 9002 |
| Node 3 | Member 4 | 10.207.4.193 | 9003 |

### Step 0: Prerequisites (all machines)

1. **Go 1.21+** installed on all machines
2. All connected to the **same WiFi / LAN network**
3. Copy the project folder to all machines
4. **Allow TCP ports through the firewall** (if needed):

```bash
# Mac:
# System Preferences > Security & Privacy > Firewall > Allow incoming connections

# Linux:
sudo ufw allow 9000:9003/tcp

# Windows (run as Administrator):
New-NetFirewallRule -DisplayName "DS-CaseStudy" -Direction Inbound -Protocol TCP -LocalPort 9000-9003 -Action Allow
```

### Step 1: Start all 4 Nodes

**Your Machine (Node 0 - 10.207.4.131):**
```bash
go run ./cmd/coordinator/ -id=0 -addr=:9000 -peers="1=10.207.4.156:9001,2=10.207.4.197:9002,3=10.207.4.193:9003"
```

**Member 2 (Node 1 - 10.207.4.156):**
```bash
go run ./cmd/coordinator/ -id=1 -addr=:9001 -peers="0=10.207.4.131:9000,2=10.207.4.197:9002,3=10.207.4.193:9003"
```

**Member 3 (Node 2 - 10.207.4.197):**
```bash
go run ./cmd/coordinator/ -id=2 -addr=:9002 -peers="0=10.207.4.131:9000,1=10.207.4.156:9001,3=10.207.4.193:9003"
```

**Member 4 (Node 3 - 10.207.4.193):**
```bash
go run ./cmd/coordinator/ -id=3 -addr=:9003 -peers="0=10.207.4.131:9000,1=10.207.4.156:9001,2=10.207.4.197:9002"
```

> **Wait ~3 seconds** - you will see Bully election logs. Node 3 (highest ID) becomes LEADER.

### Step 2: Demonstrate Bully Leader Election

Type `status` on each terminal:

**Output on Node 3 (Leader):**
```
> status
  Node 3: State=LEADER, KnownLeader=3
  ** This node IS the leader (Bully Algorithm) **
```

**Output on Node 0 (Follower):**
```
> status
  Node 0: State=FOLLOWER, KnownLeader=3
  This node is a follower of Leader Node 3
```

### Step 3: Demonstrate Ricart-Agrawala Mutual Exclusion

**On any node's terminal:**
```
> lock General_Chat Server_A
  [OK] GRANTED critical section on "General_Chat" to [Server_A] (via Ricart-Agrawala)

> mutex
  Ricart-Agrawala: IN CRITICAL SECTION (resource: General_Chat)

> unlock General_Chat Server_A
  [OK] Released critical section on "General_Chat" (deferred replies sent)

> mutex
  Ricart-Agrawala: IDLE (not in CS, not requesting)
```

### Step 4: Demonstrate Ticket Booking (uses Ricart-Agrawala)

**On any node:**
```
> book 1
  [OK] Seat 1 booked successfully

> book 5
  [OK] Seat 5 booked successfully

> view
  [SEATS] Seat Status (Total: 10):
  Seat  1: [BOOKED]    booked
  Seat  2: [AVAILABLE] available
  ...
  Seat  5: [BOOKED]    booked
  ...
```

### Step 5: Demonstrate Deadlock Detection

**On the LEADER's terminal (Node 3):**
```
> deadlock
  [DETECT] Starting deadlock detection
  [DETECT] Sending PROBE to 3 peers
  [WFG] Wait-For Graph:
  [WFG]   (empty - no processes waiting)
  [SAFE] No deadlock detected.

> simulate_deadlock
  [SIMULATE] Creating artificial deadlock scenario
  [SIMULATE] Node 0: holds "Resource-0", waits for "Resource-1"
  [SIMULATE] Node 1: holds "Resource-1", waits for "Resource-2"
  [SIMULATE] Node 2: holds "Resource-2", waits for "Resource-3"
  [SIMULATE] Node 3: holds "Resource-3", waits for "Resource-0"
  [DEADLOCK] DEADLOCK DETECTED!
  [DEADLOCK] Cycle: Node 0 -> Node 1 -> Node 2 -> Node 3 -> Node 0
  [RESOLVE] Victim selected: Node 0 (lowest ID in cycle)
  [RESOLVE] Action: Force Node 0 to release its held resources
  [RESOLVE] Deadlock resolved!
```

### Step 6: Demonstrate Consensus

**On the LEADER's terminal:**
```
> propose ADD_SERVER Server_Beta 10.207.4.156:9001
  [PROPOSE] Broadcasting to 3 peers (need 3/4 votes)
  [COMMITTED] Proposal committed by majority

> state
  Consensus State (Routing Table):
    Server_Beta = 10.207.4.156:9001
```

**On any follower:**
```
> state
  Consensus State (Routing Table):
    Server_Beta = 10.207.4.156:9001
```

### Step 7: Demonstrate Leader Failure & Re-Election

**Kill Node 3** (Ctrl+C on Node 3's terminal).

**Wait ~3 seconds**, then type `status` on the surviving terminals:

```
> status
  Node 2: State=LEADER, KnownLeader=2
  ** This node IS the leader (Bully Algorithm) **
```

The next highest-ID alive node (Node 2) becomes the new leader automatically.

---

## CLI Commands

| Command | Description |
|---------|-------------|
| `status` | Show election state (Bully Algorithm) |
| `book <seat>` | Book a seat (uses Ricart-Agrawala for mutual exclusion) |
| `cancel <seat>` | Cancel a booking |
| `view` | View all seat statuses |
| `lock <resource> <requester>` | Enter critical section (Ricart-Agrawala) |
| `unlock <resource> <requester>` | Exit critical section (sends deferred replies) |
| `mutex` | Show mutual exclusion status |
| `deadlock` | Run deadlock detection (leader only) |
| `simulate_deadlock` | Simulate a deadlock for demo (leader only) |
| `propose <type> <key> <value>` | Propose consensus change (leader only) |
| `state` | Show consensus routing table |
| `help` | Show all commands |
| `quit` | Shutdown node |

---

## Communication Architecture

All inter-node communication uses **Go's native `net/rpc`** package over TCP.

| RPC Method | Algorithm | Direction |
|-----------|-----------|-----------|
| `Node.BullyElection` | Bully Election | Initiator --> Higher-ID nodes |
| `Node.BullyAnswer` | Bully Election | Higher-ID --> Initiator |
| `Node.BullyCoordinator` | Bully Election | Winner --> All |
| `Node.BullyHeartbeat` | Bully Election | Leader --> All |
| `Node.RARequest` | Ricart-Agrawala | Requester --> All |
| `Node.RAReply` | Ricart-Agrawala | Peer --> Requester |
| `Node.DeadlockProbe` | Deadlock Detection | Leader --> All |
| `Node.DeadlockReport` | Deadlock Detection | Peer --> Leader |
| `Node.ConsensusPropose` | Consensus | Leader --> Followers |
| `Node.ConsensusVote` | Consensus | Follower --> Leader |
| `Node.ConsensusCommit` | Consensus | Leader --> All |
| `Node.SeatUpdate` | Application | Any --> All peers |

---

*Built for DS Case Study - Distributed Systems, Semester 6*
