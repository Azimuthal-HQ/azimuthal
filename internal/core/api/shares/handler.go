// Package shares provides HTTP handlers for entity shares (v0.3 spec §6,
// ADR-0008). Shares widen visibility, never narrow it: a shared entity may
// be read by an audience with no access to its space, and by nobody else.
//
// Two route families with deliberately different guards:
//
//   - Management (/orgs/{orgID}/shares...) — create, list, revoke. Guarded
//     by manage_shares on the SHARED ENTITY'S space (space admins), with a
//     read check first so a caller who cannot see the space gets 404, not a
//     capability 403 that would leak the entity's existence.
//
//   - The standalone read route (/orgs/{orgID}/shared/{type}/{id}) — the
//     single most dangerous route in the application: it reaches content
//     WITHOUT space access, so it cannot use the space guards every other
//     read route relies on. Its only gate is an active, unexpired,
//     unrevoked share whose audience includes the caller. It returns the
//     entity and nothing about its container — no space, tree, siblings, or
//     comments (ADR-0008 rule 2/4; leak failure mode 3).
package shares

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/ticketref"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// SharedEntityView is the presentational projection of a shared entity —
// built field-by-field to carry NO container information. It has no
// space_id, no parent_id, no path, no breadcrumbs, no sibling or comment
// references (ADR-0008 rule 2, leak failure mode 3). The read route returns
// exactly this shape.
type SharedEntityView struct {
	ID           uuid.UUID `json:"id"`
	EntityType   string    `json:"entity_type"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	RenderedHTML string    `json:"rendered_html,omitempty"`
	Status       string    `json:"status,omitempty"`
	Priority     string    `json:"priority,omitempty"`
	Version      int32     `json:"version,omitempty"`
	UpdatedAt    string    `json:"updated_at,omitempty"`
	// Shared is always true on this route — it drives the standalone view's
	// persistent ShareBadge without a second request.
	Shared bool `json:"shared"`
}

// EntityReader fetches the presentational fields of a shared entity. Each
// implementation returns access.ErrSharedEntityNotFound when the entity does
// not exist live, so the read route can 404 without leaking existence. The
// reader deliberately exposes no container fields.
type EntityReader interface {
	ReadSharedEntity(ctx context.Context, entityType string, id uuid.UUID) (SharedEntityView, error)
}

// Handler holds the dependencies for share HTTP handlers.
type Handler struct {
	shares   *access.ShareService
	reader   EntityReader
	auditLog audit.Logger

	// ticketRef is the boot-time ticket-reference policy. Both mutations
	// resolve the reference AFTER their authorisation gate and BEFORE their
	// write.
	//
	// The order matters more here than anywhere else in the application. This
	// route family carries no space guard at all — resolveManageable and
	// authorizeShareManagement do the whole read-then-manage split in the
	// handler, 404 when the entity's space is unreadable so existence never
	// leaks and 403 only once it is readable. A ticket_ref 400 raised above
	// that would answer a caller who cannot see the entity differently from
	// one naming a nonexistent id, which is the ADR-0008 leak the split
	// exists to prevent. Resolving before the write is what makes a
	// required-mode 400 mean nothing happened. The zero value is permissive.
	ticketRef ticketref.Policy
}

// NewHandler creates a shares Handler.
func NewHandler(shares *access.ShareService, reader EntityReader) *Handler {
	return &Handler{shares: shares, reader: reader, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithTicketRefPolicy sets the ticket-reference requirement applied to share
// mutations.
func (h *Handler) WithTicketRefPolicy(p ticketref.Policy) *Handler {
	h.ticketRef = p
	return h
}

// ManagementRoutes returns the share management router, mounted at
// /orgs/{orgID}/shares.
func (h *Handler) ManagementRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Delete("/{shareID}", h.Revoke)
	return r
}

type createShareRequest struct {
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	Audience   string     `json:"audience"`
	AudienceID *uuid.UUID `json:"audience_id,omitempty"`
	Cascade    bool       `json:"cascade"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// shareResponse is the wire form of a share (lowercase snake_case).
type shareResponse struct {
	ID         uuid.UUID  `json:"id"`
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	Audience   string     `json:"audience"`
	AudienceID *uuid.UUID `json:"audience_id,omitempty"`
	Cascade    bool       `json:"cascade"`
	ExpiresAt  *string    `json:"expires_at,omitempty"`
	Expired    bool       `json:"expired"`
	CreatedAt  string     `json:"created_at"`
	CreatedBy  uuid.UUID  `json:"created_by"`
}

func toShareResponse(s access.Share) shareResponse {
	resp := shareResponse{
		ID:         s.ID,
		EntityType: s.EntityType,
		EntityID:   s.EntityID,
		Audience:   string(s.Audience),
		AudienceID: s.AudienceID,
		Cascade:    s.Cascade,
		Expired:    s.Expired(),
		CreatedAt:  s.CreatedAt.UTC().Format(time.RFC3339),
		CreatedBy:  s.CreatedBy,
	}
	if s.ExpiresAt != nil {
		formatted := s.ExpiresAt.UTC().Format(time.RFC3339)
		resp.ExpiresAt = &formatted
	}
	return resp
}

// resolveManageable resolves the entity named in the query/body and enforces
// the management guard: 404 when the entity does not exist, does not belong
// to the org, or its space is unreadable to the caller (existence is never
// leaked); 403 when the space is readable but the caller lacks manage_shares.
func (h *Handler) resolveManageable(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, entityType string, entityID uuid.UUID) (access.SharedEntityRef, bool) {
	if !access.ValidShareEntityType(entityType) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_type must be one of page, ticket, project_item")
		return access.SharedEntityRef{}, false
	}
	ref, err := h.shares.LookupEntity(r.Context(), orgID, entityType, entityID)
	if errors.Is(err, access.ErrSharedEntityNotFound) || errors.Is(err, access.ErrInvalidShareEntityType) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "entity not found")
		return access.SharedEntityRef{}, false
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to resolve entity")
		return access.SharedEntityRef{}, false
	}
	res := access.FromContext(r.Context())
	if res == nil || !res.CanReadSpace(ref.SpaceID) {
		// The caller cannot see the entity's space: the entity does not
		// exist as far as they can tell. 404, never 403.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "entity not found")
		return access.SharedEntityRef{}, false
	}
	if !access.Can(r.Context(), access.CapManageShares, ref.SpaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_shares required")
		return access.SharedEntityRef{}, false
	}
	return ref, true
}

// List returns the active shares on an entity, plus (for cascade-capable
// pages) the affected-page count that a cascade share would cover — served
// by the API so the dialog never counts client-side (ADR-0008 rule 7).
//
// @Summary      List entity shares
// @Description  Returns the unrevoked shares on an entity. Requires manage_shares on the entity's space. For pages, includes cascade_page_count — the number of pages a cascade share would cover.
// @Tags         shares
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string  true   "Organization ID (UUID)"
// @Param        entity_type  query     string  true   "Entity type (page, ticket, project_item)"
// @Param        entity_id    query     string  true   "Entity ID (UUID)"
// @Success      200          {object}  map[string]interface{}    "Shares and cascade page count"
// @Failure      400          {object}  api.SwaggerErrorResponse  "Invalid parameters"
// @Failure      401          {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403          {object}  api.SwaggerErrorResponse  "manage_shares required"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Entity not found"
// @Router       /orgs/{orgID}/shares [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromURL(w, r)
	if !ok {
		return
	}
	entityType := r.URL.Query().Get("entity_type")
	entityID, err := uuid.Parse(r.URL.Query().Get("entity_id"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_id is required")
		return
	}
	ref, ok := h.resolveManageable(w, r, orgID, entityType, entityID)
	if !ok {
		return
	}
	list, err := h.shares.ListByEntity(r.Context(), orgID, entityType, entityID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list shares")
		return
	}
	out := make([]shareResponse, 0, len(list))
	for _, s := range list {
		out = append(out, toShareResponse(s))
	}
	body := map[string]interface{}{"shares": out}
	// The cascade preview count is only meaningful for pages.
	if entityType == access.ShareEntityPage {
		count, err := h.shares.CascadePreview(r.Context(), ref, entityID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to count cascade pages")
			return
		}
		body["cascade_page_count"] = count
	}
	respond.JSON(w, http.StatusOK, body)
}

// Create shares an entity with an audience (ADR-0008). Only manage_shares
// may create — a space admin. Cascade is pages-only; expiry must be in the
// future; org audience carries no team, team audience must name a live team.
//
// @Summary      Create entity share
// @Description  Shares an entity with an audience (org or team). Requires manage_shares on the entity's space. Cascade is available for pages only. Read-only — a share never grants write, comment, or transition.
// @Tags         shares
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                        true  "Organization ID (UUID)"
// @Param        body   body      api.SwaggerCreateShareRequest true  "Share"
// @Param        ticket_ref  query     string                        false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Success      201    {object}  map[string]interface{}        "Created share"
// @Failure      400    {object}  api.SwaggerErrorResponse      "Validation error, or a missing/over-long ticket_ref"
// @Failure      401    {object}  api.SwaggerErrorResponse      "Not authenticated"
// @Failure      403    {object}  api.SwaggerErrorResponse      "manage_shares required"
// @Failure      404    {object}  api.SwaggerErrorResponse      "Entity not found"
// @Failure      409    {object}  api.SwaggerErrorResponse      "Duplicate share"
// @Router       /orgs/{orgID}/shares [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromURL(w, r)
	if !ok {
		return
	}
	var req createShareRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}
	// A missing entity_id is a validation error (400), not a 404 — the
	// zero UUID would otherwise fall through to "entity not found".
	if req.EntityID == uuid.Nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "entity_id is required")
		return
	}
	entity, ok := h.resolveManageable(w, r, orgID, req.EntityType, req.EntityID)
	if !ok {
		return
	}
	// After the 404/403 split, before the write — see the ticketRef field.
	ticketRef, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}

	share, err := h.shares.Create(r.Context(), entity, access.CreateShareInput{
		OrgID:      orgID,
		EntityType: req.EntityType,
		EntityID:   req.EntityID,
		Audience:   access.ShareAudience(req.Audience),
		AudienceID: req.AudienceID,
		Cascade:    req.Cascade,
		ExpiresAt:  req.ExpiresAt,
		CreatedBy:  claims.UserID,
	})
	switch {
	case errors.Is(err, access.ErrInvalidShareAudience),
		errors.Is(err, access.ErrShareAudienceIDRequired),
		errors.Is(err, access.ErrShareAudienceIDForbidden),
		errors.Is(err, access.ErrShareAudienceTeamNotFound),
		errors.Is(err, access.ErrShareCascadeNotPage),
		errors.Is(err, access.ErrShareExpiryNotFuture):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
		return
	case errors.Is(err, access.ErrDuplicateShare):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
		return
	case err != nil:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create share")
		return
	}
	h.logShareEvent(r, audit.EventTypeShareCreated, share, ticketRef)
	respond.JSON(w, http.StatusCreated, toShareResponse(share))
}

// Revoke revokes a share (sets revoked_at; never hard-deleted). Access
// recomputes per request, so revocation denies on the very next request.
//
// @Summary      Revoke entity share
// @Description  Revokes a share. Requires manage_shares on the entity's space. Access recomputes per request, so revocation is immediate.
// @Tags         shares
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        shareID  path  string  true  "Share ID (UUID)"
// @Param        ticket_ref  query  string  false  "Operator ticket reference recorded on the audit event (max 200 characters); required when the deployment sets AZIMUTHAL_TICKET_REF_REQUIRED"
// @Success      204  "Revoked"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID, or a missing/over-long ticket_ref"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "manage_shares required"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      410  {object}  api.SwaggerErrorResponse  "Already revoked"
// @Router       /orgs/{orgID}/shares/{shareID} [delete]
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	orgID, ok := orgIDFromURL(w, r)
	if !ok {
		return
	}
	shareID, err := uuid.Parse(chi.URLParam(r, "shareID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid share_id")
		return
	}
	if !h.authorizeShareManagement(w, r, orgID, shareID) {
		return
	}
	// Resolved before the revoke: a rejected DELETE must leave the share live.
	ticketRef, ok := h.ticketRef.Resolve(w, r)
	if !ok {
		return
	}
	revoked, err := h.shares.Revoke(r.Context(), shareID)
	if errors.Is(err, access.ErrShareAlreadyRevoked) {
		respond.Error(w, r, http.StatusGone, respond.CodeConflict, "share already revoked")
		return
	}
	if errors.Is(err, access.ErrShareNotFound) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "share not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to revoke share")
		return
	}
	h.logShareEvent(r, audit.EventTypeShareRevoked, revoked, ticketRef)
	w.WriteHeader(http.StatusNoContent)
}

// authorizeShareManagement loads the share (scoped to the org) and enforces
// the read-then-manage split on its space: a missing share or unreadable
// space 404s (no existence leak), a readable space without manage_shares
// 403s. It writes its own error response and returns false on any failure.
func (h *Handler) authorizeShareManagement(w http.ResponseWriter, r *http.Request, orgID, shareID uuid.UUID) bool {
	share, err := h.shares.Get(r.Context(), orgID, shareID)
	if errors.Is(err, access.ErrShareNotFound) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "share not found")
		return false
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to load share")
		return false
	}
	res := access.FromContext(r.Context())
	if res == nil || !res.CanReadSpace(share.SpaceID) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "share not found")
		return false
	}
	if !access.Can(r.Context(), access.CapManageShares, share.SpaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_shares required")
		return false
	}
	return true
}

// ReadShared is the standalone shared-entity read route (spec §6). It is
// authorised ONLY by a share covering the entity for the caller — never by
// space access — and returns the entity stripped of all container
// information. Both "no such entity" and "not shared with you" answer 404,
// so the route leaks neither existence nor shared-ness.
//
// @Summary      Read a shared entity
// @Description  Returns a shared entity with no container information — no space, tree, siblings, or comments. Authorised by an active, unexpired share whose audience includes the caller. Read-only.
// @Tags         shares
// @Produce      json
// @Security     BearerAuth
// @Param        orgID        path      string  true  "Organization ID (UUID)"
// @Param        entityType   path      string  true  "Entity type (page, ticket, project_item)"
// @Param        entityID     path      string  true  "Entity ID (UUID)"
// @Success      200          {object}  map[string]interface{}    "The shared entity, container-stripped"
// @Failure      401          {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404          {object}  api.SwaggerErrorResponse  "Not shared with you / not found"
// @Router       /orgs/{orgID}/shared/{entityType}/{entityID} [get]
func (h *Handler) ReadShared(w http.ResponseWriter, r *http.Request) {
	entityType := chi.URLParam(r, "entityType")
	entityID, err := uuid.Parse(chi.URLParam(r, "entityID"))
	if err != nil {
		// A malformed id cannot name any share — deny like any other miss.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	if !access.ValidShareEntityType(entityType) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	if !h.shares.CoversForCaller(r.Context(), entityType, entityID) {
		// Fail closed: no share coverage → the entity does not exist as far
		// as this caller can tell. Same 404 as a genuinely missing entity.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	view, err := h.reader.ReadSharedEntity(r.Context(), entityType, entityID)
	if errors.Is(err, access.ErrSharedEntityNotFound) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "not found")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to read shared entity")
		return
	}
	view.Shared = true
	respond.JSON(w, http.StatusOK, view)
}

// logShareEvent writes a handler-layer audit event for a share mutation
// (best-effort, per the default convention). ticketRef is the reference the
// caller resolved before its write — empty when none was supplied and the
// policy did not demand one, which the logger stores as SQL NULL.
//
// The revoke-on-move and revoke-on-delete invariants write their own
// share.revoked events inside the entity mutation's transaction instead — see
// the content_tx adapter. Those carry no reference, deliberately, and the
// reason is recorded there.
func (h *Handler) logShareEvent(r *http.Request, t audit.EventType, s access.Share, ticketRef string) {
	claims := auth.ClaimsFromContext(r.Context())
	actor := ""
	if claims != nil {
		actor = claims.UserID.String()
	}
	meta := map[string]string{
		"entity_type": s.EntityType,
		"entity_id":   s.EntityID.String(),
		"space_id":    s.SpaceID.String(),
		"audience":    string(s.Audience),
	}
	if s.AudienceID != nil {
		meta["audience_id"] = s.AudienceID.String()
	}
	if s.Cascade {
		meta["cascade"] = "true"
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: t, ActorID: actor, OrgID: s.OrgID.String(),
		ResourceType: "share", ResourceID: s.ID.String(), Metadata: meta,
		TicketRef: ticketRef,
	})
}

// orgIDFromURL parses {orgID}, writing the 400 itself on failure.
func orgIDFromURL(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, false
	}
	return orgID, true
}
