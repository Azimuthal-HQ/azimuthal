package archive

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func buildZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.zip")

	f, err := os.Create(path) //nolint:gosec // G304 — a t.TempDir() path
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()

	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return path
}

func openZip(t *testing.T, entries map[string]string) *Archive {
	t.Helper()
	a, err := Open(buildZip(t, entries))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	return a
}

func TestOpen_ReportsAMissingArchive(t *testing.T) {
	t.Parallel()

	_, err := Open(filepath.Join(t.TempDir(), "absent.zip"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "opening export archive")
}

func TestOpen_ReportsANonZip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "notazip.zip")
	require.NoError(t, os.WriteFile(path, []byte("plain text, not an archive"), 0o600))

	_, err := Open(path)
	require.Error(t, err)
}

// TestFindByBase_MatchesWhateverTheDepth is the behaviour Atlassian's own
// troubleshooting article exists for: real backups arrive both with
// entities.xml at the root and nested one directory deeper. Matching a fixed
// path would report a perfectly good export as containing no data.
func TestFindByBase_MatchesWhateverTheDepth(t *testing.T) {
	t.Parallel()

	for name, entry := range map[string]string{
		"at the root": "entities.xml",
		"nested":      "Jira-backup-20260729/entities.xml",
		"deeply":      "a/b/c/entities.xml",
		"odd casing":  "ENTITIES.XML",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a := openZip(t, map[string]string{entry: "<x/>"})

			f, err := a.FindByBase("entities.xml")
			require.NoError(t, err)
			require.Equal(t, entry, f.Name)
		})
	}
}

// TestFindByBase_PrefersTheShallowestMatch — a bundled sub-archive directory
// must not shadow the real top-level export.
func TestFindByBase_PrefersTheShallowestMatch(t *testing.T) {
	t.Parallel()

	a := openZip(t, map[string]string{
		"entities.xml":            "<top/>",
		"nested/old/entities.xml": "<deep/>",
		"nested/entities.xml":     "<mid/>",
	})

	f, err := a.FindByBase("entities.xml")
	require.NoError(t, err)
	require.Equal(t, "entities.xml", f.Name)
}

func TestFindByBase_ReportsAbsence(t *testing.T) {
	t.Parallel()

	a := openZip(t, map[string]string{"readme.txt": "nothing"})

	_, err := a.FindByBase("entities.xml")
	require.ErrorIs(t, err, ErrEntryNotFound)
	require.Contains(t, err.Error(), "entities.xml")
}

func TestOpenBase_StreamsTheEntry(t *testing.T) {
	t.Parallel()

	a := openZip(t, map[string]string{"entities.xml": "<entity-engine-xml/>"})

	rc, err := a.OpenBase("entities.xml")
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()

	body, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "<entity-engine-xml/>", string(body))
}

// TestOpenEntry_EnforcesTheDecompressionBound is the G110 guarantee.
//
// A zip entry's uncompressed size is declared in a header the archive itself
// controls, so it is a claim and not a limit. The bound is what stops a small
// entry that decompresses enormously, and it is enforced on the read rather
// than trusted from the header. Fails before the fix (an unbounded io.Copy),
// passes after — verified by lowering the bound below a known body size.
func TestOpenEntry_EnforcesTheDecompressionBound(t *testing.T) {
	t.Parallel()

	// Highly compressible: small on disk, large when expanded, which is the
	// decompression-bomb shape.
	body := strings.Repeat("A", 200_000)
	a := openZip(t, map[string]string{"entities.xml": body})

	// Generous bound: the whole entry is readable.
	rc, err := a.OpenBase("entities.xml")
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Len(t, got, len(body))

	// Bound below the entry: the read stops and says why.
	a.SetMaxEntryBytes(1000)
	rc, err = a.OpenBase("entities.xml")
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()

	_, err = io.ReadAll(rc)
	require.ErrorIs(t, err, ErrEntryTooLarge,
		"a bound that is not enforced on the read is not a bound")
}

func TestCountUnder_CountsTheAttachmentTree(t *testing.T) {
	t.Parallel()

	a := openZip(t, map[string]string{
		"entities.xml":               "<x/>",
		"data/attachments/1/10001":   "aaaa",
		"data/attachments/1/2/10002": "bbbbbb",
		"data/avatars/9000":          "not an attachment",
		"attachments/loose":          "cc",
	})

	count, declared := a.CountUnder("attachments")
	require.Equal(t, 3, count, "both the nested Jira tree and a loose top-level tree count")
	require.Equal(t, uint64(4+6+2), declared)

	none, zero := a.CountUnder("nothing-here")
	require.Zero(t, none)
	require.Zero(t, zero)
}

func TestNames_AreSortedForStableOutput(t *testing.T) {
	t.Parallel()

	a := openZip(t, map[string]string{"z.xml": "1", "a.xml": "2", "m.xml": "3"})
	require.Equal(t, []string{"a.xml", "m.xml", "z.xml"}, a.Names())
}

// TestArchive_NeverWritesAnything — the package extracts nothing and resolves
// no entry name against the filesystem, which is why zip-slip cannot occur
// here. This asserts the absence of the API that would make it possible.
func TestArchive_NeverWritesAnything(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("archive.go")
	require.NoError(t, err)
	body := string(src)

	for _, banned := range []string{"os.Create", "os.WriteFile", "os.MkdirAll", "io.Copy(", "os.OpenFile"} {
		require.NotContains(t, body, banned,
			"the archive package must not gain a write path: found %q", banned)
	}
}
