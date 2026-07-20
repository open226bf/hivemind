package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/ports"
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

func TestInspectService_ReconstructsSpecAndWarns(t *testing.T) {
	svc := swarm.Service{
		ID: "svc-1",
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "legacy"},
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{
					Image:   "nginx:1.25",
					Command: []string{"/entry"},
					Args:    []string{"--flag"},
					Env:     []string{"FOO=bar", "BARE"},
					Secrets: []*swarm.SecretReference{{SecretName: "s1"}},
					Mounts:  []mount.Mount{{Type: mount.TypeVolume, Source: "v", Target: "/data"}},
				},
			},
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uint64p(2)}},
			EndpointSpec: &swarm.EndpointSpec{Ports: []swarm.PortConfig{{
				Protocol: swarm.PortConfigProtocolTCP, PublishMode: swarm.PortConfigPublishModeIngress,
				TargetPort: 80, PublishedPort: 8080,
			}}},
		},
	}
	o := newTestOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/services/svc-1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(svc)
	})

	got, err := o.InspectService(context.Background(), "svc-1")
	require.NoError(t, err)
	assert.Equal(t, "legacy", got.Spec.Name)
	assert.Equal(t, "nginx:1.25", got.Spec.Image)
	assert.Equal(t, []string{"--flag"}, got.Spec.Command)
	assert.Equal(t, []string{"/entry"}, got.Spec.Entrypoint)
	assert.Equal(t, uint64(2), got.Spec.Replicas)
	assert.Equal(t, "bar", got.Spec.Env["FOO"])
	assert.Equal(t, "", got.Spec.Env["BARE"])
	require.Len(t, got.Spec.Ports, 1)
	assert.Equal(t, uint32(8080), got.Spec.Ports[0].PublishedPort)

	// Unmappable aspects are reported, not dropped silently.
	joined := strings.Join(got.Warnings, " | ")
	assert.Contains(t, joined, "secret")
	assert.Contains(t, joined, "mount")
}

func TestListServices_Empty(t *testing.T) {
	o := newTestOrchestrator(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	out, err := o.ListServices(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestToSwarmSpec_SecretConfigMountFilenames(t *testing.T) {
	o := &SwarmOrchestrator{}
	spec := ports.ServiceSpec{
		Name:     "app",
		Image:    "nginx",
		Replicas: 1,
		Secrets: []ports.SecretAttachment{
			// No target path → mount under the STABLE name, not the versioned
			// SwarmSecretName, so /run/secrets/db_password is stable across rotations.
			{SwarmSecretID: "sid1", SwarmSecretName: "db_password_v3", Name: "db_password"},
			// Target path with a directory → basename only (Swarm mounts only under
			// /run/secrets, it cannot honour an arbitrary directory).
			{SwarmSecretID: "sid2", SwarmSecretName: "api_token_v1", Name: "api_token", TargetPath: "/app/conf/token"},
		},
		Configs: []ports.ConfigAttachment{
			{SwarmConfigID: "cid1", SwarmConfigName: "nginx_conf_v2", Name: "nginx_conf"},
		},
	}

	cs := o.toSwarmSpec(spec).TaskTemplate.ContainerSpec
	require.Len(t, cs.Secrets, 2)
	assert.Equal(t, "db_password", cs.Secrets[0].File.Name, "empty target path → stable name, not db_password_v3")
	assert.Equal(t, "token", cs.Secrets[1].File.Name, "directory dropped to basename")
	require.Len(t, cs.Configs, 1)
	assert.Equal(t, "nginx_conf", cs.Configs[0].File.Name, "config: empty target path → stable name")
}

func TestStripPinnedDigest(t *testing.T) {
	cases := []struct{ in, want string }{
		// A tag remains, so the digest is dropped and the tag re-resolves.
		{"nginx:alpine@sha256:abc", "nginx:alpine"},
		{"registry.example.com:5000/app:v1@sha256:abc", "registry.example.com:5000/app:v1"},
		// Nothing pinned — untouched.
		{"nginx:alpine", "nginx:alpine"},
		{"nginx", "nginx"},
		// Digest-only: no tag to re-resolve, so it stays pinned rather than
		// being silently downgraded to :latest.
		{"nginx@sha256:abc", "nginx@sha256:abc"},
		{"registry.example.com:5000/app@sha256:abc", "registry.example.com:5000/app@sha256:abc"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, stripPinnedDigest(c.in), "input %q", c.in)
	}
}

// Restarting a service Hivemind does not manage must roll its tasks and re-pull
// the image while leaving everything it references out-of-band exactly as it is.
func TestRestartService_ForcesRollAndPreservesSpec(t *testing.T) {
	current := swarm.Service{
		ID:   "svc-1",
		Meta: swarm.Meta{Version: swarm.Version{Index: 42}},
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{
				Name:   "legacy-app",
				Labels: map[string]string{"com.example.keep": "yes"},
			},
			TaskTemplate: swarm.TaskSpec{
				ForceUpdate: 7,
				ContainerSpec: &swarm.ContainerSpec{
					Image:   "registry.example.com:5000/app:v1@sha256:aaaa",
					Env:     []string{"FOO=bar"},
					Secrets: []*swarm.SecretReference{{SecretID: "sec1", SecretName: "db-pw"}},
					Configs: []*swarm.ConfigReference{{ConfigID: "cfg1", ConfigName: "app-conf"}},
					Mounts:  []mount.Mount{{Type: mount.TypeVolume, Source: "data", Target: "/data"}},
				},
			},
			Mode: swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: uint64p(2)}},
		},
	}

	var gotSpec swarm.ServiceSpec
	var gotVersion string
	updated := false

	o := newTestOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/update") {
			updated = true
			gotVersion = r.URL.Query().Get("version")
			_ = json.NewDecoder(r.Body).Decode(&gotSpec)
			_, _ = w.Write([]byte("{}"))
			return
		}
		_ = json.NewEncoder(w).Encode(current)
	})

	require.NoError(t, o.RestartService(context.Background(), "svc-1", true))
	require.True(t, updated, "a service update must be issued")
	assert.Equal(t, "42", gotVersion, "the update must target the inspected version")

	// Rolls the tasks.
	assert.Equal(t, uint64(8), gotSpec.TaskTemplate.ForceUpdate)
	// Re-pull: the digest Swarm pinned is dropped so the tag resolves afresh.
	assert.Equal(t, "registry.example.com:5000/app:v1", gotSpec.TaskTemplate.ContainerSpec.Image)

	// Everything referenced out-of-band survives verbatim.
	cs := gotSpec.TaskTemplate.ContainerSpec
	require.Len(t, cs.Secrets, 1)
	assert.Equal(t, "db-pw", cs.Secrets[0].SecretName)
	require.Len(t, cs.Configs, 1)
	assert.Equal(t, "app-conf", cs.Configs[0].ConfigName)
	require.Len(t, cs.Mounts, 1)
	assert.Equal(t, "/data", cs.Mounts[0].Target)
	assert.Equal(t, []string{"FOO=bar"}, cs.Env)
	assert.Equal(t, "legacy-app", gotSpec.Name)
	assert.Equal(t, "yes", gotSpec.Labels["com.example.keep"])
	require.NotNil(t, gotSpec.Mode.Replicated)
	assert.Equal(t, uint64(2), *gotSpec.Mode.Replicated.Replicas)
}

// Without a re-pull the image reference is left exactly as-is (digest kept).
func TestRestartService_NoPullKeepsPinnedImage(t *testing.T) {
	current := swarm.Service{
		ID:   "svc-1",
		Meta: swarm.Meta{Version: swarm.Version{Index: 1}},
		Spec: swarm.ServiceSpec{
			Annotations: swarm.Annotations{Name: "app"},
			TaskTemplate: swarm.TaskSpec{
				ContainerSpec: &swarm.ContainerSpec{Image: "app:v1@sha256:aaaa"},
			},
		},
	}
	var gotSpec swarm.ServiceSpec
	o := newTestOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/update") {
			_ = json.NewDecoder(r.Body).Decode(&gotSpec)
			_, _ = w.Write([]byte("{}"))
			return
		}
		_ = json.NewEncoder(w).Encode(current)
	})

	require.NoError(t, o.RestartService(context.Background(), "svc-1", false))
	assert.Equal(t, "app:v1@sha256:aaaa", gotSpec.TaskTemplate.ContainerSpec.Image)
	assert.Equal(t, uint64(1), gotSpec.TaskTemplate.ForceUpdate, "still rolls the tasks")
}
