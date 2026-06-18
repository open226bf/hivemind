package application_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fake config repo ─────────────────────────────────────────────────────────

type fakeConfigRepo struct {
	byID     map[uuid.UUID]*config.Config
	byName   map[string]bool
	versions map[uuid.UUID][]*config.ConfigVersion
	attached map[uuid.UUID]bool
}

func newFakeConfigRepo() *fakeConfigRepo {
	return &fakeConfigRepo{
		byID:     map[uuid.UUID]*config.Config{},
		byName:   map[string]bool{},
		versions: map[uuid.UUID][]*config.ConfigVersion{},
		attached: map[uuid.UUID]bool{},
	}
}

func (r *fakeConfigRepo) Save(_ context.Context, c *config.Config, v *config.ConfigVersion) error {
	if r.byName[c.Name] {
		return domainerrors.ErrConflict
	}
	r.byName[c.Name] = true
	r.byID[c.ID] = c
	r.versions[c.ID] = []*config.ConfigVersion{v}
	return nil
}

func (r *fakeConfigRepo) FindByID(_ context.Context, id uuid.UUID) (*config.Config, error) {
	if c, ok := r.byID[id]; ok {
		return c, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeConfigRepo) ListVersions(_ context.Context, id uuid.UUID) ([]*config.ConfigVersion, error) {
	return r.versions[id], nil
}

func (r *fakeConfigRepo) List(_ context.Context, _ uuid.UUID, _ pagination.Page) ([]*config.Config, int64, error) {
	out := make([]*config.Config, 0, len(r.byID))
	for _, c := range r.byID {
		out = append(out, c)
	}
	return out, int64(len(out)), nil
}

func (r *fakeConfigRepo) Update(_ context.Context, c *config.Config, newVersion *config.ConfigVersion) error {
	if _, ok := r.byID[c.ID]; !ok {
		return domainerrors.ErrNotFound
	}
	r.byID[c.ID] = c
	// newest first, matching the real repo's ordering
	r.versions[c.ID] = append([]*config.ConfigVersion{newVersion}, r.versions[c.ID]...)
	return nil
}

func (r *fakeConfigRepo) Delete(_ context.Context, id uuid.UUID) error {
	c, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byName, c.Name)
	delete(r.byID, id)
	delete(r.versions, id)
	return nil
}

func (r *fakeConfigRepo) IsAttachedToService(_ context.Context, id uuid.UUID) (bool, error) {
	return r.attached[id], nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newConfigSvc(configs *fakeConfigRepo, services *fakeServiceRepo) *application.ConfigService {
	return application.NewConfigService(configs, services)
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestConfigCreate_Success(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	c, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name:       "nginx.conf",
		TargetPath: "/etc/nginx/nginx.conf",
		Content:    []byte("server { listen 80; }"),
		Comment:    "initial",
		CreatedBy:  uuid.New(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, c.CurrentVersion)
}

func TestConfigCreate_InvalidName(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateConfigInput{Name: "bad name", Content: []byte("x")})
	assert.ErrorIs(t, err, config.ErrInvalidName)
}

func TestConfigCreate_TooLarge(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name:    "big.conf",
		Content: []byte(strings.Repeat("a", 500*1024+1)),
	})
	assert.ErrorIs(t, err, config.ErrContentTooLarge)
}

func TestConfigCreate_DuplicateName(t *testing.T) {
	repo := newFakeConfigRepo()
	svc := newConfigSvc(repo, newFakeServiceRepo())
	in := application.CreateConfigInput{Name: "dup.conf", Content: []byte("x"), Comment: "initial", CreatedBy: uuid.New()}
	_, err := svc.Create(context.Background(), in)
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), in)
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

// ─── Versions ─────────────────────────────────────────────────────────────────

func TestConfigAddVersion_IncrementsAndReadsContent(t *testing.T) {
	repo := newFakeConfigRepo()
	svc := newConfigSvc(repo, newFakeServiceRepo())
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{Name: "app.yml", Content: []byte("k: v1"), Comment: "v1", CreatedBy: uuid.New()})

	updated, err := svc.AddVersion(context.Background(), c.ID, []byte("k: v2"), "bump", uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)

	versions, err := svc.ListVersions(context.Background(), c.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, []byte("k: v2"), versions[0].Content) // newest first
}

func TestConfigAddVersion_InvalidUTF8(t *testing.T) {
	repo := newFakeConfigRepo()
	svc := newConfigSvc(repo, newFakeServiceRepo())
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{Name: "app.yml", Content: []byte("ok"), Comment: "v1", CreatedBy: uuid.New()})

	_, err := svc.AddVersion(context.Background(), c.ID, []byte{0xff, 0xfe}, "bump", uuid.New())
	assert.ErrorIs(t, err, config.ErrInvalidUTF8)
}

func TestConfigListVersions_NotFound(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	_, err := svc.ListVersions(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestConfigDelete_InUse(t *testing.T) {
	repo := newFakeConfigRepo()
	svc := newConfigSvc(repo, newFakeServiceRepo())
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{Name: "c.conf", Content: []byte("x"), Comment: "initial", CreatedBy: uuid.New()})
	repo.attached[c.ID] = true

	err := svc.Delete(context.Background(), c.ID)
	assert.ErrorIs(t, err, config.ErrConfigInUse)
}

// ─── Attach / detach ──────────────────────────────────────────────────────────

func TestConfigAttach_DefaultsTargetPath(t *testing.T) {
	cfgRepo := newFakeConfigRepo()
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newConfigSvc(cfgRepo, svcRepo)
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "nginx.conf", TargetPath: "/etc/nginx/nginx.conf", Content: []byte("x"), Comment: "initial", CreatedBy: uuid.New(),
	})

	require.NoError(t, svc.AttachToService(context.Background(), s.ID, c.ID, ""))

	attached, err := svc.ListServiceConfigs(context.Background(), s.ID)
	require.NoError(t, err)
	require.Len(t, attached, 1)
	assert.Equal(t, "/etc/nginx/nginx.conf", attached[0].TargetPath)
}

func TestConfigAttach_AlreadyAttached(t *testing.T) {
	cfgRepo := newFakeConfigRepo()
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newConfigSvc(cfgRepo, svcRepo)
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{Name: "c.conf", Content: []byte("x"), Comment: "initial", CreatedBy: uuid.New()})

	require.NoError(t, svc.AttachToService(context.Background(), s.ID, c.ID, "/x"))
	err := svc.AttachToService(context.Background(), s.ID, c.ID, "/x")
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

func TestConfigDetach_NotAttached(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newConfigSvc(newFakeConfigRepo(), svcRepo)

	err := svc.DetachFromService(context.Background(), s.ID, uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

// ─── F-V2-08: diff, restore, impacted services, mandatory comment ─────────────

func TestConfigCreate_RequiresComment(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "no-comment.conf", Content: []byte("x"), CreatedBy: uuid.New(),
	})
	assert.ErrorIs(t, err, config.ErrCommentRequired)
}

func TestConfigAddVersion_RequiresComment(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	c, err := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "app.yml", Content: []byte("k: v1"), Comment: "v1", CreatedBy: uuid.New(),
	})
	require.NoError(t, err)
	_, err = svc.AddVersion(context.Background(), c.ID, []byte("k: v2"), "", uuid.New())
	assert.ErrorIs(t, err, config.ErrCommentRequired)
}

func TestConfigDiffVersions(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "app.yml", Content: []byte("a\nb\nc"), Comment: "v1", CreatedBy: uuid.New(),
	})
	_, err := svc.AddVersion(context.Background(), c.ID, []byte("a\nB\nc"), "v2", uuid.New())
	require.NoError(t, err)

	diff, err := svc.DiffVersions(context.Background(), c.ID, 1, 2)
	require.NoError(t, err)
	assert.Equal(t, 1, diff.FromVersion)
	assert.Equal(t, 2, diff.ToVersion)
	// "b" deleted, "B" added, "a" and "c" equal.
	require.Len(t, diff.Lines, 4)
}

func TestConfigDiffVersions_UnknownVersion(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "app.yml", Content: []byte("a"), Comment: "v1", CreatedBy: uuid.New(),
	})
	_, err := svc.DiffVersions(context.Background(), c.ID, 1, 9)
	assert.ErrorIs(t, err, application.ErrVersionNotFound)
}

func TestConfigRestoreVersion(t *testing.T) {
	svc := newConfigSvc(newFakeConfigRepo(), newFakeServiceRepo())
	c, _ := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "app.yml", Content: []byte("original"), Comment: "v1", CreatedBy: uuid.New(),
	})
	_, _ = svc.AddVersion(context.Background(), c.ID, []byte("changed"), "v2", uuid.New())

	updated, err := svc.RestoreVersion(context.Background(), c.ID, 1, "", uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 3, updated.CurrentVersion) // restore creates a new version

	versions, _ := svc.ListVersions(context.Background(), c.ID)
	assert.Equal(t, []byte("original"), versions[0].Content) // identical to v1
	assert.Contains(t, versions[0].Comment, "Restauration")  // default comment
}

func TestConfigImpactedServices(t *testing.T) {
	cfgRepo := newFakeConfigRepo()
	svcRepo := newFakeServiceRepo()
	svc := newConfigSvc(cfgRepo, svcRepo)

	c, _ := svc.Create(context.Background(), application.CreateConfigInput{
		Name: "app.yml", Content: []byte("x"), Comment: "v1", CreatedBy: uuid.New(),
	})
	s := mkService(t, "api")
	svcRepo.add(s)
	require.NoError(t, svc.AttachToService(context.Background(), s.ID, c.ID, "/etc/app.yml"))

	impacted, err := svc.ImpactedServices(context.Background(), c.ID)
	require.NoError(t, err)
	require.Len(t, impacted, 1)
	assert.Equal(t, "api", impacted[0].Name)
}
