package adapters_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/invites"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Adapter refusal paths, against real PostgreSQL.
//
// The store layer is where a not-found becomes a domain sentinel, where an
// org boundary is enforced, where a soft-deleted row stops existing, and
// where a unique-index collision becomes a 409 rather than a 500. Every one
// of those is a branch the happy-path suites never take, and every one of
// them is a security or correctness boundary when it goes wrong: an adapter
// that forgets `org_id` reads another tenant's row and reports success.
//
// Each test below is written so that DELETING the check it targets makes it
// fail — the spec §2 negative-test question. Where a refusal could be
// satisfied by an adapter that simply refuses everything, the test carries a
// control case that must succeed, so "filtered correctly" is distinguished
// from "returned nothing at all".

// ---------------------------------------------------------------------------
// shared helpers (all prefixed `adn` — package adapters_test is one namespace)
// ---------------------------------------------------------------------------

// adnTokenHash produces a distinct invite token hash per seed. The invite
// adapter looks invites up by hash, so fixtures must not collide.
func adnTokenHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// adnInviteEnv is one org with an admin and a wired invite adapter.
type adnInviteEnv struct {
	db    *testutil.TestDB
	ctx   context.Context
	org   testutil.Org
	admin testutil.User
	a     *adapters.InviteAdapter
}

func adnNewInviteEnv(t *testing.T) adnInviteEnv {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	admin := testutil.CreateTestUser(t, db.Pool, org.ID)
	return adnInviteEnv{
		db: db, ctx: context.Background(), org: org, admin: admin,
		a: adapters.NewInviteAdapter(db.Pool),
	}
}

// adnInvite creates an invite in the env's org, failing the test if the
// adapter refuses it.
func (e adnInviteEnv) adnInvite(t *testing.T, email string, teamID *uuid.UUID) (invites.Invite, string) {
	t.Helper()
	hash := adnTokenHash(email + "|" + uuid.NewString())
	inv, err := e.a.Create(e.ctx, invites.Invite{
		OrgID:     e.org.ID,
		Email:     email,
		OrgRole:   "member",
		TeamID:    teamID,
		InvitedBy: e.admin.ID,
		ExpiresAt: time.Now().UTC().Add(72 * time.Hour),
	}, hash)
	require.NoError(t, err, "fixture invite for %s", email)
	return inv, hash
}

// adnMakeTeam inserts a live root team directly (teams.path is the migration
// 022 materialised path: a root team's path is just itself).
func adnMakeTeam(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, slug string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO teams (id, org_id, slug, name, path) VALUES ($1,$2,$3,$4,ARRAY[$1]::uuid[])`,
		id, orgID, slug+"-"+uuid.NewString()[:8], "Team "+slug)
	require.NoError(t, err)
	return id
}

func adnSoftDeleteTeam(t *testing.T, pool *pgxpool.Pool, teamID uuid.UUID) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`UPDATE teams SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, teamID)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())
}

// adnUserState reads the three user columns the people adapter is trusted to
// leave alone when it refuses an operation.
func adnUserState(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) (active bool, generation int32, displayName string) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT is_active, token_generation, display_name FROM users WHERE id = $1`, userID).
		Scan(&active, &generation, &displayName)
	require.NoError(t, err)
	return active, generation, displayName
}

func adnSetDisplayName(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, name string) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`UPDATE users SET display_name = $2 WHERE id = $1`, userID, name)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())
}

func adnMembershipRole(t *testing.T, pool *pgxpool.Pool, orgID, userID uuid.UUID) string {
	t.Helper()
	var role string
	err := pool.QueryRow(context.Background(),
		`SELECT role FROM memberships WHERE org_id = $1 AND user_id = $2`, orgID, userID).Scan(&role)
	require.NoError(t, err)
	return role
}

func adnPrimaryTeamID(t *testing.T, pool *pgxpool.Pool, orgID, userID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`SELECT team_id FROM team_members WHERE org_id = $1 AND user_id = $2 AND is_primary`,
		orgID, userID).Scan(&id)
	require.NoError(t, err)
	return id
}

func adnScalarCount(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

// adnCreatePage inserts a page with the dotted materialised path the move
// transaction relies on: a root page's path is its own id, a child's is
// parent.path + "." + own id.
func adnCreatePage(t *testing.T, q *generated.Queries, spaceID, authorID uuid.UUID, parent *generated.Page, title string) generated.Page {
	t.Helper()
	id := uuid.New()
	params := generated.CreatePageParams{
		ID: id, SpaceID: spaceID, Title: title, Content: title + " body",
		AuthorID: authorID, Position: 0, Path: id.String(),
	}
	if parent != nil {
		params.ParentID = pgtype.UUID{Bytes: parent.ID, Valid: true}
		params.Path = parent.Path + "." + id.String()
	}
	page, err := q.CreatePage(context.Background(), params)
	require.NoError(t, err)
	return page
}

// adnCreateShare makes one active share. audienceTeam nil means the 'org'
// audience; non-nil means a team-audience row, which is the branch
// writeShareRevokedTx records an audience_id for.
func adnCreateShare(t *testing.T, q *generated.Queries, orgID, spaceID uuid.UUID, entityType string, entityID uuid.UUID, audienceTeam *uuid.UUID, cascade bool, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	params := generated.CreateEntityShareParams{
		ID: uuid.New(), OrgID: orgID, SpaceID: spaceID,
		EntityType: entityType, EntityID: entityID,
		Audience: "org", Cascade: cascade, CreatedBy: createdBy,
	}
	if audienceTeam != nil {
		params.Audience = "team"
		params.AudienceID = pgtype.UUID{Bytes: *audienceTeam, Valid: true}
	}
	share, err := q.CreateEntityShare(context.Background(), params)
	require.NoError(t, err)
	return share.ID
}

func adnShareIsActive(t *testing.T, pool *pgxpool.Pool, shareID uuid.UUID) bool {
	t.Helper()
	var revoked *time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT revoked_at FROM entity_shares WHERE id = $1`, shareID).Scan(&revoked)
	require.NoError(t, err)
	return revoked == nil
}

// adnShareRevokedPayloads returns the payload of every share.revoked audit
// row in the org, keyed by the share id the event names.
func adnShareRevokedPayloads(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) map[uuid.UUID]map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT entity_id, payload FROM audit_log
		 WHERE org_id = $1 AND action = 'share.revoked' AND entity_kind = 'share'`, orgID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[uuid.UUID]map[string]string{}
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		require.NoError(t, rows.Scan(&id, &raw))
		meta := map[string]string{}
		require.NoError(t, json.Unmarshal(raw, &meta))
		out[id] = meta
	}
	require.NoError(t, rows.Err())
	return out
}

// ---------------------------------------------------------------------------
// invites
// ---------------------------------------------------------------------------

// TestAdapterNeg_InviteCreate_TeamOutsideTheOrgIsNotFound: the initial team
// on an invite is attacker-controlled input from an admin of org A. If the
// adapter only checked that the team EXISTS, an admin of A could pre-enrol
// their invitee into a team of org B.
//
// Fails-before: delete the `team.OrgID != inv.OrgID` arm of checkInviteTeam
// and the first Create succeeds. The control at the end proves the refusal is
// about the org boundary, not about teams generally.
func TestAdapterNeg_InviteCreate_TeamOutsideTheOrgIsNotFound(t *testing.T) {
	env := adnNewInviteEnv(t)
	other := testutil.CreateTestOrg(t, env.db.Pool)
	foreignTeam := testutil.DefaultTeamID(t, env.db.Pool, other.ID)

	_, err := env.a.Create(env.ctx, invites.Invite{
		OrgID: env.org.ID, Email: "cross-org@azimuthal.dev", OrgRole: "member",
		TeamID: &foreignTeam, InvitedBy: env.admin.ID,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, adnTokenHash("cross-org"))
	require.ErrorIs(t, err, invites.ErrTeamNotFound,
		"a team belonging to another org must not be namable as an invite's initial team")

	pending, err := env.a.ListActive(env.ctx, env.org.ID)
	require.NoError(t, err)
	require.Empty(t, pending, "the refused invite must not have been persisted")

	ownTeam := testutil.DefaultTeamID(t, env.db.Pool, env.org.ID)
	inv, _ := env.adnInvite(t, "same-org@azimuthal.dev", &ownTeam)
	require.Equal(t, ownTeam, *inv.TeamID,
		"a live team of the invite's OWN org must be accepted, or the refusal above proves nothing")
}

// TestAdapterNeg_InviteCreate_SoftDeletedTeamIsNotFound: a team that has been
// deleted is gone for every purpose. Naming it would create an invite that
// enrols into a dead team.
//
// Fails-before: drop `AND deleted_at IS NULL` from GetTeamByID (and the
// DeletedAt guard beside it) and the create succeeds.
func TestAdapterNeg_InviteCreate_SoftDeletedTeamIsNotFound(t *testing.T) {
	env := adnNewInviteEnv(t)
	team := adnMakeTeam(t, env.db.Pool, env.org.ID, "doomed")

	// Control first: while the team is live the invite is accepted.
	live, _ := env.adnInvite(t, "before@azimuthal.dev", &team)
	require.Equal(t, team, *live.TeamID)

	adnSoftDeleteTeam(t, env.db.Pool, team)

	_, err := env.a.Create(env.ctx, invites.Invite{
		OrgID: env.org.ID, Email: "after@azimuthal.dev", OrgRole: "member",
		TeamID: &team, InvitedBy: env.admin.ID,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, adnTokenHash("after"))
	require.ErrorIs(t, err, invites.ErrTeamNotFound,
		"a soft-deleted team must not be namable as an invite's initial team")
}

// TestAdapterNeg_InviteCreate_ExistingMemberOfThisOrgIsRefused covers
// ErrAlreadyMember, and — the part that makes it a real test — proves the
// refusal is scoped to THIS org. An email that has an account elsewhere is
// still invitable here.
//
// Fails-before: delete the GetMembership arm of checkInviteCreatable and the
// first Create succeeds; widen it to "the email has an account anywhere" and
// the second Create starts failing.
func TestAdapterNeg_InviteCreate_ExistingMemberOfThisOrgIsRefused(t *testing.T) {
	env := adnNewInviteEnv(t)
	member := testutil.CreateTestUserWithRole(t, env.db.Pool, env.org.ID, "member")

	_, err := env.a.Create(env.ctx, invites.Invite{
		OrgID: env.org.ID, Email: member.Email, OrgRole: "member",
		InvitedBy: env.admin.ID, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, adnTokenHash("already-member"))
	require.ErrorIs(t, err, invites.ErrAlreadyMember)

	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	otherAdmin := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)
	_, err = env.a.Create(env.ctx, invites.Invite{
		OrgID: otherOrg.ID, Email: member.Email, OrgRole: "member",
		InvitedBy: otherAdmin.ID, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, adnTokenHash("other-org-invite"))
	require.NoError(t, err,
		"the same email must remain invitable into an org it is NOT already a member of")
}

// TestAdapterNeg_InviteCreate_SecondActiveInviteMapsToDuplicate is the
// unique-violation mapping: invites_one_active_per_email is a PARTIAL index,
// and the adapter has to turn its 23505 into ErrDuplicateInvite (409) rather
// than let a raw pgx error become a 500.
//
// Fails-before: delete the uniqueViolation branch in Create and the second
// call returns a wrapped pgx error, so ErrorIs fails. Making the index
// non-partial (or checking the constraint name wrongly) breaks the second
// half, where revoking frees the slot.
func TestAdapterNeg_InviteCreate_SecondActiveInviteMapsToDuplicate(t *testing.T) {
	env := adnNewInviteEnv(t)
	const email = "duplicate@azimuthal.dev"
	first, _ := env.adnInvite(t, email, nil)

	_, err := env.a.Create(env.ctx, invites.Invite{
		OrgID: env.org.ID, Email: email, OrgRole: "member",
		InvitedBy: env.admin.ID, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, adnTokenHash("duplicate-second"))
	require.ErrorIs(t, err, invites.ErrDuplicateInvite,
		"a second ACTIVE invite for the same email must map to the domain error, not a raw constraint error")

	require.NoError(t, env.a.Revoke(env.ctx, env.org.ID, first.ID))
	_, err = env.a.Create(env.ctx, invites.Invite{
		OrgID: env.org.ID, Email: email, OrgRole: "member",
		InvitedBy: env.admin.ID, ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, adnTokenHash("duplicate-third"))
	require.NoError(t, err,
		"revoking frees the slot — the index is partial on active rows, and re-inviting must work")
}

// TestAdapterNeg_InviteGetByID_IsOrgScoped: an invite id leaked (or guessed)
// by an admin of another org must read as absent, not as someone else's row.
//
// Fails-before: drop `AND org_id = $2` from GetInviteByID and the first
// lookup returns the invite.
func TestAdapterNeg_InviteGetByID_IsOrgScoped(t *testing.T) {
	env := adnNewInviteEnv(t)
	inv, _ := env.adnInvite(t, "scoped@azimuthal.dev", nil)
	other := testutil.CreateTestOrg(t, env.db.Pool)

	_, err := env.a.GetByID(env.ctx, other.ID, inv.ID)
	require.ErrorIs(t, err, invites.ErrNotFound, "another org must not be able to read this invite")

	_, err = env.a.GetByID(env.ctx, env.org.ID, uuid.New())
	require.ErrorIs(t, err, invites.ErrNotFound, "an unknown id maps to the domain sentinel")

	got, err := env.a.GetByID(env.ctx, env.org.ID, inv.ID)
	require.NoError(t, err)
	require.Equal(t, inv.ID, got.ID, "the owning org still reads it")
}

// TestAdapterNeg_InviteRevoke_OnlyActiveInvitesOfTheOwningOrg: Revoke reports
// "0 rows updated" for three different reasons — wrong org, unknown id,
// already dead — and all three must surface as ErrNotFound rather than a
// silent success.
//
// Fails-before: return nil unconditionally from Revoke and the double-revoke
// assertion fails; drop the org predicate and the cross-org revoke succeeds,
// which the ListActive assertion catches.
func TestAdapterNeg_InviteRevoke_OnlyActiveInvitesOfTheOwningOrg(t *testing.T) {
	env := adnNewInviteEnv(t)
	inv, _ := env.adnInvite(t, "revoke@azimuthal.dev", nil)
	other := testutil.CreateTestOrg(t, env.db.Pool)

	require.ErrorIs(t, env.a.Revoke(env.ctx, other.ID, inv.ID), invites.ErrNotFound)
	pending, err := env.a.ListActive(env.ctx, env.org.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1, "a cross-org revoke must not have killed the invite")

	require.ErrorIs(t, env.a.Revoke(env.ctx, env.org.ID, uuid.New()), invites.ErrNotFound)

	require.NoError(t, env.a.Revoke(env.ctx, env.org.ID, inv.ID))
	require.ErrorIs(t, env.a.Revoke(env.ctx, env.org.ID, inv.ID), invites.ErrNotFound,
		"revoking an already-revoked invite must report not-found, not succeed a second time")

	pending, err = env.a.ListActive(env.ctx, env.org.ID)
	require.NoError(t, err)
	require.Empty(t, pending, "a revoked invite leaves the pending list")
}

// TestAdapterNeg_InviteRefreshToken_RefusesForeignAndDeadInvites. Resend
// rotates the token in place, so a cross-org resend would both leak a fresh
// token and silently break the real admin's outstanding link.
//
// Fails-before: drop the org predicate from RefreshInviteToken and the
// foreign resend succeeds — caught by the assertion that the ORIGINAL token
// still resolves afterwards.
func TestAdapterNeg_InviteRefreshToken_RefusesForeignAndDeadInvites(t *testing.T) {
	env := adnNewInviteEnv(t)
	inv, original := env.adnInvite(t, "resend@azimuthal.dev", nil)
	other := testutil.CreateTestOrg(t, env.db.Pool)
	newExpiry := time.Now().UTC().Add(240 * time.Hour)

	_, err := env.a.RefreshToken(env.ctx, other.ID, inv.ID, adnTokenHash("foreign-resend"), newExpiry)
	require.ErrorIs(t, err, invites.ErrNotFound)
	insp, err := env.a.InspectByTokenHash(env.ctx, original)
	require.NoError(t, err, "the foreign resend must not have rotated the token")
	require.Equal(t, "active", insp.State)

	rotated := adnTokenHash("real-resend")
	refreshed, err := env.a.RefreshToken(env.ctx, env.org.ID, inv.ID, rotated, newExpiry)
	require.NoError(t, err)
	require.Equal(t, inv.ID, refreshed.ID)

	_, err = env.a.InspectByTokenHash(env.ctx, original)
	require.ErrorIs(t, err, invites.ErrNotFound, "the previous link must stop working the moment resend commits")
	insp, err = env.a.InspectByTokenHash(env.ctx, rotated)
	require.NoError(t, err)
	require.Equal(t, "active", insp.State)

	require.NoError(t, env.a.Revoke(env.ctx, env.org.ID, inv.ID))
	_, err = env.a.RefreshToken(env.ctx, env.org.ID, inv.ID, adnTokenHash("post-revoke"), newExpiry)
	require.ErrorIs(t, err, invites.ErrNotFound, "a revoked invite cannot be resent back to life")
}

// TestAdapterNeg_InviteInspect_ClassifiesEveryLifecycleState drives all four
// arms of inviteState plus the unknown-token refusal. The acceptance page
// renders from this classification, so a state collapsed into "active" would
// invite someone to submit a form that can only fail.
//
// Fails-before: collapse any arm of inviteState (for example drop the
// RevokedAt case) and that state's assertion fails.
func TestAdapterNeg_InviteInspect_ClassifiesEveryLifecycleState(t *testing.T) {
	env := adnNewInviteEnv(t)

	_, err := env.a.InspectByTokenHash(env.ctx, adnTokenHash("nothing-hashes-to-this"))
	require.ErrorIs(t, err, invites.ErrNotFound, "an unknown token must not leak an empty inspection")

	_, activeHash := env.adnInvite(t, "state-active@azimuthal.dev", nil)
	insp, err := env.a.InspectByTokenHash(env.ctx, activeHash)
	require.NoError(t, err)
	require.Equal(t, "active", insp.State)
	require.Equal(t, env.org.Name, insp.OrgName)
	require.False(t, insp.ExistingAccount, "no account holds this email yet")

	revoked, revokedHash := env.adnInvite(t, "state-revoked@azimuthal.dev", nil)
	require.NoError(t, env.a.Revoke(env.ctx, env.org.ID, revoked.ID))
	insp, err = env.a.InspectByTokenHash(env.ctx, revokedHash)
	require.NoError(t, err)
	require.Equal(t, "revoked", insp.State)

	expired, expiredHash := env.adnInvite(t, "state-expired@azimuthal.dev", nil)
	_, err = env.db.Pool.Exec(env.ctx,
		`UPDATE invites SET expires_at = now() - interval '1 hour' WHERE id = $1`, expired.ID)
	require.NoError(t, err)
	insp, err = env.a.InspectByTokenHash(env.ctx, expiredHash)
	require.NoError(t, err)
	require.Equal(t, "expired", insp.State)

	accepted, acceptedHash := env.adnInvite(t, "state-accepted@azimuthal.dev", nil)
	_, err = env.db.Pool.Exec(env.ctx,
		`UPDATE invites SET accepted_at = now(), accepted_user_id = $2 WHERE id = $1`,
		accepted.ID, env.admin.ID)
	require.NoError(t, err)
	insp, err = env.a.InspectByTokenHash(env.ctx, acceptedHash)
	require.NoError(t, err)
	require.Equal(t, "accepted", insp.State)

	// ExistingAccount is the other branch of Inspect: an email that already
	// has an account elsewhere gets the "confirm joining" page, not the
	// "choose a password" page.
	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	existing := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)
	_, existingHash := env.adnInvite(t, existing.Email, nil)
	insp, err = env.a.InspectByTokenHash(env.ctx, existingHash)
	require.NoError(t, err)
	require.True(t, insp.ExistingAccount,
		"an email that already has an account must be reported as such")
}

// TestAdapterNeg_InviteAccept_DeadInvitesAreRefusedByState: acceptance must
// distinguish revoked from expired from already-accepted, because each has a
// different remedy, and none of them may create a membership.
//
// Fails-before: delete the switch in loadAcceptableInvite and acceptance
// proceeds to MarkInviteAccepted, whose guard returns 0 rows — so every case
// collapses into ErrNotFound and three of the four assertions fail.
func TestAdapterNeg_InviteAccept_DeadInvitesAreRefusedByState(t *testing.T) {
	env := adnNewInviteEnv(t)
	before := adnScalarCount(t, env.db.Pool, `SELECT count(*) FROM memberships WHERE org_id = $1`, env.org.ID)

	_, err := env.a.Accept(env.ctx, adnTokenHash("no-such-token"), nil)
	require.ErrorIs(t, err, invites.ErrNotFound)

	revoked, revokedHash := env.adnInvite(t, "accept-revoked@azimuthal.dev", nil)
	require.NoError(t, env.a.Revoke(env.ctx, env.org.ID, revoked.ID))
	_, err = env.a.Accept(env.ctx, revokedHash, &invites.NewUser{DisplayName: "R", Password: "password123"})
	require.ErrorIs(t, err, invites.ErrRevoked)

	expired, expiredHash := env.adnInvite(t, "accept-expired@azimuthal.dev", nil)
	_, err = env.db.Pool.Exec(env.ctx,
		`UPDATE invites SET expires_at = now() - interval '1 hour' WHERE id = $1`, expired.ID)
	require.NoError(t, err)
	_, err = env.a.Accept(env.ctx, expiredHash, &invites.NewUser{DisplayName: "E", Password: "password123"})
	require.ErrorIs(t, err, invites.ErrExpired)

	used, usedHash := env.adnInvite(t, "accept-used@azimuthal.dev", nil)
	_, err = env.db.Pool.Exec(env.ctx,
		`UPDATE invites SET accepted_at = now(), accepted_user_id = $2 WHERE id = $1`, used.ID, env.admin.ID)
	require.NoError(t, err)
	_, err = env.a.Accept(env.ctx, usedHash, &invites.NewUser{DisplayName: "U", Password: "password123"})
	require.ErrorIs(t, err, invites.ErrAlreadyAccepted)

	require.Equal(t, before,
		adnScalarCount(t, env.db.Pool, `SELECT count(*) FROM memberships WHERE org_id = $1`, env.org.ID),
		"no refused acceptance may have created a membership")
}

// TestAdapterNeg_InviteAccept_DeactivatedAccountIsRefusedAndRollsBack: an
// admin deactivated this account; an outstanding invite must not be a way
// back in. And because the refusal happens mid-transaction, the invite must
// still be usable once the account is reactivated.
//
// Fails-before: delete the `!existingUser.IsActive` guard in
// resolveAcceptUser and the first Accept succeeds, adding a membership for a
// deactivated user.
func TestAdapterNeg_InviteAccept_DeactivatedAccountIsRefusedAndRollsBack(t *testing.T) {
	env := adnNewInviteEnv(t)
	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	existing := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)
	_, err := env.db.Pool.Exec(env.ctx, `UPDATE users SET is_active = false WHERE id = $1`, existing.ID)
	require.NoError(t, err)

	_, hash := env.adnInvite(t, existing.Email, nil)

	_, err = env.a.Accept(env.ctx, hash, nil)
	require.ErrorIs(t, err, invites.ErrAccountInactive)
	require.Equal(t, 0,
		adnScalarCount(t, env.db.Pool,
			`SELECT count(*) FROM memberships WHERE org_id = $1 AND user_id = $2`, env.org.ID, existing.ID),
		"the refused acceptance must not have added a membership")

	insp, err := env.a.InspectByTokenHash(env.ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "active", insp.State,
		"the invite must not have been consumed by the refused acceptance — the transaction rolled back")

	_, err = env.db.Pool.Exec(env.ctx, `UPDATE users SET is_active = true WHERE id = $1`, existing.ID)
	require.NoError(t, err)
	out, err := env.a.Accept(env.ctx, hash, nil)
	require.NoError(t, err, "once reactivated the same invite works — the refusal was about the account, not the invite")
	require.True(t, out.ExistingAccount)
}

// TestAdapterNeg_InviteAccept_NewAccountWithoutRegistrationFieldsIsRefused
// covers the nil-newUser arm of resolveAcceptUser, and asserts the invite
// survives the refusal so the caller can retry with the fields.
//
// Fails-before: remove the `newUser == nil` check and CreateUser is called
// with an empty display name and an unhashable password — the error would be
// a 500-shaped internal error rather than the 400 the domain sentinel maps to.
func TestAdapterNeg_InviteAccept_NewAccountWithoutRegistrationFieldsIsRefused(t *testing.T) {
	env := adnNewInviteEnv(t)
	const email = "fresh-account@azimuthal.dev"
	_, hash := env.adnInvite(t, email, nil)

	_, err := env.a.Accept(env.ctx, hash, nil)
	require.ErrorIs(t, err, invites.ErrDisplayNameAndPasswordRequired)
	require.Equal(t, 0,
		adnScalarCount(t, env.db.Pool, `SELECT count(*) FROM users WHERE email = $1`, email),
		"the refused acceptance must not have created an account")

	out, err := env.a.Accept(env.ctx, hash,
		&invites.NewUser{DisplayName: "Fresh Person", Password: "password123"})
	require.NoError(t, err, "the same invite must still be usable once the fields are supplied")
	require.False(t, out.ExistingAccount)
	require.Equal(t, email, out.User.Email)
	require.Equal(t, env.org.ID, out.OrgID)
	require.Equal(t, env.org.Slug, out.OrgSlug)
}

// TestAdapterNeg_InviteAccept_SoftDeletedInitialTeamFallsBackToDefault is the
// ADR-0006 "never teamless" obligation on the acceptance path: the invite's
// team was deleted between issue and acceptance, and the new member must land
// in the org default team rather than in a dead team or in none at all.
//
// Fails-before: drop the fallback in resolveInviteTeam and the user is
// enrolled into the soft-deleted team (team_members has no deleted_at
// predicate on its FK), so the primary-team assertion fails.
func TestAdapterNeg_InviteAccept_SoftDeletedInitialTeamFallsBackToDefault(t *testing.T) {
	env := adnNewInviteEnv(t)
	team := adnMakeTeam(t, env.db.Pool, env.org.ID, "transient")
	_, hash := env.adnInvite(t, "fallback@azimuthal.dev", &team)
	adnSoftDeleteTeam(t, env.db.Pool, team)

	out, err := env.a.Accept(env.ctx, hash,
		&invites.NewUser{DisplayName: "Fallback Person", Password: "password123"})
	require.NoError(t, err)

	defaultTeam := testutil.DefaultTeamID(t, env.db.Pool, env.org.ID)
	require.Equal(t, defaultTeam, adnPrimaryTeamID(t, env.db.Pool, env.org.ID, out.User.ID),
		"a dead initial team must fall back to the org default team, as primary")
	require.Equal(t, 0,
		adnScalarCount(t, env.db.Pool,
			`SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, team, out.User.ID),
		"nobody may be enrolled into a soft-deleted team")
}

// TestAdapterNeg_InviteAccept_ExistingAccountJoinsWithoutASecondUserRow: the
// invite path is deliberately not the register path. Accepting with an email
// that already has an account adds a membership to THAT account — it never
// creates a second user for the same email, which would split the person in
// two and break every login.
//
// Fails-before: make resolveAcceptUser always create a user and the
// single-row assertion fails (and, with the users.email unique index in
// place, the insert fails outright).
func TestAdapterNeg_InviteAccept_ExistingAccountJoinsWithoutASecondUserRow(t *testing.T) {
	env := adnNewInviteEnv(t)
	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	existing := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)
	_, hash := env.adnInvite(t, existing.Email, nil)

	out, err := env.a.Accept(env.ctx, hash, nil)
	require.NoError(t, err)
	require.True(t, out.ExistingAccount)
	require.Equal(t, existing.ID, out.User.ID, "acceptance must attach to the existing account")
	require.Equal(t, 1,
		adnScalarCount(t, env.db.Pool, `SELECT count(*) FROM users WHERE email = $1`, existing.Email),
		"no second user row may be created for an email that already has one")
	require.Equal(t, "member", adnMembershipRole(t, env.db.Pool, env.org.ID, existing.ID))

	// And the invite is consumed: a replayed link cannot join twice.
	_, err = env.a.Accept(env.ctx, hash, nil)
	require.ErrorIs(t, err, invites.ErrAlreadyAccepted)
}

// TestAdapterNeg_InviteAccept_MembershipThatAppearedMeanwhileIsAnIdempotentJoin
// covers the window the invite lifecycle cannot close: the invite was issued
// while the email was a stranger, and by the time the link is clicked the
// person has joined by another route (a second invite, a provisioning run).
// Acceptance then has to be a no-op join — it must not insert a second
// membership, must not downgrade the role they already hold to the role the
// stale invite carried, and must not move their primary team.
//
// Fails-before: delete the GetMembership check in addAcceptMembership and the
// role is overwritten with the invite's 'member' (or the insert violates the
// membership uniqueness); make enrolAcceptTeam set primary unconditionally
// and the primary-team assertion fails.
func TestAdapterNeg_InviteAccept_MembershipThatAppearedMeanwhileIsAnIdempotentJoin(t *testing.T) {
	env := adnNewInviteEnv(t)
	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	person := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)

	// The invite is issued while they are still a stranger to this org.
	_, hash := env.adnInvite(t, person.Email, nil)

	// ...and they join by another route before clicking it, landing on a
	// non-default team as primary and with a higher org role.
	elsewhere := adnMakeTeam(t, env.db.Pool, env.org.ID, "arrived-early")
	_, err := env.db.Pool.Exec(env.ctx,
		`INSERT INTO memberships (id, org_id, user_id, role) VALUES ($1,$2,$3,'admin')`,
		uuid.New(), env.org.ID, person.ID)
	require.NoError(t, err)
	_, err = env.db.Pool.Exec(env.ctx,
		`INSERT INTO team_members (team_id, user_id, org_id, is_primary) VALUES ($1,$2,$3,true)`,
		elsewhere, person.ID, env.org.ID)
	require.NoError(t, err)

	out, err := env.a.Accept(env.ctx, hash, nil)
	require.NoError(t, err, "the stale invite must still be consumable — it is simply a no-op join")
	require.True(t, out.ExistingAccount)

	require.Equal(t, 1, adnScalarCount(t, env.db.Pool,
		`SELECT count(*) FROM memberships WHERE org_id = $1 AND user_id = $2`, env.org.ID, person.ID),
		"acceptance must not insert a second membership")
	require.Equal(t, "admin", adnMembershipRole(t, env.db.Pool, env.org.ID, person.ID),
		"a stale invite's org role must not downgrade a membership that already exists")
	require.Equal(t, elsewhere, adnPrimaryTeamID(t, env.db.Pool, env.org.ID, person.ID),
		"acceptance must not move a primary team the person already holds")
}

// ---------------------------------------------------------------------------
// people
// ---------------------------------------------------------------------------

// TestAdapterNeg_PeopleLifecycle_RefusesTargetsOutsideTheActingOrg is the
// tenancy sweep over the whole admin lifecycle surface. Every one of these
// methods takes an orgID and a userID from the URL; without requireMembership
// an admin of org A could deactivate, rename, re-role or force-logout any
// user in the product by id.
//
// Fails-before: delete requireMembership from any one of these methods and
// that method's ErrNotMember assertion fails — and for UpdateProfile and
// SetAvatarURL the "untouched" assertions fail too, because the write lands
// on a foreign user.
func TestAdapterNeg_PeopleLifecycle_RefusesTargetsOutsideTheActingOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	acting := testutil.CreateTestOrg(t, db.Pool)
	testutil.CreateTestUser(t, db.Pool, acting.ID)
	actingTeam := testutil.DefaultTeamID(t, db.Pool, acting.ID)

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	outsider := testutil.CreateTestUser(t, db.Pool, otherOrg.ID)
	activeBefore, genBefore, nameBefore := adnUserState(t, db.Pool, outsider.ID)

	a := adapters.NewPeopleAdapter(db.Pool)
	ops := map[string]func() error{
		"deactivate":         func() error { return a.Deactivate(ctx, acting.ID, outsider.ID) },
		"reactivate":         func() error { return a.Reactivate(ctx, acting.ID, outsider.ID) },
		"force_logout":       func() error { return a.ForceLogout(ctx, acting.ID, outsider.ID) },
		"remove_from_org":    func() error { return a.RemoveFromOrg(ctx, acting.ID, outsider.ID) },
		"change_org_role":    func() error { return a.ChangeOrgRole(ctx, acting.ID, outsider.ID, "admin") },
		"change_primary":     func() error { return a.ChangePrimaryTeam(ctx, acting.ID, outsider.ID, actingTeam) },
		"update_profile":     func() error { return a.UpdateProfile(ctx, acting.ID, outsider.ID, "Renamed By Outsider") },
		"set_avatar_url":     func() error { return a.SetAvatarURL(ctx, acting.ID, outsider.ID, "/avatars/hijacked.png") },
		"deactivate_unknown": func() error { return a.Deactivate(ctx, acting.ID, uuid.New()) },
	}
	for name, op := range ops {
		require.ErrorIs(t, op(), people.ErrNotMember, "%s must refuse a target outside the acting org", name)
	}

	activeAfter, genAfter, nameAfter := adnUserState(t, db.Pool, outsider.ID)
	require.Equal(t, activeBefore, activeAfter, "no refused operation may have flipped is_active")
	require.Equal(t, genBefore, genAfter, "no refused operation may have bumped token_generation")
	require.Equal(t, nameBefore, nameAfter, "no refused operation may have rewritten the display name")
	require.Equal(t, 1,
		adnScalarCount(t, db.Pool, `SELECT count(*) FROM memberships WHERE user_id = $1`, outsider.ID),
		"the outsider's own membership must survive")
	require.Equal(t, 0,
		adnScalarCount(t, db.Pool,
			`SELECT count(*) FROM users WHERE id = $1 AND avatar_url IS NOT NULL`, outsider.ID),
		"no avatar may have been written onto a foreign user")
}

// TestAdapterNeg_PeopleDeactivate_LastAdminIsRefusedAndNothingIsWritten: the
// last-admin invariant lives in the store under row locks. Deactivation is a
// two-write operation (the is_active/generation UPDATE and the session
// revocation), so a refusal that fired after the first write would leave the
// org's only admin locked out of their own sessions.
//
// Fails-before: delete the ListOrgsWhereUserIsLastAdmin check and the first
// Deactivate succeeds. Move the check after DeactivateUserAccount and the
// token_generation assertion fails.
func TestAdapterNeg_PeopleDeactivate_LastAdminIsRefusedAndNothingIsWritten(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, org.ID) // 'owner' — admin class
	a := adapters.NewPeopleAdapter(db.Pool)

	_, genBefore, _ := adnUserState(t, db.Pool, owner.ID)
	require.ErrorIs(t, a.Deactivate(ctx, org.ID, owner.ID), people.ErrLastAdmin)

	active, genAfter, _ := adnUserState(t, db.Pool, owner.ID)
	require.True(t, active, "the refused deactivation must leave the account active")
	require.Equal(t, genBefore, genAfter,
		"the refused deactivation must not have bumped token_generation — that bump kills every session")

	// Control: with a second admin present the guard no longer applies, which
	// proves the refusal above was the last-admin rule and not a blanket no.
	testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "admin")
	require.NoError(t, a.Deactivate(ctx, org.ID, owner.ID))
	active, genFinal, _ := adnUserState(t, db.Pool, owner.ID)
	require.False(t, active)
	require.Greater(t, genFinal, genBefore, "a real deactivation bumps the generation")
}

// TestAdapterNeg_PeopleActivation_SecondCallReportsTheCurrentState: the
// n == 0 arms of Deactivate and Reactivate. Both guarded UPDATEs match on the
// state they are changing away from, so a repeat is zero rows — and reporting
// success for it would tell an admin they had just done something they had
// not.
//
// Fails-before: drop either `if n == 0` and the repeat call returns nil.
func TestAdapterNeg_PeopleActivation_SecondCallReportsTheCurrentState(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	testutil.CreateTestUser(t, db.Pool, org.ID) // keeps an admin in the org
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	a := adapters.NewPeopleAdapter(db.Pool)

	require.ErrorIs(t, a.Reactivate(ctx, org.ID, member.ID), people.ErrAlreadyActive,
		"reactivating an active account must report the current state")

	require.NoError(t, a.Deactivate(ctx, org.ID, member.ID))
	require.ErrorIs(t, a.Deactivate(ctx, org.ID, member.ID), people.ErrNotActive,
		"deactivating an already-deactivated account must report the current state")

	require.NoError(t, a.Reactivate(ctx, org.ID, member.ID))
	active, _, _ := adnUserState(t, db.Pool, member.ID)
	require.True(t, active)
}

// TestAdapterNeg_PeopleForceLogout_SoftDeletedUserIsNotAMember covers the
// n == 0 arm of ForceLogout: BumpTokenGeneration filters `deleted_at IS NULL`
// while the membership row survives a user soft-delete, so a deleted account
// still looks like a member to requireMembership. The adapter has to notice
// that nothing was bumped rather than go on to report success.
//
// Fails-before: drop the `if n == 0` arm and the call returns nil for an
// account that was never actually logged out of anything.
func TestAdapterNeg_PeopleForceLogout_SoftDeletedUserIsNotAMember(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	testutil.CreateTestUser(t, db.Pool, org.ID)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	a := adapters.NewPeopleAdapter(db.Pool)

	// Control: a live member is logged out, and their sessions are revoked in
	// the same transaction.
	sessionID := uuid.New()
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at)
		 VALUES ($1,$2,$3, now() + interval '1 day')`,
		sessionID, member.ID, adnTokenHash("session-"+sessionID.String()))
	require.NoError(t, err)

	_, genBefore, _ := adnUserState(t, db.Pool, member.ID)
	require.NoError(t, a.ForceLogout(ctx, org.ID, member.ID))
	_, genAfter, _ := adnUserState(t, db.Pool, member.ID)
	require.Greater(t, genAfter, genBefore, "force logout bumps the token generation")
	require.Equal(t, 1,
		adnScalarCount(t, db.Pool,
			`SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NOT NULL`, sessionID),
		"force logout revokes the DB session in the same transaction")

	_, err = db.Pool.Exec(ctx, `UPDATE users SET deleted_at = now() WHERE id = $1`, member.ID)
	require.NoError(t, err)
	require.ErrorIs(t, a.ForceLogout(ctx, org.ID, member.ID), people.ErrNotMember,
		"a soft-deleted account is not a member, even though its membership row still exists")
}

// TestAdapterNeg_PeopleChangeOrgRole_OwnerAndLastAdminRefusals: the two
// refusals that protect an org from becoming unadministrable, plus the
// control that proves the last-admin refusal is conditional.
//
// Fails-before: delete the owner check and the first assertion fails; delete
// the lockAndRequireOtherAdmins call and the second fails; make it
// unconditional and the third fails.
func TestAdapterNeg_PeopleChangeOrgRole_OwnerAndLastAdminRefusals(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	a := adapters.NewPeopleAdapter(db.Pool)

	ownerOrg := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, ownerOrg.ID)
	require.ErrorIs(t, a.ChangeOrgRole(ctx, ownerOrg.ID, owner.ID, "member"), people.ErrCannotChangeOwner)
	require.Equal(t, "owner", adnMembershipRole(t, db.Pool, ownerOrg.ID, owner.ID),
		"the refused role change must not have landed")

	// An org whose only admin-class member holds 'admin' (not 'owner'), so the
	// demotion reaches the last-admin guard rather than the owner guard.
	adminOrg := testutil.CreateTestOrg(t, db.Pool)
	lone := testutil.CreateTestUserWithRole(t, db.Pool, adminOrg.ID, "admin")
	require.ErrorIs(t, a.ChangeOrgRole(ctx, adminOrg.ID, lone.ID, "member"), people.ErrLastAdmin)
	require.Equal(t, "admin", adnMembershipRole(t, db.Pool, adminOrg.ID, lone.ID))

	testutil.CreateTestUserWithRole(t, db.Pool, adminOrg.ID, "admin")
	require.NoError(t, a.ChangeOrgRole(ctx, adminOrg.ID, lone.ID, "member"),
		"once another admin exists the demotion is allowed — the guard is conditional, not blanket")
	require.Equal(t, "member", adnMembershipRole(t, db.Pool, adminOrg.ID, lone.ID))
}

// TestAdapterNeg_PeopleChangePrimaryTeam_RefusesTeamsOutsideTheOrg: teamID is
// a body field. Without the org check an admin could make a team of another
// org someone's primary team, which then feeds the grant expansion.
//
// Fails-before: delete the `team.OrgID != orgID` arm and the foreign-team
// call succeeds; drop GetTeamByID's deleted_at predicate (and the DeletedAt
// guard beside it) and the soft-deleted call succeeds.
func TestAdapterNeg_PeopleChangePrimaryTeam_RefusesTeamsOutsideTheOrg(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	testutil.CreateTestUser(t, db.Pool, org.ID)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	defaultTeam := testutil.DefaultTeamID(t, db.Pool, org.ID)
	a := adapters.NewPeopleAdapter(db.Pool)

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	foreignTeam := testutil.DefaultTeamID(t, db.Pool, otherOrg.ID)
	dead := adnMakeTeam(t, db.Pool, org.ID, "dead")
	adnSoftDeleteTeam(t, db.Pool, dead)

	for name, teamID := range map[string]uuid.UUID{
		"foreign_org_team": foreignTeam,
		"unknown_team":     uuid.New(),
		"soft_deleted":     dead,
	} {
		require.ErrorIs(t, a.ChangePrimaryTeam(ctx, org.ID, member.ID, teamID),
			people.ErrTeamNotFound, "%s must not be settable as a primary team", name)
	}
	require.Equal(t, defaultTeam, adnPrimaryTeamID(t, db.Pool, org.ID, member.ID),
		"no refused call may have moved the member's primary team")

	live := adnMakeTeam(t, db.Pool, org.ID, "live")
	require.NoError(t, a.ChangePrimaryTeam(ctx, org.ID, member.ID, live),
		"a live team of the org must be settable, or the refusals above prove nothing")
	require.Equal(t, live, adnPrimaryTeamID(t, db.Pool, org.ID, member.ID))
	require.Equal(t, 1,
		adnScalarCount(t, db.Pool,
			`SELECT count(*) FROM team_members WHERE org_id = $1 AND user_id = $2 AND is_primary`,
			org.ID, member.ID),
		"exactly one primary membership per user per org")
}

// TestAdapterNeg_PeopleRemoveFromOrg_LastAdminRefusedThenClearsTeamsAndGrants:
// removal is three deletes that must be one transaction. A membership dropped
// without its user-subject grants would leave grant rows that silently
// re-grant the space if the person is ever re-invited.
//
// Fails-before: delete the isAdminClass/lockAndRequireOtherAdmins branch and
// the owner removal succeeds; drop either of the two follow-up deletes and
// the team-membership or grant assertion fails.
func TestAdapterNeg_PeopleRemoveFromOrg_LastAdminRefusedThenClearsTeamsAndGrants(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	owner := testutil.CreateTestUser(t, db.Pool, org.ID)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, owner.ID, "beacon")
	a := adapters.NewPeopleAdapter(db.Pool)

	_, err := db.Pool.Exec(ctx,
		`INSERT INTO space_grants (org_id, space_id, subject_type, subject_id, role, created_by)
		 VALUES ($1,$2,'user',$3,'contributor',$4)`, org.ID, space.ID, member.ID, owner.ID)
	require.NoError(t, err)

	require.ErrorIs(t, a.RemoveFromOrg(ctx, org.ID, owner.ID), people.ErrLastAdmin)
	require.Equal(t, 1,
		adnScalarCount(t, db.Pool,
			`SELECT count(*) FROM memberships WHERE org_id = $1 AND user_id = $2`, org.ID, owner.ID),
		"the refused removal must leave the admin's membership intact")

	require.NoError(t, a.RemoveFromOrg(ctx, org.ID, member.ID))
	require.Equal(t, 0, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM memberships WHERE org_id = $1 AND user_id = $2`, org.ID, member.ID))
	require.Equal(t, 0, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM team_members WHERE org_id = $1 AND user_id = $2`, org.ID, member.ID),
		"removal drops the org's team memberships")
	require.Equal(t, 0, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM space_grants WHERE org_id = $1 AND subject_type = 'user' AND subject_id = $2`,
		org.ID, member.ID),
		"removal drops the org's user-subject grants — a stale grant would re-grant on re-invite")
	require.Equal(t, 1, adnScalarCount(t, db.Pool, `SELECT count(*) FROM users WHERE id = $1`, member.ID),
		"the user record survives with their authored content's attribution intact")
}

// TestAdapterNeg_PeopleSearch_WildcardsInTheQueryAreLiteral pins the same
// escaping property the ticket typeahead and the saved-view text term pin,
// on the third ILIKE site: the caller's text is a literal substring.
//
// Fails-before: drop the replace() escaping from SearchOrgMembers and "100%"
// becomes "%100%%", which matches the decoy too. The list/search split at the
// end catches a Search that forgets `u.is_active`.
func TestAdapterNeg_PeopleSearch_WildcardsInTheQueryAreLiteral(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	admin := testutil.CreateTestUser(t, db.Pool, org.ID)
	adnSetDisplayName(t, db.Pool, admin.ID, "Admin Person")

	literal := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	decoy := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	adnSetDisplayName(t, db.Pool, literal.ID, "Rollout 100% Owner")
	adnSetDisplayName(t, db.Pool, decoy.ID, "Rollout 100 percent Owner")

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	outsider := testutil.CreateTestUser(t, db.Pool, otherOrg.ID)
	adnSetDisplayName(t, db.Pool, outsider.ID, "Rollout 100% Outsider")

	a := adapters.NewPeopleAdapter(db.Pool)
	found, err := a.Search(ctx, org.ID, "100%")
	require.NoError(t, err)
	ids := map[uuid.UUID]bool{}
	for _, p := range found {
		ids[p.ID] = true
	}
	require.True(t, ids[literal.ID], "the literal percent sign must match itself")
	require.False(t, ids[decoy.ID], "a percent sign in the caller's term must not act as a wildcard")
	require.False(t, ids[outsider.ID], "search must not cross the org boundary")

	// Deactivated members leave the picker but stay on the admin list.
	require.NoError(t, a.Deactivate(ctx, org.ID, literal.ID))
	found, err = a.Search(ctx, org.ID, "100%")
	require.NoError(t, err)
	require.Empty(t, found, "the picker offers active members only")

	listed, err := a.List(ctx, org.ID)
	require.NoError(t, err)
	var sawDeactivated bool
	for _, p := range listed {
		require.NotEqual(t, outsider.ID, p.UserID, "the admin list must not cross the org boundary")
		if p.UserID == literal.ID {
			sawDeactivated = true
			require.False(t, p.IsActive, "the admin list reports the deactivated state rather than hiding the row")
		}
	}
	require.True(t, sawDeactivated, "a deactivated member must still appear on the admin list")
}

// TestAdapterNeg_PeopleProfileWrites_TouchOnlyTheirOwnColumns is the success
// control for the two profile writes the tenancy sweep above only ever sees
// refuse — without it, an adapter that refused every caller would pass that
// test. It also pins the invariant those two methods' doc comments claim:
// they touch display_name and avatar_url and nothing else, so neither can
// become a back door around "deactivation always terminates sessions".
//
// Fails-before: add is_active or token_generation to either UPDATE and the
// last two assertions fail; leave the write out entirely and the first two do.
func TestAdapterNeg_PeopleProfileWrites_TouchOnlyTheirOwnColumns(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	testutil.CreateTestUser(t, db.Pool, org.ID)
	member := testutil.CreateTestUserWithRole(t, db.Pool, org.ID, "member")
	a := adapters.NewPeopleAdapter(db.Pool)

	_, genBefore, _ := adnUserState(t, db.Pool, member.ID)

	require.NoError(t, a.UpdateProfile(ctx, org.ID, member.ID, "Properly Renamed"))
	require.NoError(t, a.SetAvatarURL(ctx, org.ID, member.ID, "/api/v1/avatars/member.png"))

	active, genAfter, name := adnUserState(t, db.Pool, member.ID)
	require.Equal(t, "Properly Renamed", name, "an admin can rename a member of their own org")
	require.Equal(t, 1, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM users WHERE id = $1 AND avatar_url = $2`,
		member.ID, "/api/v1/avatars/member.png"),
		"and can record their avatar reference")
	require.True(t, active, "a profile edit must never flip is_active")
	require.Equal(t, genBefore, genAfter,
		"a profile edit must never bump token_generation — it is not a session-termination path")
}

// ---------------------------------------------------------------------------
// content_tx: MovePageTx and the delete-and-revoke family
// ---------------------------------------------------------------------------

// adnMoveEnv is one org, two codex spaces and a wired ContentTxAdapter.
type adnMoveEnv struct {
	db     *testutil.TestDB
	ctx    context.Context
	q      *generated.Queries
	a      *adapters.ContentTxAdapter
	org    testutil.Org
	user   testutil.User
	spaceA testutil.Space
	spaceB testutil.Space
}

func adnNewMoveEnv(t *testing.T) adnMoveEnv {
	t.Helper()
	db := testutil.NewTestDB(t)
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	return adnMoveEnv{
		db: db, ctx: context.Background(),
		q: generated.New(db.Pool), a: adapters.NewContentTxAdapter(db.Pool),
		org: org, user: user,
		spaceA: testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "codex"),
		spaceB: testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "codex"),
	}
}

// TestAdapterNeg_MovePage_MissingAndForeignOrgPagesAreBothNotFound: OrgID
// comes from the URL and PageID from the path. A page in another org must
// read as absent — the same answer a nonexistent id gets, so the refusal
// leaks no existence information.
//
// Fails-before: delete the `sourceSpace.OrgID != in.OrgID` arm of
// validateMoveSpaces and the cross-org move succeeds, relocating another
// tenant's page.
func TestAdapterNeg_MovePage_MissingAndForeignOrgPagesAreBothNotFound(t *testing.T) {
	env := adnNewMoveEnv(t)

	_, err := env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceB.ID, PageID: uuid.New(), ActorID: env.user.ID,
	})
	require.ErrorIs(t, err, wiki.ErrPageNotFound, "an unknown page id maps to the domain sentinel")

	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	otherUser := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)
	otherSpace := testutil.CreateTestSpace(t, env.db.Pool, otherOrg.ID, otherUser.ID, "codex")
	foreign := adnCreatePage(t, env.q, otherSpace.ID, otherUser.ID, nil, "Foreign page")

	_, err = env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceB.ID, PageID: foreign.ID, ActorID: env.user.ID,
	})
	require.ErrorIs(t, err, wiki.ErrPageNotFound,
		"a page belonging to another org must be indistinguishable from one that does not exist")

	after, err := env.q.GetPageByID(env.ctx, foreign.ID)
	require.NoError(t, err)
	require.Equal(t, otherSpace.ID, after.SpaceID, "the foreign page must not have moved")
}

// TestAdapterNeg_MovePage_TargetSpaceRefusals: the target space is a body
// field. Three ways for it to be invalid, one answer — and none of them may
// half-move the page.
//
// Fails-before: delete the `targetSpace.OrgID != in.OrgID` arm and the
// cross-org target succeeds, which is a page smuggled into another tenant.
func TestAdapterNeg_MovePage_TargetSpaceRefusals(t *testing.T) {
	env := adnNewMoveEnv(t)
	page := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, nil, "Stays put")

	otherOrg := testutil.CreateTestOrg(t, env.db.Pool)
	otherUser := testutil.CreateTestUser(t, env.db.Pool, otherOrg.ID)
	foreignSpace := testutil.CreateTestSpace(t, env.db.Pool, otherOrg.ID, otherUser.ID, "codex")

	deadSpace := testutil.CreateTestSpace(t, env.db.Pool, env.org.ID, env.user.ID, "codex")
	_, err := env.db.Pool.Exec(env.ctx, `UPDATE spaces SET deleted_at = now() WHERE id = $1`, deadSpace.ID)
	require.NoError(t, err)

	for name, target := range map[string]uuid.UUID{
		"unknown_space":     uuid.New(),
		"foreign_org_space": foreignSpace.ID,
		"soft_deleted":      deadSpace.ID,
	} {
		_, err := env.a.MovePageTx(env.ctx, wiki.MovePageInput{
			OrgID: env.org.ID, TargetSpaceID: target, PageID: page.ID, ActorID: env.user.ID,
		})
		require.ErrorIs(t, err, wiki.ErrTargetSpaceNotFound, "%s must not be a valid move target", name)
	}

	after, err := env.q.GetPageByID(env.ctx, page.ID)
	require.NoError(t, err)
	require.Equal(t, env.spaceA.ID, after.SpaceID, "no refused move may have relocated the page")

	res, err := env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceB.ID, PageID: page.ID, ActorID: env.user.ID,
	})
	require.NoError(t, err, "a live space of the same org must be a valid target")
	require.True(t, res.CrossSpace)
}

// TestAdapterNeg_MovePage_ParentRefusals covers resolveMoveParent's three
// rejections. The cycle case is the load-bearing one: grafting a page onto
// its own descendant detaches the whole subtree from the tree, and the
// materialised paths would then describe a loop no reader can terminate on.
//
// Fails-before: delete the PathWithinSubtree check and the two cycle
// assertions fail (and the resulting rows are unreachable); delete the
// `parent.SpaceID != in.TargetSpaceID` check and the wrong-space assertion
// fails.
func TestAdapterNeg_MovePage_ParentRefusals(t *testing.T) {
	env := adnNewMoveEnv(t)
	root := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, nil, "Root")
	child := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, &root, "Child")
	elsewhere := adnCreatePage(t, env.q, env.spaceB.ID, env.user.ID, nil, "In space B")

	unknown := uuid.New()
	_, err := env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceA.ID, PageID: root.ID,
		ParentID: &unknown, ActorID: env.user.ID,
	})
	require.ErrorIs(t, err, wiki.ErrParentPageNotFound)

	_, err = env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceA.ID, PageID: root.ID,
		ParentID: &elsewhere.ID, ActorID: env.user.ID,
	})
	require.ErrorIs(t, err, wiki.ErrParentNotInTargetSpace,
		"a parent living outside the target space must be refused")

	_, err = env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceA.ID, PageID: root.ID,
		ParentID: &child.ID, ActorID: env.user.ID,
	})
	require.ErrorIs(t, err, wiki.ErrPageMoveCycle, "a page cannot be grafted onto its own descendant")

	_, err = env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceA.ID, PageID: root.ID,
		ParentID: &root.ID, ActorID: env.user.ID,
	})
	require.ErrorIs(t, err, wiki.ErrPageMoveCycle, "a page cannot be its own parent")

	after, err := env.q.GetPageByID(env.ctx, root.ID)
	require.NoError(t, err)
	require.False(t, after.ParentID.Valid, "no refused move may have reparented the page")
	require.Equal(t, root.Path, after.Path, "no refused move may have rewritten the path")
}

// TestAdapterNeg_MovePage_CrossSpaceRevokesTheWholeSubtreeAndAudits is
// ADR-0008 rule 9 end to end: a page shared org-wide, dragged into another
// space, must arrive with its shares dead — and so must every DESCENDANT'S
// own share, which is the half that is easy to miss.
//
// It is also the ordering test. The revocation matches on the OLD space and
// OLD paths, so running it after applyPageMove would match nothing and
// silently leave the shares alive: the RevokedShares assertion would read 0.
//
// Fails-before: move the revokeSubtreeSharesTx call below applyPageMove, or
// narrow it to the root page only, and this fails.
func TestAdapterNeg_MovePage_CrossSpaceRevokesTheWholeSubtreeAndAudits(t *testing.T) {
	env := adnNewMoveEnv(t)
	team := testutil.DefaultTeamID(t, env.db.Pool, env.org.ID)

	root := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, nil, "Shared root")
	child := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, &root, "Shared child")
	bystander := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, nil, "Unrelated page")
	destParent := adnCreatePage(t, env.q, env.spaceB.ID, env.user.ID, nil, "Destination parent")

	rootShare := adnCreateShare(t, env.q, env.org.ID, env.spaceA.ID, "page", root.ID, nil, true, env.user.ID)
	childShare := adnCreateShare(t, env.q, env.org.ID, env.spaceA.ID, "page", child.ID, &team, false, env.user.ID)
	bystanderShare := adnCreateShare(t, env.q, env.org.ID, env.spaceA.ID, "page", bystander.ID, nil, false, env.user.ID)

	res, err := env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceB.ID, PageID: root.ID,
		ParentID: &destParent.ID, ActorID: env.user.ID,
	})
	require.NoError(t, err)
	require.True(t, res.CrossSpace)
	require.EqualValues(t, 2, res.RevokedShares,
		"the root's share and the descendant's own share must both be revoked")

	require.False(t, adnShareIsActive(t, env.db.Pool, rootShare), "the moved page's share must be revoked")
	require.False(t, adnShareIsActive(t, env.db.Pool, childShare), "a descendant's own share must be revoked too")
	require.True(t, adnShareIsActive(t, env.db.Pool, bystanderShare),
		"a share on a page outside the moved subtree must survive")

	movedRoot, err := env.q.GetPageByID(env.ctx, root.ID)
	require.NoError(t, err)
	require.Equal(t, env.spaceB.ID, movedRoot.SpaceID)
	require.Equal(t, destParent.Path+"."+root.ID.String(), movedRoot.Path)

	movedChild, err := env.q.GetPageByID(env.ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, env.spaceB.ID, movedChild.SpaceID, "the subtree follows its root across the space boundary")
	require.Equal(t, movedRoot.Path+"."+child.ID.String(), movedChild.Path,
		"the descendant's path is rewritten by exact-prefix surgery, not by a blind replace")

	events := adnShareRevokedPayloads(t, env.db.Pool, env.org.ID)
	require.Len(t, events, 2, "each revocation writes exactly one audit row, through the move's own transaction")
	require.Equal(t, "entity_moved", events[rootShare]["reason"])
	require.Equal(t, "entity_moved", events[childShare]["reason"])
	require.Equal(t, root.ID.String(), events[rootShare]["entity_id"])
	require.Equal(t, team.String(), events[childShare]["audience_id"],
		"a team-audience share records which team lost access")
	require.NotContains(t, events[rootShare], "audience_id",
		"an org-audience share has no audience id to record")
}

// TestAdapterNeg_MovePage_InSpaceMoveKeepsSharesAlive is the other half of the
// rule, and the one that stops the revocation from being a blanket "any move
// kills shares". Reordering a page inside its own space is not an escalation
// and must not silently unshare it.
//
// Fails-before: make the revocation unconditional (drop the `if
// res.CrossSpace` guard) and both the RevokedShares and the still-active
// assertions fail.
func TestAdapterNeg_MovePage_InSpaceMoveKeepsSharesAlive(t *testing.T) {
	env := adnNewMoveEnv(t)
	root := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, nil, "Reordered")
	sibling := adnCreatePage(t, env.q, env.spaceA.ID, env.user.ID, nil, "New parent")
	share := adnCreateShare(t, env.q, env.org.ID, env.spaceA.ID, "page", root.ID, nil, false, env.user.ID)

	res, err := env.a.MovePageTx(env.ctx, wiki.MovePageInput{
		OrgID: env.org.ID, TargetSpaceID: env.spaceA.ID, PageID: root.ID,
		ParentID: &sibling.ID, Position: 3, ActorID: env.user.ID,
	})
	require.NoError(t, err)
	require.False(t, res.CrossSpace)
	require.EqualValues(t, 0, res.RevokedShares)
	require.True(t, adnShareIsActive(t, env.db.Pool, share),
		"an in-space reparent must not revoke the page's shares")
	require.Empty(t, adnShareRevokedPayloads(t, env.db.Pool, env.org.ID),
		"and it must not write a share.revoked audit row")

	after, err := env.q.GetPageByID(env.ctx, root.ID)
	require.NoError(t, err)
	require.Equal(t, sibling.Path+"."+root.ID.String(), after.Path)
	require.EqualValues(t, 3, after.Position)
}

// TestAdapterNeg_DeleteTicketAndItem_RevokeSharesAndAudit covers the two
// delete-and-revoke wrappers that no test reached, and with them the
// audience_id branch of writeShareRevokedTx. ADR-0008 rule 10: a deleted
// entity's shares die with it, in the same transaction — a surviving share
// row on a resurrected id would re-grant access.
//
// Fails-before: drop the RevokeSharesByEntity call from
// deleteEntityAndRevokeShares and the revocation assertions fail; make it
// match on entity_id alone (ignoring entity_type) and the cross-type
// bystander assertion fails.
func TestAdapterNeg_DeleteTicketAndItem_RevokeSharesAndAudit(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	team := testutil.DefaultTeamID(t, db.Pool, org.ID)
	beacon := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "beacon")
	vector := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	q := generated.New(db.Pool)
	a := adapters.NewContentTxAdapter(db.Pool)

	ticket := insertTicket(t, db.Pool, beacon.ID, user.ID, 1, "doomed ticket", "open", "high", nil)
	keptTicket := insertTicket(t, db.Pool, beacon.ID, user.ID, 2, "kept ticket", "open", "high", nil)
	item := insertItem(t, db.Pool, org.ID, vector.ID, user.ID, 1, "doomed item", "open", "high", "task", nil)

	ticketShare := adnCreateShare(t, q, org.ID, beacon.ID, "ticket", ticket, nil, false, user.ID)
	keptShare := adnCreateShare(t, q, org.ID, beacon.ID, "ticket", keptTicket, nil, false, user.ID)
	itemShare := adnCreateShare(t, q, org.ID, vector.ID, "project_item", item, &team, false, user.ID)

	require.NoError(t, a.DeleteTicketAndRevokeShares(ctx, ticket, user.ID))
	require.Equal(t, 1, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM tickets WHERE id = $1 AND deleted_at IS NOT NULL`, ticket),
		"the ticket is soft-deleted")
	require.False(t, adnShareIsActive(t, db.Pool, ticketShare))
	require.True(t, adnShareIsActive(t, db.Pool, keptShare), "another entity's share must survive")

	require.NoError(t, a.DeleteItemAndRevokeShares(ctx, item, user.ID))
	require.Equal(t, 1, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM project_items WHERE id = $1 AND deleted_at IS NOT NULL`, item),
		"the project item is soft-deleted")
	require.False(t, adnShareIsActive(t, db.Pool, itemShare))

	events := adnShareRevokedPayloads(t, db.Pool, org.ID)
	require.Len(t, events, 2)
	require.Equal(t, "entity_deleted", events[ticketShare]["reason"])
	require.Equal(t, "ticket", events[ticketShare]["entity_type"])
	require.Equal(t, "entity_deleted", events[itemShare]["reason"])
	require.Equal(t, "project_item", events[itemShare]["entity_type"])
	require.Equal(t, team.String(), events[itemShare]["audience_id"])
}

// ---------------------------------------------------------------------------
// teams
// ---------------------------------------------------------------------------

// TestAdapterNeg_TeamReads_IgnoreDeletedAndForeignRows: Get, Update and
// ListByOrg all have to treat a soft-deleted team as gone. A team that is
// still readable after deletion is still nameable — as a grant subject, as an
// invite's initial team, as a saved view's audience.
//
// Fails-before: drop `AND deleted_at IS NULL` from GetTeamByID / UpdateTeam /
// ListTeamsByOrg and the deleted-row assertions fail; drop `org_id = $1` from
// ListTeamsByOrg and the cross-org assertion fails.
func TestAdapterNeg_TeamReads_IgnoreDeletedAndForeignRows(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	a := adapters.NewTeamAdapter(db.Pool)

	_, err := a.Get(ctx, uuid.New())
	require.ErrorIs(t, err, teams.ErrNotFound, "an unknown id maps to the domain sentinel")
	_, err = a.Update(ctx, uuid.New(), "New name", "")
	require.ErrorIs(t, err, teams.ErrNotFound, "renaming a team that does not exist maps to the sentinel")

	live := adnMakeTeam(t, db.Pool, org.ID, "live")
	doomed := adnMakeTeam(t, db.Pool, org.ID, "doomed")
	renamed, err := a.Update(ctx, doomed, "Renamed", "desc")
	require.NoError(t, err, "control: a live team renames")
	require.Equal(t, "Renamed", renamed.Name)

	adnSoftDeleteTeam(t, db.Pool, doomed)
	_, err = a.Get(ctx, doomed)
	require.ErrorIs(t, err, teams.ErrNotFound, "a soft-deleted team must read as absent")
	_, err = a.Update(ctx, doomed, "Renamed again", "")
	require.ErrorIs(t, err, teams.ErrNotFound, "a soft-deleted team must not be renameable")

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	foreign := adnMakeTeam(t, db.Pool, otherOrg.ID, "foreign")

	listed, err := a.ListByOrg(ctx, org.ID)
	require.NoError(t, err)
	got := map[uuid.UUID]bool{}
	for _, tm := range listed {
		got[tm.ID] = true
	}
	require.True(t, got[live], "a live team of the org is listed")
	require.True(t, got[testutil.DefaultTeamID(t, db.Pool, org.ID)], "so is the default team")
	require.False(t, got[doomed], "a soft-deleted team must not be listed")
	require.False(t, got[foreign], "another org's team must not be listed")
}

// TestAdapterNeg_TeamGetDefault_OrgWithoutOneIsNotFound drives the branch the
// seed's fast path normally hides: an org that has no default team yet. The
// domain answer is ErrNotFound, and seeding must then make it resolvable —
// SeedDefaultTeam's insert arm only runs when the read misses.
//
// Fails-before: return a zero team instead of teams.ErrNotFound and the first
// assertion fails; make SeedDefaultTeam a no-op after the failed read and the
// second fails.
func TestAdapterNeg_TeamGetDefault_OrgWithoutOneIsNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	a := adapters.NewTeamAdapter(db.Pool)

	// Inserted directly, deliberately without the default team that
	// CreateTestOrg seeds — this is the state a half-provisioned org is in.
	bare := uuid.New()
	_, err := db.Pool.Exec(ctx, `INSERT INTO organizations (id, slug, name) VALUES ($1,$2,$3)`,
		bare, "bare-"+uuid.NewString()[:8], "Bare Org")
	require.NoError(t, err)

	_, err = a.GetDefault(ctx, bare)
	require.ErrorIs(t, err, teams.ErrNotFound, "an org with no default team must report not-found")

	require.NoError(t, a.SeedDefaultTeam(ctx, bare))
	seeded, err := a.GetDefault(ctx, bare)
	require.NoError(t, err, "seeding must make the default team resolvable")
	require.True(t, seeded.IsDefault)
	require.Equal(t, []uuid.UUID{seeded.ID}, seeded.Path,
		"the seeded root team's path is its own id — teams_path_ends_self")
}

// TestAdapterNeg_TeamMembership_UnknownSubjectsMapToMemberNotFound: the
// membership reads and SetPrimary all take a user id from the URL. A missing
// membership must be ErrMemberNotFound, and SetPrimary must refuse BEFORE it
// clears the user's existing primary flag — otherwise a bad request leaves
// the user with no primary team at all.
//
// Fails-before: delete the GetTeamMember guard in SetPrimary and
// ClearPrimaryTeam runs anyway, so the "primary unchanged" assertion fails.
func TestAdapterNeg_TeamMembership_UnknownSubjectsMapToMemberNotFound(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	defaultTeam := testutil.DefaultTeamID(t, db.Pool, org.ID)
	a := adapters.NewTeamAdapter(db.Pool)

	_, err := a.GetMember(ctx, defaultTeam, uuid.New())
	require.ErrorIs(t, err, teams.ErrMemberNotFound)

	empty := adnMakeTeam(t, db.Pool, org.ID, "empty")
	require.ErrorIs(t, a.SetPrimary(ctx, empty, user.ID, org.ID), teams.ErrMemberNotFound,
		"a user who is not in the team cannot make it their primary")
	require.Equal(t, defaultTeam, adnPrimaryTeamID(t, db.Pool, org.ID, user.ID),
		"the refusal must fire before the existing primary flag is cleared")

	// AddMember is org-scoped through the team, not just through the id.
	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	foreignTeam := testutil.DefaultTeamID(t, db.Pool, otherOrg.ID)
	_, err = a.AddMember(ctx, foreignTeam, user.ID, org.ID, teams.MemberRoleMember)
	require.ErrorIs(t, err, teams.ErrNotFound,
		"a team of another org must not be enrollable from this org's admin surface")
	require.Equal(t, 0, adnScalarCount(t, db.Pool,
		`SELECT count(*) FROM team_members WHERE team_id = $1 AND user_id = $2`, foreignTeam, user.ID))

	// Control: the same call against a live team of the caller's own org
	// works, and ListMembers joins live user identity onto the row.
	_, err = a.AddMember(ctx, empty, user.ID, org.ID, teams.MemberRoleLead)
	require.NoError(t, err)
	members, err := a.ListMembers(ctx, empty)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, user.ID, members[0].UserID)
	require.Equal(t, user.Email, members[0].Email, "ListMembers joins user identity onto the membership")
	require.True(t, members[0].IsLead())
	require.False(t, members[0].IsPrimary, "AddMember never touches is_primary")

	require.NoError(t, a.SetPrimary(ctx, empty, user.ID, org.ID))
	require.Equal(t, empty, adnPrimaryTeamID(t, db.Pool, org.ID, user.ID))
}

// TestAdapterNeg_TeamReparent_UnknownForeignAndDefaultAreRefused: the subtree
// lock is org-scoped, so a team of another org must read as absent rather
// than be movable; and ADR-0006 makes the org default team immovable, because
// every "never teamless" fallback resolves through it.
//
// Fails-before: drop `org_id = $1` from ListSubtreeForUpdate and the
// cross-org reparent succeeds; delete the IsDefault guard and the default
// team is reparented.
func TestAdapterNeg_TeamReparent_UnknownForeignAndDefaultAreRefused(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	a := adapters.NewTeamAdapter(db.Pool)

	host := adnMakeTeam(t, db.Pool, org.ID, "host")

	_, err := a.Reparent(ctx, org.ID, uuid.New(), nil)
	require.ErrorIs(t, err, teams.ErrNotFound, "an unknown team maps to the domain sentinel")

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	foreign := adnMakeTeam(t, db.Pool, otherOrg.ID, "foreign")
	_, err = a.Reparent(ctx, org.ID, foreign, &host)
	require.ErrorIs(t, err, teams.ErrNotFound, "another org's team must not be movable from here")

	defaultTeam := testutil.DefaultTeamID(t, db.Pool, org.ID)
	_, err = a.Reparent(ctx, org.ID, defaultTeam, &host)
	require.ErrorIs(t, err, teams.ErrDefaultTeam, "the org default team is immovable")

	// A parent from another org is rejected too — as a parent, not silently
	// rooted.
	_, err = a.Reparent(ctx, org.ID, host, &foreign)
	require.ErrorIs(t, err, teams.ErrParentNotFound)

	current, err := a.Get(ctx, host)
	require.NoError(t, err)
	require.Nil(t, current.ParentID, "no refused reparent may have moved the team")
	require.Equal(t, []uuid.UUID{host}, current.Path)
}

// ---------------------------------------------------------------------------
// projects
// ---------------------------------------------------------------------------

// TestAdapterNeg_ItemLookups_AreOrgScopedAndHideSoftDeletedRows: item keys are
// human-readable and guessable (VEC-1, VEC-2...), so key resolution is a
// tenancy boundary. And a soft-deleted item has to be gone from every read
// path, not just from the list.
//
// Fails-before: drop `org_id = $1` from GetProjectItemByOrgKey and the
// cross-org resolve returns another tenant's item; drop `deleted_at IS NULL`
// from either getter and the post-delete assertions fail.
func TestAdapterNeg_ItemLookups_AreOrgScopedAndHideSoftDeletedRows(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	a := adapters.NewItemAdapter(generated.New(db.Pool))

	item := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Doomed",
		Status: "open", Priority: "medium", ReporterID: user.ID,
	}
	survivor := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Survivor",
		Status: "open", Priority: "medium", ReporterID: user.ID,
	}
	require.NoError(t, a.Create(ctx, item))
	require.NoError(t, a.Create(ctx, survivor))
	require.NotEmpty(t, item.ItemKey, "create assigns the human-readable key")

	otherOrg := testutil.CreateTestOrg(t, db.Pool)
	_, err := a.GetByOrgKey(ctx, otherOrg.ID, item.ItemKey)
	require.ErrorIs(t, err, projects.ErrNotFound, "an item key must not resolve outside its own org")

	found, err := a.GetByOrgKey(ctx, org.ID, item.ItemKey)
	require.NoError(t, err, "control: the owning org resolves the same key")
	require.Equal(t, item.ID, found.ID)

	_, err = a.GetByID(ctx, uuid.New())
	require.ErrorIs(t, err, projects.ErrNotFound)

	require.NoError(t, a.SoftDelete(ctx, item.ID))
	_, err = a.GetByID(ctx, item.ID)
	require.ErrorIs(t, err, projects.ErrNotFound, "a soft-deleted item must read as absent by id")
	_, err = a.GetByOrgKey(ctx, org.ID, item.ItemKey)
	require.ErrorIs(t, err, projects.ErrNotFound, "and by key — a dead key must not resurrect the row")

	listed, err := a.ListBySpace(ctx, space.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1, "the space listing drops the deleted item")
	require.Equal(t, survivor.ID, listed[0].ID)
}

// TestAdapterNeg_ItemWrites_LandOnTheRowAndOnlyOnThatRow: Update and
// UpdateStatus had no integration coverage at all. Both are unqualified
// single-row UPDATEs — a missing `WHERE id` predicate, or a params struct
// wired to the wrong column, would rewrite the wrong item, and nothing in the
// unit tests would notice because they assert on the params struct rather
// than on the database.
//
// Fails-before: swap any two fields in UpdateProjectItemParams and the
// round-trip assertions fail; widen the UPDATE and the bystander assertion
// fails.
func TestAdapterNeg_ItemWrites_LandOnTheRowAndOnlyOnThatRow(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	a := adapters.NewItemAdapter(generated.New(db.Pool))

	target := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Before",
		Description: "before", Status: "open", Priority: "low", ReporterID: user.ID,
	}
	bystander := &projects.Item{
		ID: uuid.New(), SpaceID: space.ID, Kind: "task", Title: "Untouched",
		Description: "untouched", Status: "open", Priority: "low", ReporterID: user.ID,
	}
	require.NoError(t, a.Create(ctx, target))
	require.NoError(t, a.Create(ctx, bystander))

	assignee := user.ID
	target.Title = "After"
	target.Description = "after"
	target.Status = "in_progress"
	target.Priority = "high"
	target.Kind = "bug"
	target.AssigneeID = &assignee
	target.Labels = []string{"regression"}
	target.Rank = "m"
	require.NoError(t, a.Update(ctx, target))

	got, err := a.GetByID(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, "After", got.Title)
	require.Equal(t, "after", got.Description)
	require.Equal(t, "in_progress", got.Status)
	require.Equal(t, "high", got.Priority)
	require.Equal(t, "bug", got.Kind)
	require.Equal(t, []string{"regression"}, got.Labels)
	require.Equal(t, "m", got.Rank)
	require.NotNil(t, got.AssigneeID)
	require.Equal(t, assignee, *got.AssigneeID)

	updated, err := a.UpdateStatus(ctx, target.ID, "done")
	require.NoError(t, err)
	require.Equal(t, "done", updated.Status)
	require.Equal(t, "After", updated.Title, "a status change must not disturb the other columns")

	other, err := a.GetByID(ctx, bystander.ID)
	require.NoError(t, err)
	require.Equal(t, "Untouched", other.Title, "neither write may touch a sibling row")
	require.Equal(t, "open", other.Status)
	require.Nil(t, other.AssigneeID)
}

// TestAdapterNeg_SprintReads_NoActiveSprintIsTheNotFoundSentinel: "this space
// has no active sprint" is the ordinary state of a space between sprints. The
// repository contract makes it ErrNotFound, which the API maps to 404 and the
// board reads as "no sprint" — an unmapped pgx.ErrNoRows here would be a 500
// on a perfectly normal board.
//
// Fails-before: delete the ErrNoRows mapping in GetActiveBySpace and the
// first two assertions fail (a wrapped pgx error is not projects.ErrNotFound);
// drop `status = 'active'` from the query and the planned-sprint assertion
// fails.
func TestAdapterNeg_SprintReads_NoActiveSprintIsTheNotFoundSentinel(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	org := testutil.CreateTestOrg(t, db.Pool)
	user := testutil.CreateTestUser(t, db.Pool, org.ID)
	space := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	a := adapters.NewSprintAdapter(db.Pool)

	_, err := a.GetActiveBySpace(ctx, space.ID)
	require.ErrorIs(t, err, projects.ErrNotFound, "a space with no sprints at all has no active sprint")

	_, err = a.GetByID(ctx, uuid.New())
	require.ErrorIs(t, err, projects.ErrNotFound)

	planned := &projects.Sprint{
		ID: uuid.New(), SpaceID: space.ID, Name: "Sprint 1",
		Status: projects.SprintStatusPlanned, CreatedBy: user.ID,
	}
	require.NoError(t, a.Create(ctx, planned))
	_, err = a.GetActiveBySpace(ctx, space.ID)
	require.ErrorIs(t, err, projects.ErrNotFound,
		"a planned sprint is not an active one — the query filters on status, not on existence")

	_, err = a.UpdateStatus(ctx, planned.ID, projects.SprintStatusActive)
	require.NoError(t, err)
	active, err := a.GetActiveBySpace(ctx, space.ID)
	require.NoError(t, err, "control: once activated it resolves")
	require.Equal(t, planned.ID, active.ID)

	// ListBySpace is space-scoped, and a second space's sprints stay out of it.
	elsewhere := testutil.CreateTestSpace(t, db.Pool, org.ID, user.ID, "vector")
	require.NoError(t, a.Create(ctx, &projects.Sprint{
		ID: uuid.New(), SpaceID: elsewhere.ID, Name: "Other space sprint",
		Status: projects.SprintStatusPlanned, CreatedBy: user.ID,
	}))
	listed, err := a.ListBySpace(ctx, space.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1, "ListBySpace must not reach into another space")
	require.Equal(t, planned.ID, listed[0].ID)

	_, err = a.GetActiveBySpace(ctx, elsewhere.ID)
	require.ErrorIs(t, err, projects.ErrNotFound,
		fmt.Sprintf("space %s has only a planned sprint", elsewhere.ID))
}
