# Local Development Requirements

This document lists all tools required for local Azimuthal development.

---

## Go Tools

### goose
**Purpose:** Database migration tool
**Required for:** `make migrate`, `make rollback`
**Install:** `go install github.com/pressly/goose/v3/cmd/goose@latest`
**Verify:** `goose --version`

### sqlc
**Purpose:** Generates type-safe Go code from SQL queries
**Required for:** `make sqlc`
**Install:** `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`
**Verify:** `sqlc version`

### air
**Purpose:** Live reload for Go development
**Required for:** `make dev`
**Install:** `go install github.com/air-verse/air@latest`
**Verify:** `air -v`

### golangci-lint
**Purpose:** Go linter aggregator
**Required for:** `make lint`, `make pre-push`
**Install:** See https://golangci-lint.run/welcome/install/
**Verify:** `golangci-lint --version`

### gosec
**Purpose:** Go security checker (SAST)
**Required for:** `make scan-sast`
**Install:** `go install github.com/securego/gosec/v2/cmd/gosec@latest`
**Verify:** `gosec --version`

### govulncheck
**Purpose:** Go vulnerability scanner
**Required for:** `make scan-vuln`
**Install:** `go install golang.org/x/vuln/cmd/govulncheck@latest`
**Verify:** `govulncheck --version`

### go-licenses
**Purpose:** License compliance checker
**Required for:** Verifying new dependency licenses
**Install:** `go install github.com/google/go-licenses@latest`
**Verify:** `go-licenses version`

### swag
**Purpose:** Generates OpenAPI 3.0 spec from Go handler annotations
**Required for:** `make docs`, `make docs-check`, `make pre-push`
**Minimum version:** v2.0.0 (must support `--v3.1` flag)
**Install:**
- All platforms: `go install github.com/swaggo/swag/v2/cmd/swag@latest`
**Verify:** `swag --version`
**Missing impact:** Cannot regenerate API docs. `make docs` will fail.
  CI `docs-check` gate handles this automatically — local install only
  needed if you are modifying API handlers.

---

## External Tools

### Docker
**Purpose:** Container runtime for local services and scanning
**Required for:** `make test-db-up`, `make docker-build`, `make scan-container`
**Install:** https://docs.docker.com/get-docker/
**Verify:** `docker --version`

### gitleaks
**Purpose:** Secret scanning
**Required for:** `make scan-secrets`
**Install:** https://github.com/gitleaks/gitleaks
**Verify:** `gitleaks version`

### trivy
**Purpose:** Container vulnerability scanning
**Required for:** `make scan-container`
**Install:** https://aquasecurity.github.io/trivy
**Verify:** `trivy --version`

---

## Node.js Tools

### Node.js
**Purpose:** Frontend build toolchain
**Required for:** `make frontend`, `make build`
**Minimum version:** 20.x
**Install:** https://nodejs.org/
**Verify:** `node --version`

### Playwright
**Purpose:** E2E browser testing
**Required for:** `make e2e-test`
**Install:** `cd web && npx playwright install chromium --with-deps`
**Verify:** `cd web && npx playwright --version`
