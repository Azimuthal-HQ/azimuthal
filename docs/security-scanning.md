# Security Scanning — Azimuthal

This document describes every security scanner used in the Azimuthal CI pipeline,
how to run them locally, what findings cause a build to fail, and how to suppress
a known false positive (with the required documentation standards).

---

## Table of Contents

1. [Overview](#overview)
2. [gosec — Static Analysis (SAST)](#gosec--static-analysis-sast)
3. [govulncheck — Dependency Vulnerability Scan](#govulncheck--dependency-vulnerability-scan)
4. [gitleaks — Secret Detection](#gitleaks--secret-detection)
5. [trivy — Container Image Scan](#trivy--container-image-scan)
6. [Suppressing False Positives](#suppressing-false-positives)
7. [Running All Scans Locally](#running-all-scans-locally)
8. [Severity Reference](#severity-reference)

---

## Overview

All four scanners run on every pull request. The `all-checks` gate at the end
of the pipeline requires every scanner to pass before a PR can merge.
No exceptions, no bypasses — not even for admins.

| Scanner      | What it scans            | Fails on               | CI job          |
|--------------|--------------------------|------------------------|-----------------|
| gosec        | Go source code (SAST)    | HIGH+ severity         | `sast`          |
| govulncheck  | Go module dependencies   | Any known CVE          | `vuln-scan`     |
| gitleaks     | Git history + files      | Any detected secret    | `secret-scan`   |
| trivy        | Container image layers   | HIGH/CRITICAL CVEs     | `container-scan`|

---

## gosec — Static Analysis (SAST)

**What it scans:** Go source code for security anti-patterns and vulnerabilities.
gosec performs static analysis using Go's AST and SSA representations. It catches
issues such as:

- SQL injection and command injection
- Hardcoded credentials in source
- Weak cryptography (MD5, SHA1, DES)
- Insufficient TLS configuration
- File path traversal
- Log injection (G706: tainted data in log calls)
- Use of `math/rand` instead of `crypto/rand`
- Insecure HTTP server configurations
- Unsafe Go operations (unsafe pointer, `reflect`)

**Configuration:** gosec is configured via CLI flags in CI (no separate config file).
Current flags: `-severity high -confidence high -exclude-dir=vendor`

**What fails the build:**
Any finding with severity **HIGH** or **CRITICAL** at confidence **HIGH** fails the CI.
MEDIUM and LOW severity findings are informational only and do not block merges.

**Local installation:**
```bash
go install github.com/securego/gosec/v2/cmd/gosec@latest
```

**Local usage:**
```bash
# Run all checks (mirrors CI exactly)
make scan-sast

# Or directly:
gosec -severity high -confidence high -exclude-dir=vendor ./...

# With HTML report:
gosec -fmt html -out gosec-report.html ./...

# With SARIF report (for IDE import):
gosec -fmt sarif -out gosec-results.sarif ./...
```

**SARIF results:** In CI, gosec results are uploaded to the GitHub Security tab
(requires GitHub Advanced Security). On free plans this step is skipped gracefully.

**Suppressing a false positive:** See [Suppressing False Positives](#suppressing-false-positives).

---

## govulncheck — Dependency Vulnerability Scan

**What it scans:** Go module dependencies against the
[Go vulnerability database](https://vuln.go.dev). Unlike `go mod audit`, govulncheck
performs call-graph analysis — it only reports vulnerabilities in code paths that
are actually reachable from your program, eliminating most false positives.

govulncheck catches:

- Known CVEs in direct and transitive Go dependencies
- Standard library vulnerabilities (e.g. `net/http`, `crypto/tls`)
- Vulnerabilities in the Go toolchain itself

**What fails the build:**
Any vulnerability that affects a reachable code path fails the build,
regardless of severity. govulncheck has no severity tiers — all findings block merges.

**Why:** A vulnerability in a reachable code path is a real risk. If govulncheck
reports it, the correct fix is to update the dependency — not to suppress.

**Local installation:**
```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
```

**Local usage:**
```bash
# Run dependency scan (mirrors CI)
make scan-vuln

# Or directly:
govulncheck ./...

# Verbose output with full call graph detail:
govulncheck -v ./...

# JSON output for tooling:
govulncheck -json ./... > vuln-report.json
```

**Resolving a finding:** Update the affected module:
```bash
go get github.com/some/module@latest
go mod tidy
```

**Suppressing a finding:** govulncheck does not support suppression.
If a dependency has a CVE in an unreachable code path and govulncheck still
reports it, open an issue in the govulncheck repository — this is a bug in
govulncheck's call-graph analysis.

If the vulnerability is in a reachable path and cannot be patched yet, the only
option is to document it as accepted risk in a GitHub Security Advisory and
note it in a PR comment with a concrete remediation timeline.

---

## gitleaks — Secret Detection

**What it scans:** The entire git history and all tracked files for secrets,
API keys, tokens, and credentials that should never be committed.

gitleaks checks:

- Hardcoded passwords, tokens, and API keys
- Private keys (RSA, EC, PGP)
- Connection strings with embedded credentials
- Cloud provider credentials (AWS, GCP, Azure)
- JWT secrets and HMAC keys
- Azimuthal-specific patterns (JWT_SECRET, LICENSE_KEY, DATABASE_URL with credentials)

**Configuration:** `.gitleaks.toml` in the repo root.

```toml
[extend]
  useDefault = true   # extends the built-in ruleset
```

The `useDefault = true` directive extends gitleaks' built-in rules (covering 100+
secret patterns). Custom Azimuthal-specific rules are added below it.

**What fails the build:**
Any detected secret in any file in the git history fails the build.
There is no severity tier — any secret = fail.

**Important:** gitleaks uses the RE2 regex engine (not PCRE). Patterns must be
RE2-compatible. PCRE features such as lookaheads (`(?!...)`, `(?=...)`) and
lookbehinds are **not supported** and will cause a panic.

**Local installation:**
```bash
# macOS
brew install gitleaks

# Linux (manual)
VERSION=$(curl -s https://api.github.com/repos/gitleaks/gitleaks/releases/latest \
  | grep '"tag_name"' | cut -d'"' -f4)
curl -sSfL \
  "https://github.com/gitleaks/gitleaks/releases/download/${VERSION}/gitleaks_${VERSION#v}_linux_x64.tar.gz" \
  | tar xz gitleaks
sudo mv gitleaks /usr/local/bin/gitleaks
```

**Local usage:**
```bash
# Scan all files and git history (mirrors CI)
make scan-secrets

# Or directly:
gitleaks detect --config .gitleaks.toml --verbose

# Scan only staged files (fast pre-commit check):
gitleaks protect --staged --config .gitleaks.toml --verbose

# Scan a specific commit range:
gitleaks detect --config .gitleaks.toml --log-opts="HEAD~10..HEAD"
```

**Suppressing a false positive:** See [Suppressing False Positives](#suppressing-false-positives).

---

## trivy — Container Image Scan

**What it scans:** The built Docker image's OS packages and language-level
dependencies (Go modules embedded in the binary) for known CVEs.

trivy checks:

- OS package CVEs (base image: `gcr.io/distroless/static:nonroot`)
- Go binary CVEs (extracted from the embedded Go module graph)
- Dockerfile misconfigurations (running as root, exposed sensitive ports, etc.)
- Secrets accidentally embedded in container layers

**Configuration:** `trivy.yaml` in the repo root.

**What fails the build:**
Any **HIGH** or **CRITICAL** severity CVE with an available fix fails the build.
Unfixed vulnerabilities (no patch available) are skipped (`ignore-unfixed: true`).

LOW and MEDIUM findings are informational. They appear in the Trivy SARIF report
uploaded to the GitHub Security tab but do not block merges.

**Local installation:**
```bash
# macOS
brew install trivy

# Linux
curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh \
  | sh -s -- -b /usr/local/bin
```

**Local usage:**
```bash
# Build image then scan (mirrors CI)
make scan-container

# Or directly:
docker build -f build/Dockerfile -t azimuthal:dev .
trivy image --config trivy.yaml azimuthal:dev

# Table output (default in trivy.yaml):
trivy image --severity HIGH,CRITICAL --ignore-unfixed azimuthal:dev

# JSON output for tooling:
trivy image --format json --output trivy-report.json azimuthal:dev

# SARIF for IDE/GitHub:
trivy image --format sarif --output trivy-results.sarif azimuthal:dev
```

**Suppressing a finding:** See [Suppressing False Positives](#suppressing-false-positives).

---

## Suppressing False Positives

Every scanner has a different suppression mechanism. All suppressions require
a justification comment — undocumented suppressions will be rejected in code review.

### gosec — Inline `//nolint` directive

Use `//nolint:gosec` **on the specific line** with a mandatory comment explaining why.

```go
// #nosec G401 -- MD5 used here for cache key generation only, not for security.
// The cache key does not need to be cryptographically strong.
hash := md5.Sum(data) //nolint:gosec
```

The `#nosec` comment is the gosec-native suppression. The `//nolint:gosec` tells
golangci-lint's gosec integration to also skip it. Use both to be safe.

**Required format:**
```go
// #nosec GXXX -- <reason>: <why this is not exploitable in our context>
someRiskyCall() //nolint:gosec
```

**Never suppress a whole file or package.** Suppress only the specific line.

### govulncheck — No suppression available

govulncheck does not support suppressions. Resolve the vulnerability by updating
the dependency. If the vulnerable code path is genuinely unreachable and the
govulncheck report is incorrect, file a bug upstream.

### gitleaks — Allowlist in `.gitleaks.toml`

Add entries to the `[allowlist]` section at the bottom of `.gitleaks.toml`.

**Option 1: Allowlist by file path** (preferred for whole files like docker-compose):
```toml
[allowlist]
  paths = [
    # <reason: why this file legitimately contains credential-like strings>
    '''build/docker-compose\.dev\.yml''',
  ]
```

**Option 2: Allowlist by regex pattern** (for specific strings across all files):
```toml
[allowlist]
  regexes = [
    # <reason: why this pattern is never a real secret>
    '''EXAMPLE_KEY_PLACEHOLDER''',
  ]
```

**Option 3: Per-rule allowlist** (suppress for a specific rule only):
```toml
[[rules]]
  id = "my-rule"
  regex = '''...'''
  [rules.allowlist]
    regexes = [
      # <reason>
      '''safe_value''',
    ]
```

**Remember:** gitleaks uses RE2 regex. No PCRE lookaheads.

### trivy — `trivy-ignore.yaml` (Rego policy)

The `trivy-ignore.yaml` file contains an OPA/Rego policy that trivy evaluates
when `ignore-policy: trivy-ignore.yaml` is set in `trivy.yaml`.

To activate the policy file:

1. Uncomment the `ignore-policy` line in `trivy.yaml`:
   ```yaml
   ignore-policy: trivy-ignore.yaml
   ```

2. Add a valid Rego module to `trivy-ignore.yaml`:

```rego
# trivy-ignore.yaml — Trivy vulnerability suppression policy
# Uses OPA Rego: https://www.openpolicyagent.org/docs/latest/policy-language/
#
# Each suppression MUST document:
#   1. The CVE / finding ID
#   2. Why it is a false positive or accepted risk
#   3. The review expiry date (max 90 days from suppression date)
#   4. The GitHub issue tracking remediation

package trivy

import rego.v1

default ignore := false

# Example (replace with a real CVE when needed):
# ignore if {
#   # CVE-2024-XXXXX: Not exploitable — we never call the affected function.
#   # Tracking: https://github.com/Azimuthal-HQ/azimuthal/issues/NNN
#   # Review by: 2025-12-31
#   input.VulnerabilityID == "CVE-2024-XXXXX"
#   input.PkgName == "affected-package"
# }
```

**Required documentation for every suppression:**
- Link to the CVE or finding
- Explanation of why it is not exploitable in Azimuthal's deployment
- GitHub issue number tracking the fix or permanent acceptance
- Expiry date (max 90 days) — reviewable suppressions keep the security posture honest

---

## Running All Scans Locally

Before pushing any code, run the full scan suite:

```bash
# Run all four scans in sequence
make scan

# Or individually:
make scan-sast        # gosec SAST
make scan-vuln        # govulncheck dependencies
make scan-secrets     # gitleaks secret detection
make scan-container   # trivy container image

# Run everything (format + lint + test + scan)
make pre-push
```

**Prerequisites for local scanning:**

| Tool        | Install command                                               |
|-------------|---------------------------------------------------------------|
| gosec       | `go install github.com/securego/gosec/v2/cmd/gosec@latest`   |
| govulncheck | `go install golang.org/x/vuln/cmd/govulncheck@latest`        |
| gitleaks    | `brew install gitleaks` (macOS) or see Linux instructions above |
| trivy       | `brew install trivy` (macOS) or see Linux instructions above |
| Docker      | Required for `scan-container` (builds the image first)       |

---

## Severity Reference

### gosec severity levels

| Level    | Build impact        | Examples                               |
|----------|---------------------|----------------------------------------|
| CRITICAL | ❌ Fails CI         | SQL injection, command injection       |
| HIGH     | ❌ Fails CI         | Weak crypto, hardcoded creds, G706     |
| MEDIUM   | ℹ️ Informational    | Weak file permissions, integer overflow|
| LOW      | ℹ️ Informational    | Minor issues, informational notes      |

CI flags: `-severity high -confidence high`

### govulncheck severity levels

govulncheck does not use severity tiers. Any vulnerability in a reachable
code path fails the build. Unreachable vulnerabilities are reported as informational.

### gitleaks severity levels

gitleaks does not use severity tiers. Any detected secret fails the build.

### trivy severity levels

| Level    | Build impact        | Notes                                         |
|----------|---------------------|-----------------------------------------------|
| CRITICAL | ❌ Fails CI         | Fails only if a fix is available              |
| HIGH     | ❌ Fails CI         | Fails only if a fix is available              |
| MEDIUM   | ℹ️ Informational    | Reported in SARIF, does not block merge       |
| LOW      | ℹ️ Informational    | Reported in SARIF, does not block merge       |
| UNKNOWN  | ℹ️ Informational    | Reported in SARIF, does not block merge       |

CI flags: `--severity HIGH,CRITICAL --ignore-unfixed`

---

*Last updated: 2026-04-02 — Agent 0B (Security Scan Layer)*
