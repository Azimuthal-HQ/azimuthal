// Package config loads and validates application configuration from environment variables.
// It uses viper to read env vars and applies sensible defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Database
	DatabaseURL string

	// Object Storage (MinIO / S3 compatible)
	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageBucket    string
	StorageUseSSL    bool

	// Auth. JWT signing uses an RS256 key persisted in the database;
	// JWTPrivateKeyPath is only a one-time import path for deployments
	// upgrading from the legacy file-based key.
	JWTExpiry         time.Duration
	JWTPrivateKeyPath string

	// Queue
	QueueEnabled bool

	// CORS — explicit list of allowed origins. Empty list in production
	// rejects all cross-origin requests; "*" matches any origin.
	AllowedOrigins []string

	// Email
	SMTPHost string
	SMTPPort int
	SMTPFrom string

	// Administration (P2.5). AllowRegistration gates POST /auth/register —
	// false (the default) 404s it and makes invites the only way in.
	// InviteDelivery is how invite links reach people: "link" (the admin
	// copies the URL) or "email" (Azimuthal sends it; requires SMTP_HOST to
	// be explicitly configured — validated at startup). InviteTTL is the
	// invite expiry window.
	AllowRegistration bool
	InviteDelivery    string
	InviteTTL         time.Duration

	// TicketRefRequired makes the operator-supplied ticket reference
	// mandatory on every administrative mutation that accepts one. Boot-time
	// only, and deliberately not a runtime settings row: an organisation
	// turns this on once, at a production cutover, and a restart is the
	// honest cost of a change that alters what every admin action requires.
	// Default false — behaviour is unchanged until an operator opts in.
	TicketRefRequired bool

	// App
	AppEnv     string
	AppPort    int
	AppBaseURL string
	LogLevel   string
}

// Invite delivery modes.
const (
	// InviteDeliveryLink returns the invite URL to the admin, who sends it
	// however they like. No SMTP required. The default.
	InviteDeliveryLink = "link"
	// InviteDeliveryEmail makes Azimuthal send the invite itself. Requires
	// SMTP configuration; startup fails loudly without it.
	InviteDeliveryEmail = "email"
)

// Load reads configuration from environment variables and returns a validated Config.
// It fails fast with clear error messages if required variables are missing.
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	// Sensible defaults
	v.SetDefault("JWT_EXPIRY", "24h")
	v.SetDefault("JWT_PRIVATE_KEY_PATH", "./data/jwt-private.pem")
	v.SetDefault("SMTP_HOST", "localhost")
	v.SetDefault("SMTP_PORT", 1025)
	v.SetDefault("SMTP_FROM", "azimuthal@localhost")
	v.SetDefault("APP_ENV", "development")
	v.SetDefault("APP_PORT", 8080)
	v.SetDefault("APP_BASE_URL", "http://localhost:8080")
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("STORAGE_BUCKET", "azimuthal")
	v.SetDefault("STORAGE_USE_SSL", false)
	v.SetDefault("AZIMUTHAL_QUEUE_ENABLED", true)
	v.SetDefault("AZIMUTHAL_ALLOW_REGISTRATION", false)
	v.SetDefault("AZIMUTHAL_INVITE_DELIVERY", InviteDeliveryLink)
	v.SetDefault("AZIMUTHAL_INVITE_TTL", "168h")
	v.SetDefault("AZIMUTHAL_TICKET_REF_REQUIRED", false)

	cfg := &Config{
		DatabaseURL:       v.GetString("DATABASE_URL"),
		StorageEndpoint:   v.GetString("STORAGE_ENDPOINT"),
		StorageAccessKey:  v.GetString("STORAGE_ACCESS_KEY"),
		StorageSecretKey:  v.GetString("STORAGE_SECRET_KEY"),
		StorageBucket:     v.GetString("STORAGE_BUCKET"),
		StorageUseSSL:     v.GetBool("STORAGE_USE_SSL"),
		JWTPrivateKeyPath: v.GetString("JWT_PRIVATE_KEY_PATH"),
		QueueEnabled:      v.GetBool("AZIMUTHAL_QUEUE_ENABLED"),
		AllowRegistration: v.GetBool("AZIMUTHAL_ALLOW_REGISTRATION"),
		InviteDelivery:    v.GetString("AZIMUTHAL_INVITE_DELIVERY"),
		TicketRefRequired: v.GetBool("AZIMUTHAL_TICKET_REF_REQUIRED"),
		AllowedOrigins:    parseAllowedOrigins(v.GetString("AZIMUTHAL_ALLOWED_ORIGINS")),
		SMTPHost:          v.GetString("SMTP_HOST"),
		SMTPPort:          v.GetInt("SMTP_PORT"),
		SMTPFrom:          v.GetString("SMTP_FROM"),
		AppEnv:            v.GetString("APP_ENV"),
		AppPort:           v.GetInt("APP_PORT"),
		AppBaseURL:        v.GetString("APP_BASE_URL"),
		LogLevel:          v.GetString("LOG_LEVEL"),
	}

	if err := cfg.parseDurations(v); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parseDurations reads the duration-typed settings, failing loudly on
// unparseable or nonsensical values.
func (c *Config) parseDurations(v *viper.Viper) error {
	expiryStr := v.GetString("JWT_EXPIRY")
	expiry, err := time.ParseDuration(expiryStr)
	if err != nil {
		return fmt.Errorf("invalid JWT_EXPIRY %q: %w", expiryStr, err)
	}
	c.JWTExpiry = expiry

	inviteTTLStr := v.GetString("AZIMUTHAL_INVITE_TTL")
	inviteTTL, err := time.ParseDuration(inviteTTLStr)
	if err != nil {
		return fmt.Errorf("invalid AZIMUTHAL_INVITE_TTL %q: %w", inviteTTLStr, err)
	}
	if inviteTTL <= 0 {
		return fmt.Errorf("invalid AZIMUTHAL_INVITE_TTL %q: must be positive", inviteTTLStr)
	}
	c.InviteTTL = inviteTTL
	return nil
}

// IsTest reports whether the application is running in test mode.
func (c *Config) IsTest() bool {
	return c.AppEnv == "test"
}

// IsDevelopment reports whether the application is running in development mode.
func (c *Config) IsDevelopment() bool {
	return c.AppEnv == "development"
}

// IsProduction reports whether the application is running in production mode.
func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}

// parseAllowedOrigins splits a comma-separated origin list. When the env var
// is unset the result is empty in every environment, which means the server
// emits no CORS headers and the browser enforces same-origin. Cross-origin
// access is admitted only by setting AZIMUTHAL_ALLOWED_ORIGINS explicitly —
// security policy is a boot-time decision, never a runtime one.
//
// Development and test used to default to "*". Nothing in this repository
// needed it: in production the SPA is served from this same binary, and in
// development Vite proxies /api server-side (web/vite.config.ts), so in
// neither case does a browser make a cross-origin request. The permissive
// default only widened the blast radius of a malicious page in an operator's
// browser, so it is gone. An operator who really does serve the frontend from
// another origin sets AZIMUTHAL_ALLOWED_ORIGINS to that origin.
//
func parseAllowedOrigins(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

// validate checks that all required configuration is present.
// In test mode (APP_ENV=test) some validations are relaxed.
func (c *Config) validate() error {
	var errs []string

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	switch c.InviteDelivery {
	case InviteDeliveryLink:
		// No SMTP required — the admin copies the link.
	case InviteDeliveryEmail:
		// Fail loudly at startup rather than silently dropping invites at
		// send time. SMTP_HOST carries a localhost default for dev relay,
		// so "configured" means the operator set it explicitly.
		if os.Getenv("SMTP_HOST") == "" {
			errs = append(errs, "AZIMUTHAL_INVITE_DELIVERY=email requires SMTP_HOST to be set explicitly")
		}
		if c.SMTPFrom == "" {
			errs = append(errs, "AZIMUTHAL_INVITE_DELIVERY=email requires SMTP_FROM")
		}
	default:
		errs = append(errs, fmt.Sprintf("invalid AZIMUTHAL_INVITE_DELIVERY %q: must be %q or %q",
			c.InviteDelivery, InviteDeliveryLink, InviteDeliveryEmail))
	}

	if len(errs) > 0 {
		return errors.New("configuration errors:\n  - " + strings.Join(errs, "\n  - "))
	}

	return nil
}
