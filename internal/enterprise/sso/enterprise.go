//go:build enterprise

// Package sso defines the Provider interface and its community stub.
// The real SAML/OIDC implementation lives in the enterprise repository.
// This file is a compile-time placeholder; the azimuthal-ee private repo
// replaces NewProvider with the real SAML/OIDC implementation.
package sso

import "net/http"

// enterpriseProvider is a placeholder that satisfies the Provider interface
// when building with the enterprise tag in the community repository.
// The azimuthal-ee private repo replaces this file entirely.
type enterpriseProvider struct{}

// NewProvider returns a placeholder Provider for enterprise builds in the
// community repository. The azimuthal-ee private repo provides the real implementation.
func NewProvider() Provider {
	return &enterpriseProvider{}
}

// BeginAuth returns ErrEnterpriseRequired — the community repo does not contain
// the real enterprise SSO logic. The azimuthal-ee private repo provides it.
func (e *enterpriseProvider) BeginAuth(_ http.ResponseWriter, _ *http.Request) error {
	return ErrEnterpriseRequired
}

// CompleteAuth returns ErrEnterpriseRequired — see BeginAuth.
func (e *enterpriseProvider) CompleteAuth(_ *http.Request) (*User, error) {
	return nil, ErrEnterpriseRequired
}

// IsAvailable returns false — the real implementation is in azimuthal-ee.
func (e *enterpriseProvider) IsAvailable() bool {
	return false
}
