// Package license defines the LicenseValidator interface and handles license key verification.
// License keys are RSA-signed JSON payloads encoded as base64url(json).base64url(signature).
// The private key used for signing is held exclusively in the Azimuthal key-issuance service;
// only the public key is embedded here for verification.
package license

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrEnterpriseRequired is returned when a caller requests a feature unavailable
// in the community edition.
var ErrEnterpriseRequired = errors.New(
	"this feature requires an enterprise license — see azimuthal.com/enterprise",
)

// ErrInvalidLicense is returned when a license key fails signature verification
// or has expired.
var ErrInvalidLicense = errors.New("invalid or expired license key")

// Edition identifies the product tier granted by a license.
type Edition string

const (
	// EditionCommunity is the open-core Apache 2.0 edition.
	EditionCommunity Edition = "community"
	// EditionEnterprise is the full proprietary enterprise edition.
	EditionEnterprise Edition = "enterprise"
)

// Feature is a named capability that may or may not be included in a license.
type Feature string

const (
	// FeatureSSO enables SAML/OIDC single sign-on.
	FeatureSSO Feature = "sso"
	// FeatureAuditLog enables the append-only audit log.
	FeatureAuditLog Feature = "audit_log"
	// FeatureRBAC enables the advanced attribute-based permissions engine.
	FeatureRBAC Feature = "rbac"
	// FeatureAnalytics enables the usage and performance reporting suite.
	FeatureAnalytics Feature = "analytics"
	// FeatureCustomFields enables custom field definitions on items.
	FeatureCustomFields Feature = "custom_fields"
	// FeatureUnlimitedUsers removes the user-count cap imposed on community installs.
	FeatureUnlimitedUsers Feature = "unlimited_users"
)

// License describes the entitlements granted by a validated license key.
type License struct {
	// Edition is the product tier (community or enterprise).
	Edition Edition
	// OrgID is the organisation this license was issued to.
	OrgID string
	// IssuedAt is when the license was created.
	IssuedAt time.Time
	// ExpiresAt is when the license expires. Zero value means it never expires.
	ExpiresAt time.Time
	// Features is the set of enterprise features enabled by this license.
	Features []Feature
}

// HasFeature reports whether f is included in this license.
func (l *License) HasFeature(f Feature) bool {
	for _, feat := range l.Features {
		if feat == f {
			return true
		}
	}
	return false
}

// licensePayload is the JSON structure embedded in the license key before signing.
type licensePayload struct {
	Edition   string   `json:"edition"`
	OrgID     string   `json:"org_id"`
	IssuedAt  int64    `json:"issued_at"`
	ExpiresAt int64    `json:"expires_at"`
	Features  []string `json:"features"`
}

// Validator verifies license keys and reports which features are active.
// Use NewRSAValidator to construct an instance with a PEM-encoded RSA public key.
type Validator interface {
	// Validate parses and cryptographically verifies a license key.
	// Returns the decoded License on success, or ErrInvalidLicense if the key
	// is malformed, tampered with, or expired.
	Validate(key string) (*License, error)

	// HasFeature reports whether the currently active license includes feature f.
	HasFeature(f Feature) bool
}

// RSAValidator verifies license keys signed with an RSA-PSS private key.
type RSAValidator struct {
	publicKey *rsa.PublicKey
	active    *License
}

// NewRSAValidator constructs an RSAValidator that verifies keys against the
// PEM-encoded RSA public key provided in publicKeyPEM.
func NewRSAValidator(publicKeyPEM []byte) (*RSAValidator, error) {
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("parsing public key: no PEM block found")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("parsing public key: expected RSA key, got %T", pub)
	}

	return &RSAValidator{publicKey: rsaPub}, nil
}

// Validate parses and cryptographically verifies a license key.
//
// Key format: base64url(json_payload) + "." + base64url(rsa_pss_sha256_signature)
//
// The RSA-PSS signature covers the raw base64url-encoded payload bytes (not the decoded JSON).
func (v *RSAValidator) Validate(key string) (*License, error) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("%w: malformed key format", ErrInvalidLicense)
	}

	payloadB64, sigB64 := parts[0], parts[1]

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding payload: %w", ErrInvalidLicense, err)
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, fmt.Errorf("%w: decoding signature: %w", ErrInvalidLicense, err)
	}

	// Verify the signature covers the raw encoded payload bytes.
	digest := sha256.Sum256([]byte(payloadB64))
	err = rsa.VerifyPSS(
		v.publicKey,
		crypto.SHA256,
		digest[:],
		sigBytes,
		&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash},
	)
	if err != nil {
		return nil, fmt.Errorf("%w: signature verification failed", ErrInvalidLicense)
	}

	var payload licensePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("%w: decoding license payload: %w", ErrInvalidLicense, err)
	}

	lic := &License{
		Edition:  Edition(payload.Edition),
		OrgID:    payload.OrgID,
		IssuedAt: time.Unix(payload.IssuedAt, 0).UTC(),
	}

	if payload.ExpiresAt > 0 {
		lic.ExpiresAt = time.Unix(payload.ExpiresAt, 0).UTC()
		if time.Now().UTC().After(lic.ExpiresAt) {
			return nil, fmt.Errorf("%w: license expired at %s", ErrInvalidLicense, lic.ExpiresAt.Format(time.RFC3339))
		}
	}

	for _, f := range payload.Features {
		lic.Features = append(lic.Features, Feature(f))
	}

	v.active = lic
	return lic, nil
}

// HasFeature reports whether the most recently validated license includes feature f.
// Returns false if no license has been validated yet.
func (v *RSAValidator) HasFeature(f Feature) bool {
	if v.active == nil {
		return false
	}
	return v.active.HasFeature(f)
}
