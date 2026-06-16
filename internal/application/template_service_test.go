package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/template"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fake template repo ─────────────────────────────────────────────────────────

type fakeTemplateRepo struct {
	byID   map[uuid.UUID]*template.Template
	byName map[string]bool
}

func newFakeTemplateRepo() *fakeTemplateRepo {
	return &fakeTemplateRepo{byID: map[uuid.UUID]*template.Template{}, byName: map[string]bool{}}
}

func (r *fakeTemplateRepo) Save(_ context.Context, t *template.Template) error {
	if r.byName[t.Name] {
		return domainerrors.ErrConflict
	}
	r.byName[t.Name] = true
	r.byID[t.ID] = t
	return nil
}
func (r *fakeTemplateRepo) FindByID(_ context.Context, id uuid.UUID) (*template.Template, error) {
	if t, ok := r.byID[id]; ok {
		return t, nil
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeTemplateRepo) List(_ context.Context, _ pagination.Page) ([]*template.Template, int64, error) {
	out := make([]*template.Template, 0, len(r.byID))
	for _, t := range r.byID {
		out = append(out, t)
	}
	return out, int64(len(out)), nil
}
func (r *fakeTemplateRepo) Update(_ context.Context, t *template.Template) error {
	r.byID[t.ID] = t
	return nil
}
func (r *fakeTemplateRepo) Delete(_ context.Context, id uuid.UUID) error {
	t, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byName, t.Name)
	delete(r.byID, id)
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newTemplateSvc(tmplRepo *fakeTemplateRepo, svcRepo *fakeServiceRepo, netRepo *fakeNetworkRepo) *application.TemplateService {
	svcSvc := application.NewServiceService(svcRepo, nil) // nil orch → capacity check skipped
	netSvc := application.NewNetworkService(netRepo, svcRepo)
	return application.NewTemplateService(tmplRepo, svcSvc, netSvc)
}

func mkTemplate(t *testing.T, repo *fakeTemplateRepo, spec template.Spec, locked []string) *template.Template {
	t.Helper()
	tmpl, err := template.New("java-api", "API Java", spec, locked, uuid.New())
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), tmpl))
	return tmpl
}

// ─── Tests ──────────────────────────────────────────────────────────────────────

func TestTemplateInstantiate_AppliesDefaults(t *testing.T) {
	tmplRepo := newFakeTemplateRepo()
	svcRepo := newFakeServiceRepo()
	svc := newTemplateSvc(tmplRepo, svcRepo, newFakeNetworkRepo())

	spec := template.Spec{Image: "nginx", Tag: "1.25", Replicas: 3, Resources: service.Resources{MemLimit: 256 << 20}}
	tmpl := mkTemplate(t, tmplRepo, spec, nil)

	out, err := svc.Instantiate(context.Background(), tmpl.ID, application.InstantiateInput{Name: "orders-api"})
	require.NoError(t, err)
	assert.Equal(t, "orders-api", out.Name)
	assert.Equal(t, "nginx", out.Image)
	assert.Equal(t, uint64(3), out.Replicas)
	assert.EqualValues(t, 256<<20, out.Resources.MemLimit)
}

func TestTemplateInstantiate_OverrideAllowed(t *testing.T) {
	tmplRepo := newFakeTemplateRepo()
	svcRepo := newFakeServiceRepo()
	svc := newTemplateSvc(tmplRepo, svcRepo, newFakeNetworkRepo())
	tmpl := mkTemplate(t, tmplRepo, template.Spec{Image: "nginx", Replicas: 1}, nil)

	five := uint64(5)
	out, err := svc.Instantiate(context.Background(), tmpl.ID, application.InstantiateInput{Name: "svc", ReplicasOverride: &five})
	require.NoError(t, err)
	assert.Equal(t, uint64(5), out.Replicas)
}

func TestTemplateInstantiate_LockedFieldRejected(t *testing.T) {
	tmplRepo := newFakeTemplateRepo()
	svcRepo := newFakeServiceRepo()
	svc := newTemplateSvc(tmplRepo, svcRepo, newFakeNetworkRepo())
	tmpl := mkTemplate(t, tmplRepo, template.Spec{Image: "nginx", Replicas: 2}, []string{"replicas"})

	five := uint64(5)
	_, err := svc.Instantiate(context.Background(), tmpl.ID, application.InstantiateInput{Name: "svc", ReplicasOverride: &five})
	assert.ErrorIs(t, err, template.ErrFieldLocked)
}

func TestTemplateInstantiate_AttachesNetworks(t *testing.T) {
	tmplRepo := newFakeTemplateRepo()
	svcRepo := newFakeServiceRepo()
	netRepo := newFakeNetworkRepo()
	svc := newTemplateSvc(tmplRepo, svcRepo, netRepo)

	n, err := network.New("backend-net")
	require.NoError(t, err)
	require.NoError(t, netRepo.Save(context.Background(), n))

	tmpl := mkTemplate(t, tmplRepo, template.Spec{Image: "nginx", Replicas: 1, NetworkIDs: []uuid.UUID{n.ID}}, nil)

	out, err := svc.Instantiate(context.Background(), tmpl.ID, application.InstantiateInput{Name: "svc"})
	require.NoError(t, err)

	ids, err := svcRepo.GetNetworkIDs(context.Background(), out.ID)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.Equal(t, n.ID, ids[0])
}

func TestTemplateInstantiate_NameConflict(t *testing.T) {
	tmplRepo := newFakeTemplateRepo()
	svcRepo := newFakeServiceRepo()
	svc := newTemplateSvc(tmplRepo, svcRepo, newFakeNetworkRepo())
	svcRepo.add(mkService(t, "taken"))
	tmpl := mkTemplate(t, tmplRepo, template.Spec{Image: "nginx", Replicas: 1}, nil)

	_, err := svc.Instantiate(context.Background(), tmpl.ID, application.InstantiateInput{Name: "taken"})
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}
