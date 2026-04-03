//go:build enterprise

// Package license defines the LicenseValidator interface and handles license key verification.
// This file is a compile-time placeholder; the azimuthal-ee private repo replaces
// NewValidator with an RSAValidator wired to the production public key.
package license

// enterpriseValidator is a compile-time placeholder that satisfies Validator
// when building with the enterprise tag in the community repository.
// The azimuthal-ee private repo replaces NewValidator with a real RSAValidator.
type enterpriseValidator struct{}

// NewValidator returns a placeholder LicenseValidator for enterprise builds in the
// community repository. The azimuthal-ee private repo provides an RSAValidator
// pre-configured with the production signing public key.
func NewValidator() Validator {
	return &enterpriseValidator{}
}

// Validate returns the community license — the azimuthal-ee repo provides the real validator.
func (e *enterpriseValidator) Validate(_ string) (*License, error) {
	return &License{Edition: EditionCommunity}, nil
}

// HasFeature always returns false — the real validator is in azimuthal-ee.
func (e *enterpriseValidator) HasFeature(_ Feature) bool {
	return false
}
