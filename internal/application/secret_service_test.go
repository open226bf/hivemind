package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fake secret repo ─────────────────────────────────────────────────────────

type storedSecret struct {
	sec    *secret.Secret
	values [][]byte // one entry per version, latest last
}

type fakeSecretRepo struct {
	byID     map[uuid.UUID]*storedSecret
	byName   map[string]bool
	attached map[uuid.UUID]bool
}

func newFakeSecretRepo() *fakeSecretRepo {
	return &fakeSecretRepo{
		byID:     map[uuid.UUID]*storedSecret{},
		byName:   map[string]bool{},
		attached: map[uuid.UUID]bool{},
	}
}

func (r *fakeSecretRepo) Save(_ context.Context, s *secret.Secret, _ *secret.SecretVersion, value []byte) error {
	if r.byName[s.Name] {
		return domainerrors.ErrConflict
	}
	r.byName[s.Name] = true
	r.byID[s.ID] = &storedSecret{sec: s, values: [][]byte{value}}
	return nil
}

func (r *fakeSecretRepo) FindByID(_ context.Context, id uuid.UUID) (*secret.Secret, error) {
	if ss, ok := r.byID[id]; ok {
		return ss.sec, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeSecretRepo) List(_ context.Context, _ pagination.Page) ([]*secret.Secret, int64, error) {
	out := make([]*secret.Secret, 0, len(r.byID))
	for _, ss := range r.byID {
		out = append(out, ss.sec)
	}
	return out, int64(len(out)), nil
}

func (r *fakeSecretRepo) Update(_ context.Context, s *secret.Secret, _ *secret.SecretVersion, value []byte) error {
	ss, ok := r.byID[s.ID]
	if !ok {
		return domainerrors.ErrNotFound
	}
	ss.sec = s
	ss.values = append(ss.values, value)
	return nil
}

func (r *fakeSecretRepo) Delete(_ context.Context, id uuid.UUID) error {
	ss, ok := r.byID[id]
	if !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byName, ss.sec.Name)
	delete(r.byID, id)
	return nil
}

func (r *fakeSecretRepo) IsAttachedToService(_ context.Context, id uuid.UUID) (bool, error) {
	return r.attached[id], nil
}

func (r *fakeSecretRepo) GetValue(_ context.Context, id uuid.UUID) ([]byte, error) {
	ss, ok := r.byID[id]
	if !ok || len(ss.values) == 0 {
		return nil, domainerrors.ErrNotFound
	}
	return ss.values[len(ss.values)-1], nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newSecretSvc(secrets *fakeSecretRepo, services *fakeServiceRepo) *application.SecretService {
	return application.NewSecretService(secrets, services)
}

// ─── Create ───────────────────────────────────────────────────────────────────

func TestSecretCreate_Success(t *testing.T) {
	repo := newFakeSecretRepo()
	svc := newSecretSvc(repo, newFakeServiceRepo())

	sec, err := svc.Create(context.Background(), application.CreateSecretInput{
		Name:       "db_password",
		TargetPath: "/run/secrets/db_password",
		Value:      []byte("s3cr3t"),
		CreatedBy:  uuid.New(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, sec.CurrentVersion)
	assert.NotEmpty(t, sec.Checksum)
	// The stored value is held by the repo, never returned on the domain object.
	assert.Equal(t, []byte("s3cr3t"), repo.byID[sec.ID].values[0])
}

func TestSecretCreate_EmptyValue(t *testing.T) {
	svc := newSecretSvc(newFakeSecretRepo(), newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateSecretInput{Name: "db_password", Value: nil})
	assert.ErrorIs(t, err, secret.ErrEmptyValue)
}

func TestSecretCreate_InvalidName(t *testing.T) {
	svc := newSecretSvc(newFakeSecretRepo(), newFakeServiceRepo())
	_, err := svc.Create(context.Background(), application.CreateSecretInput{Name: "bad name", Value: []byte("v")})
	assert.ErrorIs(t, err, secret.ErrInvalidName)
}

func TestSecretCreate_DuplicateName(t *testing.T) {
	repo := newFakeSecretRepo()
	svc := newSecretSvc(repo, newFakeServiceRepo())
	in := application.CreateSecretInput{Name: "dup", Value: []byte("v"), CreatedBy: uuid.New()}
	_, err := svc.Create(context.Background(), in)
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), in)
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

// ─── Rotate ───────────────────────────────────────────────────────────────────

func TestSecretRotate_IncrementsVersion(t *testing.T) {
	repo := newFakeSecretRepo()
	svc := newSecretSvc(repo, newFakeServiceRepo())
	sec, err := svc.Create(context.Background(), application.CreateSecretInput{Name: "db", Value: []byte("old"), CreatedBy: uuid.New()})
	require.NoError(t, err)

	rotated, err := svc.Rotate(context.Background(), sec.ID, []byte("new"))
	require.NoError(t, err)
	assert.Equal(t, 2, rotated.CurrentVersion)
	assert.Equal(t, []byte("new"), repo.byID[sec.ID].values[1])
}

func TestSecretRotate_EmptyValue(t *testing.T) {
	repo := newFakeSecretRepo()
	svc := newSecretSvc(repo, newFakeServiceRepo())
	sec, _ := svc.Create(context.Background(), application.CreateSecretInput{Name: "db", Value: []byte("old"), CreatedBy: uuid.New()})
	_, err := svc.Rotate(context.Background(), sec.ID, nil)
	assert.ErrorIs(t, err, secret.ErrEmptyValue)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func TestSecretDelete_InUse(t *testing.T) {
	repo := newFakeSecretRepo()
	svc := newSecretSvc(repo, newFakeServiceRepo())
	sec, _ := svc.Create(context.Background(), application.CreateSecretInput{Name: "db", Value: []byte("v"), CreatedBy: uuid.New()})
	repo.attached[sec.ID] = true

	err := svc.Delete(context.Background(), sec.ID)
	assert.ErrorIs(t, err, secret.ErrSecretInUse)
}

func TestSecretDelete_Success(t *testing.T) {
	repo := newFakeSecretRepo()
	svc := newSecretSvc(repo, newFakeServiceRepo())
	sec, _ := svc.Create(context.Background(), application.CreateSecretInput{Name: "db", Value: []byte("v"), CreatedBy: uuid.New()})

	require.NoError(t, svc.Delete(context.Background(), sec.ID))
	_, err := repo.FindByID(context.Background(), sec.ID)
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

// ─── Attach / detach ──────────────────────────────────────────────────────────

func TestSecretAttach_DefaultsTargetPath(t *testing.T) {
	secRepo := newFakeSecretRepo()
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newSecretSvc(secRepo, svcRepo)
	sec, _ := svc.Create(context.Background(), application.CreateSecretInput{
		Name: "db", TargetPath: "/run/secrets/db", Value: []byte("v"), CreatedBy: uuid.New(),
	})

	// Empty target path -> falls back to the secret's default.
	require.NoError(t, svc.AttachToService(context.Background(), s.ID, sec.ID, ""))

	attached, err := svc.ListServiceSecrets(context.Background(), s.ID)
	require.NoError(t, err)
	require.Len(t, attached, 1)
	assert.Equal(t, "/run/secrets/db", attached[0].TargetPath)
}

func TestSecretAttach_AlreadyAttached(t *testing.T) {
	secRepo := newFakeSecretRepo()
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newSecretSvc(secRepo, svcRepo)
	sec, _ := svc.Create(context.Background(), application.CreateSecretInput{Name: "db", Value: []byte("v"), CreatedBy: uuid.New()})

	require.NoError(t, svc.AttachToService(context.Background(), s.ID, sec.ID, "/x"))
	err := svc.AttachToService(context.Background(), s.ID, sec.ID, "/x")
	assert.ErrorIs(t, err, domainerrors.ErrConflict)
}

func TestSecretAttach_UnknownSecret(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newSecretSvc(newFakeSecretRepo(), svcRepo)

	err := svc.AttachToService(context.Background(), s.ID, uuid.New(), "/x")
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}

func TestSecretDetach_NotAttached(t *testing.T) {
	svcRepo := newFakeServiceRepo()
	s := mkService(t, "my-service")
	svcRepo.add(s)
	svc := newSecretSvc(newFakeSecretRepo(), svcRepo)

	err := svc.DetachFromService(context.Background(), s.ID, uuid.New())
	assert.ErrorIs(t, err, domainerrors.ErrNotFound)
}
