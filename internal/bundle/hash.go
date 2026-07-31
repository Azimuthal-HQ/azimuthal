// Package bundle computes a content digest over a tree of files, so that the
// frontend compiled into the binary can be compared with the one on disk.
//
// It exists because go:embed reads web/dist at COMPILE time. A binary built
// before the frontend was rebuilt carries the older bundle, serves it happily,
// and passes every health check — the E2E suite then fails against a UI that no
// longer matches the source, and the failures look like anything except what
// they are. That has happened twice here: once as stale-at-build (the binary
// compiled before `npm run build`) and once as stale-after-rebase (the bundle
// left over from before a rebase), the second costing eight phantom E2E
// failures before anyone suspected the bundle.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"sort"
)

// Digest returns a hex-encoded SHA-256 over every regular file in fsys.
//
// Both the path and the content of each file go into the hash, in sorted path
// order, each length-delimited. The path matters because otherwise moving
// bytes between two files would not change the digest; the length delimiter
// matters because otherwise concatenation is ambiguous — "ab" + "c" and "a" +
// "bc" would hash alike.
//
// Directories contribute nothing. An empty one is not a difference worth
// failing a build over, and go:embed would not carry it across anyway, so
// counting it would make the two sides disagree for no reason.
func Digest(fsys fs.FS) (string, error) {
	paths, err := regularFiles(fsys)
	if err != nil {
		return "", err
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		if err := hashOne(h, fsys, p); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Count reports how many regular files fsys holds. It is only used to make a
// mismatch message concrete ("142 files" against "1 file" says immediately
// that a stub dist was embedded, which a pair of hashes does not).
func Count(fsys fs.FS) (int, error) {
	paths, err := regularFiles(fsys)
	if err != nil {
		return 0, err
	}
	return len(paths), nil
}

func regularFiles(fsys fs.FS) ([]string, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking bundle: %w", err)
	}
	return paths, nil
}

func hashOne(h io.Writer, fsys fs.FS, p string) error {
	f, err := fsys.Open(p)
	if err != nil {
		return fmt.Errorf("opening %s: %w", p, err)
	}
	defer func() { _ = f.Close() }()

	// sha256's Write never returns an error, but the io.Writer contract does
	// not say so — hence the explicit discards rather than an unchecked call.
	_, _ = fmt.Fprintf(h, "%s\n", p)
	n, err := io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("reading %s: %w", p, err)
	}
	_, _ = fmt.Fprintf(h, "\n%d\n", n)
	return nil
}
