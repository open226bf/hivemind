package orchestrator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/open226bf/hivemind/internal/ports"
)

const (
	pollInterval     = 2 * time.Second
	hivemindLabelKey = "hivemind.service.id"
)

// SwarmOrchestrator implements ports.Orchestrator against a Docker Swarm cluster
// via the Docker Engine API. Secret/Config/Network creation is idempotent on
// name so the deployment engine can call it on every reconcile.
type SwarmOrchestrator struct {
	cli *client.Client
}

// NewSwarmOrchestrator builds a Swarm orchestrator from the ambient Docker
// environment (DOCKER_HOST, etc.) and verifies connectivity.
func NewSwarmOrchestrator(ctx context.Context) (*SwarmOrchestrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker ping: %w", err)
	}
	return &SwarmOrchestrator{cli: cli}, nil
}

// ConnSpec describes how to reach a specific Docker daemon. An empty Host means
// "use the ambient Docker environment" (the single-cluster behaviour, used by
// the seeded default cluster). TLS material is in-memory PEM; when present the
// client speaks mutual TLS to a remote daemon over TCP.
type ConnSpec struct {
	Host       string
	CACert     []byte
	ClientCert []byte
	ClientKey  []byte
}

// NewSwarmOrchestratorFromSpec builds a Swarm orchestrator for an explicit
// daemon address (and optional mutual TLS), verifying connectivity. With an
// empty spec it is equivalent to NewSwarmOrchestrator.
func NewSwarmOrchestratorFromSpec(ctx context.Context, spec ConnSpec) (*SwarmOrchestrator, error) {
	if spec.Host == "" && len(spec.CACert) == 0 && len(spec.ClientCert) == 0 {
		return NewSwarmOrchestrator(ctx)
	}

	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if spec.Host != "" {
		opts = append(opts, client.WithHost(spec.Host))
	}
	if len(spec.CACert) > 0 || len(spec.ClientCert) > 0 {
		httpClient, err := tlsHTTPClient(spec)
		if err != nil {
			return nil, err
		}
		opts = append(opts, client.WithHTTPClient(httpClient))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker ping: %w", err)
	}
	return &SwarmOrchestrator{cli: cli}, nil
}

// NewSwarmOrchestratorOverDial builds a Swarm orchestrator whose Docker API
// calls are carried over a custom dialer — the agent reverse tunnel. The dialer
// opens a stream to the agent, which proxies it to the cluster's docker.sock.
// Connectivity is already proven by the live tunnel, so we do not ping here.
func NewSwarmOrchestratorOverDial(dial func(ctx context.Context, network, addr string) (net.Conn, error)) (*SwarmOrchestrator, error) {
	// DisableKeepAlives: a yamux stream is cheap, and pooling streams across the
	// tunnel is unsafe — after a reconnect the dialer resolves a new session, but
	// the transport could otherwise hand a request a pooled stream from the old
	// (now closed) session. One fresh stream per request keeps reconnects
	// transparent.
	httpClient := &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}}
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://hivemind-agent:2375"), // dummy: the dialer ignores the address
		client.WithHTTPClient(httpClient),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client over tunnel: %w", err)
	}
	return &SwarmOrchestrator{cli: cli}, nil
}

// tlsHTTPClient builds an HTTP client speaking mutual TLS from in-memory PEM,
// so cluster credentials never have to be written to disk.
func tlsHTTPClient(spec ConnSpec) (*http.Client, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}

	if len(spec.CACert) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(spec.CACert) {
			return nil, errors.New("cluster TLS: invalid CA certificate PEM")
		}
		cfg.RootCAs = pool
	}
	if len(spec.ClientCert) > 0 || len(spec.ClientKey) > 0 {
		cert, err := tls.X509KeyPair(spec.ClientCert, spec.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("cluster TLS: invalid client key pair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}, nil
}

func (o *SwarmOrchestrator) Close() error { return o.cli.Close() }

// ─── Services ─────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) DeployService(ctx context.Context, spec ports.ServiceSpec) (string, error) {
	resp, err := o.cli.ServiceCreate(ctx, o.toSwarmSpec(spec), types.ServiceCreateOptions{})
	if err != nil {
		return "", fmt.Errorf("service create: %w", err)
	}
	return resp.ID, nil
}

func (o *SwarmOrchestrator) UpdateService(ctx context.Context, swarmServiceID string, spec ports.ServiceSpec, opts ports.UpdateServiceOptions) error {
	current, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("service inspect: %w", err)
	}
	swarmSpec := o.toSwarmSpec(spec)
	if opts.Force {
		// Incrementing ForceUpdate is Swarm's documented way to recreate every
		// task even when nothing else in the spec has changed.
		swarmSpec.TaskTemplate.ForceUpdate = current.Spec.TaskTemplate.ForceUpdate + 1
	}
	updateOpts := types.ServiceUpdateOptions{QueryRegistry: opts.QueryRegistry}
	if _, err := o.cli.ServiceUpdate(ctx, swarmServiceID, current.Version, swarmSpec, updateOpts); err != nil {
		return fmt.Errorf("service update: %w", err)
	}
	return nil
}

// RestartService re-applies a service's current raw spec with an incremented
// ForceUpdate — Swarm's documented way to recreate every task without changing
// anything else. Because the spec is reused verbatim, secrets, configs, mounts,
// networks and env the service references stay exactly as they are; Hivemind
// only asks Swarm to roll the tasks. With pull set, the digest Swarm pinned at
// deploy time is stripped and the registry re-queried so the tag resolves to the
// latest image.
func (o *SwarmOrchestrator) RestartService(ctx context.Context, swarmServiceID string, pull bool) error {
	current, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ports.ErrSwarmServiceNotFound
		}
		return fmt.Errorf("service inspect: %w", err)
	}
	spec := current.Spec
	spec.TaskTemplate.ForceUpdate = current.Spec.TaskTemplate.ForceUpdate + 1
	if pull && spec.TaskTemplate.ContainerSpec != nil {
		spec.TaskTemplate.ContainerSpec.Image = stripPinnedDigest(spec.TaskTemplate.ContainerSpec.Image)
	}
	if _, err := o.cli.ServiceUpdate(ctx, swarmServiceID, current.Version, spec,
		types.ServiceUpdateOptions{QueryRegistry: pull}); err != nil {
		return fmt.Errorf("service update (restart): %w", err)
	}
	return nil
}

// stripPinnedDigest drops the "@sha256:…" Swarm appends once it has resolved an
// image, so the tag is re-resolved against the registry on the next update.
// A digest-only reference (no tag to re-resolve) is returned untouched rather
// than silently downgraded to :latest.
func stripPinnedDigest(image string) string {
	at := strings.Index(image, "@")
	if at <= 0 {
		return image
	}
	base := image[:at]
	// A tag is a ':' after the last '/' (which would otherwise be a registry port).
	if strings.LastIndex(base, ":") > strings.LastIndex(base, "/") {
		return base
	}
	return image
}

func (o *SwarmOrchestrator) RemoveService(ctx context.Context, swarmServiceID string) error {
	if err := o.cli.ServiceRemove(ctx, swarmServiceID); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("service remove: %w", err)
	}
	return nil
}

// ListServices enumerates every Swarm service on the cluster for brownfield
// discovery (ADR 0004). It reads the hivemind.service.id label off each service
// spec so the application layer can tell managed services from foreign ones; it
// does not fetch task state (that stays per-service via GetServiceState).
func (o *SwarmOrchestrator) ListServices(ctx context.Context) ([]ports.SwarmServiceInfo, error) {
	list, err := o.cli.ServiceList(ctx, types.ServiceListOptions{})
	if err != nil {
		return nil, fmt.Errorf("service list: %w", err)
	}
	out := make([]ports.SwarmServiceInfo, 0, len(list))
	for _, svc := range list {
		info := ports.SwarmServiceInfo{
			SwarmServiceID: svc.ID,
			Name:           svc.Spec.Name,
			HivemindLabel:  svc.Spec.Labels[hivemindLabelKey],
			CreatedAt:      svc.CreatedAt,
		}
		if r := svc.Spec.Mode.Replicated; r != nil && r.Replicas != nil {
			info.Replicas = *r.Replicas
		}
		info.Image = svc.Spec.TaskTemplate.ContainerSpec.Image
		out = append(out, info)
	}
	return out, nil
}

// InspectService reconstructs a running service's spec into a ports.ServiceSpec
// for adoption (ADR 0004). Cleanly-mappable fields (image, replicas, command,
// env, resources, placement, update config, ports) are carried over; anything
// without a ServiceSpec equivalent is reported in Warnings rather than silently
// dropped, so the caller can surface it to the operator.
func (o *SwarmOrchestrator) InspectService(ctx context.Context, swarmServiceID string) (*ports.InspectedService, error) {
	svc, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, ports.ErrSwarmServiceNotFound
		}
		return nil, fmt.Errorf("service inspect: %w", err)
	}

	cs := svc.Spec.TaskTemplate.ContainerSpec
	spec := ports.ServiceSpec{
		Name:         svc.Spec.Name,
		UpdateConfig: fromUpdateConfig(svc.Spec.UpdateConfig),
	}
	var warnings []string
	if cs != nil {
		spec.Image = cs.Image
		spec.Entrypoint = cs.Command // Swarm Command is the entrypoint override
		spec.Command = cs.Args       // Swarm Args are the command arguments
		spec.Env = envMap(cs.Env)
		if len(cs.Secrets) > 0 {
			warnings = append(warnings, fmt.Sprintf("%d secret reference(s) not imported — re-attach after adoption", len(cs.Secrets)))
		}
		if len(cs.Configs) > 0 {
			warnings = append(warnings, fmt.Sprintf("%d config reference(s) not imported — re-attach after adoption", len(cs.Configs)))
		}
		if len(cs.Mounts) > 0 {
			warnings = append(warnings, fmt.Sprintf("%d mount(s) not imported — re-declare after adoption", len(cs.Mounts)))
		}
		if cs.Healthcheck != nil {
			warnings = append(warnings, "container healthcheck not imported")
		}
	}

	if r := svc.Spec.Mode.Replicated; r != nil && r.Replicas != nil {
		spec.Replicas = *r.Replicas
	} else if svc.Spec.Mode.Global != nil {
		warnings = append(warnings, "service runs in global mode; adopted as a single-replica replicated service")
		spec.Replicas = 1
	}

	spec.Resources = fromResources(svc.Spec.TaskTemplate.Resources)
	spec.Placement = fromPlacement(svc.Spec.TaskTemplate.Placement)
	if len(svc.Spec.TaskTemplate.Networks) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d network attachment(s) not imported — re-attach after adoption", len(svc.Spec.TaskTemplate.Networks)))
	}
	if svc.Spec.EndpointSpec != nil {
		spec.Ports = fromPorts(svc.Spec.EndpointSpec.Ports)
	}

	return &ports.InspectedService{Spec: spec, Warnings: warnings}, nil
}

// SetHivemindLabel stamps hivemind.service.id onto an existing service without
// otherwise changing its spec (adoption seal, ADR 0004): it re-applies the
// service's CURRENT raw spec with only the label added, so nothing else drifts.
func (o *SwarmOrchestrator) SetHivemindLabel(ctx context.Context, swarmServiceID, hivemindServiceID string) error {
	return o.updateLabel(ctx, swarmServiceID, func(labels map[string]string) map[string]string {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[hivemindLabelKey] = hivemindServiceID
		return labels
	})
}

// ClearHivemindLabel removes hivemind.service.id, leaving the rest of the spec
// intact (adoption release, ADR 0004).
func (o *SwarmOrchestrator) ClearHivemindLabel(ctx context.Context, swarmServiceID string) error {
	return o.updateLabel(ctx, swarmServiceID, func(labels map[string]string) map[string]string {
		delete(labels, hivemindLabelKey)
		return labels
	})
}

// updateLabel re-applies a service's current raw spec with its labels rewritten
// by mutate — a lossless, label-only ServiceUpdate.
func (o *SwarmOrchestrator) updateLabel(ctx context.Context, swarmServiceID string, mutate func(map[string]string) map[string]string) error {
	current, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return ports.ErrSwarmServiceNotFound
		}
		return fmt.Errorf("service inspect: %w", err)
	}
	spec := current.Spec
	spec.Labels = mutate(spec.Labels)
	if _, err := o.cli.ServiceUpdate(ctx, swarmServiceID, current.Version, spec, types.ServiceUpdateOptions{}); err != nil {
		return fmt.Errorf("service update (label): %w", err)
	}
	return nil
}

func (o *SwarmOrchestrator) GetServiceState(ctx context.Context, swarmServiceID string) (*ports.ServiceState, error) {
	svc, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, ports.ErrSwarmServiceNotFound
		}
		return nil, fmt.Errorf("service inspect: %w", err)
	}

	tasks, err := o.serviceTasks(ctx, swarmServiceID)
	if err != nil {
		return nil, err
	}

	// Keep only the most recent task per slot to avoid counting historical
	// shutdown/rejected tasks. Tasks without slots (slot=0) are kept as-is.
	// Use CreatedAt (not UpdatedAt) because a rolling update marks the old task
	// as shutdown AFTER the new task is already running, which would make the
	// old task appear more recent by UpdatedAt and cause the running task to be
	// ignored.
	latestBySlot := make(map[int]swarm.Task)
	for _, t := range tasks {
		if prev, ok := latestBySlot[t.Slot]; !ok || t.CreatedAt.After(prev.CreatedAt) {
			latestBySlot[t.Slot] = t
		}
	}

	// TaskList does not populate NetworksAttachments.Addresses; call TaskInspect
	// concurrently for each running task to retrieve per-task IPs.
	type inspectResult struct {
		slot int
		nets []swarm.NetworkAttachment
	}
	inspectCh := make(chan inspectResult, len(latestBySlot))
	var wg sync.WaitGroup
	for slot, t := range latestBySlot {
		if t.Status.State != swarm.TaskStateRunning {
			inspectCh <- inspectResult{slot: slot}
			continue
		}
		wg.Add(1)
		go func(slot int, taskID string) {
			defer wg.Done()
			full, _, err := o.cli.TaskInspectWithRaw(ctx, taskID)
			if err != nil {
				inspectCh <- inspectResult{slot: slot}
				return
			}
			inspectCh <- inspectResult{slot: slot, nets: full.NetworksAttachments}
		}(slot, t.ID)
	}
	wg.Wait()
	close(inspectCh)

	networksBySlot := make(map[int][]swarm.NetworkAttachment, len(latestBySlot))
	for r := range inspectCh {
		networksBySlot[r.slot] = r.nets
	}

	state := &ports.ServiceState{Desired: desiredReplicas(svc)}
	for _, t := range latestBySlot {
		ts := ports.TaskState{
			ID:           t.ID,
			Node:         t.NodeID,
			Image:        t.Spec.ContainerSpec.Image,
			Slot:         t.Slot,
			CurrentState: string(t.Status.State),
			DesiredState: string(t.DesiredState),
			Message:      t.Status.Message,
			ErrorMessage: t.Status.Err,
			CreatedAt:    t.CreatedAt,
			UpdatedAt:    t.UpdatedAt,
		}
		if t.Status.ContainerStatus != nil {
			ts.ContainerID = t.Status.ContainerStatus.ContainerID
			ts.PID = t.Status.ContainerStatus.PID
			ec := t.Status.ContainerStatus.ExitCode
			ts.ExitCode = &ec
		}
		for _, na := range networksBySlot[t.Slot] {
			for _, addr := range na.Addresses {
				ts.Networks = append(ts.Networks, ports.TaskNetwork{
					Name:    na.Network.Spec.Name,
					Address: addr,
				})
			}
		}
		state.Tasks = append(state.Tasks, ts)
		switch t.Status.State {
		case swarm.TaskStateRunning:
			state.Running++
		case swarm.TaskStateFailed, swarm.TaskStateRejected:
			state.Failed++
		case swarm.TaskStateComplete:
		default:
			state.Pending++
		}
	}
	state.Updating = svc.UpdateStatus != nil && svc.UpdateStatus.State == swarm.UpdateStateUpdating
	return state, nil
}

func (o *SwarmOrchestrator) ServiceLogs(ctx context.Context, swarmServiceID string, opts ports.LogOptions) (io.ReadCloser, error) {
	tail := opts.Tail
	if tail == "" {
		tail = "200"
	}
	stream, err := o.cli.ServiceLogs(ctx, swarmServiceID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
		Tail:       tail,
		Timestamps: opts.Timestamps,
		Since:      opts.Since,
	})
	if err != nil {
		return nil, fmt.Errorf("service logs: %w", err)
	}

	// Docker multiplexes stdout/stderr into a single framed stream (8-byte
	// header per frame). Demultiplex it into merged plain bytes via a pipe so
	// callers receive clean log lines.
	pr, pw := io.Pipe()
	go func() {
		_, copyErr := stdcopy.StdCopy(pw, pw, stream)
		_ = stream.Close()
		pw.CloseWithError(copyErr)
	}()
	return pr, nil
}

func (o *SwarmOrchestrator) ExecContainer(ctx context.Context, containerID string, opts ports.ExecOptions) (ports.ExecStream, error) {
	cmd := opts.Cmd
	if len(cmd) == 0 {
		// Fall back to sh; most images ship it even when bash is absent.
		cmd = []string{"/bin/sh"}
	}
	created, err := o.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          opts.Tty,
		Cmd:          cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}
	resp, err := o.cli.ContainerExecAttach(ctx, created.ID, container.ExecStartOptions{Tty: opts.Tty})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	return &swarmExecStream{cli: o.cli, execID: created.ID, resp: resp}, nil
}

// swarmExecStream adapts a Docker hijacked exec connection to ports.ExecStream.
type swarmExecStream struct {
	cli    *client.Client
	execID string
	resp   types.HijackedResponse
}

func (s *swarmExecStream) Read(p []byte) (int, error)  { return s.resp.Reader.Read(p) }
func (s *swarmExecStream) Write(p []byte) (int, error) { return s.resp.Conn.Write(p) }

func (s *swarmExecStream) Close() error {
	s.resp.Close()
	return nil
}

func (s *swarmExecStream) Resize(ctx context.Context, height, width uint) error {
	return s.cli.ContainerExecResize(ctx, s.execID, container.ResizeOptions{Height: height, Width: width})
}

func (o *SwarmOrchestrator) WaitConvergence(ctx context.Context, swarmServiceID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		st, err := o.GetServiceState(ctx, swarmServiceID)
		if err != nil {
			return err
		}
		if st.Running >= st.Desired && !st.Updating {
			return nil
		}
		if st.Failed > 0 && st.Running == 0 && !st.Updating {
			return fmt.Errorf("all tasks failed or rejected: %d/%d desired, %d failed", st.Running, st.Desired, st.Failed)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s: %d/%d tasks running", timeout, st.Running, st.Desired)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ─── Secrets ──────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) CreateSecret(ctx context.Context, name string, value []byte) (string, error) {
	resp, err := o.cli.SecretCreate(ctx, swarm.SecretSpec{
		Annotations: swarm.Annotations{Name: name},
		Data:        value,
	})
	if err != nil {
		if errdefs.IsConflict(err) {
			return o.secretIDByName(ctx, name)
		}
		return "", fmt.Errorf("secret create: %w", err)
	}
	return resp.ID, nil
}

func (o *SwarmOrchestrator) RemoveSecret(ctx context.Context, swarmSecretID string) error {
	if err := o.cli.SecretRemove(ctx, swarmSecretID); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("secret remove: %w", err)
	}
	return nil
}

func (o *SwarmOrchestrator) secretIDByName(ctx context.Context, name string) (string, error) {
	list, err := o.cli.SecretList(ctx, types.SecretListOptions{Filters: nameFilter(name)})
	if err != nil {
		return "", fmt.Errorf("secret list: %w", err)
	}
	for _, s := range list {
		if s.Spec.Name == name {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("secret %q reported as existing but not found", name)
}

// ─── Configs ──────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) CreateConfig(ctx context.Context, name string, content []byte) (string, error) {
	resp, err := o.cli.ConfigCreate(ctx, swarm.ConfigSpec{
		Annotations: swarm.Annotations{Name: name},
		Data:        content,
	})
	if err != nil {
		if errdefs.IsConflict(err) {
			return o.configIDByName(ctx, name)
		}
		return "", fmt.Errorf("config create: %w", err)
	}
	return resp.ID, nil
}

func (o *SwarmOrchestrator) RemoveConfig(ctx context.Context, swarmConfigID string) error {
	if err := o.cli.ConfigRemove(ctx, swarmConfigID); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("config remove: %w", err)
	}
	return nil
}

func (o *SwarmOrchestrator) configIDByName(ctx context.Context, name string) (string, error) {
	list, err := o.cli.ConfigList(ctx, types.ConfigListOptions{Filters: nameFilter(name)})
	if err != nil {
		return "", fmt.Errorf("config list: %w", err)
	}
	for _, c := range list {
		if c.Spec.Name == name {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("config %q reported as existing but not found", name)
}

// ─── Networks ─────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) CreateNetwork(ctx context.Context, name string, opts ports.CreateNetworkOptions) (string, error) {
	createOpts := network.CreateOptions{
		Driver:     "overlay",
		Scope:      "swarm",
		Attachable: opts.Attachable,
	}
	if opts.Subnet != "" {
		createOpts.IPAM = &network.IPAM{
			Config: []network.IPAMConfig{{Subnet: opts.Subnet}},
		}
	}
	resp, err := o.cli.NetworkCreate(ctx, name, createOpts)
	if err != nil {
		if errdefs.IsConflict(err) {
			return o.networkIDByName(ctx, name)
		}
		return "", fmt.Errorf("network create: %w", err)
	}
	return resp.ID, nil
}

func (o *SwarmOrchestrator) RemoveNetwork(ctx context.Context, swarmNetworkID string) error {
	if err := o.cli.NetworkRemove(ctx, swarmNetworkID); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("network remove: %w", err)
	}
	return nil
}

func (o *SwarmOrchestrator) ListNetworks(ctx context.Context) ([]ports.SwarmNetworkInfo, error) {
	list, err := o.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("network list: %w", err)
	}
	var out []ports.SwarmNetworkInfo
	for _, n := range list {
		if n.Driver != "overlay" {
			continue
		}
		info := ports.SwarmNetworkInfo{
			ID:     n.ID,
			Name:   n.Name,
			Scope:  n.Scope,
			Driver: n.Driver,
		}
		if len(n.IPAM.Config) > 0 {
			info.Subnet = n.IPAM.Config[0].Subnet
		}
		out = append(out, info)
	}
	return out, nil
}

func (o *SwarmOrchestrator) networkIDByName(ctx context.Context, name string) (string, error) {
	list, err := o.cli.NetworkList(ctx, network.ListOptions{Filters: nameFilter(name)})
	if err != nil {
		return "", fmt.Errorf("network list: %w", err)
	}
	for _, n := range list {
		if n.Name == name {
			return n.ID, nil
		}
	}
	return "", fmt.Errorf("network %q reported as existing but not found", name)
}

// ─── Volumes ──────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) CreateVolume(ctx context.Context, name, driver string) error {
	if driver == "" {
		driver = "local"
	}
	_, err := o.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Driver: driver})
	if err != nil {
		// VolumeCreate is idempotent on name when the driver/opts match, so a
		// conflict is not an error for our ensure-on-deploy semantics.
		if errdefs.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("volume create: %w", err)
	}
	return nil
}

func (o *SwarmOrchestrator) RemoveVolume(ctx context.Context, name string) error {
	if err := o.cli.VolumeRemove(ctx, name, false); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("volume remove: %w", err)
	}
	return nil
}

func (o *SwarmOrchestrator) ListVolumes(ctx context.Context) ([]ports.SwarmVolumeInfo, error) {
	resp, err := o.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("volume list: %w", err)
	}
	out := make([]ports.SwarmVolumeInfo, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		out = append(out, ports.SwarmVolumeInfo{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			Scope:      v.Scope,
		})
	}
	return out, nil
}

// ─── Cluster ──────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) ClusterInfo(ctx context.Context) (*ports.ClusterInfo, error) {
	nodes, err := o.cli.NodeList(ctx, types.NodeListOptions{})
	if err != nil {
		return nil, fmt.Errorf("node list: %w", err)
	}
	out := make([]ports.NodeInfo, 0, len(nodes))
	for _, n := range nodes {
		info := ports.NodeInfo{
			ID:            n.ID,
			Hostname:      n.Description.Hostname,
			Role:          string(n.Spec.Role),
			Leader:        n.ManagerStatus != nil && n.ManagerStatus.Leader,
			Availability:  string(n.Spec.Availability),
			State:         string(n.Status.State),
			Addr:          n.Status.Addr,
			EngineVersion: n.Description.Engine.EngineVersion,
			CPUs:          float64(n.Description.Resources.NanoCPUs) / 1e9,
			MemoryBytes:   n.Description.Resources.MemoryBytes,
		}
		if os := n.Description.Platform.OS; os != "" {
			info.Platform = os + "/" + n.Description.Platform.Architecture
		}
		out = append(out, info)
	}
	return &ports.ClusterInfo{Nodes: out}, nil
}

// ─── Spec translation ─────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) toSwarmSpec(spec ports.ServiceSpec) swarm.ServiceSpec {
	taskContainer := &swarm.ContainerSpec{
		Image:   spec.Image,
		Command: spec.Entrypoint, // Swarm Command is the entrypoint override
		Args:    spec.Command,    // Swarm Args are the command arguments
		Env:     envSlice(spec.Env),
	}

	for _, s := range spec.Secrets {
		taskContainer.Secrets = append(taskContainer.Secrets, &swarm.SecretReference{
			SecretID:   s.SwarmSecretID,
			SecretName: s.SwarmSecretName,
			File: &swarm.SecretReferenceFileTarget{
				Name: secretFileName(s.TargetPath, s.Name),
				Mode: 0o444,
			},
		})
	}
	for _, c := range spec.Configs {
		taskContainer.Configs = append(taskContainer.Configs, &swarm.ConfigReference{
			ConfigID:   c.SwarmConfigID,
			ConfigName: c.SwarmConfigName,
			File: &swarm.ConfigReferenceFileTarget{
				Name: defaultStr(c.TargetPath, c.Name),
				Mode: 0o444,
			},
		})
	}
	for _, m := range spec.Mounts {
		taskContainer.Mounts = append(taskContainer.Mounts, toMount(m))
	}

	task := swarm.TaskSpec{
		ContainerSpec: taskContainer,
		Resources:     toResources(spec.Resources),
		Placement:     toPlacement(spec.Placement),
	}
	for _, n := range spec.Networks {
		task.Networks = append(task.Networks, swarm.NetworkAttachmentConfig{Target: n.SwarmNetworkID})
	}

	replicas := spec.Replicas
	return swarm.ServiceSpec{
		Annotations: swarm.Annotations{
			Name:   spec.Name,
			Labels: withServiceLabel(spec.Labels),
		},
		TaskTemplate: task,
		Mode:         swarm.ServiceMode{Replicated: &swarm.ReplicatedService{Replicas: &replicas}},
		UpdateConfig: toUpdateConfig(spec.UpdateConfig),
		EndpointSpec: toEndpointSpec(spec.Ports),
	}
}

// toEndpointSpec maps published-port specs to Swarm's EndpointSpec. Returns nil
// when no port is published so the service spec stays minimal (and diff-stable).
func toEndpointSpec(specs []ports.PortSpec) *swarm.EndpointSpec {
	if len(specs) == 0 {
		return nil
	}
	out := &swarm.EndpointSpec{Ports: make([]swarm.PortConfig, 0, len(specs))}
	for _, p := range specs {
		out.Ports = append(out.Ports, swarm.PortConfig{
			Protocol:      swarm.PortConfigProtocol(p.Protocol),
			PublishMode:   swarm.PortConfigPublishMode(p.Mode),
			TargetPort:    p.TargetPort,
			PublishedPort: p.PublishedPort,
		})
	}
	return out
}

func toResources(r ports.ResourceSpec) *swarm.ResourceRequirements {
	req := &swarm.ResourceRequirements{}
	if r.CPULimit > 0 || r.MemLimit > 0 {
		req.Limits = &swarm.Limit{
			NanoCPUs:    int64(r.CPULimit * 1e9),
			MemoryBytes: r.MemLimit,
		}
	}
	if r.CPUReservation > 0 || r.MemReservation > 0 {
		req.Reservations = &swarm.Resources{
			NanoCPUs:    int64(r.CPUReservation * 1e9),
			MemoryBytes: r.MemReservation,
		}
	}
	return req
}

// toPlacement maps a placement spec to Swarm's Placement. Returns nil when no
// placement rule is set so the service spec stays minimal (and diff-stable).
func toPlacement(p ports.PlacementSpec) *swarm.Placement {
	if len(p.Constraints) == 0 && len(p.Preferences) == 0 && p.MaxReplicas == 0 {
		return nil
	}
	out := &swarm.Placement{
		Constraints: p.Constraints,
		MaxReplicas: p.MaxReplicas,
	}
	for _, pref := range p.Preferences {
		out.Preferences = append(out.Preferences, swarm.PlacementPreference{
			Spread: &swarm.SpreadOver{SpreadDescriptor: pref},
		})
	}
	return out
}

// toMount maps a platform mount spec to Docker's mount.Mount.
func toMount(m ports.MountSpec) mount.Mount {
	return mount.Mount{
		Type:     mount.Type(m.Type),
		Source:   m.Source,
		Target:   m.Target,
		ReadOnly: m.ReadOnly,
	}
}

func toUpdateConfig(uc ports.UpdateConfigSpec) *swarm.UpdateConfig {
	return &swarm.UpdateConfig{
		Parallelism:     uc.Parallelism,
		Delay:           uc.Delay,
		FailureAction:   uc.FailureAction,
		Monitor:         uc.Monitor,
		MaxFailureRatio: float32(uc.MaxFailureRatio),
		Order:           uc.Order,
	}
}

// ─── Reverse spec translation (adoption inspect, ADR 0004) ─────────────────────

func fromResources(req *swarm.ResourceRequirements) ports.ResourceSpec {
	var out ports.ResourceSpec
	if req == nil {
		return out
	}
	if l := req.Limits; l != nil {
		out.CPULimit = float64(l.NanoCPUs) / 1e9
		out.MemLimit = l.MemoryBytes
	}
	if r := req.Reservations; r != nil {
		out.CPUReservation = float64(r.NanoCPUs) / 1e9
		out.MemReservation = r.MemoryBytes
	}
	return out
}

func fromPlacement(p *swarm.Placement) ports.PlacementSpec {
	var out ports.PlacementSpec
	if p == nil {
		return out
	}
	out.Constraints = p.Constraints
	out.MaxReplicas = p.MaxReplicas
	for _, pref := range p.Preferences {
		if pref.Spread != nil {
			out.Preferences = append(out.Preferences, pref.Spread.SpreadDescriptor)
		}
	}
	return out
}

func fromUpdateConfig(uc *swarm.UpdateConfig) ports.UpdateConfigSpec {
	if uc == nil {
		return ports.UpdateConfigSpec{}
	}
	return ports.UpdateConfigSpec{
		Parallelism:     uc.Parallelism,
		Delay:           uc.Delay,
		FailureAction:   uc.FailureAction,
		Monitor:         uc.Monitor,
		MaxFailureRatio: float64(uc.MaxFailureRatio),
		Order:           uc.Order,
	}
}

func fromPorts(ports_ []swarm.PortConfig) []ports.PortSpec {
	out := make([]ports.PortSpec, 0, len(ports_))
	for _, p := range ports_ {
		out = append(out, ports.PortSpec{
			TargetPort:    p.TargetPort,
			PublishedPort: p.PublishedPort,
			Protocol:      string(p.Protocol),
			Mode:          string(p.PublishMode),
		})
	}
	return out
}

// envMap parses Swarm's "KEY=VALUE" env entries back into a map. Entries without
// a '=' are kept as a bare key with an empty value.
func envMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		out[k] = v
	}
	return out
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) serviceTasks(ctx context.Context, swarmServiceID string) ([]swarm.Task, error) {
	f := filters.NewArgs()
	f.Add("service", swarmServiceID)
	tasks, err := o.cli.TaskList(ctx, types.TaskListOptions{Filters: f})
	if err != nil {
		return nil, fmt.Errorf("task list: %w", err)
	}
	return tasks, nil
}

func desiredReplicas(svc swarm.Service) int {
	if svc.Spec.Mode.Replicated != nil && svc.Spec.Mode.Replicated.Replicas != nil {
		return int(*svc.Spec.Mode.Replicated.Replicas)
	}
	return 1 // global mode or unknown: treat one running task as converged
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out) // deterministic spec → avoids spurious updates
	return out
}

func nameFilter(name string) filters.Args {
	f := filters.NewArgs()
	f.Add("name", name)
	return f
}

func withServiceLabel(labels map[string]string) map[string]string {
	if labels == nil {
		labels = map[string]string{}
	}
	return labels
}

// secretFileName resolves the filename a secret is mounted as under
// /run/secrets. Swarm only controls the filename (not an arbitrary path), so we
// use the basename of the requested target path, falling back to the secret name.
func secretFileName(targetPath, fallback string) string {
	if targetPath == "" {
		return fallback
	}
	return path.Base(targetPath)
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
