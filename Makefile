# Makefile — Azimuthal development commands

.PHONY: help build docker-build test test-coverage lint fmt \
        scan scan-sast scan-vuln scan-secrets scan-container \
        dev migrate rollback sqlc clean pre-push

# ── Config ────────────────────────────────────────────────────
BINARY_NAME    := azimuthal
VERSION        := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME     := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS        := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)
GO_TEST_FLAGS  := -race -timeout 120s -coverprofile=coverage.out -covermode=atomic
DATABASE_URL   ?= postgres://azimuthal:dev@localhost:5432/azimuthal_dev?sslmode=disable

# ── Help ──────────────────────────────────────────────────────
help:
	@echo ""
	@echo "  Azimuthal — development commands"
	@echo "  Know exactly where your team is headed."
	@echo ""
	@echo "  Build"
	@echo "    make build              Build binary"
	@echo "    make docker-build       Build Docker image"
	@echo ""
	@echo "  Test & Quality"
	@echo "    make test               Run all tests with race detector"
	@echo "    make test-coverage      Run tests and open coverage report"
	@echo "    make lint               Run golangci-lint"
	@echo "    make fmt                Format all Go code"
	@echo ""
	@echo "  Security Scanning"
	@echo "    make scan               Run ALL security scans"
	@echo "    make scan-sast          SAST via gosec"
	@echo "    make scan-vuln          Dependency scan via govulncheck"
	@echo "    make scan-secrets       Secret scan via gitleaks"
	@echo "    make scan-container     Container scan via trivy"
	@echo ""
	@echo "  Database"
	@echo "    make migrate            Run pending migrations"
	@echo "    make rollback           Roll back last migration"
	@echo "    make sqlc               Regenerate sqlc queries"
	@echo ""
	@echo "  Development"
	@echo "    make dev                Start dev server with live reload"
	@echo "    make pre-push           Run all checks before pushing"
	@echo "    make clean              Remove build artifacts"
	@echo ""

# ── Build ─────────────────────────────────────────────────────
build:
	@echo "→ Building $(BINARY_NAME)..."
	@go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/server
	@echo "✓ Built bin/$(BINARY_NAME)"

docker-build:
	@echo "→ Building Docker image..."
	@docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(BINARY_NAME):$(VERSION) \
		-t $(BINARY_NAME):latest \
		-f build/Dockerfile \
		.
	@echo "✓ Built $(BINARY_NAME):$(VERSION)"

# ── Test ──────────────────────────────────────────────────────
test:
	@echo "→ Running tests (race detector enabled)..."
	@go test $(GO_TEST_FLAGS) ./...
	@echo "✓ Tests passed"
	@go tool cover -func=coverage.out | tail -1

test-coverage: test
	@go tool cover -html=coverage.out -o coverage.html
	@echo "✓ Coverage report: coverage.html"
	@open coverage.html 2>/dev/null || xdg-open coverage.html 2>/dev/null || true

# ── Lint & Format ─────────────────────────────────────────────
lint:
	@echo "→ Running golangci-lint..."
	@golangci-lint run --config=.golangci.yml ./...
	@echo "✓ Lint passed"

fmt:
	@echo "→ Formatting Go code..."
	@gofmt -w -s .
	@goimports -w .
	@echo "✓ Formatted"

# ── Security Scans ────────────────────────────────────────────
scan: scan-sast scan-vuln scan-secrets scan-container
	@echo ""
	@echo "✓ All security scans passed"

scan-sast:
	@echo "→ Running SAST (gosec)..."
	@which gosec > /dev/null || go install github.com/securego/gosec/v2/cmd/gosec@latest
	@gosec -severity high -confidence high -exclude-dir=vendor ./...
	@echo "✓ SAST passed"

scan-vuln:
	@echo "→ Scanning dependencies (govulncheck)..."
	@which govulncheck > /dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest
	@govulncheck ./...
	@echo "✓ Dependency scan passed"

scan-secrets:
	@echo "→ Scanning for secrets (gitleaks)..."
	@which gitleaks > /dev/null || \
		(echo "Install gitleaks: https://github.com/gitleaks/gitleaks" && exit 1)
	@gitleaks detect --config=.gitleaks.toml --verbose
	@echo "✓ Secret scan passed"

scan-container: docker-build
	@echo "→ Scanning container image (trivy)..."
	@which trivy > /dev/null || \
		(echo "Install trivy: https://aquasecurity.github.io/trivy" && exit 1)
	@trivy image --config trivy.yaml $(BINARY_NAME):latest
	@echo "✓ Container scan passed"

# ── Database ──────────────────────────────────────────────────
migrate:
	@echo "→ Running migrations..."
	@which goose > /dev/null || go install github.com/pressly/goose/v3/cmd/goose@latest
	@goose -dir migrations postgres "$(DATABASE_URL)" up
	@echo "✓ Migrations complete"

rollback:
	@echo "→ Rolling back last migration..."
	@goose -dir migrations postgres "$(DATABASE_URL)" down
	@echo "✓ Rolled back"

sqlc:
	@echo "→ Regenerating sqlc queries..."
	@which sqlc > /dev/null || go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	@sqlc generate
	@echo "✓ sqlc generated"

# ── Development ───────────────────────────────────────────────
dev:
	@echo "→ Starting Azimuthal dev server..."
	@which air > /dev/null || go install github.com/air-verse/air@latest
	@air

# ── Pre-push (run before git push) ───────────────────────────
pre-push: fmt lint test scan
	@echo ""
	@echo "✅ All local checks passed — safe to push to Azimuthal"

# ── Housekeeping ──────────────────────────────────────────────
clean:
	@rm -rf bin/ coverage.out coverage.html
	@docker rmi $(BINARY_NAME):latest $(BINARY_NAME):$(VERSION) 2>/dev/null || true
	@echo "✓ Cleaned"
