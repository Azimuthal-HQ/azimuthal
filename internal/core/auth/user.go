package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// User represents an authenticated user in the system.
type User struct {
	ID           uuid.UUID
	OrgID        uuid.UUID
	Email        string
	DisplayName  string
	PasswordHash string
	Role         string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

// UserRepository defines the data access contract for users.
// The concrete implementation lives in internal/db once Agent 1A merges.
type UserRepository interface {
	// Create persists a new user. Returns ErrEmailTaken if the email exists.
	Create(ctx context.Context, u *User) error
	// GetByID retrieves a user by primary key. Returns ErrNotFound if absent.
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	// GetByEmail retrieves a user by email address. Returns ErrNotFound if absent.
	GetByEmail(ctx context.Context, email string) (*User, error)
	// Update persists changes to an existing user record.
	Update(ctx context.Context, u *User) error
	// Delete soft-deletes a user by setting deleted_at.
	Delete(ctx context.Context, id uuid.UUID) error
}

// UserService handles user account management.
type UserService struct {
	repo UserRepository
}

// NewUserService creates a UserService backed by the given repository.
func NewUserService(repo UserRepository) *UserService {
	return &UserService{repo: repo}
}

// CreateUser registers a new user with a bcrypt-hashed password.
func (s *UserService) CreateUser(ctx context.Context, email, displayName, password string) (*User, error) {
	if email == "" {
		return nil, fmt.Errorf("creating user: email is required")
	}
	if password == "" {
		return nil, fmt.Errorf("creating user: password is required")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	now := time.Now().UTC()
	u := &User{
		ID:           uuid.New(),
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: hash,
		IsActive:     true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

// GetUser retrieves a user by ID.
func (s *UserService) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	return u, nil
}

// GetUserByEmail retrieves a user by email address.
func (s *UserService) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("getting user by email: %w", err)
	}
	return u, nil
}

// UpdateUser persists changes to a user's profile.
func (s *UserService) UpdateUser(ctx context.Context, u *User) error {
	u.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, u); err != nil {
		return fmt.Errorf("updating user: %w", err)
	}
	return nil
}

// DeactivateUser soft-deletes a user account.
func (s *UserService) DeactivateUser(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("deactivating user: %w", err)
	}
	return nil
}

// Authenticate verifies email + password and returns the matching user.
// Returns ErrInvalidCredentials if the email is unknown or the password is wrong.
// Returns ErrAccountInactive if the account has been deactivated.
func (s *UserService) Authenticate(ctx context.Context, email, password string) (*User, error) {
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		// Don't reveal whether the email exists.
		return nil, ErrInvalidCredentials
	}
	if !u.IsActive {
		return nil, ErrAccountInactive
	}
	if err := ComparePassword(u.PasswordHash, password); err != nil {
		return nil, ErrInvalidCredentials
	}
	return u, nil
}
