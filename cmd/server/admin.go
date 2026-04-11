package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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

// slugifyName converts a display name into a URL-safe slug.
// e.g. "Josh Ford" → "josh-ford"
func slugifyName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "default"
	}
	return s
}

// ensureOrgForUser creates or retrieves an organization with a slug derived from the display name.
func ensureOrgForUser(ctx context.Context, queries *generated.Queries, displayName string) (uuid.UUID, string, error) {
	orgSlug := slugifyName(displayName)

	existingOrg, err := queries.GetOrganizationBySlug(ctx, orgSlug)
	if err == nil {
		return existingOrg.ID, orgSlug, nil
	}

	orgID := uuid.New()
	orgDesc := fmt.Sprintf("Organization for %s", displayName)
	_, err = queries.CreateOrganization(ctx, generated.CreateOrganizationParams{
		ID:          orgID,
		Slug:        orgSlug,
		Name:        displayName,
		Description: &orgDesc,
		Plan:        "free",
	})
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("creating organization: %w", err)
	}
	return orgID, orgSlug, nil
}

// runCreateUser connects to the database and creates a user, organization, and membership.
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

	orgID, orgSlug, err := ensureOrgForUser(ctx, queries, createUserName)
	if err != nil {
		return fmt.Errorf("setting up organization: %w", err)
	}

	userSvc := auth.NewUserService(adapters.NewUserAdapter(queries, orgID))
	u, err := userSvc.CreateUser(ctx, createUserEmail, createUserName, createUserPassword)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	_, err = queries.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:        uuid.New(),
		OrgID:     orgID,
		UserID:    u.ID,
		Role:      "owner",
		InvitedBy: pgtype.UUID{},
	})
	if err != nil {
		return fmt.Errorf("creating membership: %w", err)
	}

	printCreateUserSuccess(u, orgSlug)
	return nil
}

// printCreateUserSuccess prints the success output after creating a user and org.
func printCreateUserSuccess(u *auth.User, orgSlug string) {
	fmt.Printf("\u2713 User created: %s (%s)\n", u.DisplayName, u.Email)
	fmt.Printf("\u2713 Organization created: %s (slug: %s)\n", u.DisplayName, orgSlug)
	fmt.Printf("\u2713 User added as owner\n")
	fmt.Println()
	fmt.Println("Login at: http://localhost:8080/login")
	fmt.Printf("Email:    %s\n", u.Email)
	fmt.Println("Password: <the password you provided>")
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

	userSvc := auth.NewUserService(adapters.NewUserAdapter(queries, uuid.Nil))
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
