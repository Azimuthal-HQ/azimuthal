# Makefile — Azimuthal development commands

.PHONY: help build docker-build test test-coverage lint fmt \
        scan scan-sast scan-vuln scan-secrets scan-container \
        dev migrate rollback sqlc clean pre-push verify-api \
        frontend frontend-install frontend-type-check \
        test-db-up test-db-down test-db-reset test-live test-live-verbose \
        e2e-test e2e-report e2e-headed docs docs-check

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
	@echo "    make build              Build frontend + Go binary"
	@echo "    make frontend           Build frontend only"
	@echo "    make docker-build       Build Docker image"
	@echo ""
	@echo "  Test & Quality"
	@echo "    make test               Run all tests with race detector"
	@echo "    make test-coverage      Run tests and open coverage report"
	@echo "    make test-live          Run all tests with real database"
	@echo "    make test-live-verbose  Run all tests with real database (verbose)"
	@echo "    make lint               Run golangci-lint"
	@echo "    make fmt                Format all Go code"
	@echo ""
	@echo "  Test Database"
	@echo "    make test-db-up         Start test postgres (:5433) + minio (:9001)"
	@echo "    make test-db-down       Stop and remove test services"
	@echo "    make test-db-reset      Wipe and recreate test database"
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

# ── Frontend ──────────────────────────────────────────────────
frontend-install:
	@echo "→ Installing frontend dependencies..."
	@cd web && npm ci
	@echo "✓ Frontend dependencies installed"

frontend-type-check:
	@echo "→ Type-checking frontend..."
	@cd web && npm run type-check
	@echo "✓ Frontend type-check passed"

frontend: frontend-install
	@echo "→ Building frontend..."
	@cd web && npm run build
	@echo "✓ Frontend built to web/dist/"

# ── Build ─────────────────────────────────────────────────────
build: frontend
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

# ── API Verification ─────────────────────────────────────────
verify-api: build
	@echo "→ Running full API verification suite..."
	@chmod +x scripts/verify-api.sh
	@./scripts/verify-api.sh
	@echo "✓ API verification complete"

# ── Documentation ────────────────────────────────────────────────────────────

docs: ## Generate OpenAPI 3.0 spec from handler annotations
	@echo "→ Generating OpenAPI 3.0 spec..."
	@which swag > /dev/null 2>&1 || (echo "swag not installed. Run: go install github.com/swaggo/swag/v2/cmd/swag@latest" && exit 1)
	@swag init \
		--generalInfo main.go \
		--dir ./cmd/server,./internal/core/api,./internal/core/api/auth,./internal/core/api/tickets,./internal/core/api/wiki,./internal/core/api/projects,./internal/core/api/spaces,./internal/core/api/comments,./internal/core/api/notifications \
		--output docs/api \
		--outputTypes yaml \
		--v3.1 \
		--parseInternal \
		--parseDependencyLevel 1
	@mv docs/api/swagger.yaml docs/api/openapi.yaml 2>/dev/null || true
	@rm -f docs/api/docs.go docs/api/swagger.json
	@echo "✓ Spec generated: docs/api/openapi.yaml"
	@echo "  View at: http://localhost:8080/api/docs"

docs-check: ## Verify OpenAPI spec is in sync with code (fails if out of date)
	@cp docs/api/openapi.yaml /tmp/check-spec.yaml
	@$(MAKE) docs --quiet
	@diff -q docs/api/openapi.yaml /tmp/check-spec.yaml > /dev/null 2>&1 \
		&& echo "✅ API spec is up to date" \
		|| (echo "❌ API spec out of sync — run 'make docs' and commit the result" && cp /tmp/check-spec.yaml docs/api/openapi.yaml && exit 1)
	@cp /tmp/check-spec.yaml docs/api/openapi.yaml

# ── Pre-push (run before git push) ───────────────────────────
pre-push: fmt lint test scan docs-check
	@echo ""
	@echo "✅ All local checks passed — safe to push to Azimuthal"

# ── Test Database ────────────────────────────────────────────────────────────

test-db-up: ## Start test database and storage (postgres on :5433, minio on :9001)
	@echo "→ Starting test services..."
	@docker compose -f build/docker-compose.test.yml up -d
	@echo "→ Waiting for postgres to be ready..."
	@until docker compose -f build/docker-compose.test.yml exec -T postgres-test pg_isready -U azimuthal_test; do \
		sleep 1; \
	done
	@echo "→ Running migrations..."
	@export $$(cat .env.test | grep -v '^#' | grep -v '^$$' | xargs) && goose -dir migrations postgres "$$DATABASE_URL" up
	@echo "✓ Test database ready at localhost:5433"

test-db-down: ## Stop and remove test database
	@docker compose -f build/docker-compose.test.yml down
	@echo "✓ Test services stopped"

test-db-reset: ## Wipe and recreate test database from scratch
	@docker compose -f build/docker-compose.test.yml down -v
	@$(MAKE) test-db-up
	@echo "✓ Test database reset complete"

test-live: test-db-up ## Run all tests including integration tests requiring a real database
	@echo "→ Running all tests with real database..."
	@export $$(cat .env.test | grep -v '^#' | grep -v '^$$' | xargs) && go test -race ./... -count=1 -timeout=120s
	@echo "✓ All tests complete"

test-live-verbose: test-db-up ## Run all tests with verbose output
	@export $$(cat .env.test | grep -v '^#' | grep -v '^$$' | xargs) && go test -race -v ./... -count=1 -timeout=120s

test-live-coverage: test-db-up ## Run tests with real database and generate coverage report
	@export $$(cat .env.test | grep -v '^#' | grep -v '^$$' | xargs) && go test ./internal/... -coverprofile=coverage.out -covermode=atomic -coverpkg=./internal/... -timeout=300s
	@go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | tail -5
	@echo "✓ Coverage report: coverage.html"

# ── E2E Tests ──────────────────────────────────────────────────────────────────

e2e-test: ## Run Playwright E2E tests against a live server
	@echo "→ Starting test database..."
	@$(MAKE) test-db-up
	@echo "→ Building binary and frontend..."
	@cd web && npm ci && npm run build
	@go build -o /tmp/azimuthal-test ./cmd/server
	@echo "→ Running Playwright E2E tests..."
	@export $$(cat .env.test | xargs) && cd web && npx playwright test
	@echo "✓ E2E tests complete"
	@$(MAKE) test-db-down

e2e-report: ## Open the last Playwright HTML report
	@cd web && npx playwright show-report

e2e-headed: ## Run E2E tests in headed mode (visible browser)
	@export $$(cat .env.test | xargs) && cd web && npx playwright test --headed

# ── Housekeeping ──────────────────────────────────────────────
clean:
	@rm -rf bin/ coverage.out coverage.html web/dist/ web/node_modules/
	@docker rmi $(BINARY_NAME):latest $(BINARY_NAME):$(VERSION) 2>/dev/null || true
	@echo "✓ Cleaned"
