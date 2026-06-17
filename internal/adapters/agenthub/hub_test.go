package agenthub

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/ports"
)

func TestOrchestrator_NoSession(t *testing.T) {
	h := New(time.Minute)

	// Never seen, no tunnel -> offline.
	_, err := h.Orchestrator(context.Background(), "ghost")
	assert.ErrorIs(t, err, ErrAgentOffline)

	// Heartbeating but no tunnel -> data plane unavailable.
	h.MarkSeen("a1", ports.AgentNode{NodeID: "n1"})
	_, err = h.Orchestrator(context.Background(), "a1")
	assert.ErrorIs(t, err, ErrDataPlaneUnavailable)
}

// attachNode wires a loopback yamux session for one node into the hub.
func attachNode(t *testing.T, h *Hub, agentID string, node ports.AgentNode) {
	t.Helper()
	c1, c2 := net.Pipe()
	go func() {
		s, err := yamux.Server(c2, nil)
		if err != nil {
			return
		}
		for {
			st, err := s.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(st, st) }()
		}
	}()
	go func() { _ = h.Attach(agentID, node.NodeID, node, c1) }()
}

func TestPickManager_PrefersLeaderAcrossNodes(t *testing.T) {
	h := New(time.Minute)
	attachNode(t, h, "a1", ports.AgentNode{NodeID: "w1", Role: "worker"})
	attachNode(t, h, "a1", ports.AgentNode{NodeID: "m1", Role: "manager", IsManager: true, IsLeader: true})

	require.Eventually(t, func() bool {
		ns, ok := h.pickManager("a1")
		return ok && ns.node.NodeID == "m1"
	}, 2*time.Second, 20*time.Millisecond, "orchestration must route to the leader manager, not the worker")

	// Node-scoped lookup resolves each node distinctly.
	_, okW := h.sessionForNode("a1", "w1")
	_, okM := h.sessionForNode("a1", "m1")
	require.True(t, okW)
	require.True(t, okM)
}

func TestAttach_BuildsOrchestratorOverTunnel(t *testing.T) {
	h := New(time.Minute)
	c1, c2 := net.Pipe()

	// Agent side: accept streams and echo (stands in for the docker proxy).
	go func() {
		s, err := yamux.Server(c2, nil)
		if err != nil {
			return
		}
		for {
			st, err := s.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(st, st) }()
		}
	}()

	// Server side: attach a manager node session; blocks until it closes.
	go func() {
		_ = h.Attach("a1", "node1", ports.AgentNode{NodeID: "node1", IsManager: true, IsLeader: true}, c1)
	}()

	require.Eventually(t, func() bool {
		_, ok := h.pickManager("a1")
		return ok
	}, 2*time.Second, 20*time.Millisecond, "tunnel session should register")

	orch, err := h.Orchestrator(context.Background(), "a1")
	require.NoError(t, err)
	require.NotNil(t, orch)

	// Closing the tunnel drops the session.
	h.Forget("a1")
	require.Eventually(t, func() bool {
		_, ok := h.pickManager("a1")
		return !ok
	}, 2*time.Second, 20*time.Millisecond, "session should be dropped after Forget")

	_, err = h.Orchestrator(context.Background(), "a1")
	assert.True(t, errors.Is(err, ErrAgentOffline) || errors.Is(err, ErrDataPlaneUnavailable))
}
