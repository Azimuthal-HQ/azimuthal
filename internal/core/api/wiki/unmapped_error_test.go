package wiki_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	wikiapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
)

// This is the wiki half of the change H5 made to the three project surfaces and
// the write-authorisation pass made to tickets (#23b) and notifications (#28).
// known-issues #23 names this file explicitly — "internal/core/api/wiki/handler.go
// remains and is the last of the family" — so the shape follows
// internal/core/api/tickets/unmapped_error_test.go deliberately rather than
// inventing a fourth idiom for the same guarantee.

// wikiLeakMarker exists nowhere else in the repository. If it appears in a
// response body, internal detail crossed the wire.
//
// The wording reproduces the disclosure this arm actually carried: handleWikiError's
// default branch was fmt.Sprintf("wiki operation failed: %v", err), so anything
// the store returned — pgx wording, the table, the constraint, the SQLSTATE —
// was handed to any caller who could reach a wiki route.
const wikiLeakMarker = "pages_leak_marker_space_id_fkey"

var errWikiSynthetic = errors.New(
	`ERROR: insert or update on table "pages" violates foreign key constraint "` +
		wikiLeakMarker + `" (SQLSTATE 23503)`)

// failingPageStore answers the scoped read with an error no arm of
// handleWikiError maps, which is the only way to reach the default arm.
//
// The embedded mockPageStore answers wiki.ErrPageNotFound for every read, and
// every other double in this package returns a mapped sentinel or nil — so
// before this type existed no test could reach the default arm at all, which is
// why the leak outlived the pass that closed its three siblings.
type failingPageStore struct {
	mockPageStore
}

// GetPageInSpace is the read GetPage actually takes. Without this override the
// handler would answer the embedded mock's ErrPageNotFound and the test would
// assert the 404 mapping rather than the default arm it exists to cover.
func (f *failingPageStore) GetPageInSpace(context.Context, generated.GetPageInSpaceParams) (generated.Page, error) {
	return generated.Page{}, errWikiSynthetic
}

// GetPageByID fails the same way, so the entity-share read path cannot quietly
// become the arm under test if GetPage's internals change.
func (f *failingPageStore) GetPageByID(context.Context, uuid.UUID) (generated.Page, error) {
	return generated.Page{}, errWikiSynthetic
}

func handlerWithFailingWiki() *wikiapi.Handler {
	svc := wiki.NewService(&failingPageStore{}, &mockContentTx{})
	tagSvc := tags.NewService(&mockTagRepo{})
	docs := wiki.NewDocumentService(&mockDocumentStore{}, &mockDocumentTx{}, wiki.UnavailableImageStore{}, tagSvc)
	return wikiapi.NewHandler(svc, docs, tagSvc)
}

func captureWikiLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

type wikiWireError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func getPageThroughFailingStore(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	h := handlerWithFailingWiki()
	req := withSpaceAccess(t,
		withParam(httptest.NewRequest(http.MethodGet, "/", nil), "pageID", uuid.New().String()),
		uuid.New())
	rr := httptest.NewRecorder()
	h.GetPage(rr, req)
	return rr
}

// Fails before the fix: the default arm was
// `fmt.Sprintf("wiki operation failed: %v", err)`, so the body read
// `wiki operation failed: getting page: ERROR: insert or update on table
// "pages" violates foreign key constraint "..." (SQLSTATE 23503)` and every
// NotContains below fails.
//
// It asserts absence rather than one exact string, following the tickets and
// projects tests: rewording the message must not silently stop testing
// anything, but a message that starts carrying the driver's text again must
// fail.
func TestUnmappedWikiError_DoesNotLeakInternalDetailToTheWire(t *testing.T) {
	rr := getPageThroughFailingStore(t)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var env wikiWireError
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env), "body was %q", rr.Body.String())
	require.Equal(t, "INTERNAL_ERROR", env.Error.Code)

	require.NotContains(t, env.Error.Message, wikiLeakMarker,
		"the constraint name reached the client — the default arm is interpolating the error again")
	require.NotContains(t, strings.ToLower(env.Error.Message), "constraint",
		"the client must not learn that a constraint was involved")
	require.NotContains(t, strings.ToLower(env.Error.Message), "pages_",
		"a schema object name must not reach the client")
	require.NotContains(t, env.Error.Message, "SQLSTATE",
		"driver vocabulary must not reach the client")
	require.NotContains(t, strings.ToLower(env.Error.Message), "getting page",
		"internal call-stack wording must not reach the client")

	require.Equal(t, "wiki operation failed", env.Error.Message)
}

// The other half of the trade. Suppressing the detail on the wire is only
// correct if the detail survives somewhere an operator can reach it, so this
// fails if the error is dropped rather than moved.
func TestUnmappedWikiError_FullErrorReachesTheServerLog(t *testing.T) {
	logs := captureWikiLogs(t)

	rr := getPageThroughFailingStore(t)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	require.Contains(t, logs.String(), wikiLeakMarker,
		"the underlying error must reach the server log, or the detail was discarded rather than moved")
	require.Contains(t, logs.String(), "unmapped handler error",
		"the log line must be identifiable as the unmapped-error arm")
	require.Contains(t, logs.String(), `"surface":"wiki"`,
		"the log line must name which surface produced it")
}
