package service

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidName      = errors.New("service name must match ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$")
	ErrInvalidReplicas  = errors.New("replicas must be >= 0")
	ErrResourceConflict = errors.New("resource limit must be >= reservation")
)

var nameRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type Status string

const (
	StatusDraft    Status = "draft"
	StatusDeployed Status = "deployed"
	StatusRemoved  Status = "removed"
)

type Resources struct {
	CPUReservation float64 // cores
	CPULimit       float64
	MemReservation int64 // bytes
	MemLimit       int64
}

func (r Resources) Validate() error {
	if r.CPULimit > 0 && r.CPULimit < r.CPUReservation {
		return ErrResourceConflict
	}
	if r.MemLimit > 0 && r.MemLimit < r.MemReservation {
		return ErrResourceConflict
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

type Service struct {
	ID           uuid.UUID
	Name         string
	Description  string
	Image        string
	Tag          string
	Replicas     uint64
	Command      []string
	Entrypoint   []string
	Resources    Resources
	UpdateConfig UpdateConfig
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func New(name, image, tag string, replicas uint64) (*Service, error) {
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
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
