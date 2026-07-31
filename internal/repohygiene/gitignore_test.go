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

// trackedGitignores returns every .gitignore git knows about, repo-relative
// and slash-separated.
//
// Every one of them, not just the root: git applies each file's patterns
// relative to its own directory, so a bare pattern in web/.gitignore has
// exactly the same reach-at-any-depth behaviour within web/ that a bare
// pattern in the root file has across the repository.
func trackedGitignores(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "--full-name", "*.gitignore", ".gitignore")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err, "listing tracked .gitignore files")

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	require.NotEmpty(t, files, "the repository must have at least a root .gitignore")
	return files
}

type gitignoreLine struct {
	Number  int
	Pattern string
}

// gitignorePatterns returns every non-blank, non-comment line of one
// .gitignore, in file order, paired with its 1-based line number.
func gitignorePatterns(t *testing.T, root, relPath string) []gitignoreLine {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	require.NoError(t, err, "reading %s", relPath)

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

// isBare reports whether git will match this pattern at EVERY directory depth
// below the .gitignore that declares it, rather than only against that file's
// own directory.
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

// reviewedBarePatterns is the allowlist, per .gitignore: every slash-less
// pattern that is deliberately global, with the reason it is.
//
// This is a ledger of decisions, not a ledger of exemptions — each entry names
// a mechanism that genuinely writes files at arbitrary depth. If you are adding
// a row here to make the suite pass, the pattern almost certainly wants a
// leading slash instead. See the header comment in .gitignore.
var reviewedBarePatterns = map[string]map[string]string{
	".gitignore": {
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
	},
	"web/.gitignore": {
		"*.log":            "a log is disposable wherever it lands",
		"npm-debug.log*":   "npm writes it into whichever directory it failed in",
		"yarn-debug.log*":  "as above, yarn",
		"yarn-error.log*":  "as above, yarn",
		"pnpm-debug.log*":  "as above, pnpm",
		"lerna-debug.log*": "as above, lerna",

		"node_modules": "nested node_modules are real and all of them are disposable",
		"*.local":      "Vite's convention for local-only env files, at any depth",

		".idea":     "JetBrains project dir; can appear beside any sub-project",
		".DS_Store": "macOS writes one into every directory it browses",
		"*.suo":     "Visual Studio user options, any depth",
		"*.ntvs*":   "Visual Studio Node tools, any depth",
		"*.njsproj": "Visual Studio Node project, any depth",
		"*.sln":     "Visual Studio solution, any depth",
		"*.sw?":     "vim swap files, written beside whatever is being edited",
	},
}

// TestGitignore_NoUnreviewedBarePatterns fails when a slash-less pattern
// appears in any tracked .gitignore without a reviewed reason.
//
// The defect this guards is the `server` trap: a bare `server` pattern matched
// `cmd/server/`, so every new file added under that directory was skipped
// silently by `git add -A`. Nothing announced it — `git status` does not report
// paths it has been told to ignore — and the files were simply missing from the
// commit. The same shape was live in eleven other patterns when this test was
// written: `data/` matched `web/src/data/`, `coverage.*` matched a
// `docs/coverage.md` that could have been added at any time, plus `bin/`,
// `go.work`, `go.work.sum`, `profile.cov` and the three progress directories in
// the root file, and `logs` and `dist-ssr` in web/.gitignore.
//
// Deleting this test's assertion would let the next one through unnoticed,
// which is the whole failure mode.
func TestGitignore_NoUnreviewedBarePatterns(t *testing.T) {
	root := repoRoot(t)
	requireGitRepo(t, root)

	for _, file := range trackedGitignores(t, root) {
		t.Run(file, func(t *testing.T) {
			reviewed, ok := reviewedBarePatterns[file]
			require.True(t, ok,
				"%s is a tracked .gitignore with no entry in reviewedBarePatterns. "+
					"Add one (an empty map is fine if it has no bare patterns) so a new "+
					"file cannot arrive with an unreviewed bare pattern in it.", file)

			var unreviewed []string
			seen := map[string]bool{}
			for _, line := range gitignorePatterns(t, root, file) {
				if !isBare(line.Pattern) {
					continue
				}
				seen[line.Pattern] = true
				if _, allowed := reviewed[line.Pattern]; !allowed {
					unreviewed = append(unreviewed,
						line.Pattern+" ("+file+":"+strconv.Itoa(line.Number)+")")
				}
			}

			require.Empty(t, unreviewed,
				"these patterns have no slash, so git matches them at EVERY depth below %s, "+
					"not just its own directory:\n  %s\n\n"+
					"If the pattern means one path, give it a leading slash (`/data/`, not `data/`). "+
					"If it is genuinely global, add it to reviewedBarePatterns with the reason. "+
					"See the header comment in .gitignore.",
				file, strings.Join(unreviewed, "\n  "))

			// The ledger must not outlive its entries either: a stale row would
			// quietly re-permit a pattern someone had deliberately anchored.
			var stale []string
			for pattern := range reviewed {
				if !seen[pattern] {
					stale = append(stale, pattern)
				}
			}
			require.Empty(t, stale,
				"reviewedBarePatterns[%q] lists patterns no longer in that file: %v — remove them",
				file, stale)
		})
	}
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
		"web/src/logs/index.ts",      // was matched by bare `logs` in web/.gitignore
		"web/src/dist-ssr/entry.ts",  // was matched by bare `dist-ssr`
	}
	for _, path := range mustBeVisible {
		ignored, by := gitIgnores(t, root, path)
		require.False(t, ignored,
			"%s would be silently ignored by rule %q — anchor that pattern with a leading slash", path, by)
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
		"web/dist/index.html",             // the built bundle, via web/.gitignore
		"web/logs/vite.log",               // web/.gitignore's own directory
		"web/dist-ssr/entry.js",           // likewise
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
	// cmd.Dir rather than `-C root`, so this invocation has no variable
	// argument at all and needs no gosec exemption.
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = root
	require.NoError(t, cmd.Run(), "%s is not a git checkout; this test asks git what .gitignore means", root)
}

// gitIgnores asks git whether path would be ignored, and by which rule.
//
// --no-index is the point: none of these paths exist, and the question is what
// git would do if a change introduced them. check-ignore exits 0 when a pattern
// matches and 1 when none does; anything else is a real error.
func gitIgnores(t *testing.T, root, path string) (ignored bool, rule string) {
	t.Helper()
	// #nosec G204 -- "git" is constant and path is a string literal from the
	// tables in this file; nothing here reaches outside the test. Same idiom and
	// same rule as cmd/server/backup.go.
	cmd := exec.Command("git", "check-ignore", "-v", "--no-index", path)
	cmd.Dir = root
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
