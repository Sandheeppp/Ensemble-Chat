package consensus

import (
	"fmt"
	"sync"
	"time"

	"distributed-chat-coordinator/internal/types"
)

const (
	// ConsensusTimeout is how long the leader waits for majority votes.
	ConsensusTimeout = 5 * time.Second
)

// SimpleConsensus implements a simple majority-vote consensus algorithm.
//
// Algorithm Overview:
//   1. The leader receives a proposal (e.g., book a seat).
//   2. The leader broadcasts a PROPOSE message to all followers.
//   3. Each follower votes ACCEPT or REJECT and sends a VOTE message back.
//   4. If the leader receives ACCEPT from a majority (>= N/2 + 1 including itself),
//      it broadcasts a COMMIT message to all.
//   5. If not enough votes, the leader broadcasts an ABORT message.
//   6. On COMMIT, all nodes apply the change to their local state machine.
//
// Properties:
//   - Safety: Only committed proposals are applied.
//   - Liveness: With majority alive, proposals are committed.
//   - Consistency: All nodes apply the same committed proposals.
type SimpleConsensus struct {
	mu sync.Mutex

	nodeID int
	peers  []int
	prefix string
	isLeader bool

	// State machine: routing table / server registry
	routingTable map[string]string // key -> value

	// Current proposal tracking (leader only)
	currentProposalID string
	votesReceived     int
	votesNeeded       int
	acceptCount       int
	votesDone         chan bool // true = committed, false = aborted

	// Channels for incoming messages
	ProposeCh chan types.ConsensusProposal
	VoteCh    chan types.ConsensusVote
	CommitCh  chan types.ConsensusCommit

	// Callbacks
	OnSendPropose func(targetID int, proposal types.ConsensusProposal)
	OnSendVote    func(targetID int, vote types.ConsensusVote)
	OnSendCommit  func(commit types.ConsensusCommit)

	// Application-level callback for applying committed changes
	OnApplyChange func(change types.ChangeType, key, value string)

	stopCh chan struct{}
}

// NewSimpleConsensus creates a new simple consensus manager.
func NewSimpleConsensus(nodeID int, peers []int) *SimpleConsensus {
	return &SimpleConsensus{
		nodeID:       nodeID,
		peers:        peers,
		prefix:       types.LogPrefix(nodeID, "CONSENSUS"),
		routingTable: make(map[string]string),
		votesDone:    make(chan bool, 1),
		ProposeCh:    make(chan types.ConsensusProposal, 100),
		VoteCh:       make(chan types.ConsensusVote, 100),
		CommitCh:     make(chan types.ConsensusCommit, 100),
		stopCh:       make(chan struct{}),
	}
}

// Start begins the consensus event loop.
func (c *SimpleConsensus) Start() {
	go c.run()
}

// Stop terminates the event loop.
func (c *SimpleConsensus) Stop() {
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
}

// BecomeLeader marks this node as leader.
func (c *SimpleConsensus) BecomeLeader() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isLeader = true
	fmt.Printf("%s[LEADER] This node is now the consensus leader\n", c.prefix)
}

// BecomeFollower marks this node as follower.
func (c *SimpleConsensus) BecomeFollower() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isLeader = false
}

// run processes incoming proposals, votes, and commits.
func (c *SimpleConsensus) run() {
	for {
		select {
		case <-c.stopCh:
			return

		case proposal := <-c.ProposeCh:
			c.handleProposal(proposal)

		case vote := <-c.VoteCh:
			c.handleVote(vote)

		case commit := <-c.CommitCh:
			c.handleCommit(commit)
		}
	}
}

// ProposeEntry proposes a new entry for consensus (called on leader).
// Blocks until majority accepts or timeout.
func (c *SimpleConsensus) ProposeEntry(change types.ChangeType, key, value string) bool {
	c.mu.Lock()
	proposalID := fmt.Sprintf("P-%d-%d", c.nodeID, time.Now().UnixMilli())
	c.currentProposalID = proposalID
	c.votesNeeded = (len(c.peers)+1)/2 + 1 // majority including self
	c.acceptCount = 1                        // leader votes for itself
	c.votesReceived = 1

	// Drain old signal
	select {
	case <-c.votesDone:
	default:
	}

	proposal := types.ConsensusProposal{
		ProposalID: proposalID,
		LeaderID:   c.nodeID,
		Change:     change,
		Key:        key,
		Value:      value,
	}

	fmt.Printf("%s=============================================\n", c.prefix)
	fmt.Printf("%s[PROPOSE] Proposal %s: %s %s=%s\n",
		c.prefix, proposalID, change, key, value)
	fmt.Printf("%s[PROPOSE] Broadcasting to %d peers (need %d/%d votes)\n",
		c.prefix, len(c.peers), c.votesNeeded, len(c.peers)+1)
	fmt.Printf("%s=============================================\n", c.prefix)

	c.mu.Unlock()

	// Broadcast proposal to all peers
	for _, peerID := range c.peers {
		if c.OnSendPropose != nil {
			pid := peerID
			go c.OnSendPropose(pid, proposal)
		}
	}

	// If no peers, commit immediately
	if len(c.peers) == 0 {
		c.applyChange(change, key, value)
		return true
	}

	// Wait for majority votes
	select {
	case committed := <-c.votesDone:
		if committed {
			fmt.Printf("%s[COMMITTED] Proposal %s COMMITTED by majority\n",
				c.prefix, proposalID)

			// Broadcast COMMIT to all peers
			commit := types.ConsensusCommit{
				ProposalID: proposalID,
				LeaderID:   c.nodeID,
				Change:     change,
				Key:        key,
				Value:      value,
				Committed:  true,
			}
			if c.OnSendCommit != nil {
				go c.OnSendCommit(commit)
			}

			// Apply locally
			c.applyChange(change, key, value)
			return true
		} else {
			fmt.Printf("%s[ABORTED] Proposal %s ABORTED (not enough votes)\n",
				c.prefix, proposalID)

			// Broadcast ABORT
			commit := types.ConsensusCommit{
				ProposalID: proposalID,
				LeaderID:   c.nodeID,
				Change:     change,
				Key:        key,
				Value:      value,
				Committed:  false,
			}
			if c.OnSendCommit != nil {
				go c.OnSendCommit(commit)
			}
			return false
		}

	case <-time.After(ConsensusTimeout):
		fmt.Printf("%s[TIMEOUT] Proposal %s timed out\n", c.prefix, proposalID)
		return false
	}
}

// handleProposal processes a PROPOSE message from the leader (follower only).
func (c *SimpleConsensus) handleProposal(proposal types.ConsensusProposal) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fmt.Printf("%s[PROPOSAL] Received proposal %s from Leader %d: %s %s=%s\n",
		c.prefix, proposal.ProposalID, proposal.LeaderID,
		proposal.Change, proposal.Key, proposal.Value)

	// Always accept (in this simple implementation)
	vote := types.ConsensusVote{
		VoterID:    c.nodeID,
		ProposalID: proposal.ProposalID,
		Accept:     true,
	}

	fmt.Printf("%s[VOTE] Voting ACCEPT for proposal %s\n", c.prefix, proposal.ProposalID)

	if c.OnSendVote != nil {
		go c.OnSendVote(proposal.LeaderID, vote)
	}
}

// handleVote processes a VOTE message from a follower (leader only).
func (c *SimpleConsensus) handleVote(vote types.ConsensusVote) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if vote.ProposalID != c.currentProposalID {
		return // stale vote
	}

	c.votesReceived++
	if vote.Accept {
		c.acceptCount++
	}

	fmt.Printf("%s[VOTE] Received %s from Node %d (%d/%d accepts, %d/%d total)\n",
		c.prefix, func() string {
			if vote.Accept {
				return "ACCEPT"
			}
			return "REJECT"
		}(), vote.VoterID, c.acceptCount, c.votesNeeded,
		c.votesReceived, len(c.peers)+1)

	// Check if we have enough votes
	totalPossible := len(c.peers) + 1
	if c.acceptCount >= c.votesNeeded {
		select {
		case c.votesDone <- true:
		default:
		}
	} else if c.votesReceived >= totalPossible {
		// All votes in, not enough accepts
		select {
		case c.votesDone <- false:
		default:
		}
	}
}

// handleCommit processes a COMMIT/ABORT message from the leader (follower only).
func (c *SimpleConsensus) handleCommit(commit types.ConsensusCommit) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if commit.Committed {
		fmt.Printf("%s[COMMIT] Applying committed proposal %s: %s %s=%s\n",
			c.prefix, commit.ProposalID, commit.Change, commit.Key, commit.Value)
		c.mu.Unlock()
		c.applyChange(commit.Change, commit.Key, commit.Value)
		c.mu.Lock()
	} else {
		fmt.Printf("%s[ABORT] Proposal %s was aborted by leader\n",
			c.prefix, commit.ProposalID)
	}
}

// applyChange applies a change to the local state machine.
func (c *SimpleConsensus) applyChange(change types.ChangeType, key, value string) {
	c.mu.Lock()
	c.routingTable[key] = value
	c.mu.Unlock()

	fmt.Printf("%s[APPLIED] %s: %s = %s\n", c.prefix, change, key, value)

	if c.OnApplyChange != nil {
		c.OnApplyChange(change, key, value)
	}
}

// GetRoutingTable returns a copy of the routing table.
func (c *SimpleConsensus) GetRoutingTable() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()

	copied := make(map[string]string)
	for k, v := range c.routingTable {
		copied[k] = v
	}
	return copied
}
