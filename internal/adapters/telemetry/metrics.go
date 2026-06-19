package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"

	"github.com/orange/hivemind/internal/domain/monitoring"
)

// swarmNodeIDLabel is the standard label Swarm puts on a task's container,
// identifying the node it runs on.
const swarmNodeIDLabel = "com.docker.swarm.node.id"

// sampleTimeout bounds the per-container stats read (two frames ≈ 1s of stream).
const sampleTimeout = 3 * time.Second

// CollectMetrics returns a one-shot snapshot of per-container CPU/memory for the
// containers on the connected daemon's node — the direct-mode coverage. CPU% is
// a delta, so for each container we read two frames of the stats stream and
// compute from the pair; reads run concurrently to bound latency. Containers
// whose stats can't be read are skipped rather than failing the whole snapshot.
//
// Samples are keyed by container id; the UI joins them to the health snapshot
// (which carries the service name for each container id).
func (c *DirectCollector) CollectMetrics(ctx context.Context) ([]monitoring.MetricSample, error) {
	containers, err := c.api.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("container list: %w", err)
	}

	out := make([]monitoring.MetricSample, len(containers))
	ok := make([]bool, len(containers))
	var wg sync.WaitGroup
	for i, ctr := range containers {
		wg.Add(1)
		go func(i int, ctr types.Container) {
			defer wg.Done()
			if s, err := c.sample(ctx, ctr); err == nil {
				out[i], ok[i] = s, true
			}
		}(i, ctr)
	}
	wg.Wait()

	samples := make([]monitoring.MetricSample, 0, len(out))
	for i := range out {
		if ok[i] {
			samples = append(samples, out[i])
		}
	}
	return samples, nil
}

func (c *DirectCollector) sample(ctx context.Context, ctr types.Container) (monitoring.MetricSample, error) {
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()

	resp, err := c.api.ContainerStats(ctx, ctr.ID, true)
	if err != nil {
		return monitoring.MetricSample{}, err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	var v container.StatsResponse
	if err := dec.Decode(&v); err != nil {
		return monitoring.MetricSample{}, err
	}
	// The second frame carries PreCPUStats = the first frame, giving a usable
	// ~1s CPU delta. Memory is instantaneous, so the latest frame is enough.
	gotDelta := dec.Decode(&v) == nil

	memUsed, memLimit, memPct := memUsage(v)
	cpu := 0.0
	if gotDelta {
		cpu = cpuPercent(v)
	}

	return monitoring.MetricSample{
		ContainerID:   ctr.ID,
		NodeID:        ctr.Labels[swarmNodeIDLabel],
		At:            c.now(),
		CPUPercent:    cpu,
		MemUsedBytes:  memUsed,
		MemLimitBytes: memLimit,
		MemPercent:    memPct,
	}, nil
}

// cpuPercent applies Docker's standard formula (same as `docker stats`):
// (cpuDelta / systemDelta) * onlineCPUs * 100.
func cpuPercent(v container.StatsResponse) float64 {
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	online := float64(v.CPUStats.OnlineCPUs)
	if online == 0 {
		online = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta > 0 && sysDelta > 0 && online > 0 {
		return (cpuDelta / sysDelta) * online * 100
	}
	return 0
}

// memUsage subtracts the page cache (inactive_file) from the cgroup usage, like
// `docker stats`, so the figure reflects working-set memory.
func memUsage(v container.StatsResponse) (used, limit uint64, pct float64) {
	used = v.MemoryStats.Usage
	if inactive, ok := v.MemoryStats.Stats["inactive_file"]; ok && inactive < used {
		used -= inactive
	} else if inactive, ok := v.MemoryStats.Stats["total_inactive_file"]; ok && inactive < used {
		used -= inactive
	}
	limit = v.MemoryStats.Limit
	if limit > 0 {
		pct = float64(used) / float64(limit) * 100
	}
	return used, limit, pct
}
