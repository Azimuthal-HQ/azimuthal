package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/assess"
)

var (
	assessJira       string
	assessConfluence string
	assessJSON       bool
	assessOutput     string
)

// assessCmd is the read-only migration assessor.
//
// # It takes no DSN, and that is structural
//
// Every other subcommand begins by calling loadConfig, which is what reaches
// DATABASE_URL. This one never does, and never imports internal/config,
// internal/db or a driver. The root command has no PersistentPreRun, so nothing
// acquires a connection on a subcommand's behalf either — which is what makes
// "this cannot touch your data" a property of the code rather than a promise in
// the help text. internal/assess.TestNoDatabaseReachability walks the package's
// transitive imports and fails by name if that ever stops being true.
//
// The point is not tidiness. The tool exists so a self-hoster can evaluate a
// migration *before* trusting Azimuthal with anything, and a tool that could
// write is one they would have to trust first.
var assessCmd = &cobra.Command{
	Use:   "assess",
	Short: "Assess a Jira or Confluence export for migration (read-only)",
	Long: `Reads a Jira Cloud backup and/or a Confluence space export and reports
what a future import would map cleanly, map with approximation, preserve as
unknown content, or lose.

Nothing is written and no database is contacted — this command does not accept
a connection string and cannot reach one.

Giving both exports at once is worth doing: an item key is unique per
organisation, so a Jira project and a Confluence space can contend for the same
space key, and neither export can reveal that on its own.

Examples:
  azimuthal assess --jira ./jira-backup.zip
  azimuthal assess --confluence ./DOCS-space-export.zip
  azimuthal assess --jira ./jira.zip --confluence ./docs.zip --output report.md
  azimuthal assess --jira ./jira.zip --json`,
	// SilenceUsage is deliberately NOT a field here. It is set inside runAssess
	// instead, so it takes effect only once flag parsing has succeeded: a
	// mistyped flag still gets usage, a runtime failure does not. Uniform
	// across every command in this binary — see runRestore for the rationale.
	RunE: runAssess,
}

func init() {
	assessCmd.Flags().StringVar(&assessJira, "jira", "", "path to a Jira Cloud backup zip")
	assessCmd.Flags().StringVar(&assessConfluence, "confluence", "", "path to a Confluence space export zip")
	assessCmd.Flags().BoolVar(&assessJSON, "json", false, "emit JSON instead of markdown")
	assessCmd.Flags().StringVarP(&assessOutput, "output", "o", "", "write the report to a file instead of stdout")
}

func runAssess(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true // runtime failure, not a usage error — see TestCommands_SilenceUsageOnRuntimeFailure
	res, err := assess.Run(assess.Input{
		JiraPath:       assessJira,
		ConfluencePath: assessConfluence,
	})
	if err != nil {
		return fmt.Errorf("assessing the export: %w", err)
	}

	out, closeOut, err := assessWriter(cmd)
	if err != nil {
		return err
	}
	defer closeOut()

	if assessJSON {
		return res.WriteJSON(out) //nolint:wrapcheck // the report writer already wraps
	}
	return res.WriteMarkdown(out) //nolint:wrapcheck // the report writer already wraps
}

// assessReportMode is the permission the --output report file is created with.
//
// Owner-only (0600), the same mode and for the same reason as the backup archive
// (backupArchiveMode in backup.go): an assessment report carries derived
// Jira/Confluence data — space and item keys, export file paths, and the
// verbatim JQL of saved filters — that should be readable only by the operator
// who ran the command, not by anyone else on the host. This was os.Create, which
// is 0666-before-umask (0644, i.e. world-readable, under the default 022), and
// the old "#nosec ... as in backup/restore" note no longer matched once T3 moved
// backup to os.OpenFile with an explicit mode. This mirrors what backup does
// now, not the os.Create it used to.
const assessReportMode os.FileMode = 0o600

// assessWriter resolves --output, defaulting to the command's own stdout so the
// report is capturable in a test.
func assessWriter(cmd *cobra.Command) (out interface{ Write([]byte) (int, error) }, closeOut func(), err error) {
	if assessOutput == "" {
		return cmd.OutOrStdout(), func() {}, nil
	}
	// G304 (path from a variable) is genuine and legitimately suppressed:
	// assessOutput is the --output CLI flag, operator-supplied at their own
	// shell, not attacker-influenced.
	f, err := os.OpenFile(assessOutput, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, assessReportMode) // #nosec G304 -- user-provided CLI flag
	if err != nil {
		return nil, nil, fmt.Errorf("creating the report file: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}
