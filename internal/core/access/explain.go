package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Space visibility values (spec §4 migration 023).
const (
	VisibilityHidden       = "hidden"
	VisibilityDiscoverable = "discoverable"
	VisibilityOrg          = "org"
)

// MatchedGrant is one link in the effective-access chain: a grant that
// reaches the target user, annotated with how it reached them.
type MatchedGrant struct {
	GrantID     uuid.UUID `json:"grant_id"`
	SubjectType string    `json:"subject_type"`
	SubjectID   uuid.UUID `json:"subject_id"`
	Role        string    `json:"role"`
	// TeamName is the granted team's name (team grants only).
	TeamName string `json:"team_name,omitempty"`
	// MatchedTeamID / MatchedTeamName identify which of the user's direct
	// teams the grant reached them through: the granted team itself when the
	// user is a direct member, otherwise the ancestor of the granted team
	// the user belongs to (subject-side expansion, ADR-0007).
	MatchedTeamID   *uuid.UUID `json:"matched_team_id,omitempty"`
	MatchedTeamName string     `json:"matched_team_name,omitempty"`
	// Depth is the tree distance from the matched direct team down to the
	// granted team: 0 for direct membership, 1 for a child team, and so on.
	Depth int `json:"depth"`
}

// Explanation answers "why can this person see this space?" — the grant
// chain, not merely the resulting role (spec §6).
type Explanation struct {
	UserID  uuid.UUID `json:"user_id"`
	SpaceID uuid.UUID `json:"space_id"`
	Access  bool      `json:"access"`
	// Role is the effective role's wire form, "" when no access.
	Role string `json:"role"`
	// OrgAdmin marks the middleware bypass — full access, zero grant rows.
	OrgAdmin bool `json:"org_admin"`
	// OrgVisibility is true when the space's visibility = 'org' contributes
	// implicit viewer access.
	OrgVisibility bool           `json:"org_visibility"`
	Grants        []MatchedGrant `json:"grants"`
}

// RawMatch is a matching grant row from the store, pre-annotation.
type RawMatch struct {
	GrantID     uuid.UUID
	SubjectType string
	SubjectID   uuid.UUID
	Role        string
	TeamName    string
	TeamPath    []uuid.UUID
}

// TeamRef names a direct team of the target user.
type TeamRef struct {
	ID   uuid.UUID
	Name string
}

// ExplainStore is the persistence contract for effective-access.
type ExplainStore interface {
	// ListMatchingGrants returns grants on the space reaching the user,
	// each with the granted team's path for chain annotation.
	ListMatchingGrants(ctx context.Context, orgID, spaceID, userID uuid.UUID) ([]RawMatch, error)
	// ListUserDirectTeams returns the user's direct teams in the org.
	ListUserDirectTeams(ctx context.Context, orgID, userID uuid.UUID) ([]TeamRef, error)
	// SpaceVisibility returns the space's visibility value.
	SpaceVisibility(ctx context.Context, spaceID uuid.UUID) (string, error)
}

// Explainer computes effective-access explanations.
type Explainer struct {
	store    ExplainStore
	resStore Store
}

// NewExplainer creates an Explainer. resStore supplies the org-role lookup
// so the explanation includes the admin bypass.
func NewExplainer(store ExplainStore, resStore Store) *Explainer {
	return &Explainer{store: store, resStore: resStore}
}

// Explain builds the grant chain for the target user on the space.
func (e *Explainer) Explain(ctx context.Context, orgID, spaceID, targetUser uuid.UUID) (*Explanation, error) {
	out := &Explanation{UserID: targetUser, SpaceID: spaceID, Grants: []MatchedGrant{}}

	orgRole, err := e.resStore.OrgRole(ctx, orgID, targetUser)
	switch {
	case err == nil && orgRole.Admin:
		out.Access = true
		out.OrgAdmin = true
		out.Role = RoleSpaceAdmin.String()
		return out, nil
	case errors.Is(err, ErrNotOrgMember):
		// Non-members hold no access by definition; return the empty chain.
		return out, nil
	case err != nil:
		return nil, fmt.Errorf("resolving target org role: %w", err)
	}

	visibility, err := e.store.SpaceVisibility(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("resolving space visibility: %w", err)
	}

	direct, err := e.store.ListUserDirectTeams(ctx, orgID, targetUser)
	if err != nil {
		return nil, fmt.Errorf("listing direct teams: %w", err)
	}
	directByID := make(map[uuid.UUID]TeamRef, len(direct))
	for _, t := range direct {
		directByID[t.ID] = t
	}

	matches, err := e.store.ListMatchingGrants(ctx, orgID, spaceID, targetUser)
	if err != nil {
		return nil, fmt.Errorf("listing matching grants: %w", err)
	}

	best := RoleNone
	for _, m := range matches {
		role, err := ParseRole(m.Role)
		if err != nil {
			continue // fail closed on unparseable rows
		}
		mg := MatchedGrant{
			GrantID:     m.GrantID,
			SubjectType: m.SubjectType,
			SubjectID:   m.SubjectID,
			Role:        role.String(),
			TeamName:    m.TeamName,
		}
		if m.SubjectType == string(SubjectTeam) {
			// The matched direct team is the deepest ancestor (or the team
			// itself) on the granted team's path that the user belongs to;
			// depth is the remaining distance down to the granted team.
			for i := len(m.TeamPath) - 1; i >= 0; i-- {
				if ref, ok := directByID[m.TeamPath[i]]; ok {
					id := ref.ID
					mg.MatchedTeamID = &id
					mg.MatchedTeamName = ref.Name
					mg.Depth = len(m.TeamPath) - 1 - i
					break
				}
			}
		}
		out.Grants = append(out.Grants, mg)
		if role > best {
			best = role
		}
	}

	if visibility == VisibilityOrg && best < RoleViewer {
		best = RoleViewer
		out.OrgVisibility = true
	}

	out.Access = best > RoleNone
	out.Role = best.String()
	return out, nil
}
