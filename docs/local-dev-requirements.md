# Local Development Requirements

This document lists all tools required for local Azimuthal development.

---

## Go Tools

### Go
**Purpose:** the toolchain everything else in this section installs through
**Minimum version:** **1.26.0** (`go.mod`'s `go` directive), with a `toolchain go1.26.6` directive
that selects the version builds actually use. CI builds with 1.26.6, and the release image uses
`golang:1.26.6-alpine`.
**Install:** https://go.dev/dl/
**Verify:** `go version`
**Missing impact:** nothing here works. Note the failure is *quiet* by default: `go.mod` carries no
`toolchain` directive and nothing sets `GOTOOLCHAIN`, so under the default `GOTOOLCHAIN=auto` an
older Go silently downloads and runs 1.26.0 rather than erroring. You only see a hard failure
under `GOTOOLCHAIN=local` or offline.

*Added 2026-07-31. Go — the single most required tool — had no entry and no version anywhere in a
document whose first line claims to list all of them, while Node, the less constrained of the two,
had an explicit minimum.*

> **Two Windows facts this document does not otherwise carry**, both recorded in `CLAUDE.md` §3:
> `-race` needs `CGO_ENABLED=1` and a C compiler, so `make test` and `make test-live` fail on the
> toolchain rather than on your change without GCC; and `make verify-api` needs `.env.test`
> exported by hand, because its Makefile target does not load it.

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
**Version:** **v2.0.0-rc5 exactly** — must match `SWAG_VERSION` in `.github/workflows/ci.yml`.
(This said "Minimum version: v2.0.0" until 2026-07-31, which CI's own pin does not satisfy: under
semver a prerelease precedes its release, so `v2.0.0-rc5 < v2.0.0`. `make docs-check` byte-diffs
regenerated output against the committed spec, so a different generator version fails the gate
even when the handlers are unchanged — and the failure names the spec, not the toolchain.)
The `--v3.1` flag requirement is real; `make docs` passes it.
**Install:**
- All platforms: `go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc5`
**Verify:** `go version -m "$(command -v swag)" | grep swaggo/swag`
> **`swag --version` cannot tell you which one you have.** It prints `v2.0.0` for the rc5 build too
> — the version string is a constant in the source, not the module version — so the obvious check
> silently confirms whatever you already believed. Verified 2026-07-31 by installing
> `@v2.0.0-rc5` and reading the resulting binary: `swag --version` said `v2.0.0` while
> `go version -m` reported `mod github.com/swaggo/swag/v2 v2.0.0-rc5`. Use the `go version -m`
> form above; it reads the module version stamped into the binary and cannot be fooled.

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
