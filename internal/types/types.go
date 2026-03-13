package types

import (
	"fmt"
	"time"
)

const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	ColorBold   = "\033[1m"
)

func LogPrefix(nodeID int, role string) string {
	colors := []string{ColorGreen, ColorBlue, ColorPurple, ColorCyan, ColorYellow, ColorRed}
	color := colors[nodeID%len(colors)]
	return fmt.Sprintf("%s%s[Node %d | %s]%s ", color, ColorBold, nodeID, role, ColorReset)
}

type MessageType string

const (
	// Bully Leader Election
	MsgBullyElection    MessageType = "BULLY_ELECTION"
	MsgBullyAnswer      MessageType = "BULLY_ANSWER"
	MsgBullyCoordinator MessageType = "BULLY_COORDINATOR"
	MsgBullyHeartbeat   MessageType = "BULLY_HEARTBEAT"

	// Ricart-Agrawala Mutual Exclusion
	MsgRARequest MessageType = "RA_REQUEST"
	MsgRAReply   MessageType = "RA_REPLY"

	// Deadlock Detection
	MsgDeadlockProbe  MessageType = "DEADLOCK_PROBE"
	MsgDeadlockReport MessageType = "DEADLOCK_REPORT"

	// Consensus (Simple Majority Vote)
	MsgConsensusPropose MessageType = "CONSENSUS_PROPOSE"
	MsgConsensusVote    MessageType = "CONSENSUS_VOTE"
	MsgConsensusCommit  MessageType = "CONSENSUS_COMMIT"

	// Application-level messages (Ticket Booking)
	MsgBookSeat   MessageType = "BOOK_SEAT"
	MsgCancelSeat MessageType = "CANCEL_SEAT"
	MsgViewSeats  MessageType = "VIEW_SEATS"
	MsgSeatUpdate MessageType = "SEAT_UPDATE"
)

type Message struct {
	Type      MessageType `json:"type"`
	SenderID  int         `json:"sender_id"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

// === Node States (Bully Election) ===

type NodeState int

const (
	Follower  NodeState = iota
	Candidate
	Leader
)

func (s NodeState) String() string {
	switch s {
	case Follower:
		return "FOLLOWER"
	case Candidate:
		return "CANDIDATE"
	case Leader:
		return "LEADER"
	default:
		return "UNKNOWN"
	}
}

// === Bully Leader Election Types ===

type BullyElectionMsg struct {
	SenderID int `json:"sender_id"`
}

type BullyAnswerMsg struct {
	SenderID int `json:"sender_id"`
}

type BullyCoordinatorMsg struct {
	LeaderID int `json:"leader_id"`
}

type BullyHeartbeat struct {
	LeaderID int `json:"leader_id"`
}

// === Ricart-Agrawala Mutual Exclusion Types ===

type RARequest struct {
	SenderID  int       `json:"sender_id"`
	Timestamp time.Time `json:"timestamp"`
	Resource  string    `json:"resource"`
}

type RAReply struct {
	SenderID int    `json:"sender_id"`
	Resource string `json:"resource"`
}

// === Deadlock Detection Types ===

// DeadlockProbe is sent by the leader to collect wait-for graph edges.
type DeadlockProbe struct {
	InitiatorID int `json:"initiator_id"`
}

// DeadlockWaitInfo reports which resource a node is waiting for (if any).
type DeadlockWaitInfo struct {
	NodeID         int    `json:"node_id"`
	WaitingFor     string `json:"waiting_for"`     // resource name, "" if not waiting
	HeldResources  []string `json:"held_resources"`  // resources currently held
	IsWaiting      bool   `json:"is_waiting"`
}

// DeadlockReport is sent from each node back to the leader with its wait state.
type DeadlockReport struct {
	SenderID int              `json:"sender_id"`
	WaitInfo DeadlockWaitInfo `json:"wait_info"`
}

// === Consensus Types (Simple Majority Vote) ===

type ChangeType string

const (
	ChangeAddServer  ChangeType = "ADD_SERVER"
	ChangeBookSeat   ChangeType = "BOOK_SEAT"
	ChangeCancelSeat ChangeType = "CANCEL_SEAT"
)

type ConsensusProposal struct {
	ProposalID string     `json:"proposal_id"`
	LeaderID   int        `json:"leader_id"`
	Change     ChangeType `json:"change_type"`
	Key        string     `json:"key"`
	Value      string     `json:"value"`
}

type ConsensusVote struct {
	VoterID    int    `json:"voter_id"`
	ProposalID string `json:"proposal_id"`
	Accept     bool   `json:"accept"`
}

type ConsensusCommit struct {
	ProposalID string     `json:"proposal_id"`
	LeaderID   int        `json:"leader_id"`
	Change     ChangeType `json:"change_type"`
	Key        string     `json:"key"`
	Value      string     `json:"value"`
	Committed  bool       `json:"committed"` // true = commit, false = abort
}

// === Lock Response (external API for critical section access) ===

type LockResponse struct {
	Resource string `json:"resource"`
	Granted  bool   `json:"granted"`
	Holder   string `json:"holder"`
	Reason   string `json:"reason"`
}

// === Seat Update (peer synchronization) ===

type SeatUpdate struct {
	SeatNum  int    `json:"seat_num"`
	Status   string `json:"status"`
	SenderID int    `json:"sender_id"`
}

// === Seat Database Types ===

const TotalSeats = 10

type SeatDatabase struct {
	Seats map[int]string `json:"seats"`
}

func NewSeatDatabase() *SeatDatabase {
	db := &SeatDatabase{
		Seats: make(map[int]string),
	}
	for i := 1; i <= TotalSeats; i++ {
		db.Seats[i] = "available"
	}
	return db
}
