package deployment

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrAlreadyInProgress = errors.New("a deployment is already in progress for this service")

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusRolledBack Status = "rolled_back"
)

type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerWebhook  Trigger = "webhook"
	TriggerRollback Trigger = "rollback"
	TriggerScale    Trigger = "scale"
)

type Deployment struct {
	ID             uuid.UUID
	ServiceID      uuid.UUID
	UserID         *uuid.UUID // nil when triggered by webhook
	ImageTag       string
	Trigger        Trigger
	Status         Status
	ErrorMessage   string
	ConfigSnapshot json.RawMessage
	StartedAt      time.Time
	FinishedAt     *time.Time
}

func New(serviceID uuid.UUID, userID *uuid.UUID, imageTag string, trigger Trigger, snapshot json.RawMessage) *Deployment {
	return &Deployment{
		ID:             uuid.New(),
		ServiceID:      serviceID,
		UserID:         userID,
		ImageTag:       imageTag,
		Trigger:        trigger,
		Status:         StatusPending,
		ConfigSnapshot: snapshot,
		StartedAt:      time.Now().UTC(),
	}
}

func (d *Deployment) Start() {
	d.Status = StatusInProgress
}

func (d *Deployment) Succeed() {
	now := time.Now().UTC()
	d.Status = StatusSucceeded
	d.FinishedAt = &now
}

func (d *Deployment) Fail(reason string) {
	now := time.Now().UTC()
	d.Status = StatusFailed
	d.ErrorMessage = reason
	d.FinishedAt = &now
}

func (d *Deployment) MarkRolledBack() {
	now := time.Now().UTC()
	d.Status = StatusRolledBack
	d.FinishedAt = &now
}

func (d *Deployment) IsTerminal() bool {
	return d.Status == StatusSucceeded ||
		d.Status == StatusFailed ||
		d.Status == StatusRolledBack
}

func (d *Deployment) Duration() *time.Duration {
	if d.FinishedAt == nil {
		return nil
	}
	dur := d.FinishedAt.Sub(d.StartedAt)
	return &dur
}
