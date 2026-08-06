package tickets_test

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

	ticketsapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
)

// This is the tickets half of the change the hygiene pass (H5) made to the
// three project surfaces. known-issues #23 was filed against THIS arm — the
// blockquote in that entry records the projects ones as closed and says
// handleTicketError "remains exactly as described above" — so the shape of the
// test follows internal/core/api/projects/unmapped_error_test.go deliberately
// rather than inventing a second idiom for the same guarantee.

// ticketLeakMarker exists nowhere else in the repository. If it appears in a
// response body, internal detail crossed the wire.
//
// The wording reproduces the real disclosure #23 recorded: a well-formed uuid
// naming no user reached the UPDATE, violated tickets_assignee_id_fkey, and the
// driver's sentence — table name, constraint name and SQLSTATE — was returned
// to any caller holding edit_any_item.
const ticketLeakMarker = "tickets_assignee_id_leak_marker_fkey"

var errTicketSynthetic = errors.New(
	`ERROR: insert or update on table "tickets" violates foreign key constraint "` +
		ticketLeakMarker + `" (SQLSTATE 23503)`)

// failingTicketRepo answers the scoped read with an error no arm of
// handleTicketError maps, which is the only way to reach the default arm.
//
// Every existing ticket mock returns nil or tickets.ErrNotFound, so no test
// could reach that arm at all — which is why the leak outlived the pass that
// closed its three siblings.
type failingTicketRepo struct {
	mockTicketRepo
}

func (f *failingTicketRepo) GetByIDInSpace(context.Context, uuid.UUID, uuid.UUID) (*tickets.Ticket, error) {
	return nil, errTicketSynthetic
}

func (f *failingTicketRepo) GetByID(context.Context, uuid.UUID) (*tickets.Ticket, error) {
	return nil, errTicketSynthetic
}

func handlerWithFailingTickets() *ticketsapi.Handler {
	return ticketsapi.NewHandler(
		tickets.NewTicketService(&failingTicketRepo{}, noopShareDeleter{}),
		nil,
	)
}

func captureTicketLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

type ticketWireError struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func getTicketThroughFailingRepo(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	h := handlerWithFailingTickets()
	req := withChiParam(withChiParam(httptest.NewRequest(http.MethodGet, "/", nil),
		"ticketID", uuid.New().String()), "spaceID", uuid.New().String())
	rr := httptest.NewRecorder()
	h.Get(rr, req)
	return rr
}

// Fails before the fix: the default arm was
// `fmt.Sprintf("ticket operation failed: %v", err)`, so the body read
// `ticket operation failed: getting ticket: ERROR: insert or update on table
// "tickets" violates foreign key constraint "..." (SQLSTATE 23503)` and every
// NotContains below fails.
//
// It asserts absence rather than one exact string, following the projects test
// and wiki_error_classes_integration_test.go: rewording the message must not
// silently stop testing anything, but a message that starts carrying the
// driver's text again must fail.
func TestUnmappedTicketError_DoesNotLeakInternalDetailToTheWire(t *testing.T) {
	rr := getTicketThroughFailingRepo(t)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var env ticketWireError
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env), "body was %q", rr.Body.String())
	require.Equal(t, "INTERNAL_ERROR", env.Error.Code)

	require.NotContains(t, env.Error.Message, ticketLeakMarker,
		"the constraint name reached the client — the default arm is interpolating the error again")
	require.NotContains(t, strings.ToLower(env.Error.Message), "constraint",
		"the client must not learn that a constraint was involved")
	require.NotContains(t, strings.ToLower(env.Error.Message), "tickets_",
		"a schema object name must not reach the client")
	require.NotContains(t, env.Error.Message, "SQLSTATE",
		"driver vocabulary must not reach the client")
	require.NotContains(t, env.Error.Message, "getting ticket",
		"internal call-stack wording must not reach the client")

	require.Equal(t, "ticket operation failed", env.Error.Message)
}

// The other half of the trade. Suppressing the detail on the wire is only
// correct if the detail survives somewhere an operator can reach it, so this
// fails if the error is dropped rather than moved.
func TestUnmappedTicketError_FullErrorReachesTheServerLog(t *testing.T) {
	logs := captureTicketLogs(t)

	rr := getTicketThroughFailingRepo(t)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	require.Contains(t, logs.String(), ticketLeakMarker,
		"the underlying error must reach the server log, or the detail was discarded rather than moved")
	require.Contains(t, logs.String(), "unmapped handler error",
		"the log line must be identifiable as the unmapped-error arm")
	require.Contains(t, logs.String(), `"surface":"ticket"`,
		"the log line must name which surface produced it")
}
