# syntax=docker/dockerfile:1

# -- Stage 1: Build frontend --------------------------------------------------
FROM node:20-alpine AS frontend
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# -- Stage 2: Build Go binary -------------------------------------------------
FROM golang:1.26-alpine AS builder
ARG VERSION=dev
WORKDIR /src

# Download dependencies first (cached layer)
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy source
COPY . .

# Copy built frontend assets from stage 1
COPY --from=frontend /web/dist ./web/dist

# Build static binary with embedded frontend
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /azimuthal \
    ./cmd/server

# -- Stage 3: Final distroless image ------------------------------------------
FROM gcr.io/distroless/static:nonroot
USER nonroot:nonroot
COPY --from=builder --chown=nonroot:nonroot /azimuthal /azimuthal
EXPOSE 8080
ENTRYPOINT ["/azimuthal", "serve"]
