package api

import (
	"time"

	"github.com/google/uuid"
)

// swagger_types.go — schema definitions for OpenAPI spec generation.
// These types are used ONLY for swag annotation — not for actual request handling.
// They must exactly match what the handlers actually send and receive.

// --- Auth ---

// SwaggerLoginRequest is the request body for POST /auth/login.
type SwaggerLoginRequest struct {
	Email    string `json:"email" example:"admin@azimuthal.com"`
	Password string `json:"password" example:"yourpassword"`
}

// SwaggerRegisterRequest is the request body for POST /auth/register.
type SwaggerRegisterRequest struct {
	Email       string `json:"email" example:"newuser@azimuthal.com"`
	DisplayName string `json:"display_name" example:"New User"`
	Password    string `json:"password" example:"securepassword123"`
}

// SwaggerRefreshRequest is the request body for POST /auth/refresh.
type SwaggerRefreshRequest struct {
	RefreshToken string `json:"refresh_token" example:"eyJhbGciOiJSUzI1NiIs..."`
}

// SwaggerUserResponse matches the userResponse struct in auth handler.
type SwaggerUserResponse struct {
	ID          uuid.UUID `json:"id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Email       string    `json:"email" example:"admin@azimuthal.com"`
	DisplayName string    `json:"display_name" example:"Admin"`
	OrgID       string    `json:"org_id" example:"9c0e1642-64bc-4745-992e-8e0eec643ee1"`
	Role        string    `json:"role" example:"member"`
	IsActive    bool      `json:"is_active" example:"true"`
}

// SwaggerOrgResponse matches the orgResponse struct in auth handler.
type SwaggerOrgResponse struct {
	ID   uuid.UUID `json:"id" example:"9c0e1642-64bc-4745-992e-8e0eec643ee1"`
	Slug string    `json:"slug" example:"my-org"`
	Name string    `json:"name" example:"My Organization"`
}

// SwaggerLoginResponse matches the loginResponse struct in auth handler.
type SwaggerLoginResponse struct {
	AccessToken  string              `json:"access_token" example:"eyJhbGciOiJSUzI1NiIs..."`
	RefreshToken string              `json:"refresh_token" example:"eyJhbGciOiJSUzI1NiIs..."`
	Token        string              `json:"token" example:"eyJhbGciOiJSUzI1NiIs..."`
	User         SwaggerUserResponse `json:"user"`
	Org          *SwaggerOrgResponse `json:"org,omitempty"`
}

// SwaggerRefreshResponse matches the refreshResponse struct in auth handler.
type SwaggerRefreshResponse struct {
	AccessToken  string `json:"access_token" example:"eyJhbGciOiJSUzI1NiIs..."`
	RefreshToken string `json:"refresh_token" example:"eyJhbGciOiJSUzI1NiIs..."`
}

// SwaggerLogoutResponse is the response body for POST /auth/logout.
type SwaggerLogoutResponse struct {
	Message string `json:"message" example:"logged out"`
}

// --- Error ---

// SwaggerErrorDetail is the inner error object.
type SwaggerErrorDetail struct {
	Code      string `json:"code" example:"UNAUTHORIZED"`
	Message   string `json:"message" example:"invalid email or password"`
	RequestID string `json:"request_id,omitempty" example:"req_a8d9912a"`
}

// SwaggerErrorResponse is the standard error format for all API errors.
type SwaggerErrorResponse struct {
	Error SwaggerErrorDetail `json:"error"`
}

// --- Health ---

// SwaggerHealthResponse matches the healthResponse struct in health.go.
type SwaggerHealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// --- Spaces ---

// SwaggerCreateSpaceRequest matches createSpaceRequest in spaces handler.
type SwaggerCreateSpaceRequest struct {
	Slug        string  `json:"slug" example:"my-space"`
	Name        string  `json:"name" example:"My Space"`
	Description *string `json:"description,omitempty" example:"A vector space"`
	Type        string  `json:"type" example:"vector"`
	Icon        *string `json:"icon,omitempty" example:"rocket"`
	IsPrivate   bool    `json:"is_private" example:"false"`
	OwnerTeamID *string `json:"owner_team_id,omitempty" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Visibility  string  `json:"visibility,omitempty" example:"discoverable" enums:"hidden,discoverable,org"`
}

// SwaggerUpdateSpaceRequest matches updateSpaceRequest in spaces handler.
type SwaggerUpdateSpaceRequest struct {
	Name        string  `json:"name" example:"Updated Name"`
	Description *string `json:"description,omitempty" example:"Updated description"`
	Icon        *string `json:"icon,omitempty" example:"star"`
	IsPrivate   bool    `json:"is_private" example:"false"`
	OwnerTeamID *string `json:"owner_team_id,omitempty" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Visibility  string  `json:"visibility,omitempty" example:"org" enums:"hidden,discoverable,org"`
}

// --- Teams (v0.3 P2) ---

// SwaggerCreateTeamRequest matches createTeamRequest in teams handler.
type SwaggerCreateTeamRequest struct {
	Slug        string  `json:"slug" example:"platform"`
	Name        string  `json:"name" example:"Platform"`
	Description string  `json:"description,omitempty" example:"Platform engineering"`
	ParentID    *string `json:"parent_id,omitempty" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
}

// SwaggerPatchTeamRequest matches patchTeamRequest in teams handler.
// parent_id: absent = unchanged, null = move to root, UUID = reparent.
type SwaggerPatchTeamRequest struct {
	Name        *string `json:"name,omitempty" example:"Platform Core"`
	Description *string `json:"description,omitempty" example:"Renamed"`
	ParentID    *string `json:"parent_id,omitempty" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
}

// SwaggerPutMemberRequest matches putMemberRequest in teams handler.
type SwaggerPutMemberRequest struct {
	Role      string `json:"role" example:"member" enums:"member,lead"`
	IsPrimary bool   `json:"is_primary" example:"false"`
}

// --- Grants (v0.3 P2) ---

// SwaggerCreateGrantRequest matches createGrantRequest in grants handler.
type SwaggerCreateGrantRequest struct {
	SubjectType string    `json:"subject_type" example:"team" enums:"user,team"`
	SubjectID   uuid.UUID `json:"subject_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Role        string    `json:"role" example:"viewer" enums:"viewer,contributor,agent,space_admin"`
}

// SwaggerUpdateGrantRequest matches updateGrantRequest in grants handler.
type SwaggerUpdateGrantRequest struct {
	Role string `json:"role" example:"contributor" enums:"viewer,contributor,agent,space_admin"`
}

// SwaggerCreateShareRequest matches createShareRequest in shares handler.
type SwaggerCreateShareRequest struct {
	EntityType string    `json:"entity_type" example:"page" enums:"page,ticket,project_item"`
	EntityID   uuid.UUID `json:"entity_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Audience   string    `json:"audience" example:"org" enums:"org,team"`
	AudienceID uuid.UUID `json:"audience_id,omitempty" example:"11111111-1111-1111-1111-111111111111"`
	Cascade    bool      `json:"cascade" example:"false"`
	ExpiresAt  string    `json:"expires_at,omitempty" example:"2026-12-31T23:59:59Z"`
}

// SwaggerUpdateOrgRequest matches updateOrgRequest in spaces handler.
type SwaggerUpdateOrgRequest struct {
	Name        string  `json:"name" example:"Updated Org"`
	Description *string `json:"description,omitempty" example:"Updated description"`
}

// SwaggerAddMemberRequest matches addMemberRequest in spaces handler.
type SwaggerAddMemberRequest struct {
	UserID uuid.UUID `json:"user_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Role   string    `json:"role" example:"member"`
}

// --- Tickets ---

// SwaggerCreateTicketRequest matches createTicketRequest in tickets handler.
type SwaggerCreateTicketRequest struct {
	Title       string     `json:"title" example:"Fix login button"`
	Description string     `json:"description" example:"The login button does not work on mobile"`
	Priority    string     `json:"priority" example:"medium"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// SwaggerUpdateTicketRequest matches updateTicketRequest in tickets handler.
// The real request type uses pointers to tell "absent" from "empty"; this is
// the documentation shape, so it keeps plain strings.
type SwaggerUpdateTicketRequest struct {
	Title       string `json:"title" example:"Fix login button (updated)"`
	Description string `json:"description" example:"Updated description"`
	Priority    string `json:"priority" example:"high"`
	// DueAt is RFC3339. Sending null clears the stored due date; omitting the
	// key leaves it alone.
	DueAt *time.Time `json:"due_at,omitempty"`
}

// SwaggerTransitionRequest matches transitionRequest in tickets handler.
type SwaggerTransitionRequest struct {
	Status string `json:"status" example:"in_progress"`
}

// SwaggerAssignRequest matches assignRequest in tickets handler.
type SwaggerAssignRequest struct {
	AssigneeID uuid.UUID `json:"assignee_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
}

// SwaggerUpdatePortalRequest matches updatePortalRequest in the portal admin
// handler. The real type uses respond.OptionalField to tell "absent" from
// "explicit null"; this is the documentation shape, so it keeps plain values.
type SwaggerUpdatePortalRequest struct {
	// Enabled toggles the portal without discarding its key, so re-enabling
	// does not invalidate URLs already handed out. Omit to leave unchanged.
	Enabled bool `json:"enabled" example:"true"`
	// Name is the portal's public display name. Required non-empty when sent;
	// omit to leave unchanged. Renaming never changes the portal key.
	Name string `json:"name" example:"Acme Support"`
	// Intro is the sign-in page's introduction text. Sending null clears it;
	// omitting the key leaves it alone.
	Intro string `json:"intro" example:"How can we help?"`
}

// SwaggerRequesterIdentity is the external requester behind a portal-raised
// ticket, as the agent surface sees them. Null on a ticket raised inside the
// product — see SwaggerTicketResponse.Requester.
type SwaggerRequesterIdentity struct {
	ID          uuid.UUID `json:"id" example:"c3d4e5f6-a7b8-9012-cdef-123456789012"`
	DisplayName string    `json:"display_name" example:"Dana Okoro"`
	Email       string    `json:"email" example:"dana@example.com"`
}

// SwaggerTicketResponse represents the Ticket domain object returned by handlers.
//
// Corrected in the portal requester-surface PR against internal/core/tickets.Ticket,
// which it had drifted from: Priority is a string enum on the wire and was
// declared int; ReporterID became nullable under migration 044 and was declared
// non-nullable; Number and RequesterID were absent entirely. Per CLAUDE.md §5 the
// repository wins and the drift is corrected in the PR that found it.
type SwaggerTicketResponse struct {
	ID          uuid.UUID `json:"id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	SpaceID     uuid.UUID `json:"space_id" example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
	Number      int32     `json:"number" example:"42"`
	Title       string    `json:"title" example:"Fix login button"`
	Description string    `json:"description" example:"The login button does not work"`
	Status      string    `json:"status" example:"open"`
	Priority    string    `json:"priority" example:"medium" enums:"urgent,high,medium,low"`
	// ReporterID is null exactly when RequesterID is set: migration 044's
	// tickets_origin_identity makes the two mutually exclusive.
	ReporterID *uuid.UUID `json:"reporter_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	// RequesterID identifies a portal-raised ticket. Non-null is the whole
	// provenance predicate; Requester carries the resolved identity.
	RequesterID *uuid.UUID                `json:"requester_id"`
	Requester   *SwaggerRequesterIdentity `json:"requester"`
	AssigneeID  *uuid.UUID                `json:"assignee_id,omitempty"`
	DueAt       *time.Time                `json:"due_at,omitempty"`
	ResolvedAt  *time.Time                `json:"resolved_at,omitempty"`
	Rank        string                    `json:"rank" example:"0|aaaaaa:"`
	CreatedAt   time.Time                 `json:"created_at"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}

// SwaggerKanbanColumn represents one column in a kanban board.
type SwaggerKanbanColumn struct {
	Status  string                  `json:"status" example:"open"`
	Tickets []SwaggerTicketResponse `json:"tickets"`
}

// --- Wiki ---

// SwaggerCreatePageRequest matches createPageRequest in wiki handler.
type SwaggerCreatePageRequest struct {
	Title    string     `json:"title" example:"Getting Started"`
	Content  string     `json:"content" example:"# Welcome\nThis is a wiki page."`
	ParentID *uuid.UUID `json:"parent_id,omitempty"`
	Position int32      `json:"position" example:"0"`
}

// SwaggerUpdatePageRequest matches updatePageRequest in wiki handler.
type SwaggerUpdatePageRequest struct {
	Title           string `json:"title" example:"Getting Started (updated)"`
	Content         string `json:"content" example:"# Updated Content"`
	ExpectedVersion int32  `json:"expected_version" example:"1"`
}

// SwaggerMovePageRequest matches movePageRequest in wiki handler.
type SwaggerMovePageRequest struct {
	ParentID *uuid.UUID `json:"parent_id"`
	Position int32      `json:"position" example:"1"`
}

// --- Projects ---

// SwaggerCreateItemRequest matches createItemRequest in projects handler.
type SwaggerCreateItemRequest struct {
	Title       string     `json:"title" example:"Implement search"`
	Description string     `json:"description" example:"Full-text search for items"`
	Kind        string     `json:"kind" example:"task"`
	Priority    string     `json:"priority" example:"medium"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	SprintID    *uuid.UUID `json:"sprint_id,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// SwaggerUpdateItemRequest matches updateItemRequest in projects handler.
// The real request type uses pointers to tell "absent" from "empty"; this is
// the documentation shape, so it keeps plain strings.
type SwaggerUpdateItemRequest struct {
	Title       string `json:"title" example:"Implement search (updated)"`
	Description string `json:"description" example:"Updated description"`
	// Kind is the org-defined item-type slug. Must name an active type;
	// unknown or archived values are rejected.
	Kind       string     `json:"kind" example:"bug"`
	Priority   string     `json:"priority" example:"high"`
	AssigneeID *uuid.UUID `json:"assignee_id,omitempty"`
	DueAt      *time.Time `json:"due_at,omitempty"`
}

// SwaggerStatusRequest matches statusRequest in projects handler.
type SwaggerStatusRequest struct {
	Status string `json:"status" example:"in_progress"`
}

// SwaggerSprintAssignRequest matches sprintAssignRequest in projects handler.
type SwaggerSprintAssignRequest struct {
	SprintID *uuid.UUID `json:"sprint_id"`
}

// SwaggerCreateSprintRequest matches createSprintRequest in projects handler.
type SwaggerCreateSprintRequest struct {
	Name     string     `json:"name" example:"Sprint 1"`
	Goal     string     `json:"goal" example:"Complete core features"`
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
}

// SwaggerUpdateSprintRequest matches updateSprintRequest in projects handler.
type SwaggerUpdateSprintRequest struct {
	Name     string     `json:"name" example:"Sprint 1 (updated)"`
	Goal     string     `json:"goal" example:"Updated goal"`
	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
}

// SwaggerCompleteSprintRequest matches completeSprintRequest in projects handler.
// The body is optional; next_sprint_id names a carry-over sprint for incomplete
// items, or is omitted to return them to the backlog.
type SwaggerCompleteSprintRequest struct {
	NextSprintID *uuid.UUID `json:"next_sprint_id,omitempty" example:"c3d4e5f6-a7b8-9012-cdef-123456789012"`
}

// SwaggerCreateRelationRequest matches createRelationRequest in projects handler.
type SwaggerCreateRelationRequest struct {
	ToID   uuid.UUID `json:"to_id"   example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
	ToType string    `json:"to_type" example:"project_item"`
	Kind   string    `json:"kind"    example:"blocks"`
}

// SwaggerMoveToSprintRequest matches moveToSprintRequest in projects handler.
type SwaggerMoveToSprintRequest struct {
	ItemID   uuid.UUID `json:"item_id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	SprintID uuid.UUID `json:"sprint_id" example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
}

// SwaggerMoveToBacklogRequest matches moveToBacklogRequest in projects handler.
type SwaggerMoveToBacklogRequest struct {
	ItemID uuid.UUID `json:"item_id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
}

// --- Comments ---

// SwaggerCreateCommentRequest matches createCommentRequest in comments handler.
type SwaggerCreateCommentRequest struct {
	Content string `json:"content" example:"This looks good, let's merge it."`
}

// SwaggerCommentResponse matches commentResponse in comments handler.
type SwaggerCommentResponse struct {
	ID         uuid.UUID `json:"id" example:"c3d4e5f6-a7b8-9012-cdef-123456789012"`
	ItemID     string    `json:"item_id,omitempty" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	AuthorID   uuid.UUID `json:"author_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	AuthorName string    `json:"author_name" example:"Admin User"`
	Body       string    `json:"body" example:"This looks good, let's merge it."`
	Content    string    `json:"content" example:"This looks good, let's merge it."`
	CreatedAt  string    `json:"created_at" example:"2026-01-15T10:30:00Z"`
	UpdatedAt  string    `json:"updated_at" example:"2026-01-15T10:30:00Z"`
}

// SwaggerHistoryResponse is one entry of a ticket's or item's audit History
// (D5): who acted, what the action was, the flat metadata payload (a status
// change carries from/to), and when.
type SwaggerHistoryResponse struct {
	ID        uuid.UUID         `json:"id" example:"c3d4e5f6-a7b8-9012-cdef-123456789012"`
	ActorID   string            `json:"actor_id,omitempty" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	ActorName string            `json:"actor_name" example:"Admin User"`
	Action    string            `json:"action" example:"ticket.status_changed"`
	Payload   map[string]string `json:"payload"`
	CreatedAt string            `json:"created_at" example:"2026-01-15T10:30:00Z"`
}

// SwaggerMessageResponse is a generic message response used by several endpoints.
type SwaggerMessageResponse struct {
	Message string `json:"message" example:"operation completed"`
}

// --- Board configuration ---

// SwaggerBoardColumn matches projects.BoardColumn.
type SwaggerBoardColumn struct {
	ID       uuid.UUID `json:"id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	SpaceID  uuid.UUID `json:"space_id" example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
	Name     string    `json:"name" example:"In Progress"`
	Position int       `json:"position" example:"2"`
	// WIPLimit is null when the column has no limit. Limits are advisory: the
	// API never refuses a transition because a column is over its limit.
	WIPLimit  *int     `json:"wip_limit" example:"3"`
	Statuses  []string `json:"statuses" example:"in_progress,in_review"`
	CreatedAt string   `json:"created_at" example:"2026-01-15T10:30:00Z"`
	UpdatedAt string   `json:"updated_at" example:"2026-01-15T10:30:00Z"`
}

// SwaggerBoardConfig matches projects.BoardConfig.
type SwaggerBoardConfig struct {
	SpaceID uuid.UUID            `json:"space_id" example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
	Columns []SwaggerBoardColumn `json:"columns"`
	// Customized is false when the space has no stored configuration and these
	// columns were derived from its workflow states.
	Customized bool `json:"customized" example:"false"`
}
