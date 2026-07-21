package attachments

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/storage"
)

// memStore is an in-memory attachments.Store for unit tests.
type memStore struct {
	rows map[uuid.UUID]Attachment
}

func newMemStore() *memStore { return &memStore{rows: map[uuid.UUID]Attachment{}} }

func (m *memStore) CreateAttachment(_ context.Context, a Attachment) (Attachment, error) {
	m.rows[a.ID] = a
	return a, nil
}
func (m *memStore) GetAttachment(_ context.Context, id uuid.UUID) (Attachment, error) {
	a, ok := m.rows[id]
	if !ok {
		return Attachment{}, ErrNotFound
	}
	return a, nil
}
func (m *memStore) ListAttachmentsByEntity(_ context.Context, entityType string, entityID uuid.UUID) ([]Attachment, error) {
	var out []Attachment
	for _, a := range m.rows {
		if a.EntityType == entityType && a.EntityID == entityID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (m *memStore) SoftDeleteAttachment(_ context.Context, id uuid.UUID) error {
	delete(m.rows, id)
	return nil
}

func newService() (*Service, *storage.MemoryStore, *memStore) {
	blobs := storage.NewMemoryStore()
	store := newMemStore()
	return NewService(store, blobs), blobs, store
}

func TestUpload_StoresObjectAndRow(t *testing.T) {
	svc, blobs, store := newService()
	orgID, entityID, userID := uuid.New(), uuid.New(), uuid.New()

	att, err := svc.Upload(context.Background(), UploadInput{
		OrgID: orgID, EntityType: access.ShareEntityPage, EntityID: entityID,
		Filename: "a.txt", ContentType: "text/plain", CreatedBy: userID,
		Content: strings.NewReader("hello"), Size: 5,
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if att.SizeBytes != 5 {
		t.Errorf("size = %d, want 5", att.SizeBytes)
	}
	// The object key is derived from the row, org-prefixed and id-suffixed —
	// never client-supplied.
	if want := "orgs/" + orgID.String() + "/attachments/" + att.ID.String(); att.ObjectKey != want {
		t.Errorf("object key = %q, want %q", att.ObjectKey, want)
	}
	if blobs.Len() != 1 {
		t.Errorf("expected one stored object, got %d", blobs.Len())
	}
	if _, ok := store.rows[att.ID]; !ok {
		t.Error("row not recorded")
	}

	// Open streams the exact bytes back.
	rc, err := svc.Open(context.Background(), att)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestUpload_RejectsOversize(t *testing.T) {
	svc, blobs, _ := newService()
	// A stream larger than the ceiling is rejected and its partial object
	// removed — even when Size under-declares the true length.
	big := bytes.Repeat([]byte("x"), MaxSizeBytes+10)
	_, err := svc.Upload(context.Background(), UploadInput{
		OrgID: uuid.New(), EntityType: access.ShareEntityPage, EntityID: uuid.New(),
		Filename: "big.bin", ContentType: "application/octet-stream", CreatedBy: uuid.New(),
		Content: bytes.NewReader(big), Size: 1, // lies about size
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if blobs.Len() != 0 {
		t.Errorf("oversize object must not remain, got %d", blobs.Len())
	}
}

func TestGetForEntity_BindsToEntity(t *testing.T) {
	svc, _, _ := newService()
	orgID := uuid.New()
	pageA, pageB := uuid.New(), uuid.New()

	att, err := svc.Upload(context.Background(), UploadInput{
		OrgID: orgID, EntityType: access.ShareEntityPage, EntityID: pageA,
		Filename: "a.txt", ContentType: "text/plain", CreatedBy: uuid.New(),
		Content: strings.NewReader("a"), Size: 1,
	})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}

	// Correct entity → found.
	if _, err := svc.GetForEntity(context.Background(), access.ShareEntityPage, pageA, att.ID); err != nil {
		t.Errorf("GetForEntity(correct entity) = %v, want nil", err)
	}
	// A valid attachment id under the WRONG entity → mismatch, not a read.
	if _, err := svc.GetForEntity(context.Background(), access.ShareEntityPage, pageB, att.ID); !errors.Is(err, ErrEntityMismatch) {
		t.Errorf("GetForEntity(wrong entity) = %v, want ErrEntityMismatch", err)
	}
	// A missing attachment id → not found.
	if _, err := svc.GetForEntity(context.Background(), access.ShareEntityPage, pageA, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetForEntity(missing) = %v, want ErrNotFound", err)
	}
}

func TestUpload_RejectsUnknownEntityType(t *testing.T) {
	svc, _, _ := newService()
	_, err := svc.Upload(context.Background(), UploadInput{
		OrgID: uuid.New(), EntityType: "sasquatch", EntityID: uuid.New(),
		Filename: "x", ContentType: "text/plain", CreatedBy: uuid.New(),
		Content: strings.NewReader("x"), Size: 1,
	})
	if err == nil {
		t.Error("expected an error for an unknown entity type")
	}
}
