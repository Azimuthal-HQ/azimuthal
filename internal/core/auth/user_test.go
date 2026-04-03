package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubUserRepo is an in-memory UserRepository for testing.
type stubUserRepo struct {
	users map[string]*User // keyed by email
}

func newStubUserRepo() *stubUserRepo {
	return &stubUserRepo{users: make(map[string]*User)}
}

func (r *stubUserRepo) Create(_ context.Context, u *User) error {
	if _, exists := r.users[u.Email]; exists {
		return ErrEmailTaken
	}
	r.users[u.Email] = u
	return nil
}

func (r *stubUserRepo) GetByID(_ context.Context, id uuid.UUID) (*User, error) {
	for _, u := range r.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (r *stubUserRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	u, ok := r.users[email]
	if !ok {
		return nil, ErrNotFound
	}
	return u, nil
}

func (r *stubUserRepo) Update(_ context.Context, u *User) error {
	if _, exists := r.users[u.Email]; !exists {
		return ErrNotFound
	}
	r.users[u.Email] = u
	return nil
}

func (r *stubUserRepo) Delete(_ context.Context, id uuid.UUID) error {
	for email, u := range r.users {
		if u.ID == id {
			now := time.Now().UTC()
			u.DeletedAt = &now
			u.IsActive = false
			r.users[email] = u
			return nil
		}
	}
	return ErrNotFound
}

func TestUserService_CreateUser(t *testing.T) {
	svc := NewUserService(newStubUserRepo())

	u, err := svc.CreateUser(context.Background(), "alice@example.com", "Alice", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %s", u.Email)
	}
	if u.PasswordHash == "password123" {
		t.Error("password must be hashed")
	}
	if !u.IsActive {
		t.Error("new user must be active")
	}
	if u.ID == (uuid.UUID{}) {
		t.Error("user must have a non-zero UUID")
	}
}

func TestUserService_CreateUser_EmailRequired(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	if _, err := svc.CreateUser(context.Background(), "", "Alice", "pass"); err == nil {
		t.Error("expected error for empty email")
	}
}

func TestUserService_CreateUser_PasswordRequired(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	if _, err := svc.CreateUser(context.Background(), "a@b.com", "Alice", ""); err == nil {
		t.Error("expected error for empty password")
	}
}

func TestUserService_CreateUser_DuplicateEmail(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	if _, err := svc.CreateUser(context.Background(), "dup@example.com", "A", "pass1"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.CreateUser(context.Background(), "dup@example.com", "B", "pass2")
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("expected ErrEmailTaken, got %v", err)
	}
}

func TestUserService_GetUser(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	created, _ := svc.CreateUser(context.Background(), "bob@example.com", "Bob", "secret")

	got, err := svc.GetUser(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Email != "bob@example.com" {
		t.Errorf("wrong email: %s", got.Email)
	}
}

func TestUserService_GetUser_NotFound(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	_, err := svc.GetUser(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUserService_Authenticate(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	if _, err := svc.CreateUser(context.Background(), "carol@example.com", "Carol", "mypassword"); err != nil {
		t.Fatal(err)
	}

	u, err := svc.Authenticate(context.Background(), "carol@example.com", "mypassword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Email != "carol@example.com" {
		t.Errorf("wrong user returned: %s", u.Email)
	}
}

func TestUserService_Authenticate_WrongPassword(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	if _, err := svc.CreateUser(context.Background(), "dave@example.com", "Dave", "correct"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Authenticate(context.Background(), "dave@example.com", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestUserService_Authenticate_UnknownEmail(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	_, err := svc.Authenticate(context.Background(), "nobody@example.com", "pass")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

func TestUserService_Authenticate_InactiveAccount(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	u, _ := svc.CreateUser(context.Background(), "eve@example.com", "Eve", "pass")
	if err := svc.DeactivateUser(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Authenticate(context.Background(), "eve@example.com", "pass")
	if !errors.Is(err, ErrAccountInactive) {
		t.Errorf("expected ErrAccountInactive, got %v", err)
	}
}

func TestUserService_DeactivateUser(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	u, _ := svc.CreateUser(context.Background(), "frank@example.com", "Frank", "pass")

	if err := svc.DeactivateUser(context.Background(), u.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := svc.GetUser(context.Background(), u.ID)
	if got.IsActive {
		t.Error("user should be inactive after deactivation")
	}
	if got.DeletedAt == nil {
		t.Error("deleted_at should be set after deactivation")
	}
}
