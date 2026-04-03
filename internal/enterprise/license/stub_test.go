//go:build !enterprise

package license_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/license"
)

// ── Community stub tests ──────────────────────────────────────────────────────

func TestCommunityValidator_AlwaysReturnsCommunityLicense(t *testing.T) {
	v := license.NewValidator()
	lic, err := v.Validate("any-key-at-all")
	if err != nil {
		t.Fatalf("community validator must never error, got: %v", err)
	}
	if lic == nil {
		t.Fatal("community validator must return a non-nil license")
	}
	if lic.Edition != license.EditionCommunity {
		t.Errorf("expected EditionCommunity, got %v", lic.Edition)
	}
}

func TestCommunityValidator_HasFeature_AlwaysFalse(t *testing.T) {
	v := license.NewValidator()
	features := []license.Feature{
		license.FeatureSSO,
		license.FeatureAuditLog,
		license.FeatureRBAC,
		license.FeatureAnalytics,
		license.FeatureCustomFields,
		license.FeatureUnlimitedUsers,
	}
	for _, f := range features {
		if v.HasFeature(f) {
			t.Errorf("community edition should not have feature %s", f)
		}
	}
}

func TestCommunityValidator_ImplementsInterface(_ *testing.T) {
	var _ = license.NewValidator()
}

// ── RSAValidator tests ────────────────────────────────────────────────────────

// generateTestKeyPair creates a throwaway RSA key pair for test use.
func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test RSA key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshaling public key: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return priv, pubPEM
}

// signLicense creates a valid license key signed with priv.
func signLicense(t *testing.T, priv *rsa.PrivateKey, payload map[string]interface{}) string {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshaling license payload: %v", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)
	digest := sha256.Sum256([]byte(payloadB64))
	sig, err := rsa.SignPSS(rand.Reader, priv, crypto.SHA256, digest[:],
		&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		t.Fatalf("signing license: %v", err)
	}
	return payloadB64 + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestRSAValidator_ValidLicenseKey(t *testing.T) {
	priv, pubPEM := generateTestKeyPair(t)
	v, err := license.NewRSAValidator(pubPEM)
	if err != nil {
		t.Fatalf("constructing validator: %v", err)
	}

	key := signLicense(t, priv, map[string]interface{}{
		"edition":    "enterprise",
		"org_id":     "org-test",
		"issued_at":  time.Now().Unix(),
		"expires_at": time.Now().Add(365 * 24 * time.Hour).Unix(),
		"features":   []string{"sso", "audit_log"},
	})

	lic, err := v.Validate(key)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if lic.Edition != license.EditionEnterprise {
		t.Errorf("expected enterprise edition, got %v", lic.Edition)
	}
	if lic.OrgID != "org-test" {
		t.Errorf("expected org-test, got %v", lic.OrgID)
	}
	if !lic.HasFeature(license.FeatureSSO) {
		t.Error("expected SSO feature to be present")
	}
	if !lic.HasFeature(license.FeatureAuditLog) {
		t.Error("expected AuditLog feature to be present")
	}
	if lic.HasFeature(license.FeatureRBAC) {
		t.Error("did not expect RBAC feature to be present")
	}
}

func TestRSAValidator_HasFeature_AfterValidate(t *testing.T) {
	priv, pubPEM := generateTestKeyPair(t)
	v, err := license.NewRSAValidator(pubPEM)
	if err != nil {
		t.Fatalf("constructing validator: %v", err)
	}

	key := signLicense(t, priv, map[string]interface{}{
		"edition":    "enterprise",
		"org_id":     "org-test",
		"issued_at":  time.Now().Unix(),
		"expires_at": 0,
		"features":   []string{"analytics"},
	})
	if _, err := v.Validate(key); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if !v.HasFeature(license.FeatureAnalytics) {
		t.Error("expected analytics feature after validate")
	}
	if v.HasFeature(license.FeatureSSO) {
		t.Error("did not expect SSO feature")
	}
}

func TestRSAValidator_ExpiredLicense(t *testing.T) {
	priv, pubPEM := generateTestKeyPair(t)
	v, err := license.NewRSAValidator(pubPEM)
	if err != nil {
		t.Fatalf("constructing validator: %v", err)
	}

	key := signLicense(t, priv, map[string]interface{}{
		"edition":    "enterprise",
		"org_id":     "org-test",
		"issued_at":  time.Now().Add(-2 * 365 * 24 * time.Hour).Unix(),
		"expires_at": time.Now().Add(-24 * time.Hour).Unix(), // expired yesterday
		"features":   []string{"sso"},
	})

	_, err = v.Validate(key)
	if err == nil {
		t.Fatal("expected error for expired license, got nil")
	}
}

func TestRSAValidator_TamperedPayload(t *testing.T) {
	priv, pubPEM := generateTestKeyPair(t)
	v, err := license.NewRSAValidator(pubPEM)
	if err != nil {
		t.Fatalf("constructing validator: %v", err)
	}

	key := signLicense(t, priv, map[string]interface{}{
		"edition":    "enterprise",
		"org_id":     "org-legitimate",
		"issued_at":  time.Now().Unix(),
		"expires_at": time.Now().Add(365 * 24 * time.Hour).Unix(),
		"features":   []string{},
	})

	// Tamper: replace the payload part with a different org_id.
	tampered := map[string]interface{}{
		"edition":    "enterprise",
		"org_id":     "org-attacker",
		"issued_at":  time.Now().Unix(),
		"expires_at": time.Now().Add(365 * 24 * time.Hour).Unix(),
		"features":   []string{"sso", "audit_log", "rbac", "analytics"},
	}
	tamperedBytes, _ := json.Marshal(tampered)
	parts := splitKey(key)
	tamperedKey := base64.RawURLEncoding.EncodeToString(tamperedBytes) + "." + parts[1]

	_, err = v.Validate(tamperedKey)
	if err == nil {
		t.Fatal("expected error for tampered license, got nil")
	}
}

func TestRSAValidator_WrongPublicKey(t *testing.T) {
	priv, _ := generateTestKeyPair(t)
	_, pubPEM2 := generateTestKeyPair(t) // different key pair

	v, err := license.NewRSAValidator(pubPEM2)
	if err != nil {
		t.Fatalf("constructing validator: %v", err)
	}

	key := signLicense(t, priv, map[string]interface{}{
		"edition":   "enterprise",
		"org_id":    "org-test",
		"issued_at": time.Now().Unix(),
		"features":  []string{},
	})

	_, err = v.Validate(key)
	if err == nil {
		t.Fatal("expected error when verifying with wrong public key, got nil")
	}
}

func TestRSAValidator_MalformedKey(t *testing.T) {
	_, pubPEM := generateTestKeyPair(t)
	v, err := license.NewRSAValidator(pubPEM)
	if err != nil {
		t.Fatalf("constructing validator: %v", err)
	}

	badKeys := []string{
		"",
		"notavalidkey",
		"no-dot-separator",
		"!!bad-base64!!.!!bad-base64!!",
	}
	for _, k := range badKeys {
		_, err := v.Validate(k)
		if err == nil {
			t.Errorf("expected error for malformed key %q, got nil", k)
		}
	}
}

func TestNewRSAValidator_InvalidPEM(t *testing.T) {
	_, err := license.NewRSAValidator([]byte("not-a-pem-block"))
	if err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
}

// splitKey splits a license key into its two dot-separated parts.
func splitKey(key string) [2]string {
	for i, c := range key {
		if c == '.' {
			return [2]string{key[:i], key[i+1:]}
		}
	}
	return [2]string{key, ""}
}
