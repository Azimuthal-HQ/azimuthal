package dashboards

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// Bounds on a dashboard's own fields.
const (
	MaxNameLen = 120
	MaxDescLen = 500
)

// Dashboard is one dashboard row plus what is derived from it.
//
// It owns LAYOUT AND NOTHING ELSE (ADR-0009 decision 3). There is no query
// here and no result: a gadget names a saved view, and the view is resolved
// against the VIEWER on every read.
type Dashboard struct {
	ID          uuid.UUID
	OrgID       uuid.UUID
	OwnerID     uuid.UUID
	Name        string
	Description string
	Module      Module
	IsDefault   bool
	// IsSeeded records that this row came from the starter layout rather than
	// from a person. It is what makes seeding run exactly once.
	IsSeeded         bool
	Visibility       views.Visibility
	VisibilityTeamID *uuid.UUID
	CreatedAt        time.Time
	UpdatedAt        time.Time

	// Derived, read-only, never stored.
	OwnerName string
	TeamName  string
	// InvalidReason is empty when the dashboard's audience still resolves.
	// When it is not, the dashboard still lists and still opens — it renders
	// "scope unavailable" and prompts its owner to re-scope, exactly as an
	// invalid saved view does (ADR-0009 case C1). It never errors.
	InvalidReason string
}

// IsValid reports whether the dashboard's audience still resolves.
func (d *Dashboard) IsValid() bool { return d.InvalidReason == "" }

// Audience is the dashboard's (visibility, team) pair. The rule it encodes is
// shared with saved views and lives in internal/core/views/audience.go.
func (d *Dashboard) Audience() views.Audience {
	return views.Audience{Visibility: d.Visibility, TeamID: d.VisibilityTeamID}
}

// CanEdit reports who may change or delete a dashboard: its owner, with the
// org-admin bypass. Ownership, not a capability — see views.Audience.OwnedBy.
func (d *Dashboard) CanEdit(a views.Actor) bool { return d.Audience().OwnedBy(d.OwnerID, a) }

// CanSee reports whether the dashboard's definition reaches this caller.
func (d *Dashboard) CanSee(a views.Actor) bool { return d.Audience().Reaches(d.OwnerID, a) }

// markValidity fills InvalidReason.
//
// One rule, and no query: a dashboard's only scope is its audience, and the
// only way that can degrade is the team being deleted, which migration 048
// records by nulling the column rather than cascading the row away. Saved
// views need a query here because they also name spaces; dashboards name none.
func (d *Dashboard) markValidity() {
	d.InvalidReason = ""
	if d.Visibility == views.VisibilityTeam && d.VisibilityTeamID == nil {
		d.InvalidReason = "the team this dashboard was shared with no longer exists"
	}
}

// Gadget is one stored tile.
type Gadget struct {
	ID          uuid.UUID
	DashboardID uuid.UUID
	// Key is a plain string, not a GadgetKey, on purpose. A row written by a
	// build that knew a gadget kind this one does not must still LOAD —
	// decision log C5 — so the type has to admit values the registry does not
	// define. Writes go through a GadgetKey and are refused unless registered.
	Key         string
	Position    int32
	ColSpan     int32
	SavedViewID *uuid.UUID
	// Config is the parsed document for a known key. For an unknown key it is
	// the zero value: the tile is inert, so there is nothing to configure.
	Config Config
}

// GadgetState is what the client should render for a tile. It is the wire form
// of ADR-0009's four mandatory degradation rules plus the ordinary case, and
// it is computed SERVER-SIDE so that no client has to re-derive an audience
// rule to decide whether to draw a tile.
type GadgetState string

// The five states. Every one but StateReady is a tile that renders content
// rather than an error — a dashboard always loads.
const (
	// StateReady means the gadget has everything it needs.
	StateReady GadgetState = "ready"
	// StateUnknownGadget is decision log C5: a stored key this build does not
	// define. An inert labelled placeholder, never a crash.
	StateUnknownGadget GadgetState = "unknown_gadget"
	// StateViewRequired is ADR-0009's fourth degradation rule: the gadget
	// needs a saved view and has none, because the view was deleted (migration
	// 048 nulls the column) or was never chosen. A recoverable empty state
	// offering to pick another view.
	StateViewRequired GadgetState = "view_required"
	// StateViewUnreadable is decision log C2: the gadget names a view whose
	// DEFINITION this viewer may not see. "Not available to you", and the
	// dashboard still loads.
	//
	// Note what this is NOT: a viewer who can see the definition but can read
	// fewer of its rows is not in this state at all. They get StateReady and
	// fewer results, which is correct behaviour and never a sync failure.
	StateViewUnreadable GadgetState = "view_unreadable"
	// StateScopeUnavailable is ADR-0009 case C1 reaching a gadget: the view is
	// visible but every space it names is gone. Renders the same "scope
	// unavailable" prompt /views does.
	StateScopeUnavailable GadgetState = "scope_unavailable"
)

// ResolvedGadget is a gadget as one viewer sees it.
type ResolvedGadget struct {
	Gadget
	State GadgetState
	// Title is what the tile should be headed: the config override, else the
	// saved view's name, else the gadget kind's own name. Resolved here so
	// renaming a view renames every untitled gadget that shows it.
	Title string
	// Render is the definition's render mode, or empty for an unknown key.
	Render RenderMode
	// Query is the document the client resolves for its data, present only in
	// StateReady and only for gadgets that have one at all. It comes from the
	// referenced saved view, or from the registry for a built-in.
	//
	// It is handed out rather than resolved here so that gadget data travels
	// the IDENTICAL path /views/preview and the aggregate endpoint already
	// take — one resolution path, per viewer, with the ADR-0008 share union.
	// A second server-side results path per gadget is the drift
	// docs/design/shared-surfaces.md exists to prevent. A viewer editing the
	// document they were handed changes only what they themselves see, which
	// is what typing into the filter builder already does.
	Query *views.Query
	// ViewName and InvalidReason carry the referenced view's own state so a
	// tile can say which view it is showing and why it cannot.
	ViewName      string
	InvalidReason string
}

// Detail is a dashboard together with its gadgets, resolved for one viewer.
type Detail struct {
	Dashboard
	Gadgets []ResolvedGadget
}

// Errors the service returns. The API layer maps them to status codes.
var (
	ErrNotFound       = errors.New("dashboard not found")
	ErrNotOwner       = errors.New("only the owner may change this dashboard")
	ErrNameRequired   = errors.New("a dashboard needs a name")
	ErrTooManyGadgets = fmt.Errorf("a dashboard may hold at most %d gadgets", MaxGadgets)
	ErrViewRequired   = errors.New("this gadget kind needs a saved view")
	ErrViewNotAllowed = errors.New("this gadget kind takes no saved view")
	ErrViewNotVisible = errors.New("that saved view is not one you can see")
	ErrSpanInvalid    = errors.New("a gadget may span 1, 2 or 4 columns")
	ErrModuleInvalid  = errors.New("a dashboard module must be home, beacon or vector")
	ErrGadgetModule   = errors.New("that gadget cannot sit on a dashboard of this module")
)

// Store is the persistence seam.
type Store interface {
	Create(ctx context.Context, d Dashboard) (Dashboard, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (Dashboard, error)
	Update(ctx context.Context, d Dashboard) (Dashboard, error)
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error)
	// ListForViewer returns every dashboard the caller may see: their own, org
	// audience, and team audience matching effectiveTeamIDs. An empty module
	// means every module.
	ListForViewer(ctx context.Context, orgID, viewerID uuid.UUID, effectiveTeamIDs []uuid.UUID, module string) ([]Dashboard, error)
	ListGadgets(ctx context.Context, dashboardID uuid.UUID) ([]Gadget, error)
	// ReplaceGadgets writes the WHOLE collection in ONE transaction, per spec
	// §6: "gadget layout saves as a whole collection, never per gadget, to
	// avoid partial states." Delete-then-insert, so positions are dense and
	// unique by construction and no permutation check is needed.
	ReplaceGadgets(ctx context.Context, dashboardID uuid.UUID, gadgets []Gadget) ([]Gadget, error)
	// DefaultFor returns the caller's default dashboard for a module, or
	// ErrNotFound.
	DefaultFor(ctx context.Context, orgID, ownerID uuid.UUID, module string) (Dashboard, error)
	// CreateStarter inserts a seeded default dashboard and its gadgets in one
	// transaction, doing nothing if the owner already has a default for that
	// module. Idempotence is the dashboards_one_default index, not a
	// check-then-insert — two tabs opening Home at once must not both seed.
	CreateStarter(ctx context.Context, d Dashboard, gadgets []Gadget) (bool, error)
}

// ViewLookup is how a dashboard learns about the saved views its gadgets name.
//
// ONE CALL FOR A WHOLE DASHBOARD. A dashboard renders N gadgets; resolving
// each one's view separately is precisely the per-item authorisation shape
// spec §2.5 case 23 forbids and TestMatrixAPI23 traces. The seam is therefore
// plural, and the audience decision is made in Go from the returned rows.
type ViewLookup interface {
	ByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]views.View, error)
}

// Service owns the dashboard lifecycle.
type Service struct {
	store Store
	views ViewLookup
}

// NewService creates a Service.
func NewService(store Store, viewLookup ViewLookup) *Service {
	return &Service{store: store, views: viewLookup}
}

// Draft is a create or update request, already decoded but not yet validated.
type Draft struct {
	Name             string
	Description      string
	Module           Module
	Visibility       views.Visibility
	VisibilityTeamID *uuid.UUID
	// IsDefault is a tri-state: nil leaves the flag alone on update and means
	// false on create. A bare bool would make every PATCH that omits it clear
	// somebody's default, which is the partial-PATCH defect that silently
	// wiped every item's due_at.
	IsDefault *bool
}

func (d *Draft) validate(a views.Actor) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return ErrNameRequired
	}
	if len([]rune(d.Name)) > MaxNameLen {
		return invalid("a dashboard name may be at most %d characters", MaxNameLen)
	}
	if len([]rune(d.Description)) > MaxDescLen {
		return invalid("a dashboard description may be at most %d characters", MaxDescLen)
	}
	if !ValidModule(d.Module) {
		return ErrModuleInvalid
	}
	// The write-path half of migration 048's deliberately absent CHECK: the
	// (team, NULL) state must be representable but never reachable by a write.
	aud, err := views.Audience{Visibility: d.Visibility, TeamID: d.VisibilityTeamID}.Normalise(a)
	if err != nil {
		// Passed through unwrapped: ErrTeamRequired and ErrTeamNotMember are
		// matched by errors.Is one layer up, and a wrapper would only prefix a
		// message already written for the person reading it.
		return err //nolint:wrapcheck // sentinel matched by errors.Is in the API layer
	}
	d.Visibility, d.VisibilityTeamID = aud.Visibility, aud.TeamID
	return nil
}

// Create saves a new dashboard owned by the actor.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, a views.Actor, d Draft) (Dashboard, error) {
	if d.Visibility == "" {
		d.Visibility = views.VisibilityPrivate
	}
	if d.Module == "" {
		d.Module = ModuleHome
	}
	if err := d.validate(a); err != nil {
		return Dashboard{}, err
	}
	created, err := s.store.Create(ctx, Dashboard{
		OrgID: orgID, OwnerID: a.UserID,
		Name: d.Name, Description: d.Description, Module: d.Module,
		IsDefault:  d.IsDefault != nil && *d.IsDefault,
		Visibility: d.Visibility, VisibilityTeamID: d.VisibilityTeamID,
	})
	if err != nil {
		return Dashboard{}, fmt.Errorf("saving the dashboard: %w", err)
	}
	created.markValidity()
	return created, nil
}

// Update replaces a dashboard's mutable surface. Owner only (org admin
// bypasses).
func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, a views.Actor, d Draft) (Dashboard, error) {
	existing, err := s.load(ctx, orgID, id, a)
	if err != nil {
		return Dashboard{}, err
	}
	if !existing.CanEdit(a) {
		return Dashboard{}, ErrNotOwner
	}
	if d.Visibility == "" {
		d.Visibility = existing.Visibility
	}
	if d.Module == "" {
		d.Module = existing.Module
	}
	if err := d.validate(a); err != nil {
		return Dashboard{}, err
	}
	existing.Name = d.Name
	existing.Description = d.Description
	existing.Module = d.Module
	existing.Visibility = d.Visibility
	existing.VisibilityTeamID = d.VisibilityTeamID
	if d.IsDefault != nil {
		existing.IsDefault = *d.IsDefault
	}
	updated, err := s.store.Update(ctx, existing)
	if err != nil {
		return Dashboard{}, fmt.Errorf("updating the dashboard: %w", err)
	}
	updated.markValidity()
	return updated, nil
}

// Delete soft-deletes a dashboard. Owner only (org admin bypasses).
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID, a views.Actor) error {
	existing, err := s.load(ctx, orgID, id, a)
	if err != nil {
		return err
	}
	if !existing.CanEdit(a) {
		return ErrNotOwner
	}
	n, err := s.store.SoftDelete(ctx, orgID, id)
	if err != nil {
		return fmt.Errorf("deleting the dashboard: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns every dashboard the caller may see. An empty module means all.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, a views.Actor, module Module) ([]Dashboard, error) {
	if module != "" && !ValidModule(module) {
		return nil, ErrModuleInvalid
	}
	rows, err := s.store.ListForViewer(ctx, orgID, a.UserID, a.EffectiveTeamIDs, string(module))
	if err != nil {
		return nil, fmt.Errorf("listing dashboards: %w", err)
	}
	for i := range rows {
		rows[i].markValidity()
	}
	return rows, nil
}

// Get returns one dashboard with its gadgets, resolved for this viewer.
func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID, a views.Actor) (Detail, error) {
	d, err := s.load(ctx, orgID, id, a)
	if err != nil {
		return Detail{}, err
	}
	return s.detail(ctx, orgID, d, a)
}

// load fetches a dashboard the caller may SEE, collapsing "not visible" into
// "not found" so the endpoint does not confirm that somebody else's private
// dashboard exists.
func (s *Service) load(ctx context.Context, orgID, id uuid.UUID, a views.Actor) (Dashboard, error) {
	d, err := s.store.Get(ctx, orgID, id)
	if err != nil {
		return Dashboard{}, fmt.Errorf("loading the dashboard: %w", err)
	}
	if !d.CanSee(a) {
		return Dashboard{}, ErrNotFound
	}
	d.markValidity()
	return d, nil
}

func (s *Service) detail(ctx context.Context, orgID uuid.UUID, d Dashboard, a views.Actor) (Detail, error) {
	gadgets, err := s.store.ListGadgets(ctx, d.ID)
	if err != nil {
		return Detail{}, fmt.Errorf("loading the dashboard's gadgets: %w", err)
	}
	resolved, err := s.resolveGadgets(ctx, orgID, gadgets, a)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Dashboard: d, Gadgets: resolved}, nil
}

// resolveGadgets turns stored rows into what one viewer should see.
//
// ONE view lookup for the whole dashboard, whatever the gadget count. The
// audience decision is then made per gadget in memory from the same
// views.Audience rule the saved-view endpoints apply, so "can I see this
// view's definition" has one answer site rather than two.
func (s *Service) resolveGadgets(ctx context.Context, orgID uuid.UUID, gadgets []Gadget, a views.Actor) ([]ResolvedGadget, error) {
	live, err := s.lookupViews(ctx, orgID, gadgets)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedGadget, 0, len(gadgets))
	for _, g := range gadgets {
		out = append(out, resolveGadget(g, live, a))
	}
	return out, nil
}

// lookupViews fetches every distinct saved view the gadgets name, in one call.
func (s *Service) lookupViews(ctx context.Context, orgID uuid.UUID, gadgets []Gadget) (map[uuid.UUID]views.View, error) {
	ids := make([]uuid.UUID, 0, len(gadgets))
	seen := map[uuid.UUID]struct{}{}
	for _, g := range gadgets {
		if g.SavedViewID == nil {
			continue
		}
		if _, dup := seen[*g.SavedViewID]; dup {
			continue
		}
		seen[*g.SavedViewID] = struct{}{}
		ids = append(ids, *g.SavedViewID)
	}
	live := map[uuid.UUID]views.View{}
	if len(ids) == 0 {
		return live, nil
	}
	rows, err := s.views.ByIDs(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("loading the views this dashboard shows: %w", err)
	}
	for _, v := range rows {
		live[v.ID] = v
	}
	return live, nil
}

// resolveGadget decides what one tile should render for one viewer. Every
// branch is an ADR-0009 degradation rule, and they are together because they
// are only checkable together — one of them missing is a tile that renders
// nothing with no way to tell why.
//
//nolint:cyclop // one linear branch per degradation rule; splitting it scatters the rule set
func resolveGadget(g Gadget, live map[uuid.UUID]views.View, a views.Actor) ResolvedGadget {
	r := ResolvedGadget{Gadget: g}
	def, known := Lookup(GadgetKey(g.Key))
	if !known {
		// C5. Nothing else is computed: an inert tile needs a label and a
		// slot, and reading a config this build cannot interpret would be
		// guessing at what an unknown gadget meant.
		r.State = StateUnknownGadget
		r.Title = g.Config.Title
		return r
	}
	r.Render = def.Render
	r.Title = g.Config.Title
	if r.Title == "" {
		r.Title = def.Name
	}

	switch {
	case !def.RequiresSavedView:
		r.State = StateReady
		if def.Query != nil {
			q := def.Query()
			r.Query = &q
		}
	case g.SavedViewID == nil:
		// The fourth degradation rule. Migration 048 nulls the column when a
		// view is hard-deleted, so this is also how a deleted view arrives
		// here.
		r.State = StateViewRequired
	default:
		v, ok := live[*g.SavedViewID]
		switch {
		case !ok:
			// The row is gone (soft-deleted). Same tile as a null column: the
			// gadget needs a view, and offering to pick another is the
			// recoverable state ADR-0009 asks for.
			r.State = StateViewRequired
		case !v.CanSee(a):
			// C2. The dashboard still loads; this tile says so.
			r.State = StateViewUnreadable
		case !v.IsValid():
			// C1 reaching a gadget.
			r.State = StateScopeUnavailable
			r.ViewName = v.Name
			r.InvalidReason = v.InvalidReason
		default:
			r.State = StateReady
			r.ViewName = v.Name
			if g.Config.Title == "" {
				r.Title = v.Name
			}
			q := v.Query
			r.Query = &q
		}
	}
	return r
}
