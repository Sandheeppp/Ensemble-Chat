package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"distributed-chat-coordinator/internal/coordinator"
	"distributed-chat-coordinator/internal/types"
)

func main() {
	id := flag.Int("id", 0, "Node ID (unique integer)")
	addr := flag.String("addr", ":9000", "Listen address for this coordinator")
	peers := flag.String("peers", "", "Comma-separated peer list: id=host:port,id=host:port,...")
	flag.Parse()

	fmt.Printf("%s%s[START] Starting Distributed Coordinator Node %d at %s%s\n",
		types.ColorGreen, types.ColorBold, *id, *addr, types.ColorReset)

	// Parse peers into map[int]string and collect peer IDs
	peerAddrs := make(map[int]string)
	var peerIDs []int

	if *peers != "" {
		for _, entry := range strings.Split(*peers, ",") {
			parts := strings.SplitN(entry, "=", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "invalid peer format %q (expected id=host:port)\n", entry)
				os.Exit(1)
			}
			pid, err := strconv.Atoi(parts[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid peer id %q: %v\n", parts[0], err)
				os.Exit(1)
			}
			peerAddrs[pid] = parts[1]
			peerIDs = append(peerIDs, pid)
		}
	}

	node := coordinator.NewCoordinatorNode(*id, peerIDs)
	node.Addr = *addr
	node.PeerAddrs = peerAddrs

	// Start net/rpc server before election so peers can connect
	if err := node.StartRPC(); err != nil {
		fmt.Fprintf(os.Stderr, "RPC start failed: %v\n", err)
		os.Exit(1)
	}

	// Wire RPC-based send callbacks
	coordinator.SetupRPCComm([]*coordinator.CoordinatorNode{node})

	// Begin all algorithm modules
	node.Start()

	fmt.Printf("%s%s[READY] Distributed Coordinator Node %d running. Type 'help' for commands.%s\n",
		types.ColorCyan, types.ColorBold, *id, types.ColorReset)
	fmt.Println()

	// Handle Ctrl+C in background
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n%s%s[SHUTDOWN] Shutting down Node %d...%s\n",
			types.ColorYellow, types.ColorBold, *id, types.ColorReset)
		node.Stop()
		os.Exit(0)
	}()

	// Interactive command loop
	scanner := bufio.NewScanner(os.Stdin)
	printHelp()

	for {
		fmt.Printf("%s> %s", types.ColorCyan, types.ColorReset)
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		cmd := strings.ToLower(parts[0])

		switch cmd {
		case "help":
			printHelp()

		case "status":
			state, leaderID := node.Election.GetState()
			fmt.Printf("  Node %d: State=%s, KnownLeader=%d\n",
				node.ID, state, leaderID)
			if node.Election.IsLeader() {
				fmt.Printf("  %s%s** This node IS the leader (Bully Algorithm) **%s\n",
					types.ColorGreen, types.ColorBold, types.ColorReset)
			} else {
				if leaderID >= 0 {
					fmt.Printf("  %sThis node is a follower of Leader Node %d%s\n",
						types.ColorBlue, leaderID, types.ColorReset)
				} else {
					fmt.Printf("  %sNo leader elected yet%s\n",
						types.ColorYellow, types.ColorReset)
				}
			}

		case "book":
			if len(parts) != 2 {
				fmt.Println("  Usage: book <seat_number>")
				continue
			}
			seatNum, err := strconv.Atoi(parts[1])
			if err != nil {
				fmt.Printf("  Invalid seat number: %q\n", parts[1])
				continue
			}
			success, msg := node.BookSeat(seatNum)
			if success {
				fmt.Printf("  %s%s[OK] %s%s\n",
					types.ColorGreen, types.ColorBold, msg, types.ColorReset)
			} else {
				fmt.Printf("  %s%s[FAIL] %s%s\n",
					types.ColorRed, types.ColorBold, msg, types.ColorReset)
			}

		case "cancel":
			if len(parts) != 2 {
				fmt.Println("  Usage: cancel <seat_number>")
				continue
			}
			seatNum, err := strconv.Atoi(parts[1])
			if err != nil {
				fmt.Printf("  Invalid seat number: %q\n", parts[1])
				continue
			}
			success, msg := node.CancelSeat(seatNum)
			if success {
				fmt.Printf("  %s%s[OK] %s%s\n",
					types.ColorGreen, types.ColorBold, msg, types.ColorReset)
			} else {
				fmt.Printf("  %s%s[FAIL] %s%s\n",
					types.ColorRed, types.ColorBold, msg, types.ColorReset)
			}

		case "view":
			node.ViewSeatsFormatted()

		case "lock":
			if len(parts) != 3 {
				fmt.Println("  Usage: lock <resource> <requester>")
				continue
			}
			resp := node.AcquireLock(parts[1], parts[2])
			if resp.Granted {
				fmt.Printf("  %s[OK] GRANTED critical section on \"%s\" to [%s] (via Ricart-Agrawala)%s\n",
					types.ColorGreen, resp.Resource, resp.Holder, types.ColorReset)
			} else {
				fmt.Printf("  %s[FAIL] DENIED critical section on \"%s\" -- %s%s\n",
					types.ColorRed, resp.Resource, resp.Reason, types.ColorReset)
			}

		case "unlock":
			if len(parts) != 3 {
				fmt.Println("  Usage: unlock <resource> <requester>")
				continue
			}
			ok := node.ReleaseLock(parts[1], parts[2])
			if ok {
				fmt.Printf("  %s[OK] Released critical section on \"%s\" (deferred replies sent)%s\n",
					types.ColorGreen, parts[1], types.ColorReset)
			} else {
				fmt.Printf("  %s[FAIL] Failed to release on \"%s\"%s\n",
					types.ColorRed, parts[1], types.ColorReset)
			}

		case "mutex":
			requesting, inCS, resource := node.Mutex.GetStatus()
			if inCS {
				fmt.Printf("  Ricart-Agrawala: IN CRITICAL SECTION (resource: %s)\n", resource)
			} else if requesting {
				fmt.Printf("  Ricart-Agrawala: REQUESTING critical section (resource: %s)\n", resource)
			} else {
				fmt.Println("  Ricart-Agrawala: IDLE (not in CS, not requesting)")
			}

		case "deadlock":
			if !node.Election.IsLeader() {
				fmt.Println("  [WARN] Only the leader can run deadlock detection.")
				fmt.Println("  [WARN] Current leader: Node", node.Election.GetLeaderID())
				continue
			}
			deadlocked, cycle := node.Deadlock.DetectDeadlock()
			if deadlocked {
				fmt.Printf("  %s%s[DEADLOCK] Deadlock detected! Cycle: %v%s\n",
					types.ColorRed, types.ColorBold, cycle, types.ColorReset)
			} else {
				fmt.Printf("  %s[SAFE] No deadlock detected.%s\n",
					types.ColorGreen, types.ColorReset)
			}

		case "simulate_deadlock":
			if !node.Election.IsLeader() {
				fmt.Println("  [WARN] Only the leader can simulate deadlock.")
				continue
			}
			node.Deadlock.SimulateDeadlock(node.AllNodeIDs)

		case "propose":
			if len(parts) < 4 {
				fmt.Println("  Usage: propose <change_type> <key> <value>")
				fmt.Println("  Example: propose BOOK_SEAT seat-1 booked")
				continue
			}
			changeType := types.ChangeType(strings.ToUpper(parts[1]))
			key := parts[2]
			value := strings.Join(parts[3:], " ")
			ok := node.ProposeChange("", changeType, key, value)
			if ok {
				fmt.Printf("  %s[OK] Proposal committed by majority%s\n",
					types.ColorGreen, types.ColorReset)
			} else {
				fmt.Printf("  %s[FAIL] Proposal failed%s\n",
					types.ColorRed, types.ColorReset)
			}

		case "state":
			rt := node.Consensus.GetRoutingTable()
			fmt.Println("  Consensus State (Routing Table):")
			if len(rt) == 0 {
				fmt.Println("    (empty)")
			}
			for k, v := range rt {
				fmt.Printf("    %s = %s\n", k, v)
			}

		case "quit", "exit":
			fmt.Printf("%s%s[SHUTDOWN] Shutting down Node %d...%s\n",
				types.ColorYellow, types.ColorBold, *id, types.ColorReset)
			node.Stop()
			return

		default:
			fmt.Printf("  Unknown command: %q. Type 'help' for commands.\n", cmd)
		}
	}
}

func printHelp() {
	fmt.Println(types.ColorYellow + "=== Distributed Coordination System ===" + types.ColorReset)
	fmt.Println(types.ColorCyan + "  Algorithms:" + types.ColorReset)
	fmt.Println("    1. Bully Leader Election")
	fmt.Println("    2. Ricart-Agrawala Mutual Exclusion")
	fmt.Println("    3. Deadlock Detection (Wait-For Graph + DFS)")
	fmt.Println("    4. Simple Majority-Vote Consensus")
	fmt.Println()
	fmt.Println(types.ColorCyan + "  Booking Commands:" + types.ColorReset)
	fmt.Println("    book <seat_number>                  -- Book a seat (uses Ricart-Agrawala)")
	fmt.Println("    cancel <seat_number>                -- Cancel a booking")
	fmt.Println("    view                                -- View all seat statuses")
	fmt.Println()
	fmt.Println(types.ColorCyan + "  System Commands:" + types.ColorReset)
	fmt.Println("    status                              -- Show election state (Bully)")
	fmt.Println("    lock <resource> <requester>         -- Enter critical section (Ricart-Agrawala)")
	fmt.Println("    unlock <resource> <requester>       -- Exit critical section")
	fmt.Println("    mutex                               -- Show mutual exclusion status")
	fmt.Println("    deadlock                            -- Run deadlock detection (leader only)")
	fmt.Println("    simulate_deadlock                   -- Simulate a deadlock scenario (leader only)")
	fmt.Println("    propose <type> <key> <value>        -- Propose consensus change (leader only)")
	fmt.Println("    state                               -- Show consensus state")
	fmt.Println("    help                                -- Show this help")
	fmt.Println("    quit                                -- Shutdown node")
	fmt.Println()
}
