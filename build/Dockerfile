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
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS builder
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

# -- Stage 3: PostgreSQL client tools (per-target) ----------------------------
# `azimuthal backup` forks pg_dump and psql; `azimuthal restore` forks psql.
# The final image therefore has to carry them, or those two commands fail at
# the fork — which is precisely what shipped until this stage existed.
#
# THIS STAGE IS THE ONE EXCEPTION TO THE NO-EMULATION RULE at the top of this
# file. It has no --platform pin, so buildx runs it on $TARGETPLATFORM and
# arm64 goes through QEMU. That is deliberate and cheap: the stage compiles
# nothing, it copies files and runs `ldd`, which must see the *target*
# architecture's binaries and loader to resolve the right closure. Seconds
# under emulation, against the 30-60 min a QEMU npm+go build would cost.
#
# Pinned to -bookworm, matching the final stage's base-debian12. This is not
# cosmetic: the unsuffixed `postgres:16` tag has already moved to Debian 13
# (glibc 2.41), and those binaries abort at startup on bookworm's glibc 2.36.
# Both sides name their Debian generation so the pairing cannot drift apart
# silently the next time an upstream tag rolls forward.

# >>> BEGIN SHARED PGCLIENT STAGE — byte-identical in build/Dockerfile.ci >>>
FROM postgres:16-bookworm AS pgclient

# The final image's own rootfs, as a reference set only — nothing from here is
# shipped. It tells the copy below which libraries the base already provides.
COPY --from=gcr.io/distroless/base-debian12:nonroot / /baseref/

RUN set -eu; \
    # True when the distroless base already ships this library. The two extra
    # cases exist because the base is NOT usr-merged — it keeps glibc under
    # /lib and openssl under /usr/lib, while postgres resolves both under
    # /lib — so a naive path test reports "missing" for libssl and libcrypto
    # and duplicates them.
    base_has() { \
        p="$1"; \
        [ -e "/baseref$p" ] && return 0; \
        case "$p" in \
            /lib/*) [ -e "/baseref/usr$p" ] && return 0 ;; \
            /usr/lib/*) [ -e "/baseref${p#/usr}" ] && return 0 ;; \
        esac; \
        return 1; \
    }; \
    mkdir -p /staging/usr/bin; \
    # /usr/bin/pg_dump and /usr/bin/psql in this image are symlinks to
    # pg_wrapper, a PERL SCRIPT. Copying those paths lands a script with no
    # interpreter behind it. The real ELF binaries are under /usr/lib.
    for bin in pg_dump psql; do \
        src="/usr/lib/postgresql/16/bin/$bin"; \
        # The Go code calls exec.Command("pg_dump", ...) — a bare name that
        # exec.LookPath resolves against $PATH. /usr/bin is on the distroless
        # default PATH, so staging them here needs no ENV PATH that a compose
        # environment: block could overwrite.
        cp -a "$src" "/staging/usr/bin/$bin"; \
        for lib in $(ldd "$src" | awk '/=> \//{print $3}' | sort -u); do \
            # Never overwrite a base-provided library. Their patch levels can
            # differ, and clobbering one would leave the image's dpkg metadata
            # — which is what Trivy reads — describing a file that is no
            # longer on disk.
            base_has "$lib" && continue; \
            mkdir -p "/staging$(dirname "$lib")"; \
            cp -L "$lib" "/staging$lib"; \
        done; \
    done; \
    # Make the copied libraries VISIBLE TO THE CONTAINER SCANNER.
    #
    # Trivy inventories a distroless image by reading /var/lib/dpkg/status.d.
    # Libraries copied in as bare files carry no package metadata, so without
    # this block the scan would report only the base's own handful of packages
    # and silently ignore everything added here — including libpq, krb5, gnutls
    # and openldap, all of which have CVE histories. A scan that cannot see
    # half the image is the same class of defect as a backup command that
    # cannot run: a gate that appears to cover the artifact and does not.
    # THIS image is usr-merged (/lib is a symlink to /usr/lib) while the
    # distroless base is not, so ldd reports /lib/... and dpkg records
    # /usr/lib/... — the lookup has to try both spellings or it reports
    # "no package" for libraries that plainly have one.
    mkdir -p /staging/var/lib/dpkg/status.d; \
    for staged in $(find /staging -name '*.so*' -type f); do \
        orig="${staged#/staging}"; \
        pkg=$(dpkg -S "$orig" 2>/dev/null | head -1 | cut -d: -f1); \
        [ -n "$pkg" ] || pkg=$(dpkg -S "/usr$orig" 2>/dev/null | head -1 | cut -d: -f1); \
        # A library whose owning package cannot be identified would ship
        # unscanned, so this is a hard failure rather than a warning.
        [ -n "$pkg" ] || { echo "FATAL: no dpkg package owns $orig" >&2; exit 1; }; \
        dpkg -s "$pkg" > "/staging/var/lib/dpkg/status.d/$pkg"; \
    done; \
    echo "=== packages declared to the scanner ==="; \
    ls /staging/var/lib/dpkg/status.d; \
    # Closure completeness check, so a future base that drops a library fails
    # the build rather than an operator's 2 AM cron.
    #
    # Deliberately NOT `/staging/usr/bin/pg_dump --version`. That would prove
    # nothing here: this stage IS the postgres image, where every library
    # already exists natively, so it passes even when the copy above staged
    # nothing at all — a check that cannot fail. What is asserted instead is
    # the property that actually matters, that every library the binaries need
    # is either staged or provided by the base. Executing them is still the
    # real proof, and it happens against the FINAL image in CI's docker-e2e
    # job, which cannot be fooled this way.
    for bin in pg_dump psql; do \
        [ -x "/staging/usr/bin/$bin" ] || { echo "FATAL: $bin was not staged" >&2; exit 1; }; \
        for lib in $(ldd "/usr/lib/postgresql/16/bin/$bin" | awk '/=> \//{print $3}' | sort -u); do \
            [ -e "/staging$lib" ] || base_has "$lib" || \
                { echo "FATAL: $bin needs $lib, which is neither staged nor in the base" >&2; exit 1; }; \
        done; \
    done; \
    echo "=== library closure complete for pg_dump and psql ==="
# <<< END SHARED PGCLIENT STAGE <<<

# -- Stage 4: Final distroless image (per-target, no execution) ---------------
# buildx selects the $TARGETARCH variant of the multi-arch distroless base.
#
# base-debian12, not static: pg_dump and psql are dynamically linked and need
# a libc. KEEP THIS STAGE IN LOCKSTEP WITH build/Dockerfile.ci — that is a
# second, independent base declaration, and it is the one CI scans and boots.
# TestDockerfiles_FinalBaseAgrees fails when the two drift.
FROM gcr.io/distroless/base-debian12:nonroot
USER nonroot:nonroot
COPY --from=builder --chown=nonroot:nonroot /azimuthal /azimuthal
COPY --from=pgclient /staging/ /
EXPOSE 8080
ENTRYPOINT ["/azimuthal", "serve"]
