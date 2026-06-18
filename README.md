<div align="center">

# Hivemind

**Self-service deployment & supervision control plane for Docker Swarm — multi-cluster, agent-ready, single binary.**

[![CI](https://github.com/open226bf/hivemind/actions/workflows/ci.yml/badge.svg)](https://github.com/open226bf/hivemind/actions/workflows/ci.yml)
[![Image](https://github.com/open226bf/hivemind/actions/workflows/build-image.yml/badge.svg)](https://github.com/open226bf/hivemind/actions/workflows/build-image.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/orange/hivemind)](https://goreportcard.com/report/github.com/orange/hivemind)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.25-00ADD8.svg)](go.mod)

</div>

Hivemind turns one or many Docker Swarm clusters into a self-service platform: model services, secrets, configs, networks and volumes through an API and web UI, deploy them with full history and rollback, and supervise the running fleet live — including clusters that sit **behind NAT** with no inbound exposure.

> ⚠️ **Status:** active development, pre-1.0. APIs and storage may change between minor versions.

---

## Highlights

- **Multi-cluster.** Every resource is scoped to a cluster; an orchestrator registry resolves a cluster id to a live connection. Switch the active cluster from the UI header — lists, creates and deploys follow it.
- **Two transports, one contract.** Reach a cluster either **directly** (mTLS to the Docker daemon over TCP) or through a **reverse-tunnel agent** that dials out — so a NAT'd or firewalled cluster needs no open ports.
- **Deploy with memory.** Services are drafted, then deployed; every deployment is recorded, and point-in-time **snapshots** make one-click rollback possible.
- **Live supervision.** Per-service status, tasks, logs and an **interactive web terminal** (`exec`) into any container.
- **Batteries included.** RBAC (Viewer / Operator / Admin), audit log, secrets/configs encrypted at rest (AES-256-GCM), EdDSA-signed JWTs.
- **Single image.** The Go binary embeds the Angular UI and serves both API and UI on one port.

## Architecture

Hivemind is three repositories that ship as one product:

| Repo | What | Stack |
|------|------|-------|
| [`hivemind`](https://github.com/open226bf/hivemind) | Control plane (API + embedded UI) | Go 1.25, Gin, GORM/Postgres, hexagonal |
| [`hivemind-agent`](https://github.com/open226bf/hivemind-agent) | Reverse-tunnel agent (one task per node) | Go 1.25, yamux |
| [`hivemind-gui`](https://github.com/open226bf/hivemind-gui) | Web console | Angular 21, PrimeNG |

```
                            ┌──────────────────────────────┐
   Browser ────────────────►│  Hivemind (single container) │
                            │  ┌────────────┐  ┌─────────┐  │
                            │  │ Angular UI │  │  REST   │  │
                            │  │ (embedded) │  │  /api   │  │
                            │  └────────────┘  └────┬────┘  │
                            │   orchestrator registry│      │
                            └──────────┬─────────────┴──────┘
                          direct (mTLS)│        │ agent (reverse tunnel)
                                       ▼        ▼
                            ┌─────────────┐   ┌──────────────────────────┐
                            │ Swarm A     │   │ Swarm B (behind NAT)     │
                            │ docker.sock │   │  agent task per node ───► │
                            └─────────────┘   │  dials out, proxies API  │
                                              └──────────────────────────┘
```

The backend follows a **hexagonal** layout: `internal/domain` (pure business rules), `internal/ports` (interfaces), `internal/application` (use cases), `internal/adapters` (HTTP, persistence, orchestrator, auth). See the [documentation](https://github.com/open226bf/hivemind-doc) for the full design.

## Quick start

### Run the released image (API + UI)

```bash
docker run -d --name hivemind -p 8080:8080 \
  -e DATABASE_URL='postgres://user:pass@db:5432/hivemind?sslmode=disable' \
  -e AES_KEY="$(openssl rand -base64 32)" \
  -e ADMIN_EMAIL=admin@example.com -e ADMIN_PASSWORD='change-me-please' \
  -v /var/run/docker.sock:/var/run/docker.sock \
  open226/hivemind:latest
```

Open <http://localhost:8080> and sign in with the bootstrap admin. The mounted
`docker.sock` lets the default (direct-mode) cluster talk to the local Swarm.

### Local development

Prerequisites: Go 1.25, Node 22, a Postgres database, and (optionally) a Docker Swarm.

```bash
# 1. Backend (serves /api on :8080; falls back to a stub orchestrator without Docker)
cp .env.example .env            # then edit DATABASE_URL, AES_KEY, ADMIN_*
make run                        # or: go run ./cmd/server

# 2. Frontend (separate repo, proxies /api to the backend)
cd ../hivemind-gui && npm install && npm start    # http://localhost:4200
```

Without a reachable Docker daemon the server starts with a **stub orchestrator**
so you can explore the API and UI; set `ORCHESTRATOR=stub` to force it.

## Configuration

The server is configured via environment variables (see [`.env.example`](.env.example)):

| Variable | Required | Description |
|----------|:---:|-------------|
| `DATABASE_URL` | ✅ | Postgres DSN. |
| `AES_KEY` | prod | Base64-encoded 32-byte key; encrypts secret/config values at rest. Without it, values are stored **unencrypted**. |
| `JWT_PRIVATE_KEY_PATH` | prod | Ed25519 signing key path. Ephemeral (per-boot) key when unset. |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | — | Bootstraps the first admin account. |
| `PORT` | — | HTTP port (default `8080`). |
| `APP_ENV` | — | `development` (default) or `production`. |
| `AUTO_MIGRATE` | — | Run DB migrations on boot (default: on outside production). |
| `ORCHESTRATOR` | — | `stub` to force the simulated backend. |
| `AGENT_HUB_ADDR` / `AGENT_HUB_PUBLIC_ADDR` | — | Enable the mutual-TLS agent hub and advertise its address. |
| `AGENT_IMAGE` | — | Agent image baked into generated install scripts. |
| `HIVEMIND_BASE_URL` | — | Canonical external URL used in rendered commands. |

## Testing

```bash
make test          # go test ./...
go vet ./...
staticcheck ./...  # honnef.co/go/tools/cmd/staticcheck
```

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) and our
[Code of Conduct](CODE_OF_CONDUCT.md). Security issues: see [SECURITY.md](SECURITY.md).

## License

[Apache 2.0](LICENSE) © The Hivemind Authors.
