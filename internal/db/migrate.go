package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// MigrationFS holds the embedded SQL migration files.
// It is populated during init() from the migrations package.
var MigrationFS embed.FS

// Migrate runs all pending goose migrations against the given pool.
// It wraps the pgxpool with the stdlib adapter that goose requires.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(MigrationFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting goose dialect: %w", err)
	}

	// "." because migrations.FS embeds *.sql at its root.
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	return nil
}

// MigrateDown rolls back the most recent migration batch.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(MigrationFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("setting goose dialect: %w", err)
	}

	if err := goose.DownContext(ctx, db, "."); err != nil {
		return fmt.Errorf("rolling back migration: %w", err)
	}

	return nil
}

// MigrationVersion returns the current schema version as reported by goose.
func MigrationVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(MigrationFS)

	if err := goose.SetDialect("postgres"); err != nil {
		return 0, fmt.Errorf("setting goose dialect: %w", err)
	}

	version, err := goose.GetDBVersionContext(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("getting migration version: %w", err)
	}

	return version, nil
}
