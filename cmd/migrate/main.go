// Package main provides a standalone migration runner for CI and deployment.
// It runs goose migrations against the DATABASE_URL env var.
// This avoids the need to install the goose CLI binary (which has transitive
// deps not in our go.sum).
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	dir := findMigrationsDir()

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatalf("opening db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("setting dialect: %v", err)
	}
	if err := goose.Up(db, dir); err != nil {
		log.Fatalf("running migrations: %v", err)
	}

	fmt.Println("migrations applied successfully")
}

func findMigrationsDir() string {
	if dir := os.Getenv("MIGRATIONS_DIR"); dir != "" {
		return dir
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "migrations")
}
