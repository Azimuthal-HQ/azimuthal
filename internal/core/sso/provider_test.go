package sso_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/sso"
)

func TestDefaultProvider_IsAvailable(t *testing.T) {
	p := sso.NewProvider()
	if p.IsAvailable() {
		t.Error("default provider IsAvailable() must return false")
	}
}

func TestDefaultProvider_BeginAuth(t *testing.T) {
	p := sso.NewProvider()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth/sso", nil)
	err := p.BeginAuth(rr, req)
	if !errors.Is(err, sso.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
}

func TestDefaultProvider_CompleteAuth(t *testing.T) {
	p := sso.NewProvider()
	req := httptest.NewRequest("POST", "/auth/sso/callback", nil)
	user, err := p.CompleteAuth(req)
	if !errors.Is(err, sso.ErrNotConfigured) {
		t.Errorf("expected ErrNotConfigured, got %v", err)
	}
	if user != nil {
		t.Error("expected nil user from default provider")
	}
}

func TestDefaultProvider_ImplementsInterface(_ *testing.T) {
	var _ = sso.NewProvider()
}
