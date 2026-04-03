// Package db manages database connectivity, migrations, and sqlc-generated queries.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is an alias for pgxpool.Pool, re-exported for use by other packages.
type Pool = pgxpool.Pool

// Config holds database connection configuration.
type Config struct {
	// URL is the full PostgreSQL connection string.
	URL             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	HealthTimeout   time.Duration
}

// DefaultConfig returns a Config with sensible defaults for the given URL.
func DefaultConfig(url string) Config {
	return Config{
		URL:             url,
		MaxConns:        25,
		MinConns:        2,
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
		HealthTimeout:   5 * time.Second,
	}
}

// Connect creates and validates a pgxpool connection pool.
// The pool is configured with the provided Config and verified with a ping.
func Connect(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	timeout := cfg.HealthTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	if err := Ping(ctx, pool, timeout); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}

// Ping verifies that the database is reachable within the given timeout.
func Ping(ctx context.Context, pool *pgxpool.Pool, timeout time.Duration) error {
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}

	return nil
}
