// Package testutil provides shared helpers for integration tests.
// Tests that require a real database use testutil.NewTestDB() to get
// a clean, isolated database connection.
//
// # How isolation is achieved
//
// Every call to NewTestDB hands the test its own PostgreSQL *database*,
// created with CREATE DATABASE ... TEMPLATE from a template that already has
// every migration applied. PostgreSQL implements that as a file-level copy of
// the template's directory, so it costs tens of milliseconds no matter how
// many migrations there are.
//
// The design it replaced ran every migration into a fresh schema on every
// single call. Measured against postgres 16 with 37 migrations: 700ms per
// call, of which 520ms was goose and 155ms was the DROP SCHEMA CASCADE at
// teardown. The template path costs ~30ms to clone and ~18ms to drop. A full
// suite makes 468 of these calls, so it is the difference between minutes and
// seconds — and the gap widens with every migration the project adds.
//
// The trade is one-time: the first NewTestDB to find no current template
// builds one, which costs a single full migration run (~600ms locally). Every
// later call, in every later test binary, reuses it — the template lives in
// the database server, not in the process.
//
// # Why the template can never be stale
//
// The template's name embeds a fingerprint of the migration set itself: a
// SHA-256 over the name and full content of every *.sql file in the embedded
// migrations filesystem. Edit a migration, add one, or delete one, and the
// fingerprint changes, so the name changes, so no template by that name
// exists, so one is built. There is no invalidation step that can be
// forgotten and no staleness check that can be wrong: a stale template is not
// detected, it is unreachable.
//
// The fingerprint is taken over migrations.FS — the same embedded filesystem
// the production migrator uses — so it is compiled into the test binary
// alongside the migrations it describes. It cannot drift from them, and it
// does not depend on the working directory.
//
// # Requirements
//
// PostgreSQL 13 or later (for DROP DATABASE ... WITH (FORCE)) and a
// DATABASE_URL whose role may CREATE DATABASE. Both hold for
// build/docker-compose.test.yml and for CI.
package testutil

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/Azimuthal-HQ/azimuthal/migrations"

	// pgx stdlib driver required by goose for database/sql compatibility.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// gooseMu serializes calls to goose's package-level setters (SetTableName,
// SetBaseFS, SetDialect) and to goose.Up. goose keeps every one of them in a
// global, so two goroutines configuring it at once clobber each other.
// internal/db calls the same setters, which is why the template build sets all
// of them explicitly instead of inheriting whatever ran last.
var gooseMu sync.Mutex

const (
	// templatePrefix names the migrated template databases. The fingerprint of
	// the migration set is appended to it.
	templatePrefix = "azimuthal_tmpl_"

	// testDBPrefix names the per-test databases. sweepAbandoned relies on this
	// prefix and on the name layout documented at newTestDBName.
	testDBPrefix = "azt_"

	// buildLockKey is the advisory-lock key that serializes template
	// construction between concurrent test binaries. Advisory locks are scoped
	// to the database the session is connected to, and every test binary
	// connects to the same maintenance database (the one in DATABASE_URL), so
	// they all contend on the same lock.
	buildLockKey int64 = 0x415A_4D54_4D50_4C00 // "AZMTMPL\0"

	// sweepAge is how long an abandoned per-test database must have existed
	// before sweepAbandoned removes it. A test database is normally dropped in
	// t.Cleanup; one survives only when a test binary was killed. The window is
	// generous because the timestamp encoded in the name is the owning
	// process's *start* time, and a long suite legitimately runs for a while.
	sweepAge = 2 * time.Hour

	// maxIdentifier is PostgreSQL's NAMEDATALEN-1. Identifiers longer than this
	// are silently truncated, which would collapse two test databases into one.
	maxIdentifier = 63
)

// TestDB wraps a database connection for integration tests.
type TestDB struct {
	// Pool is connected to this test's own database.
	Pool *pgxpool.Pool
	// DSN addresses this test's own database, so code that opens its own
	// connection from it lands in the same isolated place the Pool does.
	DSN string
	// Schema is always "public" now that isolation is per-database rather than
	// per-schema. It is retained because tests build a search_path from it.
	Schema string
}

// NewTestDB creates a fresh isolated database for a single test.
// The database is automatically dropped when the test completes.
//
// Usage:
//
//	func TestMyFeature(t *testing.T) {
//	    db := testutil.NewTestDB(t)
//	    // db.Pool is ready to use against a fully migrated, empty database
//	}
//
// # A missing DATABASE_URL skips locally and FAILS in CI
//
// Without a database this cannot do its job, and the honest local answer is to
// skip: a contributor running `go test ./...` before `make test-db-up` wants to
// be told, not buried.
//
// In CI that same skip is the most dangerous thing in the repository. Every
// test that touches persistence enters here, so one unset variable turns the
// entire integration suite — every authorisation check, every fail-closed
// workflow proof — into a silent no-op, and the pipeline reports green for a
// build that verified nothing. A skipped test and an absent test are the same
// artifact.
//
// So the precondition is a hard failure whenever CI is set. The platform sets
// that variable itself and it cannot be lost by editing a workflow file, which
// is the property the check depends on. Pattern taken from
// requirePostgresClientTools in cmd/server/backup_restore_test.go, not the
// function: that one lives in package main and is about client binaries.
func NewTestDB(t *testing.T) *TestDB {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		const msg = "DATABASE_URL not set — integration tests need a database. Run 'make test-db-up' first."
		if os.Getenv("CI") != "" {
			t.Fatal(msg + " In CI this is a failure, never a skip: a skipped integration suite reports green.")
		}
		t.Skip(msg)
	}

	h, err := sharedHarness(dsn)
	if err != nil {
		t.Fatalf("testutil.NewTestDB: %v", err)
	}

	name := h.newTestDBName(t.Name())
	ctx := context.Background()

	// CREATE DATABASE cannot run inside a transaction, and pgx does not wrap a
	// bare Exec in one. The identifier is built by this package from a
	// sanitised test name, never from external input, and is quoted regardless.
	if _, err := h.admin.Exec(ctx,
		fmt.Sprintf("CREATE DATABASE %q TEMPLATE %q", name, h.template)); err != nil {
		t.Fatalf("testutil.NewTestDB: clone template %q into %q: %v", h.template, name, err)
	}

	testDSN, err := withDatabase(h.dsn, name)
	if err != nil {
		h.dropDatabase(name)
		t.Fatalf("testutil.NewTestDB: %v", err)
	}

	pool, err := newTestPool(testDSN)
	if err != nil {
		h.dropDatabase(name)
		t.Fatalf("testutil.NewTestDB: connect to %q: %v", name, err)
	}

	t.Cleanup(func() {
		// The pool goes before the DROP. WITH (FORCE) would terminate its
		// backends anyway, but closing first keeps the teardown quiet.
		pool.Close()
		h.dropDatabase(name)
	})

	return &TestDB{Pool: pool, DSN: testDSN, Schema: "public"}
}

// harness owns the process-wide state behind NewTestDB: one pool against the
// maintenance database, and the name of the template every test clones from.
type harness struct {
	admin    *pgxpool.Pool
	dsn      string
	template string
	runTag   string
	counter  atomic.Uint64
}

var (
	harnessOnce sync.Once
	harnessVal  *harness
	harnessErr  error
)

// sharedHarness builds the harness once per test binary. Every package's tests
// run in their own process, so "once" here means once per package — but the
// template itself lives in the database server and is shared across all of
// them, so only the first process to run pays for building it.
func sharedHarness(dsn string) (*harness, error) {
	harnessOnce.Do(func() {
		harnessVal, harnessErr = newHarness(dsn)
	})
	if harnessErr != nil {
		return nil, harnessErr
	}
	return harnessVal, nil
}

func newHarness(dsn string) (*harness, error) {
	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing DATABASE_URL: %w", err)
	}
	// The admin pool only issues CREATE/DROP DATABASE. Four connections is
	// ample even when a package runs its tests in parallel, and keeping it
	// small matters: postgres defaults to max_connections=100 and every test
	// holds a pool of its own on top of this one.
	cfg.MaxConns = 4
	adminPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to the maintenance database: %w", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		return nil, fmt.Errorf("connecting to the maintenance database: %w", err)
	}

	h := &harness{admin: adminPool, dsn: dsn, runTag: runTag()}

	template, err := ensureTemplate(ctx, adminPool, dsn, migrations.FS)
	if err != nil {
		adminPool.Close()
		return nil, err
	}
	h.template = template

	h.sweepAbandoned(ctx)

	return h, nil
}

// ensureTemplate returns the name of a template database holding every
// migration in migFS, building it if no template for that exact migration set
// exists yet.
//
// It takes the filesystem as a parameter rather than reaching for
// migrations.FS directly so that the invalidation test can drive it with a
// doctored migration set and prove that a changed set produces a different
// template with the change actually in it.
func ensureTemplate(ctx context.Context, admin *pgxpool.Pool, dsn string, migFS fs.FS) (string, error) {
	fingerprint, err := fingerprintMigrations(migFS)
	if err != nil {
		return "", fmt.Errorf("fingerprinting migrations: %w", err)
	}
	name := templatePrefix + fingerprint

	exists, err := databaseExists(ctx, admin, name)
	if err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	// Another test binary may be building the same template right now. Take the
	// advisory lock, then re-check: whoever loses the race finds it finished.
	conn, err := admin.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquiring a connection to build the template: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", buildLockKey); err != nil {
		return "", fmt.Errorf("locking template construction: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", buildLockKey)
	}()

	exists, err = databaseExists(ctx, admin, name)
	if err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}

	if err := buildTemplate(ctx, admin, dsn, name, migFS); err != nil {
		return "", err
	}
	return name, nil
}

// buildTemplate migrates a staging database and only then renames it to the
// template name.
//
// The rename is what makes a half-built template impossible. If this process
// dies during the migration run, what it leaves behind is a staging database
// under a name nothing ever clones from — the template name still does not
// exist, so the next run builds it properly. Publishing the name only once the
// work is finished is cheaper and more reliable than trying to validate a
// template after the fact.
func buildTemplate(ctx context.Context, admin *pgxpool.Pool, dsn, name string, migFS fs.FS) error {
	staging := fmt.Sprintf("%s_building_%d", name, os.Getpid())
	if len(staging) > maxIdentifier {
		staging = staging[:maxIdentifier]
	}

	// A staging database under our own pid can only be our own leftovers.
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", staging)); err != nil {
		return fmt.Errorf("clearing a previous staging database %q: %w", staging, err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", staging)); err != nil {
		return fmt.Errorf("creating the staging database %q: %w", staging, err)
	}

	if err := migrateInto(ctx, dsn, staging, migFS); err != nil {
		dropQuietly(admin, staging)
		return err
	}

	// Nothing may hold a connection when the rename runs, and nothing may
	// connect to a template while it is being cloned. Refusing connections
	// outright gives both guarantees at once — it is how template0 works, and
	// CREATE DATABASE ... TEMPLATE copies the files directly rather than
	// connecting, so cloning still works.
	if _, err := admin.Exec(ctx,
		fmt.Sprintf("ALTER DATABASE %q WITH ALLOW_CONNECTIONS false", staging)); err != nil {
		dropQuietly(admin, staging)
		return fmt.Errorf("sealing the staging database %q: %w", staging, err)
	}
	if _, err := admin.Exec(ctx,
		fmt.Sprintf("ALTER DATABASE %q RENAME TO %q", staging, name)); err != nil {
		dropQuietly(admin, staging)
		return fmt.Errorf("publishing the template as %q: %w", name, err)
	}
	return nil
}

// migrateInto applies every migration in migFS to the named database.
func migrateInto(ctx context.Context, dsn, dbName string, migFS fs.FS) error {
	target, err := withDatabase(dsn, dbName)
	if err != nil {
		return err
	}

	migDB, err := sql.Open("pgx", target)
	if err != nil {
		return fmt.Errorf("opening %q for migrations: %w", dbName, err)
	}
	defer func() { _ = migDB.Close() }()

	if err := migDB.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to %q for migrations: %w", dbName, err)
	}

	// Every one of these is a goose global. internal/db.Migrate points the base
	// FS at the production migrations and never restores it, so a test that
	// called db.Migrate before the first NewTestDB would otherwise leave goose
	// resolving our directory argument against a filesystem we did not choose.
	gooseMu.Lock()
	defer gooseMu.Unlock()

	goose.SetTableName("goose_db_version")
	goose.SetBaseFS(migFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting the goose dialect: %w", err)
	}
	// "." because the migrations FS embeds *.sql at its root.
	if err := goose.UpContext(ctx, migDB, "."); err != nil {
		return fmt.Errorf("migrating %q: %w", dbName, err)
	}
	return nil
}

// fingerprintMigrations hashes the migration set: every *.sql file's name and
// full content, in sorted order. Renaming a file changes it, editing one
// changes it, adding or removing one changes it.
func fingerprintMigrations(migFS fs.FS) (string, error) {
	names, err := fs.Glob(migFS, "*.sql")
	if err != nil {
		return "", fmt.Errorf("listing migrations: %w", err)
	}
	if len(names) == 0 {
		return "", errors.New("no *.sql migrations found in the embedded migrations filesystem")
	}
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		content, err := fs.ReadFile(migFS, name)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", name, err)
		}
		// Length-prefix both name and content so that no two different sets can
		// hash alike by shifting a byte across a boundary — a plain
		// concatenation would lose exactly that distinction.
		_, _ = fmt.Fprintf(sum, "%d:%s\n%d:", len(name), name, len(content))
		_, _ = sum.Write(content)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

// newTestDBName builds a per-test database name.
//
// Layout: azt_<processStart-b36>_<pid-b36>_<counter-b36>_<test name>, clipped
// to postgres's 63-byte identifier limit. sweepAbandoned parses the second
// field back into a timestamp, so the layout is load-bearing — there is a test
// that asserts a generated name round-trips through parseRunStart.
func (h *harness) newTestDBName(testName string) string {
	n := h.counter.Add(1)
	prefix := fmt.Sprintf("%s%s_%s_", testDBPrefix, h.runTag, strconv.FormatUint(n, 36))
	name := prefix + sanitizeTestName(testName)
	if len(name) > maxIdentifier {
		name = name[:maxIdentifier]
	}
	return name
}

// runTag identifies this process by its start time and pid, both base 36.
func runTag() string {
	return strconv.FormatInt(time.Now().Unix(), 36) + "_" + strconv.FormatInt(int64(os.Getpid()), 36)
}

// sweepAbandoned drops per-test databases left behind by test binaries that
// were killed before their t.Cleanup could run. Without it, a machine where
// somebody interrupts a suite accumulates one database per test, forever.
//
// It only touches databases whose embedded process-start timestamp is older
// than sweepAge, so a suite running right now in another process is never a
// candidate. The timestamp has to come from the name because postgres does not
// record when a database was created.
//
// Template databases are deliberately NOT swept. This project's test database
// is routinely shared by concurrent sessions on different commits, and those
// sessions legitimately hold different templates; sweeping "the other one"
// would have each session deleting the other's out from under it. Templates
// are small, and `make test-db-reset` removes the volume outright.
//
// Best-effort throughout: nothing here may fail a test.
func (h *harness) sweepAbandoned(ctx context.Context) {
	rows, err := h.admin.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE $1`, testDBPrefix+"%")
	if err != nil {
		return
	}
	var stale []string
	cutoff := time.Now().Add(-sweepAge)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		if started, ok := parseRunStart(name); ok && started.Before(cutoff) {
			stale = append(stale, name)
		}
	}
	rows.Close()

	for _, name := range stale {
		h.dropDatabase(name)
	}
}

// parseRunStart recovers the owning process's start time from a per-test
// database name. It reports false for anything that does not match the layout,
// so an unrecognised name is left alone rather than guessed at.
func parseRunStart(dbName string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(dbName, testDBPrefix)
	if !ok {
		return time.Time{}, false
	}
	field, _, ok := strings.Cut(rest, "_")
	if !ok {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(field, 36, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// dropDatabase removes a database, terminating anything still connected to it.
// WITH (FORCE) needs postgres 13; the project's test stack is 16.
func (h *harness) dropDatabase(name string) {
	// A fresh context: t.Cleanup can run after the test's own context is done.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = h.admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name))
}

func dropQuietly(admin *pgxpool.Pool, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name))
}

func databaseExists(ctx context.Context, admin *pgxpool.Pool, name string) (bool, error) {
	var found bool
	err := admin.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("looking up database %q: %w", name, err)
	}
	return found, nil
}

// newTestPool connects to a per-test database.
// MaxConns is capped at 3 per test to avoid exhausting postgres
// max_connections when many integration tests run in parallel.
func newTestPool(dsn string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	poolConfig.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}
	return pool, nil
}

// withDatabase rewrites a DSN to address a different database on the same
// server, preserving credentials, host, port and every query parameter.
func withDatabase(dsn, dbName string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf(
			"DATABASE_URL must be a postgres:// URL so a per-test database can be addressed; got %q",
			redactDSN(dsn))
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// redactDSN strips any password before a DSN reaches an error message.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<unparseable DSN>"
	}
	return u.Redacted()
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
