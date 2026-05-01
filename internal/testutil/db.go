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
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	// pgx stdlib driver required by goose for database/sql compatibility.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// gooseMu serializes calls to goose.SetTableName + goose.Up because
// goose stores the table name in a global variable. Without this lock,
// parallel tests clobber each other's goose_db_version table name.
var gooseMu sync.Mutex

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

	schema := fmt.Sprintf("test_%s", sanitizeTestName(t.Name()))

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("testutil.NewTestDB: connect: %v", err)
	}

	if _, err = pool.Exec(context.Background(), fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)); err != nil {
		pool.Close()
		t.Fatalf("testutil.NewTestDB: create schema: %v", err)
	}

	runMigrations(t, dsn, schema, pool)

	pool, err = newPoolWithSchema(dsn, schema)
	if err != nil {
		t.Fatalf("testutil.NewTestDB: reconnect with schema: %v", err)
	}

	tdb := &TestDB{Pool: pool, DSN: dsn, Schema: schema}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
		pool.Close()
		// Reset goose's package-level table-name so later tests in the same
		// binary that call db.Migrate(ctx, ...) directly do not inherit our
		// schema-qualified setting and hit "schema does not exist".
		gooseMu.Lock()
		goose.SetTableName("goose_db_version")
		gooseMu.Unlock()
	})

	return tdb
}

// runMigrations applies goose migrations to the given schema.
// The goose global table-name is protected by gooseMu so parallel tests
// do not race on goose.SetTableName.
func runMigrations(t *testing.T, dsn, schema string, pool *pgxpool.Pool) {
	t.Helper()

	migDB, err := sql.Open("pgx", dsn)
	if err != nil {
		pool.Close()
		t.Fatalf("testutil: open for migrations: %v", err)
	}
	defer func() {
		if cerr := migDB.Close(); cerr != nil {
			t.Logf("testutil: close migration db: %v", cerr)
		}
	}()

	if _, err = migDB.Exec(fmt.Sprintf("SET search_path TO %q, public", schema)); err != nil {
		pool.Close()
		t.Fatalf("testutil: set search_path: %v", err)
	}

	// Serialize goose calls — SetTableName is a package-level global.
	gooseMu.Lock()
	goose.SetTableName(schema + ".goose_db_version")
	err = goose.Up(migDB, findMigrationsDir())
	gooseMu.Unlock()

	if err != nil {
		pool.Close()
		t.Fatalf("testutil: migrate: %v", err)
	}
}

// newPoolWithSchema creates a pgxpool with the search_path set to the given schema.
func newPoolWithSchema(dsn, schema string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = fmt.Sprintf("%q, public", schema)
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating pool with schema: %w", err)
	}
	return pool, nil
}

// findMigrationsDir locates the migrations directory relative to this source file.
func findMigrationsDir() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "migrations")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return "migrations"
}

// sanitizeTestName converts a test name to a valid postgres identifier.
func sanitizeTestName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c >= 'A' && c <= 'Z':
			b.WriteRune(c - 'A' + 'a')
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}
