package workflow

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCanDecide_UserApprover(t *testing.T) {
	t.Parallel()

	approver := uuid.New()
	someoneElse := uuid.New()
	approvers := []Approver{{ID: uuid.New(), SubjectType: ApproverUser, SubjectID: approver}}

	require.True(t, CanDecide(approvers, actorWith(approver, nil)))
	require.False(t, CanDecide(approvers, actorWith(someoneElse, nil)))
}

func TestCanDecide_TeamApprover(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	otherTeam := uuid.New()
	approvers := []Approver{{ID: uuid.New(), SubjectType: ApproverTeam, SubjectID: team}}

	// Membership is the caller-supplied ADR-0007 effective set, so a member of
	// an ancestor team decides here exactly as they would be granted access
	// there. The two can never disagree because they read the same expansion.
	require.True(t, CanDecide(approvers, actorWith(uuid.New(), []uuid.UUID{otherTeam, team})))
	require.False(t, CanDecide(approvers, actorWith(uuid.New(), []uuid.UUID{otherTeam})))
	require.False(t, CanDecide(approvers, actorWith(uuid.New(), nil)))
}

// Several subjects on one step is a quorum of one: any of them may decide.
// That is what ADR-0011's "pending approval from a named user, team, or role"
// describes, and it is distinct from a multi-step chain, which this version
// deliberately does not model.
func TestCanDecide_AnyOfSeveralSubjects(t *testing.T) {
	t.Parallel()

	namedUser := uuid.New()
	namedTeam := uuid.New()
	approvers := []Approver{
		{ID: uuid.New(), SubjectType: ApproverUser, SubjectID: namedUser},
		{ID: uuid.New(), SubjectType: ApproverTeam, SubjectID: namedTeam},
	}

	require.True(t, CanDecide(approvers, actorWith(namedUser, nil)))
	require.True(t, CanDecide(approvers, actorWith(uuid.New(), []uuid.UUID{namedTeam})))
	require.False(t, CanDecide(approvers, actorWith(uuid.New(), []uuid.UUID{uuid.New()})))
}

// A gate naming nobody is unsatisfiable, not open. The alternative turns a
// misconfiguration into an unguarded edge — the silent permit this tier exists
// to prevent.
func TestCanDecide_NoApproversMeansNobody(t *testing.T) {
	t.Parallel()
	require.False(t, CanDecide(nil, actorWith(uuid.New(), []uuid.UUID{uuid.New()})))
	require.False(t, CanDecide([]Approver{}, actorWith(uuid.New(), nil)))
}

// TestCanDecide_UnknownSubjectTypeMatchesNobody is the fail-closed mutation
// test for the default branch in CanDecide. Change that branch to `return true`
// — or delete the discriminator check so any subject id is compared against
// both the user id and the team set — and this test dies.
func TestCanDecide_UnknownSubjectTypeMatchesNobody(t *testing.T) {
	t.Parallel()

	shared := uuid.New()
	approvers := []Approver{{ID: uuid.New(), SubjectType: ApproverSubjectType("role"), SubjectID: shared}}

	// The actor's own id and their team set both contain the subject id, so
	// only the discriminator can keep this false.
	actor := actorWith(shared, []uuid.UUID{shared})
	require.False(t, CanDecide(approvers, actor),
		"an approver subject kind this build cannot resolve must match nobody")
}

func TestRequiresApproval(t *testing.T) {
	t.Parallel()
	require.False(t, RequiresApproval(nil))
	require.False(t, RequiresApproval([]Approver{}))
	require.True(t, RequiresApproval([]Approver{{SubjectType: ApproverUser, SubjectID: uuid.New()}}))
}

func TestApproval_IsPending(t *testing.T) {
	t.Parallel()

	now := time.Now()
	decided := DecisionApproved
	user := uuid.New()

	require.True(t, Approval{}.IsPending())
	require.False(t, Approval{DecidedAt: &now, Decision: &decided, DecidedBy: &user}.IsPending())
}

// ─── Vocabulary boundaries ────────────────────────────────────────────────────

func TestValidateApprover(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateApprover(Approver{SubjectType: ApproverUser, SubjectID: uuid.New()}))
	require.NoError(t, ValidateApprover(Approver{SubjectType: ApproverTeam, SubjectID: uuid.New()}))

	// ADR-0011 names a third approver kind. It has no representation in this
	// product and is refused rather than approximated; see approval.go.
	require.Error(t, ValidateApprover(Approver{SubjectType: "role", SubjectID: uuid.New()}),
		"approver-by-role is not representable and must not be writable")
	require.Error(t, ValidateApprover(Approver{SubjectType: ApproverUser, SubjectID: uuid.Nil}))
}

func TestParseDecision(t *testing.T) {
	t.Parallel()

	for _, d := range allDecisions {
		got, err := ParseDecision(string(d))
		require.NoError(t, err)
		require.Equal(t, d, got)
	}

	// An unrecognised value is an error, never silently one of the two — the
	// rule access.ParseRole states for roles.
	for _, bad := range []string{"", "APPROVED", "pending", "expired", "yes"} {
		_, err := ParseDecision(bad)
		require.Error(t, err, "ParseDecision(%q) must not succeed", bad)
	}
}

func TestParseApprovalEntityType(t *testing.T) {
	t.Parallel()

	for _, e := range allApprovalEntityTypes {
		got, err := ParseApprovalEntityType(string(e))
		require.NoError(t, err)
		require.Equal(t, e, got)
	}
	for _, bad := range []string{"", "page", "project_item", "tickets"} {
		_, err := ParseApprovalEntityType(bad)
		require.Error(t, err, "ParseApprovalEntityType(%q) must not succeed", bad)
	}
}

// The approver subject vocabulary must match space_grants' subject_type
// wire values, so one word means one thing across the product.
func TestApproverSubjectTypes_MatchTheGrantVocabulary(t *testing.T) {
	t.Parallel()
	require.Equal(t, "user", string(ApproverUser))
	require.Equal(t, "team", string(ApproverTeam))
}

// The entity discriminator must match the audit log's existing words for the
// same two things.
func TestApprovalEntityTypes_MatchTheAuditVocabulary(t *testing.T) {
	t.Parallel()
	require.Equal(t, "ticket", string(ApprovalEntityTicket))
	require.Equal(t, "item", string(ApprovalEntityItem))
}

func TestApprovalVocabulary_WireValuesAreSnakeCase(t *testing.T) {
	t.Parallel()

	for _, v := range allApproverSubjectTypes {
		require.Regexp(t, `^[a-z][a-z0-9_]*$`, string(v))
	}
	for _, v := range allDecisions {
		require.Regexp(t, `^[a-z][a-z0-9_]*$`, string(v))
	}
	for _, v := range allApprovalEntityTypes {
		require.Regexp(t, `^[a-z][a-z0-9_]*$`, string(v))
	}
}
