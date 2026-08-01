package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// build/Dockerfile.ci is a SECOND, independent base declaration, and it is the
// one CI actually scans and boot-tests: both the container-scan and docker-e2e
// jobs build it, not build/Dockerfile. So a change made only to build/Dockerfile
// leaves CI green while security-scanning and booting an image that is not the
// one that ships — a gate that appears to cover the artifact and does not.
//
// That is the same defect class as the one this file's neighbours exist to
// close: `azimuthal backup` forked pg_dump and `azimuthal restore` forked psql
// on an image that contained neither, and nothing announced it because no gate
// ran the command. These tests make the two files' disagreement loud.
//
// Note what is deliberately NOT asserted: that the two files are identical.
// They differ where they must — build/Dockerfile compiles the binary and copies
// it from the builder stage, Dockerfile.ci receives one pre-built by the
// runner. Only the parts that must agree are checked.

const (
	shippedDockerfile = "../../build/Dockerfile"
	ciDockerfile      = "../../build/Dockerfile.ci"

	sharedBlockBegin = "# >>> BEGIN SHARED PGCLIENT STAGE"
	sharedBlockEnd   = "# <<< END SHARED PGCLIENT STAGE"
)

func readDockerfile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	// Normalise line endings: this repository is developed on Windows as well
	// as Linux, and a CRLF checkout would otherwise fail every comparison
	// below for a reason that has nothing to do with the image.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// sharedBlock extracts the marked pgclient stage. It returns the empty string
// when the markers are missing, which the callers treat as a failure — a
// deleted marker must not silently turn this guard into a no-op.
func sharedBlock(t *testing.T, path string) string {
	t.Helper()
	src := readDockerfile(t, path)
	start := strings.Index(src, sharedBlockBegin)
	end := strings.Index(src, sharedBlockEnd)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("%s: shared pgclient stage markers missing or malformed "+
			"(want %q ... %q). The markers are what makes the two Dockerfiles "+
			"comparable; do not delete them.", path, sharedBlockBegin, sharedBlockEnd)
	}
	return src[start : end+len(sharedBlockEnd)]
}

// TestDockerfiles_PgClientStageIsIdentical is the drift guard proper. The
// pgclient stage resolves each binary's shared-library closure and is fiddly
// enough that a hand-reapplied edit would plausibly differ; requiring byte
// equality removes the judgement call.
func TestDockerfiles_PgClientStageIsIdentical(t *testing.T) {
	shipped := sharedBlock(t, shippedDockerfile)
	ci := sharedBlock(t, ciDockerfile)

	if shipped != ci {
		t.Errorf("the shared pgclient stage differs between %s and %s.\n"+
			"These must be byte-identical: CI builds Dockerfile.ci, operators run "+
			"the image built from Dockerfile, and a difference means CI is testing "+
			"an artifact nobody ships.\n\n--- %s ---\n%s\n\n--- %s ---\n%s",
			shippedDockerfile, ciDockerfile, shippedDockerfile, shipped, ciDockerfile, ci)
	}
}

// finalBase returns the image named by the LAST FROM in a Dockerfile — the
// base of the stage that actually ships.
func finalBase(t *testing.T, path string) string {
	t.Helper()
	var last string
	for _, line := range strings.Split(readDockerfile(t, path), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && strings.EqualFold(fields[0], "FROM") {
			last = fields[1]
		}
	}
	if last == "" {
		t.Fatalf("%s: no FROM instruction found", path)
	}
	return last
}

// TestDockerfiles_FinalBaseAgrees pins the two images to the same base. This
// fails if someone changes one file's base and forgets the other, which is
// exactly how CI would end up scanning a different OS from the one shipping.
func TestDockerfiles_FinalBaseAgrees(t *testing.T) {
	shipped := finalBase(t, shippedDockerfile)
	ci := finalBase(t, ciDockerfile)

	if shipped != ci {
		t.Errorf("final base image differs: %s uses %q but %s uses %q. "+
			"CI would scan and boot-test a different operating system from the "+
			"one operators run.", shippedDockerfile, shipped, ciDockerfile, ci)
	}
}

// TestDockerfiles_ShipPostgresClient asserts the client tools are actually
// copied into the final stage of BOTH files.
//
// The negative-test question (CLAUDE.md §2): delete either `COPY --from=pgclient`
// line and this fails. It is not a restatement of the identity test above —
// that one compares the pgclient *builder* stage, which both files could carry
// while neither copied its output into the image that ships.
func TestDockerfiles_ShipPostgresClient(t *testing.T) {
	for _, path := range []string{shippedDockerfile, ciDockerfile} {
		src := readDockerfile(t, path)

		_, final, ok := strings.Cut(src, sharedBlockEnd)
		if !ok {
			t.Fatalf("%s: shared block end marker missing", path)
		}
		if !strings.Contains(final, "COPY --from=pgclient") {
			t.Errorf("%s: the final stage never copies the pgclient stage's output. "+
				"`azimuthal backup` forks pg_dump and `azimuthal restore` forks psql; "+
				"without this COPY both fail at the fork inside the container, which "+
				"is the defect D105 records.", path)
		}
	}
}

// debianCodenames maps the Debian release codename a postgres tag is built on
// to the numbered distroless base that carries the matching glibc.
var debianCodenames = map[string]string{
	"bullseye": "11",
	"bookworm": "12",
	"trixie":   "13",
}

var (
	postgresTagPattern = regexp.MustCompile(`FROM\s+postgres:\d+-([a-z]+)\s`)
	distrolessPattern  = regexp.MustCompile(`FROM\s+gcr\.io/distroless/[a-z0-9-]*debian(\d+)`)
)

// TestDockerfiles_LibcGenerationsMatch encodes the trap that made this change
// non-obvious, so the next person cannot fall into it.
//
// pg_dump and psql are dynamically linked. Copying binaries built against one
// glibc onto a base carrying an older one produces an image that builds
// cleanly, scans cleanly, and dies at runtime with a version-mismatch error
// the moment an operator takes a backup. This is live, not hypothetical: the
// unsuffixed `postgres:16` tag has already rolled forward to Debian 13
// (glibc 2.41), whose binaries do not run on bookworm's 2.36.
//
// So both sides must name their Debian generation, and the two must agree.
func TestDockerfiles_LibcGenerationsMatch(t *testing.T) {
	for _, path := range []string{shippedDockerfile, ciDockerfile} {
		src := readDockerfile(t, path)

		pgMatch := postgresTagPattern.FindStringSubmatch(src)
		if pgMatch == nil {
			t.Fatalf("%s: no `FROM postgres:<major>-<codename>` found. The postgres "+
				"tag must name its Debian codename explicitly — an unsuffixed tag "+
				"moves between Debian releases and silently breaks the glibc match.", path)
		}
		codename := pgMatch[1]

		wantGeneration, known := debianCodenames[codename]
		if !known {
			t.Fatalf("%s: unrecognised Debian codename %q in the postgres tag. Add it "+
				"to debianCodenames with its numbered generation so this guard keeps "+
				"working.", path, codename)
		}

		baseMatch := distrolessPattern.FindStringSubmatch(src)
		if baseMatch == nil {
			t.Fatalf("%s: no `FROM gcr.io/distroless/...debian<N>` base found", path)
		}
		gotGeneration := baseMatch[1]

		if gotGeneration != wantGeneration {
			t.Errorf("%s: glibc generation mismatch — postgres tag is %q (Debian %s) "+
				"but the distroless base is debian%s. Binaries copied from one will "+
				"not run on the other; the image builds and scans clean and fails at "+
				"the operator's first backup.",
				path, codename, wantGeneration, gotGeneration)
		}
	}
}

// TestDockerfiles_ClientMajorMeetsServer keeps the bundled client at or above
// the server major the bundled Compose file runs. pg_dump refuses to dump a
// server newer than itself, so a client behind the server breaks backups —
// and docs/self-hosting.md publishes this rule to operators.
func TestDockerfiles_ClientMajorMeetsServer(t *testing.T) {
	clientMajor := regexp.MustCompile(`FROM\s+postgres:(\d+)-`).FindStringSubmatch(readDockerfile(t, shippedDockerfile))
	if clientMajor == nil {
		t.Fatal("could not read the bundled client's postgres major version")
	}

	compose, err := os.ReadFile("../../build/docker-compose.yml")
	if err != nil {
		t.Fatalf("reading bundled compose file: %v", err)
	}
	serverMajor := regexp.MustCompile(`image:\s+postgres:(\d+)`).FindStringSubmatch(string(compose))
	if serverMajor == nil {
		t.Fatal("could not read the bundled server's postgres major version from build/docker-compose.yml")
	}

	// Compared as integers, not strings: "16" < "9" lexically, which would
	// have made this guard pass on exactly the mismatch it exists to catch.
	client, err := strconv.Atoi(clientMajor[1])
	if err != nil {
		t.Fatalf("client major %q is not a number: %v", clientMajor[1], err)
	}
	server, err := strconv.Atoi(serverMajor[1])
	if err != nil {
		t.Fatalf("server major %q is not a number: %v", serverMajor[1], err)
	}

	if client < server {
		t.Errorf("bundled pg_dump/psql are PostgreSQL %d but build/docker-compose.yml "+
			"runs postgres:%d. pg_dump refuses to dump a server newer than itself, so "+
			"backups would fail. The client major must be >= the server major.",
			client, server)
	}
}
