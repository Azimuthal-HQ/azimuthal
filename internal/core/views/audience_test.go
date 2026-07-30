package views

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The audience rule is now shared between saved views (migration 038) and
// dashboards (migration 048). It is the one place "who may see this
// definition" is answered, so it is tested directly rather than only through
// whichever model happens to call it.
//
// Every case here is written so that WIDENING the rule fails it. A test that
// only checked the positive direction would pass with `Reaches` replaced by
// `return true`, which is the shape spec §2's negative-test question forbids.

func actor(id uuid.UUID, teams ...uuid.UUID) Actor {
	return Actor{UserID: id, EffectiveTeamIDs: teams}
}

func TestAudience_PrivateReachesOnlyTheOwner(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	a := Audience{Visibility: VisibilityPrivate}

	require.True(t, a.Reaches(owner, actor(owner)), "the owner always reaches their own definition")
	require.False(t, a.Reaches(owner, actor(other)),
		"a private definition must reach nobody else — this fails if Reaches ever defaults open")
}

func TestAudience_OrgReachesEveryMember(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	a := Audience{Visibility: VisibilityOrg}

	require.True(t, a.Reaches(owner, actor(other)))
}

func TestAudience_TeamReachesOnlyTheEffectiveSet(t *testing.T) {
	owner, inTeam, outsider := uuid.New(), uuid.New(), uuid.New()
	team, otherTeam := uuid.New(), uuid.New()
	a := Audience{Visibility: VisibilityTeam, TeamID: &team}

	require.True(t, a.Reaches(owner, actor(inTeam, team)))
	require.False(t, a.Reaches(owner, actor(outsider, otherTeam)),
		"membership of a different team must not reach a team-shared definition")
	require.False(t, a.Reaches(owner, actor(outsider)),
		"no team at all must not reach a team-shared definition")
}

// A degraded team audience — the team was deleted, so migration 038/048 nulled
// the column — must match NOBODY but the owner. Fail closed, then prompt.
//
// Fails-before: drop the `a.TeamID != nil` guard in Reaches and
// `inTeam(*a.TeamID)` panics or, if written as a zero-uuid comparison, admits
// anyone whose effective set happens to contain the nil uuid.
func TestAudience_DegradedTeamMatchesNobodyButTheOwner(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	a := Audience{Visibility: VisibilityTeam, TeamID: nil}

	require.True(t, a.Reaches(owner, actor(owner)),
		"the owner still reaches a degraded definition — that is who the re-scope prompt is for")
	require.False(t, a.Reaches(owner, actor(other, uuid.Nil)),
		"a team audience with no team must reach nobody, even an actor carrying the nil uuid")
}

// The space audience belongs to queues, whose route establishes readability.
// Resolving it here would widen a queue past the guard that bounds it.
func TestAudience_SpaceAudienceIsNeverResolvedHere(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	a := Audience{Visibility: VisibilitySpace}

	require.False(t, a.Reaches(owner, actor(other)),
		"a space-audience row must not reach anyone through the generic audience rule")
}

func TestAudience_OrgAdminBypassesBothWays(t *testing.T) {
	owner, admin := uuid.New(), uuid.New()
	a := Audience{Visibility: VisibilityPrivate}
	adminActor := Actor{UserID: admin, IsOrgAdmin: true}

	require.True(t, a.Reaches(owner, adminActor))
	require.True(t, a.OwnedBy(owner, adminActor), "the org-admin bypass is an edit right too")
	require.False(t, a.OwnedBy(owner, actor(uuid.New())),
		"an ordinary member is never an owner of somebody else's definition")
}

func TestAudienceNormalise_DropsATeamIdOnANonTeamAudience(t *testing.T) {
	team := uuid.New()
	me := uuid.New()

	for _, vis := range []Visibility{VisibilityPrivate, VisibilityOrg} {
		got, err := Audience{Visibility: vis, TeamID: &team}.Normalise(actor(me, team))
		require.NoError(t, err)
		require.Nil(t, got.TeamID,
			"a %s audience must not keep a team id — a stored one is a lie the next reader has to interpret", vis)
	}
}

func TestAudienceNormalise_RefusesTheDegradedStateOnAWrite(t *testing.T) {
	me := uuid.New()

	_, err := Audience{Visibility: VisibilityTeam}.Normalise(actor(me))
	require.ErrorIs(t, err, ErrTeamRequired,
		"the (team, NULL) state must be representable in the database and unreachable by a write")
}

func TestAudienceNormalise_RefusesATeamTheActorDoesNotBelongTo(t *testing.T) {
	me, theirTeam := uuid.New(), uuid.New()

	_, err := Audience{Visibility: VisibilityTeam, TeamID: &theirTeam}.Normalise(actor(me))
	require.ErrorIs(t, err, ErrTeamNotMember)

	// The org admin bypasses it, as everywhere else.
	_, err = Audience{Visibility: VisibilityTeam, TeamID: &theirTeam}.
		Normalise(Actor{UserID: me, IsOrgAdmin: true})
	require.NoError(t, err)
}

func TestAudienceNormalise_RefusesAnUnknownVisibility(t *testing.T) {
	_, err := Audience{Visibility: "everyone"}.Normalise(actor(uuid.New()))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrTeamRequired), "an unknown visibility is its own error, not a missing team")

	// The space audience is a real value on saved_views and still not one a
	// generic write may set: a queue is created through the queue routes.
	_, err = Audience{Visibility: VisibilitySpace}.Normalise(actor(uuid.New()))
	require.Error(t, err)
}
