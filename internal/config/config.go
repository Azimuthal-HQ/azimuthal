// Package config loads and validates application configuration from environment variables.
// It uses viper to read env vars and applies sensible defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
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

	// BcryptCost is the password hashing work factor. Boot-time, and bounded
	// below by MinBcryptCost — a configuration asking for less is refused at
	// startup, in every environment. The floor is deliberately NOT relaxed by
	// APP_ENV: APP_ENV is an ordinary environment variable that a production
	// deployment can hold any value of, so an APP_ENV=test exemption would be
	// a one-line downgrade of every password in the database. Test binaries
	// get their cheap hashing from the linker knowing they are test binaries
	// (internal/core/auth), not from configuration.
	//
	// The knob is therefore up-only: it exists so an operator can raise the
	// cost as hardware gets faster.
	BcryptCost int

	// Customer portal. PortalLinkDelivery mirrors InviteDelivery: "link"
	// returns the sign-in URL in the API response (development and test
	// only — see validate) or "email" sends it. PortalLinkTTL is how long a
	// sign-in link stays redeemable; PortalSessionTTL is how long the
	// session it produces lasts.
	PortalLinkDelivery string
	PortalLinkTTL      time.Duration
	PortalSessionTTL   time.Duration

	// App
	AppEnv     string
	AppPort    int
	AppBaseURL string
	LogLevel   string
}

// Bcrypt work-factor bounds.
//
// These duplicate auth.MinBcryptCost / auth.DefaultBcryptCost and
// bcrypt.MaxCost rather than importing them, so internal/config keeps its
// property of importing nothing from internal/core. The duplication is made
// safe by TestBcryptFloorMatchesAuthPackage, which fails the moment the two
// disagree — without it this would be a copy waiting to drift.
const (
	// MinBcryptCost is the lowest work factor this server will accept.
	MinBcryptCost = 12
	// MaxBcryptCost is bcrypt's own ceiling.
	MaxBcryptCost = 31
	// DefaultBcryptCost is used when AZIMUTHAL_BCRYPT_COST is unset.
	DefaultBcryptCost = 12
)

// Invite delivery modes.
const (
	// InviteDeliveryLink returns the invite URL to the admin, who sends it
	// however they like. No SMTP required. The default.
	InviteDeliveryLink = "link"
	// InviteDeliveryEmail makes Azimuthal send the invite itself. Requires
	// SMTP configuration; startup fails loudly without it.
	InviteDeliveryEmail = "email"
)

// Customer-portal sign-in link delivery modes.
const (
	// PortalLinkDeliveryLink returns the sign-in URL in the API response so a
	// developer or an E2E run can follow it without a mailbox.
	//
	// PRODUCTION NEVER DISCLOSES THE URL — but by a runtime degrade, not a
	// boot refusal. cmd/server/main.go gates DiscloseLink on
	// `!cfg.IsProduction()`, so a production server left in this mode mints
	// sign-in links and hands them to nobody: the portal is inert rather than
	// unsafe. The disclosure has to be stopped because the request-link
	// endpoint is unauthenticated by necessity — an external requester has no
	// credential yet — so returning the URL to its caller would let anybody
	// sign in as any address they can name. That is not a misconfiguration to
	// warn about; it is a total authentication bypass.
	//
	// WHY validate() DOES NOT REFUSE THIS MODE IN PRODUCTION, which four
	// comments across three packages used to assert that it did: this is the
	// default, and the portal has no enable flag. Refusing it at startup would
	// stop every production deployment that never set the variable from
	// booting — including the majority that run no customer portal at all —
	// which is a breaking change to unrelated deployments in the name of a
	// feature they do not use. The runtime gate already makes the unsafe
	// outcome unreachable, so the boot-time policy convention is satisfied by
	// something cheaper than a refusal.
	PortalLinkDeliveryLink = "link"
	// PortalLinkDeliveryEmail sends the sign-in link to the address that
	// asked for it, which is the only delivery that authenticates anything.
	// Requires SMTP, exactly as invite email delivery does.
	PortalLinkDeliveryEmail = "email"
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
	v.SetDefault("AZIMUTHAL_BCRYPT_COST", DefaultBcryptCost)
	v.SetDefault("AZIMUTHAL_PORTAL_LINK_DELIVERY", PortalLinkDeliveryLink)
	v.SetDefault("AZIMUTHAL_PORTAL_LINK_TTL", "1h")
	v.SetDefault("AZIMUTHAL_PORTAL_SESSION_TTL", "72h")

	cfg := &Config{
		DatabaseURL:        v.GetString("DATABASE_URL"),
		StorageEndpoint:    v.GetString("STORAGE_ENDPOINT"),
		StorageAccessKey:   v.GetString("STORAGE_ACCESS_KEY"),
		StorageSecretKey:   v.GetString("STORAGE_SECRET_KEY"),
		StorageBucket:      v.GetString("STORAGE_BUCKET"),
		StorageUseSSL:      v.GetBool("STORAGE_USE_SSL"),
		JWTPrivateKeyPath:  v.GetString("JWT_PRIVATE_KEY_PATH"),
		QueueEnabled:       v.GetBool("AZIMUTHAL_QUEUE_ENABLED"),
		AllowRegistration:  v.GetBool("AZIMUTHAL_ALLOW_REGISTRATION"),
		InviteDelivery:     v.GetString("AZIMUTHAL_INVITE_DELIVERY"),
		TicketRefRequired:  v.GetBool("AZIMUTHAL_TICKET_REF_REQUIRED"),
		PortalLinkDelivery: v.GetString("AZIMUTHAL_PORTAL_LINK_DELIVERY"),
		AllowedOrigins:     parseAllowedOrigins(v.GetString("AZIMUTHAL_ALLOWED_ORIGINS")),
		SMTPHost:           v.GetString("SMTP_HOST"),
		SMTPPort:           v.GetInt("SMTP_PORT"),
		SMTPFrom:           v.GetString("SMTP_FROM"),
		AppEnv:             v.GetString("APP_ENV"),
		AppPort:            v.GetInt("APP_PORT"),
		AppBaseURL:         v.GetString("APP_BASE_URL"),
		LogLevel:           v.GetString("LOG_LEVEL"),
	}

	if err := cfg.parseDurations(v); err != nil {
		return nil, err
	}

	if err := cfg.parseBcryptCost(v); err != nil {
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

	// A sign-in link is a credential sitting in an inbox, so its window is
	// short by default (1h) — much shorter than an invite's seven days, which
	// is an onboarding step somebody may get to next week.
	portalLinkTTL, err := time.ParseDuration(v.GetString("AZIMUTHAL_PORTAL_LINK_TTL"))
	if err != nil {
		return fmt.Errorf("invalid AZIMUTHAL_PORTAL_LINK_TTL %q: %w", v.GetString("AZIMUTHAL_PORTAL_LINK_TTL"), err)
	}
	if portalLinkTTL <= 0 {
		return fmt.Errorf("invalid AZIMUTHAL_PORTAL_LINK_TTL %q: must be positive", v.GetString("AZIMUTHAL_PORTAL_LINK_TTL"))
	}
	c.PortalLinkTTL = portalLinkTTL

	portalSessionTTL, err := time.ParseDuration(v.GetString("AZIMUTHAL_PORTAL_SESSION_TTL"))
	if err != nil {
		return fmt.Errorf("invalid AZIMUTHAL_PORTAL_SESSION_TTL %q: %w", v.GetString("AZIMUTHAL_PORTAL_SESSION_TTL"), err)
	}
	if portalSessionTTL <= 0 {
		return fmt.Errorf("invalid AZIMUTHAL_PORTAL_SESSION_TTL %q: must be positive", v.GetString("AZIMUTHAL_PORTAL_SESSION_TTL"))
	}
	c.PortalSessionTTL = portalSessionTTL
	return nil
}

// parseBcryptCost reads AZIMUTHAL_BCRYPT_COST as an integer.
//
// Deliberately strconv over v.GetInt: viper coerces an unparseable value to 0
// silently, and 0 then fails the range check in validate() with "must be
// between 12 and 31", which sends an operator who typed "twelve" looking for
// a numeric bug that is not there. Range checking stays in validate() with
// the other messages; this only rejects things that are not numbers.
func (c *Config) parseBcryptCost(v *viper.Viper) error {
	raw := strings.TrimSpace(v.GetString("AZIMUTHAL_BCRYPT_COST"))
	if raw == "" {
		c.BcryptCost = DefaultBcryptCost
		return nil
	}
	cost, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid AZIMUTHAL_BCRYPT_COST %q: must be an integer", raw)
	}
	c.BcryptCost = cost
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

	// The portal's delivery mode, mirroring InviteDelivery above. Without this
	// an unrecognised value passed validation and then matched neither branch
	// in cmd/server/main.go, which sets portalSender only for "email" and
	// DiscloseLink only for "link" — so a typo left the portal minting sign-in
	// links and delivering them nowhere, with nothing said at startup and
	// nothing wrong in the logs. A customer simply never receives a link.
	//
	// Note what this deliberately does NOT do: refuse PortalLinkDeliveryLink
	// in production. See that constant's own comment for why.
	switch c.PortalLinkDelivery {
	case PortalLinkDeliveryLink:
		// No SMTP required, and no disclosure in production either.
	case PortalLinkDeliveryEmail:
		// Same reasoning as invite email delivery: fail at startup rather than
		// drop sign-in links at send time. SMTP_HOST carries a localhost
		// default for dev relay, so "configured" means explicitly set.
		if os.Getenv("SMTP_HOST") == "" {
			errs = append(errs, "AZIMUTHAL_PORTAL_LINK_DELIVERY=email requires SMTP_HOST to be set explicitly")
		}
		if c.SMTPFrom == "" {
			errs = append(errs, "AZIMUTHAL_PORTAL_LINK_DELIVERY=email requires SMTP_FROM")
		}
	default:
		errs = append(errs, fmt.Sprintf("invalid AZIMUTHAL_PORTAL_LINK_DELIVERY %q: must be %q or %q",
			c.PortalLinkDelivery, PortalLinkDeliveryLink, PortalLinkDeliveryEmail))
	}

	// No APP_ENV exemption, by design — see the BcryptCost field comment.
	if c.BcryptCost < MinBcryptCost || c.BcryptCost > MaxBcryptCost {
		errs = append(errs, fmt.Sprintf(
			"invalid AZIMUTHAL_BCRYPT_COST %d: must be between %d and %d",
			c.BcryptCost, MinBcryptCost, MaxBcryptCost))
	}

	if len(errs) > 0 {
		return errors.New("configuration errors:\n  - " + strings.Join(errs, "\n  - "))
	}

	return nil
}
