package workflow

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// This file defines ADR-0011 tier 2: a transition that blocks pending approval,
// with the pending state visible and the decision recorded.
//
// # One step, and why that is a version rather than a limitation
//
// A transition carries at most one approval step in this version. Multi-step
// chains — sequential approvers, quorums, escalation — are deliberately out of
// scope and are NOT approximated by letting an administrator attach several
// steps that happen to run in order. Attaching two half-modelled steps would
// produce something that looks like a chain, is not one, and cannot be migrated
// into one later without reinterpreting stored rows. One step is honest; the
// ADR does not require more.
//
// Several approver SUBJECTS on one step is a different thing and is supported:
// any one of them may decide. That is a quorum of one, which is what "pending
// approval from a named user, team, or role" describes.
//
// # The item does not move while approval is pending
//
// See migration 047's header. The item stays in its source status and the
// transition commits on approval, so a declined request leaves the item exactly
// where the requester found it and no new status vocabulary is introduced.

// ApproverSubjectType is the kind of subject that may approve.
//
// Two values, not ADR-0011's three. "role" has no representation in this
// product — space roles have no user-set resolution query, and team roles are
// metadata explicitly forbidden as a permission input — and adding one would
// change the access model, which is a stop-and-raise decision rather than a
// phase decision. The gap is reported rather than approximated.
type ApproverSubjectType string

// The two approver subject kinds. The wire values match space_grants'
// subject_type (migration 023) so one word means one thing across the product.
const (
	ApproverUser ApproverSubjectType = "user"
	ApproverTeam ApproverSubjectType = "team"
)

// allApproverSubjectTypes is hand-maintained; see allGuardKinds.
var allApproverSubjectTypes = []ApproverSubjectType{ApproverUser, ApproverTeam}

// Approver is one configured subject who may decide a transition's approval.
type Approver struct {
	ID           uuid.UUID           `json:"id"`
	TransitionID uuid.UUID           `json:"transition_id"`
	SubjectType  ApproverSubjectType `json:"subject_type"`
	SubjectID    uuid.UUID           `json:"subject_id"`

	// SubjectName is resolved at read time for display, never stored. Empty
	// when the subject no longer exists.
	SubjectName string `json:"subject_name,omitempty"`
	// SubjectMissing marks an approver whose subject has been deleted — the
	// same pair access.Grant carries, resolved the same way.
	SubjectMissing bool `json:"subject_missing,omitempty"`
}

// Decision is the recorded outcome of an approval request.
type Decision string

// The two outcomes. A request with no decision is pending; there is no third
// value, and "expired" is deliberately not one — nothing times an approval out
// in this version, so a value that no code can produce would be a lie in the
// vocabulary.
const (
	DecisionApproved Decision = "approved"
	DecisionDeclined Decision = "declined"
)

// allDecisions is hand-maintained; see allGuardKinds.
var allDecisions = []Decision{DecisionApproved, DecisionDeclined}

// ApprovalEntityType names which table the awaited item lives in.
//
// tickets and project_items stay separate (ADR-0003), so an approval row
// carries a discriminator rather than two nullable foreign keys. The values
// match the audit log's existing entity_kind words for the same two things.
type ApprovalEntityType string

// The two entity kinds an approval can be about.
const (
	ApprovalEntityTicket ApprovalEntityType = "ticket"
	ApprovalEntityItem   ApprovalEntityType = "item"
)

// allApprovalEntityTypes is hand-maintained; see allGuardKinds.
var allApprovalEntityTypes = []ApprovalEntityType{ApprovalEntityTicket, ApprovalEntityItem}

// Approval is one request to traverse a guarded transition.
//
// FromStatus and ToStatus are captured at request time rather than recomputed
// from the state rows at decision time: workflow states can be renamed, and a
// rename does not rewrite the status text on items, so a recomputed source
// status could restore a name the item never had.
type Approval struct {
	ID           uuid.UUID  `json:"id"`
	TransitionID *uuid.UUID `json:"transition_id"`

	EntityType ApprovalEntityType `json:"entity_type"`
	EntityID   uuid.UUID          `json:"entity_id"`
	SpaceID    uuid.UUID          `json:"space_id"`

	FromStateID *uuid.UUID `json:"from_state_id"`
	ToStateID   *uuid.UUID `json:"to_state_id"`
	FromStatus  string     `json:"from_status"`
	ToStatus    string     `json:"to_status"`

	RequestedBy uuid.UUID `json:"requested_by"`
	RequestedAt time.Time `json:"requested_at"`

	DecidedBy *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Decision  *Decision  `json:"decision,omitempty"`

	// RequestedByName is resolved at read time for display, never stored.
	RequestedByName string `json:"requested_by_name,omitempty"`
	// DecidedByName is resolved at read time for display, never stored.
	DecidedByName string `json:"decided_by_name,omitempty"`
}

// IsPending reports whether the request is still awaiting a decision.
func (a Approval) IsPending() bool { return a.DecidedAt == nil }

// CanDecide reports whether the actor is one of the configured approvers.
//
// Approval authority is DATA, not a capability: a person may decide because an
// administrator named them (or a team they are an effective member of) on this
// transition, not because they hold a role. That is why this phase adds no new
// Capability constant — doing so would have changed the capability model, which
// CLAUDE.md §5 makes a stop-and-raise decision, and it would have been the
// wrong model anyway. "Who approves change requests" is per-gate, not per-role.
//
// Team membership is ADR-0007 EFFECTIVE membership, supplied by the caller from
// effective_team_ids() — the same expansion space grants use — so an approver
// team and a grant to that team can never disagree about who is in it.
//
// An empty approver list returns false. A transition configured to need approval
// but naming nobody is unsatisfiable rather than open to everyone: the
// alternative would turn a misconfiguration into an unguarded edge, which is the
// silent permit this tier exists to prevent.
func CanDecide(approvers []Approver, actor Actor) bool {
	for _, ap := range approvers {
		switch ap.SubjectType {
		case ApproverUser:
			if ap.SubjectID == actor.UserID {
				return true
			}
		case ApproverTeam:
			if _, ok := actor.TeamIDs[ap.SubjectID]; ok {
				return true
			}
		default:
			// An unrecognised subject type cannot match anybody. Same
			// fail-closed direction as an unknown guard kind: a subject this
			// build cannot resolve has not been shown to include the actor.
			continue
		}
	}
	return false
}

// RequiresApproval reports whether a transition is gated.
func RequiresApproval(approvers []Approver) bool { return len(approvers) > 0 }

// ─── Write-side validation ────────────────────────────────────────────────────

// ValidateApprover refuses any approver subject kind this build does not name.
//
// It does NOT check that the subject exists — that is the store layer's job,
// exactly as it is for space_grants, whose header records the same division:
// the polymorphic id carries no foreign key, so the layer that can see both
// tables owns the integrity.
func ValidateApprover(a Approver) error {
	if !knownApproverSubjectType(a.SubjectType) {
		return fmt.Errorf("unknown approver subject type %q: must be one of %s",
			a.SubjectType, joinApproverTypes(allApproverSubjectTypes))
	}
	if a.SubjectID == uuid.Nil {
		return fmt.Errorf("approver subject id is required")
	}
	return nil
}

// ParseDecision converts a wire decision into a Decision. It is the one
// permitted string→Decision boundary; an unrecognised value is an error, never
// silently one of the two — the same rule access.ParseRole states for roles.
func ParseDecision(s string) (Decision, error) {
	for _, d := range allDecisions {
		if string(d) == s {
			return d, nil
		}
	}
	return "", fmt.Errorf("unknown decision %q: must be one of %s", s, joinDecisions(allDecisions))
}

// ParseApprovalEntityType is the equivalent boundary for the entity
// discriminator.
func ParseApprovalEntityType(s string) (ApprovalEntityType, error) {
	for _, e := range allApprovalEntityTypes {
		if string(e) == s {
			return e, nil
		}
	}
	return "", fmt.Errorf("unknown approval entity type %q: must be one of %s",
		s, joinEntityTypes(allApprovalEntityTypes))
}

func knownApproverSubjectType(t ApproverSubjectType) bool {
	for _, v := range allApproverSubjectTypes {
		if v == t {
			return true
		}
	}
	return false
}

func joinApproverTypes(vs []ApproverSubjectType) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinDecisions(vs []Decision) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}

func joinEntityTypes(vs []ApprovalEntityType) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return joinQuoted(out)
}
