// Package agenthub tracks the live sessions of Hivemind agents (the "agent"
// connection mode). Agents dial out and report presence via heartbeats; the hub
// keeps that presence in memory and (in a later phase) carries the reverse
// tunnel that backs an Orchestrator.
package agenthub

import (
	"context"
	"errors"
	"sync"
	"time"

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

// Hub is the in-memory presence registry. It implements ports.AgentHub and
// ports.AgentPresence.
type Hub struct {
	offlineAfter time.Duration

	mu       sync.RWMutex
	presence map[string]NodePresence // agentID -> last reported node
}

// New builds a hub. offlineAfter is how long after the last heartbeat an agent
// is considered offline.
func New(offlineAfter time.Duration) *Hub {
	if offlineAfter <= 0 {
		offlineAfter = 45 * time.Second
	}
	return &Hub{offlineAfter: offlineAfter, presence: make(map[string]NodePresence)}
}

// MarkSeen records a heartbeat from an agent and its node identity.
func (h *Hub) MarkSeen(agentID string, node ports.AgentNode) {
	h.mu.Lock()
	h.presence[agentID] = NodePresence{AgentNode: node, LastSeen: time.Now().UTC()}
	h.mu.Unlock()
}

// Forget drops an agent's session (e.g. on cluster removal).
func (h *Hub) Forget(agentID string) {
	h.mu.Lock()
	delete(h.presence, agentID)
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

// Orchestrator backs the agent transport. The control tunnel is not implemented
// yet: a present agent yields ErrDataPlaneUnavailable, an absent one
// ErrAgentOffline — both surfaced clearly to the caller.
func (h *Hub) Orchestrator(_ context.Context, agentID string) (ports.Orchestrator, error) {
	if !h.Online(agentID) {
		return nil, ErrAgentOffline
	}
	return nil, ErrDataPlaneUnavailable
}
