package auth

import (
	"strings"
	"testing"
)

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("supersecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash == "supersecret" {
		t.Fatal("hash must not equal plaintext")
	}
	// bcrypt hashes begin with $2a$ or $2b$
	if !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash prefix, got: %s", hash[:4])
	}
}

func TestHashPassword_DifferentEachTime(t *testing.T) {
	h1, err := HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Error("bcrypt should produce unique salts for identical inputs")
	}
}

func TestComparePassword_Match(t *testing.T) {
	hash, err := HashPassword("correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := ComparePassword(hash, "correct"); err != nil {
		t.Errorf("expected match, got: %v", err)
	}
}

func TestComparePassword_Mismatch(t *testing.T) {
	hash, err := HashPassword("correct")
	if err != nil {
		t.Fatal(err)
	}
	if err := ComparePassword(hash, "wrong"); err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got: %v", err)
	}
}

func TestComparePassword_InvalidHash(t *testing.T) {
	if err := ComparePassword("not-a-bcrypt-hash", "password"); err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials for invalid hash, got: %v", err)
	}
}
