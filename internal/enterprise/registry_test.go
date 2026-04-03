//go:build !enterprise

package enterprise_test

import (
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/analytics"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/license"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/rbac"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/sso"
)

func TestNew_ReturnsNonNilRegistry(t *testing.T) {
	r := enterprise.New()
	if r == nil {
		t.Fatal("enterprise.New() returned nil")
	}
}

func TestNew_AllFieldsPopulated(t *testing.T) {
	r := enterprise.New()

	if r.SSO == nil {
		t.Error("Registry.SSO must not be nil")
	}
	if r.Audit == nil {
		t.Error("Registry.Audit must not be nil")
	}
	if r.RBAC == nil {
		t.Error("Registry.RBAC must not be nil")
	}
	if r.Analytics == nil {
		t.Error("Registry.Analytics must not be nil")
	}
	if r.License == nil {
		t.Error("Registry.License must not be nil")
	}
}

func TestNew_CommunityStubsNotAvailable(t *testing.T) {
	r := enterprise.New()

	if r.SSO.IsAvailable() {
		t.Error("SSO stub should not be available in community builds")
	}
	if r.Audit.IsAvailable() {
		t.Error("Audit stub should not be available in community builds")
	}
	if r.RBAC.IsAvailable() {
		t.Error("RBAC stub should not be available in community builds")
	}
	if r.Analytics.IsAvailable() {
		t.Error("Analytics stub should not be available in community builds")
	}
}

// TestNew_InterfaceCompliance verifies each field satisfies its interface.
// These are compile-time checks expressed as runtime assignments.
// TestNew_InterfaceCompliance verifies each Registry field satisfies its interface.
// These are compile-time checks expressed as runtime assignments.
func TestNew_InterfaceCompliance(t *testing.T) {
	r := enterprise.New()

	var _ sso.SSOProvider = r.SSO
	var _ audit.AuditLogger = r.Audit
	var _ rbac.RBACChecker = r.RBAC
	var _ analytics.AnalyticsReporter = r.Analytics
	var _ license.LicenseValidator = r.License
}
