package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/credlink"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// The credential-link battery mirrors the portal magic-link battery
// (portal_integration_test.go) case for case, because it is the same machinery
// shaped for internal users. Each negative test names the edit that makes it
// fail ("FAILS-BEFORE"), the house style the portal file established.
//
// Getting a raw token to redeem:
//   - sign-in link : the admin create-user endpoint returns the URL in the body.
//   - reset link   : the admin reset endpoint returns the URL in the body.
//   - forgot / email-change: the harness wires a recording sender with delivery
//     on (see newTestServerOn), so ts.CredentialLinks.LastURLTo(addr) yields the
//     link that would have been emailed.

// ── helpers ──────────────────────────────────────────────────────────────────

func credTokenFromURL(t *testing.T, url string) string {
	t.Helper()
	require.NotEmpty(t, url, "expected a credential link URL")
	parts := strings.Split(url, "/")
	tok := parts[len(parts)-1]
	require.NotEmpty(t, tok, "URL %q has no token segment", url)
	return tok
}

// adminCreateUserLink drives the org-admin create-user-with-link endpoint and
// returns the one-time URL and the new user id.
func adminCreateUserLink(t *testing.T, ts *testServer, email, name, role string) (url, userID string) {
	t.Helper()
	body := map[string]string{"email": email, "name": name}
	if role != "" {
		body["role"] = role
	}
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/credential-links/users", ts.OrgID), body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create user: %s", r.Body)
	var resp struct {
		URL    string `json:"url"`
		UserID string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resp))
	require.NotEmpty(t, resp.URL)
	require.NotEmpty(t, resp.UserID)
	return resp.URL, resp.UserID
}

func adminResetLink(t *testing.T, ts *testServer, email string) httpResult {
	t.Helper()
	return ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/credential-links/reset", ts.OrgID),
		map[string]string{"email": email}, true)
}

func consumeCred(t *testing.T, ts *testServer, token, password string) httpResult {
	t.Helper()
	body := map[string]string{"token": token}
	if password != "" {
		body["password"] = password
	}
	return ts.post(t, "/api/v1/credential-links/consume", body, false)
}

func inspectCred(t *testing.T, ts *testServer, token string) httpResult {
	t.Helper()
	return ts.post(t, "/api/v1/credential-links/inspect", map[string]string{"token": token}, false)
}

func forgotPassword(t *testing.T, ts *testServer, email string) httpResult {
	t.Helper()
	return ts.post(t, "/api/v1/credential-links/forgot-password", map[string]string{"email": email}, false)
}

// loginWith drives the real login endpoint with a specific password (loginAs is
// hardcoded to the fixture password).
func loginWith(t *testing.T, ts *testServer, email, password string) httpResult {
	t.Helper()
	return ts.post(t, "/api/v1/auth/login", map[string]string{"email": email, "password": password}, false)
}

func accessTokenFrom(t *testing.T, r httpResult) string {
	t.Helper()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	return body.AccessToken
}

// captureCredLogs redirects the process-global slog default to a buffer for the
// duration of the test (restored on cleanup). The server and handlers log
// through slog.Default(), so this is how the request path's logs are captured.
// Not for parallel use — the integration suite runs sequentially.
func captureCredLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// ── sign-in ──────────────────────────────────────────────────────────────────

// TestCredentialLink_SignInLinkYieldsWorkingSessionAndPassword: an admin creates
// an account behind a sign-in link; redeeming it with a password signs the user
// in AND sets a password that then works at the login form.
//
// FAILS-BEFORE: drop UpdateUserPasswordHash from the signin branch of
// CredentialLinkAdapter.Consume and the login at the end fails — the session was
// minted but no password was ever stored.
func TestCredentialLink_SignInLinkYieldsWorkingSessionAndPassword(t *testing.T) {
	ts := newTestServer(t)
	url, userID := adminCreateUserLink(t, ts, "newhire@example.com", "New Hire", "member")

	// Before redemption there is no password: login is impossible.
	require.Equal(t, http.StatusUnauthorized, loginWith(t, ts, "newhire@example.com", "whatever-they-guess").StatusCode)

	token := credTokenFromURL(t, url)
	r := consumeCred(t, ts, token, "chosen-password-123")
	require.Equal(t, http.StatusOK, r.StatusCode, "consume: %s", r.Body)
	var resp struct {
		Status       string `json:"status"`
		Purpose      string `json:"purpose"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resp))
	require.Equal(t, "signed_in", resp.Status)
	require.Equal(t, "signin", resp.Purpose)
	require.NotEmpty(t, resp.AccessToken, "a redeemed sign-in link must sign the user in")
	require.NotEmpty(t, resp.RefreshToken)

	// The minted session is live.
	require.Equal(t, http.StatusOK, meWith(t, ts, resp.AccessToken), "the minted session must work")

	// The chosen password logs in; a wrong one does not.
	require.Equal(t, http.StatusOK, loginWith(t, ts, "newhire@example.com", "chosen-password-123").StatusCode,
		"the password set on redemption must log in")
	require.Equal(t, http.StatusUnauthorized, loginWith(t, ts, "newhire@example.com", "wrong-password-123").StatusCode)

	require.Equal(t, userID, meUserID(t, ts, resp.AccessToken), "the session belongs to the created account")
}

func meUserID(t *testing.T, ts *testServer, token string) string {
	t.Helper()
	r := ts.getAs(t, token, "/api/v1/auth/me")
	require.Equal(t, http.StatusOK, r.StatusCode, "me: %s", r.Body)
	var body struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	return body.ID
}

// TestCredentialLink_ConsumeSignInRequiresPassword: a sign-in/reset link redeemed
// with no password is refused, and — because the guarded consume and the password
// write share a transaction — the link is NOT burned and still redeems with a
// password.
//
// FAILS-BEFORE: return the outcome instead of ErrPasswordRequired for a nil hash
// in Consume, and the first request would 200 with no password set.
func TestCredentialLink_ConsumeSignInRequiresPassword(t *testing.T) {
	ts := newTestServer(t)
	url, _ := adminCreateUserLink(t, ts, "nopass@example.com", "No Pass", "member")
	token := credTokenFromURL(t, url)

	requireErrorCode(t, consumeCred(t, ts, token, ""), http.StatusBadRequest, "VALIDATION_ERROR")

	// The transaction rolled back, so the link still works with a password.
	r := consumeCred(t, ts, token, "now-with-a-password-1")
	require.Equal(t, http.StatusOK, r.StatusCode, "the link must survive a password-less attempt: %s", r.Body)
}

// TestCredentialLink_TooShortPasswordRefusedAndLinkSurvives: an under-length
// password is refused before the link is touched.
func TestCredentialLink_TooShortPasswordRefusedAndLinkSurvives(t *testing.T) {
	ts := newTestServer(t)
	url, _ := adminCreateUserLink(t, ts, "shortpw@example.com", "Short PW", "member")
	token := credTokenFromURL(t, url)

	requireErrorCode(t, consumeCred(t, ts, token, "short"), http.StatusBadRequest, "VALIDATION_ERROR")
	require.Equal(t, http.StatusOK, consumeCred(t, ts, token, "long-enough-123").StatusCode,
		"a link must survive a too-short password attempt")
}

// ── single use, expiry, supersede (the portal invariants) ────────────────────

// TestCredentialLink_LinkIsSingleUse: a second redemption fails identically to a
// token that never existed.
//
// FAILS-BEFORE: remove `AND consumed_at IS NULL` from ConsumeCredentialLink and
// the second redemption succeeds.
func TestCredentialLink_LinkIsSingleUse(t *testing.T) {
	ts := newTestServer(t)
	url, _ := adminCreateUserLink(t, ts, "once@example.com", "Once", "member")
	token := credTokenFromURL(t, url)

	require.Equal(t, http.StatusOK, consumeCred(t, ts, token, "first-time-pass-1").StatusCode)

	second := consumeCred(t, ts, token, "second-time-pass-1")
	neverExisted := consumeCred(t, ts, "this-token-never-existed-abcdefghijklmnopqrstuv", "whatever-pass-1")
	require.Equal(t, http.StatusNotFound, second.StatusCode, "a consumed link must not redeem again")
	require.Equal(t, neverExisted.StatusCode, second.StatusCode,
		"a consumed link must be indistinguishable from a never-existed one")
	require.Equal(t, withoutRequestID(t, neverExisted.Body), withoutRequestID(t, second.Body),
		"the two refusals must be byte-identical bar the request id")
}

// TestCredentialLink_ExpiredLinkRefused ages a link past its window directly in
// the database, because waiting an hour is not a test.
//
// FAILS-BEFORE: remove `AND expires_at > now()` from ConsumeCredentialLink.
func TestCredentialLink_ExpiredLinkRefused(t *testing.T) {
	ts := newTestServer(t)
	url, _ := adminCreateUserLink(t, ts, "stale@example.com", "Stale", "member")
	token := credTokenFromURL(t, url)

	_, err := ts.DB.Pool.Exec(context.Background(),
		`UPDATE credential_links SET expires_at = now() - interval '1 minute'
		 WHERE consumed_at IS NULL AND invalidated_at IS NULL`)
	require.NoError(t, err)

	require.Equal(t, http.StatusNotFound, consumeCred(t, ts, token, "too-late-pass-1").StatusCode)
	// Inspect agrees — the three-guard predicate is shared.
	require.Equal(t, http.StatusNotFound, inspectCred(t, ts, token).StatusCode)
}

// TestCredentialLink_ReissueSupersedesTheOld: minting a new link for the same
// (user, purpose) invalidates the outstanding one.
//
// FAILS-BEFORE: delete the InvalidateOutstandingCredentialLinks call from
// CredentialLinkAdapter.Issue and the first link keeps working.
func TestCredentialLink_ReissueSupersedesTheOld(t *testing.T) {
	ts := newTestServer(t)
	// A user with a known password, so we can reset it twice by email.
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	first := adminResetLink(t, ts, person.Email)
	require.Equal(t, http.StatusOK, first.StatusCode, "%s", first.Body)
	second := adminResetLink(t, ts, person.Email)
	require.Equal(t, http.StatusOK, second.StatusCode, "%s", second.Body)

	oldToken := credTokenFromURL(t, linkURLFrom(t, first))
	newToken := credTokenFromURL(t, linkURLFrom(t, second))
	require.NotEqual(t, oldToken, newToken)

	require.Equal(t, http.StatusNotFound, consumeCred(t, ts, oldToken, "old-link-pass-1").StatusCode,
		"reissuing must invalidate the previous link")
	require.Equal(t, http.StatusOK, consumeCred(t, ts, newToken, "new-link-pass-1").StatusCode,
		"the newest link must still redeem")
}

func linkURLFrom(t *testing.T, r httpResult) string {
	t.Helper()
	var body struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	return body.URL
}

// TestCredentialLink_InspectDoesNotConsume: inspection reports the purpose and
// leaves the link redeemable.
//
// FAILS-BEFORE: point Inspect at ConsumeCredentialLink and the consume below
// fails because inspection burned the link.
func TestCredentialLink_InspectDoesNotConsume(t *testing.T) {
	ts := newTestServer(t)
	url, _ := adminCreateUserLink(t, ts, "inspectme@example.com", "Inspect Me", "member")
	token := credTokenFromURL(t, url)

	ins := inspectCred(t, ts, token)
	require.Equal(t, http.StatusOK, ins.StatusCode, "inspect: %s", ins.Body)
	var insp struct {
		Purpose  string `json:"purpose"`
		NewEmail string `json:"new_email"`
	}
	require.NoError(t, json.Unmarshal(ins.Body, &insp))
	require.Equal(t, "signin", insp.Purpose)
	require.Empty(t, insp.NewEmail, "a sign-in link carries no pending email")

	require.Equal(t, http.StatusOK, consumeCred(t, ts, token, "after-inspect-pass-1").StatusCode,
		"inspection must not consume the link")
}

// ── forgot-password: non-enumeration, non-disclosure, delivery ───────────────

// TestCredentialLink_ForgotPasswordDoesNotRevealWhetherAddressIsKnown: known and
// unknown addresses get byte-identical 202 responses, so the endpoint is not an
// account-existence oracle.
//
// FAILS-BEFORE: return 404 (or a different body) for an unknown address in
// ForgotPassword / RequestReset.
func TestCredentialLink_ForgotPasswordDoesNotRevealWhetherAddressIsKnown(t *testing.T) {
	ts := newTestServer(t)
	known := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	knownResp := forgotPassword(t, ts, known.Email)
	unknownResp := forgotPassword(t, ts, "never-seen-here@example.com")

	require.Equal(t, http.StatusAccepted, knownResp.StatusCode)
	require.Equal(t, knownResp.StatusCode, unknownResp.StatusCode)
	require.Equal(t, string(knownResp.Body), string(unknownResp.Body),
		"the bodies must be byte-identical — this endpoint reveals nothing")
}

// TestCredentialLink_ForgotPasswordNeverReturnsTheURL: the unauthenticated
// response never carries the link, under any configuration — the admin-issued
// link is the no-relay answer.
//
// FAILS-BEFORE: have ForgotPassword return issued.URL in its body.
func TestCredentialLink_ForgotPasswordNeverReturnsTheURL(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	r := forgotPassword(t, ts, person.Email)
	require.Equal(t, http.StatusAccepted, r.StatusCode)
	require.NotContains(t, string(r.Body), "/credential/", "the reset URL must never be in the response")
	require.NotContains(t, string(r.Body), "token", "no token material in the response")

	// The link WAS delivered out of band (recorder), and it works — so the
	// endpoint did its job without disclosing anything.
	url := ts.CredentialLinks.LastURLTo(person.Email)
	require.NotEmpty(t, url, "a live account's reset link is delivered, just not returned")
}

// TestCredentialLink_ForgotPasswordDeliversAWorkingResetLink: end to end, the
// delivered reset link sets a new password and revokes existing sessions.
func TestCredentialLink_ForgotPasswordDeliversAWorkingResetLink(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	require.Equal(t, http.StatusAccepted, forgotPassword(t, ts, person.Email).StatusCode)
	token := credTokenFromURL(t, ts.CredentialLinks.LastURLTo(person.Email))

	r := consumeCred(t, ts, token, "reset-by-forgot-1")
	require.Equal(t, http.StatusOK, r.StatusCode, "consume reset: %s", r.Body)
	var resp struct {
		Status      string `json:"status"`
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resp))
	require.Equal(t, "password_reset", resp.Status)
	require.Empty(t, resp.AccessToken, "a reset does not sign the user in — they authenticate fresh")

	require.Equal(t, http.StatusOK, loginWith(t, ts, person.Email, "reset-by-forgot-1").StatusCode)
}

// TestCredentialLink_RawTokenAbsentFromLogs: neither issuing nor redeeming a link
// writes the raw token to the logs.
//
// FAILS-BEFORE: add slog.Info("...", "token", rawToken) anywhere on the issue or
// consume path.
func TestCredentialLink_RawTokenAbsentFromLogs(t *testing.T) {
	buf := captureCredLogs(t)
	ts := newTestServer(t)

	url, _ := adminCreateUserLink(t, ts, "quiet@example.com", "Quiet", "member")
	token := credTokenFromURL(t, url)
	require.Equal(t, http.StatusOK, consumeCred(t, ts, token, "logged-nowhere-1").StatusCode)

	require.NotContains(t, buf.String(), token, "the raw token must never appear in the logs")
}

// ── password reset kills every session (B1 two-device, inverted) ─────────────

// TestCredentialLink_PasswordResetKillsEverySession: a reset is a break-glass
// event — every existing session on every device dies, on both revocation axes.
//
// FAILS-BEFORE: drop either UpdateUserPasswordHash's generation bump or the
// RevokeAllUserSessions call from the password_reset branch of Consume, and one
// of the two devices keeps working.
func TestCredentialLink_PasswordResetKillsEverySession(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	deviceA, _ := loginAs(t, ts, person.Email)
	deviceB, _ := loginAs(t, ts, person.Email)
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceA), "premise: device A works")
	require.Equal(t, http.StatusOK, meWith(t, ts, deviceB), "premise: device B works")
	genBefore := tokenGeneration(t, ts, person.ID)

	reset := adminResetLink(t, ts, person.Email)
	require.Equal(t, http.StatusOK, reset.StatusCode, "%s", reset.Body)
	token := credTokenFromURL(t, linkURLFrom(t, reset))
	require.Equal(t, http.StatusOK, consumeCred(t, ts, token, "broke-the-glass-1").StatusCode)

	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, deviceA), "device A must die on a reset")
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, deviceB), "device B must die too — every device")
	require.Equal(t, genBefore+1, tokenGeneration(t, ts, person.ID), "a reset bumps the generation")
	require.Equal(t, 2, revokedSessionCount(t, ts, person.ID), "both session rows are revoked")

	// It is a reset, not a lockout: the new password logs in.
	require.Equal(t, http.StatusOK, loginWith(t, ts, person.Email, "broke-the-glass-1").StatusCode)
}

// ── email change (C.2-c) ─────────────────────────────────────────────────────

// requestEmailChange drives the authenticated email-change request.
func requestEmailChange(t *testing.T, ts *testServer, token, newEmail, currentPassword string) httpResult {
	t.Helper()
	return ts.postAs(t, token, "/api/v1/auth/me/email-change",
		map[string]string{"new_email": newEmail, "current_password": currentPassword})
}

// TestCredentialLink_EmailChangeWithoutReauthRefused: the request half is refused
// without the current password — that reauth is the whole point of the fix.
//
// FAILS-BEFORE: delete the ComparePassword check in RequestEmailChange and a
// bearer token alone re-binds the address, which is C.2-c exactly.
func TestCredentialLink_EmailChangeWithoutReauthRefused(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token, _ := loginAs(t, ts, person.Email)

	// Wrong password: refused, nothing issued.
	requireErrorCode(t, requestEmailChange(t, ts, token, "new-addr@example.com", "not-the-password"),
		http.StatusUnauthorized, "UNAUTHORIZED")
	require.Empty(t, ts.CredentialLinks.LastURLTo("new-addr@example.com"),
		"a refused request must not issue a link")
}

// TestCredentialLink_EmailChangeBindsAddressAndBumpsGeneration: the confirmed
// change binds the new address and kills every outstanding token (the generation
// bump is the C.2-c fix), and the password is unchanged.
//
// FAILS-BEFORE: drop the token_generation bump from UpdateUserEmail and the old
// token still works after the address moves.
func TestCredentialLink_EmailChangeBindsAddressAndBumpsGeneration(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token, _ := loginAs(t, ts, person.Email)

	newEmail := "moved-to@example.com"
	r := requestEmailChange(t, ts, token, newEmail, "testpassword123")
	require.Equal(t, http.StatusAccepted, r.StatusCode, "with a relay the link is emailed: %s", r.Body)
	require.NotContains(t, string(r.Body), "/credential/", "the URL goes to the new address, not the response")

	// Before the confirm, the address has not moved and the session is live.
	require.Equal(t, http.StatusOK, meWith(t, ts, token), "premise: still signed in before confirm")

	linkURL := ts.CredentialLinks.LastURLTo(newEmail)
	require.NotEmpty(t, linkURL, "the confirmation link goes to the NEW address")
	consume := consumeCred(t, ts, credTokenFromURL(t, linkURL), "")
	require.Equal(t, http.StatusOK, consume.StatusCode, "confirm: %s", consume.Body)
	var cbody struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(consume.Body, &cbody))
	require.Equal(t, "email_changed", cbody.Status)

	// The old token is dead (generation bumped) and the account now answers to
	// the new address with the SAME password.
	require.Equal(t, http.StatusUnauthorized, meWith(t, ts, token), "the old token must die when the email moves")
	require.Equal(t, http.StatusOK, loginWith(t, ts, newEmail, "testpassword123").StatusCode,
		"the new address logs in; the password did not change")
	require.Equal(t, http.StatusUnauthorized, loginWith(t, ts, person.Email, "testpassword123").StatusCode,
		"the old address no longer resolves to this account")
}

// TestCredentialLink_EmailChangeToAnInUseAddressRefused: the new address may not
// already belong to a member of the same org.
func TestCredentialLink_EmailChangeToAnInUseAddressRefused(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	other := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token, _ := loginAs(t, ts, person.Email)

	requireErrorCode(t, requestEmailChange(t, ts, token, other.Email, "testpassword123"),
		http.StatusConflict, "CONFLICT")
}

// TestCredentialLink_EmailChangeNoRelayReturnsURLToRequester: without a relay the
// URL is returned to the reauthenticated requester (the documented no-relay
// trade), tested at the service level over the same pool — the harness handler
// runs with delivery on, so a second, non-delivering service closes the gap the
// way the portal's disclosure test does.
func TestCredentialLink_EmailChangeNoRelayReturnsURLToRequester(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	noRelay := credlink.NewService(
		adapters.NewCredentialLinkAdapter(ts.DB.Pool),
		nil, // RequestEmailChange does not use the resolver
		nil, // no sender
		credlink.Config{TTL: time.Hour, BaseURL: "http://localhost:8082", DeliverByEmail: false},
	)
	issued, err := noRelay.RequestEmailChange(context.Background(), person.ID, ts.OrgID, person.Email, "solo-move@example.com")
	require.NoError(t, err)
	require.False(t, issued.Delivered, "no relay: nothing is delivered")
	require.NotEmpty(t, issued.URL, "no relay: the URL is returned to the reauthenticated requester")

	// And it is a real, redeemable link.
	require.Equal(t, http.StatusOK, consumeCred(t, ts, credTokenFromURL(t, issued.URL), "").StatusCode)
}

// TestCredentialLink_ForgotPasswordNoRelayDeliversNothing: without a relay the
// self-service path still answers, mints a link, and delivers nothing — the
// admin-issued link is the no-relay answer. Tested at the service level.
func TestCredentialLink_ForgotPasswordNoRelayDeliversNothing(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// A real user service backs the org-less resolve; GetByEmailAcrossOrgs
	// ignores the adapter's org, exactly as login does.
	userSvc := auth.NewUserService(adapters.NewUserAdapter(ts.DB.Pool, ts.OrgID))
	noRelay := credlink.NewService(
		adapters.NewCredentialLinkAdapter(ts.DB.Pool),
		userSvc,
		nil, // no sender: delivery is impossible
		credlink.Config{TTL: time.Hour, BaseURL: "http://localhost:8082", DeliverByEmail: false},
	)
	require.NoError(t, noRelay.RequestReset(context.Background(), person.Email),
		"forgot-password answers success even with no relay")

	// A link WAS minted, it just went nowhere.
	var live int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM credential_links WHERE user_id = $1 AND purpose = 'password_reset'
		   AND consumed_at IS NULL AND invalidated_at IS NULL`, person.ID).Scan(&live))
	require.Equal(t, 1, live, "the reset link is minted even though it is delivered nowhere")
}

// ── org-scoping of the email lookup (the pinned finding) ─────────────────────

// TestCredentialLink_AdminResetIsOrgScoped: an address that is a member of ANOTHER
// org is not found here, indistinguishably from an address nobody has ever used.
//
// FAILS-BEFORE: make CredentialLinkAdapter.FindUserInOrg use the global
// GetUserByEmail and the other-org address resolves, leaking its existence and
// minting a link for it.
func TestCredentialLink_AdminResetIsOrgScoped(t *testing.T) {
	ts := newTestServer(t)
	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	elsewhere := testutil.CreateTestUserWithRole(t, ts.DB.Pool, otherOrg.ID, "member")

	otherOrgAddr := adminResetLink(t, ts, elsewhere.Email)
	neverExisted := adminResetLink(t, ts, "nobody-anywhere@example.com")

	require.Equal(t, http.StatusNotFound, otherOrgAddr.StatusCode,
		"an address in another org must not be found here")
	require.Equal(t, neverExisted.StatusCode, otherOrgAddr.StatusCode)
	require.Equal(t, withoutRequestID(t, neverExisted.Body), withoutRequestID(t, otherOrgAddr.Body),
		"another org's address must be indistinguishable from never-existed")
}

// ── admin issuance requires org-admin ────────────────────────────────────────

// TestCredentialLink_AdminIssuanceRequiresOrgAdmin: the issuance routes are the
// org-admin-404 class — a member, a stranger and an anonymous caller cannot tell
// they exist.
//
// FAILS-BEFORE: mount the admin routes without RequireOrgAdmin404 and a plain
// member can mint sign-in and reset links for anyone.
func TestCredentialLink_AdminIssuanceRequiresOrgAdmin(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberTok := ts.tokenFor(t, member.ID, member.Email)

	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	strangerUser := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	strangerTok := ts.tokenFor(t, strangerUser.ID, strangerUser.Email)

	createPath := fmt.Sprintf("/api/v1/orgs/%s/credential-links/users", ts.OrgID)
	resetPath := fmt.Sprintf("/api/v1/orgs/%s/credential-links/reset", ts.OrgID)
	createBody := map[string]string{"email": "sneaky@example.com", "name": "Sneaky"}
	resetBody := map[string]string{"email": member.Email}

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"member", memberTok, http.StatusNotFound},
		{"stranger", strangerTok, http.StatusNotFound},
	} {
		t.Run("create/"+tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ts.postAs(t, tc.token, createPath, createBody).StatusCode)
		})
		t.Run("reset/"+tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ts.postAs(t, tc.token, resetPath, resetBody).StatusCode)
		})
	}

	// The admin (owner) can. And a member never got created behind the failures
	// above.
	require.Equal(t, http.StatusCreated, ts.post(t, createPath, createBody, true).StatusCode)
	var count int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'sneaky@example.com'`).Scan(&count))
	require.Equal(t, 1, count, "exactly one account, minted by the admin — none by the refused callers")
}

// TestCredentialLink_CreatedUserHasDefaultGrant: an admin-created account gets
// the default grant — an org membership at the chosen role and a primary team
// (ADR-0006: never teamless) — and no password until the link is redeemed.
func TestCredentialLink_CreatedUserHasDefaultGrant(t *testing.T) {
	ts := newTestServer(t)
	_, userIDStr := adminCreateUserLink(t, ts, "provisioned@example.com", "Provisioned", "admin")
	userID := uuid.MustParse(userIDStr)

	var role string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2`, ts.OrgID, userID).Scan(&role))
	require.Equal(t, "admin", role, "the membership carries the requested org role")

	var teamCount, primaryCount int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM team_members WHERE org_id = $1 AND user_id = $2`, ts.OrgID, userID).Scan(&teamCount))
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM team_members WHERE org_id = $1 AND user_id = $2 AND is_primary`, ts.OrgID, userID).Scan(&primaryCount))
	require.GreaterOrEqual(t, teamCount, 1, "a created member is enrolled in a team (never teamless)")
	require.Equal(t, 1, primaryCount, "and has exactly one primary team")

	require.Equal(t, http.StatusUnauthorized, loginWith(t, ts, "provisioned@example.com", "not-set-yet-1").StatusCode,
		"no password until the sign-in link is redeemed")
}

// TestCredentialLink_EmailChangeConsumeCollisionRefused: if the pending address
// is claimed by someone else between request and confirm, the confirm is a
// conflict and nothing moves — the guarded write catches the race the
// request-time check cannot.
//
// FAILS-BEFORE: drop the uniqueViolation -> ErrEmailTaken mapping in the
// email_change branch of Consume and the confirm 500s (or worse, the constraint
// error escapes unmapped).
func TestCredentialLink_EmailChangeConsumeCollisionRefused(t *testing.T) {
	ts := newTestServer(t)
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token, _ := loginAs(t, ts, person.Email)

	raced := "raced@example.com"
	r := requestEmailChange(t, ts, token, raced, "testpassword123")
	require.Equal(t, http.StatusAccepted, r.StatusCode, "%s", r.Body)
	linkURL := ts.CredentialLinks.LastURLTo(raced)

	// Someone else claims the address before the confirm.
	require.Equal(t, http.StatusCreated,
		ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/credential-links/users", ts.OrgID),
			map[string]string{"email": raced, "name": "First"}, true).StatusCode)

	requireErrorCode(t, consumeCred(t, ts, credTokenFromURL(t, linkURL), ""),
		http.StatusConflict, "CONFLICT")

	// The requester's address did not move.
	require.Equal(t, http.StatusOK, meWith(t, ts, token), "the failed confirm leaves the session live")
	require.Equal(t, http.StatusOK, loginWith(t, ts, person.Email, "testpassword123").StatusCode)
}

// TestCredentialLink_ConsumeForDeactivatedAccountRefused: a link for an account
// that is deactivated between issue and redemption is refused, indistinguishably
// from an invalid one.
//
// FAILS-BEFORE: drop the is_active check in Consume and a deactivated account
// could set a password and sign back in through a stale link.
func TestCredentialLink_ConsumeForDeactivatedAccountRefused(t *testing.T) {
	ts := newTestServer(t)
	url, userID := adminCreateUserLink(t, ts, "frozen@example.com", "Frozen", "member")

	_, err := ts.DB.Pool.Exec(context.Background(),
		`UPDATE users SET is_active = false WHERE id = $1`, userID)
	require.NoError(t, err)

	require.Equal(t, http.StatusNotFound, consumeCred(t, ts, credTokenFromURL(t, url), "wont-work-123").StatusCode)
}

// ── validation / error branches ──────────────────────────────────────────────

// TestCredentialLink_PublicEndpointValidation: the public endpoints reject
// malformed bodies (BAD_REQUEST) and missing fields (VALIDATION_ERROR), and an
// unknown token inspects/consumes as 404.
func TestCredentialLink_PublicEndpointValidation(t *testing.T) {
	ts := newTestServer(t)

	// Malformed JSON → BAD_REQUEST on each public route.
	for _, path := range []string{
		"/api/v1/credential-links/forgot-password",
		"/api/v1/credential-links/inspect",
		"/api/v1/credential-links/consume",
	} {
		requireErrorCode(t, attdRaw(t, ts, http.MethodPost, path, "{not-json"), http.StatusBadRequest, "BAD_REQUEST")
	}

	// Missing email on forgot-password → VALIDATION_ERROR.
	requireErrorCode(t, forgotPassword(t, ts, ""), http.StatusBadRequest, "VALIDATION_ERROR")

	// Unknown token → 404 on inspect and consume alike.
	require.Equal(t, http.StatusNotFound, inspectCred(t, ts, "no-such-token-xyz").StatusCode)
	require.Equal(t, http.StatusNotFound, consumeCred(t, ts, "no-such-token-xyz", "some-password-1").StatusCode)
}

// TestCredentialLink_AdminAndEmailChangeValidation: the admin issuance and
// authenticated email-change routes reject malformed input.
func TestCredentialLink_AdminAndEmailChangeValidation(t *testing.T) {
	ts := newTestServer(t)
	createPath := fmt.Sprintf("/api/v1/orgs/%s/credential-links/users", ts.OrgID)

	// A malformed email, and an unknown role, are validation errors.
	requireErrorCode(t, ts.post(t, createPath, map[string]string{"email": "not-an-email", "name": "X"}, true),
		http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, ts.post(t, createPath, map[string]string{"email": "ok@example.com", "name": "X", "role": "wizard"}, true),
		http.StatusBadRequest, "VALIDATION_ERROR")

	// Creating the same email twice in one org is a conflict.
	require.Equal(t, http.StatusCreated, ts.post(t, createPath, map[string]string{"email": "dupe@example.com", "name": "Dupe"}, true).StatusCode)
	requireErrorCode(t, ts.post(t, createPath, map[string]string{"email": "dupe@example.com", "name": "Dupe Again"}, true),
		http.StatusConflict, "CONFLICT")

	// Reset for a malformed address is a validation error.
	requireErrorCode(t, adminResetLink(t, ts, "bad-address"), http.StatusBadRequest, "VALIDATION_ERROR")

	// Email change with missing fields is a validation error (reauth is checked
	// after the fields are present).
	person := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	token, _ := loginAs(t, ts, person.Email)
	requireErrorCode(t, requestEmailChange(t, ts, token, "", ""), http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, requestEmailChange(t, ts, token, "not-an-email", "testpassword123"),
		http.StatusBadRequest, "VALIDATION_ERROR")
}

// ── audit ────────────────────────────────────────────────────────────────────

// TestCredentialLink_AuditRowsAreWritten: issuance records an issued event by the
// admin; redemption records a consumed event by the account.
func TestCredentialLink_AuditRowsAreWritten(t *testing.T) {
	ts := newTestServer(t)
	url, userID := adminCreateUserLink(t, ts, "audited@example.com", "Audited", "member")

	issued := credAuditRows(t, ts, "credential_link.issued")
	require.NotEmpty(t, issued, "admin issuance must be audited")
	require.Equal(t, ts.UserID.String(), *issued[len(issued)-1].actor, "the acting admin is the actor")
	require.Equal(t, "signin", issued[len(issued)-1].purpose)

	require.Equal(t, http.StatusOK, consumeCred(t, ts, credTokenFromURL(t, url), "audit-me-123").StatusCode)
	consumed := credAuditRows(t, ts, "credential_link.consumed")
	require.NotEmpty(t, consumed, "redemption must be audited")
	require.Equal(t, userID, *consumed[len(consumed)-1].actor, "the account is the actor on consume")
	require.Equal(t, "signin", consumed[len(consumed)-1].purpose)
}

type credAuditRow struct {
	actor   *string
	purpose string
}

func credAuditRows(t *testing.T, ts *testServer, action string) []credAuditRow {
	t.Helper()
	rows, err := ts.DB.Pool.Query(context.Background(),
		`SELECT actor_id, payload FROM audit_log WHERE action = $1 ORDER BY created_at ASC`, action)
	require.NoError(t, err)
	defer rows.Close()
	var out []credAuditRow
	for rows.Next() {
		var actor *string
		var payload []byte
		require.NoError(t, rows.Scan(&actor, &payload))
		var p struct {
			Purpose string `json:"purpose"`
		}
		require.NoError(t, json.Unmarshal(payload, &p))
		out = append(out, credAuditRow{actor: actor, purpose: p.Purpose})
	}
	return out
}
