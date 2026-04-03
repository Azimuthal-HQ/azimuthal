package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

func TestDbUserToDomain(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	pwHash := "bcrypt-hash"

	dbUser := generated.User{
		ID:           id,
		OrgID:        uuid.New(),
		Email:        "test@example.com",
		DisplayName:  "Test User",
		PasswordHash: &pwHash,
		Role:         "member",
		IsActive:     true,
		CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		DeletedAt:    pgtype.Timestamptz{},
	}

	got := dbUserToDomain(dbUser)

	if got.ID != id {
		t.Errorf("ID mismatch: got %v, want %v", got.ID, id)
	}
	if got.Email != "test@example.com" {
		t.Errorf("Email mismatch: got %v, want test@example.com", got.Email)
	}
	if got.DisplayName != "Test User" {
		t.Errorf("DisplayName mismatch: got %v, want Test User", got.DisplayName)
	}
	if got.PasswordHash != "bcrypt-hash" {
		t.Errorf("PasswordHash mismatch: got %v, want bcrypt-hash", got.PasswordHash)
	}
	if !got.IsActive {
		t.Error("expected IsActive=true")
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt mismatch: got %v, want %v", got.CreatedAt, now)
	}
	if !got.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("UpdatedAt mismatch")
	}
	if got.DeletedAt != nil {
		t.Errorf("expected nil DeletedAt, got %v", got.DeletedAt)
	}
}

func TestDbUserToDomainNilPasswordHash(t *testing.T) {
	dbUser := generated.User{
		ID:           uuid.New(),
		OrgID:        uuid.New(),
		PasswordHash: nil,
		CreatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := dbUserToDomain(dbUser)
	if got.PasswordHash != "" {
		t.Errorf("expected empty PasswordHash for nil, got %v", got.PasswordHash)
	}
}

func TestDbUserToDomainDeletedAt(t *testing.T) {
	del := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	dbUser := generated.User{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		DeletedAt: pgtype.Timestamptz{Time: del, Valid: true},
	}

	got := dbUserToDomain(dbUser)
	if got.DeletedAt == nil {
		t.Fatal("expected non-nil DeletedAt")
	}
	if !got.DeletedAt.Equal(del) {
		t.Errorf("DeletedAt mismatch: got %v, want %v", *got.DeletedAt, del)
	}
}

func TestHashToken(t *testing.T) {
	token := "my-session-token"
	h := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(h[:])

	got := hashToken(token)
	if got != want {
		t.Errorf("hashToken mismatch: got %v, want %v", got, want)
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	token := "deterministic-test"
	a := hashToken(token)
	b := hashToken(token)
	if a != b {
		t.Error("hashToken is not deterministic")
	}
}

func TestHashTokenDifferentInputs(t *testing.T) {
	a := hashToken("token-a")
	b := hashToken("token-b")
	if a == b {
		t.Error("different tokens produced the same hash")
	}
}

func TestDbSessionToDomain(t *testing.T) {
	now := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	id := uuid.New()
	userID := uuid.New()
	ua := "Mozilla/5.0"
	ip := netip.MustParseAddr("192.168.1.100")

	dbSess := generated.Session{
		ID:        id,
		UserID:    userID,
		TokenHash: "hashed-value",
		IpAddress: &ip,
		UserAgent: &ua,
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(24 * time.Hour), Valid: true},
		RevokedAt: pgtype.Timestamptz{},
	}

	got := dbSessionToDomain(dbSess, "plain-token")

	if got.ID != id {
		t.Errorf("ID mismatch: got %v, want %v", got.ID, id)
	}
	if got.UserID != userID {
		t.Errorf("UserID mismatch: got %v, want %v", got.UserID, userID)
	}
	if got.Token != "plain-token" {
		t.Errorf("Token mismatch: got %v, want plain-token", got.Token)
	}
	if !got.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("ExpiresAt mismatch")
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt mismatch")
	}
	if got.UserAgent != "Mozilla/5.0" {
		t.Errorf("UserAgent mismatch: got %v, want Mozilla/5.0", got.UserAgent)
	}
	if got.IPAddress != "192.168.1.100" {
		t.Errorf("IPAddress mismatch: got %v, want 192.168.1.100", got.IPAddress)
	}
}

func TestDbSessionToDomainNilFields(t *testing.T) {
	dbSess := generated.Session{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		TokenHash: "hash",
		IpAddress: nil,
		UserAgent: nil,
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		ExpiresAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	got := dbSessionToDomain(dbSess, "tok")
	if got.UserAgent != "" {
		t.Errorf("expected empty UserAgent for nil, got %v", got.UserAgent)
	}
	if got.IPAddress != "" {
		t.Errorf("expected empty IPAddress for nil, got %v", got.IPAddress)
	}
}

// Verify interface compliance at compile time.
var _ auth.UserRepository = (*UserAdapter)(nil)
var _ auth.SessionRepository = (*SessionAdapter)(nil)
