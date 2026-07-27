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

// TestConfig_AllowedOrigins_EmptyByDefaultInEveryEnv pins the S5 default.
//
// Development and test used to default to []string{"*"}, which meant a
// developer's browser would honour a cross-origin call from any page on the
// internet to their local API. Nothing needs it — the SPA is served from this
// binary in production and proxied server-side by Vite in development — so the
// default is now empty everywhere and cross-origin access is opt-in.
func TestConfig_AllowedOrigins_EmptyByDefaultInEveryEnv(t *testing.T) {
	for _, appEnv := range []string{"development", "test", "staging", "production"} {
		t.Run(appEnv, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
			t.Setenv("APP_ENV", appEnv)
			t.Setenv("AZIMUTHAL_ALLOWED_ORIGINS", "")

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cfg.AllowedOrigins) != 0 {
				t.Errorf("APP_ENV=%s with no AZIMUTHAL_ALLOWED_ORIGINS: got %v, want none — "+
					"a permissive CORS default must not depend on the environment name",
					appEnv, cfg.AllowedOrigins)
			}
		})
	}
}

// TestConfig_AllowedOrigins_WildcardStaysOptIn keeps the escape hatch honest:
// an operator who really wants any-origin access can still ask for it, and the
// test above must not be satisfied by silently dropping "*" support.
func TestConfig_AllowedOrigins_WildcardStaysOptIn(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", "development")
	t.Setenv("AZIMUTHAL_ALLOWED_ORIGINS", "*")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "*" {
		t.Errorf("explicit wildcard: got %v, want [*]", cfg.AllowedOrigins)
	}
}

func TestConfig_AdministrationDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// P2.5 release-notes behaviour change: open registration defaults OFF —
	// invites are the way in. Instances relying on it must opt in.
	if cfg.AllowRegistration {
		t.Error("AllowRegistration must default to false")
	}
	if cfg.InviteDelivery != config.InviteDeliveryLink {
		t.Errorf("InviteDelivery must default to link, got %q", cfg.InviteDelivery)
	}
	if cfg.InviteTTL != 168*time.Hour {
		t.Errorf("InviteTTL must default to seven days, got %v", cfg.InviteTTL)
	}
}

func TestConfig_InviteDeliveryEmail_RequiresExplicitSMTP(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("AZIMUTHAL_INVITE_DELIVERY", "email")
	t.Setenv("SMTP_HOST", "")

	// Email delivery without explicitly configured SMTP must fail loudly at
	// startup — not silently drop invites at send time.
	if _, err := config.Load(); err == nil {
		t.Fatal("expected a configuration error for invite_delivery=email without SMTP_HOST")
	}

	t.Setenv("SMTP_HOST", "smtp.example.com")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error with SMTP_HOST set: %v", err)
	}
	if cfg.InviteDelivery != config.InviteDeliveryEmail {
		t.Errorf("expected email delivery, got %q", cfg.InviteDelivery)
	}
}

func TestConfig_InvalidInviteDeliveryRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("AZIMUTHAL_INVITE_DELIVERY", "carrier-pigeon")

	if _, err := config.Load(); err == nil {
		t.Fatal("expected a configuration error for an unknown invite delivery mode")
	}
}
