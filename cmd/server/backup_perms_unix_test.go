//go:build !windows

// This file is build-constrained rather than skipped, and the distinction is
// deliberate.
//
// The property under test — that the backup archive is created owner-only —
// does not exist on Windows. Go's os.Stat there synthesises a mode from the
// read-only attribute alone: a file created with 0600 and a file created with
// 0666 both report 0666, so the assertion cannot fail in either direction and
// a "passing" test on Windows would be pure theatre. A t.Skip would also be
// wrong under CLAUDE.md §2, which requires a named blocker, an issue number
// and a re-enable condition — none of which exist for a permanent platform
// difference. A build constraint says the honest thing: there is nothing here
// to run. CI runs the Go suite on ubuntu-latest, where this file builds.

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunBackup_ArchiveIsOwnerOnly is the regression test for the archive's
// permissions.
//
// runBackup created the output with os.Create, which is 0666 before umask —
// 0644 on any host with the default umask of 022. The file holds a full
// pg_dump (every password hash, every session and portal token, every private
// page and ticket) plus every byte in object storage, and operators leave it
// in /var/backups and copy it around. World-readable is the wrong default for
// the densest secret this system produces.
//
// The umask is pinned for the duration of the test rather than inherited. That
// is what makes this a regression test instead of a coin toss: under the
// default 022 the pre-fix os.Create yields 0644 and this fails, but under a
// umask of 077 it would yield 0600 and pass against the unfixed code. Pinning
// removes the environment from the answer. No test in this package calls
// t.Parallel, and the umask is per-process, so the window is not shared.
func TestRunBackup_ArchiveIsOwnerOnly(t *testing.T) {
	prevUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(prevUmask) })

	archive := filepath.Join(t.TempDir(), "perm-check.tar.gz")
	withBackupOutput(t, archive)

	// A syntactically valid DSN that cannot connect. runBackup creates the
	// output file before it attempts the dump, so the archive exists and
	// carries its final mode however far the run gets — which is the point:
	// the permissions of a file full of secrets must not depend on whether the
	// backup succeeded. Nothing in runBackup chmods it afterwards, so this one
	// assertion covers the successful path too.
	t.Setenv("DATABASE_URL", unreachableDSN)
	t.Setenv("STORAGE_ENDPOINT", "")

	var backupErr error
	_ = captureStdout(t, func() {
		backupErr = runBackup(backupCmd, nil)
	})
	require.Error(t, backupErr, "an unreachable database must fail the backup")

	info, err := os.Stat(archive)
	require.NoError(t, err, "runBackup must have created the output file before dumping")

	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the backup archive contains a full pg_dump and every object-storage byte; "+
			"it must be owner-only. os.Create's 0666-before-umask left it %v — readable "+
			"by every account on the host", info.Mode().Perm())
	require.Zero(t, info.Mode().Perm()&0o077,
		"no group or world bits may be set on the archive")
}

// TestOpenBackupOutput_RefusesNothingItCannotCreate pins the seam's error
// contract, which the flush tests depend on.
//
// The production opener is a func literal that could plausibly be written as
// `return os.OpenFile(...)`. That compiles and is wrong in a way nothing else
// here would catch: on error it hands back a typed-nil *os.File inside a
// non-nil io.WriteCloser, so runBackup's `if err != nil` returns first today,
// but any future caller checking the writer instead of the error gets a
// non-nil interface wrapping nil and panics on first use.
func TestOpenBackupOutput_RefusesNothingItCannotCreate(t *testing.T) {
	// A path whose parent is a regular file, so the create cannot succeed.
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o600))

	w, err := openBackupOutput(filepath.Join(parent, "archive.tar.gz"))
	require.Error(t, err, "creating an archive under a regular file must fail")
	require.ErrorContains(t, err, "creating output file",
		"the error must name the step, since runBackup returns it unwrapped; got %q", err.Error())

	// Compared with `==`, NOT require.Nil. require.Nil reflects into the
	// interface and reports a typed-nil *os.File as nil, so it would pass
	// against exactly the mistake this test exists to catch. Interface
	// comparison is the only assertion that distinguishes them.
	require.True(t, w == nil,
		"the returned writer must be a true nil interface, not a typed-nil *os.File "+
			"wrapped in a non-nil io.WriteCloser; got %#v", w)
}
