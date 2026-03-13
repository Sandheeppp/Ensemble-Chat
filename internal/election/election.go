package election

import (
	"fmt"
	"sync"
	"time"

	"distributed-chat-coordinator/internal/types"
)

const (
	// HeartbeatInterval is how often the leader sends heartbeats.
	HeartbeatInterval = 500 * time.Millisecond

	// ElectionTimeout is how long to wait for heartbeats before starting election.
	ElectionTimeout = 3000 * time.Millisecond

	// AnswerTimeout is how long a candidate waits for ANSWER from higher-ID nodes.
	AnswerTimeout = 2000 * time.Millisecond
)

// BullyNode implements the Bully Leader Election Algorithm.
//
// Algorithm Overview:
//   - Any node can initiate an election when it detects the leader has failed
//     (no heartbeat received within ElectionTimeout).
//   - The initiator sends ELECTION messages to all nodes with HIGHER IDs.
//   - If no higher-ID node responds with ANSWER within AnswerTimeout,
//     the initiator declares itself leader and broadcasts COORDINATOR to all.
//   - If a higher-ID node responds with ANSWER, the initiator yields
//     and waits for a COORDINATOR message.
//   - The highest-ID alive node always wins the election.
//
// Properties:
//   - The node with the highest ID among alive nodes becomes the leader.
//   - Leader failure is detected via heartbeat timeout.
//   - Re-election is automatic on leader crash.
type BullyNode struct {
	mu sync.Mutex

	ID    int
	Peers []int // all peer IDs

	State    types.NodeState
	LeaderID int

	lastHeartbeat time.Time
	electionInProgress bool
	answerReceived     bool

	// Channels for incoming messages
	ElectionCh    chan types.BullyElectionMsg
	AnswerCh      chan types.BullyAnswerMsg
	CoordinatorCh chan types.BullyCoordinatorMsg
	HeartbeatCh   chan types.BullyHeartbeat

	// Callbacks for sending messages (wired by coordinator)
	OnSendElection    func(targetID int, msg types.BullyElectionMsg)
	OnSendAnswer      func(targetID int, msg types.BullyAnswerMsg)
	OnSendCoordinator func(msg types.BullyCoordinatorMsg)
	OnSendHeartbeat   func(hb types.BullyHeartbeat)
	OnBecomeLeader    func()
	OnBecomeFollower  func(leaderID int)

	stopCh chan struct{}
	prefix string
}

// NewBullyNode creates a new Bully election node.
func NewBullyNode(id int, peers []int) *BullyNode {
	return &BullyNode{
		ID:            id,
		Peers:         peers,
		State:         types.Follower,
		LeaderID:      -1,
		lastHeartbeat: time.Now(),
		ElectionCh:    make(chan types.BullyElectionMsg, 100),
		AnswerCh:      make(chan types.BullyAnswerMsg, 100),
		CoordinatorCh: make(chan types.BullyCoordinatorMsg, 100),
		HeartbeatCh:   make(chan types.BullyHeartbeat, 100),
		stopCh:        make(chan struct{}),
		prefix:        types.LogPrefix(id, "BULLY"),
	}
}

// Start begins the Bully election event loop.
func (b *BullyNode) Start() {
	go b.run()
}

// Stop terminates the election event loop.
func (b *BullyNode) Stop() {
	select {
	case <-b.stopCh:
	default:
		close(b.stopCh)
	}
}

// GetState returns current state, and leader ID.
func (b *BullyNode) GetState() (types.NodeState, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.State, b.LeaderID
}

// IsLeader returns whether this node is the current leader.
func (b *BullyNode) IsLeader() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.State == types.Leader
}

// GetLeaderID returns the current known leader ID.
func (b *BullyNode) GetLeaderID() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.LeaderID
}

// run is the main event loop for the Bully algorithm.
func (b *BullyNode) run() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			return

		case <-ticker.C:
			b.checkHeartbeatTimeout()

		case msg := <-b.ElectionCh:
			b.handleElection(msg)

		case msg := <-b.AnswerCh:
			b.handleAnswer(msg)

		case msg := <-b.CoordinatorCh:
			b.handleCoordinator(msg)

		case hb := <-b.HeartbeatCh:
			b.handleHeartbeat(hb)
		}
	}
}

// checkHeartbeatTimeout triggers an election if the leader hasn't sent
// a heartbeat within ElectionTimeout.
func (b *BullyNode) checkHeartbeatTimeout() {
	b.mu.Lock()

	if b.State == types.Leader {
		b.mu.Unlock()
		b.sendHeartbeat()
		return
	}

	if time.Since(b.lastHeartbeat) > ElectionTimeout && !b.electionInProgress {
		fmt.Printf("%s[TIMEOUT] No heartbeat for %v. Starting Bully election...\n",
			b.prefix, ElectionTimeout)
		b.electionInProgress = true
		b.mu.Unlock()
		b.startElection()
		return
	}

	b.mu.Unlock()
}

// startElection initiates the Bully election by sending ELECTION messages
// to all nodes with higher IDs.
func (b *BullyNode) startElection() {
	b.mu.Lock()
	b.State = types.Candidate
	b.answerReceived = false
	myID := b.ID
	b.mu.Unlock()

	fmt.Printf("%s[ELECTION] Node %d starting Bully election\n", b.prefix, myID)
	fmt.Printf("%s=============================================\n", b.prefix)

	// Send ELECTION to all nodes with higher IDs
	higherPeersExist := false
	for _, peerID := range b.Peers {
		if peerID > myID {
			higherPeersExist = true
			fmt.Printf("%s[ELECTION] Sending ELECTION to Node %d (higher ID)\n",
				b.prefix, peerID)
			if b.OnSendElection != nil {
				go b.OnSendElection(peerID, types.BullyElectionMsg{SenderID: myID})
			}
		}
	}

	// If no higher-ID peers exist, this node is the highest => become leader immediately
	if !higherPeersExist {
		fmt.Printf("%s[ELECTION] No higher-ID nodes exist. Declaring self as leader.\n",
			b.prefix)
		b.becomeLeader()
		return
	}

	// Wait for ANSWER from higher-ID nodes
	go func() {
		time.Sleep(AnswerTimeout)

		b.mu.Lock()
		defer b.mu.Unlock()

		if b.State != types.Candidate {
			return // already resolved
		}

		if !b.answerReceived {
			fmt.Printf("%s[ELECTION] No ANSWER received within %v. Declaring self as leader.\n",
				b.prefix, AnswerTimeout)
			b.mu.Unlock()
			b.becomeLeader()
			b.mu.Lock()
		}
	}()
}

// handleElection processes an incoming ELECTION message from a lower-ID node.
func (b *BullyNode) handleElection(msg types.BullyElectionMsg) {
	b.mu.Lock()
	myID := b.ID
	b.mu.Unlock()

	if msg.SenderID < myID {
		// I have a higher ID, send ANSWER and start my own election
		fmt.Printf("%s[ELECTION] Received ELECTION from Node %d (lower ID). Sending ANSWER.\n",
			b.prefix, msg.SenderID)

		if b.OnSendAnswer != nil {
			go b.OnSendAnswer(msg.SenderID, types.BullyAnswerMsg{SenderID: myID})
		}

		// Start my own election
		b.mu.Lock()
		if !b.electionInProgress {
			b.electionInProgress = true
			b.mu.Unlock()
			b.startElection()
		} else {
			b.mu.Unlock()
		}
	}
}

// handleAnswer processes an ANSWER message from a higher-ID node.
func (b *BullyNode) handleAnswer(msg types.BullyAnswerMsg) {
	b.mu.Lock()
	defer b.mu.Unlock()

	fmt.Printf("%s[ELECTION] Received ANSWER from Node %d (higher ID). Yielding.\n",
		b.prefix, msg.SenderID)

	b.answerReceived = true
	// Higher-ID node will take over the election; revert to follower
	if b.State == types.Candidate {
		b.State = types.Follower
	}
}

// handleCoordinator processes a COORDINATOR message announcing the new leader.
func (b *BullyNode) handleCoordinator(msg types.BullyCoordinatorMsg) {
	b.mu.Lock()
	defer b.mu.Unlock()

	oldLeader := b.LeaderID
	b.LeaderID = msg.LeaderID
	b.State = types.Follower
	b.lastHeartbeat = time.Now()
	b.electionInProgress = false
	b.answerReceived = false

	fmt.Printf("%s=============================================\n", b.prefix)
	fmt.Printf("%s[COORDINATOR] Node %d is the NEW LEADER\n", b.prefix, msg.LeaderID)
	fmt.Printf("%s=============================================\n", b.prefix)

	if oldLeader != msg.LeaderID && b.OnBecomeFollower != nil {
		go b.OnBecomeFollower(msg.LeaderID)
	}
}

// handleHeartbeat processes a heartbeat from the leader.
func (b *BullyNode) handleHeartbeat(hb types.BullyHeartbeat) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.State == types.Leader && hb.LeaderID != b.ID {
		// Another node claims to be leader
		if hb.LeaderID > b.ID {
			fmt.Printf("%s[HEARTBEAT] Higher-ID leader %d detected. Stepping down.\n",
				b.prefix, hb.LeaderID)
			b.State = types.Follower
			b.LeaderID = hb.LeaderID
			b.lastHeartbeat = time.Now()
			b.electionInProgress = false
			if b.OnBecomeFollower != nil {
				go b.OnBecomeFollower(hb.LeaderID)
			}
		}
		return
	}

	oldLeader := b.LeaderID
	b.LeaderID = hb.LeaderID
	b.lastHeartbeat = time.Now()
	b.electionInProgress = false

	if b.State != types.Follower {
		b.State = types.Follower
	}

	if oldLeader != hb.LeaderID && b.OnBecomeFollower != nil {
		go b.OnBecomeFollower(hb.LeaderID)
	}
}

// becomeLeader transitions this node to LEADER state and broadcasts COORDINATOR.
func (b *BullyNode) becomeLeader() {
	b.mu.Lock()
	b.State = types.Leader
	b.LeaderID = b.ID
	b.electionInProgress = false
	b.answerReceived = false
	b.mu.Unlock()

	fmt.Printf("%s=============================================\n", b.prefix)
	fmt.Printf("%s BECAME LEADER (Bully Algorithm - Highest alive ID)\n", b.prefix)
	fmt.Printf("%s=============================================\n", b.prefix)

	// Broadcast COORDINATOR message to all peers
	coordMsg := types.BullyCoordinatorMsg{LeaderID: b.ID}
	if b.OnSendCoordinator != nil {
		go b.OnSendCoordinator(coordMsg)
	}

	if b.OnBecomeLeader != nil {
		go b.OnBecomeLeader()
	}
}

// sendHeartbeat sends periodic heartbeats to all peers.
func (b *BullyNode) sendHeartbeat() {
	b.mu.Lock()
	hb := types.BullyHeartbeat{
		LeaderID: b.ID,
	}
	b.mu.Unlock()

	if b.OnSendHeartbeat != nil {
		b.OnSendHeartbeat(hb)
	}
}
