package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

func TestLoad_MissingRequiredVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when required vars are missing, got nil")
	}

	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %s", err.Error())
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error when DATABASE_URL is missing")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("error should mention DATABASE_URL, got: %s", err.Error())
	}
}

// TestLoad_NoJWTSecretRequired: JWT signing uses an RS256 key persisted in
// the database (migration 018), so no JWT_SECRET env var exists or is
// required — deployments must not be forced to set a dead secret.
func TestLoad_NoJWTSecretRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "")

	_, err := config.Load()
	if err != nil {
		t.Fatalf("config must load without JWT_SECRET, got: %s", err.Error())
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
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

func TestConfig_IsProduction(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", "production")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IsProduction() {
		t.Error("expected IsProduction()=true when APP_ENV=production")
	}
}

func TestConfig_IsProduction_False(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", "development")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.IsProduction() {
		t.Error("expected IsProduction()=false when APP_ENV=development")
	}
}

func TestConfig_AllowedOrigins_Explicit(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", "production")
	t.Setenv("AZIMUTHAL_ALLOWED_ORIGINS", "https://app.example.com, https://admin.example.com")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 {
		t.Errorf("expected 2 allowed origins, got %d: %v", len(cfg.AllowedOrigins), cfg.AllowedOrigins)
	}
	if cfg.AllowedOrigins[0] != "https://app.example.com" {
		t.Errorf("unexpected first origin: %q", cfg.AllowedOrigins[0])
	}
}

func TestConfig_AllowedOrigins_ProductionEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", "production")
	t.Setenv("AZIMUTHAL_ALLOWED_ORIGINS", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Errorf("expected empty allowed origins in production, got %v", cfg.AllowedOrigins)
	}
}
