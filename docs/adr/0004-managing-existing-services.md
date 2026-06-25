# 4. Managing pre-existing (brownfield) services on a cluster

- **Status:** Proposed (preview / pre-1.0)
- **Date:** 2026-06-24
- **Deciders:** Hivemind maintainers

## Context

Hivemind only knows the services **it created itself**. `DeployService` stamps
each service with a `hivemind.service.id` label
(`internal/adapters/orchestrator/swarm.go` — `hivemindLabelKey`,
`withServiceLabel`) and persists the `swarm_service_id` link
(`internal/adapters/persistence/models.go`). Every `Orchestrator` operation
(`internal/ports/driven.go`) is **keyed by a `swarmServiceID` Hivemind already
holds**: `UpdateService`, `RemoveService`, `GetServiceState`, `ServiceLogs`,
`ExecContainer`.

The result is a blind spot. Services created out-of-band — `docker service
create`, `docker stack deploy`, CI pipelines, another control plane — run on the
cluster but are **invisible** to Hivemind. There is no `ListServices` on the
port (unlike `ListNetworks`/`ListVolumes`, which already exist), so the platform
cannot even enumerate what is actually running.

Drift is handled in **one direction only**: `ServiceState.ExternallyRemoved`
detects a managed service deleted out-of-band and reconciles its status to
`removed`. The inverse — a live service with no matching DB record — has no
representation at all.

We want operators to **see** and **take over** these existing services without
forcing a destroy-and-recreate, while keeping a clean line between what Hivemind
manages and what it merely observes.

**Scope of this ADR:** Swarm **services** only. Standalone containers
(`docker run`, not Swarm-scheduled) are out of scope — the entire domain model
is service-centric, and a node-local container is not addressable through the
manager-resident orchestrator the way a service is. Revisit separately if needed.

## Decision

Adopt a two-stage model: **discover first (read-only), then adopt
(write)**. Continuous background reconciliation is explicitly deferred (see
*When to revisit*).

### Identity — the label is the boundary

A live Swarm service is classified by cross-referencing its
`hivemind.service.id` label with the DB:

| Class | Condition | Meaning |
|-------|-----------|---------|
| `managed` | label present **and** matches an existing `Service` record | a first-class Hivemind service |
| `foreign` | no `hivemind.service.id` label | created out-of-band, never adopted |
| `orphan` | label/record present on one side only (label points to no record, or record's `swarm_service_id` is gone) | drift to surface, not a new entity |

The label is the single source of ownership truth: adoption **writes** it,
release **removes** it. This makes adoption idempotent and reversible.

### Stage 1 — Discovery (read-only, no new persistence)

Add one method to the `Orchestrator` port, mirroring the existing
`ListNetworks`/`ListVolumes` shape:

```go
// ListServices returns every Swarm service visible on the cluster, with the
// hivemind.service.id label (empty when absent) so the application layer can
// classify managed / foreign / orphan.
ListServices(ctx context.Context) ([]SwarmServiceInfo, error)
```

```go
type SwarmServiceInfo struct {
    SwarmServiceID string
    Name           string
    Image          string
    Replicas       uint64
    HivemindLabel  string // value of hivemind.service.id, "" if unlabelled
    CreatedAt      time.Time
}
```

A read-only endpoint merges live Swarm ↔ DB and returns the classified list:

```
GET /clusters/:id/discovered-services   (read on the cluster)
  -> [{ swarm_service_id, name, image, replicas, class, hive_id?, service_id? }]
```

The GUI gains a **"Services découverts"** tab. Because logs, exec and
`GetServiceState` are already keyed by `swarmServiceID`, **read-only
observability of foreign services works immediately** with no further plumbing.
This stage ships with **zero new tables and zero writes to the cluster**.

### Stage 2 — Adoption (take over a foreign service)

```
POST /clusters/:id/discovered-services/:swarmId/adopt   (write on the target hive)
  body: { hive_id }
```

Adoption:

1. **Inspect** the live Swarm service and reconstruct a `ServiceSpec`.
2. **Create** a `Service` record with status `deployed`, the reconstructed spec,
   `swarm_service_id` set, and `hive_id` from the request — so the adopted
   service is a first-class citizen: history, snapshots, rollback, and the
   ADR-0003 ACLs all apply via its hive.
3. **Seal ownership**: write `hivemind.service.id` onto the live service. To
   avoid a surprise rolling restart, sealing is a **label-only update** (Swarm
   `UpdateService` with the spec otherwise byte-identical to what was
   inspected); replica/image changes happen only on a later, explicit edit.
4. **Record an initial snapshot** so the pre-Hivemind state is the rollback
   floor.

A symmetric **release** (`POST .../:id/release`) removes the label and deletes
the record, leaving the service running untouched — adoption is reversible.

**Referenced resources.** A foreign service referencing networks/secrets/configs
is adopted by **reference**: the spec records the existing Swarm ids/names; we do
not clone or re-create them. Where no matching Hivemind record exists, the
discovery payload flags it so the operator can adopt those too (networks/volumes
already have `List*`); a secret/config whose value Hivemind never held stays
reference-only (its plaintext is not recoverable from Swarm).

### Spec reconstruction is lossy — and says so

`SwarmService → ServiceSpec` is best-effort. Fields with no `ServiceSpec`
equivalent (certain Swarm-native knobs) are dropped on import; the adopt
response returns a **`warnings[]`** list naming every unmapped field. The
inspected raw spec is stored in the initial snapshot so nothing is silently
lost. This is the accepted cost of reusing the existing deploy/snapshot
pipeline instead of building a parallel "imported service" code path.

### Delivery (incremental, each lot shippable)

1. Port `ListServices` + `SwarmServiceInfo`; Swarm + stub adapters; tests.
2. `GET /discovered-services` merge/classify + read-only GUI tab.
3. Adopt/release endpoints: inspect → reconstruct → record → label-seal →
   snapshot; `warnings[]`; tests (incl. orphan classification).
4. GUI adopt flow (pick hive, show warnings) and release.

Lots 1–2 are pure observability (no cluster writes). Lots 3–4 add take-over.

## Consequences

**Positive**

- Closes the brownfield blind spot: operators can see and take over existing
  services without destroy-and-recreate.
- Reuses every existing seam — the `hivemind.service.id` label, the
  multi-cluster `OrchestratorRegistry` (so discovery works **direct and agent**
  transparently), the `Cluster→Hive→Service` hierarchy, snapshots/rollback, and
  ADR-0003 ACLs (an adopted service inherits its hive's grants).
- Read-only discovery ships first with zero write risk; adoption is idempotent
  and reversible via the label.

**Negative / limitations**

- `ServiceSpec` reconstruction is lossy; mitigated by `warnings[]` + raw-spec
  snapshot, but a post-adoption edit re-applies only the mapped fields.
- The `Orchestrator` port grows a method — every adapter (Swarm, stub) and the
  agent-tunnelled path must implement `ListServices`.
- Sealing the label is a Swarm `UpdateService`; even label-only, it bumps the
  service version. Documented as expected.

**Operational**

- `ListServices` is one extra Swarm API call per discovery view; cache/paginate
  if cluster service counts grow large.
- Adoption and release are audited (reuse the existing audit log) and gated by
  `write`/`manage` on the target hive.

## When to revisit

- **Continuous reconciliation** (Approach C): a per-cluster background reconciler
  detecting foreign services and managed-service spec drift proactively, with a
  "N unmanaged services" dashboard badge. Deferred to keep this ADR additive and
  write-light.
- **Standalone containers** (`docker run`), if a node-local inventory is wanted.
- **Bulk / stack adoption** (adopt every service of a `docker stack` as one hive).

## References

- `internal/ports/driven.go` — `Orchestrator` port (`GetServiceState`,
  `ExternallyRemoved`, `ListNetworks`/`ListVolumes` shape to mirror)
- `internal/adapters/orchestrator/swarm.go` — `hivemindLabelKey`,
  `withServiceLabel`, `GetServiceState`
- `internal/adapters/persistence/models.go` — `swarm_service_id` link
- `internal/domain/service/service.go` — `Status` (`draft|deployed|removed`)
- `docs/adr/0003-fine-grained-acl.md` — hive-scoped grants inherited by adopted services
