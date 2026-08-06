package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// A4 — GET /api/v1/orgs/{orgID}/pages/suggest, the page-picker typeahead.
//
// The endpoint mirrors /tickets/suggest and inherits its threat model: it is
// mounted org-scoped and outside the admin guard, so the one thing standing
// between an ordinary member and every page title in the organisation is the
// readable-set filter the handler passes down (res.ReadableSpaceIDs() →
// SuggestPages' `space_id = ANY(...)`). Every scoping test asserts on the
// exact set of returned page ids, never on a count, and stages a page in the
// unreadable space whose title matches the query exactly so a widened filter
// cannot hide behind a query that happened to match nothing.

// pageSuggestRow is one wire row of the suggestion response. The json tags
// are the contract the frontend picker reads.
type pageSuggestRow struct {
	PageID    uuid.UUID `json:"page_id"`
	Title     string    `json:"title"`
	SpaceID   uuid.UUID `json:"space_id"`
	SpaceKey  string    `json:"space_key"`
	SpaceName string    `json:"space_name"`
}

type pageSuggestFixture struct {
	ts *testServer

	spaceA testutil.Space // the contributor holds a direct grant
	spaceB testutil.Space // no grant to anyone; admin bypass only

	contrib    testutil.User
	contribTok string
	member     testutil.User
	memberTok  string
}

func pageSuggestNewFixture(t *testing.T) *pageSuggestFixture {
	t.Helper()
	ts := newTestServer(t)
	f := &pageSuggestFixture{ts: ts}

	f.spaceA = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")
	f.spaceB = testutil.CreateTestSpace(t, ts.DB.Pool, ts.OrgID, ts.UserID, "codex")

	f.contrib = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	_, err := ts.GrantService.Create(context.Background(), ts.OrgID, f.spaceA.ID,
		access.SubjectUser, f.contrib.ID, access.RoleContributor, ts.UserID)
	require.NoError(t, err)
	f.contribTok = ts.tokenFor(t, f.contrib.ID, f.contrib.Email)

	// member holds no grants at all — the empty readable set.
	f.member = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	f.memberTok = ts.tokenFor(t, f.member.ID, f.member.Email)

	return f
}

func (f *pageSuggestFixture) mkPage(t *testing.T, spaceID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, f.ts.DB.Pool.QueryRow(context.Background(),
		`INSERT INTO pages (id, space_id, title, content, author_id, path)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		uuid.New(), spaceID, title, title+"-body", f.ts.UserID,
		"/"+strings.ToLower(strings.ReplaceAll(title, " ", "-"))+"-"+uuid.NewString()[:8]).Scan(&id))
	return id
}

func pageSuggestPath(orgID uuid.UUID, q string) string {
	if q == "" {
		return fmt.Sprintf("/api/v1/orgs/%s/pages/suggest", orgID)
	}
	return fmt.Sprintf("/api/v1/orgs/%s/pages/suggest?%s", orgID, url.Values{"q": {q}}.Encode())
}

func pageSuggestGet(t *testing.T, ts *testServer, token, q string) []pageSuggestRow {
	t.Helper()
	r := ts.getAs(t, token, pageSuggestPath(ts.OrgID, q))
	require.Equal(t, http.StatusOK, r.StatusCode, "suggest %q: %s", q, r.Body)
	requireSnakeCaseKeys(t, r.Body)
	var rows []pageSuggestRow
	require.NoError(t, json.Unmarshal(r.Body, &rows), "body: %s", r.Body)
	return rows
}

func pageSuggestIDs(rows []pageSuggestRow) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.PageID)
	}
	return out
}

// TestPageSuggest_ScopedToCallersReadableSpaces pins the security property the
// endpoint exists to keep: a contributor granted on space A never sees a page
// in space B, at any q. The space-B page carries the IDENTICAL title, so a
// widened space filter returns it for every query below.
func TestPageSuggest_ScopedToCallersReadableSpaces(t *testing.T) {
	f := pageSuggestNewFixture(t)

	readable := f.mkPage(t, f.spaceA.ID, "Deployment runbook")
	f.mkPage(t, f.spaceB.ID, "Deployment runbook")

	for _, q := range []string{"", "Deployment", "runbook", "deployment runbook"} {
		t.Run("q="+q, func(t *testing.T) {
			rows := pageSuggestGet(t, f.ts, f.contribTok, q)
			require.Equal(t, []uuid.UUID{readable}, pageSuggestIDs(rows),
				"only the readable space's page may appear")
		})
	}

	t.Run("org admin sees both through the bypass", func(t *testing.T) {
		rows := pageSuggestGet(t, f.ts, f.ts.Token, "Deployment")
		require.Len(t, rows, 2, "the admin's resolved set covers every live space")
	})
}

// TestPageSuggest_NoReadableSpaces_ReturnsEmptyJSONArray covers the empty-set
// short-circuit: a member with zero grants gets [], not null and not an error.
func TestPageSuggest_NoReadableSpaces_ReturnsEmptyJSONArray(t *testing.T) {
	f := pageSuggestNewFixture(t)
	f.mkPage(t, f.spaceA.ID, "Only page anywhere")

	r := f.ts.getAs(t, f.memberTok, pageSuggestPath(f.ts.OrgID, "page"))
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Equal(t, "[]", strings.TrimSpace(string(r.Body)),
		"an empty readable set answers an empty ARRAY, so clients need no null branch")
}

// TestPageSuggest_WildcardCharactersAreLiteral pins the ILIKE escaping: `%`
// and `_` in the operator's text are text, not patterns. A query of "100%"
// must not match "100 pages", and "a_b" must not match "aXb" — the exact bug
// the SuggestTicketRefs escape shape exists to prevent.
func TestPageSuggest_WildcardCharactersAreLiteral(t *testing.T) {
	f := pageSuggestNewFixture(t)

	literalPercent := f.mkPage(t, f.spaceA.ID, "Coverage at 100% milestone")
	f.mkPage(t, f.spaceA.ID, "Coverage at 100 pages milestone")
	literalUnderscore := f.mkPage(t, f.spaceA.ID, "the a_b convention")
	f.mkPage(t, f.spaceA.ID, "the aXb convention")

	t.Run("percent is literal", func(t *testing.T) {
		rows := pageSuggestGet(t, f.ts, f.contribTok, "100%")
		require.Equal(t, []uuid.UUID{literalPercent}, pageSuggestIDs(rows),
			"a bare %% in the query must match only a literal %%")
	})

	t.Run("underscore is literal", func(t *testing.T) {
		rows := pageSuggestGet(t, f.ts, f.contribTok, "a_b")
		require.Equal(t, []uuid.UUID{literalUnderscore}, pageSuggestIDs(rows),
			"a bare _ in the query must not act as a single-character wildcard")
	})
}

// TestPageSuggest_ExcludesSoftDeleted: a deleted page stops being suggested,
// exactly as it stops being readable anywhere else.
func TestPageSuggest_ExcludesSoftDeleted(t *testing.T) {
	f := pageSuggestNewFixture(t)

	kept := f.mkPage(t, f.spaceA.ID, "Kept page")
	gone := f.mkPage(t, f.spaceA.ID, "Gone page")
	_, err := f.ts.DB.Pool.Exec(context.Background(),
		`UPDATE pages SET deleted_at = now() WHERE id = $1`, gone)
	require.NoError(t, err)

	rows := pageSuggestGet(t, f.ts, f.contribTok, "page")
	require.Equal(t, []uuid.UUID{kept}, pageSuggestIDs(rows))
}

// TestPageSuggest_ResultSetIsBoundedAt20 pins the server-side limit. The bound
// must live in the query, not the client: 25 matching pages exist and exactly
// 20 come back, so the endpoint cannot be driven as a bulk export of every
// title the caller can read.
func TestPageSuggest_ResultSetIsBoundedAt20(t *testing.T) {
	f := pageSuggestNewFixture(t)

	for i := range 25 {
		f.mkPage(t, f.spaceA.ID, fmt.Sprintf("Bounded page %02d", i))
	}

	rows := pageSuggestGet(t, f.ts, f.contribTok, "Bounded")
	require.Len(t, rows, 20, "the LIMIT is the server's, not the client's")
}
