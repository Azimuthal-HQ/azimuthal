package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api"
)

// Attachment serve-time content-type tests.
//
// The defect these pin: the generic upload route stores the content type the
// CLIENT declared (internal/core/api/attachments/handler.go, Upload) and the
// download route echoed it back as the Content-Type of an `inline`,
// same-origin response. A space writer could upload HTML declaring
// `text/html` and have it served as a document — and by ADR-0008 a share
// crosses the space boundary, so the audience for that document is every
// share recipient, who is by definition outside the space. Stored XSS across
// a trust boundary.
//
// The fix is at SERVE time, not upload time, which is what makes it cover the
// rows already in the table. These tests therefore assert on response headers
// and never on the stored column, which deliberately still holds the declared
// value as display metadata.

// Byte fixtures for the serve-time sniff. pngBytes already exists in this
// package (admin_profile_avatar_integration_test.go) and is reused rather
// than shadowed.
var (
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00")
	jpegBytes = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	webpBytes = append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 8)...)
	pdfBytes  = []byte("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n%%EOF\n")

	// htmlBytes is the payload: same-origin script in a document the browser
	// would execute if it were served as text/html.
	htmlBytes = []byte("<!DOCTYPE html><html><body><script>alert(document.domain)</script></body></html>")

	// svgBytes is scriptable XML. It is excluded from the inline allow-list
	// even though it is nominally an image type.
	svgBytes = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
)

// uploadAttachmentDeclaring posts a multipart attachment declaring an explicit
// Content-Type on the file part. multipart.Writer.CreateFormFile always
// declares application/octet-stream, so it cannot express the case that
// matters here: an uploader who LIES about the type.
func (f *shareFixture) uploadAttachmentDeclaring(
	t *testing.T, entityType, entityID, filename, declaredType string, content []byte,
) string {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("entity_type", entityType))
	require.NoError(t, w.WriteField("entity_id", entityID))

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", declaredType)
	part, err := w.CreatePart(h)
	require.NoError(t, err)
	_, err = part.Write(content)
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
		ID          string `json:"id"`
		ContentType string `json:"content_type"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &att))
	require.NotEmpty(t, att.ID)
	// The declared type is stored verbatim — this phase changes serving, not
	// storage. If this ever fails, the upload path started normalising and
	// these tests are no longer exercising the legacy-row case.
	require.Equal(t, declaredType, att.ContentType,
		"the stored content_type remains the client-declared value (display metadata)")
	return att.ID
}

// spaceAttachmentPath is the space-scoped download route.
func (f *shareFixture) spaceAttachmentPath(attID string) string {
	return fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments/%s", f.ts.OrgID, f.spaceID, attID)
}

// requireServedAsDownload asserts the hostile-content contract: a generic
// type, an `attachment` disposition that keeps the filename, and nosniff.
func requireServedAsDownload(t *testing.T, r httpResult, filename string) {
	t.Helper()
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)
	require.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"),
		"a type the browser will not render must be served generically")
	require.Equal(t, fmt.Sprintf("attachment; filename=%q", filename),
		r.Header.Get("Content-Disposition"),
		"non-allow-listed bytes download, and the filename survives")
	require.Equal(t, "nosniff", r.Header.Get("X-Content-Type-Options"))
}

// requireServedInline asserts the benign-content contract: the SNIFFED type,
// an `inline` disposition, the filename, and nosniff.
func requireServedInline(t *testing.T, r httpResult, wantType, filename string) {
	t.Helper()
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)
	require.Equal(t, wantType, r.Header.Get("Content-Type"))
	require.Equal(t, fmt.Sprintf("inline; filename=%q", filename),
		r.Header.Get("Content-Disposition"))
	require.Equal(t, "nosniff", r.Header.Get("X-Content-Type-Options"))
}

// TestAttachmentServe_DeclaredHTMLIsNotServedAsHTML is the regression test for
// the defect. Before the serve-time sniff this returned
// `Content-Type: text/html` with `Content-Disposition: inline`, which renders
// and executes same-origin. It must now download as an opaque blob.
func TestAttachmentServe_DeclaredHTMLIsNotServedAsHTML(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)
	attID := f.uploadAttachmentDeclaring(t, "page", pageID, "payload.html", "text/html", htmlBytes)

	r := f.ts.get(t, f.spaceAttachmentPath(attID), true)
	requireServedAsDownload(t, r, "payload.html")

	// The declared type must not appear anywhere in the response headers —
	// not as the type, not smuggled into the disposition.
	require.NotContains(t, r.Header.Get("Content-Type"), "html")
	require.NotContains(t, r.Header.Get("Content-Disposition"), "inline")

	// The bytes themselves are unchanged: this is a serving fix, not a
	// content filter. A legitimate .html file still downloads intact.
	require.Equal(t, htmlBytes, r.Body, "the exact stored bytes still stream back")
}

// TestAttachmentServe_SniffBeatsDeclaredType: the stored type is ignored in
// BOTH directions. Bytes decide.
func TestAttachmentServe_SniffBeatsDeclaredType(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)

	// PNG bytes, declared text/html. A serve path that trusted the column
	// would send text/html; the sniff sends image/png and renders it inline.
	honestPNG := f.uploadAttachmentDeclaring(t, "page", pageID, "chart.png", "text/html", pngBytes)
	requireServedInline(t, f.ts.get(t, f.spaceAttachmentPath(honestPNG), true), "image/png", "chart.png")

	// HTML bytes, declared image/png. A serve path that trusted the column
	// would send image/png — harmless — but nothing stops the SAME row being
	// re-declared later. The sniff downloads it on the bytes alone.
	hostile := f.uploadAttachmentDeclaring(t, "page", pageID, "innocent.png", "image/png", htmlBytes)
	requireServedAsDownload(t, f.ts.get(t, f.spaceAttachmentPath(hostile), true), "innocent.png")
}

// TestAttachmentServe_SVGDownloadsEvenWhenHonestlyDeclared: SVG is scriptable
// XML. It downloads even when the uploader declared it accurately — the
// allow-list is the four raster types plus PDF, and SVG is in none of them.
func TestAttachmentServe_SVGDownloadsEvenWhenHonestlyDeclared(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)
	attID := f.uploadAttachmentDeclaring(t, "page", pageID, "logo.svg", "image/svg+xml", svgBytes)

	r := f.ts.get(t, f.spaceAttachmentPath(attID), true)
	requireServedAsDownload(t, r, "logo.svg")
	require.NotContains(t, r.Header.Get("Content-Type"), "svg",
		"image/svg+xml must never be echoed back as the served type")
}

// TestAttachmentServe_AllowListRendersInline: every allow-list member still
// serves inline with its sniffed type and its filename intact. Page images
// ride this same handler (there is no separate image serve route), so a
// regression here breaks every document image.
func TestAttachmentServe_AllowListRendersInline(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)

	cases := []struct {
		name     string
		filename string
		wantType string
		content  []byte
	}{
		{"png", "diagram.png", "image/png", pngBytes},
		{"jpeg", "photo.jpg", "image/jpeg", jpegBytes},
		{"gif", "anim.gif", "image/gif", gifBytes},
		{"webp", "shot.webp", "image/webp", webpBytes},
		{"pdf", "report.pdf", "application/pdf", pdfBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Declared application/octet-stream throughout: the sniff must
			// find the real type without any help from the client.
			attID := f.uploadAttachmentDeclaring(t, "page", pageID, tc.filename,
				"application/octet-stream", tc.content)
			r := f.ts.get(t, f.spaceAttachmentPath(attID), true)
			requireServedInline(t, r, tc.wantType, tc.filename)
			require.Equal(t, tc.content, r.Body, "bytes stream back unaltered")
		})
	}
}

// TestAttachmentServe_LegacyRowWithHostileStoredType is why the fix lives at
// serve time. This row is written the way a row already in the table looks:
// the stored content_type is mutated directly in the database, bypassing the
// upload path entirely. No migration rewrites those rows, so the serve path
// has to be the thing that covers them.
func TestAttachmentServe_LegacyRowWithHostileStoredType(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)
	attID := f.uploadAttachmentDeclaring(t, "page", pageID, "legacy.bin",
		"application/octet-stream", htmlBytes)

	// Simulate legacy data: a row whose stored type was never sniffed.
	attUUID, err := uuid.Parse(attID)
	require.NoError(t, err)
	tag, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE attachments SET content_type = $1 WHERE id = $2`, "text/html", attUUID)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "the legacy row must actually have been mutated")

	// The list endpoint still shows the stored value — it is metadata, and
	// this phase does not touch it.
	lr := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments?entity_type=page&entity_id=%s",
		f.ts.OrgID, f.spaceID, pageID), true)
	require.Equal(t, http.StatusOK, lr.StatusCode)
	var list []struct {
		ContentType string `json:"content_type"`
	}
	require.NoError(t, json.Unmarshal(lr.Body, &list))
	require.Len(t, list, 1)
	require.Equal(t, "text/html", list[0].ContentType,
		"the stored column is untouched — the fix is at serve time, not a migration")

	// But serving it does not trust that column.
	requireServedAsDownload(t, f.ts.get(t, f.spaceAttachmentPath(attID), true), "legacy.bin")
}

// TestAttachmentServe_ShareRecipientPathIsSafe walks the actual trust
// boundary: a share recipient with NO space access reaches the attachment
// through the shared route (ADR-0008 — shares widen). The benign image still
// renders for them; the hostile document downloads.
func TestAttachmentServe_ShareRecipientPathIsSafe(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Illustrated", "content", nil)
	imageID := f.uploadAttachmentDeclaring(t, "page", pageID, "figure.png",
		"application/octet-stream", pngBytes)
	hostileID := f.uploadAttachmentDeclaring(t, "page", pageID, "payload.html",
		"text/html", htmlBytes)

	sharedPath := func(attID string) string {
		return fmt.Sprintf("/api/v1/orgs/%s/shared/page/%s/attachments/%s", f.ts.OrgID, pageID, attID)
	}

	// Premise: without the share the outsider reads neither. This is the
	// existing authorisation assertion and it is not weakened here — if the
	// share guard broke, the rest of this test would be vacuous.
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok, sharedPath(imageID)))
	requireAPINotFound(t, f.ts.getAs(t, f.outsiderTok, sharedPath(hostileID)))

	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": pageID, "audience": "org",
	})

	// The share recipient — outside the space — still gets the image inline,
	// so a shared page's figures keep rendering.
	requireServedInline(t, f.ts.getAs(t, f.outsiderTok, sharedPath(imageID)), "image/png", "figure.png")

	// And the hostile upload cannot be turned into a document in their
	// browser. This is the cross-boundary case the defect was about.
	requireServedAsDownload(t, f.ts.getAs(t, f.outsiderTok, sharedPath(hostileID)), "payload.html")
}

// TestSecurityHeaders_AreGlobal: the security headers come from global
// middleware, so a route that never sets them still sends them. Before that
// middleware the ONLY routes carrying nosniff were the attachment and avatar
// serve paths; the wiki render endpoint, which returns user-authored content
// as text/html, carried nothing.
//
// nosniff does not close the attachment hole on its own — it constrains
// sniffing, not rendering — which is why the serve path sniffs as well.
//
// The other four headers are the v0.4.1 trust patch. The case that matters
// most is "user-authored html": /wiki/{id}/render answers text/html built from
// a page body, as a top-level document on the app's own origin. It is the one
// route here where the CSP is doing load-bearing work rather than defence in
// depth.
//
// This is the right place for the assertion because these cases go through the
// real router. A CSP check added to docs_test.go's newDocsTestServer would
// pass vacuously — that harness builds a bare chi.NewRouter() with no global
// middleware at all.
func TestSecurityHeaders_AreGlobal(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "# heading", nil)

	cases := []struct {
		name string
		r    httpResult
	}{
		{"public health", f.ts.get(t, "/health", false)},
		{"api documentation ui", f.ts.get(t, "/api/docs", false)},
		{"json api", f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s",
			f.ts.OrgID, f.spaceID, pageID), true)},
		{"user-authored html", f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki/%s/render",
			f.ts.OrgID, f.spaceID, pageID), true)},
		{"error response", f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments/%s",
			f.ts.OrgID, f.spaceID, uuid.New()), true)},
	}

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": api.ContentSecurityPolicy,
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"X-Frame-Options":         "DENY",
		// No includeSubDomains and no preload: this binary knows only the host
		// it was asked for, and both commit hostnames it has never seen.
		"Strict-Transport-Security": "max-age=31536000",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for header, value := range want {
				require.Equal(t, value, tc.r.Header.Get(header), header)
				require.Len(t, tc.r.Header.Values(header), 1,
					header+" is set once, not appended per layer")
			}
		})
	}
}

// TestContentSecurityPolicy_NeutersInlineScript asserts the two properties the
// policy exists for, against the constant itself rather than against a served
// response.
//
// Both are worth pinning separately from the reach test above, because the
// reach test compares the served header to the same constant and so would
// happily agree with a policy that had been loosened. This one would not.
func TestContentSecurityPolicy_NeutersInlineScript(t *testing.T) {
	directives := map[string]string{}
	for _, d := range strings.Split(api.ContentSecurityPolicy, ";") {
		parts := strings.Fields(strings.TrimSpace(d))
		if len(parts) > 0 {
			directives[parts[0]] = strings.Join(parts[1:], " ")
		}
	}

	require.Equal(t, "'self'", directives["script-src"],
		"script-src must stay bare 'self'. No 'unsafe-inline', no 'unsafe-eval', "+
			"no nonce and no hash: the built SPA needs none of them, and the one page "+
			"that did (the Swagger UI) had its initialiser moved to /api/docs/init.js "+
			"rather than buy an exception for every response in the product.")

	// The bypasses a bare script-src leaves open on its own.
	require.Equal(t, "'none'", directives["object-src"], "object-src")
	require.Equal(t, "'self'", directives["base-uri"], "base-uri")
	require.Equal(t, "'none'", directives["frame-ancestors"], "frame-ancestors")

	// style-src is the documented exception and is allowed to carry
	// 'unsafe-inline'; nothing else may.
	for name, value := range directives {
		if name == "style-src" {
			continue
		}
		require.NotContains(t, value, "unsafe-inline", name+" must not allow inline content")
		require.NotContains(t, value, "unsafe-eval", name+" must not allow eval")
	}
}

// TestSwaggerUI_HasNoInlineScript is the other half of the same rule, read from
// the page instead of the policy: /api/docs must not reintroduce an inline
// <script>, because under this CSP it would silently not run and the API
// reference would render as a blank page with a console-only error.
func TestSwaggerUI_HasNoInlineScript(t *testing.T) {
	f := newShareFixture(t)
	body := string(f.ts.get(t, "/api/docs", false).Body)

	require.Contains(t, body, `<script src="/api/docs/init.js"></script>`,
		"the initialiser must be loaded as a same-origin file")
	// `\s*[^<\s]` and not `\S`: `\S` matches `<`, so it happily fires on the
	// empty body of `<script src=…></script>` and the check would fail on the
	// page it is meant to pass.
	require.NotRegexp(t, `<script[^>]*>\s*[^<\s]`, body,
		"a <script> tag with a body is inline script, which script-src 'self' blocks")

	init := f.ts.get(t, "/api/docs/init.js", false)
	require.Equal(t, http.StatusOK, init.StatusCode)
	require.Contains(t, init.Header.Get("Content-Type"), "javascript")
	require.Contains(t, string(init.Body), "SwaggerUIBundle(")
}

// TestAttachmentServe_EmptyObjectDownloads: a zero-byte object sniffs as
// text/plain and must not fall through to some default that renders. The
// branch exists because DetectContentType on an empty slice still returns a
// concrete type rather than an error.
func TestAttachmentServe_EmptyObjectDownloads(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)
	attID := f.uploadAttachmentDeclaring(t, "page", pageID, "empty.html", "text/html", []byte{})

	r := f.ts.get(t, f.spaceAttachmentPath(attID), true)
	requireServedAsDownload(t, r, "empty.html")
	require.Empty(t, r.Body)
}

// TestAttachmentServe_SniffReadsBeyondFirstBytes: an object larger than the
// 512-byte sniff window still streams whole. The peek must put its bytes back
// in front of the stream, not consume them.
func TestAttachmentServe_SniffReadsBeyondFirstBytes(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Doc", "doc", nil)

	// A PNG whose body runs well past the sniff window.
	large := append(pngBytes, bytes.Repeat([]byte{0xAB}, 4096)...)
	attID := f.uploadAttachmentDeclaring(t, "page", pageID, "big.png", "application/octet-stream", large)

	r := f.ts.get(t, f.spaceAttachmentPath(attID), true)
	requireServedInline(t, r, "image/png", "big.png")
	require.Equal(t, large, r.Body, "every byte streams back, sniffed prefix included")
	require.Len(t, r.Body, len(large))
}
