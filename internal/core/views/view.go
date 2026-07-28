package views

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Visibility is who, besides the owner, may see a view's DEFINITION. It says
// nothing about results: those always resolve against the viewer's own access.
type Visibility string

// The three audiences. They mirror the share audiences of ADR-0008 minus the
// share machinery — a view is not an entity share and grants no read access to
// anything.
const (
	VisibilityPrivate Visibility = "private"
	VisibilityTeam    Visibility = "team"
	VisibilityOrg     Visibility = "org"
)

// View is a saved view.
type View struct {
	ID               uuid.UUID
	OrgID            uuid.UUID
	OwnerID          uuid.UUID
	Name             string
	Description      string
	Query            Query
	Visibility       Visibility
	VisibilityTeamID *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time

	// Derived, read-only, never stored.
	OwnerName string
	TeamName  string
	// InvalidReason is empty when the view is usable. When it is not, the view
	// still lists and still opens — ADR-0009's degradation rule (decision log
	// C1) is that it renders "scope unavailable" and prompts its owner to
	// re-scope. It never errors and it never disappears.
	InvalidReason string
}

// IsValid reports whether the view's scope still resolves.
func (v *View) IsValid() bool { return v.InvalidReason == "" }

// Errors the service returns. The API layer maps them to status codes.
var (
	ErrNotFound      = errors.New("saved view not found")
	ErrNotOwner      = errors.New("only the owner may change this view")
	ErrTeamRequired  = errors.New("a team-visible view must name a team")
	ErrTeamNotMember = errors.New("a view may only be shared with a team you belong to")
	ErrNameRequired  = errors.New("a view needs a name")
)

// Store is the persistence seam for the view rows themselves.
type Store interface {
	Create(ctx context.Context, v View) (View, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (View, error)
	Update(ctx context.Context, v View) (View, error)
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error)
	// ListForViewer returns every view the caller may see: their own, org
	// audience, and team audience matching effectiveTeamIDs.
	ListForViewer(ctx context.Context, orgID, viewerID uuid.UUID, effectiveTeamIDs []uuid.UUID) ([]View, error)
	// LiveSpaceIDs returns which of the given space ids still exist. One
	// query for a whole page of views.
	LiveSpaceIDs(ctx context.Context, orgID uuid.UUID, spaceIDs []uuid.UUID) ([]uuid.UUID, error)
	// EffectiveTeamIDs returns the caller's ADR-0007 team set — direct teams
	// plus all descendants.
	//
	// It is one query per request on the view routes only. The alternative,
	// widening the per-request access resolution to carry the team set, would
	// put the product's hottest query and its case-23 constancy tracer in the
	// blast radius of this feature. The expansion rule itself is not
	// reimplemented here: it lives in the effective_team_ids schema function
	// (migration 038), which is the same expansion ResolveAccessRows performs.
	EffectiveTeamIDs(ctx context.Context, orgID, userID uuid.UUID) ([]uuid.UUID, error)
}

// Service owns the view lifecycle.
type Service struct {
	store   Store
	results ResultStore
}

// NewService creates a Service.
func NewService(store Store, results ResultStore) *Service {
	return &Service{store: store, results: results}
}

// Actor is the calling user as the service needs to see them.
type Actor struct {
	UserID uuid.UUID
	// EffectiveTeamIDs is the subject-side expanded team set (ADR-0007). It
	// decides both which team-audience views reach the caller and which teams
	// they may share a view with.
	EffectiveTeamIDs []uuid.UUID
	// IsOrgAdmin is the middleware bypass, never a grant row.
	IsOrgAdmin bool
}

func (a Actor) inTeam(id uuid.UUID) bool {
	for _, t := range a.EffectiveTeamIDs {
		if t == id {
			return true
		}
	}
	return false
}

// CanEdit reports who may change or delete a view.
//
// OWNER SEMANTICS, NOT A CAPABILITY. Creating a private view is something any
// member may do — it reads nothing they could not already read, and gating it
// would mean a capability that every role holds. Editing is the owner's alone,
// with the org-admin bypass that applies everywhere else. If a future
// requirement wants sharing gated by a capability rather than by ownership,
// that is a change to the capability model and belongs to a maintainer, not to
// a handler.
func (v *View) CanEdit(a Actor) bool { return a.IsOrgAdmin || v.OwnerID == a.UserID }

// CanSee reports whether the view's definition reaches this caller.
func (v *View) CanSee(a Actor) bool {
	if v.CanEdit(a) {
		return true
	}
	switch v.Visibility {
	case VisibilityOrg:
		return true
	case VisibilityTeam:
		// A degraded team view (its team was deleted) matches nobody. Fail
		// closed, then prompt the owner — who still reaches it as the owner.
		return v.VisibilityTeamID != nil && a.inTeam(*v.VisibilityTeamID)
	case VisibilityPrivate:
		return false
	}
	return false
}

// Draft is a create or update request, already decoded but not yet validated.
type Draft struct {
	Name             string
	Description      string
	Query            Query
	Visibility       Visibility
	VisibilityTeamID *uuid.UUID
}

func (d *Draft) validate(a Actor) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return ErrNameRequired
	}
	if len([]rune(d.Name)) > MaxNameLen {
		return fmt.Errorf("a view name may be at most %d characters", MaxNameLen)
	}
	if len([]rune(d.Description)) > MaxDescLen {
		return fmt.Errorf("a view description may be at most %d characters", MaxDescLen)
	}
	switch d.Visibility {
	case VisibilityPrivate, VisibilityOrg:
		// Carrying a team id on a non-team view would be a lie the next
		// reader has to interpret; drop it rather than store it.
		d.VisibilityTeamID = nil
	case VisibilityTeam:
		if d.VisibilityTeamID == nil {
			return ErrTeamRequired
		}
		// The write-path half of migration 038's deliberately absent CHECK.
		// The database must be able to REPRESENT a team view whose team is
		// gone, because that is C1's degraded state; it must never be
		// reachable by a write.
		if !a.IsOrgAdmin && !a.inTeam(*d.VisibilityTeamID) {
			return ErrTeamNotMember
		}
	default:
		return fmt.Errorf("visibility %q must be %q, %q or %q",
			d.Visibility, VisibilityPrivate, VisibilityTeam, VisibilityOrg)
	}
	return d.Query.Validate()
}

// Create saves a new view owned by the actor.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, a Actor, d Draft) (View, error) {
	if d.Visibility == "" {
		d.Visibility = VisibilityPrivate
	}
	if err := d.validate(a); err != nil {
		return View{}, err
	}
	return s.store.Create(ctx, View{
		OrgID: orgID, OwnerID: a.UserID,
		Name: d.Name, Description: d.Description, Query: d.Query,
		Visibility: d.Visibility, VisibilityTeamID: d.VisibilityTeamID,
	})
}

// Update replaces a view's mutable surface. Owner only (org admin bypasses).
func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, a Actor, d Draft) (View, error) {
	existing, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return View{}, err
	}
	if !existing.CanEdit(a) {
		// A view the caller cannot even SEE must not be distinguishable from
		// one that does not exist, or the endpoint answers "does user X have a
		// view called Y" to anyone who asks.
		if !existing.CanSee(a) {
			return View{}, ErrNotFound
		}
		return View{}, ErrNotOwner
	}
	if d.Visibility == "" {
		d.Visibility = existing.Visibility
	}
	if err := d.validate(a); err != nil {
		return View{}, err
	}
	existing.Name = d.Name
	existing.Description = d.Description
	existing.Query = d.Query
	existing.Visibility = d.Visibility
	existing.VisibilityTeamID = d.VisibilityTeamID
	return s.store.Update(ctx, existing)
}

// Delete soft-deletes a view. Owner only (org admin bypasses).
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID, a Actor) error {
	existing, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return err
	}
	if !existing.CanEdit(a) {
		if !existing.CanSee(a) {
			return ErrNotFound
		}
		return ErrNotOwner
	}
	n, err := s.store.SoftDelete(ctx, orgID, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one view the caller may see.
func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID, a Actor) (View, error) {
	v, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return View{}, err
	}
	if !v.CanSee(a) {
		return View{}, ErrNotFound
	}
	if err := s.markValidity(ctx, orgID, []*View{&v}); err != nil {
		return View{}, err
	}
	return v, nil
}

// List returns every view the caller may see, each already marked valid or not.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, a Actor) ([]View, error) {
	rows, err := s.store.ListForViewer(ctx, orgID, a.UserID, a.EffectiveTeamIDs)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*View, len(rows))
	for i := range rows {
		ptrs[i] = &rows[i]
	}
	if err := s.markValidity(ctx, orgID, ptrs); err != nil {
		return nil, err
	}
	return rows, nil
}

// markValidity fills InvalidReason on a whole page of views.
//
// ADR-0009's degradation rule (case C1) in ONE query, regardless of how many
// views are checked, so the constant-query contract of §2.5 case 23 holds on
// the list endpoint. Validity is derived rather than stored: the spec's sketch
// carried an is_valid column, which needs some writer to remember to flip it,
// and a stored copy of derivable state is a stored copy that can go stale.
func (s *Service) markValidity(ctx context.Context, orgID uuid.UUID, views []*View) error {
	wanted := map[uuid.UUID]struct{}{}
	for _, v := range views {
		for _, id := range v.Query.Filter.SpaceIDs {
			wanted[id] = struct{}{}
		}
	}
	live := map[uuid.UUID]struct{}{}
	if len(wanted) > 0 {
		ids := make([]uuid.UUID, 0, len(wanted))
		for id := range wanted {
			ids = append(ids, id)
		}
		got, err := s.store.LiveSpaceIDs(ctx, orgID, ids)
		if err != nil {
			return fmt.Errorf("checking view scopes: %w", err)
		}
		for _, id := range got {
			live[id] = struct{}{}
		}
	}
	for _, v := range views {
		v.InvalidReason = ""
		if v.Visibility == VisibilityTeam && v.VisibilityTeamID == nil {
			// The team was deleted. Migration 038 nulls the column rather than
			// cascading the row away, precisely so this state can be reported.
			v.InvalidReason = "the team this view was shared with no longer exists"
			continue
		}
		named := v.Query.Filter.SpaceIDs
		if len(named) == 0 {
			continue
		}
		missing := 0
		for _, id := range named {
			if _, ok := live[id]; !ok {
				missing++
			}
		}
		// Every named space gone means the view can never return anything and
		// wants re-scoping. Some gone is a narrower view, not a broken one —
		// reporting that as invalid would cry wolf on a normal space deletion.
		if missing == len(named) {
			v.InvalidReason = "every space this view is scoped to has been deleted"
		}
	}
	return nil
}

// Results resolves one view for one viewer.
func (s *Service) Results(ctx context.Context, orgID, id uuid.UUID, a Actor, v Viewer, cursor string, limit int) (Page, error) {
	view, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return Page{}, err
	}
	if !view.CanSee(a) {
		return Page{}, ErrNotFound
	}
	return s.Preview(ctx, orgID, view.Query, v, cursor, limit)
}

// Preview resolves an unsaved query — the filter builder's live results.
// It runs the identical path Results does, so what the builder shows is what
// the saved view will return.
func (s *Service) Preview(ctx context.Context, orgID uuid.UUID, q Query, v Viewer, cursor string, limit int) (Page, error) {
	page, err := resolveWithOrg(ctx, s.results, orgID, q, v, cursor, limit)
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

// resolveWithOrg threads the org id into the fan-out parameters. Resolve
// itself is org-agnostic so it can be unit-tested against a fake store.
func resolveWithOrg(ctx context.Context, store ResultStore, orgID uuid.UUID, q Query, v Viewer, cursor string, limit int) (Page, error) {
	return Resolve(ctx, orgScopedStore{inner: store, orgID: orgID}, q, v, cursor, limit)
}

type orgScopedStore struct {
	inner ResultStore
	orgID uuid.UUID
}

func (o orgScopedStore) ListTickets(ctx context.Context, p FanoutParams) ([]Result, error) {
	p.OrgID = o.orgID
	return o.inner.ListTickets(ctx, p)
}

func (o orgScopedStore) ListProjectItems(ctx context.Context, p FanoutParams) ([]Result, error) {
	p.OrgID = o.orgID
	return o.inner.ListProjectItems(ctx, p)
}
