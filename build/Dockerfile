# syntax=docker/dockerfile:1

# ── Stage 1: Build ────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

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

# Verify the binary is static
RUN file /azimuthal | grep -q "statically linked" || \
    (echo "ERROR: binary is not statically linked" && exit 1)


# ── Stage 2: Final distroless image ───────────────────────────────────────────
FROM gcr.io/distroless/static:nonroot

# nonroot user is uid=65532, already set by distroless/static:nonroot
USER nonroot:nonroot

COPY --from=builder --chown=nonroot:nonroot /azimuthal /azimuthal

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/azimuthal", "health"] || exit 1

ENTRYPOINT ["/azimuthal"]
