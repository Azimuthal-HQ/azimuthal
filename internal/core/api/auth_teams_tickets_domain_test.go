package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Second-layer error paths of the auth, team and ticket handlers.
//
// The first negative pass pinned the obvious refusals (malformed ids, unknown
// ids, undecodable bodies on the routes it touched). What is left, and what
// this file covers, is the layer underneath: the refusals a handler raises
// AFTER the shared guards have let the request through — a tri-state field
// decoded a second time, a domain validation the service performs, a
// pre-write reference check whose whole purpose is that nothing is written,
// and the two spellings of "unassign" that must behave identically.
//
// Every assertion below names the exact status AND the error code. The two
// together are what separates the refusals: BAD_REQUEST means "this request
// is malformed", VALIDATION_ERROR means "it decoded and said something
// unacceptable", and a handler that collapses them tells the client to look
// in the wrong place.
//
// Package-scope identifiers are prefixed attd* — package api_test is one
// namespace across ~45 files.

// attdRaw sends a body that need not be valid JSON (the marshalling helpers
// cannot express an undecodable body).
func attdRaw(t *testing.T, ts *testServer, method, path, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, ts.url(path), jsonRawReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ts.Token)
	return ts.do(t, req)
}

// attdProfile reads the live users row for the caller.
func attdProfile(t *testing.T, ts *testServer, userID uuid.UUID) (displayName, email string) {
	t.Helper()
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT display_name, email FROM users WHERE id = $1`, userID).Scan(&displayName, &email))
	return displayName, email
}

// attdTeamRow reads the mutable columns of a team.
func attdTeamRow(t *testing.T, ts *testServer, teamID uuid.UUID) (name, description string, parent *uuid.UUID, deleted bool) {
	t.Helper()
	var deletedAt *string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT name, description, parent_id, deleted_at::text FROM teams WHERE id = $1`,
		teamID).Scan(&name, &description, &parent, &deletedAt))
	return name, description, parent, deletedAt != nil
}

// attdCreateTeam creates a team through the API and returns its id.
func attdCreateTeam(t *testing.T, ts *testServer, slug, name string, parentID *string) string {
	t.Helper()
	body := map[string]any{"slug": slug, "name": name}
	if parentID != nil {
		body["parent_id"] = *parentID
	}
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/teams/", ts.OrgID), body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create team %s: %s", slug, r.Body)
	var team struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &team))
	return team.ID
}

// attdOverLongRef is one character past ticketref.MaxLen.
var attdOverLongRef = strings.Repeat("z", 201)

// ---------------------------------------------------------------------------
// auth: PATCH /api/v1/auth/me
// ---------------------------------------------------------------------------

// TestAuthTeamTicketDomain_UpdateMePersistsTheProfile: the accepted path of
// UpdateMe, proven by reading the change back rather than by trusting the
// response body.
//
// Defect this catches: UpdateMe echoing the request instead of persisting it
// — a handler that built its 200 response from `req` rather than from the
// row `UpdateProfile` returned would satisfy any assertion made on the
// response alone, and the user's rename would silently vanish on their next
// sign-in. The follow-up GET /auth/me and the direct row read are what make
// that impossible: both read the database, neither reads the request.
func TestAuthTeamTicketDomain_UpdateMePersistsTheProfile(t *testing.T) {
	ts := newTestServer(t)
	beforeName, beforeEmail := attdProfile(t, ts, ts.UserID)

	newEmail := "attd-renamed@azimuthal.dev"
	r := ts.patch(t, "/api/v1/auth/me",
		map[string]string{"display_name": "Attd Renamed", "email": newEmail}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "update me: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)

	var got struct {
		ID          string `json:"id"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		OrgID       string `json:"org_id"`
		IsActive    bool   `json:"is_active"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &got))
	require.Equal(t, ts.UserID.String(), got.ID)
	require.Equal(t, "Attd Renamed", got.DisplayName)
	require.Equal(t, newEmail, got.Email)
	require.Equal(t, ts.OrgID.String(), got.OrgID)
	require.True(t, got.IsActive)

	// It really moved: both the row and the read endpoint agree.
	afterName, afterEmail := attdProfile(t, ts, ts.UserID)
	require.Equal(t, "Attd Renamed", afterName)
	require.Equal(t, newEmail, afterEmail)
	require.NotEqual(t, beforeName, afterName, "premise: the fixture name was different")
	require.NotEqual(t, beforeEmail, afterEmail, "premise: the fixture email was different")

	me := ts.get(t, "/api/v1/auth/me", true)
	require.Equal(t, http.StatusOK, me.StatusCode, "me: %s", me.Body)
	var meBody struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	require.NoError(t, json.Unmarshal(me.Body, &meBody))
	require.Equal(t, "Attd Renamed", meBody.DisplayName)
	require.Equal(t, newEmail, meBody.Email)

	// Leading and trailing whitespace is trimmed, not stored — otherwise
	// " a@b.dev " would be a second, unreachable account identity.
	r = ts.patch(t, "/api/v1/auth/me",
		map[string]string{"display_name": "  Trimmed  ", "email": "  attd-trim@azimuthal.dev  "}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "trim: %s", r.Body)
	trimName, trimEmail := attdProfile(t, ts, ts.UserID)
	require.Equal(t, "Trimmed", trimName)
	require.Equal(t, "attd-trim@azimuthal.dev", trimEmail)
}

// TestAuthTeamTicketDomain_UpdateMeRefusalsLeaveTheProfileUntouched: every
// refusal on PATCH /auth/me, each with its own code, and the row unchanged
// after all of them.
//
// Defect this catches: dropping any one of UpdateMe's four guards. Without
// the decode check a garbled body would proceed on a zero-valued struct and
// be reported as "display_name is required" — a VALIDATION_ERROR for what is
// really a malformed request. Without the blank-name check a user could
// erase their own display name and become an empty row in every picker and
// mention list. Without mail.ParseAddress the users table accepts strings
// that no mail transport can deliver to, and the account's only recovery
// channel is gone. The final row read is what proves each refusal wrote
// nothing: a handler that validated after calling UpdateProfile would answer
// 400 and still have changed the record.
func TestAuthTeamTicketDomain_UpdateMeRefusalsLeaveTheProfileUntouched(t *testing.T) {
	ts := newTestServer(t)
	beforeName, beforeEmail := attdProfile(t, ts, ts.UserID)

	// Undecodable bodies are BAD_REQUEST — never VALIDATION_ERROR.
	// respond.DecodeJSON sets DisallowUnknownFields, so a misspelt field is
	// undecodable too rather than being silently dropped.
	for name, body := range map[string]string{
		"truncated":     `{`,
		"not_json":      `nonsense`,
		"array":         `["display_name"]`,
		"empty":         ``,
		"unknown_field": `{"displayName":"camel","email":"a@b.dev"}`,
		"wrong_type":    `{"display_name":42,"email":"a@b.dev"}`,
	} {
		t.Run("undecodable/"+name, func(t *testing.T) {
			requireErrorCode(t, attdRaw(t, ts, http.MethodPatch, "/api/v1/auth/me", body),
				http.StatusBadRequest, "BAD_REQUEST")
		})
	}

	// Decoded but unacceptable is VALIDATION_ERROR.
	for name, body := range map[string]map[string]string{
		"missing_display_name":    {"email": "attd-ok@azimuthal.dev"},
		"blank_display_name":      {"display_name": "", "email": "attd-ok@azimuthal.dev"},
		"whitespace_display_name": {"display_name": "   \t  ", "email": "attd-ok@azimuthal.dev"},
		"missing_email":           {"display_name": "Fine"},
		"blank_email":             {"display_name": "Fine", "email": ""},
		"whitespace_email":        {"display_name": "Fine", "email": "   "},
		"email_no_at":             {"display_name": "Fine", "email": "not-an-email"},
		"email_no_domain":         {"display_name": "Fine", "email": "someone@"},
		"email_two_addresses":     {"display_name": "Fine", "email": "a@b.dev, c@d.dev"},
		"email_bare_domain":       {"display_name": "Fine", "email": "azimuthal.dev"},
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			requireErrorCode(t, ts.patch(t, "/api/v1/auth/me", body, true),
				http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}

	// Not one of those requests changed the record.
	afterName, afterEmail := attdProfile(t, ts, ts.UserID)
	require.Equal(t, beforeName, afterName, "a refused PATCH must not have renamed the user")
	require.Equal(t, beforeEmail, afterEmail, "a refused PATCH must not have changed the email")

	// The same shape SUCCEEDS once it is valid — without this the assertions
	// above would also pass against a handler that refused everything.
	r := ts.patch(t, "/api/v1/auth/me",
		map[string]string{"display_name": "Fine", "email": "attd-ok@azimuthal.dev"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "control: a valid PATCH is accepted: %s", r.Body)
}

// TestAuthTeamTicketDomain_LoginRefusesAWrongPassword: a wrong password is
// 401 UNAUTHORIZED, mints nothing, and is indistinguishable from an unknown
// address.
//
// Defect this catches: any regression that lets Authenticate return a user on
// a bad password — the token assertion fails the moment the refusal stops
// being a refusal, and the session-count assertion catches the subtler
// version where the refusal is written but a credential was already issued.
// The identical-message assertion pins the enumeration property: a login form
// that answers "no such account" separately from "wrong password" is an
// account-enumeration oracle, and the two arms of the check in Login
// (ErrInvalidCredentials and ErrAccountInactive) share one response for
// exactly that reason.
//
// NOTE: this deliberately does NOT assert a user.login_failed audit row.
// Login builds that event with no OrgID, and the DB logger drops any event
// whose org_id will not parse — so no row is written today. That is reported
// as a defect rather than pinned here; see the run notes.
func TestAuthTeamTicketDomain_LoginRefusesAWrongPassword(t *testing.T) {
	ts := newTestServer(t)
	user := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Premise: the fixture password really does work.
	ok := ts.post(t, "/api/v1/auth/login",
		map[string]string{"email": user.Email, "password": "testpassword123"}, false)
	require.Equal(t, http.StatusOK, ok.StatusCode, "premise: correct password signs in: %s", ok.Body)

	bad := ts.post(t, "/api/v1/auth/login",
		map[string]string{"email": user.Email, "password": "testpassword124"}, false)
	requireErrorCode(t, bad, http.StatusUnauthorized, "UNAUTHORIZED")
	require.NotContains(t, string(bad.Body), "access_token", "a refused login must not mint a token")

	// An unknown address is refused with the identical body — the response
	// must not reveal whether the account exists.
	unknown := ts.post(t, "/api/v1/auth/login",
		map[string]string{"email": "attd-nobody@azimuthal.dev", "password": "testpassword123"}, false)
	requireErrorCode(t, unknown, http.StatusUnauthorized, "UNAUTHORIZED")
	var badBody, unknownBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(bad.Body, &badBody))
	require.NoError(t, json.Unmarshal(unknown.Body, &unknownBody))
	require.Equal(t, badBody.Error.Message, unknownBody.Error.Message,
		"wrong password and unknown account must be indistinguishable")

	// A refused login leaves no credential behind: the successful sign-in
	// above is the only session on the account.
	var sessions int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE user_id = $1`, user.ID).Scan(&sessions))
	require.Zero(t, sessions, "the JWT login path issues no DB session, refused or not")

	// A deactivated account is refused with the same body — the third arm of
	// the same check, and it must not become distinguishable either.
	require.Equal(t, http.StatusNoContent,
		ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/deactivate", ts.OrgID, user.ID), nil, true).StatusCode)
	off := ts.post(t, "/api/v1/auth/login",
		map[string]string{"email": user.Email, "password": "testpassword123"}, false)
	requireErrorCode(t, off, http.StatusUnauthorized, "UNAUTHORIZED")
	var offBody struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(off.Body, &offBody))
	require.Equal(t, badBody.Error.Message, offBody.Error.Message,
		"a deactivated account must not be distinguishable from a wrong password")
}

// ---------------------------------------------------------------------------
// teams
// ---------------------------------------------------------------------------

// TestAuthTeamTicketDomain_TeamCreateParentIDBranches: the three shapes of
// parent_id on POST /teams — absent/empty (root), a parseable id (nested),
// and an unparseable one (refused).
//
// Defect this catches: dropping Create's uuid.Parse guard on parent_id. The
// string would fall through as the zero UUID, the store would look for a
// parent that cannot exist, and a typo'd parent id would surface as
// "parent team not found" — sending the operator to look for a team rather
// than at their own request. The empty-string case is the other half: it must
// mean "no parent", not "parse the empty string", or every client that sends
// parent_id:"" for a root team gets a 400.
func TestAuthTeamTicketDomain_TeamCreateParentIDBranches(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/teams/", ts.OrgID)

	parentID := attdCreateTeam(t, ts, "attd-parent", "Attd Parent", nil)

	// A parseable parent nests the team: parent_id and a two-element path.
	r := ts.post(t, base, map[string]any{
		"slug": "attd-child", "name": "Attd Child", "parent_id": parentID,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "nested create: %s", r.Body)
	var child struct {
		ID       string      `json:"id"`
		ParentID *string     `json:"parent_id"`
		Path     []uuid.UUID `json:"path"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &child))
	require.NotNil(t, child.ParentID, "a parent_id that parsed must reach the store")
	require.Equal(t, parentID, *child.ParentID)
	require.Len(t, child.Path, 2, "a nested team's materialised path carries its ancestor")

	// An empty parent_id means the root, not a parse attempt.
	r = ts.post(t, base, map[string]any{
		"slug": "attd-empty-parent", "name": "Attd Empty Parent", "parent_id": "",
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "empty parent_id: %s", r.Body)
	var rootTeam struct {
		ParentID *string     `json:"parent_id"`
		Path     []uuid.UUID `json:"path"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &rootTeam))
	require.Nil(t, rootTeam.ParentID, `parent_id:"" must mean the root`)
	require.Len(t, rootTeam.Path, 1)

	// An unparseable parent_id is VALIDATION_ERROR — the value decoded, it
	// simply is not a uuid.
	for _, bad := range []string{"banana", "12345", "not-a-uuid"} {
		t.Run("unparseable/"+bad, func(t *testing.T) {
			requireErrorCode(t, ts.post(t, base, map[string]any{
				"slug": "attd-bad-" + bad, "name": "Attd Bad", "parent_id": bad,
			}, true), http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}

	// A well-formed parent id naming no team in this org is the store's
	// refusal, and it is also a 400 — distinct cause, same class of answer.
	requireErrorCode(t, ts.post(t, base, map[string]any{
		"slug": "attd-orphan", "name": "Attd Orphan", "parent_id": uuid.NewString(),
	}, true), http.StatusBadRequest, "VALIDATION_ERROR")

	// None of the refused creates left a row behind.
	var strays int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM teams WHERE org_id = $1 AND slug LIKE 'attd-bad-%' OR slug = 'attd-orphan'`,
		ts.OrgID).Scan(&strays))
	require.Zero(t, strays, "a refused create must not have persisted a team")
}

// TestAuthTeamTicketDomain_TeamPatchRefusalsLeaveTheTeamUntouched: PATCH
// /teams/{teamID} refuses an over-long or unrepresentable ticket_ref, an
// undecodable body, and an empty name — and writes nothing in any case.
//
// Defect this catches: moving the ticketref.Resolve call BELOW the rename.
// The reference is resolved before either write for a reason — under a
// required policy a missing reference has to mean nothing happened, and a 400
// raised after svc.Rename would leave an unreferenced change committed, which
// is exactly the outcome the requirement exists to prevent. This test fails
// in that ordering: the status would still be 400 but the team would already
// carry the new name. The NUL-byte case guards the quieter half — a reference
// PostgreSQL cannot store must be refused here, because audit.Logger swallows
// its own errors and the mutation would otherwise commit with no audit row.
func TestAuthTeamTicketDomain_TeamPatchRefusalsLeaveTheTeamUntouched(t *testing.T) {
	ts := newTestServer(t)
	teamID := attdCreateTeam(t, ts, "attd-patch", "Attd Patch", nil)
	teamUUID := uuid.MustParse(teamID)
	path := fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, teamID)

	// Give it a description so a refused PATCH has something to damage.
	r := ts.patch(t, path, map[string]string{"description": "original"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "seed description: %s", r.Body)

	// The reference guard runs before the rename.
	requireErrorCode(t, ts.patch(t, path+"?ticket_ref="+attdOverLongRef,
		map[string]string{"name": "Renamed By An Over-Long Ref"}, true),
		http.StatusBadRequest, "VALIDATION_ERROR")
	requireErrorCode(t, ts.patch(t, path+"?ticket_ref=OPS-1%00",
		map[string]string{"name": "Renamed By A NUL Ref"}, true),
		http.StatusBadRequest, "VALIDATION_ERROR")

	// An undecodable body is BAD_REQUEST, not VALIDATION_ERROR.
	for name, body := range map[string]string{
		"truncated":     `{`,
		"not_json":      `nonsense`,
		"unknown_field": `{"team_name":"a field this build does not know"}`,
		"wrong_type":    `{"name":42}`,
	} {
		t.Run("undecodable/"+name, func(t *testing.T) {
			requireErrorCode(t, attdRaw(t, ts, http.MethodPatch, path, body),
				http.StatusBadRequest, "BAD_REQUEST")
		})
	}

	// An explicit empty name is the service's refusal, and it must not fall
	// through to "leave the name alone".
	requireErrorCode(t, ts.patch(t, path, map[string]any{"name": ""}, true),
		http.StatusBadRequest, "VALIDATION_ERROR")

	name, description, parent, deleted := attdTeamRow(t, ts, teamUUID)
	require.Equal(t, "Attd Patch", name, "no refused PATCH may have renamed the team")
	require.Equal(t, "original", description, "no refused PATCH may have changed the description")
	require.Nil(t, parent)
	require.False(t, deleted)
}

// TestAuthTeamTicketDomain_TeamPatchAppliesEachFieldIndependently: the PATCH
// contract — an absent field keeps its current value, a present one replaces
// it, and a body with neither writes nothing at all.
//
// Defect this catches: applyRename reading a missing field as its zero value.
// A description-only PATCH would then blank the name (refused as
// ErrNameRequired, so the symptom is a mystery 400 on an innocent request),
// and a name-only PATCH would silently erase the description — a partial-PATCH
// data loss of exactly the shape that wiped every item's due_at in this
// codebase before. Each half below fails if the other field stops being
// carried forward.
func TestAuthTeamTicketDomain_TeamPatchAppliesEachFieldIndependently(t *testing.T) {
	ts := newTestServer(t)
	teamID := attdCreateTeam(t, ts, "attd-fields", "Attd Fields", nil)
	teamUUID := uuid.MustParse(teamID)
	path := fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, teamID)

	// description only → the name survives.
	r := ts.patch(t, path, map[string]string{"description": "the squad that ships"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "description-only patch: %s", r.Body)
	name, description, _, _ := attdTeamRow(t, ts, teamUUID)
	require.Equal(t, "Attd Fields", name, "a description-only PATCH must not touch the name")
	require.Equal(t, "the squad that ships", description)

	// name only → the description survives.
	r = ts.patch(t, path, map[string]string{"name": "Attd Fields Renamed"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "name-only patch: %s", r.Body)
	name, description, _, _ = attdTeamRow(t, ts, teamUUID)
	require.Equal(t, "Attd Fields Renamed", name)
	require.Equal(t, "the squad that ships", description, "a name-only PATCH must not erase the description")

	// An explicitly empty description is a change, not an omission.
	r = ts.patch(t, path, map[string]string{"description": ""}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "clearing description: %s", r.Body)
	name, description, _, _ = attdTeamRow(t, ts, teamUUID)
	require.Equal(t, "Attd Fields Renamed", name)
	require.Empty(t, description)

	// A body carrying neither field is accepted, returns the team as it
	// stands, and writes nothing — applyRename returns early, so no
	// team.updated event joins the three the real changes above produced.
	eventsBefore := len(auditRowsFor(t, ts, "team.updated"))
	require.Equal(t, 3, eventsBefore, "premise: each real change wrote one team.updated")
	r = ts.patch(t, path, map[string]any{}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "empty patch: %s", r.Body)
	var unchanged struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &unchanged))
	require.Equal(t, "Attd Fields Renamed", unchanged.Name)
	require.Empty(t, unchanged.Description)
	require.Len(t, auditRowsFor(t, ts, "team.updated"), eventsBefore,
		"a PATCH that changed nothing must not write a team.updated event")
}

// TestAuthTeamTicketDomain_TeamReparentTriState: parent_id is tri-state on
// PATCH — absent leaves the parent alone, JSON null moves the team to the
// root, a uuid string moves it under that team.
//
// Defect this catches: collapsing "absent" and "null" into one case, the
// exact bug class that silently wiped every item's due_at in this codebase.
// parseReparentTarget is only reached when len(req.ParentID) > 0, and a
// json.RawMessage is empty precisely when the key was absent — so a
// regression to a *string or a *uuid.UUID field would make every rename also
// a move to the root. The absent-key assertion below is what fails in that
// case; the explicit-null assertion is what fails if the root move stops
// working. Neither passes without the other's branch intact.
func TestAuthTeamTicketDomain_TeamReparentTriState(t *testing.T) {
	ts := newTestServer(t)
	parentID := attdCreateTeam(t, ts, "attd-tri-parent", "Attd Tri Parent", nil)
	childID := attdCreateTeam(t, ts, "attd-tri-child", "Attd Tri Child", &parentID)
	childUUID := uuid.MustParse(childID)
	childPath := fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, childID)

	// Premise: it starts nested.
	_, _, parent, _ := attdTeamRow(t, ts, childUUID)
	require.NotNil(t, parent)
	require.Equal(t, parentID, parent.String())

	// Absent parent_id: a pure rename leaves the parent where it was.
	r := ts.patch(t, childPath, map[string]string{"name": "Attd Tri Child Renamed"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "rename: %s", r.Body)
	name, _, parent, _ := attdTeamRow(t, ts, childUUID)
	require.Equal(t, "Attd Tri Child Renamed", name)
	require.NotNil(t, parent, "an absent parent_id must not move the team")
	require.Equal(t, parentID, parent.String())
	require.Empty(t, auditRowsFor(t, ts, "team.reparented"),
		"a rename with no parent_id must not emit team.reparented")

	// Explicit JSON null: move to the root.
	r = attdRaw(t, ts, http.MethodPatch, childPath, `{"parent_id":null}`)
	require.Equal(t, http.StatusOK, r.StatusCode, "reparent to root: %s", r.Body)
	var moved struct {
		ParentID *string     `json:"parent_id"`
		Path     []uuid.UUID `json:"path"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &moved))
	require.Nil(t, moved.ParentID)
	require.Len(t, moved.Path, 1, "a root team's path is its own id alone")
	_, _, parent, _ = attdTeamRow(t, ts, childUUID)
	require.Nil(t, parent, "parent_id:null must move the team to the root")
	require.Len(t, auditRowsFor(t, ts, "team.reparented"), 1,
		"a move must emit exactly one team.reparented event")

	// A parent_id that is neither null nor a uuid string is refused, and the
	// team stays at the root.
	for name, body := range map[string]string{
		"number":  `{"parent_id":7}`,
		"bool":    `{"parent_id":true}`,
		"object":  `{"parent_id":{"id":"x"}}`,
		"not_id":  `{"parent_id":"banana"}`,
		"empty":   `{"parent_id":""}`,
		"blanks":  `{"parent_id":"   "}`,
		"array":   `{"parent_id":["x"]}`,
		"numeric": `{"parent_id":"12345"}`,
	} {
		t.Run("bad_parent/"+name, func(t *testing.T) {
			requireErrorCode(t, attdRaw(t, ts, http.MethodPatch, childPath, body),
				http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}
	_, _, parent, _ = attdTeamRow(t, ts, childUUID)
	require.Nil(t, parent, "a refused reparent must not have moved the team")
	require.Len(t, auditRowsFor(t, ts, "team.reparented"), 1,
		"a refused reparent must not emit a second event")

	// Moving it back under the parent still works — without this the
	// assertions above would also pass against a route that refused every
	// reparent.
	r = ts.patch(t, childPath, map[string]any{"parent_id": parentID}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "reparent back: %s", r.Body)
	_, _, parent, _ = attdTeamRow(t, ts, childUUID)
	require.NotNil(t, parent)
	require.Equal(t, parentID, parent.String())
}

// TestAuthTeamTicketDomain_TeamMutationsResolveTheTicketRefBeforeWriting:
// delete, member-add and member-remove all refuse an unusable ticket_ref, and
// all three refuse it before touching anything.
//
// Defect this catches: a handler that performs its write and then resolves the
// reference — or one that never resolves it at all. The status alone cannot
// tell those apart from correct behaviour, so each case below re-reads the
// thing the request would have changed: the team is still live, the member is
// still absent, the member who was there is still there. Delete is the one
// that matters most, because a soft delete followed by a 400 is unrecoverable
// through the API.
func TestAuthTeamTicketDomain_TeamMutationsResolveTheTicketRefBeforeWriting(t *testing.T) {
	ts := newTestServer(t)
	teamID := attdCreateTeam(t, ts, "attd-ref", "Attd Ref", nil)
	teamUUID := uuid.MustParse(teamID)
	teamPath := fmt.Sprintf("/api/v1/orgs/%s/teams/%s", ts.OrgID, teamID)

	enrolled := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	absent := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	// Premise: one member is in the team, the other is not.
	r := ts.put(t, fmt.Sprintf("%s/members/%s", teamPath, enrolled.ID),
		map[string]string{"role": "member"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "premise: enrol a member: %s", r.Body)

	memberCount := func() int {
		var n int
		require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM team_members WHERE team_id = $1`, teamUUID).Scan(&n))
		return n
	}
	require.Equal(t, 1, memberCount(), "premise: exactly one member")

	for name, ref := range map[string]string{
		"over_long": attdOverLongRef,
		"nul_byte":  "OPS-1%00",
	} {
		t.Run(name, func(t *testing.T) {
			q := "?ticket_ref=" + ref

			requireErrorCode(t, ts.delete(t, teamPath+q, true),
				http.StatusBadRequest, "VALIDATION_ERROR")
			_, _, _, deleted := attdTeamRow(t, ts, teamUUID)
			require.False(t, deleted, "a refused delete must not have soft-deleted the team")

			requireErrorCode(t, ts.put(t, fmt.Sprintf("%s/members/%s%s", teamPath, absent.ID, q),
				map[string]string{"role": "member"}, true),
				http.StatusBadRequest, "VALIDATION_ERROR")
			require.Equal(t, 1, memberCount(), "a refused PUT must not have enrolled anybody")

			requireErrorCode(t, ts.delete(t, fmt.Sprintf("%s/members/%s%s", teamPath, enrolled.ID, q), true),
				http.StatusBadRequest, "VALIDATION_ERROR")
			require.Equal(t, 1, memberCount(), "a refused DELETE must not have removed anybody")
		})
	}

	// The same three requests without the bad reference all succeed —
	// otherwise the refusals above would prove nothing.
	require.Equal(t, http.StatusOK,
		ts.put(t, fmt.Sprintf("%s/members/%s", teamPath, absent.ID),
			map[string]string{"role": "member"}, true).StatusCode)
	require.Equal(t, 2, memberCount())
	require.Equal(t, http.StatusNoContent,
		ts.delete(t, fmt.Sprintf("%s/members/%s", teamPath, enrolled.ID), true).StatusCode)
	require.Equal(t, http.StatusNoContent, ts.delete(t, teamPath, true).StatusCode)
	_, _, _, deleted := attdTeamRow(t, ts, teamUUID)
	require.True(t, deleted, "control: the team really can be deleted")
}

// TestAuthTeamTicketDomain_TeamMemberRoutesGuardTheTeamFirst: PUT and DELETE
// on /teams/{teamID}/members/{userID} resolve the team before anything else —
// an unknown or foreign-org team is 404 NOT_FOUND, an unparseable one is 400
// BAD_REQUEST.
//
// Defect this catches: dropping the teamInOrg call from either member handler.
// The membership write would then run against whatever {teamID} named,
// including a team in another organisation — a cross-org write reachable by
// any org admin who can guess a uuid. The foreign-org case is the one that
// proves it: it answers 404, indistinguishable from a team that never
// existed, so the route leaks nothing either way.
func TestAuthTeamTicketDomain_TeamMemberRoutesGuardTheTeamFirst(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/teams", ts.OrgID)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	foreignTeam := testutil.DefaultTeamID(t, ts.DB.Pool, otherOrg.ID)

	for name, teamID := range map[string]string{
		"unknown_team": uuid.NewString(),
		"foreign_team": foreignTeam.String(),
	} {
		t.Run(name, func(t *testing.T) {
			memberPath := fmt.Sprintf("%s/%s/members/%s", base, teamID, member.ID)
			requireErrorCode(t, ts.put(t, memberPath, map[string]string{"role": "member"}, true),
				http.StatusNotFound, "NOT_FOUND")
			requireErrorCode(t, ts.delete(t, memberPath, true), http.StatusNotFound, "NOT_FOUND")
		})
	}

	// No membership row was created anywhere, least of all in the other org.
	var rows int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM team_members WHERE user_id = $1 AND team_id = $2`,
		member.ID, foreignTeam).Scan(&rows))
	require.Zero(t, rows, "a 404 on a foreign team must not have written a membership")

	// An unparseable {teamID} is BAD_REQUEST on every route that names one —
	// a different answer from "no such team", and deliberately so.
	for _, bad := range []string{"banana", "12345", "null"} {
		t.Run("unparseable/"+bad, func(t *testing.T) {
			requireErrorCode(t, ts.get(t, base+"/"+bad, true), http.StatusBadRequest, "BAD_REQUEST")
			requireErrorCode(t, ts.get(t, base+"/"+bad+"/members", true), http.StatusBadRequest, "BAD_REQUEST")
			requireErrorCode(t, ts.patch(t, base+"/"+bad, map[string]string{"name": "X"}, true),
				http.StatusBadRequest, "BAD_REQUEST")
			requireErrorCode(t, ts.delete(t, base+"/"+bad, true), http.StatusBadRequest, "BAD_REQUEST")
			requireErrorCode(t, ts.put(t, fmt.Sprintf("%s/%s/members/%s", base, bad, member.ID),
				map[string]string{"role": "member"}, true), http.StatusBadRequest, "BAD_REQUEST")
			requireErrorCode(t, ts.delete(t, fmt.Sprintf("%s/%s/members/%s", base, bad, member.ID), true),
				http.StatusBadRequest, "BAD_REQUEST")
		})
	}
}

// ---------------------------------------------------------------------------
// tickets
// ---------------------------------------------------------------------------

// attdTicketFixture is a Beacon space with the org owner as the acting user.
type attdTicketFixture struct {
	ts      *testServer
	spaceID string
	base    string
}

func attdNewTicketFixture(t *testing.T) *attdTicketFixture {
	t.Helper()
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Attd Desk", "attd-desk", "beacon")
	return &attdTicketFixture{
		ts:      ts,
		spaceID: spaceID,
		base:    fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID),
	}
}

func (f *attdTicketFixture) create(t *testing.T, title string) string {
	t.Helper()
	r := f.ts.post(t, f.base, map[string]any{"title": title, "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket: %s", r.Body)
	var tk struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &tk))
	return tk.ID
}

// TestAuthTeamTicketDomain_TicketCreateRefusals: POST /tickets separates an
// undecodable body (BAD_REQUEST) from a decoded body the domain rejects
// (VALIDATION_ERROR), and neither leaves a row.
//
// Defect this catches: dropping Create's DecodeJSON check. The request would
// proceed on a zero-valued struct — empty title, empty priority — and be
// reported as "ticket title is required", a VALIDATION_ERROR that sends the
// client hunting for a missing field when the real fault is that their body
// never parsed. The row count at the end is what proves the refusals are
// refusals rather than 400s issued after a successful insert.
func TestAuthTeamTicketDomain_TicketCreateRefusals(t *testing.T) {
	f := attdNewTicketFixture(t)

	for name, body := range map[string]string{
		"truncated":     `{`,
		"not_json":      `nonsense`,
		"array":         `[{"title":"x"}]`,
		"empty":         ``,
		"unknown_field": `{"titel":"typo","priority":"medium"}`,
		"wrong_type":    `{"title":42,"priority":"medium"}`,
	} {
		t.Run("undecodable/"+name, func(t *testing.T) {
			requireErrorCode(t, attdRaw(t, f.ts, http.MethodPost, f.base, body),
				http.StatusBadRequest, "BAD_REQUEST")
		})
	}

	for name, body := range map[string]map[string]any{
		"missing_title":    {"priority": "medium"},
		"empty_title":      {"title": "", "priority": "medium"},
		"missing_priority": {"title": "No Priority"},
		"unknown_priority": {"title": "Bad Priority", "priority": "banana"},
		"cased_priority":   {"title": "Cased Priority", "priority": "HIGH"},
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			requireErrorCode(t, f.ts.post(t, f.base, body, true),
				http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}

	var rows int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM tickets WHERE space_id = $1`, uuid.MustParse(f.spaceID)).Scan(&rows))
	require.Zero(t, rows, "no refused create may have inserted a ticket")

	// The control: a well-formed body is accepted, so the refusals above are
	// about the bodies rather than about the route.
	require.Equal(t, http.StatusCreated,
		f.ts.post(t, f.base, map[string]any{"title": "Accepted", "priority": "medium"}, true).StatusCode)
}

// TestAuthTeamTicketDomain_TicketUpdateRefusesTheDomainInvalid: PATCH
// /tickets/{id} runs the same domain validation Create does, and a refusal
// leaves every field as it was.
//
// Defect this catches: dropping the error check on svc.Update. The handler
// has already mutated its in-memory copy by that point and answers 200 from
// it, so the client would be shown a ticket with a priority the database
// refused to store — a phantom write that only reveals itself on the next
// read. Re-reading the row through the API is what fails in that case.
//
// A6 moved one case out of the table below. `missing_title` — a body of
// {"priority": …} with no title key — asserted 400, and that was a true
// description of a handler which assigned every field unconditionally: an
// omitted title decoded as "" and was refused by the title-required rule. Under
// PATCH semantics an omitted key means "leave it alone", so it is now a 200 and
// is asserted as such further down.
//
// This is not the refusal being relaxed. "A ticket must have a title" is
// enforced exactly as before — `empty_title` below still refuses an explicitly
// blank one, and no PATCH can write "" into the column. What changed is only
// whether OMITTING the key is itself a validation error, which is the same
// distinction the item side settled (see TestUpdateItem_TitleOnlyPatchKeepsOtherFields
// and TestUpdateItem_ExplicitEmptyTitleIsStillRejected in
// item_patch_integration_test.go). Beacon disagreed with Vector on it only
// because nothing had ever called the ticket PATCH.
func TestAuthTeamTicketDomain_TicketUpdateRefusesTheDomainInvalid(t *testing.T) {
	f := attdNewTicketFixture(t)
	ticketID := f.create(t, "Attd Update Probe")
	path := f.base + "/" + ticketID

	for name, body := range map[string]map[string]any{
		"unknown_priority": {"title": "Still Fine", "priority": "banana"},
		"empty_priority":   {"title": "Still Fine", "priority": ""},
		"cased_priority":   {"title": "Still Fine", "priority": "Medium"},
		"empty_title":      {"title": "", "priority": "high"},
	} {
		t.Run(name, func(t *testing.T) {
			requireErrorCode(t, f.ts.patch(t, path, body, true),
				http.StatusBadRequest, "VALIDATION_ERROR")
		})
	}

	// Nothing moved: title and priority are as created.
	var title, priority string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT title, priority FROM tickets WHERE id = $1`, uuid.MustParse(ticketID)).Scan(&title, &priority))
	require.Equal(t, "Attd Update Probe", title, "a refused PATCH must not have renamed the ticket")
	require.Equal(t, "medium", priority, "a refused PATCH must not have changed the priority")
	require.Empty(t, auditRowsFor(t, f.ts, "ticket.updated"),
		"a refused PATCH must not write a ticket.updated event")

	// The case that moved. Omitting the title is not a refusal, and the stored
	// title survives it — which is what makes a due-date-only PATCH from the
	// ticket rail expressible at all. Ordered after the assertions above
	// because, unlike the table, this one is expected to change the row.
	t.Run("missing_title_is_a_partial_update", func(t *testing.T) {
		r := f.ts.patch(t, path, map[string]any{"priority": "high"}, true)
		require.Equal(t, http.StatusOK, r.StatusCode,
			"a PATCH that omits the title must be a partial update, not a refusal: %s", r.Body)

		var gotTitle, gotPriority string
		require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
			`SELECT title, priority FROM tickets WHERE id = $1`, uuid.MustParse(ticketID)).Scan(&gotTitle, &gotPriority))
		require.Equal(t, "Attd Update Probe", gotTitle, "an omitted title must be left alone, not blanked")
		require.Equal(t, "high", gotPriority, "the field the body did carry must have been applied")
	})

	// A valid PATCH is accepted and does change the row.
	r := f.ts.patch(t, path, map[string]any{"title": "Attd Updated", "priority": "high"}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "control patch: %s", r.Body)
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT title, priority FROM tickets WHERE id = $1`, uuid.MustParse(ticketID)).Scan(&title, &priority))
	require.Equal(t, "Attd Updated", title)
	require.Equal(t, "high", priority)
}

// TestAuthTeamTicketDomain_AssignWithNullBodyUnassigns: POST
// /tickets/{id}/assign with an explicit assignee_id of null is the second
// spelling of unassign, and it must behave exactly like DELETE on the same
// path — same row state, same audit event.
//
// Defect this catches: dropping the nil branch from Assign. *req.AssigneeID
// would dereference a nil pointer and panic (a 500 through the Recoverer),
// and the audit trail would be the thing that goes quiet first: an operator
// clearing an assignee through this spelling would leave no ticket.unassigned
// row at all. The event-type assertion is deliberate — a branch that fell
// through to the assign path would answer 200 and write ticket.assigned, which
// a status-only test would not notice.
func TestAuthTeamTicketDomain_AssignWithNullBodyUnassigns(t *testing.T) {
	f := attdNewTicketFixture(t)
	ticketID := f.create(t, "Attd Assign Probe")
	ticketUUID := uuid.MustParse(ticketID)
	assignPath := f.base + "/" + ticketID + "/assign"

	assignee := testutil.CreateTestUserWithRole(t, f.ts.DB.Pool, f.ts.OrgID, "member")

	// Premise: it is assigned to somebody.
	r := f.ts.post(t, assignPath, map[string]any{"assignee_id": assignee.ID.String()}, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "premise assign: %s", r.Body)
	var held *uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT assignee_id FROM tickets WHERE id = $1`, ticketUUID).Scan(&held))
	require.NotNil(t, held)
	require.Equal(t, assignee.ID, *held)

	// The null spelling clears it.
	r = attdRaw(t, f.ts, http.MethodPost, assignPath, `{"assignee_id":null}`)
	require.Equal(t, http.StatusOK, r.StatusCode, "assign null: %s", r.Body)
	var body struct {
		AssigneeID *string `json:"assignee_id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	require.Nil(t, body.AssigneeID, "the response must report the ticket as unassigned")

	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT assignee_id FROM tickets WHERE id = $1`, ticketUUID).Scan(&held))
	require.Nil(t, held, "assignee_id:null must clear the assignee")

	// It is recorded as an unassignment, not as an assignment.
	unassigned := auditRowsFor(t, f.ts, "ticket.unassigned")
	require.Len(t, unassigned, 1, "the null spelling must write exactly one ticket.unassigned")
	require.Equal(t, "ticket", unassigned[0].EntityKind)
	require.Equal(t, ticketUUID, unassigned[0].EntityID)
	require.NotNil(t, unassigned[0].ActorID)
	require.Equal(t, f.ts.UserID, *unassigned[0].ActorID)
	require.Len(t, auditRowsFor(t, f.ts, "ticket.assigned"), 1,
		"the null spelling must not have written a second ticket.assigned")

	// Unassigning an already-unassigned ticket is an idempotent 200, by both
	// spellings — neither is a conflict.
	r = attdRaw(t, f.ts, http.MethodPost, assignPath, `{"assignee_id":null}`)
	require.Equal(t, http.StatusOK, r.StatusCode, "repeat null assign: %s", r.Body)
	require.Equal(t, http.StatusOK, f.ts.delete(t, assignPath, true).StatusCode,
		"DELETE on an unassigned ticket is the same no-op")
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT assignee_id FROM tickets WHERE id = $1`, ticketUUID).Scan(&held))
	require.Nil(t, held)

	// The null branch carries its own error handling: a well-formed ticket id
	// naming nothing is 404 NOT_FOUND, exactly as the assign branch answers.
	// Without this the branch could return the service error unmapped and a
	// stale bookmark would surface as a 500.
	missing := f.base + "/" + uuid.NewString() + "/assign"
	requireErrorCode(t, attdRaw(t, f.ts, http.MethodPost, missing, `{"assignee_id":null}`),
		http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, f.ts.post(t, missing, map[string]any{"assignee_id": assignee.ID.String()}, true),
		http.StatusNotFound, "NOT_FOUND")
	requireErrorCode(t, f.ts.delete(t, missing, true), http.StatusNotFound, "NOT_FOUND")
}
