package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The release pipeline used to publish artifacts nobody had checked, and the
// two defects that made it so are both invisible to every other gate in this
// repository: nothing in `make test-live`, `make verify-api`, `make e2e-test`
// or any CI job reads `.github/workflows/*.yml` at all. So a green battery says
// exactly nothing about whether the release pipeline is still gated — which is
// how it came to be ungated in the first place.
//
// The two defects:
//
//  1. `ci.yml` triggered on `push: branches: [main, develop]` and never on
//     tags, while `release.yml` triggered only on tags. A tagged commit's five
//     platform binaries and its multi-arch image were therefore built and
//     published having never been built by CI, vetted, linted, unit-tested,
//     E2E'd, scanned or booted.
//
//  2. `docker-release` carried no `needs:` whatsoever and pushed the floating
//     tags (`:latest`, `:MAJOR.MINOR`) in the same step as the immutable ones,
//     while `create-release` waited on IT. So `:latest` moved to a new image
//     before the GitHub Release object, its notes and its checksums existed.
//
// These tests fail on the pre-fix files and pass on the current ones — every
// assertion below was checked in both directions by reverting the specific
// property it names. They follow dockerfile_parity_test.go, which reads
// ../../build/Dockerfile from a Go test for the same reason: it is the only
// place a claim about a build file can be re-measured rather than re-asserted.
//
// What these tests deliberately do NOT do is re-implement a YAML parser or
// assert the whole file. They check the handful of properties whose silent loss
// would restore an ungated release, and nothing else.

const (
	ciWorkflow      = "../../.github/workflows/ci.yml"
	releaseWorkflow = "../../.github/workflows/release.yml"
)

func readWorkflow(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304 — path is one of this file's two package constants, both repo-relative
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	// Normalise CRLF. Every anchor below is written against "\n", and a
	// checkout with core.autocrlf=true — the Windows default, and this project
	// develops on Windows — would otherwise fail these tests with a message
	// about a missing `jobs:` block rather than about the property under test.
	// A test that fails for the wrong reason is barely better than one that
	// cannot fail at all: it teaches the reader to distrust it.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// stripComments removes full-line `#` comments so that prose ABOUT a property
// can never be mistaken for the property itself. Without this every assertion
// here would pass on a file where the mechanism had been deleted and only the
// comment explaining it remained — which is the exact failure mode these tests
// exist to prevent, since both workflow files are heavily commented and the
// comments quote the very strings being searched for.
func stripComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestReleaseGate_CIIsCallable is the other half of the edge: release.yml can
// name ci.yml all it likes, but without this trigger the call does not resolve
// and the whole workflow fails to validate.
func TestReleaseGate_CIIsCallable(t *testing.T) {
	ci := stripComments(readWorkflow(t, ciWorkflow))

	if !regexp.MustCompile(`(?m)^\s{2}workflow_call:`).MatchString(ci) {
		t.Error("ci.yml has no `workflow_call:` trigger, so release.yml cannot call it " +
			"and the release pipeline has no CI gate at all")
	}
}

// TestReleaseGate_EveryPublishJobNeedsCI walks the actual `needs:` edges rather
// than trusting the header diagram. A publish job that loses its edge to `ci`
// publishes un-gated artifacts, and nothing else in the repository notices.
func TestReleaseGate_EveryPublishJobNeedsCI(t *testing.T) {
	rel := stripComments(readWorkflow(t, releaseWorkflow))

	needs := jobNeeds(t, rel)

	if _, ok := needs["ci"]; !ok {
		t.Fatal("release.yml declares no `ci` job — the release pipeline is un-gated")
	}

	// Every job that creates something the outside world can reach.
	for _, job := range []string{"build-release", "docker-release", "create-release", "docker-floating-tags"} {
		if _, ok := needs[job]; !ok {
			t.Errorf("release.yml has no %q job; this test is stale and must be updated "+
				"deliberately, not deleted", job)
			continue
		}
		if !reachesCI(needs, job, map[string]bool{}) {
			t.Errorf("job %q does not transitively `needs:` the `ci` job — it can publish "+
				"while CI is red or was never run", job)
		}
	}
}

// TestReleaseGate_NoJobEscapesAFailedNeed is the assertion that keeps the
// `needs:` edges meaningful. A job whose `if:` uses a status-check function
// (always/failure/cancelled/success) no longer carries the implicit `success()`
// on its needs, so a single `if: always()` on any publish job would reopen the
// hole while leaving every `needs:` edge in the file looking correct.
//
// UNLIKE its neighbours, this one PASSES on the pre-fix files, and that is not
// an oversight: the old release.yml had no status functions either. It is a
// forward guard on a property that was never broken, not a regression test for
// a defect that was. Verified by mutation instead — adding `if: always()` to
// create-release fails it.
func TestReleaseGate_NoJobEscapesAFailedNeed(t *testing.T) {
	rel := stripComments(readWorkflow(t, releaseWorkflow))

	// Job-level `if:` only — two-space indent. Step-level `if:` (six spaces) is
	// fine and is used deliberately in docker-floating-tags.
	jobIf := regexp.MustCompile(`(?m)^\s{4}if:\s*(.+)$`)
	statusFn := regexp.MustCompile(`\b(always|failure|cancelled|success)\s*\(`)

	for _, m := range jobIf.FindAllStringSubmatch(rel, -1) {
		if statusFn.MatchString(m[1]) {
			t.Errorf("a job-level `if:` uses a status-check function: %q\n"+
				"That displaces the implicit success() on `needs:`, so the job can run "+
				"even when the CI gate failed. If this is deliberate, it needs to be "+
				"argued in the PR, not merged past this test.", strings.TrimSpace(m[1]))
		}
	}
}

// TestReleaseGate_DockerReleasePublishesNoFloatingTag guards the trap that made
// the first draft of the reordering cosmetic. docker/metadata-action's `flavor`
// defaults to `latest=auto`, which emits `:latest` for any non-prerelease
// `type=semver` OR `type=ref,event=tag` entry — and both are in the tag list. So
// removing the explicit `type=raw,value=latest` line changes nothing on its own:
// without `latest=false`, docker-release goes on pushing `:latest` before the
// GitHub Release exists, exactly as before.
func TestReleaseGate_DockerReleasePublishesNoFloatingTag(t *testing.T) {
	rel := stripComments(readWorkflow(t, releaseWorkflow))

	// Anchored to a line that is ONLY `latest=false` — the flavor list entry —
	// rather than a substring search. The string also appears inside a shell
	// `echo` in docker-floating-tags' failure message, and stripComments does
	// not remove that because it is not a `#` line. A plain Contains therefore
	// passed with the real setting changed to `latest=auto`: the check was
	// satisfied by prose about itself. Caught by mutation-testing this file.
	if !regexp.MustCompile(`(?m)^\s+latest=false\s*$`).MatchString(rel) {
		t.Error("release.yml no longer sets `flavor: latest=false`. metadata-action's " +
			"default is latest=auto, which emits `:latest` for any non-prerelease " +
			"type=semver OR type=ref,event=tag entry — and both are in the tag list. So " +
			"docker-release will publish `:latest` before create-release runs, which is " +
			"the defect the floating-tag job exists to fix")
	}

	// The two floating spellings must not be back in the metadata tag list.
	for _, banned := range []string{
		"type=raw,value=latest",
		"{{major}}.{{minor}}",
	} {
		if strings.Contains(rel, banned) {
			t.Errorf("release.yml's metadata tag list contains %q. Floating tags move in "+
				"docker-floating-tags, after the GitHub Release exists — not here", banned)
		}
	}
}

// TestReleaseGate_FloatingTagsMoveAfterTheRelease is defect 2 stated directly:
// the job that reassigns `:latest` must run behind the job that publishes the
// GitHub Release, so `:latest` can never point at an image with no release.
func TestReleaseGate_FloatingTagsMoveAfterTheRelease(t *testing.T) {
	rel := stripComments(readWorkflow(t, releaseWorkflow))
	needs := jobNeeds(t, rel)

	floating, ok := needs["docker-floating-tags"]
	if !ok {
		t.Fatal("release.yml has no `docker-floating-tags` job — if the floating tags moved " +
			"back into docker-release, they move before the GitHub Release again")
	}

	var found bool
	for _, n := range floating {
		if n == "create-release" {
			found = true
		}
	}
	if !found {
		t.Error("docker-floating-tags does not `needs: create-release`, so `:latest` can move " +
			"before the GitHub Release object, its notes and its checksums exist")
	}
}

// jobNeeds maps each top-level job id to its `needs:` list. Both spellings are
// handled: `needs: ci` and `needs: [a, b]`.
func jobNeeds(t *testing.T, src string) map[string][]string {
	t.Helper()

	jobsAt := strings.Index(src, "\njobs:\n")
	if jobsAt < 0 {
		t.Fatal("workflow has no top-level `jobs:` block")
	}
	body := src[jobsAt:]

	jobRe := regexp.MustCompile(`(?m)^\s{2}([a-z][a-z0-9-]*):\s*$`)
	needsRe := regexp.MustCompile(`(?m)^\s{4}needs:\s*(.+)$`)

	locs := jobRe.FindAllStringSubmatchIndex(body, -1)
	out := make(map[string][]string, len(locs))

	for i, loc := range locs {
		name := body[loc[2]:loc[3]]
		end := len(body)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := body[loc[1]:end]

		var deps []string
		if m := needsRe.FindStringSubmatch(block); m != nil {
			raw := strings.TrimSpace(m[1])
			raw = strings.Trim(raw, "[]")
			for _, d := range strings.Split(raw, ",") {
				if d = strings.TrimSpace(d); d != "" {
					deps = append(deps, d)
				}
			}
		}
		out[name] = deps
	}

	if len(out) == 0 {
		t.Fatal("parsed no jobs out of the workflow — this test cannot assert anything, " +
			"which means the parser is broken rather than the workflow being clean")
	}
	return out
}

// reachesCI walks `needs:` edges to the `ci` job. `seen` breaks cycles, which
// GitHub itself rejects but which this parser must survive regardless.
func reachesCI(needs map[string][]string, job string, seen map[string]bool) bool {
	if seen[job] {
		return false
	}
	seen[job] = true

	for _, dep := range needs[job] {
		if dep == "ci" {
			return true
		}
		if reachesCI(needs, dep, seen) {
			return true
		}
	}
	return false
}
