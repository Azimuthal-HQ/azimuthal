package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// writeZip builds an export archive in a temp dir and returns its path.
func writeZip(t *testing.T, name string, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)

	f, err := os.Create(path) //nolint:gosec // G304 — a t.TempDir() path
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()

	zw := zip.NewWriter(f)
	for entryName, body := range entries {
		w, err := zw.Create(entryName)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return path
}

const miniJira = `<entity-engine-xml>
  <Project id="10000" key="ABC" name="Alpha"/>
  <Issue id="1" key="ABC-1" project="10000" type="1" status="1" summary="One"/>
  <IssueType id="1" name="Task"/>
  <Status id="1" name="Open"/>
</entity-engine-xml>`

const miniConfluence = `<hibernate-generic>
  <object class="Space"><id name="id">1</id><property name="key"><![CDATA[ABC]]></property></object>
  <object class="Page"><id name="id">2</id><property name="title"><![CDATA[P]]></property><property name="contentStatus">current</property></object>
</hibernate-generic>`

// runAssessCmd executes "azimuthal assess <args>" and returns its output.
//
// It drives rootCmd rather than assessCmd, because cobra's Command.Execute
// delegates to c.Root(): calling assessCmd.Execute() runs the root command and
// silently ignores SetArgs on the child, so the subcommand never runs and the
// test passes on an error that was never produced.
func runAssessCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAssessFlags(t)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"assess"}, args...))
	err := rootCmd.Execute()
	return out.String(), err
}

// resetAssessFlags restores the package-level flag vars between runs, since
// cobra commands here are package singletons.
func resetAssessFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		assessJira, assessConfluence, assessOutput, assessJSON = "", "", "", false
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})
	assessJira, assessConfluence, assessOutput, assessJSON = "", "", "", false
}

func TestAssessCmd_ReadsAJiraZipAndReportsMarkdown(t *testing.T) {
	path := writeZip(t, "jira.zip", map[string]string{
		"entities.xml":                         miniJira,
		"activeobjects.xml":                    `<entity-engine-xml/>`,
		"data/attachments/10000/10000/1/10001": "not really a png",
	})

	out, err := runAssessCmd(t, "--jira", path)
	require.NoError(t, err)

	require.Contains(t, out, "# Migration assessment")
	require.Contains(t, out, "no database was contacted")
	require.Contains(t, out, "Jira issues → project items")
	require.Contains(t, out, "1 attachment blob", "the attachment tree is counted from the zip directory")
}

// TestAssessCmd_FindsEntitiesXMLWhateverItsDepth — Atlassian has a dedicated
// KB article for backups zipped one directory too high, so real archives carry
// entities.xml at both depths. Matching a fixed path would report a valid
// export as containing no data.
func TestAssessCmd_FindsEntitiesXMLWhateverItsDepth(t *testing.T) {
	nested := writeZip(t, "nested.zip", map[string]string{
		"Jira-backup-20260729/entities.xml": miniJira,
	})

	out, err := runAssessCmd(t, "--jira", nested)
	require.NoError(t, err)
	require.Contains(t, out, "Jira issues → project items")
	require.NotContains(t, out, "0 entities assessed")
}

func TestAssessCmd_BothExportsTogetherDetectTheSharedKey(t *testing.T) {
	jiraPath := writeZip(t, "jira.zip", map[string]string{"entities.xml": miniJira})
	confPath := writeZip(t, "conf.zip", map[string]string{"entities.xml": miniConfluence})

	out, err := runAssessCmd(t, "--jira", jiraPath, "--confluence", confPath)
	require.NoError(t, err)

	require.Contains(t, out, "## Item keys")
	require.Contains(t, out, "neither could reveal alone",
		"a key claimed by both exports is the case only a joint assessment can find")
}

func TestAssessCmd_JSONOutputIsValid(t *testing.T) {
	path := writeZip(t, "jira.zip", map[string]string{"entities.xml": miniJira})

	out, err := runAssessCmd(t, "--jira", path, "--json")
	require.NoError(t, err)

	var payload struct {
		Headline struct {
			Total    int     `json:"total"`
			CleanPct float64 `json:"clean_pct"`
		} `json:"headline"`
		Assumptions []string `json:"assumptions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	require.Positive(t, payload.Headline.Total)
	require.NotEmpty(t, payload.Assumptions)
}

func TestAssessCmd_WritesToAFileWithOutput(t *testing.T) {
	path := writeZip(t, "jira.zip", map[string]string{"entities.xml": miniJira})
	dest := filepath.Join(t.TempDir(), "report.md")

	_, err := runAssessCmd(t, "--jira", path, "--output", dest)
	require.NoError(t, err)

	body, readErr := os.ReadFile(dest) //nolint:gosec // G304 — a t.TempDir() path
	require.NoError(t, readErr)
	require.Contains(t, string(body), "# Migration assessment")
}

func TestAssessCmd_RefusesWithNoExport(t *testing.T) {
	_, err := runAssessCmd(t)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--jira")
}

func TestAssessCmd_ReportsAMissingArchiveClearly(t *testing.T) {
	_, err := runAssessCmd(t, "--jira", filepath.Join(t.TempDir(), "nope.zip"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "opening export archive")
}

func TestAssessCmd_ReportsAZipWithNoEntitiesFile(t *testing.T) {
	path := writeZip(t, "empty.zip", map[string]string{"readme.txt": "nothing here"})

	_, err := runAssessCmd(t, "--jira", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "entities.xml")
}

// TestAssessCmd_RefusesAConfluenceExportGivenAsJira — the two formats share the
// entry name, and the root element is what tells them apart. Assessing one as
// the other would produce a confidently wrong report.
func TestAssessCmd_RefusesAConfluenceExportGivenAsJira(t *testing.T) {
	path := writeZip(t, "conf.zip", map[string]string{"entities.xml": miniConfluence})

	_, err := runAssessCmd(t, "--jira", path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "entity-engine-xml",
		"the error must name what was expected, so the operator can swap the flag")
}

// TestAssessCmd_TakesNoConnectionFlag is the CLI half of the isolation
// guarantee: there is nowhere to put a DSN even if someone wanted to.
func TestAssessCmd_TakesNoConnectionFlag(t *testing.T) {
	for _, name := range []string{"dsn", "database-url", "db", "database", "url", "password"} {
		require.Nil(t, assessCmd.Flags().Lookup(name),
			"assess must not accept a %q flag", name)
	}

	// "help" is cobra's own, added when the command is first executed.
	var names []string
	assessCmd.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	require.Subset(t, []string{"jira", "confluence", "json", "output", "help"}, names,
		"the flag set is the whole surface; a new one needs a deliberate decision")
	require.Subset(t, names, []string{"jira", "confluence", "json", "output"})
}

// TestAssessCmd_IsRegisteredOnRoot — the subcommand has to be reachable.
func TestAssessCmd_IsRegisteredOnRoot(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() == "assess" {
			found = true
		}
	}
	require.True(t, found, "assess must be registered on the root command")
}

// TestAssessCmd_HelpDoesNotPromiseWriting — the long help is what an operator
// reads before pointing this at a production export.
func TestAssessCmd_HelpDoesNotPromiseWriting(t *testing.T) {
	long := strings.ToLower(assessCmd.Long)
	require.Contains(t, long, "nothing is written")
	require.Contains(t, long, "no database is contacted")
}
