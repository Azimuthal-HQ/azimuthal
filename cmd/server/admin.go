package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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
	createUserRole     string
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
	createUserCmd.Flags().StringVar(&createUserRole, "role", "owner",
		"org membership role: owner, admin, or member (owner/admin are org admins under ADR-0007)")
	_ = createUserCmd.MarkFlagRequired("email")
	_ = createUserCmd.MarkFlagRequired("name")
	_ = createUserCmd.MarkFlagRequired("password")

	adminCmd.AddCommand(createUserCmd)
	adminCmd.AddCommand(resetPasswordCmd)
	adminCmd.AddCommand(verifySplitCmd)
}

// --- verify-split ---

var verifySplitCmd = &cobra.Command{
	Use:   "verify-split",
	Short: "Verify items_archive row counts match tickets + project_items after P5 migration",
	RunE:  runVerifySplit,
}

func runVerifySplit(_ *cobra.Command, _ []string) error {
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

	archiveTickets, err := queries.CountItemsArchiveTickets(ctx)
	if err != nil {
		return fmt.Errorf("counting archive tickets: %w", err)
	}

	archiveProjectItems, err := queries.CountItemsArchiveProjectItems(ctx)
	if err != nil {
		return fmt.Errorf("counting archive project items: %w", err)
	}

	// Count live rows in new tables (including soft-deleted for parity).
	var liveTickets int64
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM tickets").Scan(&liveTickets); err != nil {
		return fmt.Errorf("counting live tickets: %w", err)
	}

	var liveProjectItems int64
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM project_items").Scan(&liveProjectItems); err != nil {
		return fmt.Errorf("counting live project items: %w", err)
	}

	ok := true
	fmt.Printf("items_archive tickets:      %d\n", archiveTickets)
	fmt.Printf("tickets table:              %d\n", liveTickets)
	if archiveTickets != liveTickets {
		fmt.Printf("  ⚠️  MISMATCH: archive=%d live=%d delta=%d\n", archiveTickets, liveTickets, liveTickets-archiveTickets)
		ok = false
	} else {
		fmt.Printf("  ✅ ticket counts match\n")
	}

	fmt.Printf("items_archive project_items: %d\n", archiveProjectItems)
	fmt.Printf("project_items table:         %d\n", liveProjectItems)
	if archiveProjectItems != liveProjectItems {
		fmt.Printf("  ⚠️  MISMATCH: archive=%d live=%d delta=%d\n", archiveProjectItems, liveProjectItems, liveProjectItems-archiveProjectItems)
		ok = false
	} else {
		fmt.Printf("  ✅ project item counts match\n")
	}

	if ok {
		fmt.Println("\n✅ verify-split: zero divergence")
	} else {
		fmt.Println("\n❌ verify-split: divergence detected — manual investigation required")
		return fmt.Errorf("split divergence detected")
	}
	return nil
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
	})
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("creating organization: %w", err)
	}

	// Every org needs its seeded default workflows, no matter which path
	// created it — without them AssignDefaultWorkflowToSpace fails for
	// every space in the org. (The register endpoint seeds via
	// OrgProvisionerAdapterWithWorkflows; the CLI must do the same.)
	if err := adapters.NewWorkflowAdapter(queries).SeedDefaultWorkflows(ctx, orgID); err != nil {
		return uuid.Nil, "", fmt.Errorf("seeding default workflows: %w", err)
	}
	// Likewise every org needs its default Vector item types, no matter which
	// path created it — without them item creation fails type validation. (The
	// register endpoint seeds via OrgProvisionerAdapter.WithItemTypeSeeder.)
	if err := adapters.NewItemTypeAdapter(queries).SeedDefaults(ctx, orgID); err != nil {
		return uuid.Nil, "", fmt.Errorf("seeding default item types: %w", err)
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

	switch createUserRole {
	case "owner", "admin", "member":
	default:
		return fmt.Errorf("invalid --role %q: must be owner, admin, or member", createUserRole)
	}

	orgID, orgSlug, err := ensureOrgForUser(ctx, queries, createUserName)
	if err != nil {
		return fmt.Errorf("setting up organization: %w", err)
	}

	userSvc := auth.NewUserService(adapters.NewUserAdapter(pool, orgID))
	u, err := userSvc.CreateUser(ctx, createUserEmail, createUserName, createUserPassword)
	if err != nil {
		return fmt.Errorf("creating user: %w", err)
	}

	_, err = queries.CreateMembership(ctx, generated.CreateMembershipParams{
		ID:        uuid.New(),
		OrgID:     orgID,
		UserID:    u.ID,
		Role:      createUserRole,
		InvitedBy: pgtype.UUID{},
	})
	if err != nil {
		return fmt.Errorf("creating membership: %w", err)
	}

	if err := provisionDefaultTeam(ctx, pool, orgID, u.ID); err != nil {
		return err
	}

	printCreateUserSuccess(u, orgSlug, createUserRole)
	return nil
}

// provisionDefaultTeam mirrors the register endpoint's team provisioning:
// the org has a default team and every member belongs to it (ADR-0006
// point 4).
func provisionDefaultTeam(ctx context.Context, pool *pgxpool.Pool, orgID, userID uuid.UUID) error {
	teamAdapter := adapters.NewTeamAdapter(pool)
	if err := teamAdapter.SeedDefaultTeam(ctx, orgID); err != nil {
		return fmt.Errorf("seeding default team: %w", err)
	}
	if err := teamAdapter.EnsureDefaultMembership(ctx, orgID, userID); err != nil {
		return fmt.Errorf("enrolling in default team: %w", err)
	}
	return nil
}

// printCreateUserSuccess prints the success output after creating a user and org.
func printCreateUserSuccess(u *auth.User, orgSlug, role string) {
	fmt.Printf("\u2713 User created: %s (%s)\n", u.DisplayName, u.Email)
	fmt.Printf("\u2713 Organization created: %s (slug: %s)\n", u.DisplayName, orgSlug)
	fmt.Printf("\u2713 User added as %s\n", role)
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

	userSvc := auth.NewUserService(adapters.NewUserAdapter(pool, uuid.Nil))
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
