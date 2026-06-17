package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/domain/cluster"
)

func TestNew_DefaultsToSwarm(t *testing.T) {
	c, err := cluster.New("prod", "", "tcp://10.0.0.1:2376")
	require.NoError(t, err)
	assert.Equal(t, cluster.TypeSwarm, c.Type)
	assert.Equal(t, cluster.StatusUnknown, c.Status)
	assert.False(t, c.IsDefault)
	assert.NotEqual(t, "00000000-0000-0000-0000-000000000000", c.ID.String())
}

func TestNew_RejectsBadNameAndType(t *testing.T) {
	_, err := cluster.New("", cluster.TypeSwarm, "")
	assert.ErrorIs(t, err, cluster.ErrInvalidName)

	_, err = cluster.New("ok", cluster.Type("k8s"), "")
	assert.ErrorIs(t, err, cluster.ErrInvalidType)
}

func TestTLS_Enabled(t *testing.T) {
	assert.False(t, cluster.TLS{}.Enabled())
	assert.True(t, cluster.TLS{CACert: "pem"}.Enabled())
}

func TestSetEndpointAndStatus(t *testing.T) {
	c, err := cluster.New("prod", cluster.TypeSwarm, "")
	require.NoError(t, err)

	c.SetEndpoint("tcp://1.2.3.4:2376", cluster.TLS{CACert: "ca"})
	assert.Equal(t, "tcp://1.2.3.4:2376", c.Endpoint)
	assert.True(t, c.TLS.Enabled())

	c.MarkStatus(cluster.StatusReachable)
	assert.Equal(t, cluster.StatusReachable, c.Status)
}

func TestNew_DefaultsToDirectMode(t *testing.T) {
	c, err := cluster.New("prod", cluster.TypeSwarm, "")
	require.NoError(t, err)
	assert.Equal(t, cluster.ModeDirect, c.ConnectionMode)
}

func TestUseAgentMode_ClearsDirectConnectionAndPends(t *testing.T) {
	c, _ := cluster.New("edge", cluster.TypeSwarm, "tcp://10.0.0.1:2376")
	c.SetEndpoint("tcp://10.0.0.1:2376", cluster.TLS{CACert: "ca"})

	c.UseAgentMode()

	assert.Equal(t, cluster.ModeAgent, c.ConnectionMode)
	assert.Empty(t, c.Endpoint)
	assert.False(t, c.TLS.Enabled())
	assert.Equal(t, cluster.AgentPending, c.AgentStatus)
}

func TestGenerateEnrollment_RequiresAgentMode(t *testing.T) {
	c, _ := cluster.New("prod", cluster.TypeSwarm, "")
	_, err := c.GenerateEnrollment()
	assert.ErrorIs(t, err, cluster.ErrNotAgentMode)
}

func TestEnrollmentMatchesThenBindConsumes(t *testing.T) {
	c, _ := cluster.New("edge", cluster.TypeSwarm, "")
	c.UseAgentMode()

	token, err := c.GenerateEnrollment()
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.NotEmpty(t, c.EnrollmentTokenHash)

	ok, err := c.MatchEnrollment(token)
	require.NoError(t, err)
	assert.True(t, ok)

	wrong, err := c.MatchEnrollment("nope")
	require.NoError(t, err)
	assert.False(t, wrong)

	c.BindAgent("agent-123")
	assert.Equal(t, "agent-123", c.AgentID)
	assert.Equal(t, cluster.AgentOnline, c.AgentStatus)
	assert.Empty(t, c.EnrollmentTokenHash, "bind must consume the one-time token")

	_, err = c.MatchEnrollment(token)
	assert.ErrorIs(t, err, cluster.ErrNoEnrollment)
}
