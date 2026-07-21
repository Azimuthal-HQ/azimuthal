package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Attachment leak tests (ADR-0008 rule 3, leak failure mode 4). The object
// store must honour shares — a shared page's image renders for a viewer with
// no space access — WITHOUT becoming a way to read arbitrary object keys.

// uploadAttachment posts a multipart attachment as the owner and returns the
// created attachment id.
func (f *shareFixture) uploadAttachment(t *testing.T, entityType, entityID, filename, content string) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("entity_type", entityType))
	require.NoError(t, w.WriteField("entity_id", entityID))
	part, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		f.ts.url(fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID)), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+f.ts.Token)
	r := f.ts.do(t, req)
	require.Equal(t, http.StatusCreated, r.StatusCode, "upload attachment: %s", r.Body)
	var att struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &att))
	require.NotEmpty(t, att.ID)
	return att.ID
}

// TestAttachment_SharedPageLoadsForViewerWithoutSpaceAccess: the core
// requirement — a viewer holding a share reads the attachment bytes with no
// space access, and the same request is denied before the share exists.
func TestAttachment_SharedPageLoadsForViewerWithoutSpaceAccess(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Illustrated", "![img](x)", nil)
	attID := f.uploadAttachment(t, "page", pageID, "diagram.txt", "PNGDATA-abc")

	sharedAttPath := fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, pageID, attID)

	// Before the share, the outsider cannot read the attachment.
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok, sharedAttPath))

	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})

	// After the share, the bytes load for the outsider — no space access.
	r := f.ts.getAs(t, f.outsiderTok, sharedAttPath)
	require.Equal(t, http.StatusOK, r.StatusCode, "shared attachment must load: %s", r.Body)
	require.Equal(t, "PNGDATA-abc", string(r.Body), "the exact stored bytes stream back")

	// The list endpoint is likewise share-authorised.
	lr := f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments", f.ts.OrgID, pageID))
	require.Equal(t, http.StatusOK, lr.StatusCode, "shared attachment list: %s", lr.Body)
	var list []struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
	}
	require.NoError(t, json.Unmarshal(lr.Body, &list))
	require.Len(t, list, 1)
	require.Equal(t, "diagram.txt", list[0].Filename)
}

// TestAttachment_CannotReadArbitraryKeys: the shared attachment path takes
// the object key from the row and binds the attachment to the covered
// entity. A random attachment id, and a VALID attachment id belonging to a
// DIFFERENT (unshared) entity, both 404 — no object is readable by guessing.
func TestAttachment_CannotReadArbitraryKeys(t *testing.T) {
	f := newShareFixture(t)

	// Shared page with its own attachment.
	sharedPage, _ := f.createPage(t, "Shared", "shared", nil)
	sharedAtt := f.uploadAttachment(t, "page", sharedPage, "shared.txt", "SHARED-bytes")
	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": sharedPage, "audience": "org",
	})

	// A DIFFERENT page, NOT shared, with its own secret attachment.
	secretPage, _ := f.createPage(t, "Secret", "secret", nil)
	secretAtt := f.uploadAttachment(t, "page", secretPage, "secret.txt", "SECRET-bytes")

	// Baseline: the outsider reads the shared attachment via the shared page.
	require.Equal(t, http.StatusOK, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, sharedPage, sharedAtt)).StatusCode)

	// A random attachment id under the shared page → 404.
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, sharedPage, uuid.New())))

	// The secret attachment id pointed at the SHARED page → 404 (the
	// attachment does not belong to the shared entity — GetForEntity guard).
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, sharedPage, secretAtt)))

	// The secret attachment under its OWN (unshared) page → 404 (no share
	// covers the secret page, so the whole subtree is unreachable).
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, secretPage, secretAtt)))

	// And the secret bytes never leaked in any of the above.
	require.NotContains(t, string(f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, sharedPage, secretAtt)).Body),
		"SECRET-bytes")
}

// TestAttachment_SpaceScopedRequiresSpaceAccess: the in-space attachment
// routes stay behind the space guards — the outsider (no space access) is
// denied, the owner (space access) is served.
func TestAttachment_SpaceScopedRequiresSpaceAccess(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)
	attID := f.uploadAttachment(t, "page", pageID, "file.txt", "OWNER-bytes")

	spacePath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments/%s", f.ts.OrgID, f.spaceID, attID)

	// Owner (space admin) reads via the space route.
	r := f.ts.get(t, spacePath, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "owner reads via space route: %s", r.Body)
	require.Equal(t, "OWNER-bytes", string(r.Body))

	// Outsider (no space access) is denied the space route — even though a
	// share might exist, the space route is space-authorised only.
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok, spacePath))
}

// TestAttachment_UploadEntityMustBeInSpace: an upload naming an entity that
// lives in a different space 404s — the object is never stored against the
// wrong container.
func TestAttachment_UploadEntityMustBeInSpace(t *testing.T) {
	f := newShareFixture(t)
	otherSpace := createScopedSpace(t, f.ts, "Other", "other-space", "codex")

	// A page in the OTHER space.
	r := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", f.ts.OrgID, otherSpace),
		map[string]interface{}{"title": "Elsewhere", "content": "x"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode)
	var page struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &page))

	// Upload it under f.spaceID (the wrong space) → 404.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("entity_type", "page"))
	require.NoError(t, w.WriteField("entity_id", page.ID))
	part, err := w.CreateFormFile("file", "x.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(part, "data")
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		f.ts.url(fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID)), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+f.ts.Token)
	requireAPINotFound(t, f.ts.do(t, req))
}
