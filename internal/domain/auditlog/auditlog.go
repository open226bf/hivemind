package auditlog

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	ID           uuid.UUID
	UserID       *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   string
	Payload      json.RawMessage
	IP           string
	CreatedAt    time.Time
}

func New(userID *uuid.UUID, action, resourceType, resourceID string, payload json.RawMessage, ip string) *AuditLog {
	return &AuditLog{
		ID:           uuid.New(),
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Payload:      payload,
		IP:           ip,
		CreatedAt:    time.Now().UTC(),
	}
}
