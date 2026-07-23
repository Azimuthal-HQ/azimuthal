package api_test

// S8 regression: an org admin can edit another member's display name (audited,
// non-admins cannot), and avatar upload works for self and by admin, reusing
// the shared object store with server-side content-type + size validation and
// a server-derived object key.

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/people"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// pngBytes is a minimal PNG (signature + IHDR) — enough for
// http.DetectContentType to return image/png.
var pngBytes = []byte{
	0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A,
	0, 0, 0, 13, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0,
}

func putAvatar(t *testing.T, ts *testServer, token, path string, content []byte) httpResult {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "avatar.png")
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, ts.url(path), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

func TestAdmin_UpdatePerson_ChangesDisplayName(t *testing.T) {
	ts := newTestServer(t) // ts.Token is an org owner (admin)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	r := ts.patch(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, member.ID),
		map[string]any{"display_name": "Renamed Member"}, true)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "admin update: %s", r.Body)

	var name string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT display_name FROM users WHERE id=$1`, member.ID).Scan(&name))
	require.Equal(t, "Renamed Member", name)

	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE org_id=$1 AND entity_id=$2 AND action='user.profile_changed'`,
		ts.OrgID, member.ID).Scan(&n))
	require.Equal(t, 1, n, "admin profile edit must write an audit event")
}

func TestAdmin_UpdatePerson_NonAdmin404(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	other := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberTok := ts.tokenFor(t, member.ID, member.Email)

	r := ts.patchAs(t, memberTok, fmt.Sprintf("/api/v1/orgs/%s/users/%s", ts.OrgID, other.ID),
		map[string]any{"display_name": "Hacked"})
	require.Equal(t, http.StatusNotFound, r.StatusCode, "non-admin must get 404: %s", r.Body)

	var name string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT display_name FROM users WHERE id=$1`, other.ID).Scan(&name))
	require.NotEqual(t, "Hacked", name, "a denied edit must not land")
}

func TestAvatar_SelfUploadAndServe(t *testing.T) {
	ts := newTestServer(t)

	r := putAvatar(t, ts, ts.Token, "/api/v1/auth/me/avatar", pngBytes)
	require.Equal(t, http.StatusOK, r.StatusCode, "self avatar upload: %s", r.Body)

	var url *string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT avatar_url FROM users WHERE id=$1`, ts.UserID).Scan(&url))
	require.NotNil(t, url, "avatar_url must be set")
	require.Contains(t, *url, "/avatar")

	sr := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/avatar", ts.OrgID, ts.UserID), true)
	require.Equal(t, http.StatusOK, sr.StatusCode, "serve avatar: %s", sr.Body)
	require.Equal(t, "image/png", sr.ContentType)
	require.Equal(t, "nosniff", sr.Header.Get("X-Content-Type-Options"))
	require.Equal(t, pngBytes, sr.Body, "the exact stored bytes stream back")
}

func TestAvatar_RejectsNonImageContent(t *testing.T) {
	ts := newTestServer(t)

	// Text bytes carried in a .png-named part must still be rejected — the
	// server sniffs the content, it does not trust the filename.
	r := putAvatar(t, ts, ts.Token, "/api/v1/auth/me/avatar",
		[]byte("this is plain text, definitely not an image"))
	require.Equal(t, http.StatusUnsupportedMediaType, r.StatusCode, "non-image must be rejected: %s", r.Body)

	var url *string
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT avatar_url FROM users WHERE id=$1`, ts.UserID).Scan(&url))
	require.Nil(t, url, "avatar_url must stay unset after a rejected upload")
}

func TestAvatar_RejectsOversize(t *testing.T) {
	ts := newTestServer(t)

	big := make([]byte, people.MaxAvatarBytes+1)
	copy(big, pngBytes) // valid image header, but over the ceiling
	r := putAvatar(t, ts, ts.Token, "/api/v1/auth/me/avatar", big)
	require.Equal(t, http.StatusRequestEntityTooLarge, r.StatusCode, "oversize must be rejected: %s", r.Body)
}

func TestAvatar_AdminUploadAndAuthz(t *testing.T) {
	ts := newTestServer(t)
	member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	memberTok := ts.tokenFor(t, member.ID, member.Email)

	adminPath := fmt.Sprintf("/api/v1/orgs/%s/users/%s/avatar", ts.OrgID, member.ID)

	// An org admin can set a member's avatar.
	r := putAvatar(t, ts, ts.Token, adminPath, pngBytes)
	require.Equal(t, http.StatusOK, r.StatusCode, "admin avatar upload: %s", r.Body)

	// A non-admin cannot set another member's avatar — the surface 404s.
	r = putAvatar(t, ts, memberTok, adminPath, pngBytes)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "non-admin admin-upload must 404: %s", r.Body)

	// The avatar the admin set is readable by any org member.
	sr := ts.getAs(t, memberTok, adminPath)
	require.Equal(t, http.StatusOK, sr.StatusCode, "member reads the avatar: %s", sr.Body)
	require.Equal(t, "image/png", sr.ContentType)
}
