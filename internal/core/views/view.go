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

	// SpaceID and Position are the queue binding (migration 039). Both nil on
	// an ordinary saved view; both set on a queue, together with
	// VisibilitySpace. The database ties the three together so a half-queue
	// cannot exist.
	SpaceID  *uuid.UUID
	Position *int32

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

// ValidationError is anything the caller can fix by changing their request:
// a name too long, a visibility that is not one of the three, a gadget
// configuration outside its vocabulary.
//
// A TYPE rather than another sentinel, because these messages are written to
// be read — they name the bound they exceeded — and a sentinel would either
// have to prefix them ("validation error: a view name may be…") or force a
// new sentinel per bound. The API layer matches it with errors.As and returns
// 422 with the message unchanged.
//
// It exists because the alternative was live: before P5 a saved view with a
// 200-character name answered 500, because Draft.validate returned a bare
// fmt.Errorf that the handler's switch had no case for. Every user-fixable
// error in this package and in internal/core/dashboards now carries the type.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

// Invalid builds a ValidationError.
func Invalid(format string, a ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, a...)}
}

// Store is the persistence seam for the view rows themselves.
type Store interface {
	Create(ctx context.Context, v View) (View, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (View, error)
	Update(ctx context.Context, v View) (View, error)
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error)
	// ListForViewer returns every view the caller may see: their own, org
	// audience, and team audience matching effectiveTeamIDs.
	ListForViewer(ctx context.Context, orgID, viewerID uuid.UUID, effectiveTeamIDs []uuid.UUID) ([]View, error)
	// GetMany returns every LIVE view among ids, with no audience filtering.
	//
	// Audience-blind on purpose. Its caller is the dashboard loader, which has
	// to tell "this gadget's view was deleted" from "this gadget's view is not
	// yours to see" — two different tiles under ADR-0009 — and a query that
	// filtered by audience would collapse both into "absent". The audience is
	// then applied in Go by Audience.Reaches, which is the same rule the SQL
	// would have applied. One query for a whole dashboard, so a dashboard's
	// gadget count never becomes a query count (spec §2.5 case 23).
	GetMany(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]View, error)
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
	store      Store
	results    ResultStore
	aggregates AggregateStore
}

// NewService creates a Service.
//
// aggregates is the P5 grouped fan-out. It is a constructor parameter rather
// than a With* option because a nil one would make every count gadget answer
// "feature disabled" in silence — the dark-harness failure mode CLAUDE.md §2
// describes. One adapter satisfies all three seams.
func NewService(store Store, results ResultStore, aggregates AggregateStore) *Service {
	return &Service{store: store, results: results, aggregates: aggregates}
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

// Audience is the view's (visibility, team) pair. The rule it encodes is
// shared with dashboards (migration 048) and lives in audience.go so there is
// one implementation of it.
func (v *View) Audience() Audience {
	return Audience{Visibility: v.Visibility, TeamID: v.VisibilityTeamID}
}

// CanEdit reports who may change or delete a view: the owner, with the
// org-admin bypass. See Audience.OwnedBy for why this is ownership rather than
// a capability.
func (v *View) CanEdit(a Actor) bool { return v.Audience().OwnedBy(v.OwnerID, a) }

// CanSee reports whether the view's definition reaches this caller.
func (v *View) CanSee(a Actor) bool { return v.Audience().Reaches(v.OwnerID, a) }

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
		return Invalid("a view name may be at most %d characters", MaxNameLen)
	}
	if len([]rune(d.Description)) > MaxDescLen {
		return Invalid("a view description may be at most %d characters", MaxDescLen)
	}
	// The write-path half of migration 038's deliberately absent CHECK: the
	// database must be able to REPRESENT a team view whose team is gone,
	// because that is C1's degraded state, but no write may reach it.
	aud, err := Audience{Visibility: d.Visibility, TeamID: d.VisibilityTeamID}.Normalise(a)
	if err != nil {
		return err
	}
	d.Visibility, d.VisibilityTeamID = aud.Visibility, aud.TeamID
	return d.Query.Validate()
}

// ActorFor assembles the calling user's Actor, expanding their teams once per
// request. Handlers call this rather than reaching for the store themselves,
// so the team expansion has exactly one entry point.
func (s *Service) ActorFor(ctx context.Context, orgID, userID uuid.UUID, isOrgAdmin bool) (Actor, error) {
	teams, err := s.store.EffectiveTeamIDs(ctx, orgID, userID)
	if err != nil {
		return Actor{}, fmt.Errorf("resolving your teams: %w", err)
	}
	return Actor{UserID: userID, EffectiveTeamIDs: teams, IsOrgAdmin: isOrgAdmin}, nil
}

// Create saves a new view owned by the actor.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, a Actor, d Draft) (View, error) {
	if d.Visibility == "" {
		d.Visibility = VisibilityPrivate
	}
	if err := d.validate(a); err != nil {
		return View{}, err
	}
	v, err := s.store.Create(ctx, View{
		OrgID: orgID, OwnerID: a.UserID,
		Name: d.Name, Description: d.Description, Query: d.Query,
		Visibility: d.Visibility, VisibilityTeamID: d.VisibilityTeamID,
	})
	if err != nil {
		return View{}, fmt.Errorf("saving the view: %w", err)
	}
	return v, nil
}

// Update replaces a view's mutable surface. Owner only (org admin bypasses).
func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, a Actor, d Draft) (View, error) {
	existing, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return View{}, fmt.Errorf("loading the view to update: %w", err)
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
	// PATCH is a merge: a field the request does not carry keeps the value the
	// row already holds. Visibility inherits so that renaming a view cannot
	// un-share it as a side effect — and the team it is shared WITH has to
	// inherit alongside it, or the one visibility that carries a payload is the
	// one the inheritance cannot express. Inheriting half the pair produced a
	// merged draft of "team" with no team, which Normalise refuses: a caller
	// who sent only a new name was answered "a team-visible view must name a
	// team", about a field they did not send (known-issues #25).
	//
	// The team inherits only while the visibility is UNCHANGED. A request that
	// moves the view to another audience is stating the pair, and carrying a
	// stale team into an explicit change would be the merge overruling what the
	// caller said. So org and private still drop the team id (Normalise does
	// it), and a move TO team that names no team is still refused.
	if d.Visibility == "" {
		d.Visibility = existing.Visibility
	}
	if d.VisibilityTeamID == nil && d.Visibility == existing.Visibility {
		d.VisibilityTeamID = existing.VisibilityTeamID
	}
	if err := d.validate(a); err != nil {
		return View{}, err
	}
	existing.Name = d.Name
	existing.Description = d.Description
	existing.Query = d.Query
	existing.Visibility = d.Visibility
	existing.VisibilityTeamID = d.VisibilityTeamID
	updated, err := s.store.Update(ctx, existing)
	if err != nil {
		return View{}, fmt.Errorf("updating the view: %w", err)
	}
	return updated, nil
}

// Delete soft-deletes a view. Owner only (org admin bypasses).
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID, a Actor) error {
	existing, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return fmt.Errorf("loading the view to delete: %w", err)
	}
	if !existing.CanEdit(a) {
		if !existing.CanSee(a) {
			return ErrNotFound
		}
		return ErrNotOwner
	}
	n, err := s.store.SoftDelete(ctx, orgID, id)
	if err != nil {
		return fmt.Errorf("deleting the view: %w", err)
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
		return View{}, fmt.Errorf("loading the view: %w", err)
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
		return nil, fmt.Errorf("listing views: %w", err)
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
//
//nolint:cyclop // one linear pass per degradation rule; splitting it would scatter ADR-0009 case C1 across three functions
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
		return Page{}, fmt.Errorf("loading the view to run: %w", err)
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

// AggregateQuery counts an unsaved query's results for one viewer, optionally
// grouped. It is the aggregate twin of Preview and runs the identical
// per-viewer access union.
func (s *Service) AggregateQuery(ctx context.Context, orgID uuid.UUID, q Query, v Viewer, group GroupField) (AggregateResult, error) {
	res, err := Aggregate(ctx, orgScopedAggregates{inner: s.aggregates, orgID: orgID}, q, v, group)
	if err != nil {
		return AggregateResult{}, err
	}
	return res, nil
}

// ByIDs returns the live views among ids, each already marked valid or not,
// WITHOUT applying any audience filter. See Store.GetMany for why.
//
// Two queries regardless of how many ids are asked for, which is what keeps a
// dashboard's gadget count from becoming a query count.
func (s *Service) ByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]View, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.store.GetMany(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("loading the referenced views: %w", err)
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
	rows, err := o.inner.ListTickets(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("beacon fan-out: %w", err)
	}
	return rows, nil
}

func (o orgScopedStore) ListProjectItems(ctx context.Context, p FanoutParams) ([]Result, error) {
	p.OrgID = o.orgID
	rows, err := o.inner.ListProjectItems(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("vector fan-out: %w", err)
	}
	return rows, nil
}

// orgScopedAggregates is orgScopedStore's twin for the grouped fan-outs, and
// exists for the same reason: Aggregate is org-agnostic so it can be
// unit-tested against a fake store, and exactly one place stamps the org id.
type orgScopedAggregates struct {
	inner AggregateStore
	orgID uuid.UUID
}

func (o orgScopedAggregates) CountTickets(ctx context.Context, p FanoutParams) (int64, error) {
	p.OrgID = o.orgID
	n, err := o.inner.CountTickets(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("beacon count: %w", err)
	}
	return n, nil
}

func (o orgScopedAggregates) CountProjectItems(ctx context.Context, p FanoutParams) (int64, error) {
	p.OrgID = o.orgID
	n, err := o.inner.CountProjectItems(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("vector count: %w", err)
	}
	return n, nil
}

func (o orgScopedAggregates) BreakdownTickets(ctx context.Context, p FanoutParams) ([]Bucket, error) {
	p.OrgID = o.orgID
	rows, err := o.inner.BreakdownTickets(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("beacon breakdown: %w", err)
	}
	return rows, nil
}

func (o orgScopedAggregates) BreakdownProjectItems(ctx context.Context, p FanoutParams) ([]Bucket, error) {
	p.OrgID = o.orgID
	rows, err := o.inner.BreakdownProjectItems(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("vector breakdown: %w", err)
	}
	return rows, nil
}
