package transport

import (
	"fmt"
	"net"
	"net/rpc"
	"sync"
	"time"

	"distributed-chat-coordinator/internal/types"
)

// ──────────────────────────────────────────────────────────────────────────────
// RPC Service — registered on every coordinator node so peers can call it.
//
// Go's net/rpc requires exported methods on an exported type with the
// signature:  func (t *T) MethodName(args *Args, reply *Reply) error
//
// Each RPC method simply pushes the received payload into the appropriate
// channel on the owning CoordinatorNode (via the Handler interface).
// ──────────────────────────────────────────────────────────────────────────────

// RPCHandler is implemented by the coordinator node to dispatch inbound RPCs.
type RPCHandler interface {
	// Bully Election
	HandleBullyElection(msg types.BullyElectionMsg)
	HandleBullyAnswer(msg types.BullyAnswerMsg)
	HandleBullyCoordinator(msg types.BullyCoordinatorMsg)
	HandleBullyHeartbeat(hb types.BullyHeartbeat)

	// Ricart-Agrawala Mutual Exclusion
	HandleRARequest(req types.RARequest)
	HandleRAReply(reply types.RAReply)

	// Deadlock Detection
	HandleDeadlockProbe(probe types.DeadlockProbe)
	HandleDeadlockReport(report types.DeadlockReport)

	// Consensus
	HandleConsensusPropose(proposal types.ConsensusProposal)
	HandleConsensusVote(vote types.ConsensusVote)
	HandleConsensusCommit(commit types.ConsensusCommit)

	// Application
	HandleSeatUpdate(update types.SeatUpdate)
}

// NodeRPC is the RPC service that gets registered with net/rpc.
type NodeRPC struct {
	handler RPCHandler
}

// NewNodeRPC creates a NodeRPC backed by the given handler.
func NewNodeRPC(h RPCHandler) *NodeRPC {
	return &NodeRPC{handler: h}
}

// Empty is a placeholder reply for one-way RPCs.
type Empty struct{}

// === Bully Election RPCs ===

// BullyElection receives an ELECTION message from a lower-ID node.
func (s *NodeRPC) BullyElection(msg *types.BullyElectionMsg, reply *Empty) error {
	s.handler.HandleBullyElection(*msg)
	return nil
}

// BullyAnswer receives an ANSWER message from a higher-ID node.
func (s *NodeRPC) BullyAnswer(msg *types.BullyAnswerMsg, reply *Empty) error {
	s.handler.HandleBullyAnswer(*msg)
	return nil
}

// BullyCoordinator receives a COORDINATOR message announcing the new leader.
func (s *NodeRPC) BullyCoordinator(msg *types.BullyCoordinatorMsg, reply *Empty) error {
	s.handler.HandleBullyCoordinator(*msg)
	return nil
}

// BullyHeartbeat receives a heartbeat from the leader.
func (s *NodeRPC) BullyHeartbeat(hb *types.BullyHeartbeat, reply *Empty) error {
	s.handler.HandleBullyHeartbeat(*hb)
	return nil
}

// === Ricart-Agrawala Mutual Exclusion RPCs ===

// RARequest receives a REQUEST message for mutual exclusion.
func (s *NodeRPC) RARequest(req *types.RARequest, reply *Empty) error {
	s.handler.HandleRARequest(*req)
	return nil
}

// RAReply receives a REPLY message for mutual exclusion.
func (s *NodeRPC) RAReply(r *types.RAReply, reply *Empty) error {
	s.handler.HandleRAReply(*r)
	return nil
}

// === Deadlock Detection RPCs ===

// DeadlockProbe receives a PROBE message from the leader.
func (s *NodeRPC) DeadlockProbe(probe *types.DeadlockProbe, reply *Empty) error {
	s.handler.HandleDeadlockProbe(*probe)
	return nil
}

// DeadlockReport receives a REPORT message from a peer.
func (s *NodeRPC) DeadlockReport(report *types.DeadlockReport, reply *Empty) error {
	s.handler.HandleDeadlockReport(*report)
	return nil
}

// === Consensus RPCs ===

// ConsensusPropose receives a PROPOSE message from the leader.
func (s *NodeRPC) ConsensusPropose(proposal *types.ConsensusProposal, reply *Empty) error {
	s.handler.HandleConsensusPropose(*proposal)
	return nil
}

// ConsensusVote receives a VOTE message from a follower.
func (s *NodeRPC) ConsensusVote(vote *types.ConsensusVote, reply *Empty) error {
	s.handler.HandleConsensusVote(*vote)
	return nil
}

// ConsensusCommit receives a COMMIT/ABORT message from the leader.
func (s *NodeRPC) ConsensusCommit(commit *types.ConsensusCommit, reply *Empty) error {
	s.handler.HandleConsensusCommit(*commit)
	return nil
}

// === Application RPCs ===

// SeatUpdate receives a seat status update from a peer.
func (s *NodeRPC) SeatUpdate(update *types.SeatUpdate, reply *Empty) error {
	s.handler.HandleSeatUpdate(*update)
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Server helpers
// ──────────────────────────────────────────────────────────────────────────────

// StartRPCServer registers the service and accepts connections on the listener.
func StartRPCServer(ln net.Listener, service *NodeRPC) {
	server := rpc.NewServer()
	server.RegisterName("Node", service)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go server.ServeConn(conn)
		}
	}()
}

// ──────────────────────────────────────────────────────────────────────────────
// Client helpers — thin wrappers over net/rpc.Client for each RPC method.
// ──────────────────────────────────────────────────────────────────────────────

// RPCClient wraps a net/rpc.Client for a specific peer.
type RPCClient struct {
	addr      string
	prefix    string
	mu        sync.Mutex
	reachable bool // last known state; used to suppress repeated failure logs
}

// NewRPCClient creates a client that dials the given address.
func NewRPCClient(addr, prefix string) *RPCClient {
	return &RPCClient{addr: addr, prefix: prefix, reachable: true}
}

// dial connects to the peer with a single fast attempt.
func (c *RPCClient) dial() (*rpc.Client, error) {
	conn, err := net.DialTimeout("tcp", c.addr, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("dial %s failed: %w", c.addr, err)
	}
	return rpc.NewClient(conn), nil
}

// callAsync makes a non-blocking RPC call (fire and forget with logging).
func (c *RPCClient) callAsync(method string, args interface{}) {
	client, err := c.dial()
	if err != nil {
		c.mu.Lock()
		if c.reachable {
			fmt.Printf("%s[WARN] Peer %s unreachable (will suppress further failures)\n", c.prefix, c.addr)
			c.reachable = false
		}
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	if !c.reachable {
		fmt.Printf("%s[OK] Peer %s is reachable again\n", c.prefix, c.addr)
		c.reachable = true
	}
	c.mu.Unlock()
	defer client.Close()

	var reply Empty
	if err := client.Call(method, args, &reply); err != nil {
		fmt.Printf("%s[WARN] RPC call %s failed: %v\n", c.prefix, method, err)
	}
}

// === Bully Election Client Methods ===

func (c *RPCClient) SendBullyElection(msg types.BullyElectionMsg) {
	c.callAsync("Node.BullyElection", &msg)
}

func (c *RPCClient) SendBullyAnswer(msg types.BullyAnswerMsg) {
	c.callAsync("Node.BullyAnswer", &msg)
}

func (c *RPCClient) SendBullyCoordinator(msg types.BullyCoordinatorMsg) {
	c.callAsync("Node.BullyCoordinator", &msg)
}

func (c *RPCClient) SendBullyHeartbeat(hb types.BullyHeartbeat) {
	c.callAsync("Node.BullyHeartbeat", &hb)
}

// === Ricart-Agrawala Client Methods ===

func (c *RPCClient) SendRARequest(req types.RARequest) {
	c.callAsync("Node.RARequest", &req)
}

func (c *RPCClient) SendRAReply(reply types.RAReply) {
	c.callAsync("Node.RAReply", &reply)
}

// === Deadlock Detection Client Methods ===

func (c *RPCClient) SendDeadlockProbe(probe types.DeadlockProbe) {
	c.callAsync("Node.DeadlockProbe", &probe)
}

func (c *RPCClient) SendDeadlockReport(report types.DeadlockReport) {
	c.callAsync("Node.DeadlockReport", &report)
}

// === Consensus Client Methods ===

func (c *RPCClient) SendConsensusPropose(proposal types.ConsensusProposal) {
	c.callAsync("Node.ConsensusPropose", &proposal)
}

func (c *RPCClient) SendConsensusVote(vote types.ConsensusVote) {
	c.callAsync("Node.ConsensusVote", &vote)
}

func (c *RPCClient) SendConsensusCommit(commit types.ConsensusCommit) {
	c.callAsync("Node.ConsensusCommit", &commit)
}

// === Application Client Methods ===

func (c *RPCClient) SendSeatUpdate(update types.SeatUpdate) {
	c.callAsync("Node.SeatUpdate", &update)
}
