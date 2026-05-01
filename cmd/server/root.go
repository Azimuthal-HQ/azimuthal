// Package main provides the root cobra command for the azimuthal binary.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd is the top-level command when no subcommand is given.
var rootCmd = &cobra.Command{
	Use:   "azimuthal",
	Short: "Azimuthal — know exactly where your team is headed",
	Long: `Azimuthal is a fully open-source, self-hostable alternative to the
Atlassian suite (Jira, Confluence, Jira Service Desk).

Run "azimuthal serve" to start the HTTP server, or use one of the
subcommands below for administration tasks.`,
	Version: fmt.Sprintf("%s (built %s)", Version, BuildTime),
}

func init() {
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(adminCmd)
}

// Execute runs the root command. Called from main().
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
