// Package archive opens the zip an export arrives in and hands out streaming
// readers for the entries inside it.
//
// Both formats the assessor reads — a Jira Cloud backup and a Confluence space
// export — are zips containing an entities.xml plus a tree of attachment blobs,
// so the opening, the entry lookup and the decompression bounds live here once
// rather than twice.
//
// Nothing in this package writes. It never extracts to disk, never creates a
// file, and never resolves an entry name against the filesystem: entries are
// read as streams and their names are only ever compared. That is why the
// zip-slip class of defect cannot occur here — there is no destination path to
// traverse to.
package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// DefaultMaxEntryBytes bounds how much any single entry may decompress to.
//
// A zip entry declares its uncompressed size in a header the archive itself
// controls, so the declared size is a hint and not a limit — this is the
// decompression-bomb shape gosec flags as G110. Every read here is wrapped in
// an io.LimitedReader so a 100 KB entry claiming to be 4 GB is stopped at the
// bound rather than at the machine's memory ceiling. 2 GiB is far above any
// real entities.xml and far below anything that would matter.
const DefaultMaxEntryBytes int64 = 2 << 30

// ErrEntryNotFound reports that no entry matched.
var ErrEntryNotFound = errors.New("archive: entry not found")

// ErrEntryTooLarge reports that an entry exceeded the decompression bound.
var ErrEntryTooLarge = errors.New("archive: entry exceeds the decompression limit")

// Archive is an opened export zip.
type Archive struct {
	rc            *zip.ReadCloser
	maxEntryBytes int64
}

// Open opens an export zip for reading.
//
// The path comes from a CLI flag the operator typed, which is the same trust
// position as "azimuthal restore --input"; see the #nosec note at the call.
func Open(zipPath string) (*Archive, error) {
	rc, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening export archive %q: %w", zipPath, err)
	}
	return &Archive{rc: rc, maxEntryBytes: DefaultMaxEntryBytes}, nil
}

// Close releases the archive.
func (a *Archive) Close() error {
	if err := a.rc.Close(); err != nil {
		return fmt.Errorf("closing export archive: %w", err)
	}
	return nil
}

// SetMaxEntryBytes overrides the per-entry decompression bound. Used by tests
// to prove the bound is enforced without building a multi-gigabyte fixture.
func (a *Archive) SetMaxEntryBytes(n int64) { a.maxEntryBytes = n }

// Names lists every entry in the archive, sorted, so report output over the
// same archive does not vary between runs.
func (a *Archive) Names() []string {
	out := make([]string, 0, len(a.rc.File))
	for _, f := range a.rc.File {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

// FindByBase returns the entry whose final path segment equals base,
// case-insensitively.
//
// Lookup is by basename rather than by full path on purpose. Atlassian's own
// documentation has a dedicated troubleshooting article for backups zipped one
// directory too high, so real archives carry entities.xml at the root in some
// cases and under a "Jira-backup-20260101/" prefix in others. Matching the full
// path would fail on the second shape, and the failure would look like "this
// export contains no data".
//
// When several entries share a basename the shallowest wins, so a genuine
// top-level entities.xml is preferred over one nested inside a bundled
// sub-archive directory.
func (a *Archive) FindByBase(base string) (*zip.File, error) {
	var best *zip.File
	bestDepth := -1
	for _, f := range a.rc.File {
		if !strings.EqualFold(path.Base(f.Name), base) {
			continue
		}
		depth := strings.Count(path.Clean(f.Name), "/")
		if best == nil || depth < bestDepth {
			best, bestDepth = f, depth
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w: no entry named %q in the archive", ErrEntryNotFound, base)
	}
	return best, nil
}

// entryReader is a bounded reader over one zip entry that closes the underlying
// stream and reports when the bound was hit.
type entryReader struct {
	rc      io.ReadCloser
	limited *io.LimitedReader
	name    string
}

func (e *entryReader) Read(p []byte) (int, error) {
	n, err := e.limited.Read(p)
	if errors.Is(err, io.EOF) && e.limited.N <= 0 {
		return n, fmt.Errorf("%w: %q stopped at the limit", ErrEntryTooLarge, e.name)
	}
	// io.Reader.Read must return io.EOF unwrapped for callers to detect the end.
	return n, err //nolint:wrapcheck // preserving io.EOF identity is the contract
}

func (e *entryReader) Close() error {
	if err := e.rc.Close(); err != nil {
		return fmt.Errorf("closing archive entry %q: %w", e.name, err)
	}
	return nil
}

// OpenEntry returns a bounded streaming reader over one entry.
//
// The caller must Close it. The reader never buffers the whole entry: an
// entities.xml of several gigabytes is decoded token by token, which is the
// property the assessor's memory bound rests on.
func (a *Archive) OpenEntry(f *zip.File) (io.ReadCloser, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("opening archive entry %q: %w", f.Name, err)
	}
	// G110: the bound, not the entry's self-declared UncompressedSize, is what
	// stops a decompression bomb. One extra byte is allowed so hitting the
	// limit is distinguishable from ending exactly at it.
	return &entryReader{
		rc:      rc,
		limited: &io.LimitedReader{R: rc, N: a.maxEntryBytes + 1},
		name:    f.Name,
	}, nil
}

// OpenBase finds an entry by basename and opens a bounded reader over it.
func (a *Archive) OpenBase(base string) (io.ReadCloser, error) {
	f, err := a.FindByBase(base)
	if err != nil {
		return nil, err
	}
	return a.OpenEntry(f)
}

// CountUnder returns how many entries sit under the given directory prefix, and
// their total declared uncompressed size.
//
// Used for the attachment trees (Jira's data/attachments, Confluence's
// attachments), where the assessor reports how many blobs an import would have
// to carry without reading any of them. The size is the archive's own claim,
// which is why it is reported as declared rather than measured.
func (a *Archive) CountUnder(prefix string) (count int, declaredBytes uint64) {
	norm := strings.ToLower(strings.TrimSuffix(prefix, "/")) + "/"
	for _, f := range a.rc.File {
		name := strings.ToLower(path.Clean(f.Name))
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.Contains(name+"/", "/"+norm) || strings.HasPrefix(name, norm) {
			count++
			declaredBytes += f.UncompressedSize64
		}
	}
	return count, declaredBytes
}
