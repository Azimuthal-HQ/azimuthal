package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
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
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default LOG_LEVEL info, got %v", cfg.LogLevel)
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
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected LOG_LEVEL debug, got %v", cfg.LogLevel)
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

// The invite and portal delivery settings share one validator, which reads the
// valid values off the INVITE constants. That is safe only while the two pairs
// hold the same strings, so this fails the moment they diverge — the same
// arrangement MinBcryptCost has with the auth package, for the same reason.
//
// Without it, renaming one pair would silently make the other accept a value it
// then could not act on: exactly the "matches neither branch in main.go" defect
// the portal validator was added to close.
func TestDeliveryModeConstantsAgree(t *testing.T) {
	if config.PortalLinkDeliveryLink != config.InviteDeliveryLink {
		t.Errorf("portal link mode %q must equal the invite one %q — validateDeliveryMode "+
			"validates both against the invite constants",
			config.PortalLinkDeliveryLink, config.InviteDeliveryLink)
	}
	if config.PortalLinkDeliveryEmail != config.InviteDeliveryEmail {
		t.Errorf("portal email mode %q must equal the invite one %q",
			config.PortalLinkDeliveryEmail, config.InviteDeliveryEmail)
	}
}

// --- LOG_LEVEL ---
//
// LOG_LEVEL was parsed into Config.LogLevel and read by nothing, while
// cmd/server/serve.go hardcoded slog.LevelInfo — so both env tables promised
// debug/warn/error worked and none of them did. It is now parsed into a
// slog.Level, which is what serve.go feeds to its handler's LevelVar.

func TestLoad_LogLevel_ParsesEveryDocumentedValue(t *testing.T) {
	for raw, want := range map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		// Case-insensitive, because an operator who wrote ERROR in a .env
		// meant error and should not be told otherwise.
		"ERROR": slog.LevelError,
		"Warn":  slog.LevelWarn,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
			t.Setenv("LOG_LEVEL", raw)

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("LOG_LEVEL=%q must load: %v", raw, err)
			}
			if cfg.LogLevel != want {
				t.Errorf("LOG_LEVEL=%q: expected %v, got %v", raw, want, cfg.LogLevel)
			}
		})
	}
}

// An unparseable level is refused rather than run at info. Delete the
// UnmarshalText check and this passes with the old silent behaviour, which is
// the whole point of asserting it.
func TestLoad_LogLevel_UnknownValueRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("LOG_LEVEL", "warning") // the near-miss, not a random string

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an unknown LOG_LEVEL to be refused at startup")
	}
	if !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("the error must name the variable, got %q", err)
	}
	if !strings.Contains(err.Error(), `"warning"`) {
		t.Errorf("the error must quote the offending value, got %q", err)
	}
}

// --- AZIMUTHAL_PORTAL_LINK_DELIVERY ---
//
// The portal's delivery mode gets the same treatment as the invite one,
// because an unrecognised value is worse here than it looks. main.go sets
// portalSender only for "email" and DiscloseLink only for "link", so a typo
// matches neither branch: the portal mints sign-in links and delivers them
// nowhere, with a clean startup and nothing wrong in the logs. The person who
// finds out is a customer who never receives a link.

func TestConfig_InvalidPortalLinkDeliveryRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("AZIMUTHAL_PORTAL_LINK_DELIVERY", "emial") // the typo that motivated this

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected a configuration error for an unknown portal link delivery mode")
	}
	// Named, not merely refused: an operator who typoed a value needs to be
	// told which variable and what the alternatives are.
	if !strings.Contains(err.Error(), "AZIMUTHAL_PORTAL_LINK_DELIVERY") {
		t.Errorf("the error must name the variable, got %q", err)
	}
	if !strings.Contains(err.Error(), `"emial"`) {
		t.Errorf("the error must quote the offending value, got %q", err)
	}
}

func TestConfig_PortalLinkDeliveryEmail_RequiresExplicitSMTP(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("AZIMUTHAL_PORTAL_LINK_DELIVERY", "email")
	t.Setenv("SMTP_HOST", "")

	// Same rule as invite email delivery: a delivery mode that cannot deliver
	// fails at startup rather than dropping sign-in links at send time.
	if _, err := config.Load(); err == nil {
		t.Fatal("expected a configuration error for portal_link_delivery=email without SMTP_HOST")
	}

	t.Setenv("SMTP_HOST", "smtp.example.com")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error with SMTP_HOST set: %v", err)
	}
	if cfg.PortalLinkDelivery != config.PortalLinkDeliveryEmail {
		t.Errorf("expected email delivery, got %q", cfg.PortalLinkDelivery)
	}
}

// The default must keep booting in EVERY environment, production included.
//
// This is the guard on the decision recorded in PortalLinkDeliveryLink's own
// comment. Four comments across three packages used to claim production
// refuses "link" at startup; it does not, and it must not, because "link" is
// the default and the portal has no enable flag — refusing it would stop
// every production deployment that runs no customer portal from booting.
// Production safety comes from main.go withholding DiscloseLink instead.
//
// Delete that reasoning and add the refusal, and this test fails.
func TestConfig_PortalLinkDeliveryLinkIsAcceptedInProduction(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", "production")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("the default portal delivery mode must not stop production booting: %v", err)
	}
	if cfg.PortalLinkDelivery != config.PortalLinkDeliveryLink {
		t.Errorf("expected the link default, got %q", cfg.PortalLinkDelivery)
	}
	if !cfg.IsProduction() {
		t.Fatal("this test only means something with APP_ENV=production")
	}
}

// --- AZIMUTHAL_BCRYPT_COST (B1) ---
//
// The floor is the entire security claim of making the work factor
// configurable, so it gets a test that fails when the check is deleted and a
// test that pins the APP_ENV hole shut.

func TestLoad_BcryptCost_BelowFloorRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("AZIMUTHAL_BCRYPT_COST", "4")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected a cost below the floor to be refused, got nil")
	}
	if !strings.Contains(err.Error(), "AZIMUTHAL_BCRYPT_COST") {
		t.Errorf("error should name AZIMUTHAL_BCRYPT_COST, got: %s", err.Error())
	}
}

// TestLoad_BcryptCost_FloorNotRelaxedByAppEnv is the regression test for the
// design that was rejected: a floor with an "unless APP_ENV=test" exemption.
// APP_ENV is an ordinary environment variable, so such an exemption would be
// a one-line downgrade of every password in a production database.
func TestLoad_BcryptCost_FloorNotRelaxedByAppEnv(t *testing.T) {
	for _, env := range []string{"test", "development", "production", ""} {
		t.Run("APP_ENV="+env, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
			t.Setenv("APP_ENV", env)
			t.Setenv("AZIMUTHAL_BCRYPT_COST", "4")

			_, err := config.Load()
			if err == nil {
				t.Fatalf("APP_ENV=%q must not relax the bcrypt floor", env)
			}
			if !strings.Contains(err.Error(), "AZIMUTHAL_BCRYPT_COST") {
				t.Errorf("error should name AZIMUTHAL_BCRYPT_COST, got: %s", err.Error())
			}
		})
	}
}

func TestLoad_BcryptCost_DefaultAndAboveFloor(t *testing.T) {
	t.Run("unset defaults to 12", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
		t.Setenv("AZIMUTHAL_BCRYPT_COST", "")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BcryptCost != config.DefaultBcryptCost {
			t.Errorf("expected the default %d, got %d", config.DefaultBcryptCost, cfg.BcryptCost)
		}
	})

	t.Run("raising it is allowed", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
		t.Setenv("AZIMUTHAL_BCRYPT_COST", "14")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.BcryptCost != 14 {
			t.Errorf("expected 14, got %d", cfg.BcryptCost)
		}
	})

	t.Run("a non-integer names itself", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
		t.Setenv("AZIMUTHAL_BCRYPT_COST", "twelve")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error for a non-integer cost")
		}
		// The message must echo what was typed. Viper coerces garbage to 0,
		// and "must be between 12 and 31: got 0" sends the operator hunting
		// for a numeric bug that is not there.
		if !strings.Contains(err.Error(), "twelve") {
			t.Errorf("error should quote the offending value, got: %s", err.Error())
		}
	})
}

// TestBcryptFloorMatchesAuthPackage is what makes duplicating the constants
// safe instead of dangerous: internal/config deliberately imports nothing
// from internal/core, so the bounds are stated twice, and this fails the
// moment the two statements disagree.
func TestBcryptFloorMatchesAuthPackage(t *testing.T) {
	if config.MinBcryptCost != auth.MinBcryptCost {
		t.Errorf("config.MinBcryptCost (%d) != auth.MinBcryptCost (%d)",
			config.MinBcryptCost, auth.MinBcryptCost)
	}
	if config.DefaultBcryptCost != auth.DefaultBcryptCost {
		t.Errorf("config.DefaultBcryptCost (%d) != auth.DefaultBcryptCost (%d)",
			config.DefaultBcryptCost, auth.DefaultBcryptCost)
	}
	if config.MaxBcryptCost != bcrypt.MaxCost {
		t.Errorf("config.MaxBcryptCost (%d) != bcrypt.MaxCost (%d)",
			config.MaxBcryptCost, bcrypt.MaxCost)
	}
}
