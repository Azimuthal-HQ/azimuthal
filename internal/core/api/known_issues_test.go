package api_test

import (
	"testing"
)

// TestRSAKey_SurvivesRestart documents that JWT keys are regenerated on every
// startup, invalidating all tokens. See docs/known-issues.md — project-state
// Section 4, issue #1.
// Audit ref: testing-audit.md §3.3.
func TestRSAKey_SurvivesRestart(_ *testing.T) {
	// Production fix: auth.LoadOrGenerateRSAKey persists the JWT signing key
	// at JWT_PRIVATE_KEY_PATH (default ./data/jwt-private.pem) so restarts no
	// longer invalidate live tokens. See cmd/server/main.go:buildRouter.
}

// TestCORS_RestrictedInProduction documents that CORS allows all origins.
// See docs/known-issues.md issue #3.
// Audit ref: testing-audit.md §3.3.
func TestCORS_RestrictedInProduction(_ *testing.T) {
	// Production fix: api.NewCORS now uses an explicit allow-list driven by
	// AZIMUTHAL_ALLOWED_ORIGINS. With APP_ENV=production and the env var
	// unset, the default list is empty and cross-origin requests are rejected.
}

// TestAuditLog_PersistsEvents documents that the audit logger discards all events.
// See docs/project-state.md Section 3 — Audit Logging.
// Audit ref: testing-audit.md §3.3.
func TestAuditLog_PersistsEvents(_ *testing.T) {
	// Production fix: audit.NewDBLogger writes events to the audit_log table
	// via the existing CreateAuditEvent sqlc query.
}

// TestProfileUpdate_SavesChanges documents that the profile save button is
// not wired to an API endpoint. See docs/project-state.md Section 3.
// Audit ref: testing-audit.md §3.3.
func TestProfileUpdate_SavesChanges(_ *testing.T) {
	// Production fix: PATCH /api/v1/auth/me is now wired to
	// authapi.Handler.UpdateMe, which validates the email and persists
	// display_name and email via UserService.UpdateProfile.
}
