package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Audit ref: testing-audit.md §7.4 — backup.go and restore.go had no test
// coverage anywhere in the repo. This file exercises the full backup/restore
// chain: archive round-trip, manifest validation, the storage-path helper,
// and an end-to-end pg_dump → psql round-trip across a fresh source DB and a
// fresh target DB. The earlier PR (#40) skipped the postgres round-trip with
// an architectural rationale that was wrong on closer reading: the
// dumpPostgres / restorePostgres helpers take a databaseURL parameter
// directly, so a true round-trip can be written without modifying backup.go,
// as long as we use a dedicated test database (not just a schema) so
// pg_dump's whole-database scope does not leak into other parallel tests.

// TestBackup_TarArchiveRoundTrip writes a manifest and a fake database
// dump into a tar.gz archive using the same writer used by runBackup,
// then reads it back with readArchive and checks the entries match.
func TestBackup_TarArchiveRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")

	manifest := backupManifest{
		AzimuthalVersion: "test",
		BackupTimestamp:  time.Now().UTC().Truncate(time.Second),
		PostgresVersion:  "PostgreSQL 16",
		Files:            []string{"database.sql", "storage/avatar.png"},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)

	dumpBytes := []byte("-- fake pg_dump output\nSELECT 1;\n")
	avatarBytes := []byte("\x89PNG\r\n\x1a\nfake-image-bytes")

	out, err := os.Create(archivePath) //nolint:gosec // G304 — archivePath is a t.TempDir() path
	require.NoError(t, err)
	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)

	require.NoError(t, addToTar(tw, "database.sql", dumpBytes))
	require.NoError(t, addToTar(tw, "storage/avatar.png", avatarBytes))
	require.NoError(t, addToTar(tw, "manifest.json", manifestJSON))
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, out.Close())

	entries, err := readArchive(archivePath)
	require.NoError(t, err)
	require.Equal(t, dumpBytes, entries["database.sql"], "database dump must round-trip")
	require.Equal(t, avatarBytes, entries["storage/avatar.png"], "storage object must round-trip")
	require.Contains(t, entries, "manifest.json")

	got, err := validateManifest(entries)
	require.NoError(t, err, "manifest must validate against archive contents")
	require.Equal(t, manifest.AzimuthalVersion, got.AzimuthalVersion)
	require.Equal(t, manifest.PostgresVersion, got.PostgresVersion)
	require.Equal(t, manifest.Files, got.Files)
}

// TestRestore_ManifestRejectsMissingFiles verifies validateManifest catches
// a corrupt archive where the manifest references a file not present.
func TestRestore_ManifestRejectsMissingFiles(t *testing.T) {
	manifest := backupManifest{
		AzimuthalVersion: "test",
		BackupTimestamp:  time.Now().UTC(),
		Files:            []string{"database.sql", "storage/missing.png"},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	entries := map[string][]byte{
		"manifest.json": manifestJSON,
		"database.sql":  []byte("-- dump"),
		// storage/missing.png deliberately absent
	}

	_, err = validateManifest(entries)
	require.Error(t, err, "validateManifest must fail when a manifested file is missing")
	require.Contains(t, err.Error(), "manifest references")
}

// TestRestore_ManifestRequiresManifestFile verifies validateManifest fails
// when manifest.json is missing entirely.
func TestRestore_ManifestRequiresManifestFile(t *testing.T) {
	entries := map[string][]byte{
		"database.sql": []byte("-- dump"),
	}

	_, err := validateManifest(entries)
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifest.json not found")
}

// TestStripStoragePrefix_RoundTrip verifies the storage prefix helper used
// by restoreObjectStorage to map archive entries back to bucket keys.
func TestStripStoragePrefix_RoundTrip(t *testing.T) {
	cases := []struct {
		archivePath string
		want        string
	}{
		{"storage/avatar.png", "avatar.png"},
		{"storage/users/123/profile.jpg", "users/123/profile.jpg"},
		{"database.sql", "database.sql"},
		{"manifest.json", "manifest.json"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, stripStoragePrefix(c.archivePath), "archive path %q", c.archivePath)
	}
}

// TestBackupRestore_FixturesAreReadable verifies the fixture entities the
// round-trip test populates are themselves readable from a fresh schema.
// This anchors the round-trip test so a regression in the fixtures shows up
// here first instead of as a confusing pg_dump failure.
func TestBackupRestore_FixturesAreReadable(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	queries := generated.New(tdb.Pool)
	ctx := context.Background()

	org := testutil.CreateTestOrg(t, tdb.Pool)
	user := testutil.CreateTestUser(t, tdb.Pool, org.ID)
	space := testutil.CreateTestSpace(t, tdb.Pool, org.ID, user.ID, "beacon")

	gotOrg, err := queries.GetOrganizationByID(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, org.Slug, gotOrg.Slug)

	gotUser, err := queries.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.Email, gotUser.Email)

	gotSpace, err := queries.GetSpaceByID(ctx, space.ID)
	require.NoError(t, err)
	require.Equal(t, space.Slug, gotSpace.Slug)

	// Sanity: the membership that CreateTestUser added is reachable.
	membership, err := queries.GetMembership(ctx, generated.GetMembershipParams{
		OrgID:  org.ID,
		UserID: user.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "owner", membership.Role)
}

// TestBackupRestore_PostgresRoundTrip is the full pg_dump → psql round-trip
// the audit (§7.4) asks for. It creates two fresh databases — a source the
// fixtures get inserted into and a target the dump is restored into — so
// pg_dump's whole-database scope does not collide with any other test's
// schema.
//
// Gated by requirePostgresClientTools, which skips locally and FAILS in CI —
// it carried its own inline copy of those three checks, which was both
// duplication and a second place for the CI hard-fail to rot away. Drives the
// chain end-to-end:
//
//  1. Create source DB, run migrations, insert one of each entity (org,
//     user, space, ticket-style item, page, comment).
//  2. Call dumpPostgres against the source DSN.
//  3. Create a fresh target DB, call restorePostgres against its DSN.
//  4. Re-query each entity from the target and assert non-timestamp
//     equality.
func TestBackupRestore_PostgresRoundTrip(t *testing.T) {
	dsn := requirePostgresClientTools(t)

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "connecting with admin DSN")
	defer adminPool.Close()

	// Two fresh databases, so pg_dump's whole-database scope cannot collide
	// with any other test's schema.
	srcDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_brt_src_")
	dstDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_brt_dst_")

	// 1. Migrate the source DB and seed one of each entity.
	srcPool, err := pgxpool.New(ctx, srcDSN)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, srcPool), "migrating source DB")

	srcQueries := generated.New(srcPool)
	seed := seedRoundTripFixtures(ctx, t, srcQueries)
	srcPool.Close()

	// 2. Dump the source DB.
	dumpBytes, _, err := dumpPostgres(srcDSN)
	require.NoError(t, err, "pg_dump against source DB")
	require.NotEmpty(t, dumpBytes, "dump must not be empty")

	// 3. Restore into a fresh target DB. The dump uses --clean --if-exists
	// so the empty target gets the full schema + data.
	require.NoError(t, restorePostgres(dstDSN, dumpBytes), "psql restore into target DB")

	// 4. Verify each entity in the target DB.
	dstPool, err := pgxpool.New(ctx, dstDSN)
	require.NoError(t, err)
	defer dstPool.Close()

	dstQueries := generated.New(dstPool)

	gotOrg, err := dstQueries.GetOrganizationByID(ctx, seed.org.ID)
	require.NoError(t, err, "org row must round-trip")
	require.Equal(t, seed.org.Slug, gotOrg.Slug)
	require.Equal(t, seed.org.Name, gotOrg.Name)

	gotUser, err := dstQueries.GetUserByID(ctx, seed.user.ID)
	require.NoError(t, err, "user row must round-trip")
	require.Equal(t, seed.user.Email, gotUser.Email)
	require.Equal(t, seed.user.DisplayName, gotUser.DisplayName)
	require.Equal(t, seed.user.OrgID, gotUser.OrgID)

	gotSpace, err := dstQueries.GetSpaceByID(ctx, seed.space.ID)
	require.NoError(t, err, "space row must round-trip")
	require.Equal(t, seed.space.Slug, gotSpace.Slug)
	require.Equal(t, seed.space.Name, gotSpace.Name)
	require.Equal(t, seed.space.Type, gotSpace.Type)

	gotMembership, err := dstQueries.GetMembership(ctx, generated.GetMembershipParams{
		OrgID:  seed.org.ID,
		UserID: seed.user.ID,
	})
	require.NoError(t, err, "membership row must round-trip")
	require.Equal(t, "owner", gotMembership.Role)

	gotItem, err := dstQueries.GetTicketByID(ctx, seed.item.ID)
	require.NoError(t, err, "item row must round-trip")
	require.Equal(t, seed.item.Title, gotItem.Title)
	require.Equal(t, seed.item.Status, gotItem.Status)
	require.Equal(t, seed.item.Priority, gotItem.Priority)

	gotPage, err := dstQueries.GetPageByID(ctx, seed.page.ID)
	require.NoError(t, err, "page row must round-trip")
	require.Equal(t, seed.page.Title, gotPage.Title)
	require.Equal(t, seed.page.Content, gotPage.Content)

	// Comment list scoped to the item — exercises both the comment row and
	// the polymorphic entity link back to the ticket.
	itemComments, err := dstQueries.ListCommentsByEntity(ctx, generated.ListCommentsByEntityParams{
		EntityType: "ticket",
		EntityID:   seed.item.ID,
		// The query now reconciles the commented-on entity against a space.
		// Omitting this leaves SpaceID at uuid.Nil and the EXISTS arm matches
		// nothing — a zero value rather than a compile error, which is why the
		// round trip reported an empty comment list instead of failing to build.
		SpaceID: seed.space.ID,
	})
	require.NoError(t, err, "item comments must round-trip")
	require.Len(t, itemComments, 1, "exactly one comment expected on the item")
	require.Equal(t, seed.comment.ID, itemComments[0].ID)
	require.Equal(t, seed.comment.Body, itemComments[0].Body)
}

// requirePostgresClientTools gates the tests that fork pg_dump/psql, and
// returns the admin DSN.
//
// **In CI an unmet precondition is fatal, not a skip.** Off CI it stays a skip,
// because a developer box legitimately may not have the client tools.
//
// The distinction matters more than it looks.
// TestRestorePostgres_PartialRestoreIsAFailure is the only gate anywhere on the
// more dangerous half of D105 — a restore that half-applies and reports
// success. CI's `test` job runs on ubuntu-latest and merely *relies on* the
// runner image happening to ship psql; nothing asserts it. If a future runner
// image drops it, every test here would skip, the fail-loud property would lose
// all coverage, and CI would still report every gate green. That is the exact
// shape of the defect this file exists to close — a gate that appears to cover
// something and does not — one level down.
//
// `CI` is the right variable precisely because it is not ours: GitHub Actions
// sets it on every runner, so the hard-fail cannot be lost by editing a
// workflow. Verified when this was written: exactly one `go test` invocation
// exists across .github/workflows, in the `test` job, and that job sets
// DATABASE_URL at job level — so none of the three conditions below can fire
// spuriously in CI today.
//
// Note what this does NOT cover: docker-e2e proves the happy path inside the
// shipped image, and now also proves a bad dump is refused there. These remain
// the unit-level half.
func requirePostgresClientTools(t *testing.T) string {
	t.Helper()

	// In CI every one of these is guaranteed, so treat an unmet precondition as
	// the infrastructure regression it is rather than quietly dropping coverage.
	unmet := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("CI") != "" {
			t.Fatalf("CI precondition not met: "+format+
				"\nThis is a hard failure in CI on purpose: skipping here would silently "+
				"drop the only coverage of the fail-loud restore behaviour (D105) while "+
				"every gate still reported green.", args...)
		}
		t.Skipf(format+" — skipping locally; this is fatal in CI", args...)
	}

	if _, err := exec.LookPath("pg_dump"); err != nil {
		unmet("pg_dump is not on PATH")
	}
	if _, err := exec.LookPath("psql"); err != nil {
		unmet("psql is not on PATH")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		unmet("DATABASE_URL is not set")
	}
	return dsn
}

// newScratchDatabase creates an empty database and registers its teardown.
func newScratchDatabase(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, adminDSN, prefix string) string {
	t.Helper()
	name := prefix + uuid.New().String()[:8]

	// Identifiers cannot be parameterised in CREATE/DROP DATABASE; `name` is
	// generated locally from a UUID, so this is trusted input.
	_, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name))
	require.NoError(t, err, "creating database %q", name)

	t.Cleanup(func() {
		_, _ = adminPool.Exec(ctx,
			fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`, name))
		_, _ = adminPool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", name))
	})

	dsn, err := dsnWithDatabase(adminDSN, name)
	require.NoError(t, err)
	return dsn
}

// captureStdout swaps os.Stdout for a pipe and returns everything the callback
// printed. restoreDatabase reports progress with fmt.Println, and the defect
// under test is a *printed* claim ("  Database restored.") that contradicts
// what happened, so asserting on the returned error alone would not catch it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig
	return <-done
}

// TestRestorePostgres_PartialRestoreIsAFailure is the regression test for the
// more dangerous half of D105.
//
// Before the fix, restorePostgres ran `psql <dsn>` with no -v ON_ERROR_STOP=1
// and cmd.Stdout = io.Discard. psql reports the status of its LAST statement
// and keeps going past failures, so a dump whose statements failed still exited
// 0, restorePostgres returned nil, and restoreDatabase printed
// "  Database restored." over a database that had not been restored. An
// operator only discovers that while recovering from an incident.
//
// Verified in both directions: with -v ON_ERROR_STOP=1 removed from
// restorePostgres, this test fails on the `require.Error` below — psql exits 0
// and the success line is printed.
func TestRestorePostgres_PartialRestoreIsAFailure(t *testing.T) {
	dsn := requirePostgresClientTools(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer adminPool.Close()

	targetDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_failloud_")

	// A dump that starts well and then fails. The first statement succeeds, so
	// this is specifically the *partial* case: the database is left in a state
	// that is neither the old one nor the intended new one.
	badDump := []byte(`
CREATE TABLE survivors (id integer PRIMARY KEY);
INSERT INTO survivors (id) VALUES (1);
INSERT INTO no_such_table (id) VALUES (2);
INSERT INTO survivors (id) VALUES (3);
`)

	cfg := &config.Config{DatabaseURL: targetDSN}
	entries := map[string][]byte{"database.sql": badDump}

	var restoreErr error
	out := captureStdout(t, func() {
		restoreErr = restoreDatabase(cfg, entries)
	})

	require.Error(t, restoreErr,
		"a dump containing a failing statement must make restore fail; "+
			"exiting 0 here is how an operator ends up with an empty database and a success message")
	require.Contains(t, restoreErr.Error(), "no_such_table",
		"the error must name the statement that failed — %q on its own does not tell an "+
			"operator what went wrong", restoreErr.Error())
	require.NotContains(t, out, "Database restored",
		"a failed restore must not print the success line; stdout was:\n%s", out)
}

// TestRestorePostgres_SucceedsOnAGoodDump is the positive control for the test
// above. Without it, restorePostgres could be changed to return an error
// unconditionally and the fail-loud test would still pass.
func TestRestorePostgres_SucceedsOnAGoodDump(t *testing.T) {
	dsn := requirePostgresClientTools(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer adminPool.Close()

	targetDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_goodrestore_")

	goodDump := []byte(`
CREATE TABLE survivors (id integer PRIMARY KEY);
INSERT INTO survivors (id) VALUES (1);
INSERT INTO survivors (id) VALUES (3);
`)

	cfg := &config.Config{DatabaseURL: targetDSN}
	entries := map[string][]byte{"database.sql": goodDump}

	var restoreErr error
	out := captureStdout(t, func() {
		restoreErr = restoreDatabase(cfg, entries)
	})

	require.NoError(t, restoreErr, "a clean dump must restore without error")
	require.Contains(t, out, "Database restored", "a successful restore must say so")

	// And the rows are actually there — the success line must correspond to
	// something real.
	targetPool, err := pgxpool.New(ctx, targetDSN)
	require.NoError(t, err)
	defer targetPool.Close()

	var count int
	require.NoError(t, targetPool.QueryRow(ctx, "SELECT count(*) FROM survivors").Scan(&count))
	require.Equal(t, 2, count, "both inserted rows must be present after restore")
}

// TestDumpPostgres_ReportsVersionProbeFailure covers the swallowed probe.
//
// dumpPostgres used to assign `versionOut, _ :=`, so a failing psql produced an
// empty PostgresVersion in the manifest and no error — recording a blank value
// nothing could act on. Pointed at a database that does not exist, the probe
// must now fail the backup rather than silently produce a version-less archive.
func TestDumpPostgres_ReportsVersionProbeFailure(t *testing.T) {
	dsn := requirePostgresClientTools(t)

	missingDSN, err := dsnWithDatabase(dsn, "azim_definitely_absent_"+uuid.New().String()[:8])
	require.NoError(t, err)

	_, version, err := dumpPostgres(missingDSN)
	require.Error(t, err, "a failing version probe must be reported, not discarded")
	require.Empty(t, version)
	require.Contains(t, err.Error(), "probing postgres version",
		"the error must say which step failed; got %q", err.Error())
}

// TestDumpPostgres_RecordsAVersion is the positive control: the probe must
// actually populate the manifest field against a working database, or the
// test above would pass with the probe hard-wired to fail.
func TestDumpPostgres_RecordsAVersion(t *testing.T) {
	dsn := requirePostgresClientTools(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer adminPool.Close()

	srcDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_versionprobe_")

	_, version, err := dumpPostgres(srcDSN)
	require.NoError(t, err)
	require.Contains(t, version, "PostgreSQL",
		"the manifest's postgres_version must carry the server's version string, got %q", version)
	require.Equal(t, strings.TrimSpace(version), version,
		"the version string must be trimmed — it is printed back to the operator on restore")
}

// --- Backup integrity: flush ordering and archive permissions (T3) ----------
//
// Two defects, one shape. runBackup wrote a world-readable archive containing
// a full pg_dump, and it printed "Backup complete" and returned nil *before*
// any of its three writers had been flushed, with every Close error discarded
// by a defer. A truncated archive was reported as a success.

// unreachableDSN is syntactically valid and cannot connect: port 1 refuses,
// and these tests must fail before any connection is made. config.Load only
// validates the DSN, so this is enough to get past loadConfig.
//
// It carries no userinfo at all. The obvious spelling — the
// `postgres://unused:unused@…` used elsewhere in cmd/server — trips gosec's
// G101 "Password in URL" when it appears as a literal in a struct field, and
// the right answer to a scanner finding here is to change the string rather
// than annotate it (docs/security-scanning.md). There is no credential to
// express: nothing authenticates against this.
const unreachableDSN = "postgres://127.0.0.1:1/unused?sslmode=disable"

// withBackupOutput points the backup command's --output flag at path for the
// duration of one test.
//
// backupOutput is a package-level var bound to a cobra flag, and cobra commands
// here are package singletons — the same mutation hazard
// TestCommands_SilenceUsageOnRuntimeFailure documents for SilenceUsage, which
// runBackup also sets. Both are restored.
func withBackupOutput(t *testing.T, path string) {
	t.Helper()
	prev := backupOutput
	backupOutput = path
	t.Cleanup(func() {
		backupOutput = prev
		backupCmd.SilenceUsage = false
	})
}

// swapBackupOpener substitutes the archive's file opener for one test.
func swapBackupOpener(t *testing.T, fn func(string) (io.WriteCloser, error)) {
	t.Helper()
	prev := openBackupOutput
	openBackupOutput = fn
	t.Cleanup(func() { openBackupOutput = prev })
}

// recordingCloser reports when it was closed and, optionally, fails.
type recordingCloser struct {
	name  string
	order *[]string
	err   error
}

func (c *recordingCloser) Close() error {
	*c.order = append(*c.order, c.name)
	return c.err
}

// closeFailsWriter accepts every write and fails on Close — the sink that
// makes a flush failure reproducible without a full disk.
type closeFailsWriter struct {
	buf      bytes.Buffer
	closeErr error
	closed   bool
}

func (w *closeFailsWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *closeFailsWriter) Close() error {
	w.closed = true
	return w.closeErr
}

// TestFinalizeArchive_ClosesInOrderAndReportsEveryFailure is the unit-level
// half of the flush fix, and it needs no postgres, so it runs everywhere.
//
// It asserts the two things the deferred-close version got wrong: that all
// three writers are closed innermost-first, and that a failure in ANY of them
// is returned rather than discarded. The ordering assertion is not decoration —
// tw.Close writes the tar footer into gw and gw.Close writes the gzip trailer
// into outFile, so closing them in any other order silently truncates the
// archive. finalizeArchive takes three same-typed io.Closers, which is exactly
// the shape that swaps unnoticed; this is what would notice.
func TestFinalizeArchive_ClosesInOrderAndReportsEveryFailure(t *testing.T) {
	t.Run("all three close, innermost first", func(t *testing.T) {
		var order []string
		err := finalizeArchive(
			&recordingCloser{name: "tar", order: &order},
			&recordingCloser{name: "gzip", order: &order},
			&recordingCloser{name: "file", order: &order},
		)
		require.NoError(t, err)
		require.Equal(t, []string{"tar", "gzip", "file"}, order,
			"the writers must be closed innermost-first: the tar footer is written into "+
				"the gzip stream and the gzip trailer into the file, so any other order "+
				"truncates the archive")
	})

	// Each layer, separately: a single test that only failed the outermost
	// close would pass with the other two errors still discarded.
	failures := []struct {
		layer     string
		tarErr    error
		gzipErr   error
		fileErr   error
		wantStage string
		wantOrder []string
	}{
		{
			layer:     "tar footer",
			tarErr:    errors.New("tar boom"),
			wantStage: "finalising tar archive",
			wantOrder: []string{"tar"},
		},
		{
			layer:     "gzip trailer",
			gzipErr:   errors.New("gzip boom"),
			wantStage: "finalising gzip stream",
			wantOrder: []string{"tar", "gzip"},
		},
		{
			layer:     "output file",
			fileErr:   errors.New("file boom"),
			wantStage: "closing backup file",
			wantOrder: []string{"tar", "gzip", "file"},
		},
	}
	for _, f := range failures {
		t.Run("a failure closing the "+f.layer+" is returned", func(t *testing.T) {
			var order []string
			err := finalizeArchive(
				&recordingCloser{name: "tar", order: &order, err: f.tarErr},
				&recordingCloser{name: "gzip", order: &order, err: f.gzipErr},
				&recordingCloser{name: "file", order: &order, err: f.fileErr},
			)
			require.Error(t, err,
				"a failure closing the %s leaves a corrupt archive; discarding it is how "+
					"a truncated backup reported success", f.layer)
			require.Contains(t, err.Error(), f.wantStage,
				"the error must name the stage that failed; got %q", err.Error())
			require.Equal(t, f.wantOrder, order,
				"finalizeArchive must stop at the first failure — the archive is already corrupt")
		})
	}
}

// TestFinalizeArchive_SurfacesARealSinkFailure is the composition check for
// the test above: the fakes prove the ordering and the error plumbing, this
// proves the same function behaves over the real tar and gzip writers rather
// than only over io.Closers that agree with it.
func TestFinalizeArchive_SurfacesARealSinkFailure(t *testing.T) {
	sink := &closeFailsWriter{closeErr: errors.New("disk went away")}
	gw := gzip.NewWriter(sink)
	tw := tar.NewWriter(gw)
	require.NoError(t, addToTar(tw, "database.sql", []byte("-- dump")))

	err := finalizeArchive(tw, gw, sink)
	require.Error(t, err, "a sink that cannot be closed must fail the backup")
	require.ErrorContains(t, err, "disk went away")
	require.True(t, sink.closed, "the sink must actually have been closed")

	// The bytes did reach the sink before it failed — proof the tar footer and
	// gzip trailer were flushed in order and the failure is genuinely the
	// close, not an earlier short-circuit that never wrote anything.
	require.NotEmpty(t, sink.buf.Bytes(), "the archive bytes must have been flushed to the sink")
}

// TestRunBackup_FlushFailureIsNotASuccess is the end-to-end regression test:
// runBackup itself must report the failure and must not print the success line.
//
// This is the defect exactly as it shipped. The three writers were closed by
// deferred functions that discarded their errors, and defers run after the
// return statement — so `fmt.Printf("Backup complete: %s\n", ...)` had already
// printed and runBackup had already returned nil by the time the flush failed.
// The operator saw a success message over a truncated archive and discovered
// otherwise while restoring.
//
// Gated on requirePostgresClientTools like its sibling
// TestRestorePostgres_PartialRestoreIsAFailure, and for the same reason: the
// dump is runBackup's first step, so there is no route to the flush without a
// real server. That gate is fatal in CI, so the coverage is real there.
func TestRunBackup_FlushFailureIsNotASuccess(t *testing.T) {
	dsn := requirePostgresClientTools(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer adminPool.Close()

	srcDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_flushfail_")

	t.Setenv("DATABASE_URL", srcDSN)
	t.Setenv("STORAGE_ENDPOINT", "") // the supported "no object store" case
	withBackupOutput(t, filepath.Join(t.TempDir(), "unflushable.tar.gz"))

	sink := &closeFailsWriter{closeErr: errors.New("simulated flush failure")}
	swapBackupOpener(t, func(string) (io.WriteCloser, error) { return sink, nil })

	var backupErr error
	out := captureStdout(t, func() {
		backupErr = runBackup(backupCmd, nil)
	})

	require.Error(t, backupErr,
		"a backup whose archive could not be flushed must fail; returning nil here is how "+
			"a truncated archive becomes a backup an operator trusts")
	require.ErrorContains(t, backupErr, "simulated flush failure",
		"the error must carry the underlying flush failure; got %q", backupErr.Error())
	require.NotContains(t, out, "Backup complete",
		"a backup that could not be flushed must not print the success line; stdout was:\n%s", out)
}

// TestRunBackup_SucceedsAndPrintsCompleteOnAGoodRun is the positive control.
// Without it, runBackup could be changed to return an error unconditionally
// and the test above would still pass.
//
// It also closes the loop the flush fix is really about: the archive left on
// disk is read back and must contain the dump the run claimed to take.
func TestRunBackup_SucceedsAndPrintsCompleteOnAGoodRun(t *testing.T) {
	dsn := requirePostgresClientTools(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer adminPool.Close()

	srcDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_goodbackup_")

	archive := filepath.Join(t.TempDir(), "good.tar.gz")
	t.Setenv("DATABASE_URL", srcDSN)
	t.Setenv("STORAGE_ENDPOINT", "")
	withBackupOutput(t, archive)

	var backupErr error
	out := captureStdout(t, func() {
		backupErr = runBackup(backupCmd, nil)
	})

	require.NoError(t, backupErr, "a backup against a reachable database must succeed")
	require.Contains(t, out, "Backup complete", "a successful backup must say so")

	// And the file it left behind is a readable archive with a dump in it —
	// the success line must correspond to something real.
	entries, err := readArchive(archive)
	require.NoError(t, err, "the archive runBackup wrote must be readable")
	require.Contains(t, entries, "database.sql", "the archive must contain the database dump")
	require.NotEmpty(t, entries["database.sql"])
	require.Contains(t, entries, "manifest.json")
}

// TestRestore_MissingDatabaseDumpIsAFailure is the regression test for the
// silent no-op restore.
//
// restoreDatabase printed "No database dump found in backup, skipping." and
// returned nil, so runRestore carried on to "Restore complete (N files in
// manifest)." having restored no database at all. validateManifest does not
// close this: it verifies that files the manifest *lists* are present, and a
// manifest listing no dump passes it. An operator restoring from a corrupt or
// foreign archive got a success message and an untouched database.
func TestRestore_MissingDatabaseDumpIsAFailure(t *testing.T) {
	manifest := backupManifest{
		AzimuthalVersion: "test",
		BackupTimestamp:  time.Now().UTC(),
		Files:            []string{"storage/avatar.png"},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	entries := map[string][]byte{
		"manifest.json":      manifestJSON,
		"storage/avatar.png": []byte("fake-image-bytes"),
		// database.sql deliberately absent
	}

	// The manifest itself is valid — this archive passes every check that
	// existed before the fix, which is precisely why the skip was dangerous.
	_, err = validateManifest(entries)
	require.NoError(t, err, "the archive must be manifest-valid, or this test proves nothing new")

	cfg := &config.Config{DatabaseURL: unreachableDSN}

	var restoreErr error
	out := captureStdout(t, func() {
		restoreErr = restoreDatabase(cfg, entries)
	})

	require.Error(t, restoreErr,
		"an archive with no database.sql must fail the restore; returning nil is a "+
			"restore that silently restores nothing")
	require.ErrorIs(t, restoreErr, errNoDatabaseDump)
	require.Contains(t, restoreErr.Error(), "database.sql",
		"the error must name what is missing; got %q", restoreErr.Error())
	require.NotContains(t, out, "skipping",
		"a missing dump must not be reported as a skip; stdout was:\n%s", out)
}

// TestRestore_PresentDatabaseDumpIsNotRefused is the negative control for the
// test above: without it, restoreDatabase could return errNoDatabaseDump
// unconditionally and the regression test would still pass.
//
// It needs no postgres — whatever happens once psql is forked (missing binary,
// unreachable DSN), the one thing that must NOT come back is the missing-dump
// sentinel.
func TestRestore_PresentDatabaseDumpIsNotRefused(t *testing.T) {
	cfg := &config.Config{DatabaseURL: unreachableDSN}
	entries := map[string][]byte{"database.sql": []byte("SELECT 1;")}

	var restoreErr error
	_ = captureStdout(t, func() {
		restoreErr = restoreDatabase(cfg, entries)
	})

	require.NotErrorIs(t, restoreErr, errNoDatabaseDump,
		"a dump IS present, so the missing-dump sentinel must not be what comes back; got %v", restoreErr)
}

// seedFixtures captures the rows seedRoundTripFixtures inserted so the
// caller can assert against them after the round-trip.
type seedFixtures struct {
	org     generated.Organization
	user    generated.User
	space   generated.Space
	item    generated.Ticket
	page    generated.Page
	comment generated.Comment
}

// seedRoundTripFixtures inserts one of each entity into the source DB.
func seedRoundTripFixtures(ctx context.Context, t *testing.T, q *generated.Queries) seedFixtures {
	t.Helper()

	suffix := uuid.New().String()[:8]
	desc := "Round-trip fixture org " + suffix

	org, err := q.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID:          uuid.New(),
		Slug:        "rt-org-" + suffix,
		Name:        "Round Trip Org " + suffix,
		Description: &desc,
	})
	require.NoError(t, err)

	hash := "$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LewdBPj/VK.s4VqK2"
	user, err := q.CreateUser(ctx, generated.CreateUserParams{
		ID:           uuid.New(),
		OrgID:        org.ID,
		Email:        "rt-" + suffix + "@azimuthal.dev",
		DisplayName:  "Round Trip User",
		PasswordHash: &hash,
		Role:         "member",
	})
	require.NoError(t, err)

	_, err = q.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:        uuid.New(),
		OrgID:     org.ID,
		UserID:    user.ID,
		Role:      "owner",
		InvitedBy: pgtype.UUID{},
	})
	require.NoError(t, err)

	// spaces.owner_team_id is NOT NULL and visibility is CHECK-constrained
	// since migration 023 — the fixture org needs its default team first,
	// exactly as every production org-creation path seeds one.
	team, err := q.CreateTeam(ctx, generated.CreateTeamParams{
		ID:        uuid.New(),
		OrgID:     org.ID,
		Slug:      "default",
		Name:      "Default",
		IsDefault: true,
		Source:    "manual",
	})
	require.NoError(t, err)

	spaceDesc := "Round-trip fixture space"
	space, err := q.CreateSpace(ctx, generated.CreateSpaceParams{
		ID:          uuid.New(),
		OrgID:       org.ID,
		Slug:        "rt-space-" + suffix,
		Name:        "Round Trip Space",
		Description: &spaceDesc,
		Type:        "beacon",
		IsPrivate:   false,
		CreatedBy:   user.ID,
		Key:         "RT",
		OwnerTeamID: team.ID,
		Visibility:  "discoverable",
	})
	require.NoError(t, err)

	itemDesc := "Round-trip fixture item"
	item, err := q.CreateTicket(ctx, generated.CreateTicketParams{
		ID:          uuid.New(),
		SpaceID:     space.ID,
		Number:      1,
		Title:       "Round Trip Ticket",
		Description: itemDesc,
		Status:      "open",
		Priority:    "medium",
		ReporterID:  pgtype.UUID{Bytes: user.ID, Valid: true},
		Rank:        "a0",
	})
	require.NoError(t, err)

	page, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID:       uuid.New(),
		SpaceID:  space.ID,
		Title:    "Round Trip Page",
		Content:  "Hello from the round-trip test.",
		AuthorID: user.ID,
		Position: 0,
	})
	require.NoError(t, err)

	comment, err := q.CreateComment(ctx, generated.CreateCommentParams{
		ID:         uuid.New(),
		EntityType: "ticket",
		EntityID:   item.ID,
		AuthorID:   pgtype.UUID{Bytes: user.ID, Valid: true},
		Body:       "Round-trip comment body.",
	})
	require.NoError(t, err)

	return seedFixtures{
		org:     org,
		user:    user,
		space:   space,
		item:    item,
		page:    page,
		comment: comment,
	}
}

// dsnWithDatabase rewrites the database name in a postgres URL, leaving
// host/port/user/password/query parameters untouched.
func dsnWithDatabase(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing DSN: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// guard against unused-import for bytes when only one helper happens to
// use it during refactors; keep this so go vet stays clean.
var _ = bytes.NewReader
