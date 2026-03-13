package coordinator

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"

	"distributed-chat-coordinator/internal/booking"
	"distributed-chat-coordinator/internal/consensus"
	"distributed-chat-coordinator/internal/deadlock"
	"distributed-chat-coordinator/internal/election"
	"distributed-chat-coordinator/internal/lock"
	"distributed-chat-coordinator/internal/transport"
	"distributed-chat-coordinator/internal/types"
)

// CoordinatorNode represents a single node in the coordination ensemble.
// It integrates four distributed algorithms:
//   - Bully Leader Election (election module)
//   - Ricart-Agrawala Mutual Exclusion (lock module)
//   - Wait-For Graph Deadlock Detection (deadlock module)
//   - Simple Majority-Vote Consensus (consensus module)
type CoordinatorNode struct {
	mu sync.Mutex

	ID        int
	Addr      string
	Peers     []*CoordinatorNode
	PeerAddrs map[int]string // peerID -> "host:port"
	AllNodeIDs []int          // all node IDs including self

	Election  *election.BullyNode
	Mutex     *lock.RicartAgrawalaManager
	Deadlock  *deadlock.DeadlockDetector
	Consensus *consensus.SimpleConsensus
	SeatDB    *booking.SeatDB

	listener   net.Listener
	rpcClients map[int]*transport.RPCClient // lazy-initialized per peer
	prefix     string
	alive      bool
	stopCh     chan struct{}

	// Callback to propagate seat updates to peers
	OnSeatUpdate func(update types.SeatUpdate)
}

// NewCoordinatorNode creates a new coordinator node with all four algorithms.
func NewCoordinatorNode(id int, peerIDs []int) *CoordinatorNode {
	// Build sorted list of all node IDs
	allNodes := make([]int, 0, len(peerIDs)+1)
	allNodes = append(allNodes, id)
	allNodes = append(allNodes, peerIDs...)
	sort.Ints(allNodes)

	node := &CoordinatorNode{
		ID:         id,
		prefix:     types.LogPrefix(id, "COORDINATOR"),
		alive:      true,
		stopCh:     make(chan struct{}),
		PeerAddrs:  make(map[int]string),
		rpcClients: make(map[int]*transport.RPCClient),
		AllNodeIDs: allNodes,
		Election:   election.NewBullyNode(id, peerIDs),
		Mutex:      lock.NewRicartAgrawalaManager(id, peerIDs),
		Deadlock:   deadlock.NewDeadlockDetector(id, peerIDs),
		Consensus:  consensus.NewSimpleConsensus(id, peerIDs),
		SeatDB:     booking.NewSeatDB(id),
	}

	// Wire election callbacks to update consensus module
	node.Election.OnBecomeLeader = func() {
		fmt.Printf("%s[LEADER] I am now the LEADER\n", node.prefix)
		node.Consensus.BecomeLeader()
	}

	node.Election.OnBecomeFollower = func(leaderID int) {
		fmt.Printf("%s[FOLLOWER] Following Leader Node %d\n",
			node.prefix, leaderID)
		node.Consensus.BecomeFollower()
	}

	return node
}

// Start begins all algorithm modules.
func (n *CoordinatorNode) Start() {
	fmt.Printf("%s[START] Starting coordinator node...\n", n.prefix)
	n.Election.Start()
	n.Mutex.Start()
	n.Deadlock.Start()
	n.Consensus.Start()
}

// Stop shuts down all modules.
func (n *CoordinatorNode) Stop() {
	n.mu.Lock()
	n.alive = false
	n.mu.Unlock()

	if n.listener != nil {
		n.listener.Close()
	}

	fmt.Printf("%s[STOP] Stopping coordinator node...\n", n.prefix)
	n.Election.Stop()
	n.Mutex.Stop()
	n.Deadlock.Stop()
	n.Consensus.Stop()
}

// ══════════════════════════════════════════════════════════════════════════════
// net/rpc server — implements transport.RPCHandler
// ══════════════════════════════════════════════════════════════════════════════

// --- Bully Election handlers ---

func (n *CoordinatorNode) HandleBullyElection(msg types.BullyElectionMsg) {
	n.Election.ElectionCh <- msg
}

func (n *CoordinatorNode) HandleBullyAnswer(msg types.BullyAnswerMsg) {
	n.Election.AnswerCh <- msg
}

func (n *CoordinatorNode) HandleBullyCoordinator(msg types.BullyCoordinatorMsg) {
	n.Election.CoordinatorCh <- msg
}

func (n *CoordinatorNode) HandleBullyHeartbeat(hb types.BullyHeartbeat) {
	n.Election.HeartbeatCh <- hb
}

// --- Ricart-Agrawala Mutual Exclusion handlers ---

func (n *CoordinatorNode) HandleRARequest(req types.RARequest) {
	n.Mutex.RequestCh <- req
}

func (n *CoordinatorNode) HandleRAReply(reply types.RAReply) {
	n.Mutex.ReplyCh <- reply
}

// --- Deadlock Detection handlers ---

func (n *CoordinatorNode) HandleDeadlockProbe(probe types.DeadlockProbe) {
	n.Deadlock.ProbeCh <- probe
}

func (n *CoordinatorNode) HandleDeadlockReport(report types.DeadlockReport) {
	n.Deadlock.ReportCh <- report
}

// --- Consensus handlers ---

func (n *CoordinatorNode) HandleConsensusPropose(proposal types.ConsensusProposal) {
	n.Consensus.ProposeCh <- proposal
}

func (n *CoordinatorNode) HandleConsensusVote(vote types.ConsensusVote) {
	n.Consensus.VoteCh <- vote
}

func (n *CoordinatorNode) HandleConsensusCommit(commit types.ConsensusCommit) {
	n.Consensus.CommitCh <- commit
}

// --- Application handlers ---

func (n *CoordinatorNode) HandleSeatUpdate(update types.SeatUpdate) {
	fmt.Printf("%s[SYNC] Received seat update from Node %d: Seat %d -> %s\n",
		n.prefix, update.SenderID, update.SeatNum, update.Status)
	n.SeatDB.SetSeatStatus(update.SeatNum, update.Status)
}

// StartRPC opens a TCP listener and registers this node's RPC service.
func (n *CoordinatorNode) StartRPC() error {
	ln, err := net.Listen("tcp", n.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", n.Addr, err)
	}
	n.listener = ln

	svc := transport.NewNodeRPC(n)
	transport.StartRPCServer(ln, svc)

	fmt.Printf("%s[RPC] Server started on %s\n", n.prefix, n.Addr)
	return nil
}

// ══════════════════════════════════════════════════════════════════════════════
// net/rpc client helpers
// ══════════════════════════════════════════════════════════════════════════════

func (n *CoordinatorNode) rpcClient(peerID int) *transport.RPCClient {
	if c, ok := n.rpcClients[peerID]; ok {
		return c
	}
	addr, ok := n.PeerAddrs[peerID]
	if !ok {
		fmt.Printf("%s[WARN] No address for peer %d\n", n.prefix, peerID)
		return nil
	}
	c := transport.NewRPCClient(addr, n.prefix)
	n.rpcClients[peerID] = c
	return c
}

// ══════════════════════════════════════════════════════════════════════════════
// SetupRPCComm — wire all callbacks for outbound RPCs.
// ══════════════════════════════════════════════════════════════════════════════

func SetupRPCComm(nodes []*CoordinatorNode) {
	for _, node := range nodes {
		n := node

		// --- Bully Election callbacks (RPC) ---

		n.Election.OnSendElection = func(targetID int, msg types.BullyElectionMsg) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendBullyElection(msg)
			}
		}

		n.Election.OnSendAnswer = func(targetID int, msg types.BullyAnswerMsg) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendBullyAnswer(msg)
			}
		}

		n.Election.OnSendCoordinator = func(msg types.BullyCoordinatorMsg) {
			for peerID := range n.PeerAddrs {
				c := n.rpcClient(peerID)
				if c != nil {
					go c.SendBullyCoordinator(msg)
				}
			}
		}

		n.Election.OnSendHeartbeat = func(hb types.BullyHeartbeat) {
			for peerID := range n.PeerAddrs {
				c := n.rpcClient(peerID)
				if c != nil {
					go c.SendBullyHeartbeat(hb)
				}
			}
		}

		// --- Ricart-Agrawala callbacks (RPC) ---

		n.Mutex.OnSendRequest = func(targetID int, req types.RARequest) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendRARequest(req)
			}
		}

		n.Mutex.OnSendReply = func(targetID int, reply types.RAReply) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendRAReply(reply)
			}
		}

		// --- Deadlock Detection callbacks (RPC) ---

		n.Deadlock.OnSendProbe = func(targetID int, probe types.DeadlockProbe) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendDeadlockProbe(probe)
			}
		}

		n.Deadlock.OnSendReport = func(targetID int, report types.DeadlockReport) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendDeadlockReport(report)
			}
		}

		// --- Consensus callbacks (RPC) ---

		n.Consensus.OnSendPropose = func(targetID int, proposal types.ConsensusProposal) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendConsensusPropose(proposal)
			}
		}

		n.Consensus.OnSendVote = func(targetID int, vote types.ConsensusVote) {
			c := n.rpcClient(targetID)
			if c != nil {
				c.SendConsensusVote(vote)
			}
		}

		n.Consensus.OnSendCommit = func(commit types.ConsensusCommit) {
			for peerID := range n.PeerAddrs {
				c := n.rpcClient(peerID)
				if c != nil {
					go c.SendConsensusCommit(commit)
				}
			}
		}

		// --- Seat Update callback (RPC) ---

		n.OnSeatUpdate = func(update types.SeatUpdate) {
			for peerID := range n.PeerAddrs {
				c := n.rpcClient(peerID)
				if c != nil {
					go c.SendSeatUpdate(update)
				}
			}
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// In-process communication — for demos and testing
// ══════════════════════════════════════════════════════════════════════════════

func SetupInProcessComm(nodes []*CoordinatorNode) {
	for _, node := range nodes {
		node.Peers = nodes
	}

	for _, node := range nodes {
		n := node

		// --- Bully Election callbacks (in-process) ---
		n.Election.OnSendElection = func(targetID int, msg types.BullyElectionMsg) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Election.ElectionCh <- msg
				}
			}
		}

		n.Election.OnSendAnswer = func(targetID int, msg types.BullyAnswerMsg) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Election.AnswerCh <- msg
				}
			}
		}

		n.Election.OnSendCoordinator = func(msg types.BullyCoordinatorMsg) {
			for _, peer := range nodes {
				if peer.ID != n.ID && peer.IsAlive() {
					peer.Election.CoordinatorCh <- msg
				}
			}
		}

		n.Election.OnSendHeartbeat = func(hb types.BullyHeartbeat) {
			for _, peer := range nodes {
				if peer.ID != n.ID && peer.IsAlive() {
					peer.Election.HeartbeatCh <- hb
				}
			}
		}

		// --- Ricart-Agrawala callbacks (in-process) ---
		n.Mutex.OnSendRequest = func(targetID int, req types.RARequest) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Mutex.RequestCh <- req
				}
			}
		}

		n.Mutex.OnSendReply = func(targetID int, reply types.RAReply) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Mutex.ReplyCh <- reply
				}
			}
		}

		// --- Deadlock Detection callbacks (in-process) ---
		n.Deadlock.OnSendProbe = func(targetID int, probe types.DeadlockProbe) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Deadlock.ProbeCh <- probe
				}
			}
		}

		n.Deadlock.OnSendReport = func(targetID int, report types.DeadlockReport) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Deadlock.ReportCh <- report
				}
			}
		}

		// --- Consensus callbacks (in-process) ---
		n.Consensus.OnSendPropose = func(targetID int, proposal types.ConsensusProposal) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Consensus.ProposeCh <- proposal
				}
			}
		}

		n.Consensus.OnSendVote = func(targetID int, vote types.ConsensusVote) {
			for _, peer := range nodes {
				if peer.ID == targetID && peer.IsAlive() {
					peer.Consensus.VoteCh <- vote
				}
			}
		}

		n.Consensus.OnSendCommit = func(commit types.ConsensusCommit) {
			for _, peer := range nodes {
				if peer.ID != n.ID && peer.IsAlive() {
					peer.Consensus.CommitCh <- commit
				}
			}
		}

		// --- Seat Update callback (in-process) ---
		n.OnSeatUpdate = func(update types.SeatUpdate) {
			for _, peer := range nodes {
				if peer.ID != n.ID && peer.IsAlive() {
					peer.SeatDB.SetSeatStatus(update.SeatNum, update.Status)
				}
			}
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Node lifecycle helpers
// ══════════════════════════════════════════════════════════════════════════════

func (n *CoordinatorNode) IsAlive() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.alive
}

func (n *CoordinatorNode) Kill() {
	n.mu.Lock()
	n.alive = false
	n.mu.Unlock()
	n.Election.Stop()
	n.Mutex.Stop()
	n.Deadlock.Stop()
	n.Consensus.Stop()
	fmt.Printf("%s=============================================\n", n.prefix)
	fmt.Printf("%s NODE KILLED (simulating crash)\n", n.prefix)
	fmt.Printf("%s=============================================\n", n.prefix)
}

// ══════════════════════════════════════════════════════════════════════════════
// Application-level operations
// ══════════════════════════════════════════════════════════════════════════════

// AcquireLock requests access to the critical section via Ricart-Agrawala.
func (n *CoordinatorNode) AcquireLock(resource, requester string) types.LockResponse {
	return n.Mutex.RequestAccess(resource, requester)
}

// ReleaseLock exits the critical section and sends deferred replies.
func (n *CoordinatorNode) ReleaseLock(resource, requester string) bool {
	return n.Mutex.ReleaseAccess(resource, requester)
}

// ProposeChange proposes a change via simple majority consensus.
func (n *CoordinatorNode) ProposeChange(txID string, change types.ChangeType, key, value string) bool {
	if !n.Election.IsLeader() {
		fmt.Printf("%s[WARN] Cannot propose change -- not the leader\n", n.prefix)
		return false
	}
	return n.Consensus.ProposeEntry(change, key, value)
}

// ══════════════════════════════════════════════════════════════════════════════
// Ticket Booking Operations
// ══════════════════════════════════════════════════════════════════════════════

// BookSeat books a seat using Ricart-Agrawala for mutual exclusion.
func (n *CoordinatorNode) BookSeat(seatNum int) (bool, string) {
	requester := fmt.Sprintf("Node-%d", n.ID)
	resource := fmt.Sprintf("seat-%d", seatNum)

	fmt.Printf("%s[BOOK] Node %d requesting critical section for booking seat %d\n",
		n.prefix, n.ID, seatNum)

	// Step 1: Acquire lock (Ricart-Agrawala mutual exclusion)
	resp := n.AcquireLock(resource, requester)
	if !resp.Granted {
		return false, fmt.Sprintf("Failed to acquire lock: %s", resp.Reason)
	}

	fmt.Printf("%s[BOOK] Node %d entered critical section\n", n.prefix, n.ID)

	// Step 2: Check and book the seat
	success, msg := n.SeatDB.BookSeat(seatNum)

	// Step 3: Release the critical section
	n.ReleaseLock(resource, requester)

	// Step 4: Propagate update to peers if booking succeeded
	if success {
		fmt.Printf("%s[SYNC] Seat update propagated to peers\n", n.prefix)
		if n.OnSeatUpdate != nil {
			n.OnSeatUpdate(types.SeatUpdate{
				SeatNum:  seatNum,
				Status:   "booked",
				SenderID: n.ID,
			})
		}
	}

	return success, msg
}

// CancelSeat cancels a seat booking using Ricart-Agrawala for mutual exclusion.
func (n *CoordinatorNode) CancelSeat(seatNum int) (bool, string) {
	requester := fmt.Sprintf("Node-%d", n.ID)
	resource := fmt.Sprintf("seat-%d", seatNum)

	fmt.Printf("%s[CANCEL] Node %d requesting critical section for cancelling seat %d\n",
		n.prefix, n.ID, seatNum)

	resp := n.AcquireLock(resource, requester)
	if !resp.Granted {
		return false, fmt.Sprintf("Failed to acquire lock: %s", resp.Reason)
	}

	fmt.Printf("%s[CANCEL] Node %d entered critical section\n", n.prefix, n.ID)

	success, msg := n.SeatDB.CancelSeat(seatNum)

	n.ReleaseLock(resource, requester)

	if success {
		fmt.Printf("%s[SYNC] Seat update propagated to peers\n", n.prefix)
		if n.OnSeatUpdate != nil {
			n.OnSeatUpdate(types.SeatUpdate{
				SeatNum:  seatNum,
				Status:   "available",
				SenderID: n.ID,
			})
		}
	}

	return success, msg
}

// ViewSeats returns the current seat status from the local database.
func (n *CoordinatorNode) ViewSeats() map[int]string {
	return n.SeatDB.ViewSeats()
}

// ViewSeatsFormatted prints a formatted view of all seats.
func (n *CoordinatorNode) ViewSeatsFormatted() {
	seats := n.ViewSeats()
	fmt.Printf("%s[SEATS] Seat Status (Total: %d):\n", n.prefix, types.TotalSeats)
	fmt.Printf("%s------------------------\n", n.prefix)
	for i := 1; i <= types.TotalSeats; i++ {
		status := seats[i]
		icon := "[AVAILABLE]"
		if status == "booked" {
			icon = "[BOOKED]   "
		}
		fmt.Printf("%s  Seat %2d: %s %s\n", n.prefix, i, icon, status)
	}
	fmt.Printf("%s------------------------\n", n.prefix)

	available := 0
	booked := 0
	for _, s := range seats {
		if s == "available" {
			available++
		} else {
			booked++
		}
	}
	fmt.Printf("%s  Available: %d | Booked: %d\n", n.prefix, available, booked)
}

// Placeholder to suppress unused import
var _ = strconv.Itoa
