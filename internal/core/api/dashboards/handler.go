// Package dashboards provides HTTP handlers for dashboards and gadgets (P5,
// ADR-0009, spec §6) — composable grids whose data always comes from the
// saved-view layer.
//
// ONE ORG-SCOPED ROUTE FAMILY, for ADR-0010's reason and P4's: a dashboard
// arranges gadgets that cross containers, so there is no {spaceID} to hang it
// off. Every route is org-member. Who may see or change one is decided by the
// dashboard's own ownership and visibility, exactly as it is for a saved view,
// and by the same code (views.Audience).
//
// NO SHARE RESOLVER ON THIS FAMILY. Not one route here reads a ticket or an
// item: the dashboard response hands the client the QUERY each gadget should
// run, and the client resolves it through /views/preview and /views/aggregate,
// which carry ResolveShares themselves. Mounting it here would make every
// dashboard read pay for a share query it never uses, which is the per-family
// mounting spec §5 and the case-23 budget forbid.
//
// NO AUDIT EVENTS, on P4's reasoning restated: spec §6's audit list is teams,
// memberships, grants, space visibility, owner team and shares — every one of
// them an access change. A dashboard grants nothing to anybody. Sharing one
// shares an ARRANGEMENT, and each gadget still resolves against the viewer's
// own access. Audit rows here would record activity, not authority, and the
// append-only log is for authority.
//
// NO NEW CAPABILITY. access.CapReadAggregates exists, is placed at RoleViewer,
// and is still not called — including by the aggregate endpoint this phase
// adds. The test is whether anyone can read items but should not see counts of
// them, and the answer is no: an aggregate resolves the identical readable set
// a results page does and returns counts of rows the caller could enumerate
// one at a time. A gate there would refuse a person who could get the same
// number by paging. Placement stays speculative; inventing a use to tidy the
// capability table would be the wrong kind of completeness.
package dashboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/dashboards"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// Handler serves the dashboard routes.
type Handler struct {
	svc *dashboards.Service
	// views is the saved-view service, used only to expand the caller's teams
	// once per request. The dashboard service takes the resulting Actor; it
	// never reaches for a store itself, so team expansion has one entry point
	// across both families.
	views *views.Service
}

// NewHandler creates a dashboards Handler.
func NewHandler(svc *dashboards.Service, viewSvc *views.Service) *Handler {
	return &Handler{svc: svc, views: viewSvc}
}

// Routes returns the dashboard router, mounted at /orgs/{orgID}/dashboards.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	// Static before wildcard: chi matches /dashboards/home ahead of
	// /dashboards/{dashboardID}, so "home" is never parsed as a dashboard id.
	r.Get("/home", h.Home)
	r.Get("/{dashboardID}", h.Get)
	r.Patch("/{dashboardID}", h.Update)
	r.Delete("/{dashboardID}", h.Delete)
	r.Put("/{dashboardID}/gadgets", h.SetGadgets)
	return r
}

// dashboardResponse is the wire shape of a dashboard. Lowercase snake_case
// without exception.
type dashboardResponse struct {
	ID               uuid.UUID  `json:"id"`
	OwnerID          uuid.UUID  `json:"owner_id"`
	OwnerName        string     `json:"owner_name,omitempty"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Module           string     `json:"module"`
	IsDefault        bool       `json:"is_default"`
	IsSeeded         bool       `json:"is_seeded"`
	Visibility       string     `json:"visibility"`
	VisibilityTeamID *uuid.UUID `json:"visibility_team_id"`
	TeamName         string     `json:"team_name,omitempty"`
	// IsOwner drives the UI's own/shared provenance distinction without the
	// client having to compare ids against the session.
	IsOwner bool `json:"is_owner"`
	// IsValid and InvalidReason carry the degradation state. An invalid
	// dashboard still lists and still opens.
	IsValid       bool   `json:"is_valid"`
	InvalidReason string `json:"invalid_reason,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// dashboardDetailResponse adds the gadgets, and is used only by the routes
// that resolve them.
//
// A SEPARATE TYPE rather than an omitempty field on the one above. With
// omitempty an EMPTY dashboard would serialise no `gadgets` key at all, and a
// client reading `body.gadgets.map(...)` would take the page down on exactly
// the dashboard somebody just created. Without omitempty the LIST response
// would carry `"gadgets":null` on every row, which is worse: it looks like an
// answer. Two types, each honest about what it holds.
type dashboardDetailResponse struct {
	dashboardResponse
	// Always a slice, never null: an empty dashboard is a real state the UI
	// renders an "add a gadget" prompt for.
	Gadgets []gadgetResponse `json:"gadgets"`
}

// MarshalJSON flattens the embedded dashboard into one object. Go's embedding
// promotes fields for selection but not for encoding/json, which would
// otherwise nest the dashboard under a "dashboardResponse" key.
func (d dashboardDetailResponse) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(d.dashboardResponse)
	if err != nil {
		return nil, fmt.Errorf("encoding the dashboard: %w", err)
	}
	gadgets, err := json.Marshal(d.Gadgets)
	if err != nil {
		return nil, fmt.Errorf("encoding the dashboard's gadgets: %w", err)
	}
	// base always ends in '}' — it is a struct with at least one field.
	out := make([]byte, 0, len(base)+len(gadgets)+16)
	out = append(out, base[:len(base)-1]...)
	out = append(out, []byte(`,"gadgets":`)...)
	out = append(out, gadgets...)
	out = append(out, '}')
	return out, nil
}

// gadgetResponse is one resolved tile.
type gadgetResponse struct {
	ID          uuid.UUID  `json:"id"`
	GadgetKey   string     `json:"gadget_key"`
	Position    int32      `json:"position"`
	ColSpan     int32      `json:"col_span"`
	SavedViewID *uuid.UUID `json:"saved_view_id"`
	Config      Config     `json:"config"`
	// State is the server's answer to "what should this tile draw", covering
	// every ADR-0009 degradation rule. The client dispatches on it rather than
	// re-deriving an audience rule.
	State string `json:"state"`
	Title string `json:"title"`
	// Render is the registry's render mode, empty for an unknown gadget.
	Render string `json:"render,omitempty"`
	// Query is the filter document this tile should resolve, present only when
	// the state is ready and the gadget has a query at all. The client posts
	// it to /views/preview or /views/aggregate — the same two endpoints the
	// filter builder uses, so gadget data takes exactly one resolution path.
	Query json.RawMessage `json:"query,omitempty"`
	// ViewName and InvalidReason describe the referenced saved view.
	ViewName      string `json:"view_name,omitempty"`
	InvalidReason string `json:"invalid_reason,omitempty"`
}

// Config is the wire form of a gadget's configuration. It mirrors
// dashboards.Config field for field; the domain type is not reused directly so
// that a change to storage cannot silently change the wire format.
type Config struct {
	Title   string `json:"title,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
	GroupBy string `json:"group_by,omitempty"`
	Body    string `json:"body,omitempty"`
}

func toConfig(c dashboards.Config) Config {
	return Config{Title: c.Title, Limit: c.Limit, GroupBy: c.GroupBy, Body: c.Body}
}

func toDashboardResponse(d dashboards.Dashboard, a views.Actor) dashboardResponse {
	return dashboardResponse{
		ID: d.ID, OwnerID: d.OwnerID, OwnerName: d.OwnerName,
		Name: d.Name, Description: d.Description, Module: string(d.Module),
		IsDefault: d.IsDefault, IsSeeded: d.IsSeeded,
		Visibility: string(d.Visibility), VisibilityTeamID: d.VisibilityTeamID,
		TeamName: d.TeamName,
		IsOwner:  d.OwnerID == a.UserID,
		IsValid:  d.IsValid(), InvalidReason: d.InvalidReason,
		CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: d.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func toDetailResponse(d dashboards.Detail, a views.Actor) (dashboardDetailResponse, error) {
	out := dashboardDetailResponse{
		dashboardResponse: toDashboardResponse(d.Dashboard, a),
		Gadgets:           make([]gadgetResponse, 0, len(d.Gadgets)),
	}
	for _, g := range d.Gadgets {
		row := gadgetResponse{
			ID: g.ID, GadgetKey: g.Key, Position: g.Position, ColSpan: g.ColSpan,
			SavedViewID: g.SavedViewID, Config: toConfig(g.Config),
			State: string(g.State), Title: g.Title, Render: string(g.Render),
			ViewName: g.ViewName, InvalidReason: g.InvalidReason,
		}
		if g.Query != nil {
			raw, err := g.Query.Encode()
			if err != nil {
				return dashboardDetailResponse{}, fmt.Errorf("encoding the gadget's filter document: %w", err)
			}
			row.Query = raw
		}
		out.Gadgets = append(out.Gadgets, row)
	}
	return out, nil
}

// dashboardRequest is the create/update body.
type dashboardRequest struct {
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Module           string     `json:"module"`
	Visibility       string     `json:"visibility"`
	VisibilityTeamID *uuid.UUID `json:"visibility_team_id"`
	// IsDefault is a pointer so an update that omits it leaves the flag alone.
	// A bare bool would make every PATCH clear somebody's default — the
	// absent-versus-null collapse that silently wiped every item's due_at.
	IsDefault *bool `json:"is_default"`
}

// gadgetRequest is one tile in a layout write. There is no position: the
// server numbers the collection from the order it was sent.
type gadgetRequest struct {
	GadgetKey   string          `json:"gadget_key"`
	ColSpan     int32           `json:"col_span"`
	SavedViewID *uuid.UUID      `json:"saved_view_id"`
	Config      json.RawMessage `json:"config"`
}

// List returns every dashboard whose definition reaches the caller.
//
// @Summary      List dashboards
// @Description  Returns the caller's own dashboards plus those shared with them (org audience, or a team in their effective team set). Filter by module with ?module=home|beacon|vector.
// @Tags         dashboards
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string                    true   "Organization ID (UUID)"
// @Param        module  query     string                    false  "Restrict to one module"
// @Success      200     {object}  map[string]interface{}    "Dashboards"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Invalid parameters"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not an org member"
// @Router       /orgs/{orgID}/dashboards [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	module := dashboards.Module(r.URL.Query().Get("module"))
	rows, err := h.svc.List(r.Context(), orgID, actor, module)
	if err != nil {
		h.fail(w, r, err, "could not list dashboards")
		return
	}
	out := make([]dashboardResponse, 0, len(rows))
	for _, d := range rows {
		out = append(out, toDashboardResponse(d, actor))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"dashboards": out})
}

// Create saves a new dashboard.
//
// @Summary      Create a dashboard
// @Description  Creates a dashboard owned by the caller. Any org member may create one; it is private unless a visibility is given.
// @Tags         dashboards
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                    true  "Organization ID (UUID)"
// @Param        body   body      map[string]interface{}    true  "Dashboard"
// @Success      201    {object}  map[string]interface{}    "Created dashboard"
// @Failure      400    {object}  api.SwaggerErrorResponse  "Invalid parameters"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      422    {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/dashboards [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	draft, ok := h.draft(w, r)
	if !ok {
		return
	}
	d, err := h.svc.Create(r.Context(), orgID, actor, draft)
	if err != nil {
		h.fail(w, r, err, "could not save the dashboard")
		return
	}
	respond.JSON(w, http.StatusCreated, toDashboardResponse(d, actor))
}

// Home returns the caller's Home dashboard, seeding a starter on first visit.
//
// @Summary      Resolve the Home dashboard
// @Description  Returns the caller's default Home dashboard. On a first visit it creates a starter layout for them — once, and idempotently, so two tabs opening Home cannot produce two dashboards. Changing team never re-seeds.
// @Tags         dashboards
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                    true  "Organization ID (UUID)"
// @Success      200    {object}  map[string]interface{}    "The Home dashboard with its gadgets"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Not an org member"
// @Router       /orgs/{orgID}/dashboards/home [get]
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.ResolveHome(r.Context(), orgID, actor)
	if err != nil {
		h.fail(w, r, err, "could not load your Home dashboard")
		return
	}
	h.respondDetail(w, r, detail, actor, "could not load your Home dashboard")
}

// Get returns one dashboard with its gadgets, resolved for the caller.
//
// @Summary      Get a dashboard
// @Description  Every gadget resolves per viewer: two people opening one shared dashboard legitimately see different rows and different numbers.
// @Tags         dashboards
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string                    true  "Organization ID (UUID)"
// @Param        dashboardID  path      string                    true  "Dashboard ID (UUID)"
// @Success      200          {object}  map[string]interface{}    "Dashboard with gadgets"
// @Failure      401          {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Router       /orgs/{orgID}/dashboards/{dashboardID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	id, ok := uuidParam(w, r, "dashboardID")
	if !ok {
		return
	}
	detail, err := h.svc.Get(r.Context(), orgID, id, actor)
	if err != nil {
		h.fail(w, r, err, "could not load the dashboard")
		return
	}
	h.respondDetail(w, r, detail, actor, "could not load the dashboard")
}

// Update replaces a dashboard's mutable surface. Owner only.
//
// @Summary      Update a dashboard
// @Description  Owner only; org admins bypass. A dashboard the caller cannot see answers 404 rather than 403, so the endpoint does not confirm that somebody else's private dashboard exists.
// @Tags         dashboards
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string                    true  "Organization ID (UUID)"
// @Param        dashboardID  path      string                    true  "Dashboard ID (UUID)"
// @Param        body         body      map[string]interface{}    true  "Dashboard"
// @Success      200          {object}  map[string]interface{}    "Updated dashboard"
// @Failure      401          {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403          {object}  api.SwaggerErrorResponse  "Not the owner"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Failure      422          {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/dashboards/{dashboardID} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	id, ok := uuidParam(w, r, "dashboardID")
	if !ok {
		return
	}
	draft, ok := h.draft(w, r)
	if !ok {
		return
	}
	d, err := h.svc.Update(r.Context(), orgID, id, actor, draft)
	if err != nil {
		h.fail(w, r, err, "could not update the dashboard")
		return
	}
	respond.JSON(w, http.StatusOK, toDashboardResponse(d, actor))
}

// Delete soft-deletes a dashboard. Owner only.
//
// @Summary      Delete a dashboard
// @Description  Owner only; org admins bypass.
// @Tags         dashboards
// @Security     BearerAuth
// @Param        orgID        path  string  true  "Organization ID (UUID)"
// @Param        dashboardID  path  string  true  "Dashboard ID (UUID)"
// @Success      204          "Deleted"
// @Failure      401          {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403          {object}  api.SwaggerErrorResponse  "Not the owner"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Router       /orgs/{orgID}/dashboards/{dashboardID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	id, ok := uuidParam(w, r, "dashboardID")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), orgID, id, actor); err != nil {
		h.fail(w, r, err, "could not delete the dashboard")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetGadgets replaces the whole gadget collection.
//
// @Summary      Save a dashboard's layout
// @Description  Replaces the whole gadget collection in one transaction — layout saves as a collection, never per gadget, so a dashboard is never left half-arranged. Order in the array is the display order; the server assigns positions. Owner only.
// @Tags         dashboards
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string                    true  "Organization ID (UUID)"
// @Param        dashboardID  path      string                    true  "Dashboard ID (UUID)"
// @Param        body         body      map[string]interface{}    true  "Ordered gadgets"
// @Success      200          {object}  map[string]interface{}    "Dashboard with the saved gadgets"
// @Failure      401          {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403          {object}  api.SwaggerErrorResponse  "Not the owner"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Failure      422          {object}  api.SwaggerErrorResponse  "Unknown gadget, or invalid configuration"
// @Router       /orgs/{orgID}/dashboards/{dashboardID}/gadgets [put]
func (h *Handler) SetGadgets(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	id, ok := uuidParam(w, r, "dashboardID")
	if !ok {
		return
	}
	var body struct {
		Gadgets []gadgetRequest `json:"gadgets"`
	}
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "could not read the request")
		return
	}
	drafts := make([]dashboards.GadgetDraft, 0, len(body.Gadgets))
	for _, g := range body.Gadgets {
		drafts = append(drafts, dashboards.GadgetDraft{
			Key:         dashboards.GadgetKey(g.GadgetKey),
			ColSpan:     g.ColSpan,
			SavedViewID: g.SavedViewID,
			Config:      g.Config,
		})
	}
	detail, err := h.svc.SetGadgets(r.Context(), orgID, id, actor, drafts)
	if err != nil {
		h.fail(w, r, err, "could not save the layout")
		return
	}
	h.respondDetail(w, r, detail, actor, "could not save the layout")
}

func (h *Handler) respondDetail(w http.ResponseWriter, r *http.Request, d dashboards.Detail, a views.Actor, fallback string) {
	resp, err := toDetailResponse(d, a)
	if err != nil {
		h.fail(w, r, err, fallback)
		return
	}
	respond.JSON(w, http.StatusOK, resp)
}

// context resolves the org id and the calling actor, writing its own error
// responses. Team expansion happens exactly once per request, through the
// saved-view service, so both families expand teams the same way.
func (h *Handler) context(w http.ResponseWriter, r *http.Request) (uuid.UUID, views.Actor, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, views.Actor{}, false
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "not authenticated")
		return uuid.Nil, views.Actor{}, false
	}
	isAdmin := false
	if res := access.FromContext(r.Context()); res != nil {
		isAdmin = res.IsOrgAdmin
	}
	actor, err := h.views.ActorFor(r.Context(), orgID, claims.UserID, isAdmin)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not resolve your access")
		return uuid.Nil, views.Actor{}, false
	}
	return orgID, actor, true
}

func (h *Handler) draft(w http.ResponseWriter, r *http.Request) (dashboards.Draft, bool) {
	var body dashboardRequest
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "could not read the request")
		return dashboards.Draft{}, false
	}
	return dashboards.Draft{
		Name: body.Name, Description: body.Description,
		Module:           dashboards.Module(body.Module),
		Visibility:       views.Visibility(body.Visibility),
		VisibilityTeamID: body.VisibilityTeamID,
		IsDefault:        body.IsDefault,
	}, true
}

// fail maps domain errors to status codes. Everything the domain names is a
// human-readable sentence written server-side; anything else collapses to the
// caller's fallback so no internal string reaches a user.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	// Anything the caller can fix by changing their request carries the typed
	// error and is returned verbatim — a bound they exceeded is worth naming.
	var invalid views.ValidationError
	if errors.As(err, &invalid) {
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, invalid.Error())
		return
	}
	switch {
	case errors.Is(err, dashboards.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "dashboard not found")
	case errors.Is(err, dashboards.ErrNotOwner):
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, dashboards.ErrNotOwner.Error())
	case errors.Is(err, dashboards.ErrNameRequired),
		errors.Is(err, dashboards.ErrModuleInvalid),
		errors.Is(err, dashboards.ErrTooManyGadgets),
		errors.Is(err, dashboards.ErrUnknownGadget),
		errors.Is(err, dashboards.ErrUnknownConfigKey),
		errors.Is(err, dashboards.ErrViewRequired),
		errors.Is(err, dashboards.ErrViewNotAllowed),
		errors.Is(err, dashboards.ErrViewNotVisible),
		errors.Is(err, dashboards.ErrSpanInvalid),
		errors.Is(err, dashboards.ErrGadgetModule),
		errors.Is(err, views.ErrGroupFieldModule),
		errors.Is(err, views.ErrUnknownGroupField),
		errors.Is(err, views.ErrTeamRequired),
		errors.Is(err, views.ErrTeamNotMember):
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, fallback)
	}
}

func uuidParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}
