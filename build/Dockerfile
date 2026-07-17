# syntax=docker/dockerfile:1

# Multi-arch build with ZERO emulation.
#
# Both heavy stages run natively on the build platform ($BUILDPLATFORM) and
# produce arch-independent or cross-compiled output:
#   - the frontend bundle is static assets (arch-independent), and
#   - the Go binary is CGO-free, so it cross-compiles to $TARGETARCH natively.
# Only the final distroless layer is per-target, and that is just a static
# binary copy (nothing is executed under emulation). This keeps `docker buildx
# --platform linux/amd64,linux/arm64` at a few minutes instead of the 30-60
# min a QEMU-emulated arm64 npm+go build takes.

# -- Stage 1: Build frontend (native, arch-independent output) ----------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS frontend
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# -- Stage 2: Build Go binary (native toolchain, cross-compiled) --------------
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS builder
ARG VERSION=dev
# TARGETOS/TARGETARCH are provided automatically by buildx per target platform.
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# Download dependencies first (cached layer)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source
COPY . .

# Copy built frontend assets from stage 1
COPY --from=frontend /web/dist ./web/dist

# Cross-compile a static binary with embedded frontend. The Go compiler runs
# on the native build platform and targets $TARGETOS/$TARGETARCH, so no QEMU
# is involved even when building the arm64 image on an amd64 runner.
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /azimuthal \
    ./cmd/server

# -- Stage 3: Final distroless image (per-target, no execution) ---------------
# buildx selects the $TARGETARCH variant of the multi-arch distroless base.
FROM gcr.io/distroless/static:nonroot
USER nonroot:nonroot
COPY --from=builder --chown=nonroot:nonroot /azimuthal /azimuthal
EXPOSE 8080
ENTRYPOINT ["/azimuthal", "serve"]
