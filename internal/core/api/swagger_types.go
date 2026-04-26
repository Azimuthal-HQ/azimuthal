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
	Slug        string  `json:"slug" example:"my-project"`
	Name        string  `json:"name" example:"My Project"`
	Description *string `json:"description,omitempty" example:"A project space"`
	Type        string  `json:"type" example:"project"`
	Icon        *string `json:"icon,omitempty" example:"rocket"`
	IsPrivate   bool    `json:"is_private" example:"false"`
}

// SwaggerUpdateSpaceRequest matches updateSpaceRequest in spaces handler.
type SwaggerUpdateSpaceRequest struct {
	Name        string  `json:"name" example:"Updated Name"`
	Description *string `json:"description,omitempty" example:"Updated description"`
	Icon        *string `json:"icon,omitempty" example:"star"`
	IsPrivate   bool    `json:"is_private" example:"false"`
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
	Labels      []string   `json:"labels,omitempty" example:"bug,frontend"`
}

// SwaggerUpdateTicketRequest matches updateTicketRequest in tickets handler.
type SwaggerUpdateTicketRequest struct {
	Title       string   `json:"title" example:"Fix login button (updated)"`
	Description string   `json:"description" example:"Updated description"`
	Priority    string   `json:"priority" example:"high"`
	Labels      []string `json:"labels,omitempty"`
}

// SwaggerTransitionRequest matches transitionRequest in tickets handler.
type SwaggerTransitionRequest struct {
	Status string `json:"status" example:"in_progress"`
}

// SwaggerAssignRequest matches assignRequest in tickets handler.
type SwaggerAssignRequest struct {
	AssigneeID uuid.UUID `json:"assignee_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
}

// SwaggerTicketResponse represents the Ticket domain object returned by handlers.
type SwaggerTicketResponse struct {
	ID          uuid.UUID  `json:"id" example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	SpaceID     uuid.UUID  `json:"space_id" example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
	Title       string     `json:"title" example:"Fix login button"`
	Description string     `json:"description" example:"The login button does not work"`
	Status      string     `json:"status" example:"open"`
	Priority    int        `json:"priority" example:"1"`
	ReporterID  uuid.UUID  `json:"reporter_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	Labels      []string   `json:"labels"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	Rank        string     `json:"rank" example:"0|aaaaaa:"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
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
	Labels      []string   `json:"labels,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
}

// SwaggerUpdateItemRequest matches updateItemRequest in projects handler.
type SwaggerUpdateItemRequest struct {
	Title       string     `json:"title" example:"Implement search (updated)"`
	Description string     `json:"description" example:"Updated description"`
	Priority    string     `json:"priority" example:"high"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	Labels      []string   `json:"labels,omitempty"`
	DueAt       *time.Time `json:"due_at,omitempty"`
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

// SwaggerCreateRelationRequest matches createRelationRequest in projects handler.
type SwaggerCreateRelationRequest struct {
	ToID uuid.UUID `json:"to_id" example:"b2c3d4e5-f6a7-8901-bcde-f12345678901"`
	Kind string    `json:"kind" example:"blocks"`
}

// SwaggerCreateLabelRequest matches createLabelRequest in projects handler.
type SwaggerCreateLabelRequest struct {
	Name  string `json:"name" example:"bug"`
	Color string `json:"color" example:"#ff0000"`
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

// SwaggerMessageResponse is a generic message response used by several endpoints.
type SwaggerMessageResponse struct {
	Message string `json:"message" example:"operation completed"`
}

// --- Notifications (P1.3) ---

// SwaggerNotification matches notifications.Notification.
type SwaggerNotification struct {
	ID         uuid.UUID `json:"id" example:"d4e5f6a7-b8c9-0123-def0-456789abcdef"`
	UserID     uuid.UUID `json:"user_id" example:"874d6314-6353-45e9-ab2a-5fe930ea4dbc"`
	Kind       string    `json:"kind" example:"assigned"`
	Title      string    `json:"title" example:"Assigned: bug fix"`
	Body       string    `json:"body,omitempty" example:""`
	EntityKind string    `json:"entity_kind,omitempty" example:"ticket"`
	EntityID   uuid.UUID `json:"entity_id,omitempty"`
	IsRead     bool      `json:"is_read" example:"false"`
	CreatedAt  string    `json:"created_at" example:"2026-04-26T10:00:00Z"`
	ReadAt     string    `json:"read_at,omitempty"`
}

// SwaggerNotificationListResponse matches the listResponse in the notifications handler.
type SwaggerNotificationListResponse struct {
	Notifications []SwaggerNotification `json:"notifications"`
	UnreadCount   int64                 `json:"unread_count" example:"3"`
}
