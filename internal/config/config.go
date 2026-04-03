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
	JWTSecret string
	JWTExpiry time.Duration

	// Email
	SMTPHost string
	SMTPPort int
	SMTPFrom string

	// Enterprise (EE builds only)
	LicenseKey string

	// App
	AppEnv     string
	AppPort    int
	AppBaseURL string
	LogLevel   string
}

// Load reads configuration from environment variables and returns a validated Config.
// It fails fast with clear error messages if required variables are missing.
func Load() (*Config, error) {
	v := viper.New()
	v.AutomaticEnv()

	// Sensible defaults
	v.SetDefault("JWT_EXPIRY", "24h")
	v.SetDefault("SMTP_HOST", "localhost")
	v.SetDefault("SMTP_PORT", 1025)
	v.SetDefault("SMTP_FROM", "azimuthal@localhost")
	v.SetDefault("APP_ENV", "development")
	v.SetDefault("APP_PORT", 8080)
	v.SetDefault("APP_BASE_URL", "http://localhost:8080")
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("STORAGE_BUCKET", "azimuthal")
	v.SetDefault("STORAGE_USE_SSL", false)

	cfg := &Config{
		DatabaseURL:      v.GetString("DATABASE_URL"),
		StorageEndpoint:  v.GetString("STORAGE_ENDPOINT"),
		StorageAccessKey: v.GetString("STORAGE_ACCESS_KEY"),
		StorageSecretKey: v.GetString("STORAGE_SECRET_KEY"),
		StorageBucket:    v.GetString("STORAGE_BUCKET"),
		StorageUseSSL:    v.GetBool("STORAGE_USE_SSL"),
		JWTSecret:        v.GetString("JWT_SECRET"),
		SMTPHost:         v.GetString("SMTP_HOST"),
		SMTPPort:         v.GetInt("SMTP_PORT"),
		SMTPFrom:         v.GetString("SMTP_FROM"),
		LicenseKey:       v.GetString("LICENSE_KEY"),
		AppEnv:           v.GetString("APP_ENV"),
		AppPort:          v.GetInt("APP_PORT"),
		AppBaseURL:       v.GetString("APP_BASE_URL"),
		LogLevel:         v.GetString("LOG_LEVEL"),
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
