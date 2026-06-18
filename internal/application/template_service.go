package application

import (
	"context"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/service"
	"github.com/orange/hivemind/internal/domain/template"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/pagination"
)

// TemplateService manages service templates (F-V2-07) and instantiates services
// from them. Instantiation reuses the regular service-creation use case (so
// cluster-capacity validation and name-uniqueness still apply) and the network
// attachment use case.
type TemplateService struct {
	templates ports.TemplateRepository
	services  *ServiceService
	networks  *NetworkService
}

func NewTemplateService(templates ports.TemplateRepository, services *ServiceService, networks *NetworkService) *TemplateService {
	return &TemplateService{templates: templates, services: services, networks: networks}
}

type SaveTemplateInput struct {
	Name         string
	Description  string
	Spec         template.Spec
	LockedFields []string
	CreatedBy    uuid.UUID
}

func (s *TemplateService) Create(ctx context.Context, in SaveTemplateInput) (*template.Template, error) {
	t, err := template.New(in.Name, in.Description, in.Spec, in.LockedFields, in.CreatedBy)
	if err != nil {
		return nil, err
	}
	if err := s.templates.Save(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *TemplateService) Get(ctx context.Context, id uuid.UUID) (*template.Template, error) {
	return s.templates.FindByID(ctx, id)
}

func (s *TemplateService) List(ctx context.Context, page pagination.Page) ([]*template.Template, int64, error) {
	return s.templates.List(ctx, page)
}

// Update replaces the template's spec/locks and bumps its version.
func (s *TemplateService) Update(ctx context.Context, id uuid.UUID, in SaveTemplateInput) (*template.Template, error) {
	t, err := s.templates.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := t.Update(in.Description, in.Spec, in.LockedFields); err != nil {
		return nil, err
	}
	if err := s.templates.Update(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *TemplateService) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.templates.FindByID(ctx, id); err != nil {
		return err
	}
	return s.templates.Delete(ctx, id)
}

// InstantiateInput carries the per-instance values. Overrides for a locked
// field are rejected; nil overrides fall back to the template's defaults.
type InstantiateInput struct {
	Name              string
	Description       string
	TagOverride       *string
	ReplicasOverride  *uint64
	ResourcesOverride *service.Resources
	// Cluster is the orchestration target for the instantiated service. The zero
	// value selects the default cluster.
	Cluster uuid.UUID
}

// Instantiate creates a new service from a template, applying allowed overrides.
// Locked fields always take the template's value; supplying an override for a
// locked field returns ErrFieldLocked.
func (s *TemplateService) Instantiate(ctx context.Context, templateID uuid.UUID, in InstantiateInput) (*service.Service, error) {
	t, err := s.templates.FindByID(ctx, templateID)
	if err != nil {
		return nil, err
	}

	spec := t.Spec
	create := CreateServiceInput{
		Name:        in.Name,
		Description: in.Description,
		Image:       spec.Image,
		Tag:         spec.Tag,
		Replicas:    spec.Replicas,
		Resources:   spec.Resources,
		Placement:   spec.Placement,
		Cluster:     in.Cluster,
	}
	uc := spec.UpdateConfig
	create.UpdateConfig = &uc

	if in.TagOverride != nil {
		if t.IsLocked(template.FieldTag) {
			return nil, template.ErrFieldLocked
		}
		create.Tag = *in.TagOverride
	}
	if in.ReplicasOverride != nil {
		if t.IsLocked(template.FieldReplicas) {
			return nil, template.ErrFieldLocked
		}
		create.Replicas = *in.ReplicasOverride
	}
	if in.ResourcesOverride != nil {
		if t.IsLocked(template.FieldResources) {
			return nil, template.ErrFieldLocked
		}
		create.Resources = *in.ResourcesOverride
	}

	svc, err := s.services.Create(ctx, create)
	if err != nil {
		return nil, err
	}

	// Attach the template's networks (best-effort, validated by the use case).
	for _, nid := range spec.NetworkIDs {
		if err := s.networks.AttachToService(ctx, svc.ID, nid); err != nil {
			return nil, err
		}
	}
	return svc, nil
}
