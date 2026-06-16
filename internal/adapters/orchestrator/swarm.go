package orchestrator

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/orange/hivemind/internal/ports"
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

func (o *SwarmOrchestrator) Close() error { return o.cli.Close() }

// ─── Services ─────────────────────────────────────────────────────────────────

func (o *SwarmOrchestrator) DeployService(ctx context.Context, spec ports.ServiceSpec) (string, error) {
	resp, err := o.cli.ServiceCreate(ctx, o.toSwarmSpec(spec), types.ServiceCreateOptions{})
	if err != nil {
		return "", fmt.Errorf("service create: %w", err)
	}
	return resp.ID, nil
}

func (o *SwarmOrchestrator) UpdateService(ctx context.Context, swarmServiceID string, spec ports.ServiceSpec) error {
	current, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		return fmt.Errorf("service inspect: %w", err)
	}
	if _, err := o.cli.ServiceUpdate(ctx, swarmServiceID, current.Version, o.toSwarmSpec(spec), types.ServiceUpdateOptions{}); err != nil {
		return fmt.Errorf("service update: %w", err)
	}
	return nil
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

func (o *SwarmOrchestrator) GetServiceState(ctx context.Context, swarmServiceID string) (*ports.ServiceState, error) {
	svc, _, err := o.cli.ServiceInspectWithRaw(ctx, swarmServiceID, types.ServiceInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("service inspect: %w", err)
	}

	tasks, err := o.serviceTasks(ctx, swarmServiceID)
	if err != nil {
		return nil, err
	}

	// Keep only the most recent task per slot to avoid counting historical
	// shutdown/rejected tasks. Tasks without slots (slot=0) are kept as-is.
	latestBySlot := make(map[int]swarm.Task)
	for _, t := range tasks {
		if prev, ok := latestBySlot[t.Slot]; !ok || t.UpdatedAt.After(prev.UpdatedAt) {
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
		stream.Close()
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
		if n.IPAM.Config != nil && len(n.IPAM.Config) > 0 {
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
				Name: secretFileName(s.TargetPath, s.SwarmSecretName),
				Mode: 0o444,
			},
		})
	}
	for _, c := range spec.Configs {
		taskContainer.Configs = append(taskContainer.Configs, &swarm.ConfigReference{
			ConfigID:   c.SwarmConfigID,
			ConfigName: c.SwarmConfigName,
			File: &swarm.ConfigReferenceFileTarget{
				Name: defaultStr(c.TargetPath, c.SwarmConfigName),
				Mode: 0o444,
			},
		})
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
	}
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
