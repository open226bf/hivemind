package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/cluster"
	"github.com/orange/hivemind/internal/ports"
)

type fakePresence struct {
	seen map[string]ports.AgentNode
}

func newFakePresence() *fakePresence { return &fakePresence{seen: map[string]ports.AgentNode{}} }

func (p *fakePresence) MarkSeen(agentID string, n ports.AgentNode) { p.seen[agentID] = n }
func (p *fakePresence) Forget(agentID string)                      { delete(p.seen, agentID) }
func (p *fakePresence) Online(agentID string) bool                 { _, ok := p.seen[agentID]; return ok }

func newAgentSvc(t *testing.T) (*application.AgentService, *fakeClusterRepo, *fakePresence, *cluster.Cluster) {
	t.Helper()
	clusters := newFakeClusterRepo()
	c, err := cluster.New("edge", cluster.TypeSwarm, "")
	require.NoError(t, err)
	require.NoError(t, clusters.Save(context.Background(), c))
	presence := newFakePresence()
	return application.NewAgentService(clusters, presence, nil, nil, "", ""), clusters, presence, c
}

func TestAgentEnrollThenRegisterReusableToken(t *testing.T) {
	svc, clusters, presence, c := newAgentSvc(t)

	enr, err := svc.Enroll(context.Background(), c.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, enr.Token)

	// Stored cluster is now in agent mode with a pending token.
	stored, _ := clusters.FindByID(context.Background(), c.ID)
	assert.Equal(t, cluster.ModeAgent, stored.ConnectionMode)

	reg, err := svc.Register(context.Background(), application.RegisterInput{
		EnrollToken: enr.Token,
		Node:        ports.AgentNode{NodeID: "n1", Role: "manager", IsManager: true, IsLeader: true},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, reg.AgentID)
	assert.Equal(t, c.ID.String(), reg.AgentID, "agent id is the stable cluster id")
	assert.Equal(t, c.ID, reg.ClusterID)
	assert.True(t, presence.Online(reg.AgentID))

	// The token is reusable (not consumed): a second register with it succeeds
	// and resolves to the same stable agent id — so extra nodes and restarts work.
	again, err := svc.Register(context.Background(), application.RegisterInput{EnrollToken: enr.Token})
	require.NoError(t, err)
	assert.Equal(t, reg.AgentID, again.AgentID)
}

func TestAgentRegister_BadToken(t *testing.T) {
	svc, _, _, _ := newAgentSvc(t)
	_, err := svc.Register(context.Background(), application.RegisterInput{EnrollToken: "nope"})
	assert.ErrorIs(t, err, application.ErrInvalidEnrollment)
}

func TestAgentReconnectAndHeartbeat(t *testing.T) {
	svc, _, presence, c := newAgentSvc(t)
	enr, err := svc.Enroll(context.Background(), c.ID)
	require.NoError(t, err)
	reg, err := svc.Register(context.Background(), application.RegisterInput{EnrollToken: enr.Token})
	require.NoError(t, err)

	// Reconnection by agent id (no token).
	again, err := svc.Register(context.Background(), application.RegisterInput{AgentID: reg.AgentID})
	require.NoError(t, err)
	assert.Equal(t, reg.AgentID, again.AgentID)

	require.NoError(t, svc.Heartbeat(context.Background(), reg.AgentID, ports.AgentNode{NodeID: "n1"}))
	assert.True(t, presence.Online(reg.AgentID))

	// Unknown agent heartbeat is rejected.
	err = svc.Heartbeat(context.Background(), "ghost", ports.AgentNode{})
	assert.ErrorIs(t, err, application.ErrAgentNotRegistered)
}
