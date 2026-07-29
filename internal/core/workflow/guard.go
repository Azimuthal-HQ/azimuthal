package workflow

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
)

// This file is the single place the workflow guard vocabulary is defined. It
// is the contract four other things depend on, and they will drift the moment
// it is duplicated:
//
//   - the API validator, which refuses any guard this file does not name;
//   - the CHECK constraints in migration 046, which refuse the same set one
//     layer down;
//   - the admin UI's pickers, which may only offer what this file enumerates
//     (mirrored in TypeScript and held equal by web/src/lib/workflow/guards.test.ts);
//   - the Jira importer anticipated by ADR-0011 and ADR-0012, which maps Jira
//     conditions and validators onto exactly these kinds and must be able to
//     report what it cannot represent rather than approximating it silently.
//
// # Why this is not an expression language
//
// ADR-0011 admits conditions and validators and permanently refuses arbitrary
// scripting — "no Groovy, no JavaScript hooks, no user-supplied code, no plugin
// execution — at any tier, under any framing, in any edition, now or later."
// That refusal is only real if the thing that replaces it cannot become a
// language by accident.
//
// So a guard is a RECORD, not a tree. There is no operator, no boolean nesting,
// no negation, and no way for a caller to name a field the code does not
// already know. A transition's guards combine with AND, and that is the whole
// semantics. The same reasoning internal/core/views/filter.go applies to saved
// views applies here without change, and for the same reason: a predicate you
// cannot reason about statically is one you cannot explain, test, or migrate.
//
// # Why one vocabulary serves both classes
//
// ADR-0011 separates the two by effect, not by question: "A condition
// determines whether a transition is offered; a validator determines whether it
// succeeds." The question each asks is the same question, so GuardClass is a
// field rather than the discriminator between two vocabularies. `field_required`
// as a condition hides "Close" until the field is filled; the same guard as a
// validator offers "Close" and refuses it with a reason. Both are useful, and
// neither needs a vocabulary of its own.

// GuardClass is which half of ADR-0011 tier 1 a guard belongs to.
type GuardClass string

// The two guard classes. A condition is evaluated when transitions are listed
// and silently removes the transition from the offer; a validator is evaluated
// when a transition is applied and refuses it by name.
const (
	GuardConditionClass GuardClass = "condition"
	GuardValidatorClass GuardClass = "validator"
)

// allGuardClasses is hand-maintained on purpose. Go cannot enumerate the
// members of a const block at runtime, so a derived list would assert nothing —
// the same reasoning internal/core/access/capability_validity_test.go writes
// out for the capability constants.
var allGuardClasses = []GuardClass{GuardConditionClass, GuardValidatorClass}

// GuardKind is the predicate a guard asks. The set is closed and this is the
// only place it is defined.
type GuardKind string

// The four guard kinds, each traced to ADR-0011.
const (
	// GuardActorIsAssignee is ADR-0011's "only the assignee may move an item to
	// In Review". An entity with no assignee can never satisfy it.
	GuardActorIsAssignee GuardKind = "actor_is_assignee"

	// GuardActorInTeam is ADR-0011's "Only members of a given team may reopen".
	// Membership is ADR-0007 EFFECTIVE membership — the actor's direct teams and
	// every descendant — resolved by the same effective_team_ids() function
	// space grants use, so a guard and a grant can never disagree about who is
	// in a team.
	GuardActorInTeam GuardKind = "actor_in_team"

	// GuardActorHasCapability is ADR-0011's "restricted transitions", expressed
	// against the capability model rather than against role names. It reads the
	// resolved capability set, never a role string.
	GuardActorHasCapability GuardKind = "actor_has_capability"

	// GuardFieldRequired is ADR-0011's "A required field must be non-empty".
	// The field vocabulary is closed; see FieldKey.
	GuardFieldRequired GuardKind = "field_required"
)

// allGuardKinds is hand-maintained; see allGuardClasses.
var allGuardKinds = []GuardKind{
	GuardActorIsAssignee,
	GuardActorInTeam,
	GuardActorHasCapability,
	GuardFieldRequired,
}

// FieldKey names a field GuardFieldRequired can require.
//
// Every member exists, with the same meaning, on BOTH tickets and
// project_items, and every member is genuinely emptiable. `priority` is
// deliberately absent: it is NOT NULL with a default on both tables, so
// requiring it would assert something that cannot be false — a check that reads
// as coverage and is not.
type FieldKey string

// The four requirable fields.
const (
	FieldAssigneeID  FieldKey = "assignee_id"
	FieldDueAt       FieldKey = "due_at"
	FieldDescription FieldKey = "description"
	FieldLabels      FieldKey = "labels"
)

// allFieldKeys is hand-maintained; see allGuardClasses.
var allFieldKeys = []FieldKey{
	FieldAssigneeID,
	FieldDueAt,
	FieldDescription,
	FieldLabels,
}

// guardCapabilities is the closed subset of the capability model a
// GuardActorHasCapability guard may name.
//
// It references the access constants directly rather than restating their wire
// strings, so a capability rename cannot leave a stale copy here. It is a
// SUBSET and not the whole table because most capabilities are meaningless as a
// transition guard: gating a transition on `read_items` asserts nothing (the
// actor already read the item to transition it), and gating it on
// `manage_grants` conflates workflow with administration. These four are the
// ones that describe how much authority an actor has over an item.
var guardCapabilities = []access.Capability{
	access.CapEditAnyItem,
	access.CapTransitionAnyItem,
	access.CapManageQueue,
	access.CapManageSpace,
}

// Guard is one configured predicate on one transition.
//
// Exactly one parameter field is populated, chosen by Kind and enforced by
// migration 046's shape CHECK. The parameters are typed columns rather than a
// document because they carry constraints a document would discard — see the
// migration header.
type Guard struct {
	ID           uuid.UUID  `json:"id"`
	TransitionID uuid.UUID  `json:"transition_id"`
	Class        GuardClass `json:"guard_class"`
	Kind         GuardKind  `json:"kind"`
	Position     int32      `json:"position"`

	// Capability is set iff Kind is GuardActorHasCapability.
	Capability *access.Capability `json:"capability,omitempty"`
	// TeamID is set iff Kind is GuardActorInTeam — except in the degraded
	// state, where the team was deleted and the reference was SET NULL. A
	// GuardActorInTeam with no TeamID is unsatisfiable by design; see
	// Evaluate.
	TeamID *uuid.UUID `json:"team_id,omitempty"`
	// FieldKey is set iff Kind is GuardFieldRequired.
	FieldKey *FieldKey `json:"field_key,omitempty"`
}

// Actor is what a guard is allowed to know about the person transitioning.
//
// It is a resolved snapshot rather than a context: the evaluator performs no
// queries, so it is pure, table-testable, and incapable of turning into an
// engine that reaches for data a guard was not given.
type Actor struct {
	UserID uuid.UUID
	// TeamIDs is the actor's ADR-0007 effective team set for the org — direct
	// teams and all descendants — as returned by effective_team_ids().
	TeamIDs map[uuid.UUID]struct{}
	// Capabilities is the actor's resolved capability set in the entity's
	// space.
	Capabilities map[access.Capability]struct{}
}

// EntitySnapshot is what a guard is allowed to know about the thing being
// transitioned.
//
// It is deliberately entity-agnostic. Tickets and project items are separate
// tables and stay separate (ADR-0003), but every field here exists on both, so
// nothing in this evaluator presumes ticket shape and Vector needs no fork of
// it.
type EntitySnapshot struct {
	AssigneeID  *uuid.UUID
	DueAt       *time.Time
	Description string
	Labels      []string
}

// Refusal explains why a guard was not satisfied.
//
// ADR-0011's case for tier 1 rests on inspectability — "they are fully
// inspectable — the engine can always explain why a transition was refused" —
// so a refusal names the guard that produced it rather than collapsing to a
// flat error. Reason is written for a person and is safe to show: it names
// configuration, never another user's data.
type Refusal struct {
	GuardID uuid.UUID  `json:"guard_id"`
	Class   GuardClass `json:"guard_class"`
	Kind    GuardKind  `json:"kind"`
	Reason  string     `json:"reason"`
}

// Error makes a Refusal usable as an error at call sites that only need the
// sentence.
func (r *Refusal) Error() string { return r.Reason }

// Evaluate returns the first guard of the given class that the actor and entity
// do not satisfy, or nil when every one of them is satisfied.
//
// Guards are evaluated in the order given; callers read them ordered by
// (position, id) so a transition blocked by two guards names the same one every
// time.
//
// # Unknown kinds deny
//
// A kind this build does not recognise is a guard that cannot be evaluated, and
// a guard that cannot be evaluated has NOT been satisfied. The default branch
// below refuses it. That direction is not a matter of taste: skipping an
// unevaluable condition OFFERS a transition an administrator restricted, and
// skipping an unevaluable validator COMMITS a write an administrator forbade.
// Both are silent permits. This mirrors Role.Grants, which answers false for an
// unknown capability through an explicit test rather than a map zero value
// (internal/core/access/capability.go).
//
// The case is reachable in exactly one way — a row written by a newer build and
// read by an older one, during a rolling deploy or after a rollback — and that
// is precisely the case where failing closed matters.
func Evaluate(guards []Guard, class GuardClass, actor Actor, entity EntitySnapshot) *Refusal {
	for i := range guards {
		g := guards[i]
		if g.Class != class {
			continue
		}
		if r := evaluateOne(g, actor, entity); r != nil {
			return r
		}
	}
	return nil
}

func evaluateOne(g Guard, actor Actor, entity EntitySnapshot) *Refusal {
	switch g.Kind {
	case GuardActorIsAssignee:
		return evalActorIsAssignee(g, actor, entity)
	case GuardActorInTeam:
		return evalActorInTeam(g, actor)
	case GuardActorHasCapability:
		return evalActorHasCapability(g, actor)
	case GuardFieldRequired:
		return evalFieldRequired(g, entity)
	default:
		// See the "Unknown kinds deny" note on Evaluate. Deleting this branch
		// makes every unrecognised guard permit, which is the defect
		// TestEvaluate_UnknownKindFailsClosed exists to catch.
		return g.refuse("this transition carries a guard this version of Azimuthal does not understand, so it cannot be allowed")
	}
}

func evalActorIsAssignee(g Guard, actor Actor, entity EntitySnapshot) *Refusal {
	if entity.AssigneeID != nil && *entity.AssigneeID == actor.UserID {
		return nil
	}
	return g.refuse("only the assignee may make this transition")
}

func evalActorInTeam(g Guard, actor Actor) *Refusal {
	if g.TeamID == nil {
		// Degraded: the team was deleted and migration 046's ON DELETE SET NULL
		// preserved the guard rather than dropping it. Dropping it would have
		// removed the restriction silently, so the guard survives as
		// unsatisfiable until an administrator re-scopes it.
		return g.refuse("this transition is restricted to a team that no longer exists; an administrator must re-scope it")
	}
	if _, ok := actor.TeamIDs[*g.TeamID]; ok {
		return nil
	}
	return g.refuse("this transition is restricted to members of a specific team")
}

func evalActorHasCapability(g Guard, actor Actor) *Refusal {
	if g.Capability == nil {
		return g.refuse("this transition names a capability that is missing from its configuration")
	}
	if _, ok := actor.Capabilities[*g.Capability]; ok {
		return nil
	}
	return g.refuse(fmt.Sprintf("this transition requires the %q capability in this space", string(*g.Capability)))
}

func evalFieldRequired(g Guard, entity EntitySnapshot) *Refusal {
	if g.FieldKey == nil {
		return g.refuse("this transition requires a field that is missing from its configuration")
	}
	if fieldPresent(*g.FieldKey, entity) {
		return nil
	}
	return g.refuse(fmt.Sprintf("%s must be set before this transition", fieldLabel(*g.FieldKey)))
}

func (g Guard) refuse(reason string) *Refusal {
	return &Refusal{GuardID: g.ID, Class: g.Class, Kind: g.Kind, Reason: reason}
}

// fieldPresent reports whether the required field carries a value.
//
// "Non-empty" is per-field and deliberate: a description of only whitespace is
// empty, an empty label array is empty, and a zero timestamp cannot occur
// because the column is nullable rather than defaulted.
func fieldPresent(f FieldKey, e EntitySnapshot) bool {
	switch f {
	case FieldAssigneeID:
		return e.AssigneeID != nil
	case FieldDueAt:
		return e.DueAt != nil
	case FieldDescription:
		return strings.TrimSpace(e.Description) != ""
	case FieldLabels:
		return len(e.Labels) > 0
	default:
		// Unknown field key: not present. Same fail-closed direction as an
		// unknown kind — a field this build cannot read has not been filled in
		// as far as this build can tell.
		return false
	}
}

// fieldLabel renders a field key for a refusal sentence. Refusals are shown to
// people, and "assignee_id must be set" reads like a stack trace.
func fieldLabel(f FieldKey) string {
	switch f {
	case FieldAssigneeID:
		return "An assignee"
	case FieldDueAt:
		return "A due date"
	case FieldDescription:
		return "A description"
	case FieldLabels:
		return "At least one label"
	default:
		return "A required field"
	}
}

// ─── Write-side validation ────────────────────────────────────────────────────

// ValidateGuard refuses any guard this build does not name, and any guard whose
// parameter does not match its kind.
//
// It is the API boundary's check and runs before anything is written, so no
// unrecognised guard ever reaches the table. Migration 046's CHECK constraints
// refuse the same set one layer down; this exists so the caller gets a sentence
// rather than a constraint violation.
func ValidateGuard(g Guard) error {
	if !knownClass(g.Class) {
		return fmt.Errorf("unknown guard class %q: must be one of %s", g.Class, joinClasses(allGuardClasses))
	}
	if !knownKind(g.Kind) {
		return fmt.Errorf("unknown guard kind %q: must be one of %s", g.Kind, joinKinds(allGuardKinds))
	}

	switch g.Kind {
	case GuardActorIsAssignee:
		return requireNoParams(g)
	case GuardActorInTeam:
		return validateTeamGuard(g)
	case GuardActorHasCapability:
		return validateCapabilityGuard(g)
	case GuardFieldRequired:
		return validateFieldGuard(g)
	default:
		// Unreachable while knownKind above is exhaustive, and kept because the
		// alternative is a silent accept if a kind is ever added to
		// allGuardKinds without a case here.
		// TestGuardVocabulary_EveryKindIsEvaluableAndWritable fails in exactly
		// that case.
		return fmt.Errorf("guard kind %q has no shape rule", g.Kind)
	}
}

func requireNoParams(g Guard) error {
	if g.Capability != nil || g.TeamID != nil || g.FieldKey != nil {
		return fmt.Errorf("guard kind %q takes no parameters", g.Kind)
	}
	return nil
}

func validateTeamGuard(g Guard) error {
	if g.TeamID == nil {
		return fmt.Errorf("guard kind %q requires a team", g.Kind)
	}
	if g.Capability != nil || g.FieldKey != nil {
		return fmt.Errorf("guard kind %q takes only a team", g.Kind)
	}
	return nil
}

func validateCapabilityGuard(g Guard) error {
	if g.Capability == nil {
		return fmt.Errorf("guard kind %q requires a capability", g.Kind)
	}
	if !knownGuardCapability(*g.Capability) {
		return fmt.Errorf("capability %q cannot guard a transition: must be one of %s",
			string(*g.Capability), joinCapabilities(guardCapabilities))
	}
	if g.TeamID != nil || g.FieldKey != nil {
		return fmt.Errorf("guard kind %q takes only a capability", g.Kind)
	}
	return nil
}

func validateFieldGuard(g Guard) error {
	if g.FieldKey == nil {
		return fmt.Errorf("guard kind %q requires a field", g.Kind)
	}
	if !knownFieldKey(*g.FieldKey) {
		return fmt.Errorf("unknown field %q: must be one of %s", string(*g.FieldKey), joinFields(allFieldKeys))
	}
	if g.TeamID != nil || g.Capability != nil {
		return fmt.Errorf("guard kind %q takes only a field", g.Kind)
	}
	return nil
}

func knownClass(c GuardClass) bool {
	for _, k := range allGuardClasses {
		if k == c {
			return true
		}
	}
	return false
}

func knownKind(k GuardKind) bool {
	for _, v := range allGuardKinds {
		if v == k {
			return true
		}
	}
	return false
}

func knownFieldKey(f FieldKey) bool {
	for _, v := range allFieldKeys {
		if v == f {
			return true
		}
	}
	return false
}

func knownGuardCapability(c access.Capability) bool {
	for _, v := range guardCapabilities {
		if v == c {
			return true
		}
	}
	return false
}

func joinClasses(vs []GuardClass) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinKinds(vs []GuardKind) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinFields(vs []FieldKey) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinCapabilities(vs []access.Capability) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinQuoted(vs []string) string {
	s := ""
	for i, v := range vs {
		if i > 0 {
			s += ", "
		}
		s += `"` + v + `"`
	}
	return s
}
