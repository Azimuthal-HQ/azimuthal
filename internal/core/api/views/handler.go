// Package views provides HTTP handlers for saved views (ADR-0009, ADR-0010,
// spec §6) — named, reusable queries over Beacon tickets and Vector project
// items.
//
// One org-scoped route family, per ADR-0010, because a view is cross-container
// by nature and has no {spaceID} to hang off. Every route is org-member: any
// member may keep private views, and who may see or change one is decided by
// the view's own ownership and visibility rather than by a space capability.
//
// TWO ROUTES CARRY EXTRA MIDDLEWARE. /results and /preview resolve entity
// shares, because a saved view is the sanctioned ADR-0008 exception and must
// union the caller's shared entities into its results. Share resolution is
// mounted on exactly those two routes and nowhere else in this family, so
// listing or editing a view does not pay for a query it does not use — the
// same reasoning that keeps ResolveShares off every space-scoped route.
//
// NO AUDIT EVENTS. The spec §6 audit list covers teams, memberships, grants,
// space visibility, owner team and shares — every one of them an access
// change. A saved view grants nothing to anybody: sharing one shares a
// QUERY, and each viewer still resolves it against their own access. Adding
// audit rows here would record activity, not authority, and the append-only
// log is for authority.
package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// Handler serves the saved-view routes.
type Handler struct {
	svc *views.Service
	// queues is the P4 PR-B queue lifecycle. A queue is a saved view with a
	// space binding, so it shares this handler and, crucially, the same
	// resolution path — QueueResults calls svc.Preview.
	queues *views.QueueService
}

// NewHandler creates a views Handler.
func NewHandler(svc *views.Service, queues *views.QueueService) *Handler {
	return &Handler{svc: svc, queues: queues}
}

// Routes returns the saved-view router, mounted at /orgs/{orgID}/views.
//
// shareResolver is the ResolveShares middleware, applied to the two routes
// that need it. It is passed in rather than built here so the router owns
// middleware construction, exactly as the admin guards are passed in.
func (h *Handler) Routes(shareResolver func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	// Static before wildcard: chi matches /views/preview ahead of
	// /views/{viewID}, so "preview" is never parsed as a view id.
	r.With(shareResolver).Post("/preview", h.Preview)
	// The P5 aggregate endpoint. Share-resolved for the same reason /preview
	// is: a count over a saved view is the same read a results page performs,
	// so it unions the caller's shared entities identically or the two would
	// report different totals for the same query.
	r.With(shareResolver).Post("/aggregate", h.Aggregate)
	r.Get("/{viewID}", h.Get)
	r.Patch("/{viewID}", h.Update)
	r.Delete("/{viewID}", h.Delete)
	r.With(shareResolver).Get("/{viewID}/results", h.Results)
	return r
}

// viewResponse is the wire shape of a saved view. Lowercase snake_case
// without exception.
type viewResponse struct {
	ID               uuid.UUID       `json:"id"`
	OwnerID          uuid.UUID       `json:"owner_id"`
	OwnerName        string          `json:"owner_name,omitempty"`
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Query            json.RawMessage `json:"query"`
	Visibility       string          `json:"visibility"`
	VisibilityTeamID *uuid.UUID      `json:"visibility_team_id"`
	TeamName         string          `json:"team_name,omitempty"`
	// IsOwner drives the UI's own/shared provenance distinction without the
	// client having to compare ids against the session.
	IsOwner bool `json:"is_owner"`
	// IsValid and InvalidReason carry ADR-0009's degradation state. An
	// invalid view still lists and still opens; it renders "scope
	// unavailable" and prompts its owner to re-scope.
	IsValid       bool   `json:"is_valid"`
	InvalidReason string `json:"invalid_reason,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func toViewResponse(v views.View, a views.Actor) (viewResponse, error) {
	raw, err := v.Query.Encode()
	if err != nil {
		return viewResponse{}, fmt.Errorf("encoding the view's filter document: %w", err)
	}
	return viewResponse{
		ID: v.ID, OwnerID: v.OwnerID, OwnerName: v.OwnerName,
		Name: v.Name, Description: v.Description, Query: raw,
		Visibility: string(v.Visibility), VisibilityTeamID: v.VisibilityTeamID,
		TeamName: v.TeamName,
		IsOwner:  v.OwnerID == a.UserID,
		IsValid:  v.IsValid(), InvalidReason: v.InvalidReason,
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: v.UpdatedAt.UTC().Format(time.RFC3339),
	}, nil
}

type resultResponse struct {
	Module     string     `json:"module"`
	ID         uuid.UUID  `json:"id"`
	Key        string     `json:"key"`
	Title      string     `json:"title"`
	SpaceID    uuid.UUID  `json:"space_id"`
	SpaceKey   string     `json:"space_key"`
	SpaceName  string     `json:"space_name"`
	Status     string     `json:"status"`
	Priority   string     `json:"priority"`
	AssigneeID *uuid.UUID `json:"assignee_id"`
	// AssigneeName is joined in the fan-out so the UI never has to look a
	// person up per row. Null when unassigned, or when the id names no user.
	AssigneeName *string    `json:"assignee_name"`
	Labels       []string   `json:"labels"`
	Kind         *string    `json:"kind,omitempty"`
	SprintID     *uuid.UUID `json:"sprint_id,omitempty"`
	CreatedAt    string     `json:"created_at"`
	UpdatedAt    string     `json:"updated_at"`
	DueAt        *string    `json:"due_at,omitempty"`
	ResolvedAt   *string    `json:"resolved_at,omitempty"`
}

func rfc3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toResultResponses(rows []views.Result) []resultResponse {
	out := make([]resultResponse, 0, len(rows))
	for _, r := range rows {
		labels := r.Labels
		if labels == nil {
			labels = []string{}
		}
		out = append(out, resultResponse{
			Module: string(r.Module), ID: r.ID, Key: r.Key, Title: r.Title,
			SpaceID: r.SpaceID, SpaceKey: r.SpaceKey, SpaceName: r.SpaceName,
			Status: r.Status, Priority: r.Priority,
			AssigneeID: r.AssigneeID, AssigneeName: r.AssigneeName,
			Labels: labels, Kind: r.Kind, SprintID: r.SprintID,
			CreatedAt:  r.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:  r.UpdatedAt.UTC().Format(time.RFC3339),
			DueAt:      rfc3339Ptr(r.DueAt),
			ResolvedAt: rfc3339Ptr(r.ResolvedAt),
		})
	}
	return out
}

// viewRequest is the create/update body. Query arrives as raw JSON so the
// domain's own strict parser sees the caller's exact bytes — decoding it into
// a Go struct here would silently drop the unknown fields the parser must
// refuse.
type viewRequest struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	Query            json.RawMessage `json:"query"`
	Visibility       string          `json:"visibility"`
	VisibilityTeamID *uuid.UUID      `json:"visibility_team_id"`
}

// List returns every saved view whose definition reaches the caller.
//
// @Summary      List saved views
// @Description  Returns the caller's own views plus those shared with them (org audience, or a team in their effective team set). Each carries is_owner and is_valid.
// @Tags         views
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                    true  "Organization ID (UUID)"
// @Success      200    {object}  map[string]interface{}    "Saved views"
// @Failure      400    {object}  api.SwaggerErrorResponse  "Invalid parameters"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Not an org member"
// @Router       /orgs/{orgID}/views [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	rows, err := h.svc.List(r.Context(), orgID, actor)
	if err != nil {
		h.fail(w, r, err, "could not list saved views")
		return
	}
	out := make([]viewResponse, 0, len(rows))
	for _, v := range rows {
		resp, err := toViewResponse(v, actor)
		if err != nil {
			h.fail(w, r, err, "could not list saved views")
			return
		}
		out = append(out, resp)
	}
	respond.JSON(w, http.StatusOK, map[string]any{"views": out})
}

// Create saves a new view.
//
// @Summary      Create a saved view
// @Description  Creates a saved view owned by the caller. Any org member may create one; it is private unless a visibility is given. Unknown fields in the query document are rejected.
// @Tags         views
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                    true  "Organization ID (UUID)"
// @Param        body   body      map[string]interface{}    true  "Saved view"
// @Success      201    {object}  map[string]interface{}    "Created view"
// @Failure      400    {object}  api.SwaggerErrorResponse  "Invalid parameters"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      422    {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/views [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	draft, ok := h.draft(w, r)
	if !ok {
		return
	}
	v, err := h.svc.Create(r.Context(), orgID, actor, draft)
	if err != nil {
		h.fail(w, r, err, "could not save the view")
		return
	}
	resp, err := toViewResponse(v, actor)
	if err != nil {
		h.fail(w, r, err, "could not save the view")
		return
	}
	respond.JSON(w, http.StatusCreated, resp)
}

// Get returns one saved view.
//
// @Summary      Get a saved view
// @Tags         views
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string                    true  "Organization ID (UUID)"
// @Param        viewID  path      string                    true  "Saved view ID (UUID)"
// @Success      200     {object}  map[string]interface{}    "Saved view"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Router       /orgs/{orgID}/views/{viewID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	viewID, ok := uuidParam(w, r, "viewID")
	if !ok {
		return
	}
	v, err := h.svc.Get(r.Context(), orgID, viewID, actor)
	if err != nil {
		h.fail(w, r, err, "could not load the view")
		return
	}
	resp, err := toViewResponse(v, actor)
	if err != nil {
		h.fail(w, r, err, "could not load the view")
		return
	}
	respond.JSON(w, http.StatusOK, resp)
}

// Update replaces a saved view's mutable surface. Owner only.
//
// @Summary      Update a saved view
// @Description  Owner only; org admins bypass. A view the caller cannot see answers 404 rather than 403, so the endpoint does not confirm that somebody else's private view exists.
// @Tags         views
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string                    true  "Organization ID (UUID)"
// @Param        viewID  path      string                    true  "Saved view ID (UUID)"
// @Param        body    body      map[string]interface{}    true  "Saved view"
// @Success      200     {object}  map[string]interface{}    "Updated view"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403     {object}  api.SwaggerErrorResponse  "Not the owner"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Failure      422     {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/views/{viewID} [patch]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	viewID, ok := uuidParam(w, r, "viewID")
	if !ok {
		return
	}
	draft, ok := h.draft(w, r)
	if !ok {
		return
	}
	v, err := h.svc.Update(r.Context(), orgID, viewID, actor, draft)
	if err != nil {
		h.fail(w, r, err, "could not update the view")
		return
	}
	resp, err := toViewResponse(v, actor)
	if err != nil {
		h.fail(w, r, err, "could not update the view")
		return
	}
	respond.JSON(w, http.StatusOK, resp)
}

// Delete soft-deletes a saved view. Owner only.
//
// @Summary      Delete a saved view
// @Description  Owner only; org admins bypass.
// @Tags         views
// @Security     BearerAuth
// @Param        orgID   path  string  true  "Organization ID (UUID)"
// @Param        viewID  path  string  true  "Saved view ID (UUID)"
// @Success      204     "Deleted"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403     {object}  api.SwaggerErrorResponse  "Not the owner"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Router       /orgs/{orgID}/views/{viewID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	viewID, ok := uuidParam(w, r, "viewID")
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), orgID, viewID, actor); err != nil {
		h.fail(w, r, err, "could not delete the view")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Results resolves a saved view for the calling viewer.
//
// @Summary      Run a saved view
// @Description  Resolves the view against the CALLER's readable spaces unioned with their shared entities. Two people running the same view legitimately see different rows. Cursor-paginated.
// @Tags         views
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string                    true   "Organization ID (UUID)"
// @Param        viewID  path      string                    true   "Saved view ID (UUID)"
// @Param        cursor  query     string                    false  "Opaque cursor from a previous page"
// @Param        limit   query     int                       false  "Page size (default 50, max 200)"
// @Success      200     {object}  map[string]interface{}    "Results, next_cursor and has_more"
// @Failure      400     {object}  api.SwaggerErrorResponse  "Malformed cursor"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404     {object}  api.SwaggerErrorResponse  "Not found or not visible to the caller"
// @Router       /orgs/{orgID}/views/{viewID}/results [get]
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	orgID, actor, ok := h.context(w, r)
	if !ok {
		return
	}
	viewID, ok := uuidParam(w, r, "viewID")
	if !ok {
		return
	}
	page, err := h.svc.Results(r.Context(), orgID, viewID, actor,
		viewerFrom(r), r.URL.Query().Get("cursor"), pageLimit(r))
	if err != nil {
		h.fail(w, r, err, "could not run the view")
		return
	}
	respondPage(w, page)
}

// Preview resolves an unsaved query — the filter builder's live results.
//
// @Summary      Preview an unsaved query
// @Description  Runs an unsaved filter document through the identical path a saved view uses, so the builder shows exactly what the saved view will return.
// @Tags         views
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID   path      string                    true   "Organization ID (UUID)"
// @Param        cursor  query     string                    false  "Opaque cursor from a previous page"
// @Param        limit   query     int                       false  "Page size (default 50, max 200)"
// @Param        body    body      map[string]interface{}    true   "Filter document under a query key"
// @Success      200     {object}  map[string]interface{}    "Results, next_cursor and has_more"
// @Failure      401     {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      422     {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/views/preview [post]
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := h.context(w, r)
	if !ok {
		return
	}
	var body struct {
		Query json.RawMessage `json:"query"`
	}
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "could not read the request")
		return
	}
	q, err := views.ParseQuery(body.Query)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, err.Error())
		return
	}
	page, err := h.svc.Preview(r.Context(), orgID, q, viewerFrom(r),
		r.URL.Query().Get("cursor"), pageLimit(r))
	if err != nil {
		h.fail(w, r, err, "could not run the query")
		return
	}
	respondPage(w, page)
}

// bucketResponse is one group of a breakdown.
type bucketResponse struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int64  `json:"count"`
	// Other marks the rollup carrying everything past the bucket cap, so the
	// UI can label it rather than showing it as a real value. Nothing is
	// dropped: the counts still sum to total.
	Other        bool `json:"other,omitempty"`
	OtherBuckets int  `json:"other_buckets,omitempty"`
}

// Aggregate counts an unsaved query's results for the calling viewer.
//
// @Summary      Count or group a query's results
// @Description  Counts the rows a filter document resolves to for the CALLER, optionally grouped by one field (status, priority, assignee or kind). Grouping happens in the database — this is what a count or breakdown gadget calls instead of fetching pages and counting them. Two people running the same query legitimately see different totals.
// @Tags         views
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                    true  "Organization ID (UUID)"
// @Param        body   body      map[string]interface{}    true  "Filter document under a query key, with an optional group_by"
// @Success      200    {object}  map[string]interface{}    "Total, buckets and truncated"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Not an org member"
// @Failure      422    {object}  api.SwaggerErrorResponse  "Validation error"
// @Router       /orgs/{orgID}/views/aggregate [post]
func (h *Handler) Aggregate(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := h.context(w, r)
	if !ok {
		return
	}
	var body struct {
		Query   json.RawMessage `json:"query"`
		GroupBy string          `json:"group_by"`
	}
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "could not read the request")
		return
	}
	q, err := views.ParseQuery(body.Query)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, err.Error())
		return
	}
	group, err := views.ParseGroupField(body.GroupBy)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, err.Error())
		return
	}
	res, err := h.svc.AggregateQuery(r.Context(), orgID, q, viewerFrom(r), group)
	if err != nil {
		h.fail(w, r, err, "could not count the results")
		return
	}
	buckets := make([]bucketResponse, 0, len(res.Buckets))
	for _, b := range res.Buckets {
		buckets = append(buckets, bucketResponse{
			Key: b.Key, Label: b.Label, Count: b.Count,
			Other: b.Other, OtherBuckets: b.OtherBuckets,
		})
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"total":     res.Total,
		"buckets":   buckets,
		"truncated": res.Truncated,
	})
}

func respondPage(w http.ResponseWriter, page views.Page) {
	respond.JSON(w, http.StatusOK, map[string]any{
		"results":     toResultResponses(page.Results),
		"next_cursor": page.NextCursor,
		"has_more":    page.HasMore,
	})
}

// viewerFrom assembles the per-request Viewer.
//
// THE ADR-0008 EXCEPTION IS TAKEN HERE. The readable-space set comes from the
// request's resolved access; the two shared-id sets come from the share
// coverage that the ResolveShares middleware put on the context for exactly
// these two routes. A nil resolution or nil share coverage yields empty sets —
// fail closed, never fail open.
func viewerFrom(r *http.Request) views.Viewer {
	ctx := r.Context()
	v := views.Viewer{}
	if claims := auth.ClaimsFromContext(ctx); claims != nil {
		v.UserID = claims.UserID
	}
	if res := access.FromContext(ctx); res != nil {
		v.ReadableSpaceIDs = res.ReadableSpaceIDs()
	}
	if se := access.SharedEntitiesFromContext(ctx); se != nil {
		v.SharedTicketIDs = se.DirectIDs(access.ShareEntityTicket)
		v.SharedItemIDs = se.DirectIDs(access.ShareEntityProjectItem)
	}
	return v
}

// context resolves the org id and the calling actor, writing its own error
// responses. It is the one place the caller's team expansion is triggered.
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
	actor, err := h.svc.ActorFor(r.Context(), orgID, claims.UserID, isAdmin)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not resolve your access")
		return uuid.Nil, views.Actor{}, false
	}
	return orgID, actor, true
}

func (h *Handler) draft(w http.ResponseWriter, r *http.Request) (views.Draft, bool) {
	var body viewRequest
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "could not read the request")
		return views.Draft{}, false
	}
	// The domain parser sees the caller's exact bytes: an unknown field must
	// be refused, and decoding into a struct here would have dropped it.
	q, err := views.ParseQuery(body.Query)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, err.Error())
		return views.Draft{}, false
	}
	return views.Draft{
		Name: body.Name, Description: body.Description, Query: q,
		Visibility:       views.Visibility(body.Visibility),
		VisibilityTeamID: body.VisibilityTeamID,
	}, true
}

// fail maps domain errors to status codes. Everything the domain names is a
// human-readable sentence written server-side; anything else collapses to the
// caller's fallback so no internal string reaches a user.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if status, code, msg, ok := queueErrorStatus(err); ok {
		respond.Error(w, r, status, code, msg)
		return
	}
	// Anything the caller can fix by changing their request carries the typed
	// error and is returned verbatim. Before it existed a view with a
	// 200-character name answered 500, because the switch below had no case
	// for a bare fmt.Errorf.
	var invalid views.ValidationError
	if errors.As(err, &invalid) {
		respond.Error(w, r, http.StatusUnprocessableEntity, respond.CodeValidation, invalid.Error())
		return
	}
	switch {
	case errors.Is(err, views.ErrNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "saved view not found")
	case errors.Is(err, views.ErrNotOwner):
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, views.ErrNotOwner.Error())
	case errors.Is(err, views.ErrBadCursor):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "that page cursor is not valid")
	case errors.Is(err, views.ErrTeamRequired),
		errors.Is(err, views.ErrTeamNotMember),
		errors.Is(err, views.ErrNameRequired),
		errors.Is(err, views.ErrUnknownField),
		errors.Is(err, views.ErrUnknownGroupField),
		errors.Is(err, views.ErrGroupFieldModule):
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

func pageLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil {
		return views.DefaultPageSize
	}
	return n
}
