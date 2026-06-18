# 2. Monitoring and alerting across agent and agentless clusters

- **Status:** Accepted (preview / pre-1.0)
- **Date:** 2026-06-18
- **Deciders:** Hivemind maintainers

## Context

Hivemind has no built-in monitoring or alerting. Operators can see a service's
live replica/task state in the Supervision tab (via the SSE status stream) but
get no per-node health rollup, no per-container resource metrics, and no alerts
when something breaks.

We want to add observability **without** forcing external infrastructure on the
default deployment, and it must work across **both** cluster connection modes:

- **agent** — an agent runs inside the cluster and dials out to the control
  plane over an mTLS, yamux-multiplexed **reverse tunnel** (no inbound exposure);
- **agentless / direct** — the control plane talks straight to a manager's
  Docker API (tcp+mTLS or local socket).

A hard Docker constraint shapes everything: `GET /containers/{id}/stats` and
`GET /events` are served by the daemon you connect to and are **node-local**;
Swarm has **no cluster-wide container-stats endpoint**. Only `GET /tasks` (the
manager API) is cluster-wide.

## Decision

### The contract (this ADR / phase 0)

A driven port **`TelemetryCollector`** (with a `TelemetryCollectorRegistry`,
mirroring `Orchestrator` / `OrchestratorRegistry`) is the single seam. Two
adapters implement it, chosen by connection mode; everything downstream (alert
engine, API, UI) consumes the port and never learns the mode:

- `DirectCollector` — Docker API of the agentless cluster;
- `AgentCollector` — telemetry over the agent's reverse tunnel.

A backend-neutral domain package **`monitoring`** carries the model
(`ClusterHealth` → `NodeHealth` → `ContainerHealth`, `MetricSample`, `Alert`,
`AlertRule`). Alerts leave the engine through an **`AlertRouter`** output port
that reuses the existing `Notifier` channels (and can later forward to an
external Alertmanager).

### Granularity (what each mode can actually deliver)

| Signal | Agent | Agentless (direct) |
|---|---|---|
| Per-node container **health** ("what is struggling, where") | ✅ cluster-wide + fine signals (OOM, healthcheck) | ✅ cluster-wide from `GET /tasks` (state/reason/crashloop); fine worker signals inferred |
| Per-container **CPU/mem** metrics | ✅ cluster-wide (node-local stats pushed over tunnel) | ⚠️ only the connected node — cluster-wide needs an in-cluster exporter |

`CollectorCapabilities` advertises this asymmetry (`MetricsCoverage =
cluster | connected-node`) so callers and the UI degrade gracefully.

### Native-first, then hybrid

- **Event-driven alerts** (deploy failed, replicas < desired, task
  failed/rejected, crashloop, node unreachable, agent tunnel down) are evaluated
  **natively** from the health snapshot — **no time-series store** — and routed
  via `AlertRouter`/`Notifier`. This covers most of the value with zero external
  infra.
- **Threshold/metric alerts** (CPU/mem sustained over a window) require a TSDB
  and arrive later as a **Prometheus `/metrics` + Alertmanager** integration; in
  direct mode they also need cAdvisor/node-exporter deployed as a Swarm global
  service. `RuleKind.NeedsMetrics()` marks which rules require the metrics path.

### Phased roadmap

- **0 — Contract (this ADR):** `TelemetryCollector` port, `monitoring` domain,
  `AlertRouter` port. No implementation.
- **1 — Per-node health (MVP, both modes):** `DirectCollector` + `AgentCollector`
  health path, native event-driven engine → `Notifier`, health view in
  Supervision.
- **2 — Per-container metrics (agent):** agent pushes `stats` over the tunnel;
  recent-window store; threshold alerts.
- **3 — Direct-mode metrics + hybrid:** optional cAdvisor/node-exporter global
  service, `/metrics` on the control plane, Alertmanager (v2 API) integration.
- **4 — Polish:** notification channels per hive/cluster, alert history, RBAC on
  alert config, dashboards.

Each phase is demonstrable on its own and adds no external dependency before
phase 3.

## Consequences

**Positive**

- Reuses what already exists: the agent **reverse tunnel** as the secure push
  channel, the `Notifier` port for routing, `AgentHub.ConnectedNodeIDs` for
  per-node tunnel health, and the `Orchestrator`/registry hexagonal shape.
- The MVP (phases 0–1) ships **no external infrastructure** and works behind
  NAT/firewalls in agent mode.
- One published image serves every cluster mode; the mode-specific cost is
  isolated in one adapter.

**Negative / limitations**

- Direct-mode per-container metrics are **partial** (connected node only) until
  an exporter is deployed (phase 3).
- The native engine reimplements a slice of Alertmanager (dedup/silence/grouping)
  until the phase-3 integration; we deliberately keep it small (event-driven).
- The agent does more work (local collection + push); needs sampling/backpressure.

**Operational**

- Collection is **per cluster**: many clusters multiply control-plane work in
  direct mode (polling `/tasks`); agent mode offloads collection to the edge.
- Metrics volume must be bounded (sample interval, per-service scoping) to keep
  tunnel and memory use sane.

## When to revisit

- Demand for historical dashboards / PromQL beyond event alerts (pull phase 3
  forward).
- A non-Swarm backend (Kubernetes) — the port stays, a new adapter appears.

## References

- `internal/ports/telemetry.go` — `TelemetryCollector`, `TelemetryCollectorRegistry`, `AlertRouter`
- `internal/domain/monitoring/monitoring.go` — health, metric and alert model
- `internal/ports/driven.go` — `Orchestrator`, `Notifier`, `AgentHub` (reused)
