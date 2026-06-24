package dto

import "time"

// DiscoveredService is one live Swarm service surfaced by brownfield discovery
// (ADR 0004), annotated with its ownership class so the UI can offer adoption.
//
// class is one of:
//   - "managed": a Hivemind-owned service (its hivemind.service.id label resolves
//     to a known record); service_id / hive_id identify that record.
//   - "foreign": created out-of-band, never adopted (no hivemind.service.id label).
//   - "orphan":  carries a hivemind.service.id label that resolves to no known
//     record (e.g. the record was deleted).
type DiscoveredService struct {
	SwarmServiceID string    `json:"swarm_service_id"`
	Name           string    `json:"name"`
	Image          string    `json:"image"`
	Replicas       uint64    `json:"replicas"`
	Class          string    `json:"class"`
	ServiceID      string    `json:"service_id,omitempty"`
	HiveID         string    `json:"hive_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
