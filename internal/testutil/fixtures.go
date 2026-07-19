package testutil

import (
	"context"
	"fmt"
	"strings"
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

// CreateTestOrg creates a test organization directly in the database, with
// its default team — production org creation always seeds one (ADR-0006
// point 4), and spaces.owner_team_id backfills against it.
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
	teamID := uuid.New()
	_, err = pool.Exec(context.Background(),
		`INSERT INTO teams (id, org_id, parent_id, path, slug, name, is_default)
		 VALUES ($1, $2, NULL, ARRAY[$1]::uuid[], 'default', 'Default', true)`,
		teamID, org.ID,
	)
	if err != nil {
		t.Fatalf("CreateTestOrg default team: %v", err)
	}
	return org
}

// DefaultTeamID returns the org's default team id.
func DefaultTeamID(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`SELECT id FROM teams WHERE org_id = $1 AND is_default AND deleted_at IS NULL`,
		orgID).Scan(&id)
	if err != nil {
		t.Fatalf("DefaultTeamID: %v", err)
	}
	return id
}

// CreateTestUser creates a test user with a known password hash and an
// 'owner' org membership (an org admin under ADR-0007's bypass).
// The password for this user is "testpassword123".
func CreateTestUser(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) User {
	t.Helper()
	return CreateTestUserWithRole(t, pool, orgID, "owner")
}

// CreateTestUserWithRole creates a test user with the given org membership
// role ("owner"/"admin" are org admins; "member" is not). Mirroring
// production provisioning, the user is enrolled in the org default team as
// their primary team — never teamless (ADR-0006 point 4).
func CreateTestUserWithRole(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, orgRole string) User {
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
	_, err = pool.Exec(context.Background(),
		`INSERT INTO memberships (id, org_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		uuid.New(), orgID, user.ID, orgRole,
	)
	if err != nil {
		t.Fatalf("CreateTestUser membership: %v", err)
	}
	_, err = pool.Exec(context.Background(),
		`INSERT INTO team_members (team_id, user_id, org_id, is_primary)
		 SELECT t.id, $1, $2, true FROM teams t
		 WHERE t.org_id = $2 AND t.is_default AND t.deleted_at IS NULL
		 ON CONFLICT (team_id, user_id) DO NOTHING`,
		user.ID, orgID,
	)
	if err != nil {
		t.Fatalf("CreateTestUser default team enrolment: %v", err)
	}
	return user
}

// CreateTestSpace creates a test space in the database.
// Requires a user to set as the creator (spaces.created_by is NOT NULL).
func CreateTestSpace(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, createdBy uuid.UUID, spaceType string) Space {
	t.Helper()
	slug := fmt.Sprintf("test-%s-%s", spaceType, uuid.New().String()[:8])
	space := Space{
		ID:   uuid.New(),
		Slug: slug,
		Name: fmt.Sprintf("Test %s", spaceType),
		Type: spaceType,
	}
	// Derive a unique key from the space type (strip underscores, uppercase, max 6 chars)
	// plus 2 random hex digits so multiple spaces of the same type don't collide.
	base := strings.ToUpper(strings.ReplaceAll(spaceType, "_", ""))
	if len(base) > 6 {
		base = base[:6]
	}
	key := base + strings.ToUpper(uuid.New().String()[:2])
	// owner_team_id is NOT NULL since migration 023: every space belongs to
	// a team, defaulting to the org default team seeded by 022.
	_, err := pool.Exec(context.Background(),
		`INSERT INTO spaces (id, org_id, slug, name, type, created_by, key, owner_team_id)
		 SELECT $1, $2, $3, $4, $5, $6, $7, t.id
		 FROM teams t WHERE t.org_id = $2 AND t.is_default AND t.deleted_at IS NULL`,
		space.ID, orgID, space.Slug, space.Name, space.Type, createdBy, key,
	)
	if err != nil {
		t.Fatalf("CreateTestSpace: %v", err)
	}
	return space
}

// SetSpaceVisibility updates a space's visibility ('hidden', 'discoverable',
// or 'org') for permission-matrix fixtures.
func SetSpaceVisibility(t *testing.T, pool *pgxpool.Pool, spaceID uuid.UUID, visibility string) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`UPDATE spaces SET visibility = $2 WHERE id = $1`, spaceID, visibility)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("SetSpaceVisibility: err=%v rows=%d", err, tag.RowsAffected())
	}
}
