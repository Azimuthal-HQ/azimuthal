// Package credlink is the internal-user credential-link machinery: one
// single-use, self-expiring, hashed-at-rest token backing three credential
// handoffs —
//
//   - an admin-issued SIGN-IN link (the account is created with a default grant
//     and no password; the user sets their own password on redemption);
//   - a PASSWORD RESET, self-service (forgot-password) or admin-issued, for the
//     deployments with no SSO/LDAP — which is all of them;
//   - the confirmation step that closes the authenticated EMAIL-CHANGE vector
//     (security finding C.2-c: the old path rebound an account's email with no
//     reauthentication, no confirmation and no token_generation bump).
//
// It is shaped VERBATIM on the customer-portal magic link (internal/core/portal)
// and the invite token (internal/core/invites), which have already solved this
// problem correctly: 32 bytes of crypto/rand as URL-safe base64, only the
// SHA-256 hex digest stored, the raw token returned exactly once and never
// persisted or logged; single use enforced by a guarded UPDATE, not a pre-check;
// issue-supersedes-outstanding. There is no second token discipline here.
package credlink

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Purpose is what a credential link does on consume. The three values match the
// credential_links.purpose CHECK (migration 056).
type Purpose string

const (
	// PurposeSignIn: the redeemer sets a password and is signed in.
	PurposeSignIn Purpose = "signin"
	// PurposePasswordReset: the redeemer sets a password; every existing session
	// dies (generation bump + session revocation). No auto sign-in — a reset is a
	// break-glass event, and the user then authenticates fresh.
	PurposePasswordReset Purpose = "password_reset"
	// PurposeEmailChange: the pending new address is bound and generation bumped.
	PurposeEmailChange Purpose = "email_change"
)

// Valid reports whether p is one of the three known purposes.
func (p Purpose) Valid() bool {
	switch p {
	case PurposeSignIn, PurposePasswordReset, PurposeEmailChange:
		return true
	default:
		return false
	}
}

// Inspection is what a non-consuming lookup returns to the redemption page so it
// can render the right form: the purpose, and — for email_change only — the
// address the link will bind. Never the token, never anything else.
type Inspection struct {
	Purpose  Purpose
	NewEmail string // set only for PurposeEmailChange
}

// Consumed is the outcome of redeeming a link: whose account it acted on, what
// it did, and (email_change) the address it bound. The handler turns a signin
// Consumed into a session; the other two need no follow-up.
type Consumed struct {
	UserID   uuid.UUID
	Purpose  Purpose
	NewEmail string
}

// Issued pairs a freshly minted link with the raw token material that exists
// only in this return value.
type Issued struct {
	// RawToken is shown/returned exactly once and never persisted.
	RawToken string
	// URL is the redemption link embedding the raw token.
	URL string
	// ExpiresAt is when the link stops being redeemable.
	ExpiresAt time.Time
	// Delivered is true when the link was emailed. It is deliberately NOT the
	// same question as "may the caller see the URL": the forgot-password handler
	// never exposes the URL regardless of Delivered, and the email-change handler
	// exposes it only when Delivered is false (no relay).
	Delivered bool
}

// Store is the credential_links persistence contract. Every apply-on-consume
// effect — set password, bind email, revoke sessions, bump token_generation —
// happens in the SAME transaction as the guarded consume, so a burned link and
// its effect commit together or not at all. A link that is consumed but whose
// effect fails leaves the user stranded with a dead link, which is the failure
// this atomicity exists to prevent.
type Store interface {
	// Issue supersedes every outstanding link for (userID, purpose) and inserts a
	// fresh one, in one transaction (issue-supersedes-outstanding). newEmail is
	// required for email_change and must be nil otherwise (the
	// credential_links_payload_shape CHECK enforces this at the database too).
	// createdBy is the acting admin or the user themselves, or nil for the
	// unauthenticated forgot-password path.
	Issue(ctx context.Context, userID uuid.UUID, purpose Purpose, tokenHash string, newEmail *string, expiresAt time.Time, createdBy *uuid.UUID) error

	// Inspect returns the link's purpose (and pending email) without consuming
	// it. Returns ErrInvalidLink for a link that is unknown, consumed, superseded
	// or expired — one answer for all four, so a caller cannot tell which.
	Inspect(ctx context.Context, tokenHash string) (Inspection, error)

	// Consume redeems a link (the guarded single-use UPDATE) and applies its
	// effect atomically:
	//   signin / password_reset -> set passwordHash (bumps token_generation);
	//     password_reset also revokes all of the user's sessions.
	//   email_change            -> bind the pending new_email (bumps generation).
	// Returns ErrInvalidLink (unknown/consumed/superseded/expired/dead account),
	// ErrPasswordRequired (a password purpose reached with a nil hash) or
	// ErrEmailTaken (the pending address now collides in the user's org).
	Consume(ctx context.Context, tokenHash string, passwordHash *string) (Consumed, error)

	// CreateUserWithSignInLink provisions a member in orgID — user row (no
	// password), org membership, and default-team enrolment (ADR-0006: never
	// teamless) — and mints its sign-in link, in one transaction. Returns
	// ErrEmailTaken when the address is already a user in orgID.
	CreateUserWithSignInLink(ctx context.Context, p NewUser, tokenHash string, expiresAt time.Time) (uuid.UUID, error)

	// FindUserInOrg resolves a user by email WITHIN orgID, returning found=false
	// when the address is not a member here — including when it exists only in
	// another org (per-org unique email). Org-scoped by necessity, never the
	// global lookup: it backs both the admin reset-link path (found=false ->
	// ErrUserNotFound, indistinguishable from never-existed) and the email-change
	// collision check (found=true -> ErrEmailTaken).
	FindUserInOrg(ctx context.Context, orgID uuid.UUID, email string) (userID uuid.UUID, found bool, err error)
}

// NewUser carries the fields for admin-provisioning an account behind a sign-in
// link.
type NewUser struct {
	OrgID       uuid.UUID
	Email       string
	DisplayName string
	// Role is the org membership role (owner/admin/member). Validated by the
	// caller against the org-role vocabulary.
	Role string
	// CreatedBy is the admin minting the account and its link.
	CreatedBy uuid.UUID
}

// Sender delivers a credential link out of band. Mirrors invites.Sender /
// portal.Sender; the link-delivery (no-SMTP) path never calls it.
type Sender interface {
	SendCredentialLink(ctx context.Context, toEmail string, purpose Purpose, linkURL string, expiresAt time.Time) error
}

// Config holds credential-link behaviour.
type Config struct {
	// TTL is the redemption window for all three purposes (default 60m,
	// AZIMUTHAL_CREDENTIAL_LINK_TTL).
	TTL time.Duration
	// BaseURL prefixes generated link URLs, e.g. "https://azimuthal.example.com".
	BaseURL string
	// DeliverByEmail is true when a mail relay is configured (Config.SMTPConfigured).
	// It decides whether forgot-password and email-change email the link — and,
	// for email-change, whether the URL is instead returned to the reauthenticated
	// requester (the no-relay trade). Forgot-password NEVER returns the URL under
	// any configuration.
	DeliverByEmail bool
}

// Domain errors. Collapsed on purpose where discrimination would help an
// attacker: ErrInvalidLink is the single answer for every redemption failure.
var (
	// ErrInvalidLink is returned for a link that is unknown, already consumed,
	// superseded by a newer one, expired, or bound to a dead account. One error
	// for all of them — telling a caller which check failed tells an attacker
	// which one to work on.
	ErrInvalidLink = errors.New("credential link is invalid or has expired")
	// ErrPasswordRequired is a password-setting purpose (signin, password_reset)
	// reached with no password. Internal to the consume flow; the redemption page
	// learns the purpose from Inspect first and always sends one.
	ErrPasswordRequired = errors.New("a password is required to redeem this link")
	// ErrPasswordTooShort mirrors the invite rule.
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	// ErrEmailTaken is the pending email colliding with an existing account in
	// the org — at request time or, racing, at consume time.
	ErrEmailTaken = errors.New("that email address is already in use")
	// ErrInvalidEmail is a malformed address.
	ErrInvalidEmail = errors.New("a valid email address is required")
	// ErrUserNotFound is an org-scoped user lookup that resolved nothing — an
	// admin naming a person who is not a member of their org. The handler maps it
	// to the same 404 as never-existed.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidRole is an org membership role outside owner/admin/member.
	ErrInvalidRole = errors.New("invalid org role")
	// MinPasswordLength is the shared floor.
	// (Kept a var-adjacent const below for the length check.)
)

// MinPasswordLength is the minimum length for a password set through a
// credential link, matching the invite flow.
const MinPasswordLength = 8
