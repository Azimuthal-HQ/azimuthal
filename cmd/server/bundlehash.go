package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/bundle"
	"github.com/Azimuthal-HQ/azimuthal/web"
)

var (
	bundleHashDir    string
	bundleHashVerify string
)

// bundleHashCmd reports, and can verify, the digest of the frontend compiled
// into this binary.
//
// # Why a subcommand rather than a script
//
// The comparison has to be between the bundle INSIDE the binary and the one on
// disk, and only the binary can read the first — go:embed puts web/dist into
// the executable at compile time and nothing outside it can walk that tree. A
// script could sample the binary's bytes for a hashed asset filename, which is
// cheaper but samples rather than proves: it would miss web/public assets,
// which Vite copies through unhashed (favicon.svg today).
//
// Running both sides through the same code also removes a whole class of false
// alarm. Two implementations of "hash a directory" disagree about sort order,
// path separators and empty directories, and every one of those disagreements
// would read as a stale bundle.
//
// # It takes no DSN, like assess
//
// Every subcommand that needs the database calls loadConfig; this one does not,
// imports neither internal/config nor internal/db, and the root command has no
// PersistentPreRun to acquire a connection on its behalf. That is what lets it
// run as a preflight before postgres exists, and why CI can call it on a
// downloaded artifact with nothing else set up.
var bundleHashCmd = &cobra.Command{
	Use:   "bundle-hash",
	Short: "Print or verify the digest of the frontend embedded in this binary",
	Long: `Prints a SHA-256 over the frontend bundle compiled into this binary.

With --dir, prints the digest of a bundle on disk instead. With --verify, does
both and exits non-zero if they differ — which is the preflight the E2E suite
runs, because go:embed reads web/dist at COMPILE time and a binary built before
the frontend serves the older bundle without complaint.

  azimuthal bundle-hash                    # the embedded bundle
  azimuthal bundle-hash --dir web/dist     # a bundle on disk
  azimuthal bundle-hash --verify web/dist  # compare the two`,
	// SilenceUsage is set inside the RunE below rather than as a field here, so
	// a mistyped flag still gets usage while a runtime failure does not. See
	// runRestore for the rationale.
	RunE: func(cmd *cobra.Command, _ []string) error {
		cmd.SilenceUsage = true // runtime failure, not a usage error — see TestCommands_SilenceUsageOnRuntimeFailure
		embedded, err := embeddedBundle()
		if err != nil {
			return err
		}

		switch {
		case bundleHashVerify != "":
			return verifyBundle(embedded, bundleHashVerify)

		case bundleHashDir != "":
			digest, err := bundle.Digest(os.DirFS(bundleHashDir))
			if err != nil {
				return fmt.Errorf("hashing %s: %w", bundleHashDir, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), digest)
			return nil

		default:
			digest, err := bundle.Digest(embedded)
			if err != nil {
				return fmt.Errorf("hashing the embedded bundle: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), digest)
			return nil
		}
	},
}

// embeddedBundle strips the "dist" prefix that go:embed all:dist creates, so
// embedded paths ("index.html") line up with on-disk ones under web/dist.
// Without this the two digests could never match and the preflight would be a
// permanent false alarm.
func embeddedBundle() (fs.FS, error) {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("opening the embedded bundle: %w", err)
	}
	return sub, nil
}

func verifyBundle(embedded fs.FS, dir string) error {
	embeddedDigest, err := bundle.Digest(embedded)
	if err != nil {
		return fmt.Errorf("hashing the embedded bundle: %w", err)
	}
	onDisk := os.DirFS(dir)
	diskDigest, err := bundle.Digest(onDisk)
	if err != nil {
		return fmt.Errorf("hashing %s: %w", dir, err)
	}
	if embeddedDigest == diskDigest {
		return nil
	}

	embeddedCount, _ := bundle.Count(embedded)
	diskCount, _ := bundle.Count(onDisk)

	// One named error, on one line, then the detail. The whole point of this
	// command is that a stale bundle stops presenting as a screenful of
	// unrelated E2E failures.
	return fmt.Errorf(
		"STALE EMBEDDED FRONTEND: this binary's embedded bundle does not match %s\n"+
			"  embedded: %s (%s)\n"+
			"  on disk:  %s (%s)\n"+
			"go:embed reads web/dist at COMPILE time, so the binary carries whatever was\n"+
			"there when it was built. Rebuild in this order:\n"+
			"  cd web && npm run build   # refresh web/dist from the current source\n"+
			"  go build -o <binary> ./cmd/server",
		dir, embeddedDigest, plural(embeddedCount), diskDigest, plural(diskCount))
}

// plural keeps the counts readable, because "1 files" in a failure message is
// the sort of thing that makes a reader wonder what else was sloppy — and this
// message's whole job is to be believed on sight.
func plural(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func init() {
	bundleHashCmd.Flags().StringVar(&bundleHashDir, "dir", "",
		"hash this directory instead of the embedded bundle")
	bundleHashCmd.Flags().StringVar(&bundleHashVerify, "verify", "",
		"compare the embedded bundle against this directory and fail if they differ")
}
