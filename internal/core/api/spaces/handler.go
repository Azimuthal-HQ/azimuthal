// Package spaces provides HTTP handlers for space management endpoints.
package spaces

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// Handler holds the dependencies for space HTTP handlers.
type Handler struct {
	queries *generated.Queries
}

// NewHandler creates a space Handler.
func NewHandler(queries *generated.Queries) *Handler {
	return &Handler{queries: queries}
}

// Routes returns a chi.Router with all space endpoints mounted.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Get("/{spaceID}", h.Get)
	r.Put("/{spaceID}", h.Update)
	r.Delete("/{spaceID}", h.Delete)
	r.Get("/{spaceID}/members", h.ListMembers)
	r.Post("/{spaceID}/members", h.AddMember)
	r.Delete("/{spaceID}/members/{userID}", h.RemoveMember)
	return r
}

type createSpaceRequest struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Type        string  `json:"type"`
	Icon        *string `json:"icon,omitempty"`
	IsPrivate   bool    `json:"is_private"`
}

type updateSpaceRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Icon        *string `json:"icon,omitempty"`
	IsPrivate   bool    `json:"is_private"`
}

type addMemberRequest struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
}

// GetOrg returns an organization by ID.
//
// @Summary      Get organization
// @Description  Returns an organization by its ID.
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
	respond.JSON(w, http.StatusOK, org)
}

type updateOrgRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// UpdateOrg updates an organization's details.
//
// @Summary      Update organization
// @Description  Updates an organization's name and description (preserves plan).
// @Tags         spaces
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                      true  "Organization ID (UUID)"
// @Param        body   body      api.SwaggerUpdateOrgRequest true  "Updated org fields"
// @Success      200    {object}  map[string]interface{}      "Updated organization"
// @Failure      400    {object}  api.SwaggerErrorResponse    "Validation error"
// @Failure      401    {object}  api.SwaggerErrorResponse    "Not authenticated"
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

	// Fetch current org to preserve plan
	current, err := h.queries.GetOrganizationByID(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "organization not found")
		return
	}

	org, err := h.queries.UpdateOrganization(r.Context(), generated.UpdateOrganizationParams{
		ID:          orgID,
		Name:        req.Name,
		Description: req.Description,
		Plan:        current.Plan,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update organization")
		return
	}
	respond.JSON(w, http.StatusOK, org)
}

// List returns all spaces for the organization.
//
// @Summary      List spaces
// @Description  Returns all spaces belonging to the specified organization.
// @Tags         spaces
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {array}   map[string]interface{}    "List of spaces"
// @Failure      400    {object}  api.SwaggerErrorResponse  "Invalid org ID"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500    {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, err := orgIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid org_id")
		return
	}

	spaces, err := h.queries.ListSpacesByOrg(r.Context(), orgID)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to list spaces")
		return
	}
	respond.JSON(w, http.StatusOK, spaces)
}

// Create creates a new space.
//
// @Summary      Create space
// @Description  Creates a new space in the organization. Type must be 'project', 'wiki', or 'service_desk'.
// @Tags         spaces
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string                         true  "Organization ID (UUID)"
// @Param        body   body      api.SwaggerCreateSpaceRequest  true  "Space details"
// @Success      201    {object}  map[string]interface{}          "Created space"
// @Failure      400    {object}  api.SwaggerErrorResponse        "Validation error"
// @Failure      401    {object}  api.SwaggerErrorResponse        "Not authenticated"
// @Failure      500    {object}  api.SwaggerErrorResponse        "Internal error"
// @Router       /orgs/{orgID}/spaces [post]
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
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

	space, err := h.queries.CreateSpace(r.Context(), generated.CreateSpaceParams{
		ID:          uuid.New(),
		OrgID:       orgID,
		Slug:        req.Slug,
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		Icon:        req.Icon,
		IsPrivate:   req.IsPrivate,
		CreatedBy:   claims.UserID,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to create space")
		return
	}
	respond.JSON(w, http.StatusCreated, space)
}

// Get returns a single space by ID.
//
// @Summary      Get space
// @Description  Returns a single space by ID.
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
// @Description  Updates a space's name, description, icon, and privacy setting.
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
// @Failure      500      {object}  api.SwaggerErrorResponse         "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID} [put]
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
		return
	}

	var req updateSpaceRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "name is required")
		return
	}

	space, err := h.queries.UpdateSpace(r.Context(), generated.UpdateSpaceParams{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Icon:        req.Icon,
		IsPrivate:   req.IsPrivate,
	})
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "failed to update space")
		return
	}
	respond.JSON(w, http.StatusOK, space)
}

// Delete soft-deletes a space.
//
// @Summary      Delete space
// @Description  Soft-deletes a space by ID.
// @Tags         spaces
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Success      204  "Deleted"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID} [delete]
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
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
// @Description  Returns all members of the specified space.
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
// @Description  Adds a user as a member of the specified space.
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
// @Failure      500      {object}  api.SwaggerErrorResponse      "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/members [post]
func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
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
// @Description  Removes a user from the specified space.
// @Tags         members
// @Security     BearerAuth
// @Param        orgID    path  string  true  "Organization ID (UUID)"
// @Param        spaceID  path  string  true  "Space ID (UUID)"
// @Param        userID   path  string  true  "User ID (UUID)"
// @Success      204  "Removed"
// @Failure      400  {object}  api.SwaggerErrorResponse  "Invalid ID"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      500  {object}  api.SwaggerErrorResponse  "Internal error"
// @Router       /orgs/{orgID}/spaces/{spaceID}/members/{userID} [delete]
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	spaceID, err := spaceIDFromURL(r)
	if err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid space ID")
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
