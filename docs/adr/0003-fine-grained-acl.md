# 3. Fine-grained ACLs for clusters and hives

- **Status:** Proposed (preview / pre-1.0)
- **Date:** 2026-06-23
- **Deciders:** Hivemind maintainers

## Context

Authorization today is **global RBAC only**. Three hierarchical roles
(`viewer < operator < admin`) live on the user and travel in the JWT
(`TokenClaims.Role`); `middleware.RequireRole(min)` gates each route
(`internal/adapters/api/middleware/rbac.go`). There is **no per-resource access
control**: any `operator` can read and mutate **every** hive of **every**
cluster.

The "active cluster" (`X-Hivemind-Cluster` header → `currentCluster(c)` /
`writeCluster(c)` in `middleware/cluster.go`) is only a **display filter**, not a
security boundary. It scopes lists and picks the write target; it never denies.

The entity hierarchy is `Cluster → Hive (ClusterID) → Service (ClusterID +
optional HiveID)`. Networks, volumes, secrets and configs are cluster-scoped.

We want hives (projects) and clusters **restricted to authorized people** via
fine-grained grants, while **admins keep full access to everything**.

## Decision

Add an **access-grant layer** on top of the existing roles. The global role is
retained for non-scoped endpoints (user management, global templates) and for
the `admin` super-bypass; access to **scoped resources** (clusters, hives, and
everything beneath them) is driven by **grants**.

### Permission model — verbs, not roles

Grants carry one of three ordered **verbs**: `read < write < manage`.

| Verb | Grants |
|------|--------|
| `read` | GET (list/get) of in-scope hives, services, deployments, metrics |
| `write` | CRUD of services/deployments/snapshots, edit the hive |
| `manage` | everything in `write` + **grant/revoke ACLs** on the resource (owner delegation) + delete the hive |

We chose explicit verbs (decoupled from the global roles) rather than reusing
`viewer/operator/admin` per resource, so the scoped vocabulary is independent of
the platform-wide role ladder.

### Granularity & inheritance (cluster → hive cascade)

Grants attach to a **cluster** or a **hive**. A cluster grant **cascades** to all
its hives (and their services); a hive grant refines (and may elevate) a single
hive. Effective verb for user `u` on hive `H` of cluster `C`:

```
effective(u, H) = max( grant(u, cluster=C), grant(u, hive=H) )
admin (global)  → manage everywhere (bypass)
no grant        → ⊘ (deny-by-default: the resource is invisible)
```

A **service** inherits `max(cluster grant, hive grant if assigned)`; a service
with no hive (`hive_id NULL`) depends on the cluster grant only.

### Deny-by-default

Once enforcement is on, a non-admin with **no grant** sees nothing. To avoid
locking everyone out on rollout, enforcement is gated by an env flag
`HIVEMIND_ACL_ENFORCED`:

- `false` (default) — **shadow mode**: middleware logs what it *would* deny (via
  the existing `AuditForbidden`) but lets the request through, and lists are not
  filtered. Ships the schema + management UI with **zero behavioural change**.
- `true` — real enforcement.

A one-shot **seed** (`SeedDefaultGrants`) maps each existing non-admin user's
global role to a grant on the default cluster (`operator→write`, `viewer→read`)
so the switch to `true` preserves current access. Idempotent.

### Scopes in the JWT + immediate revocation

Effective scopes are computed at `Login`/`Refresh` and embedded in the access
token, so per-request authorization needs no grant lookup. To keep revocation
**immediate** (the access TTL is ≤ 15 min but grant changes must take effect at
once), each user carries a `token_version`:

- a grant change bumps `users.token_version`;
- the `Auth` middleware compares `claims.token_ver` to the stored value — **one
  indexed `users` read per request** — and rejects stale tokens.

This is the accepted cost/security trade-off: a negligible indexed read buys
immediate revocation and makes JWT-embedded scopes safe. Scopes are compacted
(a cluster `manage` grant omits its subsumed hive grants) to bound token size;
if a user accrues too many hive grants, fall back to a cluster-wide scope.

### Enforcement points (two, not one)

1. **Targeted resource** — `middleware.RequireVerb(resourceType, param, min,
   resolver)` resolves the URL id to its `(cluster, hive)`, computes the
   effective verb from `claims.Scopes`, and 403s otherwise. `admin` short-circuits.
2. **List filtering (critical)** — a clean UI still leaks data if lists aren't
   filtered. A `scopeACL(q, claims, resourceType)` helper (beside the existing
   `scopeCluster`) adds `WHERE cluster_id IN (...) OR hive_id IN (...)`. Repo
   `List` signatures gain an allowed-ids scope (`nil` = admin, no filter).
   Covers hives, services, and cascaded networks/volumes/secrets/configs.

### Data model

A single additive table `acl_grants`:

```
id            uuid pk
subject_id    uuid           -- the user (extensible to groups later)
resource_type text           -- "cluster" | "hive"
resource_id   uuid
verb          text           -- "read" | "write" | "manage"
created_by    uuid           -- audit: who granted
created_at    timestamptz
expires_at    timestamptz?   -- optional time-bound grant
```

- UNIQUE `(subject_id, resource_type, resource_id)` — one grant per (user, resource).
- INDEX `(resource_type, resource_id)` — "who has access to this resource".
- INDEX `expires_at` — prune/filter expired grants.

`users` gains `token_version int not null default 0`. Both via `AutoMigrate`
(consistent with the current migration approach in `internal/adapters/persistence/migrate.go`).

### Management API

```
GET/POST  /clusters/:id/grants    (manage on the cluster)
GET/POST  /hives/:id/grants       (manage on the hive)
DELETE    /grants/:id             (manage on the grant's resource; admin everywhere)
```

Each write bumps the affected user's `token_version` for immediate effect.
`/auth/me` is enriched with `is_admin` + `scopes` to drive the UI.

### GUI

- `auth.service` exposes `scopes()`, `canWriteHive(id)`, `canManageHive(id)`;
  scoped screens stop using the global `isOperator()`.
- `cluster-context.service` lists only in-scope clusters (admin = all).
- Hive/service lists are API-filtered; create/edit/delete buttons gate on the
  effective verb.
- A new **"Habilitations"** panel (PrimeNG `p-table`) on the hive/cluster detail
  lists `user · verb · expiry` with grant/revoke, shown when `canManage`.

### Delivery (incremental, shippable in shadow mode)

1. Domain `acl` + `acl_grants` table + `token_version` + repo + tests.
2. JWT scopes + `token_version` revocation + `AclService.ScopesFor`.
3. Enforcement: `RequireVerb` middleware + list filtering across scoped repos.
4. Grant API + enriched `/me` + `HIVEMIND_ACL_ENFORCED` flag + seed.
5. GUI: filtering, per-resource gating, "Habilitations" screen.

Lots 1–4 ship in shadow mode (zero prod impact). Then deploy, observe shadow
denials in the audit log, seed grants, flip the flag, and lot 5 turns on the UX.

## Consequences

**Positive**

- Reuses the existing seams: `X-Hivemind-Cluster` scoping, the audit log
  (already journals 403s), the middleware chain, and the cluster→hive hierarchy.
- Deny-by-default with an explicit shadow-mode flag makes rollout safe and
  observable rather than a hard cut-over.
- Admin bypass keeps break-glass access trivial.
- Owner delegation (`manage`) lets a cluster/hive owner administer access
  without involving a platform admin.

**Negative / limitations**

- One indexed `users` read per request is added to `Auth` (today it parses the
  JWT with no DB hit). Accepted for immediate revocation.
- `TokenService.GenerateAccessToken` gains a `scopes` argument — touches every
  caller of `issuePair` (Login/Refresh) and the port's test mocks.
- JWT size grows with hive grants; mitigated by cluster-level compaction, with a
  cluster-wide-scope fallback for pathological cases.
- List-filtering must be applied to **every** scoped repo or data leaks despite a
  clean UI — covered by integration tests per repo.

**Operational**

- Grant changes are immediate but cost a `token_version` bump + are audited.
- The seed must run before flipping `HIVEMIND_ACL_ENFORCED` to `true`.

## When to revisit

- Group/team subjects (extend `subject_id` with a `subject_type`).
- Per-verb custom permissions beyond read/write/manage.
- A non-Swarm backend — the grant model is backend-neutral and stays.

## References

- `internal/adapters/api/middleware/rbac.go` — current global RBAC
- `internal/adapters/api/middleware/cluster.go` — active-cluster scoping (display only)
- `internal/ports/driven.go` — `TokenClaims`, `TokenService`
- `internal/adapters/auth/jwt.go` — EdDSA JWT issue/parse
- `internal/domain/user/user.go` — roles
- `internal/domain/hive/hive.go`, `internal/domain/cluster/cluster.go` — scoped entities
- `internal/adapters/persistence/models.go` — GORM models, `scopeCluster`
