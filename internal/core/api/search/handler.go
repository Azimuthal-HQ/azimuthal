// Package search serves the cross-module search endpoint (P6, spec §5 and §7).
//
// One route, org-scoped for the same reason /views and /dashboards are: a
// search spans spaces by definition, so there is no single space to scope it to
// and RequireSpaceReadable has nothing to check. What replaces it is the
// per-viewer access set — resolved once per request and applied inside every
// query — which is ADR-0010's rule for every cross-space read path.
package search

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/search"
)

// Handler serves the search route.
type Handler struct{ svc *search.Service }

// NewHandler creates a search Handler.
func NewHandler(svc *search.Service) *Handler { return &Handler{svc: svc} }

// Routes returns the search router, mounted at /orgs/{orgID}/search.
//
// shareResolver is the ResolveShares middleware, passed in rather than built
// here so the router owns middleware construction — the same arrangement
// /views uses. Search NEEDS it: shares are the only way an entity outside the
// viewer's readable spaces can legitimately appear in results, and without the
// middleware the share and subtree arms are silently empty on every request.
// That failure has no symptom other than shared things quietly not being found.
func (h *Handler) Routes(shareResolver func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.With(shareResolver).Get("/", h.Search)
	return r
}

// resultResponse is the wire shape of one hit. Lowercase snake_case without
// exception.
//
// The container fields are omitempty and are populated only for a hit the
// viewer reached through a space they can read. A share-only hit carries none
// of them — see search.Result and matrix case 16. The serializer does not
// re-derive that: it renders what the service already decided, so there is one
// place the rule lives.
type resultResponse struct {
	Module    string     `json:"module"`
	ID        uuid.UUID  `json:"id"`
	Title     string     `json:"title"`
	Origin    string     `json:"origin"`
	SpaceID   *uuid.UUID `json:"space_id,omitempty"`
	SpaceKey  string     `json:"space_key,omitempty"`
	SpaceName string     `json:"space_name,omitempty"`
	Number    int32      `json:"number,omitempty"`
	ItemKey   string     `json:"item_key,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	Status    string     `json:"status,omitempty"`
	Priority  string     `json:"priority,omitempty"`
	Path      string     `json:"path,omitempty"`
	Snippet   string     `json:"snippet,omitempty"`
	UpdatedAt string     `json:"updated_at"`
}

// Search runs a cross-module search for the calling viewer.
//
// @Summary     Search across Codex, Beacon and Vector
// @Description Ranked, permission-filtered results across pages, tickets and project items. Supports the `type:` and `tag:` operators.
// @Tags        search
// @Produce     json
// @Param       orgID  path     string true  "Organization ID"
// @Param       q      query    string true  "Search query"
// @Param       limit  query    int    false "Page size (default 20, max 50)"
// @Param       cursor query    string false "Opaque cursor from a previous page"
// @Success     200 {object} map[string]interface{} "Ranked results, the effective module set, and the result state"
// @Failure     400 {object} api.SwaggerErrorResponse "Invalid parameters or cursor"
// @Failure     401 {object} api.SwaggerErrorResponse "Not authenticated"
// @Router      /orgs/{orgID}/search [get]
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	orgID, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}
	if claims := auth.ClaimsFromContext(r.Context()); claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "not authenticated")
		return
	}

	req := requestFrom(r)
	req.OrgID = orgID

	page, err := h.svc.Search(r.Context(), req)
	if err != nil {
		if errors.Is(err, search.ErrBadCursor) {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid cursor")
			return
		}
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "search failed")
		return
	}

	modules := make([]string, 0, len(page.Modules))
	for _, m := range page.Modules {
		modules = append(modules, string(m))
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"results":     toResultResponses(page.Results),
		"next_cursor": page.NextCursor,
		"modules":     modules,
		"tag":         page.TagSlug,
		"state":       string(page.State),
	})
}

func toResultResponses(rows []search.Result) []resultResponse {
	out := make([]resultResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, resultResponse{
			Module:    string(r.Module),
			ID:        r.ID,
			Title:     r.Title,
			Origin:    string(r.Origin),
			SpaceID:   r.SpaceID,
			SpaceKey:  r.SpaceKey,
			SpaceName: r.SpaceName,
			Number:    r.Number,
			ItemKey:   r.ItemKey,
			Kind:      r.Kind,
			Status:    r.Status,
			Priority:  r.Priority,
			Path:      r.Path,
			Snippet:   r.Snippet,
			UpdatedAt: r.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return out
}

// requestFrom assembles the per-request search from the caller's resolved
// access.
//
// A nil resolution or nil share coverage yields empty sets — fail closed, never
// fail open. The service then reports no_readable_scope rather than running a
// fan-out whose every array is empty, which would answer "you may see nothing"
// and "nothing matched" identically.
func requestFrom(r *http.Request) search.Request {
	ctx := r.Context()
	req := search.Request{
		Raw:    r.URL.Query().Get("q"),
		Cursor: r.URL.Query().Get("cursor"),
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		req.Limit = n
	}
	if res := access.FromContext(ctx); res != nil {
		req.ReadableSpaceIDs = res.ReadableSpaceIDs()
	}
	if se := access.SharedEntitiesFromContext(ctx); se != nil {
		req.SharedPageIDs = se.DirectIDs(access.ShareEntityPage)
		req.SharedTicketIDs = se.DirectIDs(access.ShareEntityTicket)
		req.SharedItemIDs = se.DirectIDs(access.ShareEntityProjectItem)
		// The D46 pair. Both halves come from one call so their alignment
		// cannot be lost between here and the query.
		req.SubtreeSpaceIDs, req.SubtreePatterns = se.CascadeSubtreeArrays()
	}
	return req
}
