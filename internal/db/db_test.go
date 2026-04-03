package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// testPool returns a pool for integration tests; skips if DATABASE_URL unset.
func testPool(t *testing.T) (*db.Pool, func()) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set: skipping integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, db.DefaultConfig(url))
	if err != nil {
		t.Fatalf("connecting to database: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("running migrations: %v", err)
	}
	return pool, pool.Close
}

func TestConnect(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.Ping(ctx, pool, 5*time.Second); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestMigrateVersion(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	version, err := db.MigrationVersion(context.Background(), pool)
	if err != nil {
		t.Fatalf("getting migration version: %v", err)
	}
	if version < 9 {
		t.Errorf("expected at least version 9, got %d", version)
	}
}

func TestConnect_InvalidURL(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := db.Connect(ctx, db.Config{
		URL:           "postgres://invalid:badpass@localhost:5499/notexist", //nolint:gosec
		HealthTimeout: 1 * time.Second,
	})
	if err == nil {
		t.Error("expected error for invalid connection, got nil")
	}
}

// setupOrg creates a test organisation and registers soft-delete on cleanup.
func setupOrg(t *testing.T, q *generated.Queries, suffix string) generated.Organization {
	t.Helper()
	org, err := q.CreateOrganization(context.Background(), generated.CreateOrganizationParams{
		ID:   uuid.New(),
		Slug: "test-" + suffix,
		Name: "Test Org " + suffix,
		Plan: "community",
	})
	if err != nil {
		t.Fatalf("setupOrg: %v", err)
	}
	t.Cleanup(func() { _ = q.SoftDeleteOrganization(context.Background(), org.ID) })
	return org
}

// setupUser creates a test user and registers soft-delete on cleanup.
func setupUser(t *testing.T, q *generated.Queries, orgID uuid.UUID, email string) generated.User {
	t.Helper()
	user, err := q.CreateUser(context.Background(), generated.CreateUserParams{
		ID:          uuid.New(),
		OrgID:       orgID,
		Email:       email,
		DisplayName: email,
		Role:        "member",
	})
	if err != nil {
		t.Fatalf("setupUser: %v", err)
	}
	t.Cleanup(func() { _ = q.SoftDeleteUser(context.Background(), user.ID) })
	return user
}

// setupSpace creates a test space and registers soft-delete on cleanup.
func setupSpace(t *testing.T, q *generated.Queries, orgID, createdBy uuid.UUID, kind string) generated.Space {
	t.Helper()
	space, err := q.CreateSpace(context.Background(), generated.CreateSpaceParams{
		ID:        uuid.New(),
		OrgID:     orgID,
		Slug:      kind + "-" + uuid.New().String()[:8],
		Name:      kind + " space",
		Type:      kind,
		IsPrivate: false,
		CreatedBy: createdBy,
	})
	if err != nil {
		t.Fatalf("setupSpace: %v", err)
	}
	t.Cleanup(func() { _ = q.SoftDeleteSpace(context.Background(), space.ID) })
	return space
}

func TestOrganizationCRUD(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	slug := "crud-" + uuid.New().String()[:8]
	org, err := q.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID:   uuid.New(),
		Slug: slug,
		Name: "CRUD Org",
		Plan: "community",
	})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}
	defer func() { _ = q.SoftDeleteOrganization(ctx, org.ID) }()

	got, err := q.GetOrganizationByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetOrganizationByID: %v", err)
	}
	if got.ID != org.ID {
		t.Errorf("ID mismatch")
	}

	got, err = q.GetOrganizationBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("GetOrganizationBySlug: %v", err)
	}
	if got.Slug != slug {
		t.Errorf("slug mismatch")
	}

	desc := "updated"
	updated, err := q.UpdateOrganization(ctx, generated.UpdateOrganizationParams{
		ID:          org.ID,
		Name:        "Updated Org",
		Description: &desc,
		Plan:        "community",
	})
	if err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	if updated.Name != "Updated Org" {
		t.Errorf("name not updated: %s", updated.Name)
	}

	if err := q.SoftDeleteOrganization(ctx, org.ID); err != nil {
		t.Fatalf("SoftDeleteOrganization: %v", err)
	}
	_, err = q.GetOrganizationByID(ctx, org.ID)
	if err == nil {
		t.Error("expected error after soft delete, got nil")
	}
}

func TestUserAndSessionLifecycle(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	org := setupOrg(t, q, uuid.New().String()[:8])
	pwHash := "bcrypt-hash-placeholder" //nolint:gosec
	user, err := q.CreateUser(ctx, generated.CreateUserParams{
		ID:           uuid.New(),
		OrgID:        org.ID,
		Email:        "test@example.com",
		DisplayName:  "Test User",
		PasswordHash: &pwHash,
		Role:         "member",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer func() { _ = q.SoftDeleteUser(ctx, user.ID) }()

	found, err := q.GetUserByEmail(ctx, generated.GetUserByEmailParams{
		OrgID: org.ID,
		Email: "test@example.com",
	})
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("user ID mismatch")
	}

	tokenHash := "sha256:" + uuid.New().String()
	sess, err := q.CreateSession(ctx, generated.CreateSessionParams{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	fetched, err := q.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if fetched.ID != sess.ID {
		t.Errorf("session ID mismatch")
	}

	if err := q.RevokeSession(ctx, sess.ID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	_, err = q.GetSessionByTokenHash(ctx, tokenHash)
	if err == nil {
		t.Error("expected error after session revocation, got nil")
	}
}

func TestMembershipCRUD(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "member@example.com")

	m, err := q.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:     uuid.New(),
		OrgID:  org.ID,
		UserID: user.ID,
		Role:   "member",
	})
	if err != nil {
		t.Fatalf("CreateMembership: %v", err)
	}
	if m.Role != "member" {
		t.Errorf("unexpected role: %s", m.Role)
	}

	if err := q.UpdateMembershipRole(ctx, generated.UpdateMembershipRoleParams{
		OrgID:  org.ID,
		UserID: user.ID,
		Role:   "admin",
	}); err != nil {
		t.Fatalf("UpdateMembershipRole: %v", err)
	}

	got, err := q.GetMembership(ctx, generated.GetMembershipParams{
		OrgID:  org.ID,
		UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("GetMembership: %v", err)
	}
	if got.Role != "admin" {
		t.Errorf("role not updated")
	}

	if err := q.DeleteMembership(ctx, generated.DeleteMembershipParams{
		OrgID:  org.ID,
		UserID: user.ID,
	}); err != nil {
		t.Fatalf("DeleteMembership: %v", err)
	}
	_, err = q.GetMembership(ctx, generated.GetMembershipParams{OrgID: org.ID, UserID: user.ID})
	if err == nil {
		t.Error("expected error after DeleteMembership, got nil")
	}
}

func TestSpaceLifecycle(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "creator@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "project")

	got, err := q.GetSpaceBySlug(ctx, generated.GetSpaceBySlugParams{
		OrgID: org.ID,
		Slug:  space.Slug,
	})
	if err != nil {
		t.Fatalf("GetSpaceBySlug: %v", err)
	}
	if got.ID != space.ID {
		t.Errorf("space ID mismatch")
	}

	spaces, err := q.ListSpacesByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListSpacesByOrg: %v", err)
	}
	if len(spaces) == 0 {
		t.Error("expected at least one space")
	}

	if err := q.SoftDeleteSpace(ctx, space.ID); err != nil {
		t.Fatalf("SoftDeleteSpace: %v", err)
	}
	_, err = q.GetSpaceByID(ctx, space.ID)
	if err == nil {
		t.Error("expected error after soft delete")
	}
}

func TestItemStatusUpdateAndSoftDelete(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "reporter@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "project")

	item, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID:         uuid.New(),
		SpaceID:    space.ID,
		Kind:       "task",
		Title:      "Implement data layer",
		Status:     "open",
		Priority:   "high",
		ReporterID: user.ID,
		Labels:     []string{"backend"},
		Rank:       "a",
	})
	if err != nil {
		t.Fatalf("CreateItem: %v", err)
	}

	updated, err := q.UpdateItemStatus(ctx, generated.UpdateItemStatusParams{
		ID:     item.ID,
		Status: "in_progress",
	})
	if err != nil {
		t.Fatalf("UpdateItemStatus: %v", err)
	}
	if updated.Status != "in_progress" {
		t.Errorf("status not updated: %s", updated.Status)
	}

	if err := q.SoftDeleteItem(ctx, item.ID); err != nil {
		t.Fatalf("SoftDeleteItem: %v", err)
	}
	_, err = q.GetItemByID(ctx, item.ID)
	if err == nil {
		t.Error("expected error after soft delete")
	}
}

func TestPageOptimisticLocking(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "author@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "wiki")

	page, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID:       uuid.New(),
		SpaceID:  space.ID,
		Title:    "Getting Started",
		Content:  "Initial.",
		AuthorID: user.ID,
		Position: 0,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if page.Version != 1 {
		t.Errorf("expected version=1, got %d", page.Version)
	}

	updated, err := q.UpdatePageContent(ctx, generated.UpdatePageContentParams{
		ID:      page.ID,
		Version: 1,
		Title:   "Getting Started v2",
		Content: "Updated.",
	})
	if err != nil {
		t.Fatalf("UpdatePageContent correct version: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("expected version=2, got %d", updated.Version)
	}

	// Stale version must be rejected.
	_, err = q.UpdatePageContent(ctx, generated.UpdatePageContentParams{
		ID:      page.ID,
		Version: 1,
		Title:   "Conflict",
		Content: "Should not apply.",
	})
	if err == nil {
		t.Error("expected error on stale-version conflict, got nil")
	}
}

func TestPageRevisions(t *testing.T) {
	pool, cleanup := testPool(t)
	defer cleanup()
	ctx := context.Background()
	q := generated.New(pool)

	org := setupOrg(t, q, uuid.New().String()[:8])
	user := setupUser(t, q, org.ID, "rev@example.com")
	space := setupSpace(t, q, org.ID, user.ID, "wiki")

	page, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID:       uuid.New(),
		SpaceID:  space.ID,
		Title:    "Versioned",
		Content:  "v1",
		AuthorID: user.ID,
		Position: 0,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	_, err = q.CreatePageRevision(ctx, generated.CreatePageRevisionParams{
		ID:       uuid.New(),
		PageID:   page.ID,
		Version:  1,
		Title:    "Versioned",
		Content:  "v1",
		AuthorID: user.ID,
	})
	if err != nil {
		t.Fatalf("CreatePageRevision: %v", err)
	}

	revs, err := q.ListPageRevisions(ctx, page.ID)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	if len(revs) != 1 {
		t.Errorf("expected 1 revision, got %d", len(revs))
	}
}
