package workflow

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
)

// The guard evaluator is pure: it performs no queries and reaches for no data
// it was not handed. That is what makes these table tests the real coverage of
// ADR-0011 tier 1 rather than a smoke screen over an integration test.

func ptr[T any](v T) *T { return &v }

func actorWith(userID uuid.UUID, teams []uuid.UUID, caps ...access.Capability) Actor {
	a := Actor{
		UserID:       userID,
		TeamIDs:      map[uuid.UUID]struct{}{},
		Capabilities: map[access.Capability]struct{}{},
	}
	for _, t := range teams {
		a.TeamIDs[t] = struct{}{}
	}
	for _, c := range caps {
		a.Capabilities[c] = struct{}{}
	}
	return a
}

// ─── The fail-closed rule ─────────────────────────────────────────────────────

// TestEvaluate_UnknownKindFailsClosed is the mutation test for the default
// branch in evaluateOne. Delete that branch and this test dies: the switch
// falls through, evaluateOne returns nil, and an unrecognised guard silently
// permits the transition.
//
// The case models a row written by a newer build and read by an older one —
// a rolling deploy or a rollback — which is the only way it occurs and exactly
// the case where failing closed matters.
func TestEvaluate_UnknownKindFailsClosed(t *testing.T) {
	t.Parallel()

	for _, class := range []GuardClass{GuardConditionClass, GuardValidatorClass} {
		g := Guard{ID: uuid.New(), Class: class, Kind: GuardKind("from_a_future_release")}

		// The actor is maximally privileged and the entity is fully populated:
		// nothing here is missing, so a refusal can only come from the kind
		// being unrecognised.
		actor := actorWith(uuid.New(), nil,
			access.CapEditAnyItem, access.CapTransitionAnyItem, access.CapManageQueue, access.CapManageSpace)
		entity := EntitySnapshot{
			AssigneeID:  ptr(actor.UserID),
			DueAt:       ptr(time.Now()),
			Description: "filled in",
			Labels:      []string{"a"},
		}

		refusal := Evaluate([]Guard{g}, class, actor, entity)

		require.NotNil(t, refusal, "an unrecognised guard kind must refuse, never permit (class %q)", class)
		require.Equal(t, g.ID, refusal.GuardID)
		require.Contains(t, refusal.Reason, "does not understand")
	}
}

// TestFieldPresent_UnknownFieldKeyIsAbsent is the same rule one level down: a
// field key this build cannot read has not been filled in as far as this build
// can tell, so the requirement is unmet rather than waived.
func TestFieldPresent_UnknownFieldKeyIsAbsent(t *testing.T) {
	t.Parallel()

	full := EntitySnapshot{
		AssigneeID:  ptr(uuid.New()),
		DueAt:       ptr(time.Now()),
		Description: "filled in",
		Labels:      []string{"a"},
	}
	require.False(t, fieldPresent(FieldKey("story_points"), full),
		"an unknown field key must read as absent, so the guard refuses rather than waives")
}

// ─── Per-kind behaviour, both directions ──────────────────────────────────────

func TestEvaluate_ActorIsAssignee(t *testing.T) {
	t.Parallel()

	assignee := uuid.New()
	other := uuid.New()
	g := Guard{ID: uuid.New(), Class: GuardValidatorClass, Kind: GuardActorIsAssignee}

	t.Run("the assignee is allowed", func(t *testing.T) {
		t.Parallel()
		r := Evaluate([]Guard{g}, GuardValidatorClass,
			actorWith(assignee, nil), EntitySnapshot{AssigneeID: ptr(assignee)})
		require.Nil(t, r)
	})

	t.Run("anyone else is refused", func(t *testing.T) {
		t.Parallel()
		r := Evaluate([]Guard{g}, GuardValidatorClass,
			actorWith(other, nil), EntitySnapshot{AssigneeID: ptr(assignee)})
		require.NotNil(t, r)
		require.Equal(t, GuardActorIsAssignee, r.Kind)
		require.Contains(t, r.Reason, "assignee")
	})

	t.Run("an unassigned entity can never satisfy it", func(t *testing.T) {
		t.Parallel()
		r := Evaluate([]Guard{g}, GuardValidatorClass,
			actorWith(other, nil), EntitySnapshot{AssigneeID: nil})
		require.NotNil(t, r, "no assignee means nobody is the assignee, including the actor")
	})
}

func TestEvaluate_ActorInTeam(t *testing.T) {
	t.Parallel()

	team := uuid.New()
	otherTeam := uuid.New()

	t.Run("an effective member is allowed", func(t *testing.T) {
		t.Parallel()
		g := Guard{ID: uuid.New(), Class: GuardConditionClass, Kind: GuardActorInTeam, TeamID: ptr(team)}
		r := Evaluate([]Guard{g}, GuardConditionClass,
			actorWith(uuid.New(), []uuid.UUID{otherTeam, team}), EntitySnapshot{})
		require.Nil(t, r)
	})

	t.Run("a non-member is refused", func(t *testing.T) {
		t.Parallel()
		g := Guard{ID: uuid.New(), Class: GuardConditionClass, Kind: GuardActorInTeam, TeamID: ptr(team)}
		r := Evaluate([]Guard{g}, GuardConditionClass,
			actorWith(uuid.New(), []uuid.UUID{otherTeam}), EntitySnapshot{})
		require.NotNil(t, r)
		require.Equal(t, GuardActorInTeam, r.Kind)
	})

	// The degraded state migration 046 deliberately keeps representable: the
	// team was deleted and ON DELETE SET NULL preserved the guard rather than
	// dropping it. Dropping it would have removed the restriction silently.
	t.Run("a deleted team makes the guard unsatisfiable, never absent", func(t *testing.T) {
		t.Parallel()
		g := Guard{ID: uuid.New(), Class: GuardConditionClass, Kind: GuardActorInTeam, TeamID: nil}
		r := Evaluate([]Guard{g}, GuardConditionClass,
			actorWith(uuid.New(), []uuid.UUID{team, otherTeam}), EntitySnapshot{})
		require.NotNil(t, r, "a guard whose team was deleted must refuse everyone, not permit everyone")
		require.Contains(t, r.Reason, "no longer exists")
	})
}

func TestEvaluate_ActorHasCapability(t *testing.T) {
	t.Parallel()

	g := Guard{
		ID: uuid.New(), Class: GuardValidatorClass,
		Kind: GuardActorHasCapability, Capability: ptr(access.CapManageSpace),
	}

	t.Run("holding the capability is allowed", func(t *testing.T) {
		t.Parallel()
		r := Evaluate([]Guard{g}, GuardValidatorClass,
			actorWith(uuid.New(), nil, access.CapTransitionAnyItem, access.CapManageSpace), EntitySnapshot{})
		require.Nil(t, r)
	})

	// The persona that matters: someone who cleared the route's own
	// transition_any_item floor and still lacks the guarded capability. A
	// viewer would prove nothing here — they never reach the guard.
	t.Run("clearing the transition floor is not enough", func(t *testing.T) {
		t.Parallel()
		r := Evaluate([]Guard{g}, GuardValidatorClass,
			actorWith(uuid.New(), nil, access.CapTransitionAnyItem), EntitySnapshot{})
		require.NotNil(t, r)
		require.Contains(t, r.Reason, string(access.CapManageSpace))
	})

	t.Run("a capability missing from the configuration refuses", func(t *testing.T) {
		t.Parallel()
		broken := Guard{ID: uuid.New(), Class: GuardValidatorClass, Kind: GuardActorHasCapability}
		r := Evaluate([]Guard{broken}, GuardValidatorClass,
			actorWith(uuid.New(), nil, access.CapManageSpace), EntitySnapshot{})
		require.NotNil(t, r, "a parameterless capability guard cannot be satisfied by anyone")
	})
}

func TestEvaluate_FieldRequired(t *testing.T) {
	t.Parallel()

	full := EntitySnapshot{
		AssigneeID:  ptr(uuid.New()),
		DueAt:       ptr(time.Now()),
		Description: "a real description",
		Labels:      []string{"needs-review"},
	}

	cases := []struct {
		field   FieldKey
		empty   EntitySnapshot
		inWords string
	}{
		{FieldAssigneeID, EntitySnapshot{AssigneeID: nil, DueAt: full.DueAt, Description: full.Description, Labels: full.Labels}, "assignee"},
		{FieldDueAt, EntitySnapshot{AssigneeID: full.AssigneeID, DueAt: nil, Description: full.Description, Labels: full.Labels}, "due date"},
		{FieldDescription, EntitySnapshot{AssigneeID: full.AssigneeID, DueAt: full.DueAt, Description: "   \t\n ", Labels: full.Labels}, "description"},
		{FieldLabels, EntitySnapshot{AssigneeID: full.AssigneeID, DueAt: full.DueAt, Description: full.Description, Labels: nil}, "label"},
	}

	for _, tc := range cases {
		t.Run(string(tc.field), func(t *testing.T) {
			t.Parallel()
			g := Guard{ID: uuid.New(), Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(tc.field)}
			actor := actorWith(uuid.New(), nil)

			require.Nil(t, Evaluate([]Guard{g}, GuardValidatorClass, actor, full),
				"a populated entity satisfies %q", tc.field)

			r := Evaluate([]Guard{g}, GuardValidatorClass, actor, tc.empty)
			require.NotNil(t, r, "an empty %q must refuse", tc.field)
			require.Contains(t, r.Reason, tc.inWords)
		})
	}
}

// A description of only whitespace is empty. Asserted separately because it is
// the one emptiness rule that is a judgement rather than a nil check.
func TestFieldPresent_WhitespaceDescriptionIsEmpty(t *testing.T) {
	t.Parallel()
	require.False(t, fieldPresent(FieldDescription, EntitySnapshot{Description: "  \n\t "}))
	require.True(t, fieldPresent(FieldDescription, EntitySnapshot{Description: " x "}))
}

// ─── Class separation and ordering ────────────────────────────────────────────

// A validator must not fire while conditions are being evaluated, and vice
// versa. Without this, a validator would hide a transition instead of refusing
// it — the user would never see the action, and never learn why.
func TestEvaluate_OnlyEvaluatesTheRequestedClass(t *testing.T) {
	t.Parallel()

	validator := Guard{ID: uuid.New(), Class: GuardValidatorClass, Kind: GuardActorIsAssignee}
	condition := Guard{ID: uuid.New(), Class: GuardConditionClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt)}
	guards := []Guard{validator, condition}

	// Actor is not the assignee (fails the validator) but the due date is set
	// (satisfies the condition).
	actor := actorWith(uuid.New(), nil)
	entity := EntitySnapshot{AssigneeID: ptr(uuid.New()), DueAt: ptr(time.Now())}

	require.Nil(t, Evaluate(guards, GuardConditionClass, actor, entity),
		"the failing validator must not be evaluated as a condition")

	r := Evaluate(guards, GuardValidatorClass, actor, entity)
	require.NotNil(t, r)
	require.Equal(t, validator.ID, r.GuardID)
}

// Guards are read ordered by (position, id) so a transition blocked by two
// guards names the same one every time. A refusal that changes identity between
// requests is a refusal a user cannot act on.
func TestEvaluate_ReturnsTheFirstFailingGuardInOrder(t *testing.T) {
	t.Parallel()

	first := Guard{ID: uuid.New(), Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt), Position: 0}
	second := Guard{ID: uuid.New(), Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldLabels), Position: 1}

	r := Evaluate([]Guard{first, second}, GuardValidatorClass, actorWith(uuid.New(), nil), EntitySnapshot{})
	require.NotNil(t, r)
	require.Equal(t, first.ID, r.GuardID, "the earliest failing guard is the one reported")
}

// No guards is not a refusal. This is the invariant that keeps every existing
// workflow byte-identical: the seeded defaults carry no guards, so every
// transition that worked before this migration still works.
func TestEvaluate_NoGuardsPermits(t *testing.T) {
	t.Parallel()
	require.Nil(t, Evaluate(nil, GuardValidatorClass, actorWith(uuid.New(), nil), EntitySnapshot{}))
	require.Nil(t, Evaluate([]Guard{}, GuardConditionClass, actorWith(uuid.New(), nil), EntitySnapshot{}))
}

// ─── Write-side validation ────────────────────────────────────────────────────

func TestValidateGuard_RefusesUnknownVocabulary(t *testing.T) {
	t.Parallel()

	require.Error(t, ValidateGuard(Guard{Class: "sometimes", Kind: GuardActorIsAssignee}))
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: "actor_is_lucky"}))
	require.Error(t, ValidateGuard(Guard{
		Class: GuardConditionClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldKey("story_points")),
	}))

	// A capability that exists in the access model but is not a permitted guard
	// capability is still refused — the guard vocabulary is a deliberate subset,
	// not "any capability".
	require.Error(t, ValidateGuard(Guard{
		Class: GuardConditionClass, Kind: GuardActorHasCapability, Capability: ptr(access.CapReadItems),
	}), "read_items asserts nothing as a transition guard and must not be configurable")
}

func TestValidateGuard_RefusesParameterMismatch(t *testing.T) {
	t.Parallel()

	team := uuid.New()

	// Right kind, wrong parameter.
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardActorIsAssignee, TeamID: &team}))
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardActorInTeam, FieldKey: ptr(FieldDueAt)}))
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt), TeamID: &team}))

	// Missing parameter.
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardActorInTeam}))
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardActorHasCapability}))
	require.Error(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardFieldRequired}))

	// The four well-formed shapes.
	require.NoError(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardActorIsAssignee}))
	require.NoError(t, ValidateGuard(Guard{Class: GuardValidatorClass, Kind: GuardActorInTeam, TeamID: &team}))
	require.NoError(t, ValidateGuard(Guard{Class: GuardConditionClass, Kind: GuardActorHasCapability, Capability: ptr(access.CapManageSpace)}))
	require.NoError(t, ValidateGuard(Guard{Class: GuardValidatorClass, Kind: GuardFieldRequired, FieldKey: ptr(FieldLabels)}))
}

// ─── Exhaustiveness ───────────────────────────────────────────────────────────

// Every kind in the vocabulary must be reachable by both halves of the model.
// A kind added to allGuardKinds without an evaluate case would fall into the
// fail-closed default and refuse everything; a kind added without a shape rule
// would be unwritable. Both are silent until something like this asserts them.
func TestGuardVocabulary_EveryKindIsEvaluableAndWritable(t *testing.T) {
	t.Parallel()

	// A well-formed guard for each kind. Adding a kind without extending this
	// map fails the length check below.
	wellFormed := map[GuardKind]Guard{
		GuardActorIsAssignee:    {Kind: GuardActorIsAssignee},
		GuardActorInTeam:        {Kind: GuardActorInTeam, TeamID: ptr(uuid.New())},
		GuardActorHasCapability: {Kind: GuardActorHasCapability, Capability: ptr(access.CapManageSpace)},
		GuardFieldRequired:      {Kind: GuardFieldRequired, FieldKey: ptr(FieldDueAt)},
	}
	require.Len(t, wellFormed, len(allGuardKinds),
		"every guard kind needs a well-formed example here; add the new kind to this map and to the cases below")

	for _, kind := range allGuardKinds {
		g, ok := wellFormed[kind]
		require.True(t, ok, "guard kind %q has no well-formed example", kind)

		g.ID = uuid.New()
		g.Class = GuardValidatorClass
		require.NoError(t, ValidateGuard(g), "guard kind %q has no shape rule that accepts it", kind)

		// The kind must reach a real evaluate case rather than the fail-closed
		// default. An actor and entity that satisfy every kind proves it.
		actor := actorWith(uuid.New(), nil, access.CapManageSpace)
		if g.TeamID != nil {
			actor.TeamIDs[*g.TeamID] = struct{}{}
		}
		entity := EntitySnapshot{
			AssigneeID:  ptr(actor.UserID),
			DueAt:       ptr(time.Now()),
			Description: "x",
			Labels:      []string{"x"},
		}
		require.Nil(t, Evaluate([]Guard{g}, GuardValidatorClass, actor, entity),
			"guard kind %q fell through to the fail-closed default instead of being evaluated", kind)
	}
}

// The guard capability subset must stay a subset of the real capability model.
// A rename in internal/core/access that left a stale constant here would be a
// guard nobody can ever satisfy.
func TestGuardCapabilities_AreRealCapabilities(t *testing.T) {
	t.Parallel()

	// Role.Grants answers false for an unknown capability, so a capability that
	// no role holds is either org-level or a typo. All four guard capabilities
	// are space-scoped, so space_admin must hold every one of them.
	for _, c := range guardCapabilities {
		require.True(t, access.RoleSpaceAdmin.Grants(c),
			"guard capability %q is not held by space_admin — it is not a space-scoped capability, or the constant is stale", c)
	}
}

// The wire values are what migration 046's CHECK constraints list and what the
// TypeScript mirror compares against. A duplicate would make two kinds
// indistinguishable on the wire.
func TestGuardVocabulary_WireValuesAreUniqueAndSnakeCase(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	add := func(kind, v string) {
		require.False(t, seen[v], "wire value %q is used twice", v)
		seen[v] = true
		require.Regexp(t, `^[a-z][a-z0-9_]*$`, v, "%s wire value %q is not snake_case", kind, v)
	}
	for _, v := range allGuardClasses {
		add("guard class", string(v))
	}
	for _, v := range allGuardKinds {
		add("guard kind", string(v))
	}
	for _, v := range allFieldKeys {
		add("field key", string(v))
	}
}
