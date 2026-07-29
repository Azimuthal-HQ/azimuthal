package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Config holds the portal service's boot-time settings.
type Config struct {
	// LinkTTL is how long a sign-in link remains redeemable. Much shorter
	// than an invite's (default 7 days) because a magic link is a credential
	// in an inbox rather than an onboarding step somebody may get to next
	// week.
	LinkTTL time.Duration
	// DeliverByEmail mirrors AZIMUTHAL_PORTAL_LINK_DELIVERY.
	DeliverByEmail bool
	// DiscloseLink returns the sign-in URL in the API response instead of only
	// sending it. This is a DEVELOPMENT AND TEST AFFORDANCE and config
	// rejects it in production — see config.validate. The request-link
	// endpoint is unauthenticated, so a disclosed URL would let any caller
	// sign in as any address they can name.
	DiscloseLink bool
	// BaseURL is the deployment's public base, used to build link URLs.
	BaseURL string
}

// Service implements the portal's flows over a Store.
type Service struct {
	store  Store
	tokens *TokenService
	sender Sender
	cfg    Config
}

// NewService creates a portal Service. sender may be nil in link-delivery
// mode; deliver() checks both the flag and the pointer, as invites does.
func NewService(store Store, tokens *TokenService, sender Sender, cfg Config) *Service {
	if cfg.LinkTTL <= 0 {
		cfg.LinkTTL = time.Hour
	}
	return &Service{store: store, tokens: tokens, sender: sender, cfg: cfg}
}

// LookupPortal resolves a public portal key. Returns ErrPortalNotFound for
// both "no such key" and "disabled", which is the same non-disclosure the
// share family applies to entities.
func (s *Service) LookupPortal(ctx context.Context, key string) (Portal, error) {
	p, err := s.store.PortalByKey(ctx, key)
	if err != nil {
		return Portal{}, fmt.Errorf("looking up portal: %w", err)
	}
	return p, nil
}

// LookupPortalByID resolves an enabled portal by id.
func (s *Service) LookupPortalByID(ctx context.Context, id uuid.UUID) (Portal, error) {
	p, err := s.store.PortalByID(ctx, id)
	if err != nil {
		return Portal{}, fmt.Errorf("looking up portal: %w", err)
	}
	return p, nil
}

// SessionTTL reports how long a redeemed session lasts, for the wire response.
func (s *Service) SessionTTL() time.Duration { return s.tokens.SessionTTL() }

// LinkIssued is the outcome of a sign-in link request.
type LinkIssued struct {
	// URL is populated only when Config.DiscloseLink is set. It is empty in
	// production by construction.
	URL string
	// Delivered reports whether an email was actually sent.
	Delivered bool
}

// RequestLink issues a sign-in link for an address on one portal.
//
// THIS ENDPOINT MUST NOT REVEAL WHETHER THE ADDRESS IS KNOWN. It returns the
// same shape for a first-time address, a returning requester and a
// deactivated one, and the caller answers 202 in every case. Login already
// takes this posture — a wrong password and a deactivated account produce one
// identical 401 — and the reasoning is stronger here, because the caller is
// unauthenticated and the answer would otherwise be a free membership oracle
// for any address an attacker cares to try.
//
// Consequently a deactivated requester is refused SILENTLY: a link is not
// created, and the response is indistinguishable from success.
//
// The requester row is created on first contact. That is not
// self-registration in the sense the phase fence forbids — there is no
// account, no password, no profile and no confirmation step, and nothing is
// granted by the row's existence. It is simply where an account-less identity
// has to come from, since the alternative (an agent pre-creating every
// customer) would make a public service desk unusable.
func (s *Service) RequestLink(ctx context.Context, p Portal, rawEmail, displayName string) (LinkIssued, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		// The one thing worth refusing loudly: a string that is not an email
		// address at all is a client bug, not an enumeration probe, and
		// accepting it would silently create junk identities.
		return LinkIssued{}, err
	}

	req, err := s.store.UpsertRequester(ctx, p.OrgID, email, strings.TrimSpace(displayName))
	if err != nil {
		return LinkIssued{}, fmt.Errorf("resolving requester: %w", err)
	}
	if !req.IsActive {
		// Silently indistinguishable from success. See the doc comment.
		return LinkIssued{}, nil
	}

	raw, hash, err := generateToken()
	if err != nil {
		return LinkIssued{}, err
	}
	expiresAt := time.Now().UTC().Add(s.cfg.LinkTTL)
	if err := s.store.CreateMagicLink(ctx, req.ID, p.ID, hash, expiresAt); err != nil {
		return LinkIssued{}, fmt.Errorf("issuing sign-in link: %w", err)
	}

	url := s.linkURL(p.Key, raw)
	out := LinkIssued{Delivered: s.deliver(ctx, email, p.Name, url, expiresAt)}
	if s.cfg.DiscloseLink {
		out.URL = url
	}
	return out, nil
}

// Redeem exchanges a raw magic-link token for a portal session token.
//
// Single use is enforced inside ConsumeMagicLink's UPDATE, not by a read
// here. Every failure collapses to ErrInvalidLink.
func (s *Service) Redeem(ctx context.Context, rawToken string) (string, Session, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return "", Session{}, ErrInvalidLink
	}

	redemption, err := s.store.ConsumeMagicLink(ctx, HashToken(rawToken))
	if err != nil {
		return "", Session{}, fmt.Errorf("redeeming sign-in link: %w", err)
	}

	sess, err := s.loadSession(ctx, redemption.RequesterID, redemption.PortalID)
	if err != nil {
		return "", Session{}, err
	}

	state, err := s.store.RequesterState(ctx, redemption.RequesterID)
	if err != nil {
		return "", Session{}, fmt.Errorf("reading requester state: %w", err)
	}
	if !state.IsActive {
		return "", Session{}, ErrInvalidLink
	}

	token, err := s.tokens.IssueSession(sess.RequesterID, sess.PortalID, sess.OrgID.String(), state.SessionGeneration)
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// Authenticate validates a portal session token and returns the principal.
//
// This is the guard's whole job, and it is two steps that must both happen:
// the cryptographic check (ValidateSession) and the LIVE revocation check
// against requesters.session_generation. Dropping the second would make a
// deactivated requester's token valid until it expired — the exact hole
// users.token_generation exists to close on the internal side, restated in
// shared-surfaces §8.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	claims, err := s.tokens.ValidateSession(token)
	if err != nil {
		return Session{}, err
	}

	state, err := s.store.RequesterState(ctx, claims.RequesterID)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	if !state.IsActive || state.SessionGeneration != claims.SessionGeneration {
		return Session{}, ErrInvalidSession
	}

	sess, err := s.loadSession(ctx, claims.RequesterID, claims.PortalID)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	return sess, nil
}

// SignOut revokes every session the requester holds. Unlike POST /auth/logout
// — which revokes database sessions this product never creates and leaves the
// caller's JWT valid — this actually invalidates the credential, because the
// generation bump is checked on the next request.
func (s *Service) SignOut(ctx context.Context, requesterID uuid.UUID) error {
	if err := s.store.BumpRequesterSessions(ctx, requesterID); err != nil {
		return fmt.Errorf("signing out requester: %w", err)
	}
	return nil
}

// Submit raises a request. The payload is the requester-safe subset: a
// summary and a description, and nothing else.
func (s *Service) Submit(ctx context.Context, sess Session, in NewRequest) (Request, error) {
	in.Summary = strings.TrimSpace(in.Summary)
	in.Description = strings.TrimSpace(in.Description)
	if in.Summary == "" {
		return Request{}, ErrSummaryRequired
	}
	req, err := s.store.CreateRequest(ctx, sess.PortalID, sess.SpaceID, sess.RequesterID, in)
	if err != nil {
		return Request{}, fmt.Errorf("submitting request: %w", err)
	}
	return req, nil
}

// ListRequests returns the requester's own requests.
//
// Scoping is in the query, by requester_id, not applied to a wider result set
// afterwards. "Per viewer in the strictest sense" means the rows never exist
// in the first place, so there is no filtered-out set for a serialiser bug to
// reveal.
func (s *Service) ListRequests(ctx context.Context, sess Session) ([]Request, error) {
	reqs, err := s.store.ListRequests(ctx, sess.SpaceID, sess.RequesterID)
	if err != nil {
		return nil, fmt.Errorf("listing requests: %w", err)
	}
	return reqs, nil
}

// GetRequest returns one of the requester's own requests with its public
// message thread.
func (s *Service) GetRequest(ctx context.Context, sess Session, requestID uuid.UUID) (Request, []Message, error) {
	req, err := s.store.GetRequest(ctx, sess.SpaceID, sess.RequesterID, requestID)
	if err != nil {
		return Request{}, nil, fmt.Errorf("loading request: %w", err)
	}
	msgs, err := s.store.ListPublicMessages(ctx, req.ID)
	if err != nil {
		return Request{}, nil, fmt.Errorf("loading request messages: %w", err)
	}
	return req, msgs, nil
}

// Reply appends the requester's message to a request they raised.
//
// The comment is written public — enforced by the database, not just by this
// call site (migration 045's comments_requester_public).
func (s *Service) Reply(ctx context.Context, sess Session, requestID uuid.UUID, body string) (Message, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Message{}, ErrBodyRequired
	}
	// Resolve the request under the requester's own scope first: replying to
	// somebody else's request must 404 exactly as reading it does, and doing
	// the ownership check here rather than inside the insert keeps the two
	// answers identical.
	req, err := s.store.GetRequest(ctx, sess.SpaceID, sess.RequesterID, requestID)
	if err != nil {
		return Message{}, fmt.Errorf("loading request: %w", err)
	}
	msg, err := s.store.AppendRequesterMessage(ctx, req.ID, sess.RequesterID, body)
	if err != nil {
		return Message{}, fmt.Errorf("appending reply: %w", err)
	}
	return msg, nil
}

// AssigneeFor reports who should be told about a requester's reply.
func (s *Service) AssigneeFor(ctx context.Context, requestID uuid.UUID) (uuid.UUID, error) {
	id, err := s.store.AssigneeFor(ctx, requestID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolving assignee: %w", err)
	}
	return id, nil
}

// CreatePortal opts a Beacon space into the portal. Agent-side; guarded by
// manage_space at the route.
func (s *Service) CreatePortal(ctx context.Context, spaceID uuid.UUID, spaceType, name, intro string, createdBy uuid.UUID) (Portal, error) {
	if spaceType != "beacon" {
		return Portal{}, ErrNotBeaconSpace
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Portal{}, ErrPortalNameRequired
	}
	key, err := generatePortalKey()
	if err != nil {
		return Portal{}, err
	}
	created, err := s.store.CreatePortal(ctx, Portal{
		SpaceID: spaceID,
		Key:     key,
		Name:    name,
		Intro:   strings.TrimSpace(intro),
		Enabled: true,
	}, createdBy)
	if err != nil {
		return Portal{}, fmt.Errorf("creating portal: %w", err)
	}
	return created, nil
}

// PortalForSpace returns a space's portal configuration, enabled or not.
func (s *Service) PortalForSpace(ctx context.Context, spaceID uuid.UUID) (Portal, error) {
	p, err := s.store.PortalBySpace(ctx, spaceID)
	if err != nil {
		return Portal{}, fmt.Errorf("loading space portal: %w", err)
	}
	return p, nil
}

// SetPortalEnabled enables or disables a space's portal without discarding
// its key, so that re-enabling does not invalidate every URL already shared.
func (s *Service) SetPortalEnabled(ctx context.Context, spaceID uuid.UUID, enabled bool) (Portal, error) {
	p, err := s.store.SetPortalEnabled(ctx, spaceID, enabled)
	if err != nil {
		return Portal{}, fmt.Errorf("setting portal enabled: %w", err)
	}
	return p, nil
}

// RequesterByEmail resolves a requester for the agent-side surface.
func (s *Service) RequesterByEmail(ctx context.Context, orgID uuid.UUID, email string) (Requester, error) {
	norm, err := NormalizeEmail(email)
	if err != nil {
		return Requester{}, err
	}
	req, err := s.store.RequesterByEmail(ctx, orgID, norm)
	if err != nil {
		return Requester{}, fmt.Errorf("looking up requester: %w", err)
	}
	return req, nil
}

// loadSession assembles the principal from the portal row, refusing if the
// portal has since been disabled — so disabling a portal ends its sessions on
// their next request rather than at their next expiry.
func (s *Service) loadSession(ctx context.Context, requesterID, portalID uuid.UUID) (Session, error) {
	p, err := s.store.PortalByID(ctx, portalID)
	if err != nil {
		return Session{}, fmt.Errorf("loading session portal: %w", err)
	}
	r, err := s.store.RequesterByID(ctx, requesterID)
	if err != nil {
		return Session{}, fmt.Errorf("loading session requester: %w", err)
	}
	return Session{
		RequesterID: r.ID,
		PortalID:    p.ID,
		SpaceID:     p.SpaceID,
		OrgID:       p.OrgID,
		Email:       r.Email,
		DisplayName: r.DisplayName,
	}, nil
}

func (s *Service) deliver(ctx context.Context, email, portalName, url string, expiresAt time.Time) bool {
	if !s.cfg.DeliverByEmail || s.sender == nil {
		return false
	}
	if err := s.sender.SendMagicLink(ctx, email, portalName, url, expiresAt); err != nil {
		return false
	}
	return true
}

func (s *Service) linkURL(portalKey, rawToken string) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/portal/" + portalKey + "/signin/" + rawToken
}

// NormalizeEmail lowercases, trims and validates an address. Same
// construction as invites.NormalizeEmail plus the parse invites does at its
// call site, kept together here so the two cannot drift apart.
func NormalizeEmail(raw string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(raw))
	if norm == "" {
		return "", ErrInvalidEmail
	}
	if _, err := mail.ParseAddress(norm); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidEmail, err.Error())
	}
	return norm, nil
}

// HashToken produces the SHA-256 hex digest stored in
// requester_magic_links.token_hash — the same construction invites and
// sessions use.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// generateToken returns 32 bytes of crypto/rand as URL-safe base64, plus its
// storage hash. The raw value is returned exactly once and never persisted.
func generateToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("reading random bytes: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}

// portalKeyLength is the number of characters in a portal's public
// identifier. Twenty characters over a 32-symbol alphabet is 100 bits, which
// is not a security boundary — the portal key is public by design and grants
// nothing on its own — but is comfortably beyond guessing, so a portal is not
// discoverable by enumeration.
const portalKeyLength = 20

// portalKeyAlphabet is RFC 4648 base32 lowercased: no digits that read as
// letters (0/1) and no letters that read as digits. Every character satisfies
// migration 044's `^[a-z0-9]{16,32}$`.
const portalKeyAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// generatePortalKey returns the opaque public identifier for a portal.
//
// Random rather than derived, because this string appears in a URL an
// external requester reads. Every derivable alternative — the space slug, the
// space key, a slug of the space name — describes the internal shape of the
// product to somebody who is not supposed to see it.
//
// One character per random byte, masked to the alphabet. The mask is uniform
// because the alphabet is exactly 32 symbols and 256 is a whole multiple of
// it, so there is no modulo bias to correct for; an alphabet of any other
// size would need rejection sampling here.
func generatePortalKey() (string, error) {
	buf := make([]byte, portalKeyLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	out := make([]byte, portalKeyLength)
	for i, b := range buf {
		out[i] = portalKeyAlphabet[b&31]
	}
	return string(out), nil
}

// IsNotFound reports whether err is one of the portal's not-found sentinels,
// all of which the API layer answers 404 for.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrPortalNotFound) || errors.Is(err, ErrRequestNotFound)
}
