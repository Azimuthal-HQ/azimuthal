package credlink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// UserResolver resolves the account for the org-less forgot-password flow. It is
// the ONE global lookup this package makes, and exactly the sanctioned caller
// auth.UserRepository.GetByEmailAcrossOrgs documents (login being the other).
type UserResolver interface {
	GetUserByEmailAcrossOrgs(ctx context.Context, email string) (*auth.User, error)
}

// Service implements the credential-link lifecycle over a Store.
type Service struct {
	store  Store
	users  UserResolver
	sender Sender
	cfg    Config
}

// NewService creates a credential-link Service. sender may be nil when no relay
// is configured (cfg.DeliverByEmail false).
func NewService(store Store, users UserResolver, sender Sender, cfg Config) *Service {
	if cfg.TTL <= 0 {
		cfg.TTL = 60 * time.Minute
	}
	return &Service{store: store, users: users, sender: sender, cfg: cfg}
}

// Inspect answers the redemption page's pre-submit lookup from a raw token,
// without consuming it. ErrInvalidLink for anything not redeemable.
func (s *Service) Inspect(ctx context.Context, rawToken string) (Inspection, error) {
	if rawToken == "" {
		return Inspection{}, ErrInvalidLink
	}
	insp, err := s.store.Inspect(ctx, HashToken(rawToken))
	if err != nil {
		return Inspection{}, fmt.Errorf("inspecting credential link: %w", err)
	}
	return insp, nil
}

// Consume redeems a link and applies its effect (see Store.Consume). password is
// required for signin/password_reset and ignored for email_change; it is
// validated and hashed here, BEFORE the guarded consume, so a too-short password
// never burns a link.
func (s *Service) Consume(ctx context.Context, rawToken, password string) (Consumed, error) {
	if rawToken == "" {
		return Consumed{}, ErrInvalidLink
	}
	var hash *string
	if password != "" {
		if len(password) < MinPasswordLength {
			return Consumed{}, ErrPasswordTooShort
		}
		h, err := auth.HashPassword(password)
		if err != nil {
			return Consumed{}, fmt.Errorf("hashing password: %w", err)
		}
		hash = &h
	}
	consumed, err := s.store.Consume(ctx, HashToken(rawToken), hash)
	if err != nil {
		return Consumed{}, fmt.Errorf("consuming credential link: %w", err)
	}
	return consumed, nil
}

// RequestReset is the self-service forgot-password flow. It resolves the address
// GLOBALLY (org-less, like login), and for a live account issues a reset link
// and, when a relay is configured, emails it. It answers IDENTICALLY whether the
// address is known: no error and no observable difference for an unknown or
// deactivated address, so the endpoint is not an account-existence oracle. It
// NEVER returns the URL — this is an unauthenticated endpoint, and disclosure
// here is the portal-disclosure bug all over again; the admin-issued link is the
// no-relay answer.
func (s *Service) RequestReset(ctx context.Context, email string) error {
	email = NormalizeEmail(email)
	if _, err := mail.ParseAddress(email); err != nil {
		// A malformed address is a client bug, not an enumeration probe; but it
		// must not reveal anything either, so treat it as a no-op success.
		return nil
	}
	u, err := s.users.GetUserByEmailAcrossOrgs(ctx, email)
	if err != nil {
		// Unknown address (ErrNotFound) — indistinguishable from success. A
		// genuine infra error is swallowed too: a 500 only for known addresses
		// would itself be an oracle. It is logged for the operator.
		if !isNotFound(err) {
			slog.Error("credlink: forgot-password lookup failed", "error", err)
		}
		return nil
	}
	if !u.IsActive {
		return nil // silently indistinguishable from success
	}
	issued, err := s.mint(ctx, u.ID, PurposePasswordReset, nil, nil)
	if err != nil {
		slog.Error("credlink: issuing reset link failed", "error", err)
		return nil // still indistinguishable
	}
	s.deliver(ctx, u.Email, PurposePasswordReset, issued)
	return nil
}

// IssueReset is the admin "generate a password-reset link for an existing user"
// path. The user is resolved WITHIN orgID; an address that is not a member here
// (including one that exists only in another org) is ErrUserNotFound —
// indistinguishable, to the admin, from never-existed. The URL is returned to
// the admin exactly once; this is an org-admin-guarded endpoint, so disclosure
// to the caller is safe, unlike the unauthenticated forgot-password path.
func (s *Service) IssueReset(ctx context.Context, orgID uuid.UUID, email string, createdBy uuid.UUID) (Issued, error) {
	email = NormalizeEmail(email)
	if _, err := mail.ParseAddress(email); err != nil {
		return Issued{}, ErrInvalidEmail
	}
	userID, found, err := s.store.FindUserInOrg(ctx, orgID, email)
	if err != nil {
		return Issued{}, fmt.Errorf("resolving user: %w", err)
	}
	if !found {
		return Issued{}, ErrUserNotFound
	}
	return s.mint(ctx, userID, PurposePasswordReset, nil, &createdBy)
}

// RequestEmailChange is the authenticated email-change request (the C.2-c fix).
// The caller has already reauthenticated with the current password — that alone
// closes the token-thief vector. This validates the new address, refuses it if
// it already belongs to someone in the same org, and issues an email_change
// link. With a relay the link goes to the NEW address (proving control of it);
// the handler emails it and hides the URL. Without a relay the handler returns
// the URL to the reauthenticated requester — weaker (no proof of new-address
// control), but the reauth-plus-bump is the security content, stated in the
// handler and the docs.
func (s *Service) RequestEmailChange(ctx context.Context, userID, orgID uuid.UUID, currentEmail, newEmail string) (Issued, error) {
	newEmail = NormalizeEmail(newEmail)
	if _, err := mail.ParseAddress(newEmail); err != nil {
		return Issued{}, ErrInvalidEmail
	}
	if newEmail == NormalizeEmail(currentEmail) {
		return Issued{}, ErrEmailTaken // it is already yours; nothing to confirm
	}
	if _, found, err := s.store.FindUserInOrg(ctx, orgID, newEmail); err != nil {
		return Issued{}, fmt.Errorf("checking address: %w", err)
	} else if found {
		return Issued{}, ErrEmailTaken
	}
	issued, err := s.mint(ctx, userID, PurposeEmailChange, &newEmail, &userID)
	if err != nil {
		return Issued{}, err
	}
	// With a relay the link goes to the NEW address, proving control of it; the
	// handler then hides the URL. Without a relay Delivered stays false and the
	// handler returns the URL to the reauthenticated requester (the no-relay
	// trade).
	issued.Delivered = s.deliver(ctx, newEmail, PurposeEmailChange, issued)
	return issued, nil
}

// CreateUserWithSignInLink provisions a member and mints its sign-in link (admin
// path). The account is created with a default grant and NO password; the user
// sets one when they redeem. The URL is returned to the admin exactly once.
func (s *Service) CreateUserWithSignInLink(ctx context.Context, p NewUser) (Issued, uuid.UUID, error) {
	p.Email = NormalizeEmail(p.Email)
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if _, err := mail.ParseAddress(p.Email); err != nil {
		return Issued{}, uuid.Nil, ErrInvalidEmail
	}
	if p.DisplayName == "" {
		return Issued{}, uuid.Nil, fmt.Errorf("%w: display name is required", ErrInvalidEmail)
	}
	switch p.Role {
	case "":
		p.Role = "member"
	case "owner", "admin", "member":
		// valid
	default:
		return Issued{}, uuid.Nil, ErrInvalidRole
	}

	raw, hash, err := generateToken()
	if err != nil {
		return Issued{}, uuid.Nil, fmt.Errorf("generating token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(s.cfg.TTL)
	userID, err := s.store.CreateUserWithSignInLink(ctx, p, hash, expiresAt)
	if err != nil {
		return Issued{}, uuid.Nil, fmt.Errorf("creating user with sign-in link: %w", err)
	}
	return Issued{RawToken: raw, URL: s.linkURL(raw), ExpiresAt: expiresAt}, userID, nil
}

// mint generates a token, supersedes outstanding links for (userID, purpose) and
// stores the new one, returning the raw material (never persisted).
func (s *Service) mint(ctx context.Context, userID uuid.UUID, purpose Purpose, newEmail *string, createdBy *uuid.UUID) (Issued, error) {
	raw, hash, err := generateToken()
	if err != nil {
		return Issued{}, fmt.Errorf("generating token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(s.cfg.TTL)
	if err := s.store.Issue(ctx, userID, purpose, hash, newEmail, expiresAt, createdBy); err != nil {
		return Issued{}, fmt.Errorf("issuing link: %w", err)
	}
	return Issued{RawToken: raw, URL: s.linkURL(raw), ExpiresAt: expiresAt}, nil
}

// deliver emails the link when a relay is configured. Best-effort — a send
// failure never fails the request, and never changes what the caller observes,
// so it cannot leak "this address exists and our SMTP is down" by timing.
func (s *Service) deliver(ctx context.Context, toEmail string, purpose Purpose, issued Issued) bool {
	if !s.cfg.DeliverByEmail || s.sender == nil {
		return false
	}
	if err := s.sender.SendCredentialLink(ctx, toEmail, purpose, issued.URL, issued.ExpiresAt); err != nil {
		slog.Error("credlink: delivering link failed", "purpose", purpose, "error", err)
		return false
	}
	return true
}

func (s *Service) linkURL(rawToken string) string {
	return strings.TrimRight(s.cfg.BaseURL, "/") + "/credential/" + rawToken
}

// NormalizeEmail lowercases and trims an address — the canonical form for
// storage and lookup, matching invites.NormalizeEmail.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// HashToken produces the SHA-256 hex digest stored in credential_links.token_hash
// — the same construction invites, portal links and sessions use.
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

// isNotFound reports whether err is auth's not-found sentinel.
func isNotFound(err error) bool {
	return errors.Is(err, auth.ErrNotFound)
}
