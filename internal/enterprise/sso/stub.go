//go:build !enterprise

package sso

import (
	"net/http"
)

// communityProvider is the community-edition stub for the SSOProvider interface.
// All methods return ErrEnterpriseRequired to direct users to the enterprise offering.
type communityProvider struct{}

// NewProvider returns the community stub SSOProvider.
// In enterprise builds this function is replaced by the real SAML/OIDC implementation.
func NewProvider() SSOProvider {
	return &communityProvider{}
}

// BeginAuth always returns ErrEnterpriseRequired in the community edition.
func (p *communityProvider) BeginAuth(_ http.ResponseWriter, _ *http.Request) error {
	return ErrEnterpriseRequired
}

// CompleteAuth always returns ErrEnterpriseRequired in the community edition.
func (p *communityProvider) CompleteAuth(_ *http.Request) (*SSOUser, error) {
	return nil, ErrEnterpriseRequired
}

// IsAvailable always returns false in the community edition.
func (p *communityProvider) IsAvailable() bool {
	return false
}
