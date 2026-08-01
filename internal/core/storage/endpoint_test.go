package storage_test

import (
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// The scheme/STORAGE_USE_SSL interaction is published to operators in the
// environment tables of both README.md and docs/self-hosting.md, and until now
// was asserted nowhere. Every row below is a sentence from those tables.
//
// The negative-test question (CLAUDE.md §2): delete the `https://` arm and rows
// 1-2 fail; delete the `http://` arm and rows 3-4 fail on the endpoint; collapse
// the http arm into "always false" and row 4 fails on useSSL. No row passes
// vacuously.
func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		name         string
		endpoint     string
		useSSL       bool
		wantEndpoint string
		wantSSL      bool
	}{
		{
			// "An `https://` prefix forces this to `true` regardless of what
			// you set" — the asymmetry that fails safe.
			name:     "https overrides an explicit STORAGE_USE_SSL=false",
			endpoint: "https://minio.example.com:9000", useSSL: false,
			wantEndpoint: "minio.example.com:9000", wantSSL: true,
		},
		{
			name:     "https with STORAGE_USE_SSL=true stays true",
			endpoint: "https://minio.example.com:9000", useSSL: true,
			wantEndpoint: "minio.example.com:9000", wantSSL: true,
		},
		{
			// "`http://` leaves your setting alone" — TLS is never turned off.
			name:     "http does not turn off an explicit STORAGE_USE_SSL=true",
			endpoint: "http://minio.example.com:9000", useSSL: true,
			wantEndpoint: "minio.example.com:9000", wantSSL: true,
		},
		{
			// This is the shipped build/docker-compose.yml's exact value, and
			// the one the backup and restore commands got wrong: unnormalised,
			// minio-go was handed a host literally named "http://storage".
			name:     "the bundled compose endpoint",
			endpoint: "http://storage:9000", useSSL: false,
			wantEndpoint: "storage:9000", wantSSL: false,
		},
		{
			name:     "a scheme-less endpoint takes STORAGE_USE_SSL verbatim (false)",
			endpoint: "storage:9000", useSSL: false,
			wantEndpoint: "storage:9000", wantSSL: false,
		},
		{
			name:     "a scheme-less endpoint takes STORAGE_USE_SSL verbatim (true)",
			endpoint: "storage:9000", useSSL: true,
			wantEndpoint: "storage:9000", wantSSL: true,
		},
		{
			// A blank endpoint means "no object store"; the callers gate on it
			// before reaching here, but it must not be mangled into something
			// non-empty that would then look configured.
			name:     "a blank endpoint stays blank",
			endpoint: "", useSSL: false,
			wantEndpoint: "", wantSSL: false,
		},
		{
			// Only the leading scheme is stripped — a host containing the
			// substring must survive intact.
			name:     "only a leading scheme is stripped",
			endpoint: "s3.http://not-a-scheme.example:9000", useSSL: false,
			wantEndpoint: "s3.http://not-a-scheme.example:9000", wantSSL: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotEndpoint, gotSSL := storage.NormalizeEndpoint(c.endpoint, c.useSSL)
			if gotEndpoint != c.wantEndpoint {
				t.Errorf("endpoint: got %q, want %q", gotEndpoint, c.wantEndpoint)
			}
			if gotSSL != c.wantSSL {
				t.Errorf("useSSL: got %v, want %v", gotSSL, c.wantSSL)
			}
		})
	}
}

// TestNormalizeEndpoint_NeverDisablesTLS states the safety property the case
// table above implies but does not name: whatever the operator writes, this
// function does not turn TLS off. It would catch a future rewrite that
// "simplified" the http arm into an unconditional `false`.
func TestNormalizeEndpoint_NeverDisablesTLS(t *testing.T) {
	for _, endpoint := range []string{
		"https://minio.example.com:9000",
		"http://minio.example.com:9000",
		"minio.example.com:9000",
		"",
	} {
		if _, gotSSL := storage.NormalizeEndpoint(endpoint, true); !gotSSL {
			t.Errorf("NormalizeEndpoint(%q, true) turned TLS off", endpoint)
		}
	}
}
