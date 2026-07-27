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

// S7 — the avatar serve path decides the content type from the shared image
// allow-list, not from whatever the bytes happen to sniff as.
//
// The serve handler sets the returned type with Content-Disposition: inline
// and X-Content-Type-Options: nosniff. Returning the raw sniffed type meant
// that an object sniffing as text/html was served as an HTML document, on the
// application's own origin, to a browser that had been explicitly told not to
// second-guess the type — every org member being a valid reader of every other
// member's avatar. Stored XSS, one GET away.
//
// The upload gate has always refused such an object, and that is exactly why
// the test has to plant one directly: a serve path that is safe only because
// a different code path was careful is not safe, it is lucky. Objects written
// before the gate existed, or by any future writer to the same bucket prefix,
// take this path too.
//
// Fails before the fix with 200 and Content-Type: text/html.
func TestAvatarServe_RefusesAnObjectThatIsNotAnAllowedImage(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	// An HTML document parked at the avatar key the serve route derives.
	key := fmt.Sprintf("orgs/%s/avatars/%s", ts.OrgID, ts.UserID)
	require.NoError(t, ts.AvatarBlobs.Put(ctx, key,
		bytes.NewReader([]byte("<html><body><script>alert(document.domain)</script></body></html>"))))

	r := ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/avatar", ts.OrgID, ts.UserID), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"an avatar object that is not an allowed image must not be served: %s", r.Body)
	require.NotContains(t, r.Header.Get("Content-Type"), "text/html",
		"the response must never carry the sniffed type of a non-image object")
	require.NotContains(t, string(r.Body), "<script>",
		"the planted document must not come back")
}

// SVG is the case the allow-list exists for. It is a scriptable XML document,
// not a bitmap, and http.DetectContentType reports it as text/xml — so it is
// excluded by not being on the list rather than by a rule about SVG, and would
// stay excluded if the sniffer's answer ever changed.
func TestAvatarServe_RefusesSVG(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert(document.domain)</script></svg>`)

	// Refused at upload...
	r := putAvatar(t, ts, ts.Token, "/api/v1/auth/me/avatar", svg)
	require.Equal(t, http.StatusUnsupportedMediaType, r.StatusCode, "upload: %s", r.Body)

	// ...and refused on the way out, for an object that got there anyway.
	key := fmt.Sprintf("orgs/%s/avatars/%s", ts.OrgID, ts.UserID)
	require.NoError(t, ts.AvatarBlobs.Put(ctx, key, bytes.NewReader(svg)))
	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/avatar", ts.OrgID, ts.UserID), true)
	require.Equal(t, http.StatusNotFound, r.StatusCode, "serve: %s", r.Body)
}

// A real image still round-trips, with its sniffed type — the guard must not
// have broken the feature it protects.
func TestAvatarServe_ServesAnAllowedImageInline(t *testing.T) {
	ts := newTestServer(t)

	r := putAvatar(t, ts, ts.Token, "/api/v1/auth/me/avatar", pngBytes)
	require.Equal(t, http.StatusOK, r.StatusCode, "upload: %s", r.Body)

	r = ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/users/%s/avatar", ts.OrgID, ts.UserID), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "serve: %s", r.Body)
	require.Equal(t, "image/png", r.Header.Get("Content-Type"))
	require.Equal(t, "nosniff", r.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "inline", r.Header.Get("Content-Disposition"))
	require.Equal(t, pngBytes, r.Body)
}
