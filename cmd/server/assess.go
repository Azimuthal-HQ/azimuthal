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
	RunE:         runAssess,
	SilenceUsage: true,
}

func init() {
	assessCmd.Flags().StringVar(&assessJira, "jira", "", "path to a Jira Cloud backup zip")
	assessCmd.Flags().StringVar(&assessConfluence, "confluence", "", "path to a Confluence space export zip")
	assessCmd.Flags().BoolVar(&assessJSON, "json", false, "emit JSON instead of markdown")
	assessCmd.Flags().StringVarP(&assessOutput, "output", "o", "", "write the report to a file instead of stdout")
}

func runAssess(cmd *cobra.Command, _ []string) error {
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

// assessWriter resolves --output, defaulting to the command's own stdout so the
// report is capturable in a test.
func assessWriter(cmd *cobra.Command) (out interface{ Write([]byte) (int, error) }, closeOut func(), err error) {
	if assessOutput == "" {
		return cmd.OutOrStdout(), func() {}, nil
	}
	f, err := os.Create(assessOutput) // #nosec G304 -- user-provided CLI flag, as in backup/restore
	if err != nil {
		return nil, nil, fmt.Errorf("creating the report file: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}
