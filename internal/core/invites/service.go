// Package invites implements the organisation invite lifecycle (P2.5 W2):
// create, list, revoke, resend, accept. The raw token is generated with
// crypto/rand, shown once, and never persisted — only its SHA-256 is stored.
// Acceptance for an email that already has an account adds a membership to
// that account; it never creates a second user or a second org (this is a
// different path from the register provisioner, deliberately).
package invites

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/rbac"
)

// Invite is one pending invitation into an org.
type Invite struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Email     string
	OrgRole   string
	TeamID    *uuid.UUID
	InvitedBy uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
	// InvitedByName and TeamName are display joins on list rows.
	InvitedByName string
	TeamName      string
}

// IsExpired reports whether the invite is past its expiry.
func (i *Invite) IsExpired() bool { return time.Now().UTC().After(i.ExpiresAt) }

// AcceptOutcome describes what acceptance did.
type AcceptOutcome struct {
	// User is the account the invite attached to — freshly created, or the
	// pre-existing account holding the invited email.
	User *auth.User
	// ExistingAccount is true when acceptance added a membership to an
	// account that already existed (the caller signs in with their
	// existing password; no tokens are minted for them).
	ExistingAccount bool
	OrgID           uuid.UUID
	OrgSlug         string
	OrgName         string
}

// Store is the persistence contract, implemented by internal/db/adapters
// against real PostgreSQL. Accept is transactional: user creation (when
// needed), membership, team enrolment, and marking the invite accepted
// commit together or not at all.
type Store interface {
	// Create persists a new invite. Returns ErrDuplicateInvite when an
	// active invite for the email exists, ErrAlreadyMember when the email
	// belongs to an account that is already an org member, and
	// ErrTeamNotFound when teamID is set but not a live team of the org.
	Create(ctx context.Context, inv Invite, tokenHash string) (Invite, error)
	// GetByID returns an invite scoped to the org. ErrNotFound if absent.
	GetByID(ctx context.Context, orgID, id uuid.UUID) (Invite, error)
	// ListActive returns the org's pending invites (not accepted, not
	// revoked; expired rows included so admins can resend or revoke).
	ListActive(ctx context.Context, orgID uuid.UUID) ([]Invite, error)
	// Revoke marks an active invite revoked. ErrNotFound when the invite
	// does not exist or is no longer active.
	Revoke(ctx context.Context, orgID, id uuid.UUID) error
	// RefreshToken replaces the token hash and expiry of an active invite
	// (resend). ErrNotFound when the invite is not active.
	RefreshToken(ctx context.Context, orgID, id uuid.UUID, newHash string, newExpiry time.Time) (Invite, error)
	// InspectByTokenHash returns the invite for a raw-token lookup along
	// with its org display fields and lifecycle state. ErrNotFound when the
	// hash matches nothing.
	InspectByTokenHash(ctx context.Context, tokenHash string) (Inspection, error)
	// Accept consumes the invite transactionally. newUser is nil when the
	// invited email already has an account. Returns ErrNotFound /
	// ErrRevoked / ErrAlreadyAccepted / ErrExpired for dead invites,
	// ErrAccountInactive when the existing account is deactivated, and
	// ErrDisplayNameAndPasswordRequired when a fresh account is needed but
	// newUser is nil.
	Accept(ctx context.Context, tokenHash string, newUser *NewUser) (AcceptOutcome, error)
}

// Inspection is what the acceptance page needs before submitting: whom the
// invite is for, which org, and whether it is still live.
type Inspection struct {
	Email   string
	OrgName string
	// State is one of "active", "expired", "revoked", "accepted".
	State string
	// ExistingAccount is true when the invited email already has an
	// account, so the acceptance page asks them to confirm joining rather
	// than to choose a display name and password.
	ExistingAccount bool
}

// NewUser carries the fields for creating the account during acceptance.
type NewUser struct {
	DisplayName string
	Password    string
}

// Sender delivers an invite. Implemented over the email package (resolving
// the org's display name itself); the link delivery mode never calls it.
type Sender interface {
	SendInvite(ctx context.Context, toEmail string, orgID uuid.UUID, inviteURL string, expiresAt time.Time) error
}

// Config holds invite behaviour settings.
type Config struct {
	// TTL is the invite expiry window (default seven days, configurable).
	TTL time.Duration
	// DeliverByEmail sends the invite via SMTP on create and resend. When
	// false (link mode, the default) the URL is only returned to the admin.
	DeliverByEmail bool
	// BaseURL prefixes generated invite URLs, e.g. "https://azimuthal.example.com".
	BaseURL string
}

// Service implements the invite lifecycle over a Store.
type Service struct {
	store  Store
	sender Sender
	cfg    Config
}

// NewService creates an invite Service. sender may be nil when
// cfg.DeliverByEmail is false.
func NewService(store Store, sender Sender, cfg Config) *Service {
	if cfg.TTL <= 0 {
		cfg.TTL = 7 * 24 * time.Hour
	}
	return &Service{store: store, sender: sender, cfg: cfg}
}

// Created pairs an invite with the raw token material that exists only in
// this return value.
type Created struct {
	Invite Invite
	// RawToken is shown to the admin exactly once and never persisted.
	RawToken string
	// URL is the acceptance link embedding the raw token.
	URL string
	// Delivered is true when the invite was emailed to the recipient.
	Delivered bool
}

// Create validates and persists a new invite, generating its token.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, email, orgRole string, teamID *uuid.UUID, invitedBy uuid.UUID) (Created, error) {
	email = NormalizeEmail(email)
	if _, err := mail.ParseAddress(email); err != nil {
		return Created{}, ErrInvalidEmail
	}
	if orgRole == "" {
		orgRole = string(rbac.RoleMember)
	}
	if orgRole != string(rbac.RoleMember) && orgRole != string(rbac.RoleAdmin) {
		return Created{}, ErrInvalidOrgRole
	}

	raw, hash, err := generateToken()
	if err != nil {
		return Created{}, fmt.Errorf("generating invite token: %w", err)
	}

	inv, err := s.store.Create(ctx, Invite{
		OrgID:     orgID,
		Email:     email,
		OrgRole:   orgRole,
		TeamID:    teamID,
		InvitedBy: invitedBy,
		ExpiresAt: time.Now().UTC().Add(s.cfg.TTL),
	}, hash)
	if err != nil {
		return Created{}, err
	}

	created := Created{Invite: inv, RawToken: raw, URL: s.inviteURL(raw)}
	created.Delivered = s.deliver(ctx, inv, created.URL)
	return created, nil
}

// Resend rotates the token and expiry of an active invite. The previous
// link stops working immediately.
func (s *Service) Resend(ctx context.Context, orgID, id uuid.UUID) (Created, error) {
	raw, hash, err := generateToken()
	if err != nil {
		return Created{}, fmt.Errorf("generating invite token: %w", err)
	}
	inv, err := s.store.RefreshToken(ctx, orgID, id, hash, time.Now().UTC().Add(s.cfg.TTL))
	if err != nil {
		return Created{}, err
	}
	created := Created{Invite: inv, RawToken: raw, URL: s.inviteURL(raw)}
	created.Delivered = s.deliver(ctx, inv, created.URL)
	return created, nil
}

// List returns the org's pending invites.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Invite, error) {
	return s.store.ListActive(ctx, orgID)
}

// Revoke marks an active invite revoked.
func (s *Service) Revoke(ctx context.Context, orgID, id uuid.UUID) error {
	return s.store.Revoke(ctx, orgID, id)
}

// GetByID returns one invite scoped to the org.
func (s *Service) GetByID(ctx context.Context, orgID, id uuid.UUID) (Invite, error) {
	return s.store.GetByID(ctx, orgID, id)
}

// Inspect answers the acceptance page's pre-submit lookup from a raw token.
func (s *Service) Inspect(ctx context.Context, rawToken string) (Inspection, error) {
	if rawToken == "" {
		return Inspection{}, ErrNotFound
	}
	return s.store.InspectByTokenHash(ctx, HashToken(rawToken))
}

// Accept consumes an invite. When the invited email has no account yet,
// newUser must carry a display name and password; when the email already
// has an account, newUser is ignored and a membership is added to it.
func (s *Service) Accept(ctx context.Context, rawToken string, newUser *NewUser) (AcceptOutcome, error) {
	if rawToken == "" {
		return AcceptOutcome{}, ErrNotFound
	}
	if newUser != nil {
		newUser.DisplayName = strings.TrimSpace(newUser.DisplayName)
		if newUser.DisplayName == "" || newUser.Password == "" {
			// Partially filled registration fields are a validation error
			// rather than a silent fall-through to the existing-account path.
			return AcceptOutcome{}, ErrDisplayNameAndPasswordRequired
		}
		if len(newUser.Password) < 8 {
			return AcceptOutcome{}, ErrPasswordTooShort
		}
	}
	return s.store.Accept(ctx, HashToken(rawToken), newUser)
}

// deliver emails the invite when email delivery is configured. Send
// failures are reported as Delivered=false rather than failing the create —
// the URL is always returned, so the admin can still deliver it by hand.
func (s *Service) deliver(ctx context.Context, inv Invite, url string) bool {
	if !s.cfg.DeliverByEmail || s.sender == nil {
		return false
	}
	if err := s.sender.SendInvite(ctx, inv.Email, inv.OrgID, url, inv.ExpiresAt); err != nil {
		return false
	}
	return true
}

func (s *Service) inviteURL(rawToken string) string {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	return base + "/invite/" + rawToken
}

// NormalizeEmail lowercases and trims an address — the canonical form for
// storage and lookup.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// HashToken produces the SHA-256 hex digest stored in invites.token_hash —
// the same construction the sessions table uses.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// generateToken returns 32 bytes of crypto/rand as URL-safe base64, plus
// its storage hash.
func generateToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, HashToken(raw), nil
}
