package service

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidName          = errors.New("service name must match ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")
	ErrInvalidImage         = errors.New("image is required")
	ErrInvalidReplicas      = errors.New("replicas must be >= 0")
	ErrResourceConflict     = errors.New("resource limit must be >= reservation")
	ErrNegativeResource     = errors.New("resource values must be >= 0")
	ErrInvalidFailureAction = errors.New("failure_action must be one of: pause, continue, rollback")
	ErrInvalidOrder         = errors.New("order must be one of: start-first, stop-first")
	ErrInvalidFailureRatio  = errors.New("max_failure_ratio must be between 0 and 1")
	ErrInvalidConstraint    = errors.New("placement constraint must be of the form key==value or key!=value")
	ErrInvalidPreference    = errors.New("placement preference (spread descriptor) must not be empty")
)

var nameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type Status string

const (
	StatusDraft    Status = "draft"
	StatusDeployed Status = "deployed"
	StatusRemoved  Status = "removed"
)

// IsValid reports whether s is a recognised service status.
func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusDeployed, StatusRemoved:
		return true
	default:
		return false
	}
}

type Resources struct {
	CPUReservation float64 // cores
	CPULimit       float64
	MemReservation int64 // bytes
	MemLimit       int64
}

func (r Resources) Validate() error {
	if r.CPUReservation < 0 || r.CPULimit < 0 || r.MemReservation < 0 || r.MemLimit < 0 {
		return ErrNegativeResource
	}
	if r.CPULimit > 0 && r.CPULimit < r.CPUReservation {
		return ErrResourceConflict
	}
	if r.MemLimit > 0 && r.MemLimit < r.MemReservation {
		return ErrResourceConflict
	}
	return nil
}

// Placement controls where the orchestrator schedules a service's tasks across
// the cluster (Swarm placement). Constraints are hard filters; preferences are
// soft spreading hints; MaxReplicas caps the number of tasks on a single node
// (0 = unlimited).
type Placement struct {
	Constraints []string // e.g. "node.role==worker", "node.labels.zone==a"
	Preferences []string // spread descriptors, e.g. "node.labels.zone"
	MaxReplicas uint64   // max tasks per node; 0 = unlimited
}

// constraintRegex matches a Swarm placement constraint: a non-empty key, the
// == or != operator, and a non-empty value. Whitespace around the operator is
// tolerated and normalised away by NormalizedConstraints.
var constraintRegex = regexp.MustCompile(`^\s*\S+\s*(==|!=)\s*\S.*$`)

// Validate checks that every constraint is well-formed and every preference is
// non-empty. Empty entries are rejected so a stray blank line in the UI does not
// silently produce a meaningless rule.
func (p Placement) Validate() error {
	for _, c := range p.Constraints {
		if strings.TrimSpace(c) == "" || !constraintRegex.MatchString(c) {
			return ErrInvalidConstraint
		}
	}
	for _, pref := range p.Preferences {
		if strings.TrimSpace(pref) == "" {
			return ErrInvalidPreference
		}
	}
	return nil
}

type UpdateConfig struct {
	Parallelism     uint64
	Delay           time.Duration
	FailureAction   string // pause | continue | rollback
	Monitor         time.Duration
	MaxFailureRatio float64
	Order           string // start-first | stop-first
}

func DefaultUpdateConfig() UpdateConfig {
	return UpdateConfig{
		Parallelism:     1,
		Delay:           10 * time.Second,
		FailureAction:   "rollback",
		Monitor:         30 * time.Second,
		MaxFailureRatio: 0,
		Order:           "start-first",
	}
}

var validFailureActions = map[string]bool{"pause": true, "continue": true, "rollback": true}
var validOrders = map[string]bool{"start-first": true, "stop-first": true}

// Validate checks that the rolling-update parameters are within Swarm's
// accepted vocabulary and bounds.
func (uc UpdateConfig) Validate() error {
	if !validFailureActions[uc.FailureAction] {
		return ErrInvalidFailureAction
	}
	if !validOrders[uc.Order] {
		return ErrInvalidOrder
	}
	if uc.MaxFailureRatio < 0 || uc.MaxFailureRatio > 1 {
		return ErrInvalidFailureRatio
	}
	return nil
}

// Overlay returns a copy of uc with the non-zero fields of override applied.
// A zero-valued override field means "not provided" and keeps uc's value, so a
// partial update_config payload can change individual settings without wiping
// the sensible defaults of the others. MaxFailureRatio of 0 is already the
// default, so it cannot be distinguished from "unset" — acceptable here.
func (uc UpdateConfig) Overlay(override UpdateConfig) UpdateConfig {
	if override.Parallelism != 0 {
		uc.Parallelism = override.Parallelism
	}
	if override.Delay != 0 {
		uc.Delay = override.Delay
	}
	if override.FailureAction != "" {
		uc.FailureAction = override.FailureAction
	}
	if override.Monitor != 0 {
		uc.Monitor = override.Monitor
	}
	if override.MaxFailureRatio != 0 {
		uc.MaxFailureRatio = override.MaxFailureRatio
	}
	if override.Order != "" {
		uc.Order = override.Order
	}
	return uc
}

type Service struct {
	ID             uuid.UUID
	ClusterID      uuid.UUID  // orchestration target; zero value = the default cluster
	HiveID         *uuid.UUID // project the service belongs to (nil = unassigned)
	Name           string
	Description    string
	Image          string
	Tag            string
	Replicas       uint64
	Command        []string
	Entrypoint     []string
	Resources      Resources
	Placement      Placement
	UpdateConfig   UpdateConfig
	Status         Status
	SwarmServiceID string // set after first deploy
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func New(name, image, tag string, replicas uint64, hiveId *uuid.UUID) (*Service, error) {
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
	}
	if strings.TrimSpace(image) == "" {
		return nil, ErrInvalidImage
	}
	return &Service{
		ID:           uuid.New(),
		Name:         name,
		Image:        image,
		Tag:          tag,
		Replicas:     replicas,
		UpdateConfig: DefaultUpdateConfig(),
		Status:       StatusDraft,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
		HiveID:       hiveId,
	}, nil
}

func (s *Service) SetResources(r Resources) error {
	if err := r.Validate(); err != nil {
		return err
	}
	s.Resources = r
	s.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *Service) SetPlacement(p Placement) error {
	if err := p.Validate(); err != nil {
		return err
	}
	s.Placement = p
	s.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *Service) UpdateTag(tag string) {
	s.Tag = tag
	s.UpdatedAt = time.Now().UTC()
}

func (s *Service) FullImage() string {
	if s.Tag == "" {
		return s.Image
	}
	return s.Image + ":" + s.Tag
}
