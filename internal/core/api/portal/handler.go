// Package portal serves the customer portal: the unauthenticated sign-in
// surface and the requester-authenticated request surface.
//
// THE WIRE TYPES IN THIS FILE ARE THE ZERO-CONTEXT GUARANTEE. They are
// separate structs, not reuses of tickets.Ticket or the comment DTO, and they
// carry no space id, space key, space name, slug, ticket number, assignee,
// reporter, priority, labels, rank or workflow state. That is deliberate and
// it is the enforcement point: a field that does not exist on the struct
// cannot be leaked by a serialiser change, a struct embed, or a future author
// who reaches for the richer type because it was already there.
// TestPortalWire_CarriesNoContainerContext asserts the exact key set of every
// response, so adding a field to one of these types fails a test rather than
// quietly widening what an external customer can see.
//
// This mirrors internal/core/api/shares/reader.go, which solves the same
// problem for entity shares and states the rule the same way: the mapping is
// where stripping is enforced, by never copying the field in.
package portal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/audit"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/portal"
	"github.com/Azimuthal-HQ/azimuthal/internal/jobs"
)

// Handler serves the portal API.
type Handler struct {
	svc      *portal.Service
	auditLog audit.Logger
	notifs   notifyFunc
	// spaceTypes resolves a space's module, so the agent-side create route can
	// refuse a portal on a Codex or Vector space. A function rather than a
	// queries handle because that is the entire dependency — see
	// SpaceOrgResolver in the router, which takes the same shape for the same
	// reason.
	spaceTypes func(ctx context.Context, spaceID uuid.UUID) (string, error)
}

// notifyFunc is the narrow shape the handler actually uses, so that wiring a
// real queue and wiring a no-op are the same code path.
type notifyFunc func(args jobs.NotificationArgs)

// NewHandler creates a portal Handler.
func NewHandler(svc *portal.Service) *Handler {
	return &Handler{svc: svc, auditLog: audit.NewLogger(), notifs: func(jobs.NotificationArgs) {}}
}

// WithAuditLogger attaches an audit logger.
func (h *Handler) WithAuditLogger(l audit.Logger) *Handler {
	h.auditLog = l
	return h
}

// WithSpaceTypes attaches the space-module resolver used by the agent-side
// create route.
func (h *Handler) WithSpaceTypes(f func(ctx context.Context, spaceID uuid.UUID) (string, error)) *Handler {
	h.spaceTypes = f
	return h
}

// WithNotifier attaches the notification dispatcher used when a requester
// replies.
func (h *Handler) WithNotifier(f func(jobs.NotificationArgs)) *Handler {
	if f != nil {
		h.notifs = f
	}
	return h
}

// ── Wire types ───────────────────────────────────────────────────────────

// portalView is what an anonymous visitor learns about a service desk before
// signing in: its display name and its blurb. Not the space it belongs to,
// not the organisation, not how many requests exist.
type portalView struct {
	Name  string `json:"name"`
	Intro string `json:"intro"`
}

type requestLinkRequest struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// requestLinkResponse is deliberately uninformative. It says the same thing
// for a known address, an unknown one and a deactivated one — see
// portal.Service.RequestLink. MagicLinkURL is populated only in development
// and test, where config permits disclosure.
type requestLinkResponse struct {
	Status       string `json:"status"`
	Delivered    bool   `json:"delivered"`
	MagicLinkURL string `json:"magic_link_url,omitempty"`
}

type redeemRequest struct {
	Token string `json:"token"`
}

type sessionResponse struct {
	SessionToken string     `json:"session_token"`
	ExpiresIn    int        `json:"expires_in"`
	Requester    requesterV `json:"requester"`
	Portal       portalView `json:"portal"`
}

type requesterV struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

type newRequestBody struct {
	Summary     string `json:"summary"`
	Description string `json:"description"`
}

// requestView is the portal's whole view of a ticket.
//
// Reference is the ticket id, used as an opaque handle. It is NOT the
// "BEA-42" form the internal product shows, because that is composed from the
// SPACE KEY (tickets.ComposeRef) and would tell an external customer what the
// internal space is called.
type requestView struct {
	Reference string `json:"reference"`
	Summary   string `json:"summary"`
	// Status is requester-facing language, not the internal status string.
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type messageView struct {
	ID            string `json:"id"`
	Author        string `json:"author"`
	FromRequester bool   `json:"from_requester"`
	Body          string `json:"body"`
	CreatedAt     string `json:"created_at"`
}

type requestDetailResponse struct {
	Request  requestView   `json:"request"`
	Messages []messageView `json:"messages"`
}

type replyBody struct {
	Body string `json:"body"`
}

// ── Status language ──────────────────────────────────────────────────────

// requesterStatus maps an internal status to what a customer is told.
//
// The internal vocabulary leaks process: "in_progress" and a workflow state
// name like "Awaiting third party" describe how the team works, and
// tickets.status has NO database CHECK — the workflow transition route writes
// arbitrary state names into it — so the portal cannot assume the four Go
// constants. Anything unrecognised therefore falls through to "In progress"
// rather than being shown raw, which is the fail-closed direction: a customer
// seeing a generic status is a small loss, a customer seeing an internal
// workflow state name is a leak of exactly the container context this surface
// exists to withhold.
func requesterStatus(internal string) string {
	switch strings.ToLower(strings.TrimSpace(internal)) {
	case "open":
		return "Received"
	case "in_progress":
		return "In progress"
	case "resolved":
		return "Resolved"
	case "closed":
		return "Closed"
	default:
		return "In progress"
	}
}

// ── Public routes ────────────────────────────────────────────────────────

// PublicRoutes are unauthenticated by design: possession of a magic-link token
// is the credential, exactly as it is for invite acceptance
// (internal/core/api/invites PublicRoutes).
func (h *Handler) PublicRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{portalKey}", h.Describe)
	r.Post("/{portalKey}/auth/request-link", h.RequestLink)
	r.Post("/auth/redeem", h.Redeem)
	return r
}

// Describe returns the portal's public face.
//
// @Summary      Describe a customer portal
// @Description  Returns the public name and introduction for a portal. Unauthenticated.
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Success      200  {object}  portal.portalView
// @Failure      404  {object}  api.SwaggerErrorResponse  "No such portal"
// @Router       /portal/{portalKey} [get]
func (h *Handler) Describe(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.LookupPortal(r.Context(), chi.URLParam(r, "portalKey"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "portal not found")
		return
	}
	respond.JSON(w, http.StatusOK, portalView{Name: p.Name, Intro: p.Intro})
}

// RequestLink issues a sign-in link.
//
// ALWAYS 202, WHATEVER HAPPENED. A known address, an unknown one and a
// deactivated requester all produce the same body. The endpoint is
// unauthenticated, so any difference between those cases would be a free
// oracle for testing whether an address has ever contacted this service desk.
// Login takes the same posture for the same reason.
//
// @Summary      Request a portal sign-in link
// @Description  Sends a single-use sign-in link. Always answers 202, whether or not the address is known.
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Param        request  body  portal.requestLinkRequest  true  "Email address"
// @Success      202  {object}  portal.requestLinkResponse
// @Failure      400  {object}  api.SwaggerErrorResponse  "Malformed address"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No such portal"
// @Router       /portal/{portalKey}/auth/request-link [post]
func (h *Handler) RequestLink(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.LookupPortal(r.Context(), chi.URLParam(r, "portalKey"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "portal not found")
		return
	}

	var req requestLinkRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	issued, err := h.svc.RequestLink(r.Context(), p, req.Email, req.Name)
	if errors.Is(err, portal.ErrInvalidEmail) {
		// A string that is not an address at all is a client bug, not an
		// enumeration probe, and accepting it would create junk identities.
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a valid email address is required")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not issue a sign-in link")
		return
	}

	respond.JSON(w, http.StatusAccepted, requestLinkResponse{
		Status:       "sent",
		Delivered:    issued.Delivered,
		MagicLinkURL: issued.URL,
	})
}

// Redeem exchanges a magic-link token for a portal session.
//
// @Summary      Redeem a portal sign-in link
// @Description  Exchanges a single-use sign-in token for a portal session token.
// @Tags         portal
// @Param        request  body  portal.redeemRequest  true  "Sign-in token"
// @Success      200  {object}  portal.sessionResponse
// @Failure      400  {object}  api.SwaggerErrorResponse  "Malformed body"
// @Failure      401  {object}  api.SwaggerErrorResponse  "Unknown, used, superseded or expired link"
// @Router       /portal/auth/redeem [post]
func (h *Handler) Redeem(w http.ResponseWriter, r *http.Request) {
	var req redeemRequest
	if err := respond.DecodeJSON(r, &req); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	token, sess, err := h.svc.Redeem(r.Context(), req.Token)
	if err != nil {
		// One answer for unknown, consumed, superseded, expired and
		// deactivated. See portal.ErrInvalidLink.
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "this sign-in link is no longer valid")
		return
	}

	p, err := h.svc.LookupPortalByID(r.Context(), sess.PortalID)
	if err != nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "this sign-in link is no longer valid")
		return
	}

	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypePortalSignIn, ActorID: sess.RequesterID.String(),
		OrgID: sess.OrgID.String(), ResourceType: "requester", ResourceID: sess.RequesterID.String(),
	})

	respond.JSON(w, http.StatusOK, sessionResponse{
		SessionToken: token,
		ExpiresIn:    int(h.svc.SessionTTL().Seconds()),
		Requester:    requesterV{Email: sess.Email, Name: sess.DisplayName},
		Portal:       portalView{Name: p.Name, Intro: p.Intro},
	})
}

// ── Session routes ───────────────────────────────────────────────────────

// SessionRoutes are guarded by RequirePortalSession, which the router mounts.
func (h *Handler) SessionRoutes() chi.Router {
	r := chi.NewRouter()
	r.Post("/requests", h.Submit)
	r.Get("/requests", h.List)
	r.Get("/requests/{requestID}", h.Get)
	r.Post("/requests/{requestID}/replies", h.Reply)
	r.Post("/auth/sign-out", h.SignOut)
	return r
}

// Submit raises a request.
//
// @Summary      Submit a request
// @Security     PortalSessionAuth
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Param        request  body  portal.newRequestBody  true  "Summary and description"
// @Success      201  {object}  portal.requestView
// @Failure      400  {object}  api.SwaggerErrorResponse  "Missing summary"
// @Failure      401  {object}  api.SwaggerErrorResponse  "No portal session"
// @Router       /portal/{portalKey}/my/requests [post]
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	sess := portal.SessionFromContext(r.Context())
	if sess == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
		return
	}

	var body newRequestBody
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	req, err := h.svc.Submit(r.Context(), *sess, portal.NewRequest{
		Summary: body.Summary, Description: body.Description,
	})
	if errors.Is(err, portal.ErrSummaryRequired) {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a summary is required")
		return
	}
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not submit your request")
		return
	}

	_ = h.auditLog.Log(r.Context(), audit.Event{
		Type: audit.EventTypePortalRequestCreated, ActorID: sess.RequesterID.String(),
		OrgID: sess.OrgID.String(), ResourceType: "ticket", ResourceID: req.ID.String(),
	})

	respond.JSON(w, http.StatusCreated, toRequestView(req, false))
}

// List returns the requester's own requests.
//
// @Summary      List my requests
// @Security     PortalSessionAuth
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Success      200  {array}  portal.requestView
// @Failure      401  {object}  api.SwaggerErrorResponse  "No portal session"
// @Router       /portal/{portalKey}/my/requests [get]
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	sess := portal.SessionFromContext(r.Context())
	if sess == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
		return
	}
	reqs, err := h.svc.ListRequests(r.Context(), *sess)
	if err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not load your requests")
		return
	}
	out := make([]requestView, 0, len(reqs))
	for _, q := range reqs {
		out = append(out, toRequestView(q, false))
	}
	respond.JSON(w, http.StatusOK, out)
}

// Get returns one request and its public message thread.
//
// A request belonging to another requester answers 404, never 403 — a 403
// would confirm it exists (§2.6).
//
// @Summary      Get one of my requests
// @Security     PortalSessionAuth
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Param        requestID  path  string  true  "Request reference"
// @Success      200  {object}  portal.requestDetailResponse
// @Failure      401  {object}  api.SwaggerErrorResponse  "No portal session"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No such request, or not yours"
// @Router       /portal/{portalKey}/my/requests/{requestID} [get]
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	sess := portal.SessionFromContext(r.Context())
	if sess == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "requestID"))
	if err != nil {
		// A malformed reference is answered exactly like an unknown one, so
		// the shape of a valid reference is not confirmed by trial.
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "request not found")
		return
	}

	req, msgs, err := h.svc.GetRequest(r.Context(), *sess, id)
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "request not found")
		return
	}

	out := make([]messageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageView{
			ID: m.ID.String(), Author: m.AuthorLabel, FromRequester: m.FromRequester,
			Body: m.Body, CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	respond.JSON(w, http.StatusOK, requestDetailResponse{
		Request: toRequestView(req, true), Messages: out,
	})
}

// Reply appends the requester's message and tells the assignee.
//
// @Summary      Reply to one of my requests
// @Security     PortalSessionAuth
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Param        requestID  path  string  true  "Request reference"
// @Param        request  body  portal.replyBody  true  "Message text"
// @Success      201  {object}  portal.messageView
// @Failure      400  {object}  api.SwaggerErrorResponse  "Empty message"
// @Failure      401  {object}  api.SwaggerErrorResponse  "No portal session"
// @Failure      404  {object}  api.SwaggerErrorResponse  "No such request, or not yours"
// @Router       /portal/{portalKey}/my/requests/{requestID}/replies [post]
func (h *Handler) Reply(w http.ResponseWriter, r *http.Request) {
	sess := portal.SessionFromContext(r.Context())
	if sess == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "requestID"))
	if err != nil {
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "request not found")
		return
	}

	var body replyBody
	if err := respond.DecodeJSON(r, &body); err != nil {
		respond.Error(w, r, http.StatusBadRequest, respond.CodeBadRequest, "invalid request body")
		return
	}

	msg, err := h.svc.Reply(r.Context(), *sess, id, body.Body)
	switch {
	case errors.Is(err, portal.ErrBodyRequired):
		respond.Error(w, r, http.StatusBadRequest, respond.CodeValidation, "a message is required")
		return
	case errors.Is(err, portal.ErrRequestNotFound):
		respond.Error(w, r, http.StatusNotFound, respond.CodeNotFound, "request not found")
		return
	case err != nil:
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not send your reply")
		return
	}

	h.notifyAssignee(r, *sess, id)

	respond.JSON(w, http.StatusCreated, messageView{
		ID: msg.ID.String(), Author: sess.DisplayName, FromRequester: true,
		Body: msg.Body, CreatedAt: msg.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// SignOut revokes every session this requester holds.
//
// Unlike POST /auth/logout on the internal side — which revokes database
// sessions production never creates and leaves the caller's JWT valid until it
// expires — this bumps session_generation, so the token stops working on its
// very next use.
//
// @Summary      Sign out of the portal
// @Security     PortalSessionAuth
// @Tags         portal
// @Param        portalKey  path  string  true  "Opaque portal key"
// @Success      204  "Signed out"
// @Failure      401  {object}  api.SwaggerErrorResponse  "No portal session"
// @Router       /portal/{portalKey}/my/auth/sign-out [post]
func (h *Handler) SignOut(w http.ResponseWriter, r *http.Request) {
	sess := portal.SessionFromContext(r.Context())
	if sess == nil {
		respond.Error(w, r, http.StatusUnauthorized, respond.CodeUnauthorized, "sign in to continue")
		return
	}
	if err := h.svc.SignOut(r.Context(), sess.RequesterID); err != nil {
		respond.Error(w, r, http.StatusInternalServerError, respond.CodeInternal, "could not sign you out")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notifyAssignee tells the ticket's assignee that the customer replied.
//
// Best-effort and unassigned-safe. This is the first notification the comment
// path has ever emitted: the comments handler has held a NotificationEnqueuer
// since it was written, main.go wires a live queue into it, and it has never
// been read (see the phase report). A customer reply that nobody is told about
// is the failure mode that matters most here, so the portal wires it rather
// than inheriting the gap.
func (h *Handler) notifyAssignee(r *http.Request, sess portal.Session, requestID uuid.UUID) {
	assignee, err := h.svc.AssigneeFor(r.Context(), requestID)
	if err != nil || assignee == uuid.Nil {
		return
	}
	h.notifs(jobs.NotificationArgs{
		UserID:     assignee.String(),
		EventKind:  "portal.reply",
		Message:    "A customer replied to a request you are assigned",
		ResourceID: requestID.String(),
		EntityKind: "ticket",
		SpaceID:    sess.SpaceID.String(),
	})
}

func toRequestView(q portal.Request, withBody bool) requestView {
	v := requestView{
		Reference: q.ID.String(),
		Summary:   q.Summary,
		Status:    requesterStatus(q.Status),
		CreatedAt: q.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: q.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if withBody {
		v.Description = q.Description
	}
	return v
}
