package storage

import "strings"

// NormalizeEndpoint splits an operator-supplied STORAGE_ENDPOINT into the bare
// host:port that minio-go wants and the TLS flag that goes with it.
// STORAGE_ENDPOINT may carry an http(s):// scheme — the bundled
// build/docker-compose.yml sets `http://storage:9000`, and .env.test does the
// same — while minio.New wants the scheme stripped and Secure set separately.
//
// The scheme and STORAGE_USE_SSL do not compose symmetrically, and the rule is
// that this function never turns TLS off. An `https://` endpoint sets useSSL
// unconditionally, so it overrides an explicit STORAGE_USE_SSL=false; an
// `http://` endpoint leaves useSSL alone, so an explicit STORAGE_USE_SSL=true
// survives it. Only a scheme-less endpoint takes STORAGE_USE_SSL verbatim in
// both directions.
//
// Both asymmetries fail safe — the disagreement resolves towards TLS either
// way — so this is written down rather than changed. An operator who asks for
// plaintext against an `https://` endpoint has asked for something
// contradictory, and gets the encrypted reading of it. README.md and
// docs/self-hosting.md both publish this rule in their environment tables, so
// changing either direction contradicts shipped documentation.
//
// This lives here, beside NewS3Store, rather than in any one caller, because
// three separate copies of the rule had already grown — and two of the three
// were wrong in a way nothing detected. `azimuthal backup` and
// `azimuthal restore` passed STORAGE_ENDPOINT to minio.New unmodified, so on
// the shipped Compose file they addressed a host literally named
// "http://storage" (ledgered as D105).
func NormalizeEndpoint(endpoint string, useSSL bool) (string, bool) {
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		return strings.TrimPrefix(endpoint, "https://"), true
	case strings.HasPrefix(endpoint, "http://"):
		return strings.TrimPrefix(endpoint, "http://"), useSSL
	default:
		return endpoint, useSSL
	}
}
