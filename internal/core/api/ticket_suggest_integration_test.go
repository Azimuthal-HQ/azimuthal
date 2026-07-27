package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	coretickets "github.com/Azimuthal-HQ/azimuthal/internal/core/tickets"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/adapters"
	"github.com/Azimuthal-HQ/azimuthal/internal/db/generated"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// A1 — GET /api/v1/orgs/{orgID}/tickets/suggest, the ticket_ref typeahead.
//
// The endpoint is mounted org-scoped and OUTSIDE the admin guard, so the one
// thing standing between an ordinary member and every ticket in the
// organisation is the readable-set filter the handler passes down
// (res.ReadableSpaceIDs() → SuggestTicketRefs' `space_id = ANY(...)`). Every
// scoping test here therefore asserts on the exact set of returned ticket
// ids, never on a count, and stages a ticket in the unreadable space whose
// title matches the query exactly so a widened filter cannot hide behind a
// query that happened to match nothing.
//
// Tickets are created through the real space-scoped POST route wherever the
// API can express what the test needs. Raw SQL is used only for the three
// things the API cannot set: a specific updated_at (ordering), a specific
// ticket number (the "BEA-42" ref match), and a soft-deleted space.

// ticketSuggestRow is one wire row of the suggestion response. The json tags
// are the contract the frontend picker reads — a rename here is a breaking
// change, which is why the shape test asserts the exact key set too.
type ticketSuggestRow struct {
	Ref          string    `json:"ref"`
	TicketID     uuid.UUID `json:"ticket_id"`
	Number       int32     `json:"number"`
	Title        string    `json:"title"`
	SpaceID      uuid.UUID `json:"space_id"`
	SpaceKey     string    `json:"space_key"`
	Status       string    `json:"status"`
	AssignedToMe bool      `json:"assigned_to_me"`
}

// ticketSuggestTicket is a created ticket, reduced to what the assertions need.
type ticketSuggestTicket struct {
	ID     uuid.UUID
	Number int32
	Title  string
}

// ticketSuggestFixture is one org holding two Beacon spaces with distinct
// keys — BEA, readable by the contributor persona, and OTH, readable by
// nobody but the org admin — plus the personas of spec §2.6.
type ticketSuggestFixture struct {
	ts *testServer

	spaceA uuid.UUID // key BEA — the contributor holds a direct grant
	spaceB uuid.UUID // key OTH — no grant to anyone; admin bypass only
	keyA   string
	keyB   string

	// contrib is past the create_items write floor but holds no org role:
	// exactly the persona whose readable set is a strict subset of the org.
	contrib    testutil.User
	contribTok string

	// member is an org member with zero grants — an empty readable set.
	member    testutil.User
	memberTok string

	// strangerTok authenticates a user of a DIFFERENT org against this org.
	strangerTok string
}

func ticketSuggestNewFixture(t *testing.T) *ticketSuggestFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &ticketSuggestFixture{ts: ts}

	f.spaceA, f.keyA = ticketSuggestCreateSpace(t, ts, "Beacon Desk", "beacon-desk", "BEA")
	f.spaceB, f.keyB = ticketSuggestCreateSpace(t, ts, "Other Desk", "other-desk", "OTH")

	f.contrib = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(context.Background(), ts.OrgID, f.spaceA,
		access.SubjectUser, f.contrib.ID, access.RoleContributor, ts.UserID)
	require.NoError(t, err)
	f.contribTok = ts.tokenFor(t, f.contrib.ID, f.contrib.Email)

	f.member = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	f.memberTok = ts.tokenFor(t, f.member.ID, f.member.Email)

	otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
	stranger := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
	f.strangerTok = ts.tokenFor(t, stranger.ID, stranger.Email)

	return f
}

// ticketSuggestCreateSpace creates a Beacon space with an EXPLICIT key, so
// every composed ref in these tests is known ahead of time. The harness
// admin creates it, which (spaces.Create) deliberately seeds no creator
// grant — an org admin already reads every space through the bypass, so the
// contributor's readable set stays exactly what this fixture grants it.
func ticketSuggestCreateSpace(t *testing.T, ts *testServer, name, slug, key string) (uuid.UUID, string) {
	t.Helper()
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
		"name": name, "slug": slug, "type": "beacon", "key": key,
	}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create space %s: %s", key, r.Body)
	var space struct {
		ID  uuid.UUID `json:"id"`
		Key string    `json:"key"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &space))
	require.Equal(t, key, space.Key, "the space must keep the explicit key the ref assertions assume")
	return space.ID, space.Key
}

// ticketSuggestCreateTicket creates a ticket through the real space-scoped
// route as the harness admin, optionally assigned to someone.
func ticketSuggestCreateTicket(t *testing.T, ts *testServer, spaceID uuid.UUID, title string, assignee *uuid.UUID) ticketSuggestTicket {
	t.Helper()
	body := map[string]any{"title": title, "priority": "medium"}
	if assignee != nil {
		body["assignee_id"] = assignee.String()
	}
	r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID), body, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create ticket %q: %s", title, r.Body)
	var created struct {
		ID     uuid.UUID `json:"id"`
		Number int32     `json:"number"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &created))
	require.NotEqual(t, uuid.Nil, created.ID)
	return ticketSuggestTicket{ID: created.ID, Number: created.Number, Title: title}
}

// ticketSuggestQ encodes one q= query string.
func ticketSuggestQ(q string) string { return url.Values{"q": {q}}.Encode() }

// ticketSuggestPath builds the endpoint URL with an already-encoded query.
func ticketSuggestPath(orgID uuid.UUID, rawQuery string) string {
	if rawQuery == "" {
		return fmt.Sprintf("/api/v1/orgs/%s/tickets/suggest", orgID)
	}
	return fmt.Sprintf("/api/v1/orgs/%s/tickets/suggest?%s", orgID, rawQuery)
}

// ticketSuggestGet issues the request as the given persona and requires 200
// with the documented JSON shape, returning the decoded rows.
func ticketSuggestGet(t *testing.T, ts *testServer, token, rawQuery string) []ticketSuggestRow {
	t.Helper()
	r := ts.getAs(t, token, ticketSuggestPath(ts.OrgID, rawQuery))
	require.Equal(t, http.StatusOK, r.StatusCode, "suggest %q: %s", rawQuery, r.Body)
	require.Contains(t, r.ContentType, "application/json")
	requireSnakeCaseKeys(t, r.Body)
	var rows []ticketSuggestRow
	require.NoError(t, json.Unmarshal(r.Body, &rows), "body: %s", r.Body)
	return rows
}

// ticketSuggestIDs reduces rows to the ticket ids, in order.
func ticketSuggestIDs(rows []ticketSuggestRow) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.TicketID)
	}
	return out
}

// ticketSuggestSetUpdatedAt forces a ticket's updated_at. The API has no way
// to write that column to a chosen instant, and the ordering test is only
// meaningful with instants it chose — tickets carry no updated_at trigger
// (migration 009 installs one on items/pages/spaces, not tickets), so this
// UPDATE sticks.
func ticketSuggestSetUpdatedAt(t *testing.T, ts *testServer, ticketID uuid.UUID, at time.Time) {
	t.Helper()
	tag, err := ts.DB.Pool.Exec(context.Background(),
		`UPDATE tickets SET updated_at = $2 WHERE id = $1`, ticketID, at)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())
}

// ticketSuggestService builds the suggestion service over the same real
// schema the HTTP server uses, wired exactly as newTestServer wires it. It
// exists for the one assertion HTTP cannot make: passing a readable set that
// CONTAINS a soft-deleted space, which the resolver would never produce.
func ticketSuggestService(ts *testServer) *coretickets.SuggestionService {
	return coretickets.NewSuggestionService(adapters.NewTicketAdapter(generated.New(ts.DB.Pool)))
}

// --- 1. Read scoping ---

// TestTicketSuggest_ScopedToCallersReadableSpaces pins the security property
// the endpoint exists to keep: a contributor granted on BEA never sees a
// ticket in OTH, at ANY q. The OTH ticket carries the IDENTICAL title, so a
// widened space filter returns it for every query below.
func TestTicketSuggest_ScopedToCallersReadableSpaces(t *testing.T) {
	f := ticketSuggestNewFixture(t)

	readable := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Payment gateway timeout", nil)
	hidden := ticketSuggestCreateTicket(t, f.ts, f.spaceB, "Payment gateway timeout", nil)
	require.NotEqual(t, readable.ID, hidden.ID)

	// Premise: both tickets exist and are both matchable — the admin sees the
	// pair. Without this, an empty contributor result could mean "the second
	// ticket was never created" rather than "the filter held".
	adminIDs := ticketSuggestIDs(ticketSuggestGet(t, f.ts, f.ts.Token, ticketSuggestQ("Payment gateway timeout")))
	require.ElementsMatch(t, []uuid.UUID{readable.ID, hidden.ID}, adminIDs,
		"premise: both tickets match the query for a caller who can read both spaces")

	// Every query shape: unfiltered, title words in either case, the bare
	// number both tickets share, and the ref of the ticket in the space the
	// contributor cannot read.
	for _, q := range []string{
		"",
		"Payment",
		"gateway",
		"TIMEOUT",
		"1",
		f.keyB + "-" + fmt.Sprint(hidden.Number),
	} {
		t.Run("q="+q, func(t *testing.T) {
			rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ(q))
			for _, row := range rows {
				require.Equal(t, f.spaceA, row.SpaceID,
					"a suggestion escaped the caller's readable set: %+v", row)
				require.Equal(t, f.keyA, row.SpaceKey)
			}
			require.NotContains(t, ticketSuggestIDs(rows), hidden.ID,
				"the ticket in the unreadable space must never be suggested")
		})
	}

	// And the readable one is genuinely reachable — the loop above would also
	// pass if the endpoint answered nothing at all.
	rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("Payment"))
	require.Equal(t, []uuid.UUID{readable.ID}, ticketSuggestIDs(rows))
	require.Equal(t, f.keyA+"-"+fmt.Sprint(readable.Number), rows[0].Ref)
}

// --- 2. Empty readable set ---

// TestTicketSuggest_NoReadableSpaces_ReturnsEmptyJSONArray: an org member
// with no grants gets 200 and the literal wire value [] — not null, not an
// error. The picker calls .map on this body; a null is a runtime crash.
func TestTicketSuggest_NoReadableSpaces_ReturnsEmptyJSONArray(t *testing.T) {
	f := ticketSuggestNewFixture(t)
	live := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Grantless probe ticket", nil)

	// Premise: the ticket exists and matches, so [] below is the empty
	// readable set and not an empty database.
	require.Equal(t, []uuid.UUID{live.ID},
		ticketSuggestIDs(ticketSuggestGet(t, f.ts, f.ts.Token, ticketSuggestQ("Grantless"))),
		"premise: the ticket is matchable for a caller who can read its space")

	for _, raw := range []string{
		"",                          // no q parameter at all
		ticketSuggestQ(""),          // explicit empty q
		ticketSuggestQ("Grantless"), // a q that matches a real ticket
		ticketSuggestQ(f.keyA + "-" + fmt.Sprint(live.Number)), // and its exact ref
	} {
		r := f.ts.getAs(t, f.memberTok, ticketSuggestPath(f.ts.OrgID, raw))
		require.Equal(t, http.StatusOK, r.StatusCode, "query %q: %s", raw, r.Body)
		require.Contains(t, r.ContentType, "application/json")
		require.Equal(t, "[]", strings.TrimSpace(string(r.Body)),
			"an empty readable set must serialise as [], never null: query %q", raw)
	}
}

// --- 3. Org-admin bypass ---

// TestTicketSuggest_OrgAdminSeesAcrossSpaces: the bypass fills the readable
// set with every live space, so an admin's suggestions span both spaces —
// including the one that carries no grant rows at all.
func TestTicketSuggest_OrgAdminSeesAcrossSpaces(t *testing.T) {
	f := ticketSuggestNewFixture(t)

	inA := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Crossspace probe alpha", nil)
	inB := ticketSuggestCreateTicket(t, f.ts, f.spaceB, "Crossspace probe bravo", nil)

	rows := ticketSuggestGet(t, f.ts, f.ts.Token, ticketSuggestQ("Crossspace probe"))
	require.ElementsMatch(t, []uuid.UUID{inA.ID, inB.ID}, ticketSuggestIDs(rows))

	byID := map[uuid.UUID]ticketSuggestRow{}
	for _, row := range rows {
		byID[row.TicketID] = row
	}
	require.Equal(t, f.spaceA, byID[inA.ID].SpaceID)
	require.Equal(t, f.keyA, byID[inA.ID].SpaceKey)
	require.Equal(t, f.spaceB, byID[inB.ID].SpaceID)
	require.Equal(t, f.keyB, byID[inB.ID].SpaceKey,
		"the admin bypass must reach a space with no grant rows")

	// Same call as a persona whose grant covers only BEA: the bypass, not the
	// endpoint, is what widened the set above.
	require.Equal(t, []uuid.UUID{inA.ID},
		ticketSuggestIDs(ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("Crossspace probe"))))
}

// --- 4. Ordering ---

// TestTicketSuggest_OrdersAssignedToCallerFirstThenRecency stages the caller's
// own assignment as the OLDEST row by updated_at, and gives the other three
// an updated_at order that matches neither the order they were created in nor
// its reverse. The exact sequence asserted below is therefore reachable only
// by "assigned to the caller first, then updated_at DESC" — it is not heap
// order, not created_at in either direction, not recency alone, and not what
// dropping the COALESCE produces (a NULL assignee comparison under DESC
// NULLS FIRST floats every unassigned row to the top).
func TestTicketSuggest_OrdersAssignedToCallerFirstThenRecency(t *testing.T) {
	f := ticketSuggestNewFixture(t)
	now := time.Now()

	// Creation order: mine, older, newest, theirs.
	mine := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Ordering probe mine", &f.contrib.ID)
	older := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Ordering probe older", nil)
	newest := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Ordering probe newest", nil)
	theirs := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Ordering probe theirs", &f.ts.UserID)

	ticketSuggestSetUpdatedAt(t, f.ts, mine.ID, now.Add(-72*time.Hour))   // oldest of all
	ticketSuggestSetUpdatedAt(t, f.ts, older.ID, now.Add(-24*time.Hour))  //
	ticketSuggestSetUpdatedAt(t, f.ts, newest.ID, now.Add(-2*time.Hour))  // newest of all
	ticketSuggestSetUpdatedAt(t, f.ts, theirs.ID, now.Add(-48*time.Hour)) //

	// Expected: the caller's own assignment first despite being the oldest,
	// then the rest strictly by recency.
	//   creation order        [mine, older, newest, theirs]
	//   created_at DESC       [theirs, newest, older, mine]
	//   recency alone         [newest, older, theirs, mine]
	//   COALESCE dropped      [newest, older, mine, theirs]
	want := []uuid.UUID{mine.ID, newest.ID, older.ID, theirs.ID}

	for _, q := range []string{"Ordering probe", ""} {
		t.Run("q="+q, func(t *testing.T) {
			rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ(q))
			require.Equal(t, want, ticketSuggestIDs(rows),
				"assigned-to-caller first, then most recently updated")

			// assigned_to_me is caller-relative: the ticket assigned to the
			// ADMIN is not the caller's, and neither are the unassigned ones.
			require.True(t, rows[0].AssignedToMe, "the caller's own assignment reports assigned_to_me")
			for _, row := range rows[1:] {
				require.False(t, row.AssignedToMe,
					"assigned_to_me must be false for tickets assigned to someone else or nobody: %+v", row)
			}
		})
	}
}

// --- 5. Query matching ---

// TestTicketSuggest_QueryMatching covers the three shapes an operator types:
// part of a title (case-insensitively), the composed ref in either case, and
// the bare number. A decoy ticket in the same readable space fails the test
// if the q filter stops filtering.
func TestTicketSuggest_QueryMatching(t *testing.T) {
	f := ticketSuggestNewFixture(t)

	decoy := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Unrelated chore", nil)
	target := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Payment gateway timeout", nil)

	// The API assigns ticket numbers sequentially and offers no way to ask
	// for 42, so the number is forced here — everything else about the
	// ticket came through the real create path.
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE tickets SET number = 42 WHERE id = $1`, target.ID)
	require.NoError(t, err)
	target.Number = 42

	require.Equal(t, int32(1), decoy.Number, "the decoy's ref must not contain '42' or 'bea-42'")

	cases := []struct {
		name string
		q    string
		want []uuid.UUID
	}{
		{"title_substring", "gateway", []uuid.UUID{target.ID}},
		{"title_substring_uppercase_is_case_insensitive", "GATEWAY", []uuid.UUID{target.ID}},
		{"title_substring_mixed_case", "PaYmEnT", []uuid.UUID{target.ID}},
		{"composed_ref", "BEA-42", []uuid.UUID{target.ID}},
		{"composed_ref_lowercase", "bea-42", []uuid.UUID{target.ID}},
		{"bare_number", "42", []uuid.UUID{target.ID}},
		{"other_bare_number_selects_the_other_ticket", "1", []uuid.UUID{decoy.ID}},
		{"decoy_title", "chore", []uuid.UUID{decoy.ID}},
		{"empty_q_returns_everything_readable", "", []uuid.UUID{target.ID, decoy.ID}},
		{"no_match", "quokka", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ(tc.q))
			require.ElementsMatch(t, tc.want, ticketSuggestIDs(rows), "q=%q", tc.q)
		})
	}

	// The composed ref is exactly space key + "-" + number.
	rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("bea-42"))
	require.Len(t, rows, 1)
	require.Equal(t, "BEA-42", rows[0].Ref)
	require.Equal(t, int32(42), rows[0].Number)

	// A ref whose key belongs to another space must not match this ticket:
	// the query composes the ref from the JOINED space, not from any space.
	require.Empty(t, ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("OTH-42")))
}

// TestTicketSuggest_WildcardCharactersAreLiteral pins S11.
//
// The query text is bound as a parameter and then wrapped in '%' || … || '%'
// inside the SQL, so it was never an injection risk — but until S11 nothing
// escaped the caller's own `%` and `_`, and ILIKE read them as wildcards. A
// user typing a single `%` matched every ticket they could read, and `_`
// matched any character, so a search for "a_b" also returned "axb".
//
// Verified fails-before: with the replace() escaping removed from
// SuggestTicketRefs, the percent, underscore, and underscore_is_not_any_char
// subtests all fail.
func TestTicketSuggest_WildcardCharactersAreLiteral(t *testing.T) {
	f := ticketSuggestNewFixture(t)

	plain := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Ordinary ticket", nil)
	pct := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Discount 50% off", nil)
	under := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "snake_case naming", nil)
	axb := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "axb collision probe", nil)

	t.Run("percent_matches_only_the_literal_percent", func(t *testing.T) {
		rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("%"))
		require.ElementsMatch(t, []uuid.UUID{pct.ID}, ticketSuggestIDs(rows),
			"a bare %% must match the ticket containing a literal %%, not every readable ticket")
	})

	t.Run("underscore_matches_only_the_literal_underscore", func(t *testing.T) {
		rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("_"))
		require.ElementsMatch(t, []uuid.UUID{under.ID}, ticketSuggestIDs(rows),
			"a bare _ must match the ticket containing a literal _, not every readable ticket")
	})

	t.Run("underscore_is_not_any_char", func(t *testing.T) {
		rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("a_b"))
		require.Empty(t, ticketSuggestIDs(rows),
			"a_b must not match 'axb' — _ is a literal, not a single-character wildcard")
	})

	t.Run("backslash_is_literal_too", func(t *testing.T) {
		// The escape character itself must be escaped, or a trailing backslash
		// produces a malformed pattern and Postgres errors the whole query.
		rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ(`\`))
		require.Empty(t, ticketSuggestIDs(rows),
			"a lone backslash must be a literal that matches nothing here, not a dangling escape")
	})

	// The negative guard: ordinary substring search must still work, or every
	// assertion above would be satisfied by a query that matches nothing.
	t.Run("ordinary_substring_still_matches", func(t *testing.T) {
		rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("Ordinary"))
		require.ElementsMatch(t, []uuid.UUID{plain.ID}, ticketSuggestIDs(rows))
	})

	t.Run("empty_query_still_returns_everything_readable", func(t *testing.T) {
		rows := ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ(""))
		require.ElementsMatch(t,
			[]uuid.UUID{plain.ID, pct.ID, under.ID, axb.ID},
			ticketSuggestIDs(rows))
	})
}

// --- 6. Soft deletes ---

// TestTicketSuggest_ExcludesSoftDeleted covers both halves of the liveness
// filter: a deleted ticket, and a live ticket whose SPACE is deleted.
//
// The second half is asserted twice on purpose. Over HTTP the resolver
// already drops a deleted space from the readable set, so that path alone
// would still pass with the query's `s.deleted_at IS NULL` removed. The
// service-level call underneath hands the query a readable set that
// explicitly contains the deleted space — the only way to reach the JOIN
// guard, and the assertion that fails if it is dropped.
func TestTicketSuggest_ExcludesSoftDeleted(t *testing.T) {
	f := ticketSuggestNewFixture(t)

	spaceC, _ := ticketSuggestCreateSpace(t, f.ts, "Doomed Desk", "doomed-desk", "GON")
	_, err := f.ts.GrantService.Create(context.Background(), f.ts.OrgID, spaceC,
		access.SubjectUser, f.contrib.ID, access.RoleContributor, f.ts.UserID)
	require.NoError(t, err)

	live := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Liveness probe survivor", nil)
	doomed := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Liveness probe deleted ticket", nil)
	inDoomedSpace := ticketSuggestCreateTicket(t, f.ts, spaceC, "Liveness probe deleted space", nil)

	// Premise: with everything live, all three are suggested to the
	// contributor — so each disappearance below is caused by the delete.
	require.ElementsMatch(t, []uuid.UUID{live.ID, doomed.ID, inDoomedSpace.ID},
		ticketSuggestIDs(ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("Liveness probe"))),
		"premise: all three tickets are readable while live")

	// Ticket soft-delete through the real route.
	dr := f.ts.delete(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets/%s", f.ts.OrgID, f.spaceA, doomed.ID), true)
	require.Equal(t, http.StatusNoContent, dr.StatusCode, "delete ticket: %s", dr.Body)

	// Space soft-delete by hand: the DELETE route also soft-deletes the
	// space's contents, and this case needs a LIVE ticket in a DEAD space.
	tag, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE spaces SET deleted_at = now() WHERE id = $1`, spaceC)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected())

	var stillLive bool
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NULL FROM tickets WHERE id = $1`, inDoomedSpace.ID).Scan(&stillLive))
	require.True(t, stillLive, "premise: the ticket in the deleted space is itself still live")

	// Over HTTP: only the survivor remains, for the contributor and the admin.
	for name, token := range map[string]string{"contributor": f.contribTok, "org_admin": f.ts.Token} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, []uuid.UUID{live.ID},
				ticketSuggestIDs(ticketSuggestGet(t, f.ts, token, ticketSuggestQ("Liveness probe"))))
		})
	}

	// Underneath HTTP, with the deleted space FORCED into the readable set.
	t.Run("deleted_space_in_an_explicit_readable_set", func(t *testing.T) {
		got, err := ticketSuggestService(f.ts).Suggest(context.Background(), coretickets.SuggestParams{
			ReadableSpaceIDs: []uuid.UUID{f.spaceA, spaceC},
			CallerID:         f.contrib.ID,
			Query:            "Liveness probe",
		})
		require.NoError(t, err)
		ids := make([]uuid.UUID, 0, len(got))
		for _, s := range got {
			ids = append(ids, s.TicketID)
		}
		require.Equal(t, []uuid.UUID{live.ID}, ids,
			"a live ticket in a soft-deleted space must stay hidden even when that space id is readable")
	})
}

// --- 7. Bounded result set ---

// TestTicketSuggest_ResultSetIsBoundedAt20 proves the cap is in the query and
// that no client parameter lifts it: the endpoint reads only q, so limit,
// per_page and friends are inert.
func TestTicketSuggest_ResultSetIsBoundedAt20(t *testing.T) {
	f := ticketSuggestNewFixture(t)

	const created = 25
	for i := 0; i < created; i++ {
		ticketSuggestCreateTicket(t, f.ts, f.spaceA, fmt.Sprintf("Cap probe %02d", i), nil)
	}

	// Premise: more rows exist than the cap allows, so a length of 20 below
	// is the LIMIT and not the size of the table.
	var live int
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM tickets WHERE space_id = $1 AND deleted_at IS NULL`, f.spaceA).Scan(&live))
	require.Equal(t, created, live, "premise: 25 matchable tickets exist")

	queries := []string{
		ticketSuggestQ(""),
		ticketSuggestQ("Cap probe"),
		ticketSuggestQ("Cap probe") + "&limit=100",
		ticketSuggestQ("Cap probe") + "&per_page=100",
		ticketSuggestQ("Cap probe") + "&page_size=100&offset=0",
		ticketSuggestQ("Cap probe") + "&count=100&max=100&size=100",
	}
	for name, token := range map[string]string{"contributor": f.contribTok, "org_admin": f.ts.Token} {
		t.Run(name, func(t *testing.T) {
			for _, raw := range queries {
				rows := ticketSuggestGet(t, f.ts, token, raw)
				require.Len(t, rows, 20, "the typeahead is capped at 20 regardless of %q", raw)
				seen := map[uuid.UUID]bool{}
				for _, row := range rows {
					require.False(t, seen[row.TicketID], "duplicate suggestion %s", row.TicketID)
					seen[row.TicketID] = true
				}
			}
		})
	}
}

// --- 8. Endpoint matrix (spec §2.6) ---

// TestTicketSuggest_EndpointMatrix: 401 without credentials, 404 for a
// non-member of the org (existence is never leaked), and 200 for any org
// member — the route is deliberately outside the admin guard.
func TestTicketSuggest_EndpointMatrix(t *testing.T) {
	f := ticketSuggestNewFixture(t)
	ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Matrix probe ticket", nil)
	path := ticketSuggestPath(f.ts.OrgID, ticketSuggestQ("Matrix probe"))

	// No credentials → 401 with the documented envelope.
	unauth := f.ts.getAs(t, "", path)
	require.Equal(t, http.StatusUnauthorized, unauth.StatusCode, "body: %s", unauth.Body)
	require.Contains(t, unauth.ContentType, "application/json")
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(unauth.Body, &errBody))
	require.Equal(t, "UNAUTHORIZED", errBody.Error.Code)

	// A member of another org → 404 from the org group, same as every other
	// org-scoped route (member_search, admin surface).
	requireAPINotFound(t, f.ts.getAs(t, f.strangerTok, path))

	// A plain org member → 200, empty because they hold no grants.
	memberRows := ticketSuggestGet(t, f.ts, f.memberTok, ticketSuggestQ("Matrix probe"))
	require.Empty(t, memberRows)

	// A contributor and the admin → 200 with the ticket.
	require.Len(t, ticketSuggestGet(t, f.ts, f.contribTok, ticketSuggestQ("Matrix probe")), 1)
	require.Len(t, ticketSuggestGet(t, f.ts, f.ts.Token, ticketSuggestQ("Matrix probe")), 1)

	// An unparseable org id is a 400 from the resolution middleware, not a 500.
	bad := f.ts.getAs(t, f.contribTok, "/api/v1/orgs/not-a-uuid/tickets/suggest?q=x")
	require.Equal(t, http.StatusBadRequest, bad.StatusCode, "body: %s", bad.Body)
}

// --- 9. Wire shape ---

// TestTicketSuggest_ResponseShape pins the exact key set and every value the
// picker reads. Tickets carry no key column, so `ref` is composed — this is
// the assertion that catches a drift between ComposeRef and the row.
func TestTicketSuggest_ResponseShape(t *testing.T) {
	f := ticketSuggestNewFixture(t)
	created := ticketSuggestCreateTicket(t, f.ts, f.spaceA, "Shape probe ticket", &f.contrib.ID)

	r := f.ts.getAs(t, f.contribTok, ticketSuggestPath(f.ts.OrgID, ticketSuggestQ("Shape probe")))
	require.Equal(t, http.StatusOK, r.StatusCode, "body: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)

	var raw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(r.Body, &raw))
	require.Len(t, raw, 1)
	keys := make([]string, 0, len(raw[0]))
	for k := range raw[0] {
		keys = append(keys, k)
	}
	require.ElementsMatch(t,
		[]string{"ref", "ticket_id", "number", "title", "space_id", "space_key", "status", "assigned_to_me"},
		keys, "the typeahead wire contract is these eight keys exactly")

	var rows []ticketSuggestRow
	require.NoError(t, json.Unmarshal(r.Body, &rows))
	require.Equal(t, ticketSuggestRow{
		Ref:          fmt.Sprintf("%s-%d", f.keyA, created.Number),
		TicketID:     created.ID,
		Number:       created.Number,
		Title:        "Shape probe ticket",
		SpaceID:      f.spaceA,
		SpaceKey:     f.keyA,
		Status:       "open",
		AssignedToMe: true,
	}, rows[0])
}
