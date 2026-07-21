package invites

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubStore is an in-memory invites.Store for service-level behaviour that
// needs no database: validation ordering and delivery semantics. Every
// persistence assertion lives in the adapter/API integration suites against
// real PostgreSQL.
type stubStore struct {
	created   []Invite
	createErr error
}

func (s *stubStore) Create(_ context.Context, inv Invite, _ string) (Invite, error) {
	if s.createErr != nil {
		return Invite{}, s.createErr
	}
	inv.ID = uuid.New()
	s.created = append(s.created, inv)
	return inv, nil
}
func (s *stubStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (Invite, error) {
	return Invite{}, ErrNotFound
}
func (s *stubStore) ListActive(context.Context, uuid.UUID) ([]Invite, error) { return nil, nil }
func (s *stubStore) Revoke(context.Context, uuid.UUID, uuid.UUID) error      { return ErrNotFound }
func (s *stubStore) RefreshToken(_ context.Context, _, _ uuid.UUID, _ string, _ time.Time) (Invite, error) {
	return Invite{}, ErrNotFound
}
func (s *stubStore) InspectByTokenHash(context.Context, string) (Inspection, error) {
	return Inspection{}, ErrNotFound
}
func (s *stubStore) Accept(context.Context, string, *NewUser) (AcceptOutcome, error) {
	return AcceptOutcome{}, ErrNotFound
}

type recordingSender struct {
	sent []string
	err  error
}

func (r *recordingSender) SendInvite(_ context.Context, toEmail string, _ uuid.UUID, _ string, _ time.Time) error {
	r.sent = append(r.sent, toEmail)
	return r.err
}

func TestService_Create_ValidatesBeforeTouchingTheStore(t *testing.T) {
	store := &stubStore{}
	svc := NewService(store, nil, Config{BaseURL: "http://x"})

	if _, err := svc.Create(context.Background(), uuid.New(), "not-an-email", "member", nil, uuid.New()); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail, got %v", err)
	}
	if _, err := svc.Create(context.Background(), uuid.New(), "a@b.com", "owner", nil, uuid.New()); !errors.Is(err, ErrInvalidOrgRole) {
		t.Fatalf("expected ErrInvalidOrgRole for 'owner', got %v", err)
	}
	if len(store.created) != 0 {
		t.Fatal("validation failures must not reach the store")
	}
}

func TestService_Create_NormalisesEmailAndDefaultsRole(t *testing.T) {
	store := &stubStore{}
	svc := NewService(store, nil, Config{BaseURL: "http://x"})

	created, err := svc.Create(context.Background(), uuid.New(), "  MiXeD@Example.COM ", "", nil, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if created.Invite.Email != "mixed@example.com" {
		t.Errorf("email must be normalised, got %q", created.Invite.Email)
	}
	if created.Invite.OrgRole != "member" {
		t.Errorf("empty role must default to member, got %q", created.Invite.OrgRole)
	}
	if created.RawToken == "" || created.URL != "http://x/invite/"+created.RawToken {
		t.Errorf("the one-time URL must embed the raw token: %q / %q", created.RawToken, created.URL)
	}
}

func TestService_Delivery_LinkModeNeverSends_EmailModeReportsFailures(t *testing.T) {
	// Link mode: the sender is never called even when one is wired.
	sender := &recordingSender{}
	svc := NewService(&stubStore{}, sender, Config{BaseURL: "http://x", DeliverByEmail: false})
	created, err := svc.Create(context.Background(), uuid.New(), "link@example.com", "member", nil, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if created.Delivered || len(sender.sent) != 0 {
		t.Fatal("link mode must not send email")
	}

	// Email mode: delivered on success…
	sender = &recordingSender{}
	svc = NewService(&stubStore{}, sender, Config{BaseURL: "http://x", DeliverByEmail: true})
	created, err = svc.Create(context.Background(), uuid.New(), "mail@example.com", "member", nil, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if !created.Delivered || len(sender.sent) != 1 {
		t.Fatal("email mode must send and report delivered")
	}

	// …and a send failure surfaces as Delivered=false with the URL still
	// returned — the admin can always deliver the link by hand.
	sender = &recordingSender{err: errors.New("smtp down")}
	svc = NewService(&stubStore{}, sender, Config{BaseURL: "http://x", DeliverByEmail: true})
	created, err = svc.Create(context.Background(), uuid.New(), "down@example.com", "member", nil, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if created.Delivered {
		t.Fatal("a failed send must report Delivered=false")
	}
	if created.URL == "" {
		t.Fatal("the URL must be returned regardless of delivery")
	}
}

func TestService_TTLDefaultsToSevenDays(t *testing.T) {
	store := &stubStore{}
	svc := NewService(store, nil, Config{BaseURL: "http://x"})
	created, err := svc.Create(context.Background(), uuid.New(), "ttl@example.com", "member", nil, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	until := time.Until(created.Invite.ExpiresAt)
	if until < 6*24*time.Hour || until > 8*24*time.Hour {
		t.Errorf("default TTL must be ~7 days, got %v", until)
	}
}
