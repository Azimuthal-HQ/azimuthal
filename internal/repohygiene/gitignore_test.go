// Package repohygiene holds structural guards over the repository itself —
// assertions about files like .gitignore that no product package owns, and
// that no other test would ever exercise.
//
// It contains no non-test code on purpose. The things it guards are not
// libraries; they are facts about the checkout.
package repohygiene

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "reached the filesystem root without finding go.mod")
		dir = parent
	}
	t.Fatal("could not locate the module root")
	return ""
}

// gitignorePatterns returns every non-blank, non-comment line of the root
// .gitignore, in file order, paired with its 1-based line number.
func gitignorePatterns(t *testing.T) []gitignoreLine {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".gitignore"))
	require.NoError(t, err, "the repository must have a root .gitignore")

	var out []gitignoreLine
	for i, line := range strings.Split(string(raw), "\n") {
		p := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		out = append(out, gitignoreLine{Number: i + 1, Pattern: p})
	}
	return out
}

type gitignoreLine struct {
	Number  int
	Pattern string
}

// isBare reports whether git will match this pattern at EVERY directory depth
// rather than only against the repository root.
//
// git's rule: a pattern containing a slash anywhere other than at the end is
// relative to the .gitignore's own directory; a pattern with no slash, or with
// only a trailing one, is matched against every path component at every depth.
// A leading slash therefore anchors, and so does any interior slash.
func isBare(pattern string) bool {
	p := strings.TrimPrefix(pattern, "!") // a negation anchors by the same rule
	p = strings.TrimSuffix(p, "/")        // a trailing slash only means "directory"
	return !strings.Contains(p, "/")
}

// reviewedBarePatterns is the allowlist: every slash-less pattern that is
// deliberately global, with the reason it is.
//
// This is a ledger of decisions, not a ledger of exemptions — each entry names
// a mechanism that genuinely writes files at arbitrary depth. If you are adding
// a row here to make the suite pass, the pattern almost certainly wants a
// leading slash instead. See the header comment in .gitignore.
var reviewedBarePatterns = map[string]string{
	"*.exe":   "compiled binary; never committed at any depth",
	"*.exe~":  "editor backup of a compiled binary",
	"*.dll":   "compiled binary; never committed at any depth",
	"*.so":    "compiled binary; never committed at any depth",
	"*.dylib": "compiled binary; never committed at any depth",

	"*.test":         "`go test -c` writes it into whichever package directory it ran in",
	"*.out":          "`go test -coverprofile` and pprof write next to the invocation",
	"*.coverprofile": "same class as *.out; tooling writes it where it is invoked",

	".env":          "a .env is a secret wherever it appears — web/.env as much as the root one",
	"!.env.test":    "negation of the above; bare so it keeps tracking samples at any depth",
	"!.env.example": "negation of the above; bare so it keeps tracking samples at any depth",

	".claude/":          "Claude Code settings; worktrees and nested checkouts each get their own",
	".golangci-cache*/": "per-worktree lint caches, created beside whichever worktree runs the linter",
}

// TestGitignore_NoUnreviewedBarePatterns fails when a slash-less pattern
// appears in .gitignore without a reviewed reason.
//
// The defect this guards is the `server` trap: a bare `server` pattern matched
// `cmd/server/`, so every new file added under that directory was skipped
// silently by `git add -A`. Nothing announced it — `git status` does not report
// paths it has been told to ignore — and the files were simply missing from the
// commit. The same shape was live in five other patterns when this test was
// written (`data/` matched `web/src/data/`, `coverage.*` matched a
// `docs/coverage.md` that could have been added at any time, plus `bin/`,
// `go.work` and `profile.cov`).
//
// Deleting this test's assertion would let the next one through unnoticed,
// which is the whole failure mode.
func TestGitignore_NoUnreviewedBarePatterns(t *testing.T) {
	var unreviewed []string
	seen := map[string]bool{}

	for _, line := range gitignorePatterns(t) {
		if !isBare(line.Pattern) {
			continue
		}
		seen[line.Pattern] = true
		if _, ok := reviewedBarePatterns[line.Pattern]; !ok {
			unreviewed = append(unreviewed, line.Pattern+" (.gitignore:"+strconv.Itoa(line.Number)+")")
		}
	}

	require.Empty(t, unreviewed,
		"these .gitignore patterns have no slash, so git matches them at EVERY depth, "+
			"not just the repository root:\n  %s\n\n"+
			"If the pattern means one path, give it a leading slash (`/data/`, not `data/`). "+
			"If it is genuinely global, add it to reviewedBarePatterns with the reason. "+
			"See the header comment in .gitignore.",
		strings.Join(unreviewed, "\n  "))

	// The ledger must not outlive its entries either: a stale row here would
	// quietly re-permit a pattern someone had deliberately anchored.
	var stale []string
	for pattern := range reviewedBarePatterns {
		if !seen[pattern] {
			stale = append(stale, pattern)
		}
	}
	require.Empty(t, stale,
		"reviewedBarePatterns lists patterns that are no longer in .gitignore: %v — remove them",
		stale)
}

// TestGitignore_RootIntentPatternsDoNotMatchAtDepth is the behavioural half.
//
// The test above reads pattern text; this one asks git itself, which is the
// only authority on what these patterns actually match. Each path below is one
// git would have ignored before the anchoring pass — `web/src/data/seed.ts` was
// matched by `data/`, `docs/coverage.md` by `coverage.*`. None of these files
// exist, which is why --no-index is required: the question is what git WOULD do
// if a change introduced them.
//
// Fails before the fix, passes after: on the pre-anchoring .gitignore every one
// of these paths reports ignored.
func TestGitignore_RootIntentPatternsDoNotMatchAtDepth(t *testing.T) {
	root := repoRoot(t)
	requireGitRepo(t, root)

	// Paths a future change could plausibly add, which must NOT be ignored.
	mustBeVisible := []string{
		"web/src/data/seed.ts",       // was matched by bare `data/`
		"internal/core/data/fix.go",  // was matched by bare `data/`
		"internal/tools/bin/run.sh",  // was matched by bare `bin/`
		"docs/coverage.md",           // was matched by bare `coverage.*`
		"internal/x/go.work",         // was matched by bare `go.work`
		"internal/x/profile.cov",     // was matched by bare `profile.cov`
		"docs/release-progress/n.md", // was matched by bare `release-progress/`
		"cmd/server/handlers.go",     // the original defect: bare `server`
	}
	for _, path := range mustBeVisible {
		ignored, by := gitIgnores(t, root, path)
		require.False(t, ignored,
			"%s would be silently ignored by .gitignore rule %q — anchor that pattern with a leading slash", path, by)
	}

	// The anchoring must not have weakened anything: these still must be ignored.
	mustStayIgnored := []string{
		"data/rsa.pem",                    // the root key-persistence dir
		"cmd/server/data/rsa.pem",         // named explicitly on its own line
		"bin/azimuthal",                   // the root build output
		"coverage.out",                    // root coverage artifact
		"server",                          // the root build output that started this
		"web/node_modules/react/index.js", // unchanged
		"internal/core/api/handler.exe",   // genuinely global extension
		".env",                            // a secret at the root
		"web/.env",                        // and at depth — deliberately still global
	}
	for _, path := range mustStayIgnored {
		ignored, _ := gitIgnores(t, root, path)
		require.True(t, ignored, "%s must still be ignored — the anchoring pass went too far", path)
	}
}

// requireGitRepo fails loudly rather than skipping. Every environment this
// suite runs in is a git checkout: CI uses actions/checkout, and a developer
// runs it from a clone or a worktree.
func requireGitRepo(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is not on PATH; this test asks git what .gitignore means: %v", err)
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--git-dir")
	require.NoError(t, cmd.Run(), "%s is not a git checkout; this test asks git what .gitignore means", root)
}

// gitIgnores asks git whether path would be ignored, and by which rule.
//
// --no-index is the point: none of these paths exist, and the question is what
// git would do if a change introduced them. check-ignore exits 0 when a pattern
// matches and 1 when none does; anything else is a real error.
func gitIgnores(t *testing.T, root, path string) (ignored bool, rule string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "check-ignore", "-v", "--no-index", path)
	out, err := cmd.Output()
	if err == nil {
		return true, strings.TrimSpace(string(out))
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, ""
	}
	t.Fatalf("git check-ignore %s failed: %v", path, err)
	return false, ""
}
