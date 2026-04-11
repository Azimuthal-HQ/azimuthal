package testutil

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Org represents a test organization.
type Org struct {
	ID   uuid.UUID
	Slug string
	Name string
}

// User represents a test user.
type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	PassHash    string
}

// Space represents a test space.
type Space struct {
	ID   uuid.UUID
	Slug string
	Name string
	Type string
}

// CreateTestOrg creates a test organization directly in the database.
func CreateTestOrg(t *testing.T, pool *pgxpool.Pool) Org {
	t.Helper()
	org := Org{
		ID:   uuid.New(),
		Slug: fmt.Sprintf("test-org-%s", uuid.New().String()[:8]),
		Name: "Test Organization",
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO organizations (id, slug, name) VALUES ($1, $2, $3)`,
		org.ID, org.Slug, org.Name,
	)
	if err != nil {
		t.Fatalf("CreateTestOrg: %v", err)
	}
	return org
}

// CreateTestUser creates a test user with a known password hash.
// The password for this user is "testpassword123".
func CreateTestUser(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) User {
	t.Helper()
	// bcrypt hash of "testpassword123"
	testHash := "$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LewdBPj/VK.s4VqK2"
	user := User{
		ID:          uuid.New(),
		Email:       fmt.Sprintf("test-%s@azimuthal.dev", uuid.New().String()[:8]),
		DisplayName: "Test User",
		PassHash:    testHash,
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, org_id, email, display_name, password_hash)
		 VALUES ($1, $2, $3, $4, $5)`,
		user.ID, orgID, user.Email, user.DisplayName, user.PassHash,
	)
	if err != nil {
		t.Fatalf("CreateTestUser: %v", err)
	}
	// Create membership
	_, err = pool.Exec(context.Background(),
		`INSERT INTO memberships (id, org_id, user_id, role) VALUES ($1, $2, $3, 'owner')`,
		uuid.New(), orgID, user.ID,
	)
	if err != nil {
		t.Fatalf("CreateTestUser membership: %v", err)
	}
	return user
}

// CreateTestSpace creates a test space in the database.
// Requires a user to set as the creator (spaces.created_by is NOT NULL).
func CreateTestSpace(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, createdBy uuid.UUID, spaceType string) Space {
	t.Helper()
	space := Space{
		ID:   uuid.New(),
		Slug: fmt.Sprintf("test-%s-%s", spaceType, uuid.New().String()[:8]),
		Name: fmt.Sprintf("Test %s", spaceType),
		Type: spaceType,
	}
	_, err := pool.Exec(context.Background(),
		`INSERT INTO spaces (id, org_id, slug, name, type, created_by) VALUES ($1, $2, $3, $4, $5, $6)`,
		space.ID, orgID, space.Slug, space.Name, space.Type, createdBy,
	)
	if err != nil {
		t.Fatalf("CreateTestSpace: %v", err)
	}
	return space
}
