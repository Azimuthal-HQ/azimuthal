// Package spaces provides HTTP handlers for space management and the org
// space directory (v0.3 spec §6). Every read is filtered against the
// caller's resolved readable set; mutations are capability-checked
// (manage_space). Space creation authority is org admin or a lead of the
// owning team — the one sanctioned use of the team metadata role, per
// ADR-0007's administrative-authority clause.
package spaces

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

var validKey = regexp.MustCompile(`^[A-Z0-9]{1,10}$`)
var nonAlphanumeric = regexp.MustCompile(`[^A-Z0-9]`)

// validSpaceTypes are the space type values accepted by the API, matching the
// spaces_type_valid CHECK constraint (migration 021).
var validSpaceTypes = map[string]bool{"beacon": true, "codex": true, "vector": true}

// moduleDisplayNames maps space type values to the module names users see in
// the product, for error messages that name the module.
var moduleDisplayNames = map[string]string{"beacon": "Beacon", "codex": "Codex", "vector": "Vector"}

// validVisibilities matches spaces_visibility_valid (migration 023).
var validVisibilities = map[string]bool{
	access.VisibilityHidden:       true,
	access.VisibilityDiscoverable: true,
	access.VisibilityOrg:          true,
}

// deriveKey generates a default key from a space name: uppercase, strip
// non-alphanumeric chars, take the first word, cap at 8 characters.
func deriveKey(name string) string {
	upper := regexp.MustCompile(`[^a-zA-Z0-9\s]`).ReplaceAllString(name, "")
	words := regexp.MustCompile(`\s+`).Split(strings.TrimSpace(upper), -1)
	first := strings.ToUpper(words[0])
	first = nonAlphanumeric.ReplaceAllString(first, "")
	if len(first) > 8 {
		first = first[:8]
	}
	if first == "" {
		return "SPACE"
	}
	return first
}

// uniqueViolation reports whether err is a Postgres unique-constraint
// violation, and if so on which constraint.
func uniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// dedupeKey produces the next candidate key for a derived key that collided:
// KEY → KEY2 → KEY3 … The numeric suffix replaces trailing characters when
// needed so the result never exceeds 10 characters.
func dedupeKey(base string, attempt int) string {
	suffix := strconv.Itoa(attempt + 1)
	if len(base)+len(suffix) > 10 {
		base = base[:10-len(suffix)]
	}
	return base + suffix
}

// WorkflowAssigner assigns a default workflow to a newly created space.
type WorkflowAssigner interface {
	AssignDefaultWorkflowToSpace(ctx context.Context, orgID uuid.UUID, spaceType string, spaceID uuid.UUID) error
}

// GrantCreator creates the creator's space_admin grant on a new space, so a
// non-org-admin creator can reach the space they just made.
type GrantCreator interface {
	Create(ctx context.Context, orgID, spaceID uuid.UUID, subjectType access.SubjectType, subjectID uuid.UUID, role access.Role, createdBy uuid.UUID) (access.Grant, error)
}

// Handler holds the dependencies for space HTTP handlers.
type Handler struct {
	queries  *generated.Queries
	wfAssign WorkflowAssigner
	teamSvc  *teams.Service
	grantSvc GrantCreator
	auditLog audit.Logger
}

// NewHandler creates a space Handler.
func NewHandler(queries *generated.Queries) *Handler {
	return &Handler{queries: queries, auditLog: audit.NewLogger()}
}

// WithWorkflowAssigner attaches a WorkflowAssigner to the handler so new spaces
// are automatically assigned their default workflow.
func (h *Handler) WithWorkflowAssigner(wa WorkflowAssigner) *Handler {
	h.wfAssign = wa
	return h
}

// WithTeamService attaches the team service (owner-team defaulting and the
// lead authority check on space creation).
func (h *Handler) WithTeamService(svc *teams.Service) *Handler {
	h.teamSvc = svc
	return h
}

// WithGrantService attaches the grant service (creator auto-grant).
func (h *Handler) WithGrantService(svc GrantCreator) *Handler {
	h.grantSvc = svc
	return h
}

// WithAuditLogger attaches an audit logger to the handler.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// Routes returns a chi.Router with all space endpoints mounted. spaceGuard
// (org↔space ownership, 404) and readableGuard (resolved readable set, 404)
// wrap every {spaceID}-scoped route; org-level list/create are filtered and
// authority-checked in their handlers.
func (h *Handler) Routes(spaceGuard, readableGuard func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Route("/{spaceID}", func(r chi.Router) {
		if spaceGuard != nil {
			r.Use(spaceGuard)
		}
		if readableGuard != nil {
			r.Use(readableGuard)
		}
		r.Get("/", h.Get)
		r.Put("/", h.Update)
		r.Delete("/", h.Delete)
		r.Get("/summary", h.ContentsSummary)
		r.Get("/members", h.ListMembers)
		r.Post("/members", h.AddMember)
		r.Delete("/members/{userID}", h.RemoveMember)
	})
	return r
}

// ContentsSummary counts what a space contains, backing the delete
// confirmation (P2.5 W8) — the dialog names the space and these counts.
//
// @Summary      Space contents summary
// @Description  Counts of live tickets, pages, and project items in the space. Requires manage_space.
// @Tags         spaces
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {object}  map[string]int            "Counts"
// @Failure      403      {object}  api.SwaggerErrorResponse  "manage_space required"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID}/summary [get]
func (h *Handler) ContentsSummary(w http.ResponseWriter, r *http.Request) {
	spaceID, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space_id")
		return
	}
	if !access.Can(r.Context(), access.CapManageSpace, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "insufficient permissions")
		return
	}
	counts, err := h.queries.CountSpaceContents(r.Context(), spaceID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to count space contents")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]int{
		"tickets": int(counts.Tickets),
		"pages":   int(counts.Pages),
		"items":   int(counts.Items),
	})
}

type createSpaceRequest struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Key         string  `json:"key"`
	Description *string `json:"description,omitempty"`
	Type        string  `json:"type"`
	Icon        *string `json:"icon,omitempty"`
	IsPrivate   bool    `json:"is_private"`
	OwnerTeamID *string `json:"owner_team_id,omitempty"`
	Visibility  string  `json:"visibility,omitempty"`
}

type updateSpaceRequest struct {
	Name        string  `json:"name"`
	Key         string  `json:"key"`
	Description *string `json:"description,omitempty"`
	Icon        *string `json:"icon,omitempty"`
	IsPrivate   bool    `json:"is_private"`
	OwnerTeamID *string `json:"owner_team_id,omitempty"`
	Visibility  string  `json:"visibility,omitempty"`
}

type addMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
}

// directoryRow is one space in the org directory. Readable spaces carry the
// full record and the caller's effective role; discoverable-but-unreadable
// spaces appear as locked rows with identity fields only ("contact a space
// admin" — no request-access workflow in v0.3).
type directoryRow struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Type        string     `json:"type"`
	Description *string    `json:"description,omitempty"`
	Icon        *string    `json:"icon,omitempty"`
	Key         string     `json:"key,omitempty"`
	OwnerTeamID uuid.UUID  `json:"owner_team_id"`
	Visibility  string     `json:"visibility"`
	Readable    bool       `json:"readable"`
	Role        string     `json:"effective_role,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
}

// GetOrg returns an organization by ID.
//
// @Summary      Get organization
// @Description  Returns an organization by its ID. 404 for non-members — existence is never leaked.
// @Tags         spaces
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {object}  map[string]interface{}      "Organization details"
// @Failure      400    {object}  api.SwaggerErrorResponse    "Invalid org ID"
// @Failure      401    {object}  api.SwaggerErrorResponse    "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse    "Not found"
// @Router       /orgs/{orgID} [get]
func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	org, err := h.queries.GetOrganizationByID(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "organization not found")
		return
	}

	// Additive caller fields (P2.5): the avatar menu shows the Admin entry
	// from caller_is_admin. Read from the request's resolution — zero extra
	// queries. The JWT role claim cannot serve here: it carries the legacy
	// users.role column, not the membership role.
	out := struct {
		generated.Organization
		CallerOrgRole string `json:"caller_org_role,omitempty"`
		CallerIsAdmin bool   `json:"caller_is_admin"`
	}{Organization: org}
	if res := access.FromContext(r.Context()); res != nil {
		out.CallerOrgRole = res.OrgRoleName
		out.CallerIsAdmin = res.IsOrgAdmin
	}
	respond.JSON(w, http.StatusOK, out)
}

type updateOrgRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// UpdateOrg updates an organization's details.
//
// @Summary      Update organization
// @Description  Updates an organization's name and description. Org admin only.
// @Tags         spaces
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                      true  "Organization ID (UUID)"
// @Param        body   body      api.SwaggerUpdateOrgRequest true  "Updated org fields"
// @Success      200    {object}  map[string]interface{}      "Updated organization"
// @Failure      400    {object}  api.SwaggerErrorResponse    "Validation error"
// @Failure      401    {object}  api.SwaggerErrorResponse    "Not authenticated"
// @Failure      403    {object}  api.SwaggerErrorResponse    "Org admin required"
// @Failure      404    {object}  api.SwaggerErrorResponse    "Not found"
// @Failure      500    {object}  api.SwaggerErrorResponse    "Internal error"
// @Router       /orgs/{orgID} [patch]
func (h *Handler) UpdateOrg(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	var req updateOrgRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name is required")
		return
	}

	// Verify the org exists before updating.
	_, err = h.queries.GetOrganizationByID(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "organization not found")
		return
	}

	org, err := h.queries.UpdateOrganization(r.Context(), generated.UpdateOrganizationParams{
		ID:          orgID,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update organization")
		return
	}
	respond.JSON(w, http.StatusOK, org)
}

// List returns the org space directory.
//
// @Summary      Space directory
// @Description  Returns every space the caller can see: readable spaces in full (with effective role), plus discoverable-but-unreadable spaces as locked rows. Hidden spaces are absent for non-grantees. Filter with module= (beacon|codex|vector) and team_id= (owning team).
// @Tags         spaces
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true   "Organization ID (UUID)"
// @Param        module   query     string  false  "Filter by module type"
// @Param        team_id  query     string  false  "Filter by owning team"
// @Success      200      {array}   map[string]interface{}    "Directory rows"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid org ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not an org member"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	moduleFilter, teamFilter, ok := parseDirectoryFilters(w, r)
	if !ok {
		return
	}

	spaces, err := h.queries.ListSpacesByOrg(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list spaces")
		return
	}

	// One resolution per request (middleware); the loop below never issues
	// another query — matrix case 23.
	res := access.FromContext(r.Context())

	rows := make([]directoryRow, 0, len(spaces))
	for _, s := range spaces {
		if moduleFilter != "" && s.Type != moduleFilter {
			continue
		}
		if teamFilter != nil && s.OwnerTeamID != *teamFilter {
			continue
		}
		if row, visible := directoryRowFor(s, res); visible {
			rows = append(rows, row)
		}
	}
	respond.JSON(w, http.StatusOK, rows)
}

// parseDirectoryFilters reads the directory's ?module= and ?team_id= query
// filters, writing the 400 response itself on a malformed team id.
func parseDirectoryFilters(w http.ResponseWriter, r *http.Request) (string, *uuid.UUID, bool) {
	moduleFilter := r.URL.Query().Get("module")
	raw := r.URL.Query().Get("team_id")
	if raw == "" {
		return moduleFilter, nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid team_id")
		return "", nil, false
	}
	return moduleFilter, &id, true
}

// directoryRowFor maps one space onto its directory representation for the
// caller: readable spaces in full with the effective role, unreadable
// discoverable spaces as locked identity-only rows, hidden spaces absent.
// A missing resolution reads nothing — fail closed.
func directoryRowFor(s generated.Space, res *access.Resolution) (directoryRow, bool) {
	if res != nil && res.CanReadSpace(s.ID) {
		return directoryRow{
			ID: s.ID, Name: s.Name, Slug: s.Slug, Type: s.Type,
			Description: s.Description, Icon: s.Icon, Key: s.Key,
			OwnerTeamID: s.OwnerTeamID, Visibility: s.Visibility,
			Readable: true, Role: res.RoleOn(s.ID).String(),
			CreatedBy: &s.CreatedBy,
		}, true
	}
	if s.Visibility == access.VisibilityDiscoverable {
		return directoryRow{
			ID: s.ID, Name: s.Name, Slug: s.Slug, Type: s.Type,
			OwnerTeamID: s.OwnerTeamID, Visibility: s.Visibility,
			Readable: false,
		}, true
	}
	return directoryRow{}, false
}

// Create creates a new space.
//
// @Summary      Create space
// @Description  Creates a space in the organization. Type must be 'beacon', 'codex', or 'vector'. Slugs are unique per module: the same slug may exist in different modules of one organization. The owning team defaults to the org default team. Authority: org admin, or a lead of the owning team.
// @Tags         spaces
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                         true  "Organization ID (UUID)"
// @Param        body   body      api.SwaggerCreateSpaceRequest  true  "Space details"
// @Success      201    {object}  map[string]interface{}          "Created space"
// @Failure      400    {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401    {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      403    {object}  api.SwaggerErrorResponse        "Not org admin or lead of the owning team"
// @Failure      409    {object}  api.SwaggerErrorResponse        "Duplicate key in the organization, or duplicate slug within the module"
// @Failure      500    {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) { //nolint:cyclop,funlen // HTTP handler; validation + authority + key derivation requires branching
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "authentication required")
		return
	}

	var req createSpaceRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if req.Name == "" || req.Slug == "" || req.Type == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name, slug, and type are required")
		return
	}

	if !validSpaceTypes[req.Type] {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "type must be one of 'beacon', 'codex', or 'vector'")
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = access.VisibilityDiscoverable
	}
	if !validVisibilities[visibility] {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "visibility must be one of 'hidden', 'discoverable', or 'org'")
		return
	}

	// Resolve the owning team: explicit owner_team_id, else the org default.
	ownerTeam, ok := h.resolveOwnerTeam(w, r, orgID, req.OwnerTeamID)
	if !ok {
		return
	}

	// Authority (ADR-0007 administrative authority): org admin, or a lead of
	// the owning team. This consults the team metadata role — the single
	// sanctioned administrative use; capability checks never do.
	if !h.canCreateSpace(r.Context(), ownerTeam, claims.UserID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "space creation requires an org admin or a lead of the owning team")
		return
	}

	keyDerived := req.Key == ""
	key := req.Key
	if keyDerived {
		key = deriveKey(req.Name)
	}
	if !validKey.MatchString(key) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "key must be 1–10 uppercase letters or digits (e.g. HR, COM, IT2)")
		return
	}

	// A derived key that collides is retried with a numeric suffix (KEY →
	// KEY2 → KEY3 …); an explicit key that collides is the client's conflict.
	const maxKeyAttempts = 20
	var space generated.Space
	baseKey := key
	for attempt := 0; ; attempt++ {
		space, err = h.queries.CreateSpace(r.Context(), generated.CreateSpaceParams{
			ID:          uuid.New(),
			OrgID:       orgID,
			Slug:        req.Slug,
			Name:        req.Name,
			Description: req.Description,
			Type:        req.Type,
			Icon:        req.Icon,
			IsPrivate:   req.IsPrivate,
			CreatedBy:   claims.UserID,
			Key:         key,
			OwnerTeamID: ownerTeam.ID,
			Visibility:  visibility,
		})
		if err == nil {
			break
		}
		constraint, isUnique := uniqueViolation(err)
		switch {
		case !isUnique:
			slog.Error("CreateSpace failed", "error", err, "org_id", orgID) //nolint:gosec // G706: org_id is a UUID, not attacker-controlled
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create space")
			return
		case constraint == "idx_spaces_org_key":
			if keyDerived && attempt < maxKeyAttempts {
				key = dedupeKey(baseKey, attempt)
				continue
			}
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict, "a space with this key already exists in the organization")
			return
		case constraint == "spaces_org_id_type_slug_key":
			// Slug collisions are per (org, module) since migration 028 — the
			// same slug in a different module is not a conflict at all.
			respond.Error(w, r, http.StatusConflict, respond.CodeConflict,
				fmt.Sprintf("a %s space with this slug already exists in the organization", moduleDisplayNames[req.Type]))
			return
		default:
			// A unique violation on a constraint this handler doesn't know is
			// a bug, not a client conflict — surface it as one.
			slog.Error("CreateSpace unexpected unique violation", "constraint", constraint, "org_id", orgID) //nolint:gosec // G706: constraint is a Postgres identifier from our own schema, org_id a UUID
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create space")
			return
		}
	}

	// Auto-add the creator as an admin member of the new space (legacy
	// space_members metadata, kept for continuity).
	if _, err := h.queries.AddSpaceMember(r.Context(), generated.AddSpaceMemberParams{
		ID:      uuid.New(),
		SpaceID: space.ID,
		UserID:  claims.UserID,
		Role:    "admin",
	}); err != nil {
		slog.Error("AddSpaceMember failed", "error", err, "space_id", space.ID)
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to add creator as member")
		return
	}

	// A non-org-admin creator (a lead) needs a grant to reach the space they
	// just created — org admins keep zero grant rows (ADR-0007).
	res := access.FromContext(r.Context())
	if h.grantSvc != nil && (res == nil || !res.IsOrgAdmin) {
		if _, err := h.grantSvc.Create(r.Context(), orgID, space.ID, access.SubjectUser, claims.UserID, access.RoleSpaceAdmin, claims.UserID); err != nil {
			slog.Error("creator auto-grant failed", "error", err, "space_id", space.ID)
			respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to grant creator access")
			return
		}
	}

	// Assign the default workflow for this space type (best-effort; non-fatal).
	if h.wfAssign != nil {
		if err := h.wfAssign.AssignDefaultWorkflowToSpace(r.Context(), orgID, req.Type, space.ID); err != nil {
			slog.Warn("AssignDefaultWorkflowToSpace failed", "error", err, "space_id", space.ID)
		}
	}

	respond.JSON(w, http.StatusCreated, space)
}

// resolveOwnerTeam maps the request's owner_team_id (or the org default) to
// a team, writing the error response itself on failure.
func (h *Handler) resolveOwnerTeam(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, raw *string) (teams.Team, bool) {
	if h.teamSvc == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "team service not configured")
		return teams.Team{}, false
	}
	if raw != nil && *raw != "" {
		teamID, err := uuid.Parse(*raw)
		if err != nil {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "invalid owner_team_id")
			return teams.Team{}, false
		}
		team, err := h.teamSvc.Get(r.Context(), teamID)
		if err != nil || team.OrgID != orgID {
			respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "owner_team_id does not name a team in this organization")
			return teams.Team{}, false
		}
		return team, true
	}
	team, err := h.teamSvc.GetDefault(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "org default team missing")
		return teams.Team{}, false
	}
	return team, true
}

// canCreateSpace implements the v0.3 space-creation authority: org admin, or
// a lead of the owning team.
func (h *Handler) canCreateSpace(ctx context.Context, ownerTeam teams.Team, userID uuid.UUID) bool {
	if res := access.FromContext(ctx); res != nil && res.IsOrgAdmin {
		return true
	}
	if h.teamSvc == nil {
		return false
	}
	member, err := h.teamSvc.GetMember(ctx, ownerTeam.ID, userID)
	if err != nil {
		return false
	}
	return member.IsLead()
}

// Get returns a single space by ID.
//
// @Summary      Get space
// @Description  Returns a single space by ID. 404 unless the space is in the caller's readable set.
// @Tags         spaces
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {object}  map[string]interface{}    "Space details"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404      {object}  api.SwaggerErrorResponse  "Not found"
// @Router       /orgs/{orgID}/spaces/{spaceID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	space, err := h.queries.GetSpaceByID(r.Context(), id)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "space not found")
		return
	}
	respond.JSON(w, http.StatusOK, space)
}

// Update modifies an existing space.
//
// @Summary      Update space
// @Description  Updates a space's name, description, icon, privacy, visibility, and owning team. Requires manage_space; changing visibility additionally requires set_visibility, held only by org admins. Changing owner_team_id or visibility is audited.
// @Tags         spaces
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                          true  "Organization ID (UUID)"
// @Param        spaceID  path      string                          true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerUpdateSpaceRequest   true  "Updated fields"
// @Success      200      {object}  map[string]interface{}           "Updated space"
// @Failure      400      {object}  api.SwaggerErrorResponse         "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse         "Not authenticated"
// @Failure      403      {object}  api.SwaggerErrorResponse         "manage_space required; or set_visibility (org admin) for a visibility change"
// @Failure      404      {object}  api.SwaggerErrorResponse         "Not found"
// @Failure      500      {object}  api.SwaggerErrorResponse         "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	if !access.Can(r.Context(), access.CapManageSpace, id) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_space required")
		return
	}

	req, ok := decodeSpaceUpdate(w, r)
	if !ok {
		return
	}

	// Fetch current space so we can keep the existing key if none provided.
	current, err := h.queries.GetSpaceByID(r.Context(), id)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "space not found")
		return
	}

	// Checked before any field is written so a denied request applies nothing.
	if !requireVisibilityAuthority(w, r, current, req.Visibility) {
		return
	}
	key := req.Key
	if key == "" {
		key = current.Key
	}

	space, err := h.queries.UpdateSpace(r.Context(), generated.UpdateSpaceParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Icon:        req.Icon,
		IsPrivate:   req.IsPrivate,
		Key:         key,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update space")
		return
	}

	space, ok = h.applyVisibilityChange(w, r, current, req.Visibility, space)
	if !ok {
		return
	}
	space, ok = h.applyOwnerTeamChange(w, r, current, req.OwnerTeamID, space)
	if !ok {
		return
	}

	respond.JSON(w, http.StatusOK, space)
}

// decodeSpaceUpdate parses and validates the space-update body, writing the
// 400 response itself on failure.
func decodeSpaceUpdate(w http.ResponseWriter, r *http.Request) (updateSpaceRequest, bool) {
	var req updateSpaceRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return req, false
	}
	if req.Name == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name is required")
		return req, false
	}
	if req.Key != "" && !validKey.MatchString(req.Key) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "key must be 1–10 uppercase letters or digits")
		return req, false
	}
	if req.Visibility != "" && !validVisibilities[req.Visibility] {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "visibility must be one of 'hidden', 'discoverable', or 'org'")
		return req, false
	}
	return req, true
}

// requireVisibilityAuthority enforces set_visibility when the update asks for
// an actual visibility change, writing the 403 itself. Visibility is an
// org-level concern: the capability is held only by the org-admin bypass, not
// by space_admin. An empty or unchanged value is not a change and passes.
func requireVisibilityAuthority(w http.ResponseWriter, r *http.Request, current generated.Space, requested string) bool {
	if requested == "" || requested == current.Visibility {
		return true
	}
	if !access.Can(r.Context(), access.CapSetVisibility, current.ID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "set_visibility required")
		return false
	}
	return true
}

// applyVisibilityChange persists a requested visibility change and writes
// the space.visibility_changed audit event. No-ops (empty or unchanged
// values) write nothing. Returns ok=false after writing an error response.
func (h *Handler) applyVisibilityChange(w http.ResponseWriter, r *http.Request, current generated.Space, visibility string, space generated.Space) (generated.Space, bool) {
	if visibility == "" || visibility == current.Visibility {
		return space, true
	}
	updated, err := h.queries.SetSpaceVisibility(r.Context(), generated.SetSpaceVisibilityParams{
		ID: current.ID, Visibility: visibility,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to change visibility")
		return space, false
	}
	h.logSpaceEvent(r, audit.EventTypeSpaceVisibilityChanged, current.ID, map[string]string{
		"from": current.Visibility, "to": visibility,
	})
	return updated, true
}

// applyOwnerTeamChange persists a requested owner-team change (validating
// the team lives in the same org) and writes the space.owner_team_changed
// audit event. Returns ok=false after writing an error response.
func (h *Handler) applyOwnerTeamChange(w http.ResponseWriter, r *http.Request, current generated.Space, ownerTeamID *string, space generated.Space) (generated.Space, bool) {
	if ownerTeamID == nil || *ownerTeamID == "" {
		return space, true
	}
	newOwner, err := uuid.Parse(*ownerTeamID)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "invalid owner_team_id")
		return space, false
	}
	if newOwner == current.OwnerTeamID {
		return space, true
	}
	if h.teamSvc == nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "team service not configured")
		return space, false
	}
	team, err := h.teamSvc.Get(r.Context(), newOwner)
	if err != nil || team.OrgID != current.OrgID {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "owner_team_id does not name a team in this organization")
		return space, false
	}
	updated, err := h.queries.SetSpaceOwnerTeam(r.Context(), generated.SetSpaceOwnerTeamParams{
		ID: current.ID, OwnerTeamID: newOwner,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to change owner team")
		return space, false
	}
	h.logSpaceEvent(r, audit.EventTypeSpaceOwnerTeamChanged, current.ID, map[string]string{
		"from": current.OwnerTeamID.String(), "to": newOwner.String(),
	})
	return updated, true
}

// Delete soft-deletes a space.
//
// @Summary      Delete space
// @Description  Soft-deletes a space by ID. Requires manage_space.
// @Tags         spaces
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "manage_space required"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	if !access.Can(r.Context(), access.CapManageSpace, id) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_space required")
		return
	}

	if err := h.queries.SoftDeleteSpace(r.Context(), id); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to delete space")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListMembers returns all members of a space.
//
// @Summary      List space members
// @Description  Returns all members of the specified space (legacy space_members metadata).
// @Tags         members
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string  true  "Organization ID (UUID)"
// @Param        spaceID  path      string  true  "Space ID (UUID)"
// @Success      200      {array}   map[string]interface{}    "List of members"
// @Failure      400      {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401      {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500      {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/members [get]
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	id, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	members, err := h.queries.ListSpaceMembers(r.Context(), id)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list members")
		return
	}
	respond.JSON(w, http.StatusOK, members)
}

// AddMember adds a user to a space.
//
// @Summary      Add space member
// @Description  Adds a user as a member of the specified space (legacy space_members metadata). Requires manage_space.
// @Tags         members
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID    path      string                       true  "Organization ID (UUID)"
// @Param        spaceID  path      string                       true  "Space ID (UUID)"
// @Param        body     body      api.SwaggerAddMemberRequest  true  "Member details"
// @Success      201      {object}  map[string]interface{}        "Member added"
// @Failure      400      {object}  api.SwaggerErrorResponse      "Validation error"
// @Failure      401      {object}  api.SwaggerErrorResponse      "Not authenticated"
// @Failure      403      {object}  api.SwaggerErrorResponse      "manage_space required"
// @Failure      500      {object}  api.SwaggerErrorResponse      "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/members [post]
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	if !access.Can(r.Context(), access.CapManageSpace, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_space required")
		return
	}

	var req addMemberRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if req.Role == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "role is required")
		return
	}

	member, err := h.queries.AddSpaceMember(r.Context(), generated.AddSpaceMemberParams{
		ID:      uuid.New(),
		SpaceID: spaceID,
		UserID:  req.UserID,
		Role:    req.Role,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to add member")
		return
	}
	respond.JSON(w, http.StatusCreated, member)
}

// RemoveMember removes a user from a space.
//
// @Summary      Remove space member
// @Description  Removes a user from the specified space (legacy space_members metadata). Requires manage_space.
// @Tags         members
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        userID   path  string  true  "User ID (UUID)"
// @Success      204  "Removed"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      403  {object}  api.SwaggerErrorResponse  "manage_space required"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/members/{userID} [delete]
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	if !access.Can(r.Context(), access.CapManageSpace, spaceID) {
		respond.Error(w, r, http.StatusForbidden, respond.CodeForbidden, "manage_space required")
		return
	}

	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid user ID")
		return
	}

	if err := h.queries.RemoveSpaceMember(r.Context(), generated.RemoveSpaceMemberParams{
		SpaceID: spaceID,
		UserID:  userID,
	}); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to remove member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// logSpaceEvent writes an audit event for a space governance mutation.
func (h *Handler) logSpaceEvent(r *http.Request, t audit.EventType, spaceID uuid.UUID, meta map[string]string) {
	claims := auth.ClaimsFromContext(r.Context())
	actor := ""
	if claims != nil {
		actor = claims.UserID.String()
	}
	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: t, ActorID: actor, OrgID: chi.URLParam(r, "orgID"),
		ResourceType: "space", ResourceID: spaceID.String(), Metadata: meta,
	})
}

func spaceIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "spaceID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing space ID: %w", err)
	}
	return id, nil
}

func orgIDFromURL(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "orgID"))
	if err != nil {
		return uuid.Nil, fmt.Errorf("parsing org ID: %w", err)
	}
	return id, nil
}
