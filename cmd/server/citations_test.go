package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This is TestDockerfiles_CitedGuardsExist generalised from Dockerfile comments
// to Go source. That guard scans the two Dockerfiles for TestDockerfiles_* names
// and fails when one names a test that does not exist; it deliberately looks at
// nothing else, so the citation-honesty rule (CLAUDE.md §6 / D148 — "a citation
// is only worth making if something fails when it stops being true") had no
// teeth over Go source at all.
//
// It had teeth to grow into: the cmd/server command files cite their guards as
// reassurance — `cmd.SilenceUsage = true // ... see TestCommands_SilenceUsageOnRuntimeFailure`
// appears in eight RunEs, restore.go's doc comment names two more, and backup.go,
// config.go, main.go and assess.go each cite the test that pins their behaviour.
// Those citations were unverified: rename the test and the comment silently
// becomes a lie. This walks the package's non-test source and fails when a cited
// Test* name resolves to no such function.
//
// The module path is needed to normalise a fully-qualified cross-package
// citation (github.com/Azimuthal-HQ/azimuthal/internal/assess.TestX) back to the
// repo-relative directory it names.
const modulePath = "github.com/Azimuthal-HQ/azimuthal"

// citedTestPattern captures a Go test-function name cited in a comment, with an
// optional leading import-path qualifier:
//
//	"TestCommands_SilenceUsageOnRuntimeFailure"    -> qualifier "",               name "TestCommands_..."
//	"internal/assess.TestNoDatabaseReachability"   -> qualifier "internal/assess", name "TestNoDatabase..."
//
// Two deliberate choices in this pattern:
//
//   - The name is `Test[A-Z_]…`, not `Test\w+`. Go test functions are `Test`
//     followed by an uppercase letter or underscore, so this matches
//     TestCommands_… while an English word like "Testing" or "Tests" — which a
//     comment may legitimately contain — is not mistaken for a citation.
//   - The qualifier alternative REQUIRES a slash. A citation that names another
//     package always spells the import path with one (internal/assess.Test…); a
//     slashless "word.TestX" is far more likely a sentence boundary than a
//     package reference, so it is treated as an unqualified, locally-resolved
//     citation — which is correct for this package, whose citations are either
//     local or slash-qualified.
var citedTestPattern = regexp.MustCompile(`(?:([\w.-]+(?:/[\w.-]+)+)\.)?(Test[A-Z_][A-Za-z0-9_]*)`)

type citation struct {
	file      string
	qualifier string // "" for a local citation; a repo-relative import path otherwise
	name      string
}

// TestSource_CitedTestsResolve fails when a comment in this package's non-test
// source cites a Test* name that resolves to no such test — locally, or in the
// package the citation qualifies.
//
// Fails-before/passes-after is built in as a subtest: the resolver is exercised
// against a name that cannot exist and must return false, so the assertions
// below cannot pass vacuously. Deleting every citation is caught too, by the
// total==0 guard at the end — the same shape as TestDockerfiles_CitedGuardsExist.
func TestSource_CitedTestsResolve(t *testing.T) {
	root := repoRootFromHere(t)

	local := testFuncsInDir(t, ".") // cmd/server itself, _test.go included
	crossPkg := map[string]map[string]bool{}

	// resolve reports whether a cited (qualifier, name) names a real test.
	resolve := func(qualifier, name string) bool {
		if qualifier == "" {
			return local[name]
		}
		rel := strings.TrimPrefix(qualifier, modulePath+"/")
		set, ok := crossPkg[rel]
		if !ok {
			set = testFuncsInDir(t, filepath.Join(root, filepath.FromSlash(rel)))
			crossPkg[rel] = set
		}
		return set[name]
	}

	// The resolver must be able to say "no", or every assertion below asserts
	// nothing. Both arms: a local miss and a cross-package miss against a real
	// package (internal/assess exists; this test name does not).
	t.Run("resolver rejects a nonexistent citation", func(t *testing.T) {
		if resolve("", "TestSurelyNoSuchLocalTestExistsZZZ") {
			t.Fatal("resolver claims a nonexistent local test exists — the guard would be vacuous")
		}
		if resolve("internal/assess", "TestSurelyNoSuchTestExistsZZZ") {
			t.Fatal("resolver claims a nonexistent cross-package test exists — the guard would be vacuous")
		}
	})

	total := 0
	for _, c := range citationsInPackageSource(t, ".") {
		total++
		if resolve(c.qualifier, c.name) {
			continue
		}
		where := "this package"
		prefix := ""
		if c.qualifier != "" {
			where = c.qualifier
			prefix = c.qualifier + "."
		}
		t.Errorf("%s cites %s%s, which is not a test in %s. Either the test was renamed and "+
			"the comment was not, or the comment describes a test nobody wrote — both leave the "+
			"source asserting something no test enforces (CLAUDE.md §6/D148).",
			c.file, prefix, c.name, where)
	}

	if total == 0 {
		t.Error("no Test* citations found in this package's non-test source. The command files " +
			"cite their guards (e.g. TestCommands_SilenceUsageOnRuntimeFailure) as reassurance; " +
			"if those citations were removed, restore them — a guard nobody points at rots unseen.")
	}
}

// citationsInPackageSource extracts every Test* citation from the comments of
// dir's non-test .go files.
//
// Comments only, and non-test files only. Scanning comments (never code) is what
// distinguishes a citation from a definition or a call; skipping _test.go keeps
// the guard pointed at production source, where a stale citation misleads,
// rather than at the test files that legitimately name many tests.
func citationsInPackageSource(t *testing.T, dir string) []citation {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading package directory %s: %v", dir, err)
	}

	var out []citation
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, group := range file.Comments {
			for _, m := range citedTestPattern.FindAllStringSubmatch(group.Text(), -1) {
				out = append(out, citation{file: name, qualifier: m[1], name: m[2]})
			}
		}
	}
	return out
}

// testFuncsInDir returns the set of top-level Test* function names defined in
// dir's .go files (test files included — that is where tests live).
//
// A missing directory yields an empty set rather than a fatal error, so a
// citation that names a package which does not exist is reported as an
// unresolved citation by the caller — which is exactly what it is — instead of
// crashing the guard.
func testFuncsInDir(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]bool{}
	}

	found := map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", filepath.Join(dir, name), err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if n := fn.Name.Name; strings.HasPrefix(n, "Test") {
				found[n] = true
			}
		}
	}
	return found
}

// repoRootFromHere walks up from the test's working directory to the module
// root (the directory holding go.mod). cmd/server's tests run with their cwd at
// the package directory, so cross-package citations resolve relative to this.
func repoRootFromHere(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for range 12 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate the module root (go.mod) above the test's working directory")
	return ""
}
