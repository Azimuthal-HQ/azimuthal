package bundle_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/bundle"
)

func mapFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for name, body := range files {
		out[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return out
}

func digest(t *testing.T, files map[string]string) string {
	t.Helper()
	d, err := bundle.Digest(mapFS(files))
	require.NoError(t, err)
	require.Len(t, d, 64, "sha256 hex is 64 characters")
	return d
}

// TestDigest_IsStableAcrossWalkOrder pins the property the whole preflight
// rests on: the same content always produces the same digest, so a mismatch
// means a real difference rather than a different iteration order.
func TestDigest_IsStableAcrossWalkOrder(t *testing.T) {
	files := map[string]string{
		"index.html":            "<!doctype html><script src=/assets/index-A.js>",
		"assets/index-A.js":     "console.log(1)",
		"assets/index-B.css":    "body{}",
		"favicon.svg":           "<svg/>",
		"nested/deep/thing.txt": "x",
	}
	first := digest(t, files)
	for range 5 {
		require.Equal(t, first, digest(t, files))
	}
}

// TestDigest_ChangesOnEveryKindOfDifference is the negative-test question
// applied directly: each case below must produce a DIFFERENT digest, or the
// preflight would pass on a bundle that had genuinely changed.
func TestDigest_ChangesOnEveryKindOfDifference(t *testing.T) {
	base := map[string]string{
		"index.html":        "<!doctype html>",
		"assets/index-A.js": "console.log(1)",
	}
	baseline := digest(t, base)

	cases := []struct {
		name  string
		files map[string]string
	}{
		{"content edited", map[string]string{
			"index.html": "<!doctype html>", "assets/index-A.js": "console.log(2)",
		}},
		{"file added", map[string]string{
			"index.html": "<!doctype html>", "assets/index-A.js": "console.log(1)",
			"assets/extra.js": "",
		}},
		{"file removed", map[string]string{
			"index.html": "<!doctype html>",
		}},
		{"file renamed, content identical", map[string]string{
			"index.html": "<!doctype html>", "assets/index-B.js": "console.log(1)",
		}},
		// The two below are why path and length are hashed alongside content.
		{"content moved between files", map[string]string{
			"index.html": "", "assets/index-A.js": "<!doctype html>console.log(1)",
		}},
		{"same bytes split differently", map[string]string{
			"index.html": "<!doctype", "assets/index-A.js": " html>console.log(1)",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotEqual(t, baseline, digest(t, tc.files),
				"this difference must change the digest, or a stale bundle passes the preflight")
		})
	}
}

// TestDigest_IgnoresEmptyDirectories documents the one difference the digest
// deliberately does not see, so the embedded and on-disk sides agree: go:embed
// carries no empty directory, and an on-disk one is not a bundle difference.
func TestDigest_IgnoresEmptyDirectories(t *testing.T) {
	withFile := mapFS(map[string]string{"a/keep.txt": "x"})
	withExtraDir := mapFS(map[string]string{"a/keep.txt": "x"})
	withExtraDir["b"] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}

	a, err := bundle.Digest(withFile)
	require.NoError(t, err)
	b, err := bundle.Digest(withExtraDir)
	require.NoError(t, err)
	require.Equal(t, a, b)
}

func TestCount_CountsRegularFilesOnly(t *testing.T) {
	n, err := bundle.Count(mapFS(map[string]string{
		"index.html": "x", "assets/a.js": "y", "assets/deep/b.css": "z",
	}))
	require.NoError(t, err)
	require.Equal(t, 3, n)

	// The stub dist CI writes for go:embed — one file. This is the case Count
	// exists to make legible in a failure message.
	n, err = bundle.Count(mapFS(map[string]string{"index.html": "stub"}))
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestDigest_EmptyBundleIsNotAnError(t *testing.T) {
	d, err := bundle.Digest(fstest.MapFS{})
	require.NoError(t, err)
	require.Len(t, d, 64)

	// But it must not collide with a bundle that has a file in it.
	require.NotEqual(t, d, digest(t, map[string]string{"index.html": ""}))
}
