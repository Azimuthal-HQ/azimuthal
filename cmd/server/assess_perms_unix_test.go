//go:build !windows

// Build-constrained rather than skipped, for the same reason as
// backup_perms_unix_test.go: the property under test — that the assessment
// report file is created owner-only — does not exist on Windows, where os.Stat
// synthesises a mode from the read-only attribute alone and a 0600 and a 0666
// file both report 0666. A t.Skip would violate CLAUDE.md §2 (no named blocker,
// issue, or re-enable condition for a permanent platform difference); a build
// constraint says the honest thing. CI runs the Go suite on ubuntu-latest.

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestAssessWriter_OutputFileIsOwnerOnly is the regression test for the
// assessment report's permissions.
//
// assessWriter created the --output file with os.Create, which is 0666 before
// umask — 0644 on any host with the default umask of 022, i.e. world-readable.
// The report carries derived Jira/Confluence data (space and item keys, export
// file paths, the verbatim JQL of saved filters), so world-readable is the wrong
// default, the same conclusion T3 reached for the backup archive.
//
// The umask is pinned for the duration of the test rather than inherited, which
// is what makes this a regression test instead of a coin toss: under the default
// 022 the pre-fix os.Create yields 0644 and this fails, but under a umask of 077
// it would yield 0600 and pass against the unfixed code. Pinning removes the
// environment from the answer. No test in this package calls t.Parallel, and the
// umask and assessOutput are per-process, so the window is not shared.
func TestAssessWriter_OutputFileIsOwnerOnly(t *testing.T) {
	prevUmask := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(prevUmask) })

	path := filepath.Join(t.TempDir(), "report.md")
	prev := assessOutput
	assessOutput = path
	t.Cleanup(func() { assessOutput = prev })

	// assessWriter only consults cmd in the stdout branch (assessOutput == ""),
	// which this does not take, so a bare command is enough.
	out, closeOut, err := assessWriter(&cobra.Command{})
	require.NoError(t, err, "assessWriter must create the report file")
	require.NotNil(t, out)
	closeOut()

	info, err := os.Stat(path)
	require.NoError(t, err, "assessWriter must have created the output file")

	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the assessment report carries extracted Jira/Confluence data and must be "+
			"owner-only. os.Create's 0666-before-umask left it %v — readable by every "+
			"account on the host", info.Mode().Perm())
	require.Zero(t, info.Mode().Perm()&0o077,
		"no group or world bits may be set on the assessment report")
}
