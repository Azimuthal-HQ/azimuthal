package workflow

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ─── The fail-closed rule, from the other side ────────────────────────────────

// TestPlanPostFunctions_UnknownKindAborts is the mutation test for the default
// branch in PlanPostFunctions. Replace that branch with `continue` and this
// test dies — and the defect it lets through is a transition that commits
// having silently not performed an action an administrator configured.
//
// An unevaluable guard must not permit; an unperformable action must not be
// treated as performed. Same rule, both directions.
func TestPlanPostFunctions_UnknownKindAborts(t *testing.T) {
	t.Parallel()

	pfs := []PostFunction{
		{ID: uuid.New(), Kind: PostAssignTo},
		{ID: uuid.New(), Kind: PostFunctionKind("send_carrier_pigeon")},
	}

	effects, err := PlanPostFunctions(pfs)

	require.Error(t, err, "an unrecognised post-function must abort the transition, never be skipped")
	require.ErrorIs(t, err, ErrPostFunctionUnknown)
	require.Nil(t, effects, "no effect may be applied when the plan could not be completed")
}

func TestPlanPostFunctions_MalformedSetFieldAborts(t *testing.T) {
	t.Parallel()

	t.Run("missing field key", func(t *testing.T) {
		t.Parallel()
		_, err := PlanPostFunctions([]PostFunction{{ID: uuid.New(), Kind: PostSetField}})
		require.ErrorIs(t, err, ErrPostFunctionMalformed)
	})

	t.Run("unparseable due date", func(t *testing.T) {
		t.Parallel()
		bad := "next tuesday"
		_, err := PlanPostFunctions([]PostFunction{{
			ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldDueAt), FieldValue: &bad,
		}})
		require.ErrorIs(t, err, ErrPostFunctionMalformed)
	})

	t.Run("unknown field key", func(t *testing.T) {
		t.Parallel()
		v := "x"
		_, err := PlanPostFunctions([]PostFunction{{
			ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldKey("story_points")), FieldValue: &v,
		}})
		require.ErrorIs(t, err, ErrPostFunctionUnknown)
	})
}

// ─── Planning ─────────────────────────────────────────────────────────────────

func TestPlanPostFunctions_AssignTo(t *testing.T) {
	t.Parallel()

	target := uuid.New()

	t.Run("assigns a named user", func(t *testing.T) {
		t.Parallel()
		effects, err := PlanPostFunctions([]PostFunction{{ID: uuid.New(), Kind: PostAssignTo, AssigneeUserID: &target}})
		require.NoError(t, err)
		require.Len(t, effects, 1)
		require.NotNil(t, effects[0].SetAssignee)
		require.NotNil(t, *effects[0].SetAssignee)
		require.Equal(t, target, **effects[0].SetAssignee)
	})

	// Nil means unassign — and also means "the configured user was deleted",
	// because migration 046 collapses the two with ON DELETE SET NULL. The
	// outcome removes an assignment rather than granting one, which is why the
	// collapse is safe.
	t.Run("no user means unassign, and the effect is still applied", func(t *testing.T) {
		t.Parallel()
		effects, err := PlanPostFunctions([]PostFunction{{ID: uuid.New(), Kind: PostAssignTo}})
		require.NoError(t, err)
		require.Len(t, effects, 1)
		require.NotNil(t, effects[0].SetAssignee, "unassign is an effect, not the absence of one")
		require.Nil(t, *effects[0].SetAssignee)
	})
}

func TestPlanPostFunctions_SetField(t *testing.T) {
	t.Parallel()

	t.Run("due date parses as RFC3339", func(t *testing.T) {
		t.Parallel()
		v := "2026-08-01T09:30:00Z"
		effects, err := PlanPostFunctions([]PostFunction{{
			ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldDueAt), FieldValue: &v,
		}})
		require.NoError(t, err)
		require.NotNil(t, effects[0].SetDueAt)
		require.NotNil(t, *effects[0].SetDueAt)
		require.Equal(t, time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC), (**effects[0].SetDueAt).UTC())
	})

	t.Run("an empty due date clears it", func(t *testing.T) {
		t.Parallel()
		v := "  "
		effects, err := PlanPostFunctions([]PostFunction{{
			ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldDueAt), FieldValue: &v,
		}})
		require.NoError(t, err)
		require.NotNil(t, effects[0].SetDueAt)
		require.Nil(t, *effects[0].SetDueAt)
	})

	t.Run("labels split on commas, dropping empties", func(t *testing.T) {
		t.Parallel()
		v := "needs-review, ,escalated ,"
		effects, err := PlanPostFunctions([]PostFunction{{
			ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldLabels), FieldValue: &v,
		}})
		require.NoError(t, err)
		require.NotNil(t, effects[0].SetLabels)
		require.Equal(t, []string{"needs-review", "escalated"}, *effects[0].SetLabels)
	})

	t.Run("no label value is the empty set, not one empty label", func(t *testing.T) {
		t.Parallel()
		effects, err := PlanPostFunctions([]PostFunction{{
			ID: uuid.New(), Kind: PostSetField, FieldKey: ptr(PostFieldLabels),
		}})
		require.NoError(t, err)
		require.NotNil(t, effects[0].SetLabels)
		require.Empty(t, *effects[0].SetLabels)
	})
}

// Post-functions apply in order, and a later one writing the same field wins.
// That is the ordinary meaning of a sequence, and it is the reason the read is
// ordered by (position, id).
func TestPlanPostFunctions_PreservesOrder(t *testing.T) {
	t.Parallel()

	first, second := uuid.New(), uuid.New()
	effects, err := PlanPostFunctions([]PostFunction{
		{ID: uuid.New(), Kind: PostAssignTo, AssigneeUserID: &first, Position: 0},
		{ID: uuid.New(), Kind: PostAssignTo, AssigneeUserID: &second, Position: 1},
	})
	require.NoError(t, err)
	require.Len(t, effects, 2)
	require.Equal(t, first, **effects[0].SetAssignee)
	require.Equal(t, second, **effects[1].SetAssignee)
}

// No post-functions is no effects — the invariant that keeps every existing
// workflow byte-identical.
func TestPlanPostFunctions_NoneIsNoEffects(t *testing.T) {
	t.Parallel()
	effects, err := PlanPostFunctions(nil)
	require.NoError(t, err)
	require.Empty(t, effects)
}

// ─── Write-side validation ────────────────────────────────────────────────────

func TestValidatePostFunction(t *testing.T) {
	t.Parallel()

	user := uuid.New()
	good := "2026-08-01T09:30:00Z"
	bad := "tomorrow"

	require.Error(t, ValidatePostFunction(PostFunction{Kind: "add_comment"}),
		"add_comment is ADR-sanctioned but not built here; it must not be writable")
	require.Error(t, ValidatePostFunction(PostFunction{Kind: "transition_linked_item"}))
	require.Error(t, ValidatePostFunction(PostFunction{Kind: PostSetField}))
	require.Error(t, ValidatePostFunction(PostFunction{
		Kind: PostSetField, FieldKey: ptr(PostFieldKey("description")), FieldValue: &good,
	}), "description is readable by a guard and must not be writable by a post-function")
	require.Error(t, ValidatePostFunction(PostFunction{
		Kind: PostSetField, FieldKey: ptr(PostFieldKey("assignee_id")),
	}), "assign_to owns the assignee; set_field must not be a second writer")
	require.Error(t, ValidatePostFunction(PostFunction{Kind: PostAssignTo, FieldKey: ptr(PostFieldDueAt)}))
	require.Error(t, ValidatePostFunction(PostFunction{
		Kind: PostSetField, AssigneeUserID: &user, FieldKey: ptr(PostFieldDueAt), FieldValue: &good,
	}))

	// A value that cannot be parsed is refused at configuration time, not at
	// transition time — otherwise the mistake surfaces far from its cause, on
	// somebody else's transition.
	require.Error(t, ValidatePostFunction(PostFunction{
		Kind: PostSetField, FieldKey: ptr(PostFieldDueAt), FieldValue: &bad,
	}))

	require.NoError(t, ValidatePostFunction(PostFunction{Kind: PostAssignTo, AssigneeUserID: &user}))
	require.NoError(t, ValidatePostFunction(PostFunction{Kind: PostAssignTo}))
	require.NoError(t, ValidatePostFunction(PostFunction{
		Kind: PostSetField, FieldKey: ptr(PostFieldDueAt), FieldValue: &good,
	}))
	require.NoError(t, ValidatePostFunction(PostFunction{Kind: PostSetField, FieldKey: ptr(PostFieldLabels)}))
}

// Every kind must be both writable and plannable. A kind added to
// allPostFunctionKinds without a plan case would abort every transition through
// its edge; without a shape rule it would be unwritable.
func TestPostFunctionVocabulary_EveryKindIsWritableAndPlannable(t *testing.T) {
	t.Parallel()

	user := uuid.New()
	v := "2026-08-01T09:30:00Z"
	wellFormed := map[PostFunctionKind]PostFunction{
		PostAssignTo: {Kind: PostAssignTo, AssigneeUserID: &user},
		PostSetField: {Kind: PostSetField, FieldKey: ptr(PostFieldDueAt), FieldValue: &v},
	}
	require.Len(t, wellFormed, len(allPostFunctionKinds),
		"every post-function kind needs a well-formed example here")

	for _, kind := range allPostFunctionKinds {
		p, ok := wellFormed[kind]
		require.True(t, ok, "post-function kind %q has no well-formed example", kind)
		p.ID = uuid.New()

		require.NoError(t, ValidatePostFunction(p), "kind %q has no shape rule that accepts it", kind)

		effects, err := PlanPostFunctions([]PostFunction{p})
		require.NoError(t, err, "kind %q fell through to the abort branch instead of being planned", kind)
		require.Len(t, effects, 1)
	}
}

// Every writable field must be plannable, and no guard-only field may be.
func TestPostFieldVocabulary_IsANarrowingOfTheGuardFields(t *testing.T) {
	t.Parallel()

	for _, f := range allPostFieldKeys {
		_, err := planSetField(f, nil)
		require.NoError(t, err, "writable field %q has no plan case", f)

		// Every writable field is also a readable one, so a guard can require
		// what a post-function sets.
		require.True(t, knownFieldKey(FieldKey(f)),
			"writable field %q is not in the guard vocabulary, so no guard could require it", f)
	}

	// The narrowing is real, not accidental: at least one guard field is
	// deliberately not writable. If this ever stops being true the comment in
	// migration 046 explaining why is stale.
	require.Less(t, len(allPostFieldKeys), len(allFieldKeys),
		"the post-function field set must stay strictly narrower than the guard field set")
	require.False(t, knownPostFieldKey(PostFieldKey(FieldDescription)))
	require.False(t, knownPostFieldKey(PostFieldKey(FieldAssigneeID)))
}

func TestPostFunctionVocabulary_WireValuesAreUniqueAndSnakeCase(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, v := range allPostFunctionKinds {
		require.False(t, seen[string(v)], "wire value %q is used twice", v)
		seen[string(v)] = true
		require.Regexp(t, `^[a-z][a-z0-9_]*$`, string(v))
	}
	for _, v := range allPostFieldKeys {
		require.Regexp(t, `^[a-z][a-z0-9_]*$`, string(v))
	}
}

// Errors are matched with errors.Is at the HTTP layer, so they must wrap.
func TestPostFunctionErrors_Wrap(t *testing.T) {
	t.Parallel()
	_, err := PlanPostFunctions([]PostFunction{{ID: uuid.New(), Kind: "nope"}})
	require.True(t, errors.Is(err, ErrPostFunctionUnknown))
}
