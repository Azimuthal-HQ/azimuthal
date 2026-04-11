// Package testutil provides shared helpers for integration tests.
// Tests that require a real database use testutil.NewTestDB() to get
// a clean, isolated database connection.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestDB wraps a database connection for integration tests.
type TestDB struct {
	Pool   *pgxpool.Pool
	DSN    string
	Schema string
}

// NewTestDB creates a fresh isolated schema for a single test.
// It skips the test if DATABASE_URL is not set.
// The schema is automatically dropped when the test completes.
//
// Usage:
//
//	func TestMyFeature(t *testing.T) {
//	    db := testutil.NewTestDB(t)
//	    // db.Pool is ready to use with a clean schema
//	}
func NewTestDB(t *testing.T) *TestDB {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test. Run 'make test-db-up' first.")
	}

	ctx := context.Background()

	// Create a unique schema for this test to ensure isolation
	schema := fmt.Sprintf("test_%s", sanitizeTestName(t.Name()))

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("testutil.NewTestDB: connect: %v", err)
	}

	// Create isolated schema
	_, err = pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema))
	if err != nil {
		pool.Close()
		t.Fatalf("testutil.NewTestDB: create schema: %v", err)
	}

	// Run migrations in isolated schema using database/sql (goose requirement)
	migDB, err := sql.Open("pgx", dsn)
	if err != nil {
		pool.Close()
		t.Fatalf("testutil.NewTestDB: open for migrations: %v", err)
	}
	defer migDB.Close()

	// Set search_path for migrations
	if _, err := migDB.Exec(fmt.Sprintf("SET search_path TO %q, public", schema)); err != nil {
		pool.Close()
		t.Fatalf("testutil.NewTestDB: set search_path for migrations: %v", err)
	}

	goose.SetTableName(schema + ".goose_db_version")

	migrationsDir := findMigrationsDir()
	if err := goose.Up(migDB, migrationsDir); err != nil {
		pool.Close()
		t.Fatalf("testutil.NewTestDB: migrate: %v", err)
	}

	// Set search_path on the pool connections
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		pool.Close()
		t.Fatalf("testutil.NewTestDB: parse config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = fmt.Sprintf("%q, public", schema)

	pool.Close()
	pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("testutil.NewTestDB: reconnect with schema: %v", err)
	}

	tdb := &TestDB{
		Pool:   pool,
		DSN:    dsn,
		Schema: schema,
	}

	// Cleanup: drop schema when test completes
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
		pool.Close()
	})

	return tdb
}

// findMigrationsDir locates the migrations directory relative to this source file.
func findMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	// thisFile is internal/testutil/db.go, so go up two levels to repo root
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "migrations")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return "migrations"
}

// sanitizeTestName converts a test name to a valid postgres identifier.
func sanitizeTestName(name string) string {
	result := make([]byte, 0, len(name))
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, byte(c))
		} else if c >= 'A' && c <= 'Z' {
			result = append(result, byte(c+32)) // lowercase
		} else {
			result = append(result, '_')
		}
	}
	// Postgres identifiers max 63 chars
	if len(result) > 50 {
		result = result[:50]
	}
	return string(result)
}
