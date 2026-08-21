package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestUserService_GetUserByEmail(t *testing.T) {
	repo := newStubUserRepo()
	svc := NewUserService(repo)
	ctx := context.Background()

	created, err := svc.CreateUser(ctx, "find@example.com", "Find Me", "pass123")
	if err != nil {
		t.Fatal(err)
	}

	found, err := svc.GetUserByEmailAcrossOrgs(ctx, "find@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmailAcrossOrgs: %v", err)
	}
	if found.ID != created.ID {
		t.Errorf("ID mismatch: got %v, want %v", found.ID, created.ID)
	}
}

func TestUserService_GetUserByEmail_NotFound(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	ctx := context.Background()

	_, err := svc.GetUserByEmailAcrossOrgs(ctx, "nobody@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUserService_UpdateUser(t *testing.T) {
	repo := newStubUserRepo()
	svc := NewUserService(repo)
	ctx := context.Background()

	created, err := svc.CreateUser(ctx, "update@example.com", "Original", "pass123")
	if err != nil {
		t.Fatal(err)
	}

	created.DisplayName = "Updated Name"
	err = svc.UpdateUser(ctx, created)
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}

	fetched, err := svc.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.DisplayName != "Updated Name" {
		t.Errorf("expected 'Updated Name', got %q", fetched.DisplayName)
	}
}

func TestUserService_UpdateProfile(t *testing.T) {
	repo := newStubUserRepo()
	svc := NewUserService(repo)
	ctx := context.Background()

	created, err := svc.CreateUser(ctx, "profile@example.com", "Old Name", "pass123")
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpdateProfile(ctx, created.ID, "New Name", "newprofile@example.com")
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.DisplayName != "New Name" {
		t.Errorf("expected 'New Name', got %q", updated.DisplayName)
	}
	if updated.Email != "newprofile@example.com" {
		t.Errorf("expected 'newprofile@example.com', got %q", updated.Email)
	}
}

func TestUserService_UpdateProfile_NotFound(t *testing.T) {
	svc := NewUserService(newStubUserRepo())
	ctx := context.Background()

	_, err := svc.UpdateProfile(ctx, uuid.New(), "Name", "email@example.com")
	if err == nil {
		t.Error("expected error for non-existent user")
	}
}
