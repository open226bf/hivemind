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

	"github.com/orange/hivemind/internal/ports"
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

// attachMarked wires a loopback yamux session whose agent side writes a single
// marker byte on every accepted stream, so a caller can tell which session
// served a given dial. It returns a stop func that drops the session and waits
// for the hub to fully deregister it.
func attachMarked(t *testing.T, h *Hub, agentID string, node ports.AgentNode, marker byte) (stop func()) {
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
			go func(st net.Conn) {
				_, _ = st.Write([]byte{marker})
				_, _ = io.Copy(io.Discard, st)
				_ = st.Close()
			}(st)
		}
	}()
	done := make(chan struct{})
	go func() { _ = h.Attach(agentID, node.NodeID, node, c1); close(done) }()
	return func() { _ = c1.Close(); <-done }
}

// dialMarker opens one stream through dial and returns the agent's marker byte.
func dialMarker(t *testing.T, dial func(context.Context, string, string) (net.Conn, error)) byte {
	t.Helper()
	conn, err := dial(context.Background(), "", "")
	require.NoError(t, err)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 1)
	_, err = io.ReadFull(conn, b)
	require.NoError(t, err)
	return b[0]
}

// TestManagerDialer_FollowsReconnect locks the fix for the stale-session bug:
// the orchestrator dialer is resolved per call, so a dialer captured once (as
// the registry caches it) keeps reaching the current live session after the
// agent's tunnel drops and reconnects.
func TestManagerDialer_FollowsReconnect(t *testing.T) {
	h := New(time.Minute)
	node := ports.AgentNode{NodeID: "m1", Role: "manager", IsManager: true, IsLeader: true}

	stopA := attachMarked(t, h, "a1", node, 'A')
	require.Eventually(t, func() bool { _, ok := h.pickManager("a1"); return ok },
		2*time.Second, 20*time.Millisecond, "first session should register")

	// The registry resolves and caches the orchestrator (its dialer) once.
	dial := h.managerDialer("a1")
	require.Equal(t, byte('A'), dialMarker(t, dial), "dial should reach the first session")

	// Tunnel drops; a fresh session attaches for the same node (reconnect).
	stopA()
	stopB := attachMarked(t, h, "a1", node, 'B')
	defer stopB()
	require.Eventually(t, func() bool {
		ns, ok := h.pickManager("a1")
		return ok && !ns.session.IsClosed()
	}, 2*time.Second, 20*time.Millisecond, "reconnected session should register")

	// The SAME cached dialer must now reach the new session, not the dead one.
	require.Equal(t, byte('B'), dialMarker(t, dial), "dial must follow the reconnect")
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
