package projects_test

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

	projectsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/projects"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/customfields"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/itemtypes"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/projects"
)

// leakMarker is a string that exists nowhere else in the repository, embedded
// in a synthetic error shaped like the real one. If it appears in a response
// body, internal detail crossed the wire.
//
// The wording is not arbitrary: known-issues #24 recorded a real 500 reading
// `project operation failed: ... duplicate key value violates unique
// constraint "..."`, which handed a client the name of a schema object. That
// is the disclosure this guards.
const leakMarker = "azimuthal_items_leak_marker_key"

var errSynthetic = errors.New(
	`ERROR: duplicate key value violates unique constraint "` + leakMarker + `" (SQLSTATE 23505)`)

// failingItemRepo answers GetByID with an error no arm of handleProjectError
// maps, which is the only way to reach the default arm. The rest of the
// interface comes from the embedded mockItemRepo.
//
// Nothing like this existed before: every projects mock returned nil or
// projects.ErrNotFound, so no test could reach the default arm at all. That is
// why the leak survived as long as it did.
type failingItemRepo struct {
	mockItemRepo
}

func (f *failingItemRepo) GetByID(context.Context, uuid.UUID) (*projects.Item, error) {
	return nil, errSynthetic
}

// GetByIDInSpace fails the same way. It is the read the space-scoped routes
// actually take now, so without this override the handler would answer the
// embedded mock's ErrNotFound and these tests would assert a 404 mapping rather
// than the default arm they exist to cover.
func (f *failingItemRepo) GetByIDInSpace(context.Context, uuid.UUID, uuid.UUID) (*projects.Item, error) {
	return nil, errSynthetic
}

func handlerWithFailingItems() *projectsapi.Handler {
	ir := &failingItemRepo{}
	sr := &mockSprintRepo{}
	return projectsapi.NewHandler(
		projects.NewItemService(ir, noopShareDeleter{}),
		projects.NewSprintService(sr),
		projects.NewBacklogService(ir, sr),
		projects.NewRoadmapService(ir, sr),
		nil,
	)
}

// captureLogs redirects the default slog logger into a buffer for one test and
// restores it afterwards. There is no log-capture helper in this repository —
// this is the first assertion that something was logged rather than returned.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// wireError is the error envelope as a caller sees it.
type wireError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func decodeWireError(t *testing.T, rr *httptest.ResponseRecorder) wireError {
	t.Helper()
	var env wireError
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env), "body was %q", rr.Body.String())
	return env
}

// TestUnmappedProjectError_DoesNotLeakInternalDetailToTheWire is the
// fails-before test for H5.
//
// Before the change the default arm was
// `fmt.Sprintf("project operation failed: %v", err)`, so the assertion below
// that the body does not contain leakMarker fails: the body reads
// `project operation failed: getting item: ERROR: duplicate key value violates
// unique constraint "azimuthal_items_leak_marker_key" (SQLSTATE 23505)`.
//
// It asserts absence rather than a new exact string on purpose, following
// wiki_error_classes_integration_test.go: a reworded message must not silently
// stop testing anything, but a message that starts carrying the driver's text
// again must fail.
func TestUnmappedProjectError_DoesNotLeakInternalDetailToTheWire(t *testing.T) {
	h := handlerWithFailingItems()
	req := withParam(withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"itemID", uuid.New().String()), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()

	h.GetItem(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	env := decodeWireError(t, rr)
	require.Equal(t, "INTERNAL_ERROR", env.Error.Code)

	require.NotContains(t, env.Error.Message, leakMarker,
		"the constraint name reached the client — the default arm is interpolating the error again")
	require.NotContains(t, strings.ToLower(env.Error.Message), "constraint",
		"the client must not learn that a constraint was involved")
	require.NotContains(t, env.Error.Message, "SQLSTATE",
		"driver vocabulary must not reach the client")
	require.NotContains(t, env.Error.Message, "getting item",
		"internal call-stack wording must not reach the client")

	// The generic envelope is still useful: a fixed, human-readable message.
	require.Equal(t, "project operation failed", env.Error.Message)
}

// TestUnmappedProjectError_FullErrorReachesTheServerLog is the other half of
// the trade. Suppressing the detail on the wire is only correct if the detail
// is still recorded somewhere — otherwise H5 would be destroying the one record
// of the cause, since handleProjectError did not log at all before this change.
func TestUnmappedProjectError_FullErrorReachesTheServerLog(t *testing.T) {
	logs := captureLogs(t)
	h := handlerWithFailingItems()

	// The request id is the whole point of the trade, so the handler is driven
	// through respond.RequestID rather than called bare: without the middleware
	// the id is empty on both sides and the join below would assert nothing.
	// A supplied X-Request-ID is echoed verbatim, which makes it deterministic.
	const wantID = "req_h5_join_probe"
	r := withParam(withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"itemID", uuid.New().String()), "spaceID", uuid.New().String())
	r.Header.Set("X-Request-ID", wantID)
	rr := httptest.NewRecorder()
	respond.RequestID(http.HandlerFunc(h.GetItem)).ServeHTTP(rr, r)

	require.Equal(t, http.StatusInternalServerError, rr.Code)

	logged := logs.String()
	require.Contains(t, logged, leakMarker,
		"the underlying error must still be recorded server-side; it is the only record of the cause")
	require.Contains(t, logged, "unmapped handler error")
	require.Contains(t, logged, `"surface":"project"`)
	require.Contains(t, logged, `"level":"ERROR"`)

	// The join: the id the caller was handed must be the id on the log line,
	// or "quote your request id" is advice that leads an operator nowhere.
	env := decodeWireError(t, rr)
	require.Equal(t, wantID, env.Error.RequestID, "the client must be handed the request id")
	require.Equal(t, wantID, rr.Header().Get("X-Request-ID"))
	require.Contains(t, logged, `"request_id":"`+wantID+`"`,
		"the log line's request_id must match the one the client was handed")
}

// TestUnmappedSchemaErrors_DoNotLeakInternalDetailToTheWire covers the two
// sibling switches in this same file — handleItemTypeError and
// handleCustomFieldError — which carried a byte-identical default arm.
//
// They are in scope because H5's blast radius is every unmapped 500 in the
// projects handler; leaving two copies of the same disclosure in the same file
// would have fixed the instance and not the defect. The equivalent arms in
// tickets and wiki are a different package and stay open under
// docs/known-issues.md #23(b).
func TestUnmappedSchemaErrors_DoNotLeakInternalDetailToTheWire(t *testing.T) {
	org := uuid.New().String()

	cases := []struct {
		name        string
		handler     *projectsapi.Handler
		route       func(*projectsapi.Handler) http.HandlerFunc
		params      map[string]string
		wantMessage string
		wantSurface string
	}{
		{
			name:        "item type",
			handler:     schemaHandler(&mockTypeRepo{getByIDErr: errSynthetic}, &mockFieldDefRepo{}),
			route:       func(h *projectsapi.Handler) http.HandlerFunc { return h.UpdateItemType },
			params:      map[string]string{"orgID": org, "typeID": uuid.New().String()},
			wantMessage: "item type operation failed",
			wantSurface: `"surface":"item type"`,
		},
		{
			name:        "custom field",
			handler:     schemaHandler(&mockTypeRepo{}, &mockFieldDefRepo{getByIDErr: errSynthetic}),
			route:       func(h *projectsapi.Handler) http.HandlerFunc { return h.UpdateCustomField },
			params:      map[string]string{"orgID": org, "fieldID": uuid.New().String()},
			wantMessage: "custom field operation failed",
			wantSurface: `"surface":"custom field"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)

			rr := httptest.NewRecorder()
			tc.route(tc.handler)(rr, req(t, http.MethodPatch, `{"name":"Renamed"}`, tc.params))

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			env := decodeWireError(t, rr)
			require.Equal(t, "INTERNAL_ERROR", env.Error.Code)
			require.Equal(t, tc.wantMessage, env.Error.Message)
			require.NotContains(t, env.Error.Message, leakMarker,
				"the constraint name reached the client")

			require.Contains(t, logs.String(), leakMarker, "the cause must still be logged")
			require.Contains(t, logs.String(), tc.wantSurface)
		})
	}
}

// Compile-time proof that the sentinels these switches DO map are unaffected by
// H5: the mapped arms still pass err.Error(), and one existing test depends on
// it (projects_domain_errors_test.go asserts "getting item" on a 404, which is
// the only thing distinguishing it from the custom-field 404). This test pins
// the boundary so a later "tidy-up" that generalises the mapped arms too fails
// here rather than there.
func TestMappedProjectErrors_StillCarryTheirOwnMessages(t *testing.T) {
	h := setupHandler() // mockItemRepo.GetByID returns projects.ErrNotFound
	rr := httptest.NewRecorder()
	h.GetItem(rr, withParam(withParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"itemID", uuid.New().String()), "spaceID", uuid.New().String()))

	require.Equal(t, http.StatusNotFound, rr.Code)
	env := decodeWireError(t, rr)
	require.Equal(t, "NOT_FOUND", env.Error.Code)
	require.Contains(t, env.Error.Message, "not found",
		"mapped arms still pass err.Error(); only the default arm was generalised")

	// Sanity: the sentinels are ours, so none of them can carry driver text.
	for _, err := range []error{
		projects.ErrNotFound, projects.ErrInvalidTransition, projects.ErrSprintActive,
		itemtypes.ErrNotFound, customfields.ErrNotFound,
	} {
		require.NotContains(t, err.Error(), "SQLSTATE")
	}
}
