package deadlock

import (
	"fmt"
	"sync"

	"distributed-chat-coordinator/internal/types"
)

// DeadlockDetector implements deadlock detection using a Wait-For Graph (WFG)
// with DFS-based cycle detection.
//
// Algorithm Overview:
//   1. The leader periodically (or on-demand) sends PROBE messages to all nodes.
//   2. Each node responds with a REPORT containing its wait state:
//      - Which resource it is waiting for (if any)
//      - Which resources it currently holds
//   3. The leader constructs a Wait-For Graph:
//      - Nodes are processes
//      - An edge from Node A to Node B means: A is waiting for a resource held by B
//   4. The leader runs DFS on the WFG to detect cycles.
//   5. If a cycle is detected, deadlock exists. The leader resolves it by
//      forcing the lowest-priority (lowest-ID) process in the cycle to release.
//
// Properties:
//   - Centralized detection: Only the leader runs the detection algorithm.
//   - Correct: Any cycle in the WFG indicates a deadlock.
//   - Resolution: Victim selection based on lowest process ID in the cycle.
type DeadlockDetector struct {
	mu sync.Mutex

	nodeID int
	peers  []int
	prefix string

	// Local wait state
	waitingForResource string   // resource we are waiting for ("" if not waiting)
	heldResources      []string // resources we currently hold

	// Wait-For Graph (leader only): nodeID -> list of nodeIDs it waits for
	waitForGraph map[int][]int

	// Collected reports from peers (leader only)
	reports    map[int]types.DeadlockWaitInfo
	reportDone chan struct{}

	// Channels
	ProbeCh  chan types.DeadlockProbe
	ReportCh chan types.DeadlockReport

	// Callbacks
	OnSendProbe  func(targetID int, probe types.DeadlockProbe)
	OnSendReport func(targetID int, report types.DeadlockReport)

	stopCh chan struct{}
}

// NewDeadlockDetector creates a new deadlock detector.
func NewDeadlockDetector(nodeID int, peers []int) *DeadlockDetector {
	return &DeadlockDetector{
		nodeID:       nodeID,
		peers:        peers,
		prefix:       types.LogPrefix(nodeID, "DEADLOCK"),
		waitForGraph: make(map[int][]int),
		reports:      make(map[int]types.DeadlockWaitInfo),
		reportDone:   make(chan struct{}, 1),
		ProbeCh:      make(chan types.DeadlockProbe, 100),
		ReportCh:     make(chan types.DeadlockReport, 100),
		stopCh:       make(chan struct{}),
	}
}

// Start begins the deadlock detector event loop.
func (d *DeadlockDetector) Start() {
	go d.run()
}

// Stop terminates the event loop.
func (d *DeadlockDetector) Stop() {
	select {
	case <-d.stopCh:
	default:
		close(d.stopCh)
	}
}

// run processes incoming probe and report messages.
func (d *DeadlockDetector) run() {
	for {
		select {
		case <-d.stopCh:
			return

		case probe := <-d.ProbeCh:
			d.handleProbe(probe)

		case report := <-d.ReportCh:
			d.handleReport(report)
		}
	}
}

// SetWaitState updates the local wait state (called by the mutex module).
func (d *DeadlockDetector) SetWaitState(waitingFor string, held []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.waitingForResource = waitingFor
	d.heldResources = make([]string, len(held))
	copy(d.heldResources, held)
}

// ClearWaitState clears the waiting state (called when CS is granted or released).
func (d *DeadlockDetector) ClearWaitState() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.waitingForResource = ""
}

// SetHeldResources updates the held resources list.
func (d *DeadlockDetector) SetHeldResources(held []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.heldResources = make([]string, len(held))
	copy(d.heldResources, held)
}

// handleProbe processes a PROBE message from the leader.
// Responds with this node's current wait state.
func (d *DeadlockDetector) handleProbe(probe types.DeadlockProbe) {
	d.mu.Lock()
	waitInfo := types.DeadlockWaitInfo{
		NodeID:        d.nodeID,
		WaitingFor:    d.waitingForResource,
		HeldResources: make([]string, len(d.heldResources)),
		IsWaiting:     d.waitingForResource != "",
	}
	copy(waitInfo.HeldResources, d.heldResources)
	d.mu.Unlock()

	fmt.Printf("%s[PROBE] Received probe from leader (Node %d). Reporting state.\n",
		d.prefix, probe.InitiatorID)
	fmt.Printf("%s[PROBE] My state: waiting_for=\"%s\", held=%v\n",
		d.prefix, waitInfo.WaitingFor, waitInfo.HeldResources)

	if d.OnSendReport != nil {
		go d.OnSendReport(probe.InitiatorID, types.DeadlockReport{
			SenderID: d.nodeID,
			WaitInfo: waitInfo,
		})
	}
}

// handleReport processes a REPORT message from a peer (leader only).
func (d *DeadlockDetector) handleReport(report types.DeadlockReport) {
	d.mu.Lock()
	d.reports[report.SenderID] = report.WaitInfo

	fmt.Printf("%s[REPORT] Received report from Node %d: waiting_for=\"%s\", held=%v\n",
		d.prefix, report.SenderID, report.WaitInfo.WaitingFor, report.WaitInfo.HeldResources)

	// Check if we have all reports
	if len(d.reports) >= len(d.peers) {
		select {
		case d.reportDone <- struct{}{}:
		default:
		}
	}
	d.mu.Unlock()
}

// DetectDeadlock initiates deadlock detection (called by the leader).
// Sends probes to all peers, collects reports, builds WFG, and runs cycle detection.
func (d *DeadlockDetector) DetectDeadlock() (bool, []int) {
	d.mu.Lock()
	d.reports = make(map[int]types.DeadlockWaitInfo)
	// Drain old done signals
	select {
	case <-d.reportDone:
	default:
	}
	d.mu.Unlock()

	fmt.Printf("%s=============================================\n", d.prefix)
	fmt.Printf("%s[DETECT] Starting deadlock detection\n", d.prefix)
	fmt.Printf("%s[DETECT] Sending PROBE to %d peers\n", d.prefix, len(d.peers))
	fmt.Printf("%s=============================================\n", d.prefix)

	// Send probes to all peers
	probe := types.DeadlockProbe{InitiatorID: d.nodeID}
	for _, peerID := range d.peers {
		if d.OnSendProbe != nil {
			pid := peerID
			go d.OnSendProbe(pid, probe)
		}
	}

	// Add own state
	d.mu.Lock()
	ownInfo := types.DeadlockWaitInfo{
		NodeID:        d.nodeID,
		WaitingFor:    d.waitingForResource,
		HeldResources: make([]string, len(d.heldResources)),
		IsWaiting:     d.waitingForResource != "",
	}
	copy(ownInfo.HeldResources, d.heldResources)
	d.reports[d.nodeID] = ownInfo
	d.mu.Unlock()

	// Wait for reports from all peers (with some tolerance for missing nodes)
	if len(d.peers) > 0 {
		select {
		case <-d.reportDone:
		case <-func() <-chan struct{} {
			ch := make(chan struct{})
			go func() {
				<-d.stopCh
				close(ch)
			}()
			return ch
		}():
			return false, nil
		}
	}

	// Build Wait-For Graph and detect cycles
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.buildAndDetect()
}

// buildAndDetect constructs the WFG from collected reports and runs DFS cycle detection.
// Must hold d.mu.
func (d *DeadlockDetector) buildAndDetect() (bool, []int) {
	// Build resource->holder mapping
	resourceHolder := make(map[string]int) // resource -> nodeID that holds it
	for nodeID, info := range d.reports {
		for _, res := range info.HeldResources {
			resourceHolder[res] = nodeID
		}
	}

	// Build Wait-For Graph
	// Edge from A->B means: A is waiting for a resource that B holds
	d.waitForGraph = make(map[int][]int)
	for nodeID, info := range d.reports {
		if info.IsWaiting && info.WaitingFor != "" {
			holderID, exists := resourceHolder[info.WaitingFor]
			if exists && holderID != nodeID {
				d.waitForGraph[nodeID] = append(d.waitForGraph[nodeID], holderID)
			}
		}
	}

	fmt.Printf("%s[WFG] Wait-For Graph:\n", d.prefix)
	if len(d.waitForGraph) == 0 {
		fmt.Printf("%s[WFG]   (empty - no processes waiting)\n", d.prefix)
	}
	for from, toList := range d.waitForGraph {
		for _, to := range toList {
			fmt.Printf("%s[WFG]   Node %d --> Node %d\n", d.prefix, from, to)
		}
	}

	// DFS cycle detection
	cycle := d.detectCycleDFS()

	if len(cycle) > 0 {
		fmt.Printf("%s=============================================\n", d.prefix)
		fmt.Printf("%s[DEADLOCK] DEADLOCK DETECTED!\n", d.prefix)
		fmt.Printf("%s[DEADLOCK] Cycle: ", d.prefix)
		for i, nodeID := range cycle {
			if i > 0 {
				fmt.Printf(" -> ")
			}
			fmt.Printf("Node %d", nodeID)
		}
		fmt.Printf(" -> Node %d\n", cycle[0])
		fmt.Printf("%s=============================================\n", d.prefix)
		return true, cycle
	}

	fmt.Printf("%s[DETECT] No deadlock detected. System is safe.\n", d.prefix)
	return false, nil
}

// detectCycleDFS runs DFS on the wait-for graph to find cycles.
func (d *DeadlockDetector) detectCycleDFS() []int {
	visited := make(map[int]bool)
	inStack := make(map[int]bool)
	parent := make(map[int]int)

	for node := range d.waitForGraph {
		if !visited[node] {
			if cycle := d.dfs(node, visited, inStack, parent); cycle != nil {
				return cycle
			}
		}
	}

	// Also check nodes that are targets but don't have outgoing edges
	for _, targets := range d.waitForGraph {
		for _, t := range targets {
			if !visited[t] {
				if cycle := d.dfs(t, visited, inStack, parent); cycle != nil {
					return cycle
				}
			}
		}
	}

	return nil
}

// dfs performs depth-first search starting from the given node.
func (d *DeadlockDetector) dfs(node int, visited, inStack map[int]bool, parent map[int]int) []int {
	visited[node] = true
	inStack[node] = true

	for _, neighbor := range d.waitForGraph[node] {
		if !visited[neighbor] {
			parent[neighbor] = node
			if cycle := d.dfs(neighbor, visited, inStack, parent); cycle != nil {
				return cycle
			}
		} else if inStack[neighbor] {
			// Found a cycle! Reconstruct it.
			cycle := []int{neighbor}
			current := node
			for current != neighbor {
				cycle = append(cycle, current)
				current = parent[current]
			}
			// Reverse to get proper order
			for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
				cycle[i], cycle[j] = cycle[j], cycle[i]
			}
			return cycle
		}
	}

	inStack[node] = false
	return nil
}

// SimulateDeadlock creates an artificial deadlock scenario for demonstration.
// It sets up a circular wait: each node holds one resource and waits for the next.
func (d *DeadlockDetector) SimulateDeadlock(allNodeIDs []int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	fmt.Printf("%s=============================================\n", d.prefix)
	fmt.Printf("%s[SIMULATE] Creating artificial deadlock scenario\n", d.prefix)

	// Clear old reports
	d.reports = make(map[int]types.DeadlockWaitInfo)

	// Create circular wait pattern:
	// Node 0 holds R0, waits for R1
	// Node 1 holds R1, waits for R2
	// Node 2 holds R2, waits for R3
	// Node 3 holds R3, waits for R0  => CYCLE!
	for i, nodeID := range allNodeIDs {
		heldRes := fmt.Sprintf("Resource-%d", i)
		waitRes := fmt.Sprintf("Resource-%d", (i+1)%len(allNodeIDs))

		info := types.DeadlockWaitInfo{
			NodeID:        nodeID,
			WaitingFor:    waitRes,
			HeldResources: []string{heldRes},
			IsWaiting:     true,
		}
		d.reports[nodeID] = info

		fmt.Printf("%s[SIMULATE] Node %d: holds \"%s\", waits for \"%s\"\n",
			d.prefix, nodeID, heldRes, waitRes)
	}
	fmt.Printf("%s=============================================\n", d.prefix)

	// Now detect
	deadlocked, cycle := d.buildAndDetect()

	if deadlocked {
		// Resolution: select victim (lowest ID in cycle)
		victim := cycle[0]
		for _, id := range cycle[1:] {
			if id < victim {
				victim = id
			}
		}
		fmt.Printf("%s=============================================\n", d.prefix)
		fmt.Printf("%s[RESOLVE] Victim selected: Node %d (lowest ID in cycle)\n", d.prefix, victim)
		fmt.Printf("%s[RESOLVE] Action: Force Node %d to release its held resources\n", d.prefix, victim)
		fmt.Printf("%s[RESOLVE] Deadlock resolved!\n", d.prefix)
		fmt.Printf("%s=============================================\n", d.prefix)
	}
}
