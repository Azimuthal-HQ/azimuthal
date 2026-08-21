// Package portal implements the customer portal: the surface on which an
// external requester raises and tracks requests against one Beacon service
// desk without holding an account.
//
// Three things about this package are load-bearing and easy to undo by
// accident.
//
// A REQUESTER IS OUTSIDE THE CAPABILITY MODEL, not at the bottom of it.
// There is no users row, no membership, no grant and no team enrolment
// (migration 044), so ADR-0007's resolution produces nothing for them and
// access.Can can never return true. Portal routes therefore carry their own
// guard and MUST NOT call access.Can — a call that always returns false is
// not a check, it is a coincidence waiting to be refactored.
//
// THE PORTAL RENDERS ZERO CONTAINER CONTEXT. Not "hides" — never assembles.
// The types below carry no space id, space key, space name, slug, ticket
// number, assignee, reporter, queue, label, rank or workflow state, so the
// portal's serialisers have nothing to leak even if one of them is written
// carelessly. This is the same discipline as
// internal/core/api/shares/reader.go, whose doc comment puts it well: the
// mapping is where stripping is enforced, by never copying the field in.
//
// INTERNAL COMMENTS ARE EXCLUDED BY THE QUERY, NOT BY THE SERIALISER. The
// portal's comment read is its own SQL statement carrying a literal
// `visibility = 'public'`, not a shared statement with a parameter. A
// parameter is one bad call site away from a disclosure; a literal in a
// portal-only query cannot be passed the wrong value.
package portal

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Requester is an external person who raises requests without an account.
type Requester struct {
	ID                uuid.UUID
	OrgID             uuid.UUID
	Email             string
	DisplayName       string
	IsActive          bool
	SessionGeneration int
	CreatedAt         time.Time
}

// RequesterIdentity is the subset of a Requester that the AGENT side may see:
// who raised this ticket, and how to reach them.
//
// It is a separate type from Requester deliberately. Requester carries
// IsActive and SessionGeneration — the state the portal guard compares on
// every request — and those must never reach an agent response merely because
// somebody serialised the struct they had to hand. A type that does not carry
// a field cannot leak it.
//
// This travels the agent wire only. The portal's own wire types stay in
// internal/core/api/portal and gain nothing from this; see
// TestPortal_WireCarriesNoContainerContext.
type RequesterIdentity struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
}

// Portal is one Beacon space's customer-facing surface.
type Portal struct {
	ID      uuid.UUID
	SpaceID uuid.UUID
	OrgID   uuid.UUID
	// Key is the opaque public identifier that appears in portal URLs. It is
	// random rather than derived from the space's slug, key or name, all of
	// which describe the internal organisation of the product — see migration
	// 044's format CHECK.
	Key     string
	Name    string
	Intro   string
	Enabled bool
}

// Session is the authenticated portal principal placed on the request context
// by the portal guard.
//
// It carries SpaceID because the portal's own queries need it to reach
// tickets, and OrgID because audit writes need it. Neither is ever
// serialised: the wire DTOs in internal/core/api/portal are separate structs
// that do not have the fields.
type Session struct {
	RequesterID uuid.UUID
	PortalID    uuid.UUID
	SpaceID     uuid.UUID
	OrgID       uuid.UUID
	Email       string
	DisplayName string
}

// Request is the portal's view of a ticket. Compare tickets.Ticket, which
// carries space_id, number, priority, reporter_id, assignee_id, labels, rank
// and resolved_at — none of which appear here, and none of which the portal
// query selects.
type Request struct {
	ID          uuid.UUID
	Summary     string
	Description string
	// Status is the raw ticket status. The API layer maps it to
	// requester-facing language; the domain keeps the truth.
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message is the portal's view of one public comment on a request.
type Message struct {
	ID uuid.UUID
	// AuthorLabel is a display name. For an agent it is the agent's display
	// name, which a public reply inherently reveals — an agent choosing to
	// answer the customer is choosing to be seen answering. For the requester
	// it is their own name.
	AuthorLabel string
	// FromRequester distinguishes the requester's own messages from the
	// service desk's, so the portal can align the thread without needing
	// author identifiers on the wire.
	FromRequester bool
	Body          string
	CreatedAt     time.Time
}

// NewRequest is the requester-safe submission payload. It is deliberately
// narrower than tickets.CreateTicketParams: no priority (a requester does not
// set their own urgency), no assignee, no labels, no due date.
type NewRequest struct {
	Summary     string
	Description string
}

// WorkflowPositioner resolves where a NEW entity in a space starts — the same
// seam the internal ticket and project-item create paths use
// (tiergate.Gate.InitialPosition), so a portal-raised ticket is born in exactly
// the state an agent-raised one would be. ok=false means the space has no
// workflow (or a misconfigured one) and the caller keeps the literal default,
// which is what the internal paths do too.
//
// It is an interface here, satisfied by *tiergate.Gate, rather than a direct
// dependency, so the core portal package does not import the API layer.
type WorkflowPositioner interface {
	InitialPosition(ctx context.Context, spaceID uuid.UUID) (status string, stateID *uuid.UUID, ok bool)
}

// UpdatePortalParams is a partial update to a portal's configuration. A nil
// field means "leave the stored value alone" — the API layer has already
// resolved JSON presence, so by this point absent and explicit-null have been
// collapsed into whatever each field's semantics demand (a null name is a
// validation error, a null intro clears it).
//
// The portal key is deliberately not here. It is never updatable: a rename or
// a toggle that regenerated it would invalidate every URL already handed to a
// customer.
type UpdatePortalParams struct {
	Enabled *bool
	Name    *string
	Intro   *string
}

// Store is the persistence seam. Implemented by
// internal/db/adapters.PortalAdapter.
type Store interface {
	// PortalByKey returns the enabled portal with this public key.
	PortalByKey(ctx context.Context, key string) (Portal, error)
	// PortalBySpace returns a space's portal whether or not it is enabled.
	PortalBySpace(ctx context.Context, spaceID uuid.UUID) (Portal, error)
	// PortalByID returns an ENABLED portal by id. Used when rebuilding a
	// session, so that disabling a portal ends its outstanding sessions on
	// their next request rather than at their next expiry.
	PortalByID(ctx context.Context, id uuid.UUID) (Portal, error)
	// CreatePortal opts a Beacon space in.
	CreatePortal(ctx context.Context, p Portal, createdBy uuid.UUID) (Portal, error)
	// UpdatePortal applies a partial update — enabled, name, intro; nil means
	// "leave it alone" — without destroying the portal's key, so that
	// disabling and re-enabling (or renaming) does not orphan every link
	// already sent. The space predicate lives in the UPDATE statement itself:
	// an update addressed at a space whose portal this is not affects zero
	// rows and returns ErrPortalNotFound.
	UpdatePortal(ctx context.Context, spaceID uuid.UUID, params UpdatePortalParams) (Portal, error)

	// UpsertRequester finds or creates the requester for (org, email). The
	// upsert is atomic so two simultaneous first-time link requests from one
	// address cannot produce two identities.
	UpsertRequester(ctx context.Context, orgID uuid.UUID, email, displayName string) (Requester, error)
	// RequesterByID returns one requester.
	RequesterByID(ctx context.Context, id uuid.UUID) (Requester, error)
	// RequesterState returns the live is_active and session_generation for
	// the portal guard's per-request revocation check.
	RequesterState(ctx context.Context, requesterID uuid.UUID) (RequesterState, error)
	// BumpRequesterSessions invalidates every session a requester holds.
	BumpRequesterSessions(ctx context.Context, requesterID uuid.UUID) error

	// CreateMagicLink supersedes the requester's outstanding links for this
	// portal and stores the new one, in a single transaction.
	CreateMagicLink(ctx context.Context, requesterID, portalID uuid.UUID, tokenHash string, expiresAt time.Time) error
	// ConsumeMagicLink redeems a link by hash. The single-use guard lives in
	// the UPDATE's WHERE clause, so two concurrent redemptions cannot both
	// succeed. Returns ErrInvalidLink when zero rows are affected.
	ConsumeMagicLink(ctx context.Context, tokenHash string) (MagicLinkRedemption, error)

	// CreateRequest writes a portal-originated ticket with requester_id set
	// and reporter_id null. status and workflowStateID are the workflow
	// placement the service resolved for the space (see WorkflowPositioner), so
	// a portal ticket is born in its space workflow's initial state exactly as
	// an agent-created ticket is; a nil workflowStateID with status "open" is
	// the no-workflow default both paths share.
	CreateRequest(ctx context.Context, portalID, spaceID, requesterID uuid.UUID, in NewRequest, status string, workflowStateID *uuid.UUID) (Request, error)
	// ListRequests returns this requester's own requests in this space.
	ListRequests(ctx context.Context, spaceID, requesterID uuid.UUID) ([]Request, error)
	// GetRequest returns one request, scoped to the requester who raised it.
	// A request belonging to someone else is indistinguishable from one that
	// does not exist.
	GetRequest(ctx context.Context, spaceID, requesterID, requestID uuid.UUID) (Request, error)
	// ListPublicMessages returns the PUBLIC comments on a request. The
	// visibility predicate is a literal in the query, not a parameter.
	ListPublicMessages(ctx context.Context, requestID uuid.UUID) ([]Message, error)
	// AppendRequesterMessage writes a requester's reply as a public comment.
	AppendRequesterMessage(ctx context.Context, requestID, requesterID uuid.UUID, body string) (Message, error)
	// AssigneeFor reports the ticket's current assignee, for notification.
	// uuid.Nil means unassigned.
	AssigneeFor(ctx context.Context, requestID uuid.UUID) (uuid.UUID, error)
}

// RequesterState is the live revocation state the portal guard reads once per
// request — the requester-side counterpart of auth.State.
type RequesterState struct {
	IsActive          bool
	SessionGeneration int
}

// MagicLinkRedemption is what a successfully consumed link yields.
type MagicLinkRedemption struct {
	RequesterID uuid.UUID
	PortalID    uuid.UUID
}

// Sender delivers a sign-in link. Implemented over internal/core/email; the
// "link" delivery mode never calls it. Mirrors invites.Sender.
type Sender interface {
	SendMagicLink(ctx context.Context, toEmail, portalName, linkURL string, expiresAt time.Time) error
}
