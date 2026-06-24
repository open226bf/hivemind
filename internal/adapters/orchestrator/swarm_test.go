package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestOrchestrator points a real Docker client at an httptest server so the
// adapter's request/response mapping can be exercised without a Swarm. A fixed
// API version avoids the negotiation ping.
func newTestOrchestrator(t *testing.T, handler http.HandlerFunc) *SwarmOrchestrator {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cli, err := client.NewClientWithOpts(
		client.WithHost(strings.Replace(srv.URL, "http://", "tcp://", 1)),
		client.WithVersion("1.45"),
	)
	require.NoError(t, err)
	return &SwarmOrchestrator{cli: cli}
}

func uint64p(v uint64) *uint64 { return &v }

func TestListServices_MapsFieldsAndLabel(t *testing.T) {
	services := []swarm.Service{
		{
			ID: "svc-foreign",
			Spec: swarm.ServiceSpec{
				Annotations: swarm.Annotations{Name: "legacy-nginx"}, // no hivemind label
				TaskTemplate: swarm.TaskSpec{
					ContainerSpec: &swarm.ContainerSpec{Image: "nginx:1.25"},
				},
				Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uint64p(3)}},
			},
		},
		{
			ID: "svc-managed",
			Spec: swarm.ServiceSpec{
				Annotations: swarm.Annotations{
					Name:   "hm-app",
					Labels: map[string]string{hivemindLabelKey: "11111111-1111-1111-1111-111111111111"},
				},
				TaskTemplate: swarm.TaskSpec{
					ContainerSpec: &swarm.ContainerSpec{Image: "app:v2"},
				},
				Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uint64p(1)}},
			},
		},
	}

	o := newTestOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/services"), "path=%s", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(services)
	})

	out, err := o.ListServices(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)

	foreign := out[0]
	assert.Equal(t, "svc-foreign", foreign.SwarmServiceID)
	assert.Equal(t, "legacy-nginx", foreign.Name)
	assert.Equal(t, "nginx:1.25", foreign.Image)
	assert.Equal(t, uint64(3), foreign.Replicas)
	assert.Empty(t, foreign.HivemindLabel, "a service with no hivemind label is foreign")

	managed := out[1]
	assert.Equal(t, "svc-managed", managed.SwarmServiceID)
	assert.Equal(t, "app:v2", managed.Image)
	assert.Equal(t, uint64(1), managed.Replicas)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", managed.HivemindLabel)
}

func TestListServices_Empty(t *testing.T) {
	o := newTestOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	out, err := o.ListServices(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out)
}
