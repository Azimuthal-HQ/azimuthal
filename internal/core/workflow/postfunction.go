package workflow

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
)

// This file is the single place the workflow post-function vocabulary is
// defined, and it is CLOSED BY DESIGN in the strongest sense ADR-0011 uses:
//
//	"Permitted actions: set a field, assign to a user or team, add a comment,
//	 transition a linked item. That set is defined in code. It is extended only
//	 by a deliberate release decision, never by configuration, and never by
//	 anything a user supplies."
//
// There is therefore no registration mechanism, no plugin seam, and no way for
// a deployment to add a fifth action. Adding one is a code change, a migration
// widening migration 046's CHECK, and a release.
//
// # What ships, and what does not
//
// Two of the ADR's four actions ship. The other two are absent for reasons of
// data model rather than policy, and are recorded here so the gap is legible
// rather than looking like an oversight:
//
//   - set a field            SHIPS, over a narrower field set than the guards
//     read. See PostFieldKey.
//   - assign to a user       SHIPS.
//   - ...or a team           NOT REPRESENTABLE. Both entity tables declare
//     `assignee_id UUID REFERENCES users (id)` (migration
//     014). A team cannot hold an assignment in this
//     product, so there is nothing to write.
//   - add a comment          NOT BUILT HERE. The comment surface is owned by
//     another track in flight; a second writer would
//     collide with it.
//   - transition a linked    NOT MODELLED. No link table exists. The only
//     item                   structural relation is project_items.parent_id,
//     which is a hierarchy, and traversing it would need
//     a cycle guard the ADR does not describe.
//
// # Where these run
//
// Every action here MUTATES, and every one of them runs inside the same
// transaction as the status change it follows. That is not a preference: a
// post-function that lands when the transition rolls back has invented an
// effect with no cause, and one that is lost when the transition commits has
// silently not run. Notification of the resulting change is the only part that
// happens after commit, because the notification queue is pool-backed and
// cannot enlist in the caller's transaction.

// PostFunctionKind is an action a transition performs after it is permitted and
// before it commits.
type PostFunctionKind string

// The two shipped actions. See the file header for the two that are not.
const (
	// PostAssignTo sets the assignee to a fixed user, or clears it when no user
	// is named.
	PostAssignTo PostFunctionKind = "assign_to"
	// PostSetField writes a literal value into one of the fields PostFieldKey
	// names.
	PostSetField PostFunctionKind = "set_field"
)

// allPostFunctionKinds is hand-maintained; see allGuardKinds.
var allPostFunctionKinds = []PostFunctionKind{PostAssignTo, PostSetField}

// PostFieldKey names a field PostSetField may write.
//
// It is deliberately NARROWER than the guard vocabulary, which only reads:
//
//   - `description` is readable by a guard and not writable here. A
//     post-function that overwrote it would destroy author-written prose on
//     every transition — silent data loss dressed as automation.
//   - `assignee_id` is absent because PostAssignTo owns it. Two ways to write
//     one column is how the two come to disagree.
type PostFieldKey string

// The two writable fields.
const (
	PostFieldDueAt PostFieldKey = "due_at"
	// PostFieldTags was PostFieldLabels ("labels") until the entity-tags
	// convergence (migration 055). The wire encoding of its value is
	// unchanged and stated here so nobody infers it from the parser: a
	// COMMA-SEPARATED label list ("escalated,urgent"), never a JSON array.
	// What changed is where it lands — the applier replaces the entity's
	// entity_tags associations instead of writing a text-array column.
	PostFieldTags PostFieldKey = "tags"
)

// allPostFieldKeys is hand-maintained; see allGuardKinds.
var allPostFieldKeys = []PostFieldKey{PostFieldDueAt, PostFieldTags}

// PostFunction is one configured action on one transition.
type PostFunction struct {
	ID           uuid.UUID        `json:"id"`
	TransitionID uuid.UUID        `json:"transition_id"`
	Kind         PostFunctionKind `json:"kind"`
	Position     int32            `json:"position"`

	// AssigneeUserID is meaningful iff Kind is PostAssignTo. Nil means
	// "unassign" — and also means "the configured user was deleted", because
	// migration 046 collapses the two with ON DELETE SET NULL. Both outcomes
	// remove an assignment rather than granting one, which is why the collapse
	// is safe; see the migration header.
	AssigneeUserID *uuid.UUID `json:"assignee_user_id,omitempty"`

	// FieldKey and FieldValue are meaningful iff Kind is PostSetField.
	FieldKey   *PostFieldKey `json:"field_key,omitempty"`
	FieldValue *string       `json:"field_value,omitempty"`
}

// Effect is one resolved mutation, ready for the transaction to apply.
//
// Planning is separated from applying so the decision of WHAT to write is pure
// and table-testable, and the adapter that owns the transaction only has to
// know how to write. Exactly one field of an Effect is non-nil.
type Effect struct {
	// SetAssignee is present when the assignee changes. The inner pointer is
	// nil to unassign, which is why this is a pointer to a pointer: the outer
	// says "assignment changes", the inner says "to whom".
	SetAssignee **uuid.UUID
	// SetDueAt is present when the due date changes; the inner pointer is nil
	// to clear it.
	SetDueAt **time.Time
	// SetTags is present when the tag set is replaced. The labels it carries
	// are resolved to tag rows by the applier, leniently: a stored label that
	// cannot become a tag is dropped at apply time rather than refusing the
	// transition, because the value may predate the convergence and a person
	// mid-transition cannot fix a workflow's configuration.
	SetTags *[]string
}

// PlanPostFunctions turns the configured post-functions into the ordered list of
// mutations to apply, refusing any it cannot represent.
//
// Post-functions are applied in the order given; callers read them ordered by
// (position, id). A later post-function writing the same field as an earlier one
// wins, which is the ordinary meaning of a sequence and the reason the order is
// stable.
//
// # Unknown kinds abort the transition
//
// An unrecognised kind is an action this build cannot perform. It does not skip:
// skipping means the transition commits having silently not done something an
// administrator configured, and for an approval-gated compliance workflow that
// is exactly the outcome the tier exists to prevent. The transition fails
// instead, and the error names the kind.
//
// This is the same direction as an unknown guard, arrived at from the other
// side: an unevaluable guard must not permit, and an unperformable action must
// not be treated as performed.
func PlanPostFunctions(pfs []PostFunction) ([]Effect, error) {
	effects := make([]Effect, 0, len(pfs))

	for i := range pfs {
		p := pfs[i]
		switch p.Kind {
		case PostAssignTo:
			assignee := p.AssigneeUserID
			effects = append(effects, Effect{SetAssignee: &assignee})

		case PostSetField:
			if p.FieldKey == nil {
				return nil, fmt.Errorf("post-function %s: %w", p.ID, ErrPostFunctionMalformed)
			}
			e, err := planSetField(*p.FieldKey, p.FieldValue)
			if err != nil {
				return nil, fmt.Errorf("post-function %s: %w", p.ID, err)
			}
			effects = append(effects, e)

		default:
			// See "Unknown kinds abort the transition" above. Returning nil
			// here instead would let an unrecognised action pass as done;
			// TestPlanPostFunctions_UnknownKindAborts catches that.
			return nil, fmt.Errorf("post-function %s has kind %q: %w", p.ID, p.Kind, ErrPostFunctionUnknown)
		}
	}

	return effects, nil
}

func planSetField(key PostFieldKey, raw *string) (Effect, error) {
	switch key {
	case PostFieldDueAt:
		if raw == nil || strings.TrimSpace(*raw) == "" {
			var cleared *time.Time
			return Effect{SetDueAt: &cleared}, nil
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
		if err != nil {
			// Stored values are validated on write, so this is a value written
			// by a build that parsed it differently. Refusing is the same
			// fail-closed direction as an unknown kind.
			return Effect{}, fmt.Errorf("due date %q is not RFC3339: %w", *raw, ErrPostFunctionMalformed)
		}
		p := &t
		return Effect{SetDueAt: &p}, nil

	case PostFieldTags:
		return Effect{SetTags: ptrTo(parseTags(raw))}, nil

	default:
		return Effect{}, fmt.Errorf("field %q: %w", key, ErrPostFunctionUnknown)
	}
}

// parseTags reads the stored comma-separated label list — the same wire
// encoding PostFieldLabels always used. Empty entries are dropped and
// surrounding whitespace is trimmed, so "a, ,b " is {"a","b"} and "" is the
// empty set rather than a single empty label.
func parseTags(raw *string) []string {
	if raw == nil {
		return []string{}
	}
	parts := strings.Split(*raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func ptrTo[T any](v T) *T { return &v }

// ─── Write-side validation ────────────────────────────────────────────────────

// ValidatePostFunction refuses any post-function this build does not name, and
// any whose parameters do not match its kind. It runs at the API boundary, so
// nothing unrecognised is ever written; migration 046's CHECK constraints refuse
// the same set one layer down.
func ValidatePostFunction(p PostFunction) error {
	if !knownPostFunctionKind(p.Kind) {
		return fmt.Errorf("unknown post-function kind %q: must be one of %s",
			p.Kind, joinPostKinds(allPostFunctionKinds))
	}

	switch p.Kind {
	case PostAssignTo:
		if p.FieldKey != nil || p.FieldValue != nil {
			return fmt.Errorf("post-function kind %q takes only an assignee", p.Kind)
		}
		return nil

	case PostSetField:
		return validateSetField(p)

	default:
		// Unreachable while knownPostFunctionKind is exhaustive; kept because
		// the alternative is a silent accept when a kind is added to
		// allPostFunctionKinds without a case here.
		return fmt.Errorf("post-function kind %q has no shape rule", p.Kind)
	}
}

func validateSetField(p PostFunction) error {
	if p.AssigneeUserID != nil {
		return fmt.Errorf("post-function kind %q takes only a field", p.Kind)
	}
	if p.FieldKey == nil {
		return fmt.Errorf("post-function kind %q requires a field", p.Kind)
	}
	if !knownPostFieldKey(*p.FieldKey) {
		return fmt.Errorf("field %q cannot be set by a post-function: must be one of %s",
			string(*p.FieldKey), joinPostFields(allPostFieldKeys))
	}
	// The value must be readable now, not at transition time. A due date that
	// fails to parse would otherwise break every transition through this edge,
	// at a moment far from the mistake that caused it.
	if _, err := planSetField(*p.FieldKey, p.FieldValue); err != nil {
		return err
	}
	// Tag labels are additionally checked as taggable at the API boundary —
	// and ONLY here. The apply path is deliberately lenient (see Effect.SetTags),
	// so a label that can never become a tag would otherwise be accepted now
	// and silently set nothing on every transition later. The person writing
	// the configuration is the one who can fix it, so they are the one told.
	if *p.FieldKey == PostFieldTags {
		for _, label := range parseTags(p.FieldValue) {
			if tags.Slugify(label) == "" {
				return fmt.Errorf("label %q cannot become a tag: %w", label, ErrPostFunctionMalformed)
			}
		}
	}
	return nil
}

func knownPostFunctionKind(k PostFunctionKind) bool {
	for _, v := range allPostFunctionKinds {
		if v == k {
			return true
		}
	}
	return false
}

func knownPostFieldKey(f PostFieldKey) bool {
	for _, v := range allPostFieldKeys {
		if v == f {
			return true
		}
	}
	return false
}

func joinPostKinds(vs []PostFunctionKind) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinPostFields(vs []PostFieldKey) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}
