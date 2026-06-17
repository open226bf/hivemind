// Package agenthub tracks the live sessions of Hivemind agents (the "agent"
// connection mode). Agents dial out and report presence via heartbeats; the hub
// keeps that presence in memory and carries the reverse tunnel (yamux) that
// backs a Swarm Orchestrator, multiplexing Docker API calls to the agent's
// docker.sock.
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
// but the reverse tunnel that would back an Orchestrator is not yet wired.
var ErrDataPlaneUnavailable = errors.New("agent is online but its control tunnel is not available yet")

// ErrAgentOffline is returned when no live session exists for an agent.
var ErrAgentOffline = errors.New("agent has no live session")

// NodePresence is the last node identity an agent task reported, plus the time.
type NodePresence struct {
	ports.AgentNode
	LastSeen time.Time
}

// Hub is the in-memory presence + tunnel-session registry. It implements
// ports.AgentHub and ports.AgentPresence.
type Hub struct {
	offlineAfter time.Duration

	mu       sync.RWMutex
	presence map[string]NodePresence   // agentID -> last reported node
	sessions map[string]*yamux.Session // agentID -> live reverse tunnel
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
		sessions:     make(map[string]*yamux.Session),
	}
}

// Attach registers the agent's reverse tunnel. The hub multiplexes Docker API
// calls onto this connection by opening yamux streams (the server is the yamux
// client; the agent accepts and proxies each stream to its docker.sock). A
// previous session for the same agent is closed. Attach blocks until the
// session ends, so callers run it on the connection's goroutine.
func (h *Hub) Attach(agentID string, conn net.Conn) error {
	session, err := yamux.Client(conn, nil)
	if err != nil {
		return err
	}

	h.mu.Lock()
	if old := h.sessions[agentID]; old != nil {
		_ = old.Close()
	}
	h.sessions[agentID] = session
	h.mu.Unlock()

	<-session.CloseChan() // block until the tunnel drops

	h.mu.Lock()
	if h.sessions[agentID] == session {
		delete(h.sessions, agentID)
	}
	h.mu.Unlock()
	return nil
}

// session returns the live tunnel for an agent, if any.
func (h *Hub) session(agentID string) (*yamux.Session, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.sessions[agentID]
	return s, ok && !s.IsClosed()
}

// MarkSeen records a heartbeat from an agent and its node identity.
func (h *Hub) MarkSeen(agentID string, node ports.AgentNode) {
	h.mu.Lock()
	h.presence[agentID] = NodePresence{AgentNode: node, LastSeen: time.Now().UTC()}
	h.mu.Unlock()
}

// Forget drops an agent's presence and closes its tunnel (e.g. on cluster removal).
func (h *Hub) Forget(agentID string) {
	h.mu.Lock()
	delete(h.presence, agentID)
	if s := h.sessions[agentID]; s != nil {
		_ = s.Close()
		delete(h.sessions, agentID)
	}
	h.mu.Unlock()
}

// Online reports whether the agent heartbeated within the offline window.
func (h *Hub) Online(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.presence[agentID]
	return ok && time.Since(p.LastSeen) <= h.offlineAfter
}

// Presence returns the last reported node for an agent.
func (h *Hub) Presence(agentID string) (NodePresence, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.presence[agentID]
	return p, ok
}

// Orchestrator backs the agent transport: it returns a Swarm orchestrator whose
// Docker API calls are carried over the agent's reverse tunnel (each call opens
// a yamux stream the agent proxies to its docker.sock). Without a live tunnel it
// returns ErrAgentOffline (no session) or ErrDataPlaneUnavailable (heartbeating
// but the tunnel has not been established).
func (h *Hub) Orchestrator(_ context.Context, agentID string) (ports.Orchestrator, error) {
	session, ok := h.session(agentID)
	if !ok {
		if h.Online(agentID) {
			return nil, ErrDataPlaneUnavailable
		}
		return nil, ErrAgentOffline
	}
	dial := func(_ context.Context, _, _ string) (net.Conn, error) {
		return session.Open()
	}
	return orchestrator.NewSwarmOrchestratorOverDial(dial)
}
