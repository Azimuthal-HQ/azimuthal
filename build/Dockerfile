# syntax=docker/dockerfile:1

# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

ARG VERSION=dev
ARG CGO_ENABLED=0

WORKDIR /src

# Download dependencies first (cached layer)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /azimuthal \
    ./cmd/server


# ── Stage 2: Final distroless image ───────────────────────────────────────────
# distroless/static:nonroot has no shell — use exec form for all instructions.
FROM gcr.io/distroless/static:nonroot

# nonroot user (uid=65532) is the default for distroless/static:nonroot.
USER nonroot:nonroot

COPY --from=builder --chown=nonroot:nonroot /azimuthal /azimuthal

EXPOSE 8080

# Liveness probe is handled by the orchestrator (Kubernetes/Compose) via HTTP GET /health.
# HEALTHCHECK is omitted: distroless has no shell to exec a health command.

ENTRYPOINT ["/azimuthal"]
