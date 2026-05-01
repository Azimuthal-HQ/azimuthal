// Package sso defines the SSOProvider interface for SAML/OIDC authentication.
// SSO is a standard feature available to all Azimuthal users.
package sso

import (
	"net/http"
)

// User holds the identity information returned by an SSO provider after
// successful authentication. The auth layer maps this to an internal User.
type User struct {
	// ExternalID is the subject/nameID issued by the identity provider.
	ExternalID string
	// Email is the user's email address from the identity provider.
	Email string
	// DisplayName is the human-readable name from the identity provider.
	DisplayName string
	// ProviderName identifies which SSO provider authenticated the user (e.g. "saml", "oidc").
	ProviderName string
}

// Provider defines the interface for SAML/OIDC authentication.
type Provider interface {
	// BeginAuth initiates the SSO flow by redirecting the user to the identity provider.
	BeginAuth(w http.ResponseWriter, r *http.Request) error
	// CompleteAuth handles the identity-provider callback and returns the authenticated user.
	CompleteAuth(r *http.Request) (*User, error)
	// IsAvailable reports whether an SSO provider has been configured and is ready.
	IsAvailable() bool
}

// defaultProvider is a no-op implementation returned when SSO has not been configured.
// Once SAML/OIDC configuration is wired in, this will be replaced by a real provider.
type defaultProvider struct{}

// NewProvider returns the default SSO Provider.
// Returns a no-op provider until SAML/OIDC configuration is implemented.
func NewProvider() Provider {
	return &defaultProvider{}
}

// ErrNotConfigured is returned when SSO methods are called but no provider is configured.
var ErrNotConfigured = errorString("SSO provider not configured — configure SAML/OIDC in admin settings")

type errorString string

func (e errorString) Error() string { return string(e) }

// BeginAuth returns ErrNotConfigured until an SSO provider is configured.
func (p *defaultProvider) BeginAuth(_ http.ResponseWriter, _ *http.Request) error {
	return ErrNotConfigured
}

// CompleteAuth returns ErrNotConfigured until an SSO provider is configured.
func (p *defaultProvider) CompleteAuth(_ *http.Request) (*User, error) {
	return nil, ErrNotConfigured
}

// IsAvailable returns false until an SSO provider is configured.
func (p *defaultProvider) IsAvailable() bool {
	return false
}
