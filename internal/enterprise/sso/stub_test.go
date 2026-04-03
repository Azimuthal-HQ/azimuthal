//go:build !enterprise

package sso

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestCommunityProvider_IsAvailable(t *testing.T) {
	p := NewProvider()
	if p.IsAvailable() {
		t.Error("community stub IsAvailable() must return false")
	}
}

func TestCommunityProvider_BeginAuth(t *testing.T) {
	p := NewProvider()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/sso", nil)
	err := p.BeginAuth(rr, req)
	if !errors.Is(err, ErrEnterpriseRequired) {
		t.Errorf("expected ErrEnterpriseRequired, got %v", err)
	}
}

func TestCommunityProvider_CompleteAuth(t *testing.T) {
	p := NewProvider()
	req := httptest.NewRequest("POST", "/auth/sso/callback", nil)
	user, err := p.CompleteAuth(req)
	if !errors.Is(err, ErrEnterpriseRequired) {
		t.Errorf("expected ErrEnterpriseRequired, got %v", err)
	}
	if user != nil {
		t.Error("expected nil user from community stub")
	}
}

func TestCommunityProvider_ImplementsInterface(t *testing.T) {
	// Compile-time assertion.
	var _ SSOProvider = &communityProvider{}
}
