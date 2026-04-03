package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

func TestLoad_MissingRequiredVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when required vars are missing, got nil")
	}

	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("error should mention JWT_SECRET, got: %s", err.Error())
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_SECRET", "supersecretjwttokenfortest")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %s", err.Error())
	}
}

func TestLoad_MissingJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when JWT_SECRET is missing")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Errorf("error should mention JWT_SECRET, got: %s", err.Error())
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "supersecretjwttokenfortest")
	// Unset optional vars to verify defaults
	t.Setenv("APP_ENV", "")
	t.Setenv("APP_PORT", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("STORAGE_BUCKET", "")
	t.Setenv("JWT_EXPIRY", "")
	t.Setenv("SMTP_PORT", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppPort != 8080 {
		t.Errorf("expected default APP_PORT 8080, got %d", cfg.AppPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LOG_LEVEL 'info', got %q", cfg.LogLevel)
	}
	if cfg.StorageBucket != "azimuthal" {
		t.Errorf("expected default STORAGE_BUCKET 'azimuthal', got %q", cfg.StorageBucket)
	}
	if cfg.JWTExpiry != 24*time.Hour {
		t.Errorf("expected default JWT_EXPIRY 24h, got %v", cfg.JWTExpiry)
	}
	if cfg.SMTPPort != 1025 {
		t.Errorf("expected default SMTP_PORT 1025, got %d", cfg.SMTPPort)
	}
}

func TestLoad_ValidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "supersecretjwttokenfortest")
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("JWT_EXPIRY", "12h")
	t.Setenv("STORAGE_ENDPOINT", "http://localhost:9000")
	t.Setenv("STORAGE_ACCESS_KEY", "minioadmin")
	t.Setenv("STORAGE_SECRET_KEY", "minioadmin")
	t.Setenv("STORAGE_BUCKET", "test-bucket")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AppPort != 9090 {
		t.Errorf("expected APP_PORT 9090, got %d", cfg.AppPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LOG_LEVEL 'debug', got %q", cfg.LogLevel)
	}
	if cfg.JWTExpiry != 12*time.Hour {
		t.Errorf("expected JWT_EXPIRY 12h, got %v", cfg.JWTExpiry)
	}
	if cfg.StorageEndpoint != "http://localhost:9000" {
		t.Errorf("unexpected StorageEndpoint: %q", cfg.StorageEndpoint)
	}
	if !cfg.IsTest() {
		t.Error("expected IsTest() to be true when APP_ENV=test")
	}
}

func TestLoad_InvalidJWTExpiry(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "supersecretjwttokenfortest")
	t.Setenv("JWT_EXPIRY", "not-a-duration")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error for invalid JWT_EXPIRY, got nil")
	}
	if !strings.Contains(err.Error(), "JWT_EXPIRY") {
		t.Errorf("error should mention JWT_EXPIRY, got: %s", err.Error())
	}
}

func TestConfig_IsTest(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "supersecretjwttokenfortest")
	t.Setenv("APP_ENV", "test")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsTest() {
		t.Error("expected IsTest()=true")
	}
	if cfg.IsDevelopment() {
		t.Error("expected IsDevelopment()=false when APP_ENV=test")
	}
}
