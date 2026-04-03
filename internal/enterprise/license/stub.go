//go:build !enterprise

package license

// communityValidator always returns a community license with no enterprise features.
// It never performs RSA verification — there is no enterprise license key on a community install.
type communityValidator struct{}

// NewValidator returns the community LicenseValidator.
// In enterprise builds this function is replaced by an RSAValidator wired with
// the embedded production public key.
func NewValidator() LicenseValidator {
	return &communityValidator{}
}

// communityLicense is returned for every Validate call in the community edition.
var communityLicense = &License{
	Edition:  EditionCommunity,
	Features: nil, // No enterprise features on community installs.
}

// Validate ignores the key and always returns the community license.
// The key argument is accepted for interface compatibility but is not verified.
func (c *communityValidator) Validate(_ string) (*License, error) {
	return communityLicense, nil
}

// HasFeature always returns false in the community edition.
// No enterprise features are available without a valid enterprise license.
func (c *communityValidator) HasFeature(_ Feature) bool {
	return false
}
