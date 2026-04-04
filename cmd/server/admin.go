package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/auth"
	"github.com/Azimuthal-HQ/azimuthal/internal/db"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Administrative commands",
}

// --- create-user ---

var (
	createUserEmail    string
	createUserName     string
	createUserPassword string
)

var createUserCmd = &cobra.Command{
	Use:   "create-user",
	Short: "Create a new user account",
	RunE:  runCreateUser,
}

func init() {
	createUserCmd.Flags().StringVar(&createUserEmail, "email", "", "user email address (required)")
	createUserCmd.Flags().StringVar(&createUserName, "name", "", "display name (required)")
	createUserCmd.Flags().StringVar(&createUserPassword, "password", "", "initial password (required)")
	_ = createUserCmd.MarkFlagRequired("email")
	_ = createUserCmd.MarkFlagRequired("name")
	_ = createUserCmd.MarkFlagRequired("password")

	adminCmd.AddCommand(createUserCmd)
	adminCmd.AddCommand(resetPasswordCmd)
}

// runCreateUser connects to the database and creates a user.
func runCreateUser(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, db.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	queries := generated.New(pool)

	orgID, err := ensureDefaultOrg(ctx, queries)
	if err != nil {
		return fmt.Errorf("getting default org: %w", err)
	}

	userSvc := auth.NewUserService(adapters.NewUserAdapter(queries, orgID))
	u, err := userSvc.CreateUser(ctx, createUserEmail, createUserName, createUserPassword)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	fmt.Printf("User created:\n  ID:    %s\n  Email: %s\n  Name:  %s\n", u.ID, u.Email, u.DisplayName)
	return nil
}

// --- reset-password ---

var (
	resetEmail    string
	resetPassword string
)

var resetPasswordCmd = &cobra.Command{
	Use:   "reset-password",
	Short: "Reset a user's password",
	RunE:  runResetPassword,
}

func init() {
	resetPasswordCmd.Flags().StringVar(&resetEmail, "email", "", "user email address (required)")
	resetPasswordCmd.Flags().StringVar(&resetPassword, "password", "", "new password (required)")
	_ = resetPasswordCmd.MarkFlagRequired("email")
	_ = resetPasswordCmd.MarkFlagRequired("password")
}

// runResetPassword looks up a user by email and updates their password hash.
func runResetPassword(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, db.DefaultConfig(cfg.DatabaseURL))
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	queries := generated.New(pool)

	orgID, err := ensureDefaultOrg(ctx, queries)
	if err != nil {
		return fmt.Errorf("getting default org: %w", err)
	}

	userSvc := auth.NewUserService(adapters.NewUserAdapter(queries, orgID))
	u, err := userSvc.GetUserByEmail(ctx, resetEmail)
	if err != nil {
		return fmt.Errorf("finding user: %w", err)
	}

	hash, err := auth.HashPassword(resetPassword)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	u.PasswordHash = hash
	if err := userSvc.UpdateUser(ctx, u); err != nil {
		return fmt.Errorf("updating password: %w", err)
	}

	fmt.Printf("Password reset for %s\n", resetEmail)
	return nil
}
