package views

import "github.com/google/uuid"

// Audience is the (visibility, team) pair that decides who, besides the owner,
// may see a definition. It says nothing about results.
//
// WHY IT IS A TYPE RATHER THAN A PAIR OF FIELDS ON EACH MODEL. Saved views
// (migration 038) and dashboards (migration 042) carry the identical rule:
// three audiences, an owner who always reaches their own row, an org-admin
// bypass, a subject-side expanded team set, and a degraded team row whose team
// was deleted that must match nobody. Written twice, the two copies drift, and
// the direction an authorisation rule drifts in is "one of them grants more".
// docs/design/shared-surfaces.md exists to stop exactly that, so the rule is
// named here and both models delegate to it.
//
// It is deliberately NOT a database concern. Both tables keep their own
// columns and their own indexes; this is the one place the columns are
// INTERPRETED.
type Audience struct {
	Visibility Visibility
	// TeamID is the audience team. Non-nil only for VisibilityTeam — and nil
	// even then once that team is deleted, which is the degraded state both
	// migrations leave representable on purpose.
	TeamID *uuid.UUID
}

// Reaches reports whether a definition owned by ownerID reaches this actor.
//
// The owner and the org admin always reach it; everyone else is decided by the
// audience alone. A team audience whose team is gone matches nobody: fail
// closed, then prompt the owner to re-scope (ADR-0009 case C1).
func (a Audience) Reaches(ownerID uuid.UUID, act Actor) bool {
	if a.OwnedBy(ownerID, act) {
		return true
	}
	switch a.Visibility {
	case VisibilityOrg:
		return true
	case VisibilityTeam:
		return a.TeamID != nil && act.inTeam(*a.TeamID)
	case VisibilityPrivate, VisibilitySpace:
		// A space audience is never resolved here: it is enforced by the
		// space-read guard on the route that serves it (migration 039), and
		// nothing outside that route may widen it.
		return false
	}
	return false
}

// OwnedBy reports who may CHANGE a definition: its owner, plus the org-admin
// bypass that applies everywhere else in the product.
//
// OWNER SEMANTICS, NOT A CAPABILITY. Keeping a private saved view or a private
// dashboard reads nothing the caller could not already read, so gating it
// would mean a capability every role holds. If a future requirement wants
// sharing gated by a capability rather than by ownership, that is a change to
// the capability model and belongs to a maintainer, not to a handler.
func (a Audience) OwnedBy(ownerID uuid.UUID, act Actor) bool {
	return act.IsOrgAdmin || ownerID == act.UserID
}

// Normalise validates an audience an actor is asking to write, and returns the
// form that should be stored.
//
// Two rules, both of them the write-path half of a constraint the schema
// deliberately does not carry:
//
//   - a team audience must name a team the actor belongs to, so the
//     (team, NULL) degraded state is reachable only by deleting a team and
//     never by a write;
//   - a non-team audience drops any team id it was sent, because carrying one
//     would be a lie the next reader has to interpret.
func (a Audience) Normalise(act Actor) (Audience, error) {
	switch a.Visibility {
	case VisibilityPrivate, VisibilityOrg:
		a.TeamID = nil
		return a, nil
	case VisibilityTeam:
		if a.TeamID == nil {
			return Audience{}, ErrTeamRequired
		}
		if !act.IsOrgAdmin && !act.inTeam(*a.TeamID) {
			return Audience{}, ErrTeamNotMember
		}
		return a, nil
	default:
		return Audience{}, Invalid("visibility %q must be %q, %q or %q",
			a.Visibility, VisibilityPrivate, VisibilityTeam, VisibilityOrg)
	}
}
