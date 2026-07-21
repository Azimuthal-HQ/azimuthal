package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/access"
	coreteams "github.com/Azimuthal-HQ/azimuthal/internal/core/teams"
	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// Permission matrix (spec §2.5) at the API layer. The denial cases (4, 5,
// 9, 10) are proven positively — a 404 status with the API error shape, or
// an exactly-equal directory id set — never by the mere non-appearance of a
// fragment that could be missing for other reasons.

// apiMatrix builds the standard tree (eng > platform, eng > design) with an
// org-admin owner, a vp in eng, a dev in platform, and a designer in design.
type apiMatrix struct {
	ts *testServer

	eng, platform, design coreteams.Team

	vp, dev, designer        testutil.User
	vpTok, devTok, designTok string
	spaceID                  string
}

func newAPIMatrix(t *testing.T) *apiMatrix {
	t.Helper()
	ts := newTestServer(t)
	ctx := context.Background()

	m := &apiMatrix{ts: ts}
	mk := func(parent *uuid.UUID, slug string) coreteams.Team {
		team, err := ts.TeamService.Create(ctx, ts.OrgID, parent, slug, slug, "")
		require.NoError(t, err)
		return team
	}
	m.eng = mk(nil, "eng")
	m.platform = mk(&m.eng.ID, "platform")
	m.design = mk(&m.eng.ID, "design")

	m.vp = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	m.dev = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
	m.designer = testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")

	join := func(team coreteams.Team, u testutil.User) {
		_, err := ts.TeamService.AddMember(ctx, team.ID, u.ID, ts.OrgID, "member")
		require.NoError(t, err)
	}
	join(m.eng, m.vp)
	join(m.platform, m.dev)
	join(m.design, m.designer)

	m.vpTok = ts.tokenFor(t, m.vp.ID, m.vp.Email)
	m.devTok = ts.tokenFor(t, m.dev.ID, m.dev.Email)
	m.designTok = ts.tokenFor(t, m.designer.ID, m.designer.Email)

	m.spaceID = createScopedSpace(t, ts, "Matrix Space", "matrix-space", "vector")
	return m
}

func (m *apiMatrix) getAs(t *testing.T, token, path string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, m.ts.url(path), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	return m.ts.do(t, req)
}

func (m *apiMatrix) grantTeam(t *testing.T, team coreteams.Team, role access.Role) {
	t.Helper()
	spaceUUID, err := uuid.Parse(m.spaceID)
	require.NoError(t, err)
	_, err = m.ts.GrantService.Create(context.Background(), m.ts.OrgID, spaceUUID,
		access.SubjectTeam, team.ID, role, m.ts.UserID)
	require.NoError(t, err)
}

// requireAPINotFound asserts the positive denial shape: HTTP 404 with the
// documented error envelope — not a blank page, not a 403, not an empty 200.
func requireAPINotFound(t *testing.T, r httpResult) {
	t.Helper()
	require.Equal(t, http.StatusNotFound, r.StatusCode, "denial must be 404, got body: %s", r.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), "denial must carry the API error envelope")
	require.Equal(t, "NOT_FOUND", body.Error.Code)
}

// directoryIDs fetches the directory as the given user and returns the
// exact id set (readable and locked separately).
func (m *apiMatrix) directoryIDs(t *testing.T, token string) (readable, locked []string) {
	t.Helper()
	r := m.getAs(t, token, fmt.Sprintf("/api/v1/orgs/%s/spaces", m.ts.OrgID))
	require.Equal(t, http.StatusOK, r.StatusCode, "directory: %s", r.Body)
	var rows []struct {
		ID       string `json:"id"`
		Readable bool   `json:"readable"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &rows))
	for _, row := range rows {
		if row.Readable {
			readable = append(readable, row.ID)
		} else {
			locked = append(locked, row.ID)
		}
	}
	return readable, locked
}

// Case 4 (API) — grant to a team above: the space GET must 404 for the dev,
// and the tickets list beneath it must 404 too.
func TestMatrixAPI04_GrantAbove_404(t *testing.T) {
	m := newAPIMatrix(t)
	m.grantTeam(t, m.eng, access.RoleAgent)

	requireAPINotFound(t, m.getAs(t, m.devTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID)))
	requireAPINotFound(t, m.getAs(t, m.devTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", m.ts.OrgID, m.spaceID)))

	// The same grant DOES reach the vp above (case 3 at the API layer).
	r := m.getAs(t, m.vpTok, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID))
	require.Equal(t, http.StatusOK, r.StatusCode, "vp must read via the downward expansion: %s", r.Body)
}

// Case 5 (API) — sibling team grant: 404 for the dev in platform.
func TestMatrixAPI05_SiblingGrant_404(t *testing.T) {
	m := newAPIMatrix(t)
	m.grantTeam(t, m.design, access.RoleAgent)

	requireAPINotFound(t, m.getAs(t, m.devTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID)))

	// The designer (in the granted team) reads fine — the fixture is live.
	r := m.getAs(t, m.designTok, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID))
	require.Equal(t, http.StatusOK, r.StatusCode, "granted sibling must read: %s", r.Body)
}

// Cases 9 and 10 — visibility in the directory, proven by exact id sets.
func TestMatrixAPI09_10_DirectoryVisibility(t *testing.T) {
	m := newAPIMatrix(t)

	// Second space so the directory has both a hidden and a discoverable row.
	hiddenID := createScopedSpace(t, m.ts, "Hidden Space", "hidden-space", "vector")
	testutil.SetSpaceVisibility(t, m.ts.DB.Pool, uuid.MustParse(hiddenID), "hidden")
	// m.spaceID stays at the default 'discoverable'.

	// The designer holds no grants: the directory must contain EXACTLY one
	// locked row (the discoverable space) and zero readable rows. The hidden
	// space is absent — asserted via exact set equality, not "not contains".
	readable, locked := m.directoryIDs(t, m.designTok)
	require.Empty(t, readable, "no grants → no readable rows")
	require.Equal(t, []string{m.spaceID}, locked,
		"directory must be exactly the locked discoverable row; hidden absent")

	// Case 10's second half: listed but unreadable — the locked space 404s
	// on direct access.
	requireAPINotFound(t, m.getAs(t, m.designTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID)))

	// Case 9's second half: the hidden space 404s too.
	requireAPINotFound(t, m.getAs(t, m.designTok,
		fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, hiddenID)))

	// Sanity: a grant flips the discoverable row to readable.
	m.grantTeam(t, m.design, access.RoleViewer)
	readable, locked = m.directoryIDs(t, m.designTok)
	require.Equal(t, []string{m.spaceID}, readable)
	require.Empty(t, locked)
}

// Case 7 (API) — org admin reads everything with zero grant rows.
func TestMatrixAPI07_AdminBypass(t *testing.T) {
	m := newAPIMatrix(t)

	var grantRows int
	require.NoError(t, m.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM space_grants WHERE org_id = $1`, m.ts.OrgID).Scan(&grantRows))
	require.Zero(t, grantRows, "premise: zero grant rows")

	r := m.getAs(t, m.ts.Token, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s", m.ts.OrgID, m.spaceID))
	require.Equal(t, http.StatusOK, r.StatusCode)

	readable, _ := m.directoryIDs(t, m.ts.Token)
	require.Contains(t, readable, m.spaceID)
}

// Case 21 (API) — a grant to a non-org-member is rejected with 400.
func TestMatrixAPI21_GrantToNonMember400(t *testing.T) {
	m := newAPIMatrix(t)

	otherOrg := testutil.CreateTestOrg(t, m.ts.DB.Pool)
	outsider := testutil.CreateTestUser(t, m.ts.DB.Pool, otherOrg.ID)

	r := m.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", m.ts.OrgID, m.spaceID),
		map[string]string{
			"subject_type": "user",
			"subject_id":   outsider.ID.String(),
			"role":         "viewer",
		}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "grant to non-member must 400: %s", r.Body)

	// A team from another org is equally rejected.
	var foreignTeamID uuid.UUID
	require.NoError(t, m.ts.DB.Pool.QueryRow(context.Background(),
		`SELECT id FROM teams WHERE org_id = $1 AND is_default`, otherOrg.ID).Scan(&foreignTeamID))
	r = m.ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/grants", m.ts.OrgID, m.spaceID),
		map[string]string{
			"subject_type": "team",
			"subject_id":   foreignTeamID.String(),
			"role":         "viewer",
		}, true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode, "grant to foreign team must 400: %s", r.Body)
}

// queryCounter counts every SQL statement the server issues, and separately
// the P2.5 auth-state lookups (sqlc embeds the query name in the SQL it
// sends, so the statement is identifiable).
type queryCounter struct {
	n         atomic.Int64
	authState atomic.Int64
}

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	if strings.Contains(data.SQL, "GetUserAuthState") {
		c.authState.Add(1)
	}
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Case 23 — a list endpoint issues a constant number of queries regardless
// of result count. Proven with a pgx QueryTracer counting every statement
// through the server's pool: the count for a 3-row list must equal the
// count for a 30-row list, on both the tickets list and the directory.
func TestMatrixAPI23_ConstantAuthQueries(t *testing.T) {
	db := testutil.NewTestDB(t)

	counter := &queryCounter{}
	cfg, err := pgxpool.ParseConfig(db.DSN)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = fmt.Sprintf("%q, public", db.Schema)
	cfg.ConnConfig.Tracer = counter
	cfg.MaxConns = 3
	countingPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(countingPool.Close)

	ts := newTestServerOn(t, db, countingPool)
	spaceID := createScopedSpace(t, ts, "Counted Space", "counted-space", "vector")

	mkTickets := func(n int) {
		for i := 0; i < n; i++ {
			r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets", ts.OrgID, spaceID),
				map[string]string{"title": fmt.Sprintf("t-%d", i), "priority": "low"}, true)
			require.Equal(t, http.StatusCreated, r.StatusCode, "seed ticket: %s", r.Body)
		}
	}

	countedGet := func(path string, wantRows int) int64 {
		// Warm request first: connection setup and auth caches must not
		// pollute the measured request.
		r := ts.get(t, path, true)
		require.Equal(t, http.StatusOK, r.StatusCode, "warm: %s", r.Body)
		before := counter.n.Load()
		authBefore := counter.authState.Load()
		r = ts.get(t, path, true)
		require.Equal(t, http.StatusOK, r.StatusCode)
		var rows []json.RawMessage
		require.NoError(t, json.Unmarshal(r.Body, &rows))
		require.Len(t, rows, wantRows, "result-count premise for the assertion")
		// P2.5 session control: the auth middleware performs EXACTLY ONE
		// auth-state read (token_generation + is_active) per authenticated
		// request. Zero would mean the revocation check was optimised away
		// — stateless tokens would then outlive deactivation, which is the
		// defect this line exists to keep dead. More than one would mean
		// the single-constant-cost-lookup contract broke.
		require.Equal(t, int64(1), counter.authState.Load()-authBefore,
			"exactly one GetUserAuthState read per authenticated request")
		return counter.n.Load() - before
	}

	ticketsPath := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/tickets?limit=50", ts.OrgID, spaceID)

	mkTickets(3)
	qAt3 := countedGet(ticketsPath, 3)
	mkTickets(27)
	qAt30 := countedGet(ticketsPath, 30)
	require.Equal(t, qAt3, qAt30,
		"tickets list: query count must not grow with result count (N=3: %d, N=30: %d)", qAt3, qAt30)
	require.LessOrEqual(t, qAt3, int64(8), "per-request query budget blown: %d", qAt3)

	// Directory: 2 spaces vs 12 spaces.
	dirPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)
	mkSpace := func(i int) {
		r := ts.post(t, fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID), map[string]string{
			"name": fmt.Sprintf("Dir Space %d", i), "slug": fmt.Sprintf("dir-space-%d", i), "type": "vector",
		}, true)
		require.Equal(t, http.StatusCreated, r.StatusCode, "seed space: %s", r.Body)
	}
	mkSpace(1)
	dAt2 := countedGet(dirPath, 2)
	for i := 2; i <= 11; i++ {
		mkSpace(i)
	}
	dAt12 := countedGet(dirPath, 12)
	require.Equal(t, dAt2, dAt12,
		"directory: query count must not grow with space count (N=2: %d, N=12: %d)", dAt2, dAt12)
}

// The effective-access endpoint returns the chain that produced the access —
// which grant, which direct team matched, at what depth — not merely the
// resulting role (spec §6).
func TestEffectiveAccess_ReturnsGrantChain(t *testing.T) {
	m := newAPIMatrix(t)
	m.grantTeam(t, m.platform, access.RoleAgent)

	// The vp (in eng) is reached through platform's grant: matched team eng,
	// depth 1 (eng → platform).
	r := m.getAs(t, m.ts.Token, fmt.Sprintf(
		"/api/v1/orgs/%s/spaces/%s/effective-access?user_id=%s", m.ts.OrgID, m.spaceID, m.vp.ID))
	require.Equal(t, http.StatusOK, r.StatusCode, "effective-access: %s", r.Body)

	var body struct {
		Access        bool   `json:"access"`
		Role          string `json:"role"`
		OrgAdmin      bool   `json:"org_admin"`
		OrgVisibility bool   `json:"org_visibility"`
		Grants        []struct {
			SubjectType     string  `json:"subject_type"`
			SubjectID       string  `json:"subject_id"`
			Role            string  `json:"role"`
			TeamName        string  `json:"team_name"`
			MatchedTeamID   *string `json:"matched_team_id"`
			MatchedTeamName string  `json:"matched_team_name"`
			Depth           int     `json:"depth"`
		} `json:"grants"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body))
	require.True(t, body.Access)
	require.Equal(t, "agent", body.Role)
	require.False(t, body.OrgAdmin)
	require.Len(t, body.Grants, 1, "exactly the platform grant must match")

	g := body.Grants[0]
	require.Equal(t, "team", g.SubjectType)
	require.Equal(t, m.platform.ID.String(), g.SubjectID)
	require.Equal(t, "agent", g.Role)
	require.Equal(t, "platform", g.TeamName)
	require.NotNil(t, g.MatchedTeamID, "the chain must name the matched direct team")
	require.Equal(t, m.eng.ID.String(), *g.MatchedTeamID)
	require.Equal(t, "eng", g.MatchedTeamName)
	require.Equal(t, 1, g.Depth, "eng is one level above platform")

	// The dev (direct member of platform) matches at depth 0.
	r = m.getAs(t, m.devTok, fmt.Sprintf(
		"/api/v1/orgs/%s/spaces/%s/effective-access", m.ts.OrgID, m.spaceID))
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.NoError(t, json.Unmarshal(r.Body, &body))
	require.True(t, body.Access)
	require.Len(t, body.Grants, 1)
	require.Equal(t, 0, body.Grants[0].Depth, "direct membership matches at depth 0")
	require.Equal(t, m.platform.ID.String(), *body.Grants[0].MatchedTeamID)

	// No access → empty chain, access=false, no role.
	r = m.getAs(t, m.ts.Token, fmt.Sprintf(
		"/api/v1/orgs/%s/spaces/%s/effective-access?user_id=%s", m.ts.OrgID, m.spaceID, m.designer.ID))
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.NoError(t, json.Unmarshal(r.Body, &body))
	require.False(t, body.Access)
	require.Empty(t, body.Role)
	require.Empty(t, body.Grants)

	// A non-admin without manage_grants may not inspect someone else.
	r = m.getAs(t, m.designTok, fmt.Sprintf(
		"/api/v1/orgs/%s/spaces/%s/effective-access?user_id=%s", m.ts.OrgID, m.spaceID, m.vp.ID))
	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"designer cannot read the space at all — 404 before any capability question")
}
