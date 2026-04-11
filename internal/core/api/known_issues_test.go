package api_test

import (
	"testing"
)

// TestRSAKey_SurvivesRestart documents that JWT keys are regenerated on every
// startup, invalidating all tokens. See docs/known-issues.md — project-state
// Section 4, issue #1.
func TestRSAKey_SurvivesRestart(t *testing.T) {
	t.Skip("known issue: RSA key regenerated on every startup — all JWTs invalidated on restart. See docs/known-issues.md")
	// Test plan:
	// 1. Start server, get token
	// 2. Restart server (regenerates RSA key)
	// 3. Use token from step 1 — should succeed but currently fails
}

// TestCORS_RestrictedInProduction documents that CORS allows all origins.
// See docs/known-issues.md issue #3.
func TestCORS_RestrictedInProduction(t *testing.T) {
	t.Skip("known issue: CORS allows all origins — should restrict to APP_BASE_URL in production. See docs/known-issues.md")
	// Test plan:
	// 1. Set APP_ENV=production
	// 2. Request with Origin: http://evil.com
	// 3. Should be rejected but currently allowed
}

// TestAuditLog_PersistsEvents documents that the audit logger discards all events.
// See docs/project-state.md Section 3 — Audit Logging.
func TestAuditLog_PersistsEvents(t *testing.T) {
	t.Skip("known issue: audit logger discards all events — nothing is persisted. See docs/project-state.md Section 3")
	// Test plan:
	// 1. Perform an action (create user, create ticket)
	// 2. Query audit_log table
	// 3. Should have a row but currently empty
}

// TestProfileUpdate_SavesChanges documents that the profile save button is
// not wired to an API endpoint. See docs/project-state.md Section 3.
func TestProfileUpdate_SavesChanges(t *testing.T) {
	t.Skip("known issue: profile save button not wired to API — PUT/PATCH /api/v1/me missing or broken. See docs/project-state.md Section 3")
	// Test plan:
	// 1. Login as user
	// 2. PUT/PATCH /api/v1/me with updated display_name
	// 3. Should persist but endpoint may not exist
}
