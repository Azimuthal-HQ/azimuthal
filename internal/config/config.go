// Package config loads and validates application configuration from environment variables.
// It uses viper to read env vars and applies sensible defaults.
package config

import (
	"errors"
	"fmt"
	"log/slog"
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
	// means the operator is responsible for getting the sign-in URL to the
	// requester, "email" means Azimuthal sends it. PortalLinkTTL is how long a
	// sign-in link stays redeemable; PortalSessionTTL is how long the
	// session it produces lasts.
	//
	// PortalLinkDelivery NO LONGER DECIDES DISCLOSURE. It used to: the URL was
	// returned in the response body whenever the mode was "link" and the
	// environment was not production, and since both of those were the
	// defaults, a stock install disclosed. Delivery and disclosure are now
	// separate settings — see PortalDiscloseLink.
	PortalLinkDelivery string
	PortalLinkTTL      time.Duration
	PortalSessionTTL   time.Duration

	// PortalDiscloseLink is the operator's REQUEST that the portal's
	// request-link response body carry the sign-in URL. It is not the answer:
	// read PortalLinkDisclosureAllowed(), which is the answer, and which also
	// refuses in production.
	//
	// Disclosure is an authentication bypass wherever it is on. POST
	// /portal/{key}/auth/request-link is unauthenticated by necessity — an
	// external requester has no credential yet — so a response that carries the
	// URL signs the caller in as any address they can name. The affordance
	// exists at all because a browser test and a developer without a mailbox
	// have no other way to follow a link, which is a real need, but it is a need
	// nobody has by accident. Hence: default false, and an operator who wants it
	// has to say so in as many words.
	//
	// This is deliberately a flag of its own rather than a third delivery mode.
	// A mode is a choice between alternatives and reads as one; disclosure is
	// not an alternative to emailing a link, it is a decision to publish a
	// credential, and it should have to be spelled that way.
	PortalDiscloseLink bool

	// App
	AppEnv     string
	AppPort    int
	AppBaseURL string
	// LogLevel is the minimum level the server logs at, read by
	// cmd/server/serve.go. It is stored PARSED rather than as the raw string
	// so that a value which is not a level cannot be represented — LOG_LEVEL
	// is checked at Load like every other malformed setting here, instead of
	// being accepted and then quietly ignored, which is what it was until the
	// config & build integrity follow-up.
	LogLevel slog.Level
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
	// PortalLinkDeliveryLink means Azimuthal sends nothing and the operator is
	// responsible for getting the sign-in URL to the requester. It requires no
	// SMTP relay, which is why it is the default.
	//
	// THIS MODE NO LONGER DISCLOSES ANYTHING. It used to be half of the
	// disclosure rule — main.go set DiscloseLink when the mode was "link" AND
	// the environment was not production — and because "link" and
	// "development" were both defaults, a stock install returned the sign-in
	// URL to an unauthenticated caller. Disclosure is now its own flag,
	// PortalDiscloseLink, defaulting to false; this constant is back to
	// meaning only what its name says.
	//
	// WHY validate() STILL DOES NOT REFUSE THIS MODE IN PRODUCTION, which four
	// comments across three packages once wrongly asserted that it did: this is
	// the default, and the portal has no enable flag. Refusing it at startup
	// would stop every production deployment that never set the variable from
	// booting — including the majority that run no customer portal at all —
	// which is a breaking change to unrelated deployments in the name of a
	// feature they do not use. There is now nothing unsafe left in the mode to
	// refuse: the thing that was dangerous moved to a setting whose default is
	// off. TestConfig_PortalLinkDeliveryLinkIsAcceptedInProduction pins this.
	PortalLinkDeliveryLink = "link"
	// PortalLinkDeliveryEmail sends the sign-in link to the address that
	// asked for it, which is the only delivery that authenticates anything.
	// Requires SMTP, exactly as invite email delivery does.
	PortalLinkDeliveryEmail = "email"
)

// setDefaults registers every setting that has one.
//
// Split out of Load so that adding a setting does not push Load past the
// function-length gate, and so the defaults sit in one readable block. These
// values are the contract build/docker-compose.yml relies on: it forwards
// operator settings as bare ${KEY}, letting an unset variable fall through to
// exactly this table rather than repeating it. TestComposeForwardsOperator
// SettingsWithoutADefault keeps the two from diverging.
func setDefaults(v *viper.Viper) {
	v.SetDefault("JWT_EXPIRY", "24h")
	v.SetDefault("JWT_PRIVATE_KEY_PATH", "./data/jwt-private.pem")
	v.SetDefault("SMTP_HOST", "localhost")
	v.SetDefault("SMTP_PORT", 1025)
	v.SetDefault("SMTP_FROM", "azimuthal@localhost")
	// APP_ENV defaults to production, not development.
	//
	// An unset variable must describe the deployment that actually exists when
	// nobody set anything, and for a self-hosted product that is somebody's
	// server, not somebody's laptop. A developer runs `docker compose -f
	// build/docker-compose.dev.yml`, or a Makefile target, or a test harness —
	// every one of which sets APP_ENV explicitly — so the development default
	// was serving the case that never relied on it while the production case
	// silently inherited a developer's safety posture.
	//
	// The concrete cost of the old default: IsProduction() was false on a bare
	// `docker run`, which turned the portal's magic-link disclosure on. It is
	// no longer the only thing standing between that endpoint and an
	// authentication bypass — see PortalDiscloseLink — but "the environment
	// name defaults to the safe one" should not have needed a second control to
	// be true.
	v.SetDefault("APP_ENV", "production")
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
	v.SetDefault("AZIMUTHAL_PORTAL_DISCLOSE_LINK", false)
	v.SetDefault("AZIMUTHAL_PORTAL_LINK_TTL", "1h")
	v.SetDefault("AZIMUTHAL_PORTAL_SESSION_TTL", "72h")
}

// Load reads configuration from environment variables and returns a validated Config.
// It fails fast with clear error messages if required variables are missing.
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()
	setDefaults(v)

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
		PortalDiscloseLink: v.GetBool("AZIMUTHAL_PORTAL_DISCLOSE_LINK"),
		AllowedOrigins:     parseAllowedOrigins(v.GetString("AZIMUTHAL_ALLOWED_ORIGINS")),
		SMTPHost:           v.GetString("SMTP_HOST"),
		SMTPPort:           v.GetInt("SMTP_PORT"),
		SMTPFrom:           v.GetString("SMTP_FROM"),
		AppEnv:             v.GetString("APP_ENV"),
		AppPort:            v.GetInt("APP_PORT"),
		AppBaseURL:         v.GetString("APP_BASE_URL"),
	}

	if err := cfg.parseLogLevel(v); err != nil {
		return nil, err
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

// parseLogLevel reads LOG_LEVEL into a slog.Level.
//
// slog.Level.UnmarshalText accepts "debug", "info", "warn" and "error" in any
// case, plus offsets such as "info+2". Anything else is refused at startup
// rather than silently run at info, on the same reasoning as every other
// malformed setting in this file: a value the server ignores is worse than one
// it rejects, because an operator who typed "warning" and got info-level logs
// has no way to tell that from a server that is genuinely quiet.
//
// The viper default guarantees a non-empty string here — an env var set to ""
// is treated as unset (AllowEmptyEnv is off), so this sees "info" rather than
// the empty string, which UnmarshalText would reject.
func (c *Config) parseLogLevel(v *viper.Viper) error {
	raw := strings.TrimSpace(v.GetString("LOG_LEVEL"))
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		return fmt.Errorf("invalid LOG_LEVEL %q: must be debug, info, warn or error", raw)
	}
	c.LogLevel = lvl
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

// PortalLinkDisclosureAllowed reports whether the customer portal may return a
// sign-in URL in the body of its unauthenticated request-link response.
//
// THIS IS THE ONLY PLACE THE RULE IS STATED. cmd/server/main.go calls it and
// does no arithmetic of its own; TestMainWiresPortalDisclosureToTheConfigRule
// (cmd/server) fails if that stops being true, because a correct rule nobody
// calls is exactly the shape of the defect this replaced.
//
// Both conjuncts are load-bearing, and they are load-bearing against different
// mistakes:
//
//   - PortalDiscloseLink is what makes disclosure deliberate. The rule it
//     replaced was a conjunction of two settings that were BOTH defaults, so
//     the unsafe state was the one an operator reached by doing nothing.
//   - !IsProduction() is what makes it un-footgunnable. An operator who copies
//     a development .env onto a production host — the way this class recurs —
//     gets the flag they did not mean to bring, and production refuses it
//     anyway.
//
// A production server therefore never discloses, whatever the flag says. It is
// still a runtime degrade rather than a boot refusal: validate() does not reject
// the combination, on the same reasoning that keeps it from rejecting
// PortalLinkDeliveryLink in production. Whether an operator who explicitly asks
// for disclosure in production should instead be refused at startup is a
// maintainer decision, not one to take here.
func (c *Config) PortalLinkDisclosureAllowed() bool {
	return c.PortalDiscloseLink && !c.IsProduction()
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

// validateDeliveryMode checks one of the two delivery settings — invites and
// customer-portal sign-in links — which carry the identical rule: "link" needs
// nothing, "email" needs an explicitly configured relay, anything else is a
// typo and refuses boot.
//
// ONE IMPLEMENTATION, NOT TWO. The portal setting was added by copying the
// invite one, and the copy is exactly where a rule like this drifts — the
// direction being "one of them stops checking". envVar is a parameter so each
// message still names the setting the operator actually set.
//
// The valid values are the invite constants; the portal pair holds the same two
// strings, and TestDeliveryModeConstantsAgree fails the moment that stops being
// true. That is the same arrangement MinBcryptCost uses against the auth
// package, for the same reason.
func validateDeliveryMode(envVar, mode, smtpFrom string) []string {
	var errs []string
	switch mode {
	case InviteDeliveryLink:
		// Nothing is sent, so no relay is needed.
	case InviteDeliveryEmail:
		// Fail loudly at startup rather than dropping mail at send time.
		//
		// Deliberately a raw os.Getenv rather than c.SMTPHost: SMTP_HOST carries
		// a "localhost" default for a dev relay, so the config field is never
		// empty and could not distinguish "an operator configured a relay" from
		// "nobody set anything". This is also why build/docker-compose.yml must
		// forward SMTP_HOST bare — a `${SMTP_HOST:-localhost}` there satisfies
		// this check unconditionally and silently disables it.
		if os.Getenv("SMTP_HOST") == "" {
			errs = append(errs, envVar+"=email requires SMTP_HOST to be set explicitly")
		}
		if smtpFrom == "" {
			errs = append(errs, envVar+"=email requires SMTP_FROM")
		}
	default:
		errs = append(errs, fmt.Sprintf("invalid %s %q: must be %q or %q",
			envVar, mode, InviteDeliveryLink, InviteDeliveryEmail))
	}
	return errs
}

// validate checks that all required configuration is present.
// In test mode (APP_ENV=test) some validations are relaxed.
func (c *Config) validate() error {
	var errs []string

	if c.DatabaseURL == "" {
		errs = append(errs, "DATABASE_URL is required")
	}

	errs = append(errs, validateDeliveryMode(
		"AZIMUTHAL_INVITE_DELIVERY", c.InviteDelivery, c.SMTPFrom)...)

	// The portal's sign-in links run the identical rule. Before this existed an
	// unrecognised value passed validation and then matched neither branch in
	// cmd/server/main.go — which at the time set portalSender only for "email"
	// and DiscloseLink only for "link" — so a typo left the portal minting
	// sign-in links and delivering them nowhere, with nothing said at startup
	// and nothing wrong in the logs. A customer simply never receives a link.
	// Disclosure has since moved off the mode entirely, but the silent-nowhere
	// outcome is unchanged for "email"-shaped typos, so the check still earns
	// its place.
	//
	// Note the two things this deliberately does NOT do: refuse
	// PortalLinkDeliveryLink in production (see that constant's own comment),
	// and refuse AZIMUTHAL_PORTAL_DISCLOSE_LINK=true in production (see
	// PortalLinkDisclosureAllowed — the flag is ignored there rather than
	// rejected).
	errs = append(errs, validateDeliveryMode(
		"AZIMUTHAL_PORTAL_LINK_DELIVERY", c.PortalLinkDelivery, c.SMTPFrom)...)

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
