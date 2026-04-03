package sso

import (
	"errors"
	"net/http"
)

// ErrEnterpriseRequired is returned by community stubs for enterprise-only features.
var ErrEnterpriseRequired = errors.New(
	"this feature requires an enterprise license — see azimuthal.com/enterprise",
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
// The community build includes only a stub implementation — the real
// implementation lives in the enterprise repository.
type Provider interface {
	// BeginAuth initiates the SSO flow by redirecting the user to the identity provider.
	BeginAuth(w http.ResponseWriter, r *http.Request) error
	// CompleteAuth handles the identity-provider callback and returns the authenticated user.
	CompleteAuth(r *http.Request) (*User, error)
	// IsAvailable reports whether an SSO provider has been configured and is ready.
	IsAvailable() bool
}
