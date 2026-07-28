package testutil

// Tests for the template-database machinery behind NewTestDB.
//
// This file is in `package testutil` rather than `package testutil_test`
// because the mechanism it proves — fingerprinting, template construction,
// name layout — is deliberately unexported. The behaviour tests for NewTestDB
// itself live in db_test.go, from outside.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/migrations"
)

// probeMigration is a real goose migration added to a copy of the migration
// set to stand in for "somebody added migration 038".
const probeMigrationName = "999_template_invalidation_probe.sql"

const probeMigrationBody = `-- +goose Up
CREATE TABLE template_invalidation_probe (id integer PRIMARY KEY);

-- +goose Down
DROP TABLE template_invalidation_probe;
`

// migrationsPlus returns the real migration set with extra files layered on
// top, as an fs.FS goose can read.
func migrationsPlus(t *testing.T, extra map[string]string) fstest.MapFS {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)

	out := fstest.MapFS{}
	for _, name := range names {
		content, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		out[name] = &fstest.MapFile{Data: content}
	}
	for name, body := range extra {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

// TestTemplateInvalidation_NewMigrationRebuildsTheTemplate is the guarantee
// the whole design rests on: a migration set that has changed can never be
// served from the template built for the old one.
//
// It proves it in both directions, because only one direction is a test. A
// template that contains the new migration would also "pass" a check that only
// looked at the new one — so the run against the unmodified set has to prove
// the probe table is absent there.
//
// Deleting the fingerprint's dependence on file content (say, hashing only
// filenames) fails this test: the edited-content case below would then reuse
// the old template.
func TestTemplateInvalidation_NewMigrationRebuildsTheTemplate(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test. Run 'make test-db-up' first.")
	}
	ctx := context.Background()

	h, err := sharedHarness(dsn)
	require.NoError(t, err)

	// 1. The real set, and the same set with one migration added.
	realFP, err := fingerprintMigrations(migrations.FS)
	require.NoError(t, err)

	probeFS := migrationsPlus(t, map[string]string{probeMigrationName: probeMigrationBody})
	probeFP, err := fingerprintMigrations(probeFS)
	require.NoError(t, err)

	require.NotEqual(t, realFP, probeFP,
		"adding a migration must change the fingerprint — otherwise the stale template is reused")

	// 2. Building against the doctored set produces a DIFFERENT template, and
	//    that template really has the new migration applied.
	probeTemplate, err := ensureTemplate(ctx, h.admin, dsn, probeFS)
	require.NoError(t, err)
	t.Cleanup(func() { dropQuietly(h.admin, probeTemplate) })

	require.NotEqual(t, h.template, probeTemplate,
		"a changed migration set must produce a template under a different name")

	require.True(t, hasProbeTable(t, h, dsn, probeTemplate),
		"the rebuilt template must contain the added migration — a stale template was served")

	// 3. The direction that makes this a test: the template for the UNMODIFIED
	//    set must not have the probe table. Both templates now exist at once, so
	//    this also proves the two are not the same database wearing two names.
	require.False(t, hasProbeTable(t, h, dsn, h.template),
		"the template for the real migration set must not contain the probe migration")

	// 4. And the database NewTestDB actually hands out came from the real one.
	tdb := NewTestDB(t)
	var exists bool
	require.NoError(t, tdb.Pool.QueryRow(ctx, `SELECT to_regclass('public.template_invalidation_probe') IS NOT NULL`).Scan(&exists))
	require.False(t, exists, "NewTestDB must clone the template for the committed migration set")
}

// hasProbeTable clones the named template and reports whether the clone
// contains the probe table. It clones rather than connecting to the template
// directly because a finished template refuses connections outright.
func hasProbeTable(t *testing.T, h *harness, dsn, template string) bool {
	t.Helper()
	ctx := context.Background()

	name := h.newTestDBName("probe_" + template)
	_, err := h.admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q TEMPLATE %q", name, template))
	require.NoError(t, err)
	defer h.dropDatabase(name)

	cloneDSN, err := withDatabase(dsn, name)
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, cloneDSN)
	require.NoError(t, err)
	defer pool.Close()

	var found bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT to_regclass('public.template_invalidation_probe') IS NOT NULL`).Scan(&found))
	return found
}

// TestTemplateBuild_SurvivesTheGooseGlobalsProductionMigrateLeavesBehind
// covers a landmine, not a feature.
//
// internal/db.Migrate calls goose.SetBaseFS(migrations.FS) and never restores
// it, and goose keeps the base FS, the dialect and the version-table name in
// package globals. Any test binary that runs a production migration before a
// template is built therefore hands goose's globals to us pre-set. The old
// NewTestDB passed goose an absolute OS directory, which is not a valid path
// inside an embed.FS, so that ordering would have failed outright — it never
// did only because in cmd/server the files happen to sort so that every
// NewTestDB runs before the first db.Migrate. A new test file named earlier in
// the alphabet was all it would have taken.
//
// The template build now sets all three globals itself rather than inheriting
// them. Remove any of those lines from migrateInto and this fails.
func TestTemplateBuild_SurvivesTheGooseGlobalsProductionMigrateLeavesBehind(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test. Run 'make test-db-up' first.")
	}
	ctx := context.Background()

	h, err := sharedHarness(dsn)
	require.NoError(t, err)

	// Run a real production migration first, against a scratch database of its
	// own so nothing else in the run sees the side effects. What matters is not
	// the database — it is the state db.Migrate leaves behind in goose.
	scratch := h.newTestDBName("goose_global_poisoner")
	_, err = h.admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", scratch))
	require.NoError(t, err)
	defer h.dropDatabase(scratch)

	scratchDSN, err := withDatabase(dsn, scratch)
	require.NoError(t, err)
	scratchPool, err := pgxpool.New(ctx, scratchDSN)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, scratchPool), "production migrate against a scratch database")
	scratchPool.Close()

	// goose's globals now point at the production embedded FS. Building a
	// template must not care.
	probeFS := migrationsPlus(t, map[string]string{
		"997_goose_global_probe.sql": `-- +goose Up
CREATE TABLE template_invalidation_probe (id integer PRIMARY KEY);

-- +goose Down
DROP TABLE template_invalidation_probe;
`,
	})
	built, err := ensureTemplate(ctx, h.admin, dsn, probeFS)
	require.NoError(t, err, "the template build inherited goose's globals instead of setting its own")
	t.Cleanup(func() { dropQuietly(h.admin, built) })

	require.True(t, hasProbeTable(t, h, dsn, built),
		"a template built after a production migrate must still have every migration applied")
}

// TestFingerprint_RespondsToEveryKindOfChange pins down what the fingerprint
// is sensitive to. Each case corresponds to a way the migration set can change
// in this repository, and each would silently reuse a stale template if the
// fingerprint ignored it.
func TestFingerprint_RespondsToEveryKindOfChange(t *testing.T) {
	base := migrationsPlus(t, nil)
	baseFP, err := fingerprintMigrations(base)
	require.NoError(t, err)

	t.Run("identical input hashes identically", func(t *testing.T) {
		again, err := fingerprintMigrations(migrationsPlus(t, nil))
		require.NoError(t, err)
		require.Equal(t, baseFP, again, "the fingerprint must be deterministic or every run rebuilds")
	})

	t.Run("added migration", func(t *testing.T) {
		fp, err := fingerprintMigrations(migrationsPlus(t,
			map[string]string{probeMigrationName: probeMigrationBody}))
		require.NoError(t, err)
		require.NotEqual(t, baseFP, fp)
	})

	t.Run("edited migration content", func(t *testing.T) {
		edited := migrationsPlus(t, nil)
		// Edit the newest migration in place, the way a fix-up would.
		newest := newestName(t, edited)
		edited[newest] = &fstest.MapFile{Data: append(edited[newest].Data, []byte("\n-- touched\n")...)}
		fp, err := fingerprintMigrations(edited)
		require.NoError(t, err)
		require.NotEqual(t, baseFP, fp, "editing a migration's body must change the fingerprint")
	})

	t.Run("renamed migration", func(t *testing.T) {
		renamed := migrationsPlus(t, nil)
		newest := newestName(t, renamed)
		body := renamed[newest]
		delete(renamed, newest)
		renamed["998_renamed.sql"] = body
		fp, err := fingerprintMigrations(renamed)
		require.NoError(t, err)
		require.NotEqual(t, baseFP, fp, "renaming a migration must change the fingerprint")
	})

	t.Run("removed migration", func(t *testing.T) {
		reduced := migrationsPlus(t, nil)
		delete(reduced, newestName(t, reduced))
		fp, err := fingerprintMigrations(reduced)
		require.NoError(t, err)
		require.NotEqual(t, baseFP, fp, "removing a migration must change the fingerprint")
	})

	t.Run("empty set is an error, never an empty fingerprint", func(t *testing.T) {
		_, err := fingerprintMigrations(fstest.MapFS{})
		require.Error(t, err, "an empty migration set must fail loudly, not build an empty template")
	})
}

func newestName(t *testing.T, m fstest.MapFS) string {
	t.Helper()
	names, err := fs.Glob(m, "*.sql")
	require.NoError(t, err)
	require.NotEmpty(t, names)
	newest := names[0]
	for _, n := range names {
		if n > newest {
			newest = n
		}
	}
	return newest
}

// TestNewTestDBName_StaysWithinPostgresIdentifierLimit guards the one silent
// failure this naming scheme can have: postgres truncates identifiers past 63
// bytes without complaint, so two long test names could collapse onto the same
// database and share state. Go test names get long — table-driven subtests
// nest three deep routinely.
func TestNewTestDBName_StaysWithinPostgresIdentifierLimit(t *testing.T) {
	h := &harness{runTag: runTag()}

	long := "TestSomeVeryLongPackageLevelName/with_a_subtest/and_another_one_nested_deeper_still"
	name := h.newTestDBName(long)
	require.LessOrEqual(t, len(name), maxIdentifier)
	require.True(t, strings.HasPrefix(name, testDBPrefix))

	// The run tag survives truncation, which is what sweepAbandoned needs.
	_, ok := parseRunStart(name)
	require.True(t, ok, "a truncated name must still carry a parseable run timestamp")

	// Consecutive names differ even when the test name is identical, so a test
	// calling NewTestDB twice gets two databases rather than one collision.
	require.NotEqual(t, h.newTestDBName(long), h.newTestDBName(long))
}

// TestParseRunStart_OnlyClaimsNamesItRecognises matters because sweepAbandoned
// DROPs whatever this function dates. A false positive on an unrelated
// database name would delete somebody's data.
func TestParseRunStart_OnlyClaimsNamesItRecognises(t *testing.T) {
	h := &harness{runTag: runTag()}
	name := h.newTestDBName("TestRoundTrip")
	got, ok := parseRunStart(name)
	require.True(t, ok)
	require.WithinDuration(t, time.Now(), got, time.Minute)

	for _, bad := range []string{
		"azimuthal_test",                  // the maintenance database
		"azimuthal_tmpl_deadbeefdeadbeef", // a template
		"postgres",
		"azt_",             // prefix with nothing after it
		"azt_notbase36!_1", // unparseable tag
		"aztsomething",     // prefix-like but not the prefix
	} {
		_, ok := parseRunStart(bad)
		require.Falsef(t, ok, "parseRunStart must not claim %q — sweepAbandoned would drop it", bad)
	}
}

// TestWithDatabase_PreservesEverythingButTheDatabase pins the DSN rewrite.
// Losing sslmode or the password here would fail in CI only, and confusingly.
func TestWithDatabase_PreservesEverythingButTheDatabase(t *testing.T) {
	got, err := withDatabase(
		"postgres://azimuthal_test:testpassword@localhost:5433/azimuthal_test?sslmode=disable&application_name=x",
		"azt_abc")
	require.NoError(t, err)
	require.Equal(t,
		"postgres://azimuthal_test:testpassword@localhost:5433/azt_abc?sslmode=disable&application_name=x",
		got)

	got, err = withDatabase("postgresql://u@h/db", "other")
	require.NoError(t, err)
	require.Equal(t, "postgresql://u@h/other", got)

	// A key=value DSN cannot be rewritten by this function, and quietly
	// returning the original would put every test back in the shared database.
	_, err = withDatabase("host=localhost user=azimuthal_test dbname=azimuthal_test", "azt_abc")
	require.Error(t, err)

	// Whatever the error says, it must not say the password.
	_, err = withDatabase("mysql://user:hunter2@localhost/db", "azt_abc")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hunter2")
}
