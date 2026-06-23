package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/acl"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

// ─── Fakes ────────────────────────────────────────────────────────────────────

type fakeAclRepo struct {
	byID map[uuid.UUID]*acl.Grant
}

func newFakeAclRepo() *fakeAclRepo { return &fakeAclRepo{byID: map[uuid.UUID]*acl.Grant{}} }

func (r *fakeAclRepo) Save(_ context.Context, g *acl.Grant) error {
	// emulate the (subject,resource) upsert
	for id, ex := range r.byID {
		if ex.SubjectID == g.SubjectID && ex.ResourceType == g.ResourceType && ex.ResourceID == g.ResourceID {
			delete(r.byID, id)
		}
	}
	r.byID[g.ID] = g
	return nil
}
func (r *fakeAclRepo) FindByID(_ context.Context, id uuid.UUID) (*acl.Grant, error) {
	if g, ok := r.byID[id]; ok {
		return g, nil
	}
	return nil, domainerrors.ErrNotFound
}
func (r *fakeAclRepo) DeleteByID(_ context.Context, id uuid.UUID) error {
	if _, ok := r.byID[id]; !ok {
		return domainerrors.ErrNotFound
	}
	delete(r.byID, id)
	return nil
}
func (r *fakeAclRepo) ListBySubject(_ context.Context, subjectID uuid.UUID) ([]*acl.Grant, error) {
	var out []*acl.Grant
	for _, g := range r.byID {
		if g.SubjectID == subjectID {
			out = append(out, g)
		}
	}
	return out, nil
}
func (r *fakeAclRepo) ListByResource(_ context.Context, rt acl.ResourceType, rid uuid.UUID) ([]*acl.Grant, error) {
	var out []*acl.Grant
	for _, g := range r.byID {
		if g.ResourceType == rt && g.ResourceID == rid {
			out = append(out, g)
		}
	}
	return out, nil
}
func (r *fakeAclRepo) DeleteByResource(_ context.Context, rt acl.ResourceType, rid uuid.UUID) error {
	for id, g := range r.byID {
		if g.ResourceType == rt && g.ResourceID == rid {
			delete(r.byID, id)
		}
	}
	return nil
}

func mkACLUser(role user.Role) *user.User {
	u, _ := user.New("u@x.io", "h", role)
	return u
}

func fixedClockNow(t time.Time) ports.Clock { return stubClock{t} }

type stubClock struct{ t time.Time }

func (c stubClock) Now() time.Time { return c.t }

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestAclService_ScopesFor_AdminBypass(t *testing.T) {
	svc := application.NewAclService(newFakeAclRepo(), newFakeUserRepo(), fixedClockNow(time.Now()))
	scopes, err := svc.ScopesFor(context.Background(), mkACLUser(user.RoleAdmin))
	require.NoError(t, err)
	assert.Nil(t, scopes, "admin carries no scopes (bypass)")
}

func TestAclService_ScopesFor_DropsExpired(t *testing.T) {
	now := time.Now()
	repo := newFakeAclRepo()
	users := newFakeUserRepo()
	u := mkACLUser(user.RoleOperator)
	users.add(u)

	cluster := uuid.New()
	hive := uuid.New()
	past := now.Add(-time.Hour)
	repo.Save(context.Background(), &acl.Grant{ID: uuid.New(), SubjectID: u.ID, ResourceType: acl.ResourceCluster, ResourceID: cluster, Verb: acl.VerbWrite})
	repo.Save(context.Background(), &acl.Grant{ID: uuid.New(), SubjectID: u.ID, ResourceType: acl.ResourceHive, ResourceID: hive, Verb: acl.VerbManage, ExpiresAt: &past})

	svc := application.NewAclService(repo, users, fixedClockNow(now))
	scopes, err := svc.ScopesFor(context.Background(), u)
	require.NoError(t, err)
	require.Len(t, scopes, 1)
	assert.Equal(t, cluster, scopes[0].ID)
	assert.Equal(t, acl.VerbWrite, scopes[0].Verb)
}

func TestEffectiveVerb_Cascade(t *testing.T) {
	cluster := uuid.New()
	hive := uuid.New()
	scopes := []ports.Scope{
		{Type: acl.ResourceCluster, ID: cluster, Verb: acl.VerbRead},
		{Type: acl.ResourceHive, ID: hive, Verb: acl.VerbWrite},
	}
	// hive in this cluster: max(read from cluster, write from hive) = write
	assert.Equal(t, acl.VerbWrite, ports.EffectiveVerb(scopes, cluster, hive))
	// another hive in the same cluster: only the cluster read cascades
	assert.Equal(t, acl.VerbRead, ports.EffectiveVerb(scopes, cluster, uuid.New()))
	// unrelated cluster: nothing
	assert.Equal(t, 0, ports.EffectiveVerb(scopes, uuid.New(), uuid.New()).Rank())
}

func TestAclService_Grant_BumpsTokenVersion(t *testing.T) {
	now := time.Now()
	repo := newFakeAclRepo()
	users := newFakeUserRepo()
	granter := mkACLUser(user.RoleAdmin)
	subject := mkACLUser(user.RoleOperator)
	users.add(granter)
	users.add(subject)

	svc := application.NewAclService(repo, users, fixedClockNow(now))
	before := subject.TokenVersion
	g, err := svc.Grant(context.Background(), granter.ID, subject.ID, acl.ResourceHive, uuid.New(), acl.VerbWrite, nil)
	require.NoError(t, err)
	assert.Equal(t, acl.VerbWrite, g.Verb)
	assert.Equal(t, before+1, users.byID[subject.ID].TokenVersion, "grant bumps the subject's token version")
}

func TestAclService_Grant_RejectsSelf(t *testing.T) {
	svc := application.NewAclService(newFakeAclRepo(), newFakeUserRepo(), fixedClockNow(time.Now()))
	self := mkACLUser(user.RoleOperator)
	_, err := svc.Grant(context.Background(), self.ID, self.ID, acl.ResourceHive, uuid.New(), acl.VerbWrite, nil)
	assert.ErrorIs(t, err, application.ErrSelfGrant)
}

func TestAclService_Revoke_BumpsTokenVersion(t *testing.T) {
	now := time.Now()
	repo := newFakeAclRepo()
	users := newFakeUserRepo()
	admin := mkACLUser(user.RoleAdmin)
	subject := mkACLUser(user.RoleOperator)
	users.add(admin)
	users.add(subject)
	svc := application.NewAclService(repo, users, fixedClockNow(now))

	g, err := svc.Grant(context.Background(), admin.ID, subject.ID, acl.ResourceHive, uuid.New(), acl.VerbRead, nil)
	require.NoError(t, err)
	v := users.byID[subject.ID].TokenVersion

	require.NoError(t, svc.Revoke(context.Background(), admin.ID, g.ID))
	assert.Equal(t, v+1, users.byID[subject.ID].TokenVersion, "revoke bumps the subject's token version again")
}
