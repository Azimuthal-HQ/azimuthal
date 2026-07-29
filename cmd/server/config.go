package main

import (
	"fmt"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
)

// loadConfig reads the environment and applies every boot-time setting that
// lives somewhere other than the returned struct. Every command in this binary
// goes through it; nothing calls config.Load directly.
//
// # Why this exists rather than a call in each command
//
// AZIMUTHAL_BCRYPT_COST is not read from the Config at hashing time — it is
// pushed into internal/core/auth once, at boot. `serve` wires that in
// newServer, but `admin create-user` and `admin reset-password` hash passwords
// without ever building a server, so a per-command call is a thing to forget,
// and forgetting it is silent: the operator raises the work factor, the CLI
// keeps writing hashes at the old one, and nothing says so.
//
// So the application step lives with the load step, and
// TestCmdServer_NoDirectConfigLoad fails on any command that reaches past it.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		// config.Load's own message already enumerates every problem it found
		// and names each environment variable, so wrapping it adds a prefix
		// and nothing else.
		return nil, fmt.Errorf("loading configuration: %w", err)
	}
	if err := auth.SetPasswordCost(cfg.BcryptCost); err != nil {
		return nil, fmt.Errorf("configuring password hashing: %w", err)
	}
	return cfg, nil
}
