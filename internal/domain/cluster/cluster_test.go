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
