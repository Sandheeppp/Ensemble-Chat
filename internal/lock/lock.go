package lock

import (
	"fmt"
	"sync"
	"time"

	"distributed-chat-coordinator/internal/types"
)

// RicartAgrawalaManager implements mutual exclusion using the
// Ricart-Agrawala Algorithm.
//
// Algorithm Overview:
//   - To enter the critical section, a node broadcasts REQUEST(timestamp, nodeID)
//     to ALL other nodes and waits for REPLY from every one of them.
//   - On receiving a REQUEST from another node:
//     (a) If this node is NOT in the CS and does NOT want to enter the CS,
//         send REPLY immediately.
//     (b) If this node IS in the CS, defer the REPLY until it exits.
//     (c) If this node also wants to enter the CS, compare timestamps:
//         - If the incoming request has a LOWER timestamp (higher priority),
//           send REPLY immediately.
//         - If timestamps are equal, the node with the LOWER ID has priority.
//         - Otherwise, defer the REPLY.
//   - On exiting the CS, send all deferred REPLY messages.
//
// Properties:
//   - Mutual Exclusion: At most one node is in the critical section at a time.
//   - No Deadlock: Total ordering by (timestamp, nodeID) prevents circular waits.
//   - Fairness: Requests are granted in timestamp order (no starvation).
type RicartAgrawalaManager struct {
	mu sync.Mutex

	nodeID   int
	peers    []int
	prefix   string

	// Lamport logical clock
	clock int64

	// Current state
	requesting  bool      // true if we want to enter CS
	inCS        bool      // true if we are inside the CS
	reqTime     time.Time // timestamp of our current request
	reqResource string    // resource we are requesting

	// How many replies we still need before entering CS
	repliesNeeded int
	repliesGot    int
	grantCh       chan struct{} // signaled when all replies received

	// Deferred replies: peers whose REPLY we postponed
	deferred []deferredReply

	// Channels for incoming messages
	RequestCh chan types.RARequest
	ReplyCh   chan types.RAReply

	// Callbacks
	OnSendRequest func(targetID int, req types.RARequest)
	OnSendReply   func(targetID int, reply types.RAReply)

	stopCh chan struct{}
}

type deferredReply struct {
	peerID   int
	resource string
}

// NewRicartAgrawalaManager creates a new Ricart-Agrawala mutual exclusion manager.
func NewRicartAgrawalaManager(nodeID int, peers []int) *RicartAgrawalaManager {
	return &RicartAgrawalaManager{
		nodeID:    nodeID,
		peers:     peers,
		prefix:    types.LogPrefix(nodeID, "RICART-AGRAWALA"),
		clock:     0,
		RequestCh: make(chan types.RARequest, 100),
		ReplyCh:   make(chan types.RAReply, 100),
		grantCh:   make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
	}
}

// Start begins the Ricart-Agrawala event loop.
func (ra *RicartAgrawalaManager) Start() {
	go ra.run()
}

// Stop terminates the event loop.
func (ra *RicartAgrawalaManager) Stop() {
	select {
	case <-ra.stopCh:
	default:
		close(ra.stopCh)
	}
}

// run processes incoming REQUEST and REPLY messages.
func (ra *RicartAgrawalaManager) run() {
	for {
		select {
		case <-ra.stopCh:
			return

		case req := <-ra.RequestCh:
			ra.handleRequest(req)

		case reply := <-ra.ReplyCh:
			ra.handleReply(reply)
		}
	}
}

// handleRequest processes an incoming REQUEST from another node.
func (ra *RicartAgrawalaManager) handleRequest(req types.RARequest) {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	// Update logical clock
	ra.clock++

	fmt.Printf("%s[REQUEST] Received REQUEST from Node %d for resource \"%s\" (timestamp: %v)\n",
		ra.prefix, req.SenderID, req.Resource, req.Timestamp.UnixMilli())

	// Decision: send REPLY now or defer?
	shouldDefer := false

	if ra.inCS && req.Resource == ra.reqResource {
		// We are in the critical section for the same resource => defer
		shouldDefer = true
		fmt.Printf("%s[REQUEST] Currently IN critical section. Deferring reply to Node %d.\n",
			ra.prefix, req.SenderID)
	} else if ra.requesting && req.Resource == ra.reqResource {
		// We also want the CS for the same resource => compare priorities
		// Lower timestamp wins; if equal, lower nodeID wins
		if req.Timestamp.Before(ra.reqTime) || (req.Timestamp.Equal(ra.reqTime) && req.SenderID < ra.nodeID) {
			// Incoming request has higher priority
			fmt.Printf("%s[REQUEST] Node %d has higher priority (earlier timestamp). Sending REPLY.\n",
				ra.prefix, req.SenderID)
			shouldDefer = false
		} else {
			// Our request has higher priority
			fmt.Printf("%s[REQUEST] Our request has higher priority. Deferring reply to Node %d.\n",
				ra.prefix, req.SenderID)
			shouldDefer = true
		}
	}

	if shouldDefer {
		ra.deferred = append(ra.deferred, deferredReply{
			peerID:   req.SenderID,
			resource: req.Resource,
		})
	} else {
		// Send REPLY immediately
		fmt.Printf("%s[REPLY] Sending REPLY to Node %d for resource \"%s\"\n",
			ra.prefix, req.SenderID, req.Resource)
		if ra.OnSendReply != nil {
			go ra.OnSendReply(req.SenderID, types.RAReply{
				SenderID: ra.nodeID,
				Resource: req.Resource,
			})
		}
	}
}

// handleReply processes an incoming REPLY from another node.
func (ra *RicartAgrawalaManager) handleReply(reply types.RAReply) {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	if !ra.requesting {
		return
	}

	ra.repliesGot++
	fmt.Printf("%s[REPLY] Received REPLY from Node %d (%d/%d replies)\n",
		ra.prefix, reply.SenderID, ra.repliesGot, ra.repliesNeeded)

	if ra.repliesGot >= ra.repliesNeeded {
		fmt.Printf("%s[GRANTED] All replies received. Entering critical section.\n", ra.prefix)
		// Signal that we can enter CS
		select {
		case ra.grantCh <- struct{}{}:
		default:
		}
	}
}

// RequestAccess requests to enter the critical section for a given resource.
// Blocks until access is granted (all peers have replied).
func (ra *RicartAgrawalaManager) RequestAccess(resource, requester string) types.LockResponse {
	ra.mu.Lock()

	// Advance logical clock
	ra.clock++
	now := time.Now()

	ra.requesting = true
	ra.reqTime = now
	ra.reqResource = resource
	ra.repliesNeeded = len(ra.peers)
	ra.repliesGot = 0

	// Drain any old grant signals
	select {
	case <-ra.grantCh:
	default:
	}

	fmt.Printf("%s=============================================\n", ra.prefix)
	fmt.Printf("%s[REQUEST] Node %d requesting critical section for \"%s\"\n",
		ra.prefix, ra.nodeID, resource)
	fmt.Printf("%s[REQUEST] Broadcasting REQUEST to %d peers (timestamp: %v)\n",
		ra.prefix, ra.repliesNeeded, now.UnixMilli())
	fmt.Printf("%s=============================================\n", ra.prefix)

	req := types.RARequest{
		SenderID:  ra.nodeID,
		Timestamp: now,
		Resource:  resource,
	}

	ra.mu.Unlock()

	// Broadcast REQUEST to all peers
	for _, peerID := range ra.peers {
		if ra.OnSendRequest != nil {
			pid := peerID
			go ra.OnSendRequest(pid, req)
		}
	}

	// If no peers, grant immediately
	if len(ra.peers) == 0 {
		ra.mu.Lock()
		ra.inCS = true
		ra.requesting = false
		ra.mu.Unlock()
		return types.LockResponse{
			Resource: resource,
			Granted:  true,
			Holder:   requester,
		}
	}

	// Wait for all replies (with timeout)
	select {
	case <-ra.grantCh:
		ra.mu.Lock()
		ra.inCS = true
		ra.requesting = false
		ra.mu.Unlock()

		fmt.Printf("%s[CS-ENTER] Node %d ENTERED critical section for \"%s\"\n",
			ra.prefix, ra.nodeID, resource)

		return types.LockResponse{
			Resource: resource,
			Granted:  true,
			Holder:   requester,
		}

	case <-time.After(10 * time.Second):
		ra.mu.Lock()
		ra.requesting = false
		ra.mu.Unlock()

		fmt.Printf("%s[TIMEOUT] Timed out waiting for replies. Critical section DENIED.\n",
			ra.prefix)

		return types.LockResponse{
			Resource: resource,
			Granted:  false,
			Holder:   "",
			Reason:   "Timed out waiting for peer replies",
		}
	}
}

// ReleaseAccess exits the critical section and sends all deferred replies.
func (ra *RicartAgrawalaManager) ReleaseAccess(resource, requester string) bool {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	if !ra.inCS {
		fmt.Printf("%s[ERROR] Cannot release - not in critical section\n", ra.prefix)
		return false
	}

	ra.inCS = false
	ra.reqResource = ""

	fmt.Printf("%s[CS-EXIT] Node %d EXITED critical section for \"%s\"\n",
		ra.prefix, ra.nodeID, resource)

	// Send all deferred replies
	if len(ra.deferred) > 0 {
		fmt.Printf("%s[DEFERRED] Sending %d deferred replies\n",
			ra.prefix, len(ra.deferred))
	}

	for _, d := range ra.deferred {
		fmt.Printf("%s[DEFERRED] Sending deferred REPLY to Node %d for \"%s\"\n",
			ra.prefix, d.peerID, d.resource)
		if ra.OnSendReply != nil {
			pid := d.peerID
			res := d.resource
			go ra.OnSendReply(pid, types.RAReply{
				SenderID: ra.nodeID,
				Resource: res,
			})
		}
	}
	ra.deferred = nil

	return true
}

// GetStatus returns the current status of this node's mutual exclusion state.
func (ra *RicartAgrawalaManager) GetStatus() (requesting bool, inCS bool, resource string) {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return ra.requesting, ra.inCS, ra.reqResource
}

// IsInCS returns whether this node is currently in the critical section.
func (ra *RicartAgrawalaManager) IsInCS() bool {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return ra.inCS
}

// IsRequesting returns whether this node is currently requesting CS.
func (ra *RicartAgrawalaManager) IsRequesting() bool {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	return ra.requesting
}

// GetWaitingResource returns the resource this node is waiting for (empty if not waiting).
func (ra *RicartAgrawalaManager) GetWaitingResource() string {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	if ra.requesting && !ra.inCS {
		return ra.reqResource
	}
	return ""
}

// GetHeldResource returns the resource this node is holding (empty if not in CS).
func (ra *RicartAgrawalaManager) GetHeldResource() string {
	ra.mu.Lock()
	defer ra.mu.Unlock()
	if ra.inCS {
		return ra.reqResource
	}
	return ""
}
