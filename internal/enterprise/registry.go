// Package enterprise wires together all enterprise subsystems and exposes them
// through a single Registry. Community builds use stub implementations for every
// interface; enterprise builds replace them via build-tagged constructor overrides.
package enterprise

import (
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/analytics"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/license"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/rbac"
	"github.com/Azimuthal-HQ/azimuthal/internal/enterprise/sso"
)

// Registry holds the active implementation of every enterprise subsystem.
// Use New to construct one with the appropriate stubs or real implementations
// depending on the build tag in effect.
type Registry struct {
	// SSO is the active single sign-on provider.
	SSO sso.SSOProvider
	// Audit is the active audit logger.
	Audit audit.AuditLogger
	// RBAC is the active permissions checker.
	RBAC rbac.RBACChecker
	// Analytics is the active analytics reporter.
	Analytics analytics.AnalyticsReporter
	// License is the active license validator.
	License license.LicenseValidator
}

// New constructs a Registry populated with the implementations selected by the
// current build tag. In community builds every field is a stub; in enterprise
// builds the azimuthal-ee module replaces the constructors at compile time.
func New() *Registry {
	return &Registry{
		SSO:       sso.NewProvider(),
		Audit:     audit.NewLogger(),
		RBAC:      rbac.NewChecker(),
		Analytics: analytics.NewReporter(),
		License:   license.NewValidator(),
	}
}
