# syntax=docker/dockerfile:1
#
# Single image serving both the Hivemind API and the Angular UI on one port.
# The frontend lives in a separate repo (orange/hivemind-gui); CI checks it out
# into ./gui so it is part of this build context. The Go binary embeds the built
# UI (internal/adapters/api/web), so the runtime is one static binary.

# ── Frontend: build the Angular app on the build host's arch ──────────────────
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend
WORKDIR /gui
COPY gui/package.json gui/package-lock.json ./
# Prefer a reproducible `npm ci`; fall back to `npm install` when the lock file
# is out of sync (e.g. missing platform-specific optional deps). Keep the gui
# lock file in sync (`npm install` committed) for fully reproducible builds.
RUN npm ci || npm install
COPY gui/ ./
RUN npm run build   # defaultConfiguration=production → dist/hivemind-gui/browser

# ── Backend: embed the frontend and cross-compile the Go server ───────────────
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed embed placeholder with the real Angular build output.
COPY --from=frontend /gui/dist/hivemind-gui/browser/ ./internal/adapters/api/web/dist/
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/hivemind ./cmd/server

# ── Runtime: tiny static image ────────────────────────────────────────────────
# Runs as root (not :nonroot) so a bind-mounted /var/run/docker.sock for the
# local direct-mode cluster is reachable — the socket is root:docker. Deployments
# with no local Docker socket can harden this by overriding the user.
FROM gcr.io/distroless/static-debian12
COPY --from=backend /out/hivemind /usr/local/bin/hivemind
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/hivemind"]
