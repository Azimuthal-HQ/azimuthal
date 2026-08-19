package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The datastore lock (cmd/server/dblock.go) is the enforcement of "stop the app
// before restoring". These tests prove the two halves that matter:
//
//   - serve's SHARED hold and restore's EXCLUSIVE try use the SAME key, so a
//     live server makes restore refuse. If they used different keys the
//     exclusive try would not conflict and the refusal below would not happen —
//     which is why this is a real test of the shared constant, not just that it
//     compiles from one symbol (it does: both sites name serveStoreLockKey).
//   - a refused restore touches NOTHING: it aborts before reading the archive,
//     let alone running psql, so seeded data survives and no success line prints
//     (the T3 shape — assert the absence of the success line AND that the data
//     survived).

// withRestoreInput points the restore command's --input flag at path for one
// test, restoring the package-singleton state afterwards (restoreCmd is shared
// and runRestore sets SilenceUsage on it — the same mutation hazard the backup
// tests document).
func withRestoreInput(t *testing.T, path string) {
	t.Helper()
	prev := restoreInput
	restoreInput = path
	t.Cleanup(func() {
		restoreInput = prev
		restoreCmd.SilenceUsage = false
	})
}

// writeMinimalArchive writes a valid backup archive (manifest.json + the given
// database.sql) to a temp file and returns its path. The dump is applied only
// if a restore actually proceeds — the refusal tests pass a DESTRUCTIVE dump so
// that "the data survived" genuinely proves the restore never ran.
func writeMinimalArchive(t *testing.T, dbSQL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.tar.gz")

	manifest := backupManifest{
		AzimuthalVersion: "test",
		BackupTimestamp:  time.Now().UTC().Truncate(time.Second),
		Files:            []string{"database.sql"},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)

	out, err := os.Create(path) //nolint:gosec // G304 — path is a t.TempDir() path
	require.NoError(t, err)
	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)
	require.NoError(t, addToTar(tw, "database.sql", []byte(dbSQL)))
	require.NoError(t, addToTar(tw, "manifest.json", manifestJSON))
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, out.Close())

	return path
}

// TestStoreLock_ExclusiveTryConflictsWithSharedHold is the lock-semantics core:
// while serve's shared hold is live, restore's non-blocking exclusive try fails
// with the refusal; once the shared hold is released, the try succeeds.
//
// Verified as a real guard by construction: shared and exclusive requests on
// the SAME advisory key conflict across sessions, so this passing depends on
// both helpers naming serveStoreLockKey. Point restoreTryStoreLock at a
// different key and the refusal disappears.
func TestStoreLock_ExclusiveTryConflictsWithSharedHold(t *testing.T) {
	tdb := testutil.NewTestDB(t) // skips without DATABASE_URL; fatal in CI
	ctx := context.Background()

	// serve holds the shared lock.
	serveLock, err := serveAcquireStoreLock(ctx, tdb.DSN)
	require.NoError(t, err, "serve must be able to take the shared lock")

	// restore's exclusive try must be refused while it is held.
	refused, err := restoreTryStoreLock(ctx, tdb.DSN)
	require.Nil(t, refused, "restore must not acquire the lock while a server holds it")
	require.ErrorIs(t, err, errServerRunning,
		"a held datastore lock must refuse restore with the stop-the-server instruction")

	// Once serve releases, the exclusive try succeeds. Release is via connection
	// close, so allow the backend a moment to drop the lock.
	serveLock.Release()
	require.Eventually(t, func() bool {
		got, tryErr := restoreTryStoreLock(ctx, tdb.DSN)
		if tryErr != nil {
			return false
		}
		got.Release()
		return true
	}, 5*time.Second, 50*time.Millisecond,
		"the exclusive lock must be free once the shared holder releases")
}

// TestRunRestore_RefusesAndTouchesNothingWhileServerRuns is the end-to-end T3
// test: with a server holding the lock, runRestore exits non-zero with the
// refusal, prints no success line, and leaves seeded data untouched.
//
// It needs no psql: the lock is checked before the archive is even read, so the
// refusal happens long before restorePostgres would fork anything. That is
// exactly the property under test — the refusal is total.
func TestRunRestore_RefusesAndTouchesNothingWhileServerRuns(t *testing.T) {
	tdb := testutil.NewTestDB(t) // skips without DATABASE_URL; fatal in CI
	ctx := context.Background()

	// Seed a sentinel the archive's dump would destroy if it were ever applied.
	_, err := tdb.Pool.Exec(ctx, "CREATE TABLE restore_sentinel (id integer PRIMARY KEY)")
	require.NoError(t, err)
	_, err = tdb.Pool.Exec(ctx, "INSERT INTO restore_sentinel (id) VALUES (1)")
	require.NoError(t, err)

	// A server is live: it holds the shared lock.
	serveLock, err := serveAcquireStoreLock(ctx, tdb.DSN)
	require.NoError(t, err)
	defer serveLock.Release()

	// A real, destructive archive — DROP would erase the sentinel if restore ran.
	archive := writeMinimalArchive(t, "DROP TABLE IF EXISTS restore_sentinel;\n")
	withRestoreInput(t, archive)
	t.Setenv("DATABASE_URL", tdb.DSN)
	t.Setenv("STORAGE_ENDPOINT", "")

	var restoreErr error
	out := captureStdout(t, func() {
		restoreErr = runRestore(restoreCmd, nil)
	})

	require.ErrorIs(t, restoreErr, errServerRunning,
		"restore must refuse while a server holds the datastore lock")
	require.NotContains(t, out, "Restore complete",
		"a refused restore must not print the success line; stdout was:\n%s", out)
	require.NotContains(t, out, "Restoring PostgreSQL database",
		"a refused restore must abort before touching the database at all; stdout was:\n%s", out)

	// The sentinel survives — the refusal touched nothing.
	var count int
	require.NoError(t, tdb.Pool.QueryRow(ctx, "SELECT count(*) FROM restore_sentinel").Scan(&count))
	require.Equal(t, 1, count, "the refused restore must have left the data untouched")
}

// TestRunRestore_ProceedsWhenNoServerHoldsLock is the negative control: with no
// server holding the lock, runRestore gets past the gate and applies the dump.
// Without this, restoreTryStoreLock could refuse unconditionally and the test
// above would still pass.
//
// Gated on the postgres client tools like the other round-trip tests (fatal in
// CI, skip locally): here the restore actually proceeds through psql.
func TestRunRestore_ProceedsWhenNoServerHoldsLock(t *testing.T) {
	dsn := requirePostgresClientTools(t)
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer adminPool.Close()

	targetDSN := newScratchDatabase(ctx, t, adminPool, dsn, "azim_lockfree_")

	// A dump whose effect is observable: applying it creates a marker row.
	archive := writeMinimalArchive(t, `
CREATE TABLE restored_marker (id integer PRIMARY KEY);
INSERT INTO restored_marker (id) VALUES (1);
`)
	withRestoreInput(t, archive)
	t.Setenv("DATABASE_URL", targetDSN)
	t.Setenv("STORAGE_ENDPOINT", "")

	var restoreErr error
	out := captureStdout(t, func() {
		restoreErr = runRestore(restoreCmd, nil)
	})

	require.NoError(t, restoreErr, "with no server holding the lock, restore must proceed")
	require.Contains(t, out, "Restore complete", "a completed restore must say so")

	// The dump was applied — the lock let it through.
	targetPool, err := pgxpool.New(ctx, targetDSN)
	require.NoError(t, err)
	defer targetPool.Close()
	var count int
	require.NoError(t, targetPool.QueryRow(ctx, "SELECT count(*) FROM restored_marker").Scan(&count))
	require.Equal(t, 1, count, "the restore must have applied the dump")
}
