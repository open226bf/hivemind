// Package template defines service templates (F-V2-07): reusable presets that
// pre-fill a service's image, scaling, resources, rolling-update strategy,
// placement and networks. A template may lock individual fields so instances
// cannot override imposed values (e.g. a memory limit).
package template

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/service"
)

var (
	ErrInvalidName  = errors.New("template name must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$")
	ErrInvalidImage = errors.New("template image is required")
	ErrInvalidLock  = errors.New("locked field must be one of: image, tag, replicas, resources, update_config, placement, networks")
	ErrFieldLocked  = errors.New("field is locked by the template and cannot be overridden")
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// Lockable field identifiers.
const (
	FieldImage        = "image"
	FieldTag          = "tag"
	FieldReplicas     = "replicas"
	FieldResources    = "resources"
	FieldUpdateConfig = "update_config"
	FieldPlacement    = "placement"
	FieldNetworks     = "networks"
)

var lockableFields = map[string]bool{
	FieldImage: true, FieldTag: true, FieldReplicas: true, FieldResources: true,
	FieldUpdateConfig: true, FieldPlacement: true, FieldNetworks: true,
}

// Spec holds the default service definition a template applies.
type Spec struct {
	Image        string
	Tag          string
	Replicas     uint64
	Resources    service.Resources
	UpdateConfig service.UpdateConfig
	Placement    service.Placement
	NetworkIDs   []uuid.UUID
}

func (s Spec) Validate() error {
	if s.Image == "" {
		return ErrInvalidImage
	}
	if err := s.Resources.Validate(); err != nil {
		return err
	}
	if err := s.UpdateConfig.Validate(); err != nil {
		return err
	}
	return s.Placement.Validate()
}

// Template is a versioned, admin-managed preset. Version increments on every
// update so instances can record the template revision they came from.
type Template struct {
	ID           uuid.UUID
	Name         string
	Description  string
	Version      int
	Spec         Spec
	LockedFields []string
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func New(name, description string, spec Spec, locked []string, createdBy uuid.UUID) (*Template, error) {
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
	}
	if err := validateLocked(locked); err != nil {
		return nil, err
	}
	// Default the rolling-update strategy when the caller left it zero-valued.
	if spec.UpdateConfig == (service.UpdateConfig{}) {
		spec.UpdateConfig = service.DefaultUpdateConfig()
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Template{
		ID:           uuid.New(),
		Name:         name,
		Description:  description,
		Version:      1,
		Spec:         spec,
		LockedFields: locked,
		CreatedBy:    createdBy,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Update replaces the spec/locks and bumps the version. The name is immutable.
func (t *Template) Update(description string, spec Spec, locked []string) error {
	if err := validateLocked(locked); err != nil {
		return err
	}
	if spec.UpdateConfig == (service.UpdateConfig{}) {
		spec.UpdateConfig = service.DefaultUpdateConfig()
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	t.Description = description
	t.Spec = spec
	t.LockedFields = locked
	t.Version++
	t.UpdatedAt = time.Now().UTC()
	return nil
}

// IsLocked reports whether the named field is locked.
func (t *Template) IsLocked(field string) bool {
	for _, f := range t.LockedFields {
		if f == field {
			return true
		}
	}
	return false
}

func validateLocked(locked []string) error {
	for _, f := range locked {
		if !lockableFields[f] {
			return ErrInvalidLock
		}
	}
	return nil
}
