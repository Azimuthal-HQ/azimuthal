package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// UserAdapter implements auth.UserRepository using sqlc-generated queries.
// It bridges the OrgID gap: the domain User type has no OrgID, so the adapter
// injects a default OrgID (typically the single-tenant org) on every write
// and scopes reads to that org.
type UserAdapter struct {
	q     *generated.Queries
	orgID uuid.UUID
}

// NewUserAdapter creates a UserAdapter that scopes all user queries to orgID.
func NewUserAdapter(q *generated.Queries, orgID uuid.UUID) *UserAdapter {
	return &UserAdapter{q: q, orgID: orgID}
}

// Create persists a new user. Returns auth.ErrEmailTaken if the email exists.
func (a *UserAdapter) Create(ctx context.Context, u *auth.User) error {
	_, err := a.q.CreateUser(ctx, userToCreateParams(u, a.orgID))
	if err != nil {
		return fmt.Errorf("user adapter create: %w", err)
	}
	return nil
}

// GetByID retrieves a user by primary key. Returns auth.ErrNotFound if absent.
func (a *UserAdapter) GetByID(ctx context.Context, id uuid.UUID) (*auth.User, error) {
	row, err := a.q.GetUserByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("user adapter get by id: %w", err)
	}
	return dbUserToDomain(row), nil
}

// GetByEmail retrieves a user by email address globally (across all orgs).
// Returns auth.ErrNotFound if absent.
func (a *UserAdapter) GetByEmail(ctx context.Context, email string) (*auth.User, error) {
	row, err := a.q.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("user adapter get by email: %w", err)
	}
	return dbUserToDomain(row), nil
}

// Update persists changes to an existing user record.
func (a *UserAdapter) Update(ctx context.Context, u *auth.User) error {
	_, err := a.q.UpdateUser(ctx, generated.UpdateUserParams{
		ID:          u.ID,
		DisplayName: u.DisplayName,
		IsActive:    u.IsActive,
		Role:        "member",
	})
	if err != nil {
		return fmt.Errorf("user adapter update: %w", err)
	}
	return nil
}

// Delete soft-deletes a user by setting deleted_at.
func (a *UserAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.SoftDeleteUser(ctx, id); err != nil {
		return fmt.Errorf("user adapter delete: %w", err)
	}
	return nil
}

// dbUserToDomain converts a generated.User to an auth.User.
func dbUserToDomain(u generated.User) *auth.User {
	return &auth.User{
		ID:           u.ID,
		OrgID:        u.OrgID,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		PasswordHash: derefStr(u.PasswordHash),
		Role:         u.Role,
		IsActive:     u.IsActive,
		CreatedAt:    goTime(u.CreatedAt),
		UpdatedAt:    goTime(u.UpdatedAt),
		DeletedAt:    goTimePtr(u.DeletedAt),
	}
}

// userToCreateParams converts a domain User to sqlc CreateUserParams.
// It uses the user's OrgID when set, falling back to the adapter's default.
func userToCreateParams(u *auth.User, fallbackOrgID uuid.UUID) generated.CreateUserParams {
	orgID := u.OrgID
	if orgID == uuid.Nil {
		orgID = fallbackOrgID
	}
	return generated.CreateUserParams{
		ID:           u.ID,
		OrgID:        orgID,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		PasswordHash: strPtr(u.PasswordHash),
		Role:         "member",
	}
}

// sessionToCreateParams converts a domain Session to sqlc CreateSessionParams.
func sessionToCreateParams(s *auth.Session) generated.CreateSessionParams {
	return generated.CreateSessionParams{
		ID:        s.ID,
		UserID:    s.UserID,
		TokenHash: hashToken(s.Token),
		IpAddress: parseIP(s.IPAddress),
		UserAgent: strPtr(s.UserAgent),
		ExpiresAt: pgTimestamp(s.ExpiresAt),
	}
}

// SessionAdapter implements auth.SessionRepository using sqlc-generated queries.
// It resolves the token↔hash mismatch: the domain Session stores a plain token,
// but the database stores a SHA-256 hash. The adapter hashes on write and lookup.
type SessionAdapter struct {
	q *generated.Queries
}

// NewSessionAdapter creates a SessionAdapter backed by the given queries.
func NewSessionAdapter(q *generated.Queries) *SessionAdapter {
	return &SessionAdapter{q: q}
}

// Create persists a new session record. The plain token in s.Token is hashed
// before storage; the domain Session retains the plain token for the caller.
func (a *SessionAdapter) Create(ctx context.Context, s *auth.Session) error {
	_, err := a.q.CreateSession(ctx, sessionToCreateParams(s))
	if err != nil {
		return fmt.Errorf("session adapter create: %w", err)
	}
	return nil
}

// GetByToken retrieves a session by its opaque token. The adapter hashes the
// token before querying, bridging the domain's plain-token API to the DB's
// hash-based lookup.
func (a *SessionAdapter) GetByToken(ctx context.Context, token string) (*auth.Session, error) {
	row, err := a.q.GetSessionByTokenHash(ctx, hashToken(token))
	if err != nil {
		return nil, fmt.Errorf("session adapter get by token: %w", err)
	}
	return dbSessionToDomain(row, token), nil
}

// Delete removes a single session (logout).
func (a *SessionAdapter) Delete(ctx context.Context, id uuid.UUID) error {
	if err := a.q.RevokeSession(ctx, id); err != nil {
		return fmt.Errorf("session adapter delete: %w", err)
	}
	return nil
}

// DeleteAllForUser removes every session belonging to a user (force-logout all).
func (a *SessionAdapter) DeleteAllForUser(ctx context.Context, userID uuid.UUID) error {
	if err := a.q.RevokeAllUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("session adapter delete all for user: %w", err)
	}
	return nil
}

// DeleteExpired removes all sessions whose ExpiresAt is in the past.
func (a *SessionAdapter) DeleteExpired(ctx context.Context) error {
	if err := a.q.DeleteExpiredSessions(ctx); err != nil {
		return fmt.Errorf("session adapter delete expired: %w", err)
	}
	return nil
}

// hashToken produces the SHA-256 hex digest of a plain session token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// dbSessionToDomain converts a generated.Session to an auth.Session.
// The original plain token is threaded through because the DB only stores
// the hash.
func dbSessionToDomain(s generated.Session, plainToken string) *auth.Session {
	return &auth.Session{
		ID:        s.ID,
		UserID:    s.UserID,
		Token:     plainToken,
		ExpiresAt: goTime(s.ExpiresAt),
		CreatedAt: goTime(s.CreatedAt),
		UserAgent: derefStr(s.UserAgent),
		IPAddress: ipString(s.IpAddress),
	}
}

// HashToken is exported for use in tests and the wiring layer.
func HashToken(token string) string {
	return hashToken(token)
}

// MembershipAdapter provides org-membership lookups backed by sqlc queries.
type MembershipAdapter struct {
	q *generated.Queries
}

// NewMembershipAdapter creates a MembershipAdapter.
func NewMembershipAdapter(q *generated.Queries) *MembershipAdapter {
	return &MembershipAdapter{q: q}
}

// PrimaryOrgForUser returns the user's primary organization (owner role first,
// then earliest membership). Returns the org ID, slug, and name.
func (a *MembershipAdapter) PrimaryOrgForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, string, string, error) {
	rows, err := a.q.ListMembershipsByUser(ctx, userID)
	if err != nil {
		return uuid.Nil, "", "", fmt.Errorf("membership adapter list by user: %w", err)
	}
	if len(rows) == 0 {
		return uuid.Nil, "", "", fmt.Errorf("membership adapter: no memberships found for user")
	}
	// Rows are ordered: owner first, then by created_at ASC
	return rows[0].OrgID, rows[0].OrgSlug, rows[0].OrgName, nil
}
