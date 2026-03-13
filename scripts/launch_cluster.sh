#!/bin/bash
# Launch 4-node distributed coordination cluster across machines on same WiFi
#
# Network Layout:
#   Node 0 (Server): 10.207.4.131:9000
#   Node 1 (Member 2): 10.207.4.156:9001
#   Node 2 (Member 3): 10.207.4.197:9002
#   Node 3 (Member 4): 10.207.4.193:9003
#
# Run the appropriate command on each machine.

echo "=== Distributed Coordination System - 4 Node Cluster ==="
echo ""
echo "Run the following command on each machine:"
echo ""
echo "--- Node 0 (Server - 10.207.4.131) ---"
echo 'go run ./cmd/coordinator/ -id=0 -addr=:9000 -peers="1=10.207.4.156:9001,2=10.207.4.197:9002,3=10.207.4.193:9003"'
echo ""
echo "--- Node 1 (Member 2 - 10.207.4.156) ---"
echo 'go run ./cmd/coordinator/ -id=1 -addr=:9001 -peers="0=10.207.4.131:9000,2=10.207.4.197:9002,3=10.207.4.193:9003"'
echo ""
echo "--- Node 2 (Member 3 - 10.207.4.197) ---"
echo 'go run ./cmd/coordinator/ -id=2 -addr=:9002 -peers="0=10.207.4.131:9000,1=10.207.4.156:9001,3=10.207.4.193:9003"'
echo ""
echo "--- Node 3 (Member 4 - 10.207.4.193) ---"
echo 'go run ./cmd/coordinator/ -id=3 -addr=:9003 -peers="0=10.207.4.131:9000,1=10.207.4.156:9001,2=10.207.4.197:9002"'
echo ""
echo "=== All 4 algorithms will start automatically ==="
echo "  1. Bully Leader Election (highest ID wins)"
echo "  2. Ricart-Agrawala Mutual Exclusion"
echo "  3. Deadlock Detection (Wait-For Graph + DFS)"
echo "  4. Simple Majority-Vote Consensus"
