package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/attachments"
)

// Negative coverage for the wiki and attachment HTTP surfaces (spec §2.6).
//
// Every case here is a REFUSAL: a path parameter that is not a uuid, a body
// that will not decode, a resource that is not there, a caller past the write
// floor but short of the capability, and the validation refusals. Each asserts
// the exact status AND the documented error envelope, because "not a 500" is
// not an assertion — a handler that answered 200 with an empty body would
// satisfy it.
//
// Everything declared at package scope in this file is prefixed
// `wikiAttachNeg` — package api_test is one namespace shared by forty files.

// ── shared assertion + request helpers ─────────────────────────────────────

// wikiAttachNegRequireError asserts a refusal's exact status and error code,
// and that the body really is the API error envelope. Written rather than
// reusing requireErrorCode so the failure message names the case; the shape it
// checks is identical.
func wikiAttachNegRequireError(t *testing.T, r httpResult, status int, code, what string) {
	t.Helper()
	require.Equal(t, status, r.StatusCode, "%s: body: %s", what, r.Body)
	require.Contains(t, r.ContentType, "application/json", "%s: a refusal must be JSON", what)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "%s: error envelope expected, got: %s", what, r.Body)
	require.Equal(t, code, body.Error.Code, "%s: body: %s", what, r.Body)
	require.NotEmpty(t, body.Error.Message, "%s: a refusal must say something", what)
}

// wikiAttachNegPart is one part of a multipart body. filename == "" makes it a
// plain value field; declaredType == "" sends the file part with NO
// Content-Type header at all, which is the only way to reach the handler's
// blank-content-type default.
type wikiAttachNegPart struct {
	name         string
	filename     string
	declaredType string
	content      string
}

// wikiAttachNegPostMultipart builds a multipart body from parts and posts it.
// Same CreatePart idiom as uploadAttachmentDeclaring in
// attachment_serve_integration_test.go, extended in exactly two directions the
// refusal tests need: omitting the file part entirely, and omitting a part's
// declared Content-Type.
func wikiAttachNegPostMultipart(t *testing.T, ts *testServer, token, path string, parts []wikiAttachNegPart) httpResult {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		if p.filename == "" {
			require.NoError(t, w.WriteField(p.name, p.content))
			continue
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, p.name, p.filename))
		if p.declaredType != "" {
			h.Set("Content-Type", p.declaredType)
		}
		part, err := w.CreatePart(h)
		require.NoError(t, err)
		_, err = part.Write([]byte(p.content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.url(path), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

// wikiAttachNegPostRaw sends a body verbatim under an arbitrary Content-Type —
// the only way to hand a multipart route something that is not multipart.
func wikiAttachNegPostRaw(t *testing.T, ts *testServer, token, path, contentType, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.url(path),
		strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

// wikiAttachNegZeros is an infinite reader of zero bytes, so an over-size
// upload costs no memory to generate.
type wikiAttachNegZeros struct{}

func (wikiAttachNegZeros) Read(p []byte) (int, error) { return len(p), nil }

// wikiAttachNegPostOversized streams a multipart body whose file part is
// exactly size bytes. It streams through an io.Pipe rather than buffering,
// because the size that matters here is 25 MiB + 1 and a test should not hold
// that twice. The whole body IS consumed by the handler (ParseMultipartForm
// runs before the size check), so there is no early-close race.
func wikiAttachNegPostOversized(t *testing.T, ts *testServer, token, path string, size int64, fields map[string]string) httpResult {
	t.Helper()
	pr, pw := io.Pipe()
	w := multipart.NewWriter(pw)
	contentType := w.FormDataContentType()
	go func() {
		// Never t.Fatal from here — a failure is reported through the pipe and
		// surfaces as the request error the caller already checks.
		err := func() error {
			for key, value := range fields {
				if err := w.WriteField(key, value); err != nil {
					return err
				}
			}
			part, err := w.CreateFormFile("file", "oversized.png")
			if err != nil {
				return err
			}
			if _, err := io.CopyN(part, wikiAttachNegZeros{}, size); err != nil {
				return err
			}
			return w.Close()
		}()
		_ = pw.CloseWithError(err)
	}()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.url(path), pr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	return ts.do(t, req)
}

// wikiAttachNegAttachmentCount reports how many live attachments an entity
// carries, read straight from the table so a refusal can be proven to have
// written nothing.
func wikiAttachNegAttachmentCount(t *testing.T, ts *testServer, entityID string) int {
	t.Helper()
	var n int
	require.NoError(t, ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM attachments WHERE entity_id = $1 AND deleted_at IS NULL`,
		uuid.MustParse(entityID)).Scan(&n))
	return n
}

// ── attachments: upload refusals ───────────────────────────────────────────

// TestWikiAttachNeg_UploadRefusals walks every way an upload can be malformed.
//
// Defect each case catches, in order: a multipart parse error swallowed (the
// handler would run on an empty form and store an attachment against uuid.Nil);
// a missing entity_id read as the zero uuid (an attachment owned by nothing,
// which no read path can ever reach and no delete can ever clean up); an
// unvalidated entity_type (a row whose type no LookupEntity understands, so the
// object is unreachable AND unauthorised — nothing can decide who may read it);
// an entity in another space accepted (the object lands in a container the
// caller cannot read, which is a cross-space write); and a missing file part
// dereferenced (nil reader → panic, or a zero-byte attachment reported as
// created).
func TestWikiAttachNeg_UploadRefusals(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Target", "body", nil)
	uploadPath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID)

	t.Run("body_is_not_multipart", func(t *testing.T) {
		r := wikiAttachNegPostRaw(t, f.ts, f.ts.Token, uploadPath,
			"multipart/form-data; boundary=nonsense", `{"entity_type":"page"}`)
		wikiAttachNegRequireError(t, r, http.StatusBadRequest, "BAD_REQUEST", "non-multipart body")
	})

	t.Run("entity_id_missing", func(t *testing.T) {
		r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token, uploadPath, []wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "file", filename: "x.txt", declaredType: "text/plain", content: "data"},
		})
		wikiAttachNegRequireError(t, r, http.StatusBadRequest, "VALIDATION_ERROR", "no entity_id")
	})

	t.Run("entity_id_not_a_uuid", func(t *testing.T) {
		r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token, uploadPath, []wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "entity_id", content: "not-a-uuid"},
			{name: "file", filename: "x.txt", declaredType: "text/plain", content: "data"},
		})
		wikiAttachNegRequireError(t, r, http.StatusBadRequest, "VALIDATION_ERROR", "malformed entity_id")
	})

	t.Run("entity_type_unknown", func(t *testing.T) {
		r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token, uploadPath, []wikiAttachNegPart{
			{name: "entity_type", content: "comment"},
			{name: "entity_id", content: pageID},
			{name: "file", filename: "x.txt", declaredType: "text/plain", content: "data"},
		})
		wikiAttachNegRequireError(t, r, http.StatusBadRequest, "VALIDATION_ERROR", "unknown entity_type")
	})

	t.Run("entity_does_not_exist", func(t *testing.T) {
		r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token, uploadPath, []wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "entity_id", content: uuid.New().String()},
			{name: "file", filename: "x.txt", declaredType: "text/plain", content: "data"},
		})
		wikiAttachNegRequireError(t, r, http.StatusNotFound, "NOT_FOUND", "unknown entity")
	})

	t.Run("no_file_part", func(t *testing.T) {
		r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token, uploadPath, []wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "entity_id", content: pageID},
		})
		wikiAttachNegRequireError(t, r, http.StatusBadRequest, "VALIDATION_ERROR", "no file part")
	})

	// Not one of the refusals may have written a row. Without this the tests
	// above would pass against a handler that stored the attachment and THEN
	// reported an error.
	require.Zero(t, wikiAttachNegAttachmentCount(t, f.ts, pageID),
		"a refused upload must not leave an attachment behind")
}

// TestWikiAttachNeg_UploadWithNoDeclaredTypeIsStoredGeneric covers the blank
// content-type default. multipart.Writer.CreateFormFile always declares
// application/octet-stream, so only a hand-built part with no Content-Type
// header reaches it.
//
// Defect it catches: storing the empty string. content_type is NOT NULL but has
// no CHECK on length, so "" persists happily, and every consumer of it — the
// file-list icon, the document model's image check, any future export — then
// has to special-case a type that means nothing.
func TestWikiAttachNeg_UploadWithNoDeclaredTypeIsStoredGeneric(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Untyped", "body", nil)

	r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID),
		[]wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "entity_id", content: pageID},
			// declaredType deliberately empty: no Content-Type header at all.
			{name: "file", filename: "mystery.bin", content: "some bytes"},
		})
	require.Equal(t, http.StatusCreated, r.StatusCode, "upload: %s", r.Body)

	var att struct {
		ID          string `json:"id"`
		ContentType string `json:"content_type"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &att))
	require.Equal(t, "application/octet-stream", att.ContentType,
		"an undeclared type must be defaulted, never stored blank")

	// And it is the stored column, not just the response, that got the default.
	var stored string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT content_type FROM attachments WHERE id = $1`, uuid.MustParse(att.ID)).Scan(&stored))
	require.Equal(t, "application/octet-stream", stored)
}

// TestWikiAttachNeg_UploadRefusesOversizedFile pins the documented 413.
//
// Defect it catches: the ErrTooLarge arm collapsing into the generic error arm,
// which answers 500. A client told "internal error" retries; a client told 413
// tells the person their file is too big. The distinction is the whole reason
// the arm exists.
func TestWikiAttachNeg_UploadRefusesOversizedFile(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Big", "body", nil)

	r := wikiAttachNegPostOversized(t, f.ts, f.ts.Token,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID),
		attachments.MaxSizeBytes+1,
		map[string]string{"entity_type": "page", "entity_id": pageID})

	wikiAttachNegRequireError(t, r, http.StatusRequestEntityTooLarge, "VALIDATION_ERROR", "oversized upload")
	require.Zero(t, wikiAttachNegAttachmentCount(t, f.ts, pageID),
		"an over-size upload must not be recorded")
}

// ── attachments: list / download / delete refusals ─────────────────────────

// TestWikiAttachNeg_ListInSpaceRefusals covers the space-scoped list's own
// validation, which is separate code from the upload's despite reading the same
// two fields.
//
// The last case is the one that matters: an entity that lives in ANOTHER space
// must 404 even though the caller can read the space in the URL. Without that
// re-check, the list endpoint becomes a way to enumerate any page's attachment
// filenames from any space the caller happens to hold.
func TestWikiAttachNeg_ListInSpaceRefusals(t *testing.T) {
	f := newShareFixture(t)
	listPath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID)

	wikiAttachNegRequireError(t, f.ts.get(t, listPath+"?entity_type=page", true),
		http.StatusBadRequest, "VALIDATION_ERROR", "no entity_id")
	wikiAttachNegRequireError(t, f.ts.get(t, listPath+"?entity_type=page&entity_id=nope", true),
		http.StatusBadRequest, "VALIDATION_ERROR", "malformed entity_id")
	wikiAttachNegRequireError(t, f.ts.get(t,
		fmt.Sprintf("%s?entity_type=comment&entity_id=%s", listPath, uuid.New()), true),
		http.StatusBadRequest, "VALIDATION_ERROR", "unknown entity_type")
	wikiAttachNegRequireError(t, f.ts.get(t,
		fmt.Sprintf("%s?entity_type=page&entity_id=%s", listPath, uuid.New()), true),
		http.StatusNotFound, "NOT_FOUND", "unknown entity")

	// A page in a different space, with a real attachment on it.
	otherSpace := createScopedSpace(t, f.ts, "Elsewhere", "elsewhere-list", "codex")
	cr := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", f.ts.OrgID, otherSpace),
		map[string]interface{}{"title": "Foreign", "content": "x"}, true)
	require.Equal(t, http.StatusCreated, cr.StatusCode, "create foreign page: %s", cr.Body)
	var foreign struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(cr.Body, &foreign))

	wikiAttachNegRequireError(t, f.ts.get(t,
		fmt.Sprintf("%s?entity_type=page&entity_id=%s", listPath, foreign.ID), true),
		http.StatusNotFound, "NOT_FOUND", "entity in another space")
}

// TestWikiAttachNeg_DownloadInSpaceRefusals.
//
// A malformed attachment id answers 404, not 400, and that is deliberate: the
// shape of an id must not tell a prober whether it was well formed but absent
// or ill formed. The cross-space case is the leak one — an attachment id from
// another space, requested through a space the caller CAN read, must not stream.
func TestWikiAttachNeg_DownloadInSpaceRefusals(t *testing.T) {
	f := newShareFixture(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID)

	wikiAttachNegRequireError(t, f.ts.get(t, base+"/not-a-uuid", true),
		http.StatusNotFound, "NOT_FOUND", "malformed attachment id")

	// An attachment on a page in another space.
	otherSpace := createScopedSpace(t, f.ts, "Elsewhere", "elsewhere-dl", "codex")
	cr := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", f.ts.OrgID, otherSpace),
		map[string]interface{}{"title": "Foreign", "content": "x"}, true)
	require.Equal(t, http.StatusCreated, cr.StatusCode, "create foreign page: %s", cr.Body)
	var foreign struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(cr.Body, &foreign))

	r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, otherSpace),
		[]wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "entity_id", content: foreign.ID},
			{name: "file", filename: "foreign.txt", declaredType: "text/plain", content: "FOREIGN-bytes"},
		})
	require.Equal(t, http.StatusCreated, r.StatusCode, "foreign upload: %s", r.Body)
	var foreignAtt struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &foreignAtt))

	// Premise: it does stream through its OWN space, so the refusal below is
	// about the space in the URL and not about the attachment being unreadable.
	own := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments/%s",
		f.ts.OrgID, otherSpace, foreignAtt.ID), true)
	require.Equal(t, http.StatusOK, own.StatusCode, "own-space download: %s", own.Body)
	require.Equal(t, "FOREIGN-bytes", string(own.Body))

	cross := f.ts.get(t, base+"/"+foreignAtt.ID, true)
	wikiAttachNegRequireError(t, cross, http.StatusNotFound, "NOT_FOUND", "attachment from another space")
	require.NotContains(t, string(cross.Body), "FOREIGN-bytes", "the bytes must not leak with the refusal")
}

// TestWikiAttachNeg_DeleteInSpaceRefusals.
//
// The cross-space case is the destructive twin of the download one: a delete
// that skipped the space re-check would let anyone holding one space delete
// attachments in every other space, and a soft delete leaves no trace the owner
// would notice. The assertion that the foreign attachment still streams
// afterwards is what proves the refusal was a refusal and not a partial delete.
func TestWikiAttachNeg_DeleteInSpaceRefusals(t *testing.T) {
	f := newShareFixture(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, f.spaceID)

	wikiAttachNegRequireError(t, f.ts.delete(t, base+"/not-a-uuid", true),
		http.StatusNotFound, "NOT_FOUND", "malformed attachment id")
	wikiAttachNegRequireError(t, f.ts.delete(t, base+"/"+uuid.New().String(), true),
		http.StatusNotFound, "NOT_FOUND", "unknown attachment id")

	otherSpace := createScopedSpace(t, f.ts, "Elsewhere", "elsewhere-del", "codex")
	cr := f.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/wiki", f.ts.OrgID, otherSpace),
		map[string]interface{}{"title": "Foreign", "content": "x"}, true)
	require.Equal(t, http.StatusCreated, cr.StatusCode, "create foreign page: %s", cr.Body)
	var foreign struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(cr.Body, &foreign))

	r := wikiAttachNegPostMultipart(t, f.ts, f.ts.Token,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments", f.ts.OrgID, otherSpace),
		[]wikiAttachNegPart{
			{name: "entity_type", content: "page"},
			{name: "entity_id", content: foreign.ID},
			{name: "file", filename: "keepme.txt", declaredType: "text/plain", content: "KEEP-bytes"},
		})
	require.Equal(t, http.StatusCreated, r.StatusCode, "foreign upload: %s", r.Body)
	var foreignAtt struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &foreignAtt))

	wikiAttachNegRequireError(t, f.ts.delete(t, base+"/"+foreignAtt.ID, true),
		http.StatusNotFound, "NOT_FOUND", "delete of an attachment in another space")

	require.Equal(t, 1, wikiAttachNegAttachmentCount(t, f.ts, foreign.ID),
		"the refused delete must not have soft-deleted the foreign attachment")
	still := f.ts.get(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/attachments/%s",
		f.ts.OrgID, otherSpace, foreignAtt.ID), true)
	require.Equal(t, http.StatusOK, still.StatusCode, "%s", still.Body)
	require.Equal(t, "KEEP-bytes", string(still.Body))
}

// TestWikiAttachNeg_SharedFamilyRefusals covers the share-authorised read
// family's own parsing and coverage checks. Every refusal here is 404 by
// design: this family is reachable without space access, so distinguishing
// "no such entity" from "not shared with you" would itself be the leak.
func TestWikiAttachNeg_SharedFamilyRefusals(t *testing.T) {
	f := newShareFixture(t)
	sharedPage, _ := f.createPage(t, "Shared", "body", nil)
	attID := f.uploadAttachment(t, "page", sharedPage, "figure.txt", "SHARED-bytes")
	unsharedPage, _ := f.createPage(t, "Private", "body", nil)

	sharedBase := fmt.Sprintf("/api/v1/orgs/%s/shared", f.ts.OrgID)

	// Listing an entity nobody shared with the caller.
	wikiAttachNegRequireError(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("%s/page/%s/attachments", sharedBase, unsharedPage)),
		http.StatusNotFound, "NOT_FOUND", "list attachments of an unshared entity")

	// A malformed entity id, and an entity type the share model does not know.
	wikiAttachNegRequireError(t, f.ts.getAs(t, f.outsiderTok, sharedBase+"/page/not-a-uuid/attachments"),
		http.StatusNotFound, "NOT_FOUND", "malformed shared entity id")
	wikiAttachNegRequireError(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("%s/comment/%s/attachments", sharedBase, sharedPage)),
		http.StatusNotFound, "NOT_FOUND", "unknown shared entity type")

	f.createShare(t, map[string]interface{}{
		"entity_type": "page", "entity_id": sharedPage, "audience": "org",
	})

	// Premise: with the share in place the outsider really can read it, so the
	// refusals above and below are about the request and not about the share.
	ok := f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("%s/page/%s/attachments/%s", sharedBase, sharedPage, attID))
	require.Equal(t, http.StatusOK, ok.StatusCode, "shared download: %s", ok.Body)
	require.Equal(t, "SHARED-bytes", string(ok.Body))

	// A malformed attachment id under a genuinely shared entity.
	wikiAttachNegRequireError(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("%s/page/%s/attachments/not-a-uuid", sharedBase, sharedPage)),
		http.StatusNotFound, "NOT_FOUND", "malformed shared attachment id")
	// And a malformed entity type on the download route too — coveredEntity
	// runs before the attachment id is even looked at.
	wikiAttachNegRequireError(t, f.ts.getAs(t, f.outsiderTok,
		fmt.Sprintf("%s/project_item/not-a-uuid/attachments/%s", sharedBase, attID)),
		http.StatusNotFound, "NOT_FOUND", "malformed shared entity id on download")
}

// TestWikiAttachNeg_MissingObjectIsAnErrorNotAnEmptyBody plants a row whose
// object was never stored — what an evicted bucket, a half-rolled-back upload
// or a restored database without its blobs looks like.
//
// Defect it catches: ignoring the OpenForServing error and streaming anyway.
// That answers 200 with zero bytes and an image/... content type, which the
// browser renders as a broken image and every client reads as a successful
// download of an empty file. A 500 with the envelope is the honest answer.
func TestWikiAttachNeg_MissingObjectIsAnErrorNotAnEmptyBody(t *testing.T) {
	f := newShareFixture(t)
	pageID, _ := f.createPage(t, "Ghost", "body", nil)

	attID := uuid.New()
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`INSERT INTO attachments
		   (id, org_id, entity_type, entity_id, filename, content_type, size_bytes, object_key, created_by)
		 VALUES ($1, $2, 'page', $3, 'ghost.png', 'image/png', 11, $4, $5)`,
		attID, f.ts.OrgID, uuid.MustParse(pageID),
		"orgs/"+f.ts.OrgID.String()+"/attachments/"+attID.String(), f.ts.UserID)
	require.NoError(t, err, "planting a row whose object does not exist")

	r := f.ts.get(t, f.spaceAttachmentPath(attID.String()), true)
	wikiAttachNegRequireError(t, r, http.StatusInternalServerError, "INTERNAL_ERROR", "object missing from the store")
	require.NotEqual(t, "image/png", r.Header.Get("Content-Type"),
		"a failed read must not answer with the stored type")
}

// ── wiki: path-parameter parsing ───────────────────────────────────────────

// TestWikiAttachNeg_PageIDMustBeAUUID sweeps every wiki route that takes a
// {pageID}. Each must refuse a malformed id with 400 before it touches the
// service.
//
// Defect it catches: a handler that dropped the parse and passed uuid.Nil on.
// uuid.Nil is a perfectly valid key — a read keyed on it merely returns nothing,
// but a WRITE keyed on it is a live statement against a row-shaped id, and the
// draft table in particular is keyed on (page_id, author_id) with no foreign key
// to a live page. A missing parse there writes a draft nobody can ever reach.
func TestWikiAttachNeg_PageIDMustBeAUUID(t *testing.T) {
	f := newDocFixture(t)
	const bad = "not-a-uuid"

	cases := []struct {
		name   string
		method string
		suffix string
		body   any
	}{
		{"get_page", http.MethodGet, "", nil},
		{"update_page", http.MethodPut, "", map[string]any{"title": "T", "content": "c", "expected_version": 1}},
		{"delete_page", http.MethodDelete, "", nil},
		{"move_page", http.MethodPost, "/move", map[string]any{"position": 0}},
		{"share_impact", http.MethodGet, "/share-impact", nil},
		{"list_revisions", http.MethodGet, "/revisions", nil},
		{"get_revision", http.MethodGet, "/revisions/1", nil},
		{"diff_revisions", http.MethodGet, "/diff?from=1&to=2", nil},
		{"render_page", http.MethodGet, "/render", nil},
		{"get_document", http.MethodGet, "/document", nil},
		{"save_draft", http.MethodPut, "/draft", map[string]any{"title": "T", "doc": json.RawMessage(`{"type":"doc"}`), "base_version": 1}},
		{"discard_draft", http.MethodDelete, "/draft", nil},
		{"publish", http.MethodPost, "/publish", map[string]any{"title": "T", "doc": json.RawMessage(`{"type":"doc"}`), "base_version": 1}},
		{"upload_image", http.MethodPost, "/images", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := f.ts.requestAs(t, f.ts.Token, tc.method, f.pagePath(bad, tc.suffix), tc.body)
			wikiAttachNegRequireError(t, r, http.StatusBadRequest, "BAD_REQUEST", tc.name)
		})
	}
}

// TestWikiAttachNeg_UnknownPageIsNotFound is the same sweep for a well-formed
// id that names nothing.
//
// Defect it catches: reporting a missing page as anything other than 404 —
// a 500 leaks the internal error text, and a 200 on a write path would mean the
// handler created state for a page that does not exist.
func TestWikiAttachNeg_UnknownPageIsNotFound(t *testing.T) {
	f := newDocFixture(t)
	ghost := uuid.New().String()
	document := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"}]}`)

	cases := []struct {
		name   string
		method string
		suffix string
		body   any
	}{
		{"get_page", http.MethodGet, "", nil},
		{"update_page", http.MethodPut, "", map[string]any{"title": "T", "content": "c", "expected_version": 1}},
		{"delete_page", http.MethodDelete, "", nil},
		{"move_page", http.MethodPost, "/move", map[string]any{"position": 0}},
		{"share_impact", http.MethodGet, "/share-impact", nil},
		{"get_revision", http.MethodGet, "/revisions/1", nil},
		{"diff_revisions", http.MethodGet, "/diff?from=1&to=2", nil},
		{"render_page", http.MethodGet, "/render", nil},
		{"get_document", http.MethodGet, "/document", nil},
		{"save_draft", http.MethodPut, "/draft", map[string]any{"title": "T", "doc": document, "base_version": 1}},
		{"discard_draft", http.MethodDelete, "/draft", nil},
		{"publish", http.MethodPost, "/publish", map[string]any{"title": "T", "doc": document, "base_version": 1}},
		{"upload_image", http.MethodPost, "/images", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := f.ts.requestAs(t, f.ts.Token, tc.method, f.pagePath(ghost, tc.suffix), tc.body)
			wikiAttachNegRequireError(t, r, http.StatusNotFound, "NOT_FOUND", tc.name)
		})
	}

	// Nothing above may have created a draft for a page that does not exist.
	var drafts int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM page_drafts WHERE page_id = $1`, uuid.MustParse(ghost)).Scan(&drafts))
	require.Zero(t, drafts, "a 404 on the draft route must not have written a draft")
}

// ── wiki: body and validation refusals ─────────────────────────────────────

// TestWikiAttachNeg_CreatePageRefusals.
//
// Defect the blank-title cases catch: a page with no title is unnameable in the
// tree, unfindable by search (search_vector is generated over title and content)
// and indistinguishable from its siblings in every picker. The whitespace case
// is the one a naive `title == ""` check misses.
func TestWikiAttachNeg_CreatePageRefusals(t *testing.T) {
	f := newDocFixture(t)
	before := f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.wikiPath("/"), nil)
	require.Equal(t, http.StatusOK, before.StatusCode)
	var beforePages []json.RawMessage
	require.NoError(t, json.Unmarshal(before.Body, &beforePages))

	// A mistyped field: title is a string, so a number will not decode.
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodPost, f.wikiPath("/"), map[string]any{"title": 42}),
		http.StatusBadRequest, "BAD_REQUEST", "mistyped title")

	// An unknown field: DecodeJSON disallows them, so a client that renamed a
	// field is told rather than silently having it dropped.
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodPost, f.wikiPath("/"),
			map[string]any{"title": "T", "contents": "typo"}),
		http.StatusBadRequest, "BAD_REQUEST", "unknown field")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodPost, f.wikiPath("/"),
			map[string]any{"title": "", "content": "body"}),
		http.StatusBadRequest, "VALIDATION_ERROR", "blank title")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodPost, f.wikiPath("/"),
			map[string]any{"title": "   \t\n ", "content": "body"}),
		http.StatusBadRequest, "VALIDATION_ERROR", "whitespace-only title")

	after := f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.wikiPath("/"), nil)
	require.Equal(t, http.StatusOK, after.StatusCode)
	var afterPages []json.RawMessage
	require.NoError(t, json.Unmarshal(after.Body, &afterPages))
	require.Len(t, afterPages, len(beforePages), "no refused create may have added a page")
}

// TestWikiAttachNeg_UpdatePageRefusals — the markdown save's own body checks.
//
// The blank-title case is separate code from create's: the update path
// validates in the service before the transaction, and a page that was created
// with a title can otherwise be emptied of one by a later save.
func TestWikiAttachNeg_UpdatePageRefusals(t *testing.T) {
	f := newDocFixture(t)

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodPut, f.pagePath(f.pageID, ""),
			map[string]any{"title": "T", "content": 99, "expected_version": 1}),
		http.StatusBadRequest, "BAD_REQUEST", "mistyped content")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodPut, f.pagePath(f.pageID, ""),
			map[string]any{"title": "  ", "content": "body", "expected_version": 1}),
		http.StatusBadRequest, "VALIDATION_ERROR", "blank title on update")

	// Neither refusal advanced the page.
	var version int32
	var title string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT version, title FROM pages WHERE id = $1`, uuid.MustParse(f.pageID)).Scan(&version, &title))
	require.Equal(t, int32(1), version, "a refused save must not bump the version")
	require.Equal(t, "Runbook", title)
}

// TestWikiAttachNeg_RevisionAndDiffParamRefusals covers the version parsing on
// the history routes — the whole of GetRevision, which had no coverage at all.
//
// Defect the "abc" case catches: an unparsed version silently becoming 0, which
// matches no revision and reports "revision not found" for a request that was
// never about a real version — a client bug reported as missing data.
func TestWikiAttachNeg_RevisionAndDiffParamRefusals(t *testing.T) {
	f := newDocFixture(t)

	// The happy path first, so the refusals below are not vacuous: version 1
	// exists and is fetchable.
	ok := f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/revisions/1"), nil)
	require.Equal(t, http.StatusOK, ok.StatusCode, "revision 1: %s", ok.Body)
	var revision struct {
		Version int32  `json:"version"`
		Title   string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(ok.Body, &revision))
	require.Equal(t, int32(1), revision.Version)
	require.Equal(t, "Runbook", revision.Title)

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/revisions/abc"), nil),
		http.StatusBadRequest, "BAD_REQUEST", "non-numeric version")
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/revisions/99"), nil),
		http.StatusNotFound, "NOT_FOUND", "version that does not exist")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/diff"), nil),
		http.StatusBadRequest, "VALIDATION_ERROR", "diff with no versions")
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/diff?from=abc&to=1"), nil),
		http.StatusBadRequest, "BAD_REQUEST", "non-numeric from")
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/diff?from=1&to=xyz"), nil),
		http.StatusBadRequest, "BAD_REQUEST", "non-numeric to")
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.pagePath(f.pageID, "/diff?from=1&to=99"), nil),
		http.StatusNotFound, "NOT_FOUND", "diff against a version that does not exist")
}

// TestWikiAttachNeg_SearchParamHandling.
//
// The limit cases are the ones that assert something: a limit inside the bounds
// must actually cut the result set, and one outside them — or one that is not a
// number at all — must fall back to the default rather than refuse or be passed
// to the database. A handler that ignored `limit` entirely fails the first; one
// that passed it through unchecked fails the second, and `limit=0` would then
// reach SQL as LIMIT 0 and return nothing.
func TestWikiAttachNeg_SearchParamHandling(t *testing.T) {
	f := newDocFixture(t)
	for _, title := range []string{"Aardvark one", "Aardvark two", "Aardvark three"} {
		f.createPage(t, f.ts.Token, title, "aardvark husbandry notes")
	}

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.wikiPath("/search"), nil),
		http.StatusBadRequest, "VALIDATION_ERROR", "search with no q")

	count := func(t *testing.T, query string) int {
		t.Helper()
		r := f.ts.requestAs(t, f.ts.Token, http.MethodGet, f.wikiPath(query), nil)
		require.Equal(t, http.StatusOK, r.StatusCode, "search %s: %s", query, r.Body)
		var rows []json.RawMessage
		require.NoError(t, json.Unmarshal(r.Body, &rows))
		return len(rows)
	}

	require.Equal(t, 3, count(t, "/search?q=aardvark"), "all three pages match")
	require.Equal(t, 2, count(t, "/search?q=aardvark&limit=2"), "an in-range limit must cut the results")
	require.Equal(t, 3, count(t, "/search?q=aardvark&limit=abc"),
		"an unparseable limit must fall back to the default, not refuse and not reach SQL")
	require.Equal(t, 3, count(t, "/search?q=aardvark&limit=0"),
		"limit=0 must fall back to the default, never become LIMIT 0")
	require.Equal(t, 3, count(t, "/search?q=aardvark&limit=5000"),
		"an out-of-range limit must fall back to the default")
}

// ── wiki: capability refusals ──────────────────────────────────────────────

// TestWikiAttachNeg_MovePageRefusals.
//
// The persona for the 403 is a CONTRIBUTOR, not a viewer (CLAUDE.md §2): a
// viewer never gets past RequireWriteFloor(create_items), so a viewer-based test
// passes with the in-handler access.Can(CapEditAnyItem) check deleted — it
// asserts the middleware. A contributor clears the floor and lacks
// edit_any_item, so the in-handler gate is the only thing left that can refuse
// them. The positive control is the second request: the identical body, on the
// identical route, from a persona that DOES hold edit_any_item, answers 200. So
// the 403 is attributable to the capability and not to the request — which is
// the strongest form of the claim this suite can make without editing the
// handler, and editing it is out of bounds here.
//
// The cross-space case is the ADR-0008 rule 9 guard: moving a page into a space
// the caller cannot edit answers 404, not 403, so the destination's existence
// does not leak to somebody probing space ids.
func TestWikiAttachNeg_MovePageRefusals(t *testing.T) {
	f := newDocFixture(t)
	movePath := f.pagePath(f.pageID, "/move")

	// Contributor: past the write floor, short of edit_any_item.
	requireAPIForbidden(t, f.ts.requestAs(t, f.contribTok, http.MethodPost, movePath,
		map[string]any{"position": 1}))

	// The same request from a caller who does hold edit_any_item succeeds, so
	// the refusal above is about the capability and not about the request.
	okMove := f.ts.requestAs(t, f.editorTok, http.MethodPost, movePath, map[string]any{"position": 1})
	require.Equal(t, http.StatusOK, okMove.StatusCode, "editor move: %s", okMove.Body)

	// A body that will not decode, from a caller who passed the capability gate.
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.editorTok, http.MethodPost, movePath, map[string]any{"position": "first"}),
		http.StatusBadRequest, "BAD_REQUEST", "mistyped position")

	// A target space the caller cannot edit: 404, and the page has not moved.
	elsewhere := createScopedSpace(t, f.ts, "Elsewhere", "elsewhere-move", "codex")
	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.editorTok, http.MethodPost, movePath,
			map[string]any{"target_space_id": elsewhere, "position": 0}),
		http.StatusNotFound, "NOT_FOUND", "move into a space the caller cannot edit")

	var spaceID uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT space_id FROM pages WHERE id = $1`, uuid.MustParse(f.pageID)).Scan(&spaceID))
	require.Equal(t, f.spaceID, spaceID.String(), "a refused move must leave the page where it was")
}

// TestWikiAttachNeg_DeletePageContributorRefused.
//
// Same reasoning as the move case: the contributor clears create_items, so the
// handler's own access.CanEditEntity check against pages.author_id is the only
// thing left that can refuse this. The positive control is at the end — the same
// contributor deleting a page they created answers 204 — so the 403 is about
// ownership and not about the persona being powerless.
func TestWikiAttachNeg_DeletePageContributorRefused(t *testing.T) {
	f := newDocFixture(t)
	theirPage := f.createPage(t, f.secondTok, "Theirs", "body")

	requireAPIForbidden(t, f.ts.requestAs(t, f.contribTok, http.MethodDelete, f.pagePath(theirPage, ""), nil))

	var deletedAt *string
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT deleted_at::text FROM pages WHERE id = $1`, uuid.MustParse(theirPage)).Scan(&deletedAt))
	require.Nil(t, deletedAt, "a refused delete must not have soft-deleted the page")

	// And on their own page the same contributor may delete, so the refusal is
	// about ownership rather than the persona being powerless.
	own := f.createPage(t, f.contribTok, "Mine", "body")
	r := f.ts.requestAs(t, f.contribTok, http.MethodDelete, f.pagePath(own, ""), nil)
	require.Equal(t, http.StatusNoContent, r.StatusCode, "own delete: %s", r.Body)
}

// ── wiki: the document surface ─────────────────────────────────────────────

// TestWikiAttachNeg_DraftAndPublishRefusals.
//
// base_version 0 is the interesting one. A draft's base_version is what the
// staleness flag and the publish conflict check are both computed against, and
// page versions start at 1 — so a draft stored with 0 is permanently "stale"
// and its publish permanently conflicts, with no version the author can reload
// to. Refusing it at the door is the only place that can be fixed.
func TestWikiAttachNeg_DraftAndPublishRefusals(t *testing.T) {
	f := newDocFixture(t)
	document := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}`)

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
			map[string]any{"title": "T", "doc": document, "base_version": 0}),
		http.StatusConflict, "CONFLICT", "draft with base_version 0")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
			map[string]any{"title": "T", "doc": document, "base_version": "one"}),
		http.StatusBadRequest, "BAD_REQUEST", "mistyped base_version on draft")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
			map[string]any{"title": "T", "doc": document, "base_version": "one"}),
		http.StatusBadRequest, "BAD_REQUEST", "mistyped base_version on publish")

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
			map[string]any{"title": "   ", "doc": document, "base_version": 1}),
		http.StatusBadRequest, "VALIDATION_ERROR", "publish with a blank title")

	// No draft was stored and the page was not published by any of the above.
	var drafts int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM page_drafts WHERE page_id = $1`, uuid.MustParse(f.pageID)).Scan(&drafts))
	require.Zero(t, drafts, "a refused autosave must not store a draft")
	require.Equal(t, int32(1), f.openDocument(t, f.contribTok, f.pageID).BaseVersion,
		"a refused publish must not bump the version")
}

// TestWikiAttachNeg_PublishRefusesADocumentNestedTooDeeply.
//
// Defect it catches: the depth guard reported as a 500. The recursion limit
// exists so a hostile or generated document cannot exhaust the stack, and the
// person who sent it has to be told what is wrong with their page — an internal
// error tells them to retry, which sends the same document back.
func TestWikiAttachNeg_PublishRefusesADocumentNestedTooDeeply(t *testing.T) {
	f := newDocFixture(t)

	// 250 nested blockquotes: past the 200-node depth ceiling, and made of node
	// types the editor knows, so nothing else can refuse it first.
	node := map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "deep"}}}
	for i := 0; i < 250; i++ {
		node = map[string]any{"type": "blockquote", "content": []any{node}}
	}
	document := map[string]any{"type": "doc", "content": []any{node}}

	wikiAttachNegRequireError(t,
		f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
			map[string]any{"title": "Deep", "doc": document, "base_version": 1}),
		http.StatusBadRequest, "VALIDATION_ERROR", "over-deep document")

	require.Equal(t, int32(1), f.openDocument(t, f.contribTok, f.pageID).BaseVersion,
		"a refused publish must not bump the version")
}

// TestWikiAttachNeg_UploadImageRefusals covers the image route's own multipart
// handling, which is a second implementation of the generic attachment route's
// and refuses on its own terms.
//
// The 413 matters here for the same reason it does on the generic route: the
// editor uploads on paste, so "that image is too big" has to be distinguishable
// from "something broke", or the editor retries the same paste forever.
func TestWikiAttachNeg_UploadImageRefusals(t *testing.T) {
	f := newDocFixture(t)
	imagePath := f.pagePath(f.pageID, "/images")

	wikiAttachNegRequireError(t,
		wikiAttachNegPostRaw(t, f.ts, f.contribTok, imagePath,
			"multipart/form-data; boundary=nonsense", `{"file":"nope"}`),
		http.StatusBadRequest, "BAD_REQUEST", "non-multipart image body")

	wikiAttachNegRequireError(t,
		wikiAttachNegPostMultipart(t, f.ts, f.contribTok, imagePath, []wikiAttachNegPart{
			{name: "alt", content: "a caption but no file"},
		}),
		http.StatusBadRequest, "VALIDATION_ERROR", "image request with no file part")

	wikiAttachNegRequireError(t,
		wikiAttachNegPostOversized(t, f.ts, f.contribTok, imagePath, attachments.MaxSizeBytes+1, nil),
		http.StatusRequestEntityTooLarge, "VALIDATION_ERROR", "oversized image")

	require.Zero(t, wikiAttachNegAttachmentCount(t, f.ts, f.pageID),
		"no refused image upload may have stored an attachment")
}
