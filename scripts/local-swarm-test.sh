#!/usr/bin/env bash
# End-to-end test of the agent (mTLS) flow on a local single-node Swarm.
#
# Prereqs: Docker Desktop with Swarm active (`docker swarm init`), `jq`, `go`.
# It starts Postgres + the Hivemind server (with the mTLS agent hub), enrolls a
# cluster in agent mode, deploys the agent stack from the enrollment output, and
# waits for the agent to come online.
#
# Single-node note: the agent container reaches the host via host.docker.internal,
# so the hub server cert is issued for that name. Multi-node routing is not
# exercised here (one manager node).
set -euo pipefail

AGENT_REPO="${AGENT_REPO:-$(cd "$(dirname "$0")/../../hivemind-agent" && pwd)}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="http://localhost:8080/api/v1"
HUB_HOST="host.docker.internal"

PG_NAME=hivemind-pg
PG_PORT="${PG_PORT:-5433}" # throwaway DB port (avoids clashing with a local 5432)
ADMIN_EMAIL=admin@hivemind.local
ADMIN_PASSWORD=changeme
export AES_KEY="$(head -c 32 /dev/urandom | base64)"

cleanup() {
  echo "── cleanup"
  [[ -n "${SERVER_PID:-}" ]] && kill "$SERVER_PID" 2>/dev/null || true
  docker stack rm hivemind-agent 2>/dev/null || true
  docker secret rm hivemind_client_cert hivemind_client_key hivemind_ca_cert 2>/dev/null || true
  docker rm -f "$PG_NAME" 2>/dev/null || true
}
trap cleanup EXIT

echo "── postgres"
docker rm -f "$PG_NAME" 2>/dev/null || true
docker run -d --name "$PG_NAME" -e POSTGRES_USER=hivemind -e POSTGRES_PASSWORD=hivemind \
  -e POSTGRES_DB=hivemind -p ${PG_PORT}:5432 postgres:16-alpine >/dev/null
until docker exec "$PG_NAME" pg_isready -U hivemind >/dev/null 2>&1; do sleep 1; done

echo "── hivemind server (mTLS hub on :8443)"
( cd "$ROOT" && go build -o /tmp/hivemind-server ./cmd/server )
DATABASE_URL="postgres://hivemind:hivemind@localhost:${PG_PORT}/hivemind?sslmode=disable" \
  AES_KEY="$AES_KEY" ADMIN_EMAIL="$ADMIN_EMAIL" ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  AUTO_MIGRATE=true APP_ENV=development \
  AGENT_HUB_ADDR=":8443" AGENT_HUB_PUBLIC_ADDR="${HUB_HOST}:8443" AGENT_HUB_HOSTNAME="$HUB_HOST" \
  /tmp/hivemind-server &
SERVER_PID=$!
until curl -fsS "http://localhost:8080/readyz" >/dev/null 2>&1; do sleep 1; done

echo "── build agent image"
( cd "$AGENT_REPO" && docker build -q -t hivemind/agent:latest . >/dev/null )

echo "── login + create cluster (agent mode)"
TOKEN=$(curl -fsS -X POST "$BASE/auth/login" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" | jq -r .access_token)
AUTH=(-H "Authorization: Bearer $TOKEN")
CID=$(curl -fsS "${AUTH[@]}" -X POST "$BASE/clusters" -H 'Content-Type: application/json' \
  -d '{"name":"swarm-local","type":"swarm"}' | jq -r .id)

echo "── enroll → client certificate"
ENROLL=$(curl -fsS "${AUTH[@]}" -X POST "$BASE/clusters/$CID/enroll")
jq -r .client_cert <<<"$ENROLL" | docker secret create hivemind_client_cert - >/dev/null
jq -r .client_key  <<<"$ENROLL" | docker secret create hivemind_client_key  - >/dev/null
jq -r .ca_cert     <<<"$ENROLL" | docker secret create hivemind_ca_cert     - >/dev/null

echo "── deploy agent stack"
HIVEMIND_HUB_ADDR="${HUB_HOST}:8443" \
  docker stack deploy -c "$AGENT_REPO/deploy/hivemind-agent-mtls.yml" hivemind-agent >/dev/null

echo "── wait for agent online"
for _ in $(seq 1 30); do
  STATUS=$(curl -fsS "${AUTH[@]}" "$BASE/clusters/$CID" | jq -r .agent_status)
  echo "   agent_status=$STATUS"
  [[ "$STATUS" == "online" ]] && { echo "✅ agent online — tunnel established"; exit 0; }
  sleep 2
done
echo "❌ agent did not come online; check: docker service logs hivemind-agent_agent"
exit 1
