package assess

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// modulePath is this module's import prefix.
const modulePath = "github.com/Azimuthal-HQ/azimuthal"

// forbiddenImports are the packages that would give the assessor a way to reach
// a database, directly or by acquiring a connection string.
//
// The entries are matched as prefixes, so a subpackage is covered too.
var forbiddenImports = []string{
	"database/sql",
	"github.com/jackc/pgx",
	"github.com/riverqueue/river",
	"github.com/minio/minio-go",
	modulePath + "/internal/db",
	modulePath + "/internal/config",
	modulePath + "/internal/testutil",
}

// TestNoDatabaseReachability is the structural guarantee behind the claim the
// report prints at the top of every run: nothing was written, and no database
// was contacted.
//
// A comment saying so would be worth nothing — the whole value of this tool is
// that a self-hoster can point it at an export before deciding whether to trust
// Azimuthal with their data, and "trust me" is exactly what it must not require.
// So the guarantee is checked instead: the assessor's own packages and every
// module-internal package they reach are walked, and any import that could
// open a connection or supply a DSN fails the test by name.
//
// It fails in both directions in the sense that matters: adding a pgx import
// anywhere in the reachable set breaks it, and so does routing round the check
// by importing internal/config to read DATABASE_URL "just for a default".
func TestNoDatabaseReachability(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	roots := []string{
		modulePath + "/internal/assess",
		modulePath + "/internal/assess/archive",
		modulePath + "/internal/assess/jira",
		modulePath + "/internal/assess/confluence",
		modulePath + "/internal/assess/jql",
	}

	seen := map[string]bool{}
	var offences []string
	examined := 0
	for _, r := range roots {
		walkImports(t, root, r, seen, &offences, &examined)
	}

	require.Empty(t, offences,
		"the assessor must not be able to reach a database; found:\n  %s",
		strings.Join(offences, "\n  "))

	// The walk must actually have inspected something, or an empty offence list
	// would read as a guarantee when it is really a no-op.
	require.Greater(t, examined, 20,
		"the import walk inspected only %d imports; it is not checking anything", examined)
	require.Len(t, seen, len(roots))

	// The walk visiting exactly its roots is itself the finding, and a stronger
	// one than the absence of a driver: outside its own subpackages, the
	// assessor's production code imports nothing from this module at all. It
	// cannot reach a database because it cannot reach anything.
	//
	// The mapping tables that describe Azimuthal's model are therefore stated
	// in this package and checked against the real schema and the real Codex
	// vocabulary by mapping_test.go, where the dependency costs nothing.
	for pkg := range seen {
		require.True(t, strings.HasPrefix(pkg, modulePath+"/internal/assess"),
			"the assessor reached %s; production code here should depend on nothing outside internal/assess", pkg)
	}
}

// walkImports visits pkg and every module-internal package it imports,
// recording forbidden imports as offences.
func walkImports(t *testing.T, root, pkg string, seen map[string]bool, offences *[]string, examined *int) {
	t.Helper()
	if seen[pkg] {
		return
	}
	seen[pkg] = true

	dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath+"/")))
	for _, imp := range packageImports(t, dir) {
		*examined++
		if bad := forbidden(imp); bad != "" {
			*offences = append(*offences, pkg+" imports "+imp+" (matches "+bad+")")
			continue
		}
		if strings.HasPrefix(imp, modulePath+"/") {
			walkImports(t, root, imp, seen, offences, examined)
		}
	}
}

// packageImports parses every non-test Go file in dir and returns its imports.
//
// Test files are deliberately excluded: this test itself lives in the package
// and reaching into the repository from a test is not a production dependency.
func packageImports(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "cannot read package directory %s", dir)

	set := map[string]struct{}{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		require.NoError(t, err, "parsing %s", name)
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			set[path] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func forbidden(imp string) string {
	for _, bad := range forbiddenImports {
		if imp == bad || strings.HasPrefix(imp, bad+"/") {
			return bad
		}
	}
	return ""
}

// TestForbiddenImports_WouldActuallyBeDetected mutation-tests the check above.
//
// A reachability test that cannot fail is worse than none, because it reads as
// a guarantee. This asserts the matcher recognises the imports it is supposed
// to, including the subpackage forms an offending change would realistically
// take.
func TestForbiddenImports_WouldActuallyBeDetected(t *testing.T) {
	t.Parallel()

	for _, imp := range []string{
		"database/sql",
		"github.com/jackc/pgx/v5",
		"github.com/jackc/pgx/v5/pgxpool",
		"github.com/Azimuthal-HQ/azimuthal/internal/db",
		"github.com/Azimuthal-HQ/azimuthal/internal/db/generated",
		"github.com/Azimuthal-HQ/azimuthal/internal/config",
		"github.com/Azimuthal-HQ/azimuthal/internal/testutil",
	} {
		require.NotEmpty(t, forbidden(imp), "import %q must be recognised as forbidden", imp)
	}

	// And the imports the assessor legitimately uses must not trip it.
	for _, imp := range []string{
		"encoding/xml",
		"archive/zip",
		"github.com/Azimuthal-HQ/azimuthal/internal/core/views",
		"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc",
		"github.com/Azimuthal-HQ/azimuthal/internal/assess/jira",
	} {
		require.Empty(t, forbidden(imp), "import %q must be allowed", imp)
	}
}

// TestInput_HasNoConnectionField — the assessor's entry point must not offer a
// place to put a DSN, so a caller cannot pass one even by mistake.
func TestInput_HasNoConnectionField(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "assess", "run.go"))
	require.NoError(t, err)
	src := strings.ToLower(string(b))

	// The doc comment on Input explains the absence, so the words appear; the
	// check is that no struct field declares one.
	for _, banned := range []string{"dsn string", "databaseurl string", "connstring string", "password string"} {
		require.NotContains(t, src, banned, "Input must not carry a connection field")
	}
}
