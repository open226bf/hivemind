// Package agenthub tracks the live sessions of Hivemind agents (the "agent"
// connection mode). The agent is a global Swarm service, so a cluster has one
// tunnel per node. The hub indexes sessions by (agent, node), routes
// orchestration to a manager node (leader-preferred) and exposes per-node
// sessions for node-scoped operations. Each session carries a yamux reverse
// tunnel that backs a Swarm Orchestrator, multiplexing Docker API calls to the
// agent's docker.sock.
package agenthub

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/orange/hivemind/internal/adapters/orchestrator"
	"github.com/orange/hivemind/internal/ports"
)

// ErrDataPlaneUnavailable is returned while an agent is present (heartbeating)
// but no usable tunnel session exists yet.
var ErrDataPlaneUnavailable = errors.New("agent is online but its control tunnel is not available yet")

// ErrAgentOffline is returned when no live session exists for an agent.
var ErrAgentOffline = errors.New("agent has no live session")

// NodePresence is the last node identity an agent task reported, plus the time.
type NodePresence struct {
	ports.AgentNode
	LastSeen time.Time
}

// nodeSession is one node's live reverse tunnel and its reported identity.
type nodeSession struct {
	session *yamux.Session
	node    ports.AgentNode
}

// Hub is the in-memory presence + per-node tunnel registry. It implements
// ports.AgentHub and ports.AgentPresence.
type Hub struct {
	offlineAfter time.Duration

	mu       sync.RWMutex
	presence map[string]NodePresence            // agentID -> last reported node
	sessions map[string]map[string]*nodeSession // agentID -> nodeID -> session
}

// New builds a hub. offlineAfter is how long after the last heartbeat an agent
// is considered offline.
func New(offlineAfter time.Duration) *Hub {
	if offlineAfter <= 0 {
		offlineAfter = 45 * time.Second
	}
	return &Hub{
		offlineAfter: offlineAfter,
		presence:     make(map[string]NodePresence),
		sessions:     make(map[string]map[string]*nodeSession),
	}
}

// Attach registers a node's reverse tunnel for an agent. With a global agent
// there is one tunnel per node, keyed by nodeID; a previous session for the same
// node is replaced. Attach blocks until the session ends, so callers run it on
// the connection's goroutine.
func (h *Hub) Attach(agentID, nodeID string, node ports.AgentNode, conn net.Conn) error {
	session, err := yamux.Client(conn, nil)
	if err != nil {
		return err
	}

	h.mu.Lock()
	if h.sessions[agentID] == nil {
		h.sessions[agentID] = make(map[string]*nodeSession)
	}
	if old := h.sessions[agentID][nodeID]; old != nil {
		_ = old.session.Close()
	}
	h.sessions[agentID][nodeID] = &nodeSession{session: session, node: node}
	h.mu.Unlock()

	<-session.CloseChan() // block until the tunnel drops

	h.mu.Lock()
	if ns := h.sessions[agentID][nodeID]; ns != nil && ns.session == session {
		delete(h.sessions[agentID], nodeID)
		if len(h.sessions[agentID]) == 0 {
			delete(h.sessions, agentID)
		}
	}
	h.mu.Unlock()
	return nil
}

// pickManager returns the best session to run orchestration on: a leader, else
// any manager, else (dev/token mode where role is unknown) any live session.
func (h *Hub) pickManager(agentID string) (*nodeSession, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var anyManager, anyLive *nodeSession
	for _, ns := range h.sessions[agentID] {
		if ns.session.IsClosed() {
			continue
		}
		anyLive = ns
		if ns.node.IsManager {
			if ns.node.IsLeader {
				return ns, true
			}
			anyManager = ns
		}
	}
	if anyManager != nil {
		return anyManager, true
	}
	return anyLive, anyLive != nil
}

// sessionForNode returns the live session of a specific node (node-scoped ops).
func (h *Hub) sessionForNode(agentID, nodeID string) (*nodeSession, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ns := h.sessions[agentID][nodeID]
	return ns, ns != nil && !ns.session.IsClosed()
}

// hasLiveSession reports whether the agent has at least one live tunnel.
func (h *Hub) hasLiveSession(agentID string) bool {
	_, ok := h.pickManager(agentID)
	return ok
}

// MarkSeen records a heartbeat from an agent and its node identity.
func (h *Hub) MarkSeen(agentID string, node ports.AgentNode) {
	h.mu.Lock()
	h.presence[agentID] = NodePresence{AgentNode: node, LastSeen: time.Now().UTC()}
	h.mu.Unlock()
}

// Forget drops an agent's presence and closes all its node tunnels.
func (h *Hub) Forget(agentID string) {
	h.mu.Lock()
	delete(h.presence, agentID)
	for _, ns := range h.sessions[agentID] {
		_ = ns.session.Close()
	}
	delete(h.sessions, agentID)
	h.mu.Unlock()
}

// Online reports whether the agent has a live tunnel or heartbeated recently.
func (h *Hub) Online(agentID string) bool {
	if h.hasLiveSession(agentID) {
		return true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.presence[agentID]
	return ok && time.Since(p.LastSeen) <= h.offlineAfter
}

// ConnectedNodeIDs returns the Swarm node ids with a live tunnel for an agent.
func (h *Hub) ConnectedNodeIDs(agentID string) map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]bool)
	for nodeID, ns := range h.sessions[agentID] {
		if !ns.session.IsClosed() && nodeID != "" {
			out[nodeID] = true
		}
	}
	return out
}

// Presence returns the last reported node for an agent.
func (h *Hub) Presence(agentID string) (NodePresence, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.presence[agentID]
	return p, ok
}

// Orchestrator returns a Swarm orchestrator carried over a manager node's tunnel
// (orchestration must run on a manager). Without a usable session it returns
// ErrDataPlaneUnavailable (heartbeating but no tunnel) or ErrAgentOffline.
func (h *Hub) Orchestrator(_ context.Context, agentID string) (ports.Orchestrator, error) {
	ns, ok := h.pickManager(agentID)
	if !ok {
		if h.Online(agentID) {
			return nil, ErrDataPlaneUnavailable
		}
		return nil, ErrAgentOffline
	}
	dial := func(_ context.Context, _, _ string) (net.Conn, error) {
		return ns.session.Open()
	}
	return orchestrator.NewSwarmOrchestratorOverDial(dial)
}

// OrchestratorForNode returns an orchestrator carried over a specific node's
// tunnel, for node-scoped operations (exec/logs/stats/metrics of that node).
func (h *Hub) OrchestratorForNode(_ context.Context, agentID, nodeID string) (ports.Orchestrator, error) {
	ns, ok := h.sessionForNode(agentID, nodeID)
	if !ok {
		return nil, ErrAgentOffline
	}
	dial := func(_ context.Context, _, _ string) (net.Conn, error) {
		return ns.session.Open()
	}
	return orchestrator.NewSwarmOrchestratorOverDial(dial)
}
