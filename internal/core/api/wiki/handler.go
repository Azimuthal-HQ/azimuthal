// Package wiki provides HTTP handlers for wiki/docs endpoints.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
)

// ShareQueries backs the wiki share affordances: the move-confirmation
// warning count (ADR-0008 rule 9) and the per-space ShareBadge annotation
// (rule 5). Both are served by the API so the UI never counts or infers
// share coverage client-side.
type ShareQueries interface {
	CountActiveSharesForPageSubtree(ctx context.Context, spaceID, pageID uuid.UUID, path string) (int64, error)
	ListActiveSharesForSpacePages(ctx context.Context, spaceID uuid.UUID) ([]access.SpacePageShare, error)
}

// Handler holds the dependencies for wiki HTTP handlers.
type Handler struct {
	svc      *wiki.Service
	docs     *wiki.DocumentService
	tags     *tags.Service
	auditLog audit.Logger
	shares   ShareQueries
}

// NewHandler creates a wiki Handler.
//
// docs and tagSvc are required arguments rather than With* options, and
// deliberately so. An optional collaborator that is missing does not fail — the
// surface reports itself disabled and answers 404, which is right in production
// and silent in a test harness: every assertion against those routes would pass
// against a tidy 404 and the endpoints would read as covered (CLAUDE.md section
// 2, "No dark harness"). A required argument does not compile when it is
// forgotten, which is a stronger guarantee than a guard test.
func NewHandler(svc *wiki.Service, docs *wiki.DocumentService, tagSvc *tags.Service) *Handler {
	return &Handler{svc: svc, docs: docs, tags: tagSvc, auditLog: audit.NewLogger()}
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithShareQueries attaches the share read model backing the move-impact
// count and the space ShareBadge annotation. nil leaves both reporting empty.
func (h *Handler) WithShareQueries(q ShareQueries) *Handler {
	h.shares = q
	return h
}

// Routes returns a chi.Router with all wiki endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.ListPages)
	r.Post("/", h.CreatePage)
	r.Get("/tree", h.Tree)
	r.Get("/search", h.Search)
	r.Get("/shares", h.SpaceShareBadges)
	r.Get("/{pageID}", h.GetPage)
	r.Put("/{pageID}", h.UpdatePage)
	r.Delete("/{pageID}", h.DeletePage)
	r.Post("/{pageID}/move", h.MovePage)
	r.Get("/{pageID}/share-impact", h.ShareImpact)
	r.Get("/{pageID}/revisions", h.ListRevisions)
	r.Get("/{pageID}/revisions/{version}", h.GetRevision)
	r.Post("/{pageID}/revisions/{version}/restore", h.RestoreRevision)
	r.Get("/{pageID}/diff", h.DiffRevisions)
	r.Get("/{pageID}/render", h.RenderPage)
	// Page-level tags (migration 040). Read is the space's, writing one is the
	// same permission as editing the page.
	r.Get("/{pageID}/tags", h.ListPageTags)
	r.Put("/{pageID}/tags", h.SetPageTags)
	// The document surface (issue #15, ADR-0012). /drafts is registered before
	// /{pageID} paths for readability only — chi matches static segments ahead of
	// parameters regardless.
	r.Get("/drafts", h.ListDrafts)
	r.Get("/{pageID}/document", h.GetDocument)
	r.Put("/{pageID}/draft", h.SaveDraft)
	r.Delete("/{pageID}/draft", h.DiscardDraft)
	r.Post("/{pageID}/publish", h.Publish)
	r.Post("/{pageID}/images", h.UploadImage)
	return r
}

type createPageRequest struct {
	Title    string     `json:"title"`
	Content  string     `json:"content"`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	Position int32      `json:"position"`
}

type updatePageRequest struct {
	Title           string `json:"title"`
	Content         string `json:"content"`
	ExpectedVersion int32  `json:"expected_version"`
}

type movePageRequest struct {
	// TargetSpaceID moves the page to another space when set and different
	// from the current one — which revokes the moved subtree's shares
	// (ADR-0008 rule 9). Omitted or equal to the current space = an in-space
	// reparent/reposition.
	TargetSpaceID *uuid.UUID `json:"target_space_id,omitempty"`
	ParentID      *uuid.UUID `json:"parent_id"`
	Position      int32      `json:"position"`
}

// ListPages returns all pages in a space.
//
// @Summary      List wiki pages
// @Description  Returns all pages in the specified space.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "List of pages"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki [get]
func (h *Handler) ListPages(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	pages, err := h.svc.ListPagesBySpace(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list pages")
		return
	}
	respond.JSON(w, http.StatusOK, pages)
}

// CreatePage creates a new wiki page.
//
// @Summary      Create wiki page
// @Description  Creates a new wiki page. Author is set from the JWT.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerCreatePageRequest  true  "Page details"
// @Success      201      {object}  map[string]interface{}         "Created page"
// @Failure      400      {object}  api.SwaggerErrorResponse       "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse       "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse       "Parent page not found"
// @Failure      500      {object}  api.SwaggerErrorResponse       "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki [post]
func (h *Handler) CreatePage(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req createPageRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	input := wiki.CreatePageInput{
		SpaceID:  spaceID,
		Title:    req.Title,
		Content:  req.Content,
		AuthorID: claims.UserID,
		Position: req.Position,
	}
	if req.ParentID != nil {
		input.ParentID = req.ParentID
	}

	page, err := h.svc.CreatePage(r.Context(), input)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypePageCreated, ActorID: claims.UserID.String(),
		OrgID: claims.OrgID, ResourceType: "page", ResourceID: page.ID.String(),
	})
	respond.JSON(w, http.StatusCreated, page)
}

// GetPage returns a single page by ID.
//
// @Summary      Get wiki page
// @Description  Returns a single wiki page by ID.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "Page details"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID} [get]
func (h *Handler) GetPage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	// Read by page AND space. The subtree's guard proved {spaceID} readable and
	// proved nothing about {pageID}; this route returns the page's full content,
	// so until the two were reconciled a reader of any one space could fetch any
	// page in any organisation by id.
	page, err := h.svc.GetPageInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, page)
}

// UpdatePage updates a page with optimistic locking.
//
// @Summary      Update wiki page
// @Description  Updates a page with optimistic locking. Returns 409 with conflict details if version mismatch.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                        true  "Space ID (UUID)"
// @Param        pageID   path      string                        true  "Page ID (UUID)"
// @Param        body     body      api.SwaggerUpdatePageRequest  true  "Updated fields"
// @Success      200      {object}  map[string]interface{}         "Updated page"
// @Failure      400      {object}  api.SwaggerErrorResponse       "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse       "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse       "Not found"
// @Failure      409      {object}  map[string]interface{}          "Version conflict"
// @Failure      500      {object}  api.SwaggerErrorResponse       "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID} [put]
func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req updatePageRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	// Loaded WITH the space, which is also what makes the capability check below
	// mean what it says: CanEditEntity was being evaluated against this route's
	// spaceID and a foreign page's author, so it decided a question about one
	// space using the ownership of a page in another.
	existing, err := h.svc.GetPageInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, existing.AuthorID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	page, conflict, err := h.svc.UpdatePageOrConflict(r.Context(), wiki.UpdatePageInput{
		PageID:          id,
		SpaceID:         spaceID,
		ExpectedVersion: req.ExpectedVersion,
		Title:           req.Title,
		Content:         req.Content,
		AuthorID:        claims.UserID,
	})
	// The conflict arm is checked FIRST. UpdatePageOrConflict returns the
	// detail *together with* ErrVersionConflict — testing err first, as this
	// did until the integrity pass, made the whole merge payload unreachable
	// and every version conflict answer with the bare error envelope instead.
	if conflict != nil {
		respond.JSON(w, http.StatusConflict, conflict)
		return
	}
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypePageUpdated, ActorID: claims.UserID.String(),
		OrgID: claims.OrgID, ResourceType: "page", ResourceID: id.String(),
	})
	respond.JSON(w, http.StatusOK, page)
}

// DeletePage soft-deletes a page.
//
// @Summary      Delete wiki page
// @Description  Soft-deletes a wiki page by ID.
// @Tags         wiki
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        pageID   path  string  true  "Page ID (UUID)"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID} [delete]
func (h *Handler) DeletePage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	// The space is part of the lookup, so a page in another space is not found
	// rather than deleted: the delete transaction takes the page id alone, and
	// this read is what establishes the caller may name it at all.
	existing, err := h.svc.GetPageInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	if !access.CanEditEntity(r.Context(), spaceID, existing.AuthorID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	actorID := existing.AuthorID
	if claims != nil {
		actorID = claims.UserID
	}
	// Delete-and-revoke-shares run in one transaction (ADR-0008 rule 10);
	// the share.revoked audit rows are written inside it, attributed here.
	if err := h.svc.DeletePage(r.Context(), id, actorID); err != nil {
		handleWikiError(w, r, err)
		return
	}
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypePageDeleted, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "page", ResourceID: id.String(),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// MovePage changes a page's parent or position in the tree.
//
// @Summary      Move wiki page
// @Description  Changes a page's parent or position in the tree hierarchy.
// @Tags         wiki
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string                       true  "Space ID (UUID)"
// @Param        pageID   path      string                       true  "Page ID (UUID)"
// @Param        body     body      api.SwaggerMovePageRequest   true  "New position"
// @Success      200      {object}  map[string]interface{}       "Page moved (with revoked_shares count)"
// @Failure      400      {object}  api.SwaggerErrorResponse     "Invalid request"
// @Failure      401      {object}  api.SwaggerErrorResponse     "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse     "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse     "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/move [post]
func (h *Handler) MovePage(w http.ResponseWriter, r *http.Request) {
	input, ok := h.moveInputFromRequest(w, r)
	if !ok {
		return
	}
	res, err := h.svc.MovePage(r.Context(), input)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims != nil {
		_ = h.auditLog.Log(r.Context(), audit.Event{
			Type: audit.EventTypePageMoved, ActorID: claims.UserID.String(),
			OrgID: claims.OrgID, ResourceType: "page", ResourceID: input.PageID.String(),
			Metadata: map[string]string{
				"target_space_id": input.TargetSpaceID.String(),
				"cross_space":     fmt.Sprintf("%t", res.CrossSpace),
				"revoked_shares":  fmt.Sprintf("%d", res.RevokedShares),
			},
		})
	}
	respond.JSON(w, http.StatusOK, map[string]interface{}{
		"message":        "page moved",
		"cross_space":    res.CrossSpace,
		"revoked_shares": res.RevokedShares,
	})
}

// moveInputFromRequest parses the move request, enforces edit_any on the
// source (and, for a cross-space move, on the destination — 404 there keeps
// the destination's existence from leaking), confirms the page really is in
// the source space, and returns the assembled input. It writes its own error
// response and returns ok=false on failure.
// moveURLIDs parses the three ids the move route carries, writing its own 400
// and reporting ok=false on the first that will not parse.
func moveURLIDs(w http.ResponseWriter, r *http.Request) (orgID, spaceID, pageID uuid.UUID, ok bool) {
	pageID, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	spaceID, err = spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	orgID, err = uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return orgID, spaceID, pageID, true
}

func (h *Handler) moveInputFromRequest(w http.ResponseWriter, r *http.Request) (wiki.MovePageInput, bool) {
	orgID, spaceID, id, ok := moveURLIDs(w, r)
	if !ok {
		return wiki.MovePageInput{}, false
	}
	if !access.Can(r.Context(), access.CapEditAnyItem, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return wiki.MovePageInput{}, false
	}
	// The page has to be in the space this request was authorised against, and
	// the move transaction does not check it: it locks the page by id and then
	// validates the page's OWN space against the org. So edit_any in one space
	// was enough to drag a page out of any other space in the organisation —
	// a write, reached through the same unreconciled {spaceID}/{pageID} pair as
	// the read leaks. Checked after the capability so a caller who may not edit
	// here learns nothing about what lives here.
	if _, err := h.svc.GetPageInSpace(r.Context(), spaceID, id); err != nil {
		handleWikiError(w, r, err)
		return wiki.MovePageInput{}, false
	}
	var req movePageRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return wiki.MovePageInput{}, false
	}
	target := spaceID
	if req.TargetSpaceID != nil {
		target = *req.TargetSpaceID
	}
	if target != spaceID && !access.Can(r.Context(), access.CapEditAnyItem, target) {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "target space not found")
		return wiki.MovePageInput{}, false
	}
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return wiki.MovePageInput{}, false
	}
	return wiki.MovePageInput{
		OrgID:         orgID,
		TargetSpaceID: target,
		PageID:        id,
		ParentID:      req.ParentID,
		Position:      req.Position,
		ActorID:       claims.UserID,
	}, true
}

// ShareImpact reports how many active shares a cross-space move of the page
// (and its subtree) would revoke — the move-confirmation warning number
// (ADR-0008 rule 9). Served by the API so the UI never counts client-side.
//
// @Summary      Move share impact
// @Description  Counts the active shares that a cross-space move of the page and its subtree would revoke.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "Active share count"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/share-impact [get]
func (h *Handler) ShareImpact(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	// Scoped: the count is taken over the page's materialised path, so an
	// unreconciled read handed the subtree of a foreign page to a share query
	// that was itself correctly scoped to this space.
	page, err := h.svc.GetPageInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	var count int64
	if h.shares != nil {
		count, err = h.shares.CountActiveSharesForPageSubtree(r.Context(), spaceID, id, page.Path)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to count share impact")
			return
		}
	}
	respond.JSON(w, http.StatusOK, map[string]int64{"active_share_count": count})
}

// SpaceShareBadges returns the active page shares rooted in the space, each
// with its root path — enough for the client to mark every page as directly
// shared or cascade-covered (ShareBadge, ADR-0008 rule 5). Space-read: any
// reader sees which pages are shared, which is what lets an author know a new
// page under a shared folder is already org-visible.
//
// @Summary      Space page share badges
// @Description  Active page shares in the space with their root paths, for annotating the page tree with a shared indicator.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Active page shares"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Space not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/shares [get]
func (h *Handler) SpaceShareBadges(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	type badge struct {
		EntityID string `json:"entity_id"`
		Cascade  bool   `json:"cascade"`
		RootPath string `json:"root_path"`
	}
	out := []badge{}
	if h.shares != nil {
		rows, err := h.shares.ListActiveSharesForSpacePages(r.Context(), spaceID)
		if err != nil {
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list space shares")
			return
		}
		for _, s := range rows {
			out = append(out, badge{EntityID: s.EntityID.String(), Cascade: s.Cascade, RootPath: s.RootPath})
		}
	}
	respond.JSON(w, http.StatusOK, out)
}

// Tree returns the full page tree for a space.
//
// @Summary      Page tree
// @Description  Returns the full page tree hierarchy for a space.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Page tree"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid space ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/tree [get]
func (h *Handler) Tree(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	tree, err := h.svc.BuildTree(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to build page tree")
		return
	}
	respond.JSON(w, http.StatusOK, tree)
}

// ListRevisions returns the revision history for a page.
//
// @Summary      List page revisions
// @Description  Returns the revision history for a wiki page.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "Revision history"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/revisions [get]
func (h *Handler) ListRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	// The ledger is readable exactly when its page is, and the route proved only
	// that {spaceID} is: a revision list names every historical title and the
	// people who wrote them.
	revisions, err := h.svc.ListRevisions(r.Context(), id, spaceID)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, revisions)
}

// GetRevision returns a specific revision of a page.
//
// @Summary      Get page revision
// @Description  Returns a specific revision of a wiki page by version number.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        version  path      int     true  "Version number"
// @Success      200      {object}  map[string]interface{}    "Revision details"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID or version"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/revisions/{version} [get]
func (h *Handler) GetRevision(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	vStr := chi.URLParam(r, "version")
	v, err := strconv.ParseInt(vStr, 10, 32)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid version number")
		return
	}

	// One revision is one historical copy of the page, title and body and all,
	// so this route disclosed as much as the page read did.
	revision, err := h.svc.GetRevisionInSpace(r.Context(), spaceID, id, int32(v))
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, revision)
}

// DiffRevisions returns the diff between two page versions.
//
// @Summary      Diff page versions
// @Description  Returns the diff between two page versions. Requires 'from' and 'to' query parameters.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Param        pageID   path      string  true  "Page ID (UUID)"
// @Param        from     query     int     true  "From version number"
// @Param        to       query     int     true  "To version number"
// @Success      200      {object}  map[string]interface{}    "Diff result"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Missing or invalid params"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/diff [get]
func (h *Handler) DiffRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}

	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")
	if fromStr == "" || toStr == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "'from' and 'to' version params are required")
		return
	}

	from, err := strconv.ParseInt(fromStr, 10, 32)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid 'from' version")
		return
	}
	to, err := strconv.ParseInt(toStr, 10, 32)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid 'to' version")
		return
	}

	// A diff is built from two revision bodies, so it says everything they say.
	diff, err := h.svc.DiffRevisionsInSpace(r.Context(), spaceID, id, int32(from), int32(to))
	if err != nil {
		handleWikiError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, diff)
}

// RenderPage renders a page's markdown content as HTML.
//
// @Summary      Render page as HTML
// @Description  Renders a page's markdown content as HTML.
// @Tags         wiki
// @Produce      html
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        pageID   path  string  true  "Page ID (UUID)"
// @Success      200  {string}  string                    "Rendered HTML"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404  {object}  api.SwaggerErrorResponse  "Not found"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/{pageID}/render [get]
func (h *Handler) RenderPage(w http.ResponseWriter, r *http.Request) {
	id, err := pageIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid page ID")
		return
	}
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	// Rendering is reading: this route returns the page's whole body as HTML.
	page, err := h.svc.GetPageInSpace(r.Context(), spaceID, id)
	if err != nil {
		handleWikiError(w, r, err)
		return
	}

	html, err := h.svc.RenderPage(page.Content)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to render page")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write([]byte(html)); writeErr != nil {
		slog.Error("writing rendered html response", "error", writeErr)
	}
}

// Search performs full-text search on wiki pages.
//
// @Summary      Search wiki pages
// @Description  Full-text search on wiki pages in a space. Requires query parameter 'q'.
// @Tags         wiki
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true   "Space ID (UUID)"
// @Param        q        query     string  true   "Search query"
// @Param        limit    query     int     false  "Max results (1-200, default 50)"
// @Success      200      {array}   map[string]interface{}    "Search results"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Missing query"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/wiki/search [get]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "query parameter 'q' is required")
		return
	}

	limit := wiki.DefaultSearchLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		n, parseErr := strconv.ParseInt(l, 10, 32)
		if parseErr == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}

	results, err := h.svc.SearchPages(r.Context(), wiki.SearchInput{
		SpaceID: spaceID,
		Query:   query,
		Limit:   limit,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "search failed")
		return
	}
	respond.JSON(w, http.StatusOK, results)
}

func pageIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "pageID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing page ID: %w", err)
	}
	return id, nil
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing space ID: %w", err)
	}
	return id, nil
}

// orgIDFromURL reads the org from the path rather than from the JWT. The URL is
// the scoping convention every space-scoped route follows, and the middleware has
// already established that the space belongs to it.
func orgIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing org ID: %w", err)
	}
	return id, nil
}

func handleWikiError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, wiki.ErrPageNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, wiki.ErrVersionConflict):
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, wiki.ErrPageIsDocumentBacked):
		// 409, not 400: the request is well formed and the caller had the
		// right to make it — the page moved to a representation this endpoint
		// cannot write. Reloading is what resolves it, same as a version
		// conflict.
		respond.Error(w, r, http.StatusConflict, respond.CodeConflict, err.Error())
	case errors.Is(err, wiki.ErrEmptyTitle),
		errors.Is(err, wiki.ErrInvalidSpaceID),
		errors.Is(err, wiki.ErrInvalidAuthorID):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	case errors.Is(err, wiki.ErrRevisionNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	// The tree-shape refusals. All four were raised by the domain and matched
	// by nothing here, so every one of them fell to the default arm and
	// answered 500 — on create for the first, and on move for all four —
	// while both routes annotate 404 and 400 (known-issues #24).
	//
	// A named page or space that does not exist is 404, which is also what
	// keeps the move route from confirming that a space it refused exists. The
	// other two are 400: both things exist and it is the COMBINATION the caller
	// asked for that is wrong, which is a fact about the request.
	case errors.Is(err, wiki.ErrParentPageNotFound),
		errors.Is(err, wiki.ErrTargetSpaceNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, err.Error())
	case errors.Is(err, wiki.ErrParentNotInTargetSpace),
		errors.Is(err, wiki.ErrPageMoveCycle):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, err.Error())
	default:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal,
			fmt.Sprintf("wiki operation failed: %v", err))
	}
}
