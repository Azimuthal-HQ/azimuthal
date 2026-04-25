package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Audit ref: testing-audit.md §7.4 — backup.go and restore.go had no test
// coverage anywhere in the repo. This file exercises the full backup/restore
// chain: archive round-trip, manifest validation, the storage-path helper,
// and an end-to-end pg_dump → psql round-trip across a fresh source DB and a
// fresh target DB. The earlier PR (#40) skipped the postgres round-trip with
// an architectural rationale that was wrong on closer reading: the
// dumpPostgres / restorePostgres helpers take a databaseURL parameter
// directly, so a true round-trip can be written without modifying backup.go,
// as long as we use a dedicated test database (not just a schema) so
// pg_dump's whole-database scope does not leak into other parallel tests.

// TestBackup_TarArchiveRoundTrip writes a manifest and a fake database
// dump into a tar.gz archive using the same writer used by runBackup,
// then reads it back with readArchive and checks the entries match.
func TestBackup_TarArchiveRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")

	manifest := backupManifest{
		AzimuthalVersion: "test",
		BackupTimestamp:  time.Now().UTC().Truncate(time.Second),
		PostgresVersion:  "PostgreSQL 16",
		Files:            []string{"database.sql", "storage/avatar.png"},
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)

	dumpBytes := []byte("-- fake pg_dump output\nSELECT 1;\n")
	avatarBytes := []byte("\x89PNG\r\n\x1a\nfake-image-bytes")

	out, err := os.Create(archivePath) //nolint:gosec // G304 — archivePath is a t.TempDir() path
	require.NoError(t, err)
	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)

	require.NoError(t, addToTar(tw, "database.sql", dumpBytes))
	require.NoError(t, addToTar(tw, "storage/avatar.png", avatarBytes))
	require.NoError(t, addToTar(tw, "manifest.json", manifestJSON))
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, out.Close())

	entries, err := readArchive(archivePath)
	require.NoError(t, err)
	require.Equal(t, dumpBytes, entries["database.sql"], "database dump must round-trip")
	require.Equal(t, avatarBytes, entries["storage/avatar.png"], "storage object must round-trip")
	require.Contains(t, entries, "manifest.json")

	got, err := validateManifest(entries)
	require.NoError(t, err, "manifest must validate against archive contents")
	require.Equal(t, manifest.AzimuthalVersion, got.AzimuthalVersion)
	require.Equal(t, manifest.PostgresVersion, got.PostgresVersion)
	require.Equal(t, manifest.Files, got.Files)
}

// TestRestore_ManifestRejectsMissingFiles verifies validateManifest catches
// a corrupt archive where the manifest references a file not present.
func TestRestore_ManifestRejectsMissingFiles(t *testing.T) {
	manifest := backupManifest{
		AzimuthalVersion: "test",
		BackupTimestamp:  time.Now().UTC(),
		Files:            []string{"database.sql", "storage/missing.png"},
	}
	manifestJSON, err := json.Marshal(manifest)
	require.NoError(t, err)

	entries := map[string][]byte{
		"manifest.json": manifestJSON,
		"database.sql":  []byte("-- dump"),
		// storage/missing.png deliberately absent
	}

	_, err = validateManifest(entries)
	require.Error(t, err, "validateManifest must fail when a manifested file is missing")
	require.Contains(t, err.Error(), "manifest references")
}

// TestRestore_ManifestRequiresManifestFile verifies validateManifest fails
// when manifest.json is missing entirely.
func TestRestore_ManifestRequiresManifestFile(t *testing.T) {
	entries := map[string][]byte{
		"database.sql": []byte("-- dump"),
	}

	_, err := validateManifest(entries)
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifest.json not found")
}

// TestStripStoragePrefix_RoundTrip verifies the storage prefix helper used
// by restoreObjectStorage to map archive entries back to bucket keys.
func TestStripStoragePrefix_RoundTrip(t *testing.T) {
	cases := []struct {
		archivePath string
		want        string
	}{
		{"storage/avatar.png", "avatar.png"},
		{"storage/users/123/profile.jpg", "users/123/profile.jpg"},
		{"database.sql", "database.sql"},
		{"manifest.json", "manifest.json"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, stripStoragePrefix(c.archivePath), "archive path %q", c.archivePath)
	}
}

// TestBackupRestore_FixturesAreReadable verifies the fixture entities the
// round-trip test populates are themselves readable from a fresh schema.
// This anchors the round-trip test so a regression in the fixtures shows up
// here first instead of as a confusing pg_dump failure.
func TestBackupRestore_FixturesAreReadable(t *testing.T) {
	tdb := testutil.NewTestDB(t)
	queries := generated.New(tdb.Pool)
	ctx := context.Background()

	org := testutil.CreateTestOrg(t, tdb.Pool)
	user := testutil.CreateTestUser(t, tdb.Pool, org.ID)
	space := testutil.CreateTestSpace(t, tdb.Pool, org.ID, user.ID, "service_desk")

	gotOrg, err := queries.GetOrganizationByID(ctx, org.ID)
	require.NoError(t, err)
	require.Equal(t, org.Slug, gotOrg.Slug)

	gotUser, err := queries.GetUserByID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, user.Email, gotUser.Email)

	gotSpace, err := queries.GetSpaceByID(ctx, space.ID)
	require.NoError(t, err)
	require.Equal(t, space.Slug, gotSpace.Slug)

	// Sanity: the membership that CreateTestUser added is reachable.
	membership, err := queries.GetMembership(ctx, generated.GetMembershipParams{
		OrgID:  org.ID,
		UserID: user.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "owner", membership.Role)
}

// TestBackupRestore_PostgresRoundTrip is the full pg_dump → psql round-trip
// the audit (§7.4) asks for. It creates two fresh databases — a source the
// fixtures get inserted into and a target the dump is restored into — so
// pg_dump's whole-database scope does not collide with any other test's
// schema.
//
// Skipped automatically when pg_dump/psql are not on PATH or when
// DATABASE_URL is not set. Otherwise drives the chain end-to-end:
//
//  1. Create source DB, run migrations, insert one of each entity (org,
//     user, space, ticket-style item, page, comment).
//  2. Call dumpPostgres against the source DSN.
//  3. Create a fresh target DB, call restorePostgres against its DSN.
//  4. Re-query each entity from the target and assert non-timestamp
//     equality.
func TestBackupRestore_PostgresRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump not on PATH — skipping postgres round-trip")
	}
	if _, err := exec.LookPath("psql"); err != nil {
		t.Skip("psql not on PATH — skipping postgres round-trip")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping postgres round-trip")
	}

	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err, "connecting with admin DSN")
	defer adminPool.Close()

	// Use UUIDs (without dashes) for DB names so concurrent runs of this
	// test never collide.
	suffix := uuid.New().String()[:8]
	srcDBName := "azim_brt_src_" + suffix
	dstDBName := "azim_brt_dst_" + suffix

	createDB := func(name string) {
		// G202 doesn't apply: name is generated locally from a UUID, not
		// from request data. Identifiers cannot be parameterised in
		// CREATE/DROP DATABASE.
		_, execErr := adminPool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)) //nolint:gosec
		require.NoError(t, execErr, "creating database %q", name)
	}
	dropDB := func(name string) {
		// Disconnect any other sessions before dropping; ignore errors here.
		_, _ = adminPool.Exec(ctx,
			fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s' AND pid <> pg_backend_pid()`, name))
		_, _ = adminPool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", name)) //nolint:gosec
	}

	createDB(srcDBName)
	t.Cleanup(func() { dropDB(srcDBName) })
	createDB(dstDBName)
	t.Cleanup(func() { dropDB(dstDBName) })

	srcDSN, err := dsnWithDatabase(dsn, srcDBName)
	require.NoError(t, err)
	dstDSN, err := dsnWithDatabase(dsn, dstDBName)
	require.NoError(t, err)

	// 1. Migrate the source DB and seed one of each entity.
	srcPool, err := pgxpool.New(ctx, srcDSN)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(ctx, srcPool), "migrating source DB")

	srcQueries := generated.New(srcPool)
	seed := seedRoundTripFixtures(t, ctx, srcQueries)
	srcPool.Close()

	// 2. Dump the source DB.
	dumpBytes, _, err := dumpPostgres(srcDSN)
	require.NoError(t, err, "pg_dump against source DB")
	require.NotEmpty(t, dumpBytes, "dump must not be empty")

	// 3. Restore into a fresh target DB. The dump uses --clean --if-exists
	// so the empty target gets the full schema + data.
	require.NoError(t, restorePostgres(dstDSN, dumpBytes), "psql restore into target DB")

	// 4. Verify each entity in the target DB.
	dstPool, err := pgxpool.New(ctx, dstDSN)
	require.NoError(t, err)
	defer dstPool.Close()

	dstQueries := generated.New(dstPool)

	gotOrg, err := dstQueries.GetOrganizationByID(ctx, seed.org.ID)
	require.NoError(t, err, "org row must round-trip")
	require.Equal(t, seed.org.Slug, gotOrg.Slug)
	require.Equal(t, seed.org.Name, gotOrg.Name)
	require.Equal(t, seed.org.Plan, gotOrg.Plan)

	gotUser, err := dstQueries.GetUserByID(ctx, seed.user.ID)
	require.NoError(t, err, "user row must round-trip")
	require.Equal(t, seed.user.Email, gotUser.Email)
	require.Equal(t, seed.user.DisplayName, gotUser.DisplayName)
	require.Equal(t, seed.user.OrgID, gotUser.OrgID)

	gotSpace, err := dstQueries.GetSpaceByID(ctx, seed.space.ID)
	require.NoError(t, err, "space row must round-trip")
	require.Equal(t, seed.space.Slug, gotSpace.Slug)
	require.Equal(t, seed.space.Name, gotSpace.Name)
	require.Equal(t, seed.space.Type, gotSpace.Type)

	gotMembership, err := dstQueries.GetMembership(ctx, generated.GetMembershipParams{
		OrgID:  seed.org.ID,
		UserID: seed.user.ID,
	})
	require.NoError(t, err, "membership row must round-trip")
	require.Equal(t, "owner", gotMembership.Role)

	gotItem, err := dstQueries.GetItemByID(ctx, seed.item.ID)
	require.NoError(t, err, "item row must round-trip")
	require.Equal(t, seed.item.Title, gotItem.Title)
	require.Equal(t, seed.item.Status, gotItem.Status)
	require.Equal(t, seed.item.Priority, gotItem.Priority)

	gotPage, err := dstQueries.GetPageByID(ctx, seed.page.ID)
	require.NoError(t, err, "page row must round-trip")
	require.Equal(t, seed.page.Title, gotPage.Title)
	require.Equal(t, seed.page.Content, gotPage.Content)

	// Comment list scoped to the item — exercises both the comment row and
	// the FK back to the item.
	itemComments, err := dstQueries.ListCommentsByItem(ctx, pgtype.UUID{Bytes: seed.item.ID, Valid: true})
	require.NoError(t, err, "item comments must round-trip")
	require.Len(t, itemComments, 1, "exactly one comment expected on the item")
	require.Equal(t, seed.comment.ID, itemComments[0].ID)
	require.Equal(t, seed.comment.Body, itemComments[0].Body)
}

// seedFixtures captures the rows seedRoundTripFixtures inserted so the
// caller can assert against them after the round-trip.
type seedFixtures struct {
	org     generated.Organization
	user    generated.User
	space   generated.Space
	item    generated.Item
	page    generated.Page
	comment generated.Comment
}

// seedRoundTripFixtures inserts one of each entity into the source DB.
func seedRoundTripFixtures(t *testing.T, ctx context.Context, q *generated.Queries) seedFixtures {
	t.Helper()

	suffix := uuid.New().String()[:8]
	desc := "Round-trip fixture org " + suffix

	org, err := q.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID:          uuid.New(),
		Slug:        "rt-org-" + suffix,
		Name:        "Round Trip Org " + suffix,
		Description: &desc,
		Plan:        "free",
	})
	require.NoError(t, err)

	hash := "$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LewdBPj/VK.s4VqK2"
	user, err := q.CreateUser(ctx, generated.CreateUserParams{
		ID:           uuid.New(),
		OrgID:        org.ID,
		Email:        "rt-" + suffix + "@azimuthal.dev",
		DisplayName:  "Round Trip User",
		PasswordHash: &hash,
		Role:         "member",
	})
	require.NoError(t, err)

	_, err = q.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:        uuid.New(),
		OrgID:     org.ID,
		UserID:    user.ID,
		Role:      "owner",
		InvitedBy: pgtype.UUID{},
	})
	require.NoError(t, err)

	spaceDesc := "Round-trip fixture space"
	space, err := q.CreateSpace(ctx, generated.CreateSpaceParams{
		ID:          uuid.New(),
		OrgID:       org.ID,
		Slug:        "rt-space-" + suffix,
		Name:        "Round Trip Space",
		Description: &spaceDesc,
		Type:        "service_desk",
		IsPrivate:   false,
		CreatedBy:   user.ID,
	})
	require.NoError(t, err)

	itemDesc := "Round-trip fixture item"
	item, err := q.CreateItem(ctx, generated.CreateItemParams{
		ID:          uuid.New(),
		SpaceID:     space.ID,
		Kind:        "ticket",
		Title:       "Round Trip Ticket",
		Description: &itemDesc,
		Status:      "open",
		Priority:    "medium",
		ReporterID:  user.ID,
	})
	require.NoError(t, err)

	page, err := q.CreatePage(ctx, generated.CreatePageParams{
		ID:       uuid.New(),
		SpaceID:  space.ID,
		Title:    "Round Trip Page",
		Content:  "Hello from the round-trip test.",
		AuthorID: user.ID,
		Position: 0,
	})
	require.NoError(t, err)

	comment, err := q.CreateComment(ctx, generated.CreateCommentParams{
		ID:       uuid.New(),
		ItemID:   pgtype.UUID{Bytes: item.ID, Valid: true},
		AuthorID: user.ID,
		Body:     "Round-trip comment body.",
	})
	require.NoError(t, err)

	return seedFixtures{
		org:     org,
		user:    user,
		space:   space,
		item:    item,
		page:    page,
		comment: comment,
	}
}

// dsnWithDatabase rewrites the database name in a postgres URL, leaving
// host/port/user/password/query parameters untouched.
func dsnWithDatabase(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parsing DSN: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// guard against unused-import for bytes when only one helper happens to
// use it during refactors; keep this so go vet stays clean.
var _ = bytes.NewReader
