# Security Scanning — Azimuthal

This document describes every security scanner used in the Azimuthal CI pipeline,
how to run them locally, what findings cause a build to fail, and what to do when
a scanner reports something that is not a real vulnerability.

---

## Table of Contents

1. [Overview](#overview)
2. [gosec — Static Analysis (SAST)](#gosec--static-analysis-sast)
3. [govulncheck — Dependency Vulnerability Scan](#govulncheck--dependency-vulnerability-scan)
4. [gitleaks — Secret Detection](#gitleaks--secret-detection)
5. [trivy — Container Image Scan](#trivy--container-image-scan)
6. [Handling a False Positive](#handling-a-false-positive)
7. [Running All Scans Locally](#running-all-scans-locally)
8. [Severity Reference](#severity-reference)

---

## Overview

All four scanners gate every code pull request **on the public repository**. Each
job carries `if: endsWith(github.repository, '/azimuthal')`, so on a
differently-named mirror or private sandbox none of them run at all. Secret and
vulnerability scanning protect the public repository; they are not a safety net
on a sandbox, and "the scanners passed" on a differently-named remote means only
that they never executed.

**Documentation-only pull requests** (every changed file under `docs/**` or a
`*.md` anywhere, excluding the generated `docs/api/openapi.yaml`) skip gosec,
govulncheck, and trivy along with the build/test gates — a markdown change
cannot alter the code or dependencies those scanners examine. The skipped jobs
still report a (skipped) status, so required checks stay satisfied.
**gitleaks does not cascade-skip**: unlike the other three it declares no
`needs:`, so it still runs on a docs-only PR — a credential pastes into a
markdown file as easily as into code. It is still subject to the repository-name
gate above. The path classification lives in the `changes` job in
`.github/workflows/ci.yml`.

| Scanner      | What it scans            | Fails on               | CI job          |
|--------------|--------------------------|------------------------|-----------------|
| gosec        | Go source code (SAST)    | HIGH+ severity         | `sast`          |
| govulncheck  | Go module dependencies   | Any known CVE          | `vuln-scan`     |
| gitleaks     | Working tree (not history) | Any detected secret  | `secret-scan`   |
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
# Same flags as CI — but see the version note below
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

**A finding you believe is wrong:** See [Handling a False Positive](#handling-a-false-positive).

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

**What it scans:** The **working tree** — every file present at the checked-out
commit — for secrets, API keys, tokens, and credentials that should never be
committed.

It does **not** scan git history. CI passes `--no-git` deliberately: without it
gitleaks walks every historical commit and fails on secrets that have since been
stripped from the tree (internal scripts removed from public-facing commits by
`push-public.sh`, for instance). The consequence worth knowing is that a
credential committed and then deleted in a later commit will **not** be caught
here — a leaked credential must be rotated, not merely deleted.

gitleaks catches:

- Hardcoded passwords, tokens, and API keys
- Private keys (RSA, EC, PGP)
- Connection strings with embedded credentials
- Cloud provider credentials (AWS, GCP, Azure)
- HMAC and signing keys
- High-entropy strings that merely *look* like credentials — see
  [Handling a False Positive](#handling-a-false-positive)

**Configuration: none.** There is no `.gitleaks.toml` in this repository, and CI
passes no `--config`, so gitleaks runs on its **built-in default ruleset**
(100+ patterns). A `.gitleaks.toml` existed in the original scaffold and was
removed when the repository was made public; nothing has needed it since. Adding
one back would be adding an exemption file — see the policy below.

The exact CI invocation is:

```bash
gitleaks detect --source=. --no-git --redact --verbose --no-banner \
  --exit-code=1 --report-format=sarif --report-path=gitleaks-results.sarif
```

`--redact` keeps the secret itself out of the CI log, which is public. The SARIF
report is uploaded as a build artifact with a 7-day retention.

**What fails the build:**
Any detected secret in any file in the working tree fails the build.
There is no severity tier — any finding = fail.

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
make scan-secrets
```

The target scans the directories that actually hold this project's code, one at
a time:

```bash
for p in ./internal ./migrations ./cmd; do
  gitleaks detect --source=$p --no-git --redact --verbose --no-banner --exit-code=1 || exit 1
done
```

**Why one path at a time, and not a list.** `gitleaks detect` takes a single
`-s/--source` and accepts **no positional arguments**. A path *list* —
`--source=. ./internal ./migrations ./cmd`, which is what this section
recommended until the target was repaired — is therefore not rejected but
silently ignored: gitleaks scans `--source=.` and the three named paths do
nothing at all. Measured on this repository after `npm ci`, that is 29.4s
against 0.6s for the loop. Scoping only becomes real when each path gets its own
invocation.

**Why scope at all.** `--no-git` walks the filesystem rather than the git index,
so what it scans is "files on disk" rather than "files git tracks" — and after
`npm ci` that includes `web/node_modules`, tens of thousands of vendored files
full of test fixtures and sample keys. `node_modules` being gitignored is not
enough to keep it out of a filesystem walk.

CI never encounters this: the `secret-scan` job checks out the repository and
installs no Node dependencies, so `node_modules` does not exist there. Locally it
usually does — which is why CI can afford the unscoped `--source=.` that this
section cannot.

Add `./web/src` when you have touched the frontend; skip `./web` as a whole.

**A finding that is not a secret:** See [Handling a False Positive](#handling-a-false-positive).

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
# Build image then scan (see the caveat below — this is NOT what CI scans)
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

**A finding you believe is wrong:** See [Handling a False Positive](#handling-a-false-positive).

---

## Handling a False Positive

**The governing rule is that findings are fixed, not suppressed.** A suppression is
a permanent claim that a scanner is wrong, written by somebody who will not be
reading it in a year. Prefer changing the code so the finding does not arise.

That is usually possible, and the result is usually better code. The worked
example in this repository is `portalKeyAlphabet` in
`internal/core/portal/service.go`: a 32-character base32 alphabet, written as a
single string literal, is indistinguishable from an embedded credential to a
secret scanner, and gitleaks flagged it as a generic API key. There was nothing
to suppress — it is an alphabet. The fix was to write it as two concatenated
literals so it *reads* like one:

```go
const portalKeyAlphabet = "abcdefghijklmnopqrstuvwxyz" + "234567"
```

The finding is gone, no exemption file was created, and the constant is now
clearer than it was. Reach for that shape first.

### What "fixed" looks like, per scanner

| Scanner | The fix |
|---|---|
| gosec | Change the pattern it flagged — validate the path, use `crypto/rand`, set the TLS field, narrow the permission. |
| govulncheck | Update the dependency (`go get <module>@latest && go mod tidy`). **There is no suppression mechanism at all**, by design. |
| gitleaks | Make the string not look like a credential, or stop committing it. |
| trivy | Update the base image or the Go module the CVE lands in. |

### When a suppression is genuinely unavoidable

It requires all three of a **documented justification**, a **tracking issue**, and
an **expiry date** (maximum 90 days). Undocumented suppressions are rejected in
review. Never suppress a whole file or package — suppress the narrowest thing
that works.

> **What is actually in the tree, and an unresolved question about it.** There are
> **36** gosec-facing annotations today — 8 `#nosec` and 27 `//nolint:gosec`, plus
> one prose reference. **None carries a tracking issue or an expiry date.** Every
> one carries a reason.
>
> Whether that is 36 policy violations depends on a distinction this document has
> never drawn: between *"the rule does not apply here"* (`G304` on a
> `t.TempDir()` path — there is no risk to track and nothing to expire) and
> *"the finding is real and we are accepting it for now"* (an unpatched CVE),
> which is the case the issue-and-expiry requirement plainly exists for. Every
> annotation in the tree is the first kind.
>
> Reading the requirement as covering only the second kind would make the
> repository compliant and the rule meaningful. That reading is **not recorded
> anywhere**, so it is flagged for a maintainer rather than adopted here. Until it
> is settled, write the reason, keep it narrow, and do not treat the existing 36
> as precedent for skipping the ceremony on an accepted risk.
>
> There is no `.trivyignore`, and `trivy-ignore.yaml` holds no active rules — so
> no accepted-risk suppression exists in the repository at all right now.

**Do not create a new exemption file to hold a suppression.** In particular, do
not add a `.gitleaks.toml` allowlist: gitleaks has no allowlist in this
repository and needs none, and introducing one converts a single visible finding
into a ledger nobody re-reads. The same reasoning is why there is no eslint
baseline (`docs/known-issues.md` §17) and no `--max-warnings` slack.

The two mechanisms that already exist, and their required formats, follow.

### gosec — an inline annotation on the specific line

There are two annotations, they are read by different tools, and which one you
need depends on which tool is reporting:

- **`// #nosec GXXX -- <reason>`** is gosec's own directive. It is what silences
  the standalone `gosec` binary the `sast` CI job runs.
- **`//nolint:gosec // <reason>`** is golangci-lint's directive. `.golangci.yml`
  enables `gosec` as one of its linters, so this is what silences `make lint`.

Both carry a mandatory reason. Write the one the failing tool reads — this
repository does not double up, and the existing annotations show the split
clearly: production paths in `cmd/server/` use `#nosec` with the rule ID and a
reason, while test helpers use `//nolint:gosec` naming the rule in the comment.

**The required format**, in both cases, names the rule and says why the pattern
is not exploitable *here* — not what the rule is:

```go
// Production code, gosec's own directive:
outFile, err := os.Create(backupOutput) // #nosec G304 -- user-provided CLI flag

// Test helper, golangci-lint's directive:
f, err := os.Create(path) //nolint:gosec // G304 — a t.TempDir() path
```

"G304 is about file inclusion" is not a reason. "The path is a `t.TempDir()`
path" is.

**Never suppress a whole file or package.** Suppress only the specific line.

### govulncheck — No suppression available

govulncheck does not support suppressions. Resolve the vulnerability by updating
the dependency. If the vulnerable code path is genuinely unreachable and the
govulncheck report is incorrect, file a bug upstream.

### gitleaks — no suppression mechanism is configured, and that is deliberate

There is no `.gitleaks.toml` and no allowlist. A gitleaks finding is resolved by
changing the string, not by exempting it. The three shapes that come up:

1. **It is a real credential.** Rotate it first — `--no-git` means the working
   tree was scanned, but the credential is in the git history regardless, and
   deleting the line does not un-leak it. Then remove it from the tree and
   replace it with an environment variable.
2. **It is a placeholder or fixture.** Make it obviously synthetic. `change-me`,
   `example`, and `localhost` do not trip the default ruleset; a random-looking
   32-character hex string does, whether or not it ever authenticated anything.
   `.env.example` is scanned like every other file and passes today because its
   values say `change-me-use-a-strong-password`.
3. **It is neither — it just has high entropy.** This is the `portalKeyAlphabet`
   case above. Restructure the literal so its purpose is legible.

If a finding survives all three — a string that must exist, in that exact form,
and genuinely is not a secret — that is the point to stop and raise it with a
maintainer rather than to introduce the repository's first allowlist file. The
decision to start keeping exemptions is a maintainer's, not a contributor's.

One implementation note if a config is ever added: gitleaks uses the **RE2**
regex engine, not PCRE. Lookaheads (`(?!...)`, `(?=...)`) and lookbehinds are
unsupported and cause a panic rather than a clean error.

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

`trivy-ignore.yaml` currently holds **no active suppressions** — only the
template, commented out — and `ignore-policy` is correspondingly commented out in
`trivy.yaml`. Both must change together: an empty Rego module is invalid, so
enabling `ignore-policy` without adding a rule produces a parse error, and
removing the last rule without disabling `ignore-policy` does the same.

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

### Two ways a local run differs from the CI gate

Neither is a reason to skip the local run — a finding it reports is real. But a
*clean* local run is weaker evidence than it looks, so do not read one as "the
gate will pass."

**1. Versions.** CI pins every scanner (`.github/workflows/ci.yml`, the `env:`
block): `GOSEC_VERSION`, `GITLEAKS_VERSION`, `GOVULNCHECK_VERSION`. Every install
instruction in this document, and every `which || go install` line in the
Makefile, fetches `@latest` instead. Scanner rulesets change between releases in
both directions, so a local pass and a CI failure on identical code is an
expected outcome, not a mystery. Install the pinned version when a result matters:

```bash
go install github.com/securego/gosec/v2/cmd/gosec@v2.26.1   # match ci.yml
```

The one exception is govulncheck, whose advisory database is fetched live at scan
time — so there the *database* matches CI even when the binary does not.

**2. The container scan does not scan the same image.** CI builds
`build/Dockerfile.ci` around the server binary produced by the `build` job.
`make scan-container` and the command in the trivy section build
`build/Dockerfile`, which is a different Dockerfile compiling its own binary. The
local scan is a useful approximation of the base image and the Go module graph;
it is not the artifact the gate examines.

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
| HIGH     | ❌ Fails CI         | SQL injection, command injection, weak crypto, hardcoded creds, G706 |
| MEDIUM   | ℹ️ Informational    | Weak file permissions, integer overflow|
| LOW      | ℹ️ Informational    | Minor issues, informational notes      |

CI flags: `-severity high -confidence high`

gosec emits **HIGH, MEDIUM and LOW only** — there is no CRITICAL tier. An earlier
version of this table listed one, which made `-severity high` look like it let a
worse class of finding through. It does not: `high` is the top of the scale.

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

*Last updated: 2026-07-30 — cleanup sweep. Corrected the gitleaks section (there is
no `.gitleaks.toml`, and CI scans the working tree rather than git history),
replaced the suppression section with the policy the project actually follows, and
recorded that the repository-name gate means these scanners do not run on a
non-public remote.*
