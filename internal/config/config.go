// Package config loads and validates application configuration from environment variables.
// It uses viper to read env vars and applies sensible defaults.
package config

import (
	"errors"
	"fmt"
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

	// Auth
	JWTSecret         string
	JWTExpiry         time.Duration
	JWTPrivateKeyPath string

	// CORS — explicit list of allowed origins. Empty list in production
	// rejects all cross-origin requests; "*" matches any origin.
	AllowedOrigins []string

	// Email
	SMTPHost string
	SMTPPort int
	SMTPFrom string

	// App
	AppEnv     string
	AppPort    int
	AppBaseURL string
	LogLevel   string

	// QueueEnabled controls whether the background River job queue starts at
	// boot. Default true. Set AZIMUTHAL_QUEUE_ENABLED=false to disable for
	// self-hosters who do not need async workers.
	QueueEnabled bool
}

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

	cfg := &Config{
		DatabaseURL:       v.GetString("DATABASE_URL"),
		StorageEndpoint:   v.GetString("STORAGE_ENDPOINT"),
		StorageAccessKey:  v.GetString("STORAGE_ACCESS_KEY"),
		StorageSecretKey:  v.GetString("STORAGE_SECRET_KEY"),
		StorageBucket:     v.GetString("STORAGE_BUCKET"),
		StorageUseSSL:     v.GetBool("STORAGE_USE_SSL"),
		JWTSecret:         v.GetString("JWT_SECRET"),
		JWTPrivateKeyPath: v.GetString("JWT_PRIVATE_KEY_PATH"),
		AllowedOrigins:    parseAllowedOrigins(v.GetString("AZIMUTHAL_ALLOWED_ORIGINS"), v.GetString("APP_ENV")),
		SMTPHost:          v.GetString("SMTP_HOST"),
		SMTPPort:          v.GetInt("SMTP_PORT"),
		SMTPFrom:          v.GetString("SMTP_FROM"),
		AppEnv:            v.GetString("APP_ENV"),
		AppPort:           v.GetInt("APP_PORT"),
		AppBaseURL:        v.GetString("APP_BASE_URL"),
		LogLevel:          v.GetString("LOG_LEVEL"),
		QueueEnabled:      v.GetBool("AZIMUTHAL_QUEUE_ENABLED"),
	}

	expiryStr := v.GetString("JWT_EXPIRY")
	expiry, err := time.ParseDuration(expiryStr)
	if err != nil {
		return nil, fmt.Errorf("invalid JWT_EXPIRY %q: %w", expiryStr, err)
	}
	cfg.JWTExpiry = expiry

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
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
// is unset the default depends on AppEnv: development and test allow all
// origins ("*"); production denies all by default and forces the operator to
// configure AZIMUTHAL_ALLOWED_ORIGINS explicitly.
func parseAllowedOrigins(raw, appEnv string) []string {
	if raw == "" {
		if appEnv == "production" {
			return []string{}
		}
		return []string{"*"}
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

	if c.JWTSecret == "" {
		errs = append(errs, "JWT_SECRET is required")
	}

	if len(errs) > 0 {
		return errors.New("configuration errors:\n  - " + strings.Join(errs, "\n  - "))
	}

	return nil
}
