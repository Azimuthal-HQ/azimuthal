package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Azimuthal-HQ/azimuthal/internal/testutil"
)

// B5 — GET /api/v1/orgs/{orgID}/config.
//
// The endpoint exists so the UI can mark its ticket-reference fields required
// before the server refuses the request, rather than after. Two things about
// it are worth testing and one is worth testing carefully:
//
//   - the wire contract is exactly one key, and adding another is a
//     disclosure decision (the type-level half of that lives in
//     internal/core/api/spaces/config_test.go, which is where the omitempty
//     and untagged-field dodges are closed);
//   - the value it reports is the value the mutating handlers ENFORCE, not a
//     second copy of the flag — proven on a server where both are visible at
//     once;
//   - the guard class. TestReadPathSweep_GuardClassMatchesMiddleware verifies
//     the two admin classes against the real middleware chain and accepts
//     every other class on the accounting row alone, so for an org-member
//     route the non-member case below is the only thing that checks the claim.

func bootConfigPath(ts *testServer) string {
	return fmt.Sprintf("/api/v1/orgs/%s/config", ts.OrgID)
}

func decodeBootConfig(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &out), "boot config body: %s", body)
	return out
}

// TestBootConfig_ExposesExactlyTheAllowlistedKeys pins the wire shape.
func TestBootConfig_ExposesExactlyTheAllowlistedKeys(t *testing.T) {
	ts := newTestServer(t)

	r := ts.get(t, bootConfigPath(ts), true)
	require.Equal(t, http.StatusOK, r.StatusCode, "boot config: %s", r.Body)
	requireSnakeCaseKeys(t, r.Body)

	keys := make([]string, 0, 1)
	for k := range decodeBootConfig(t, r.Body) {
		keys = append(keys, k)
	}
	require.ElementsMatch(t, []string{"ticket_ref_required"}, keys,
		"the boot-config wire contract is this key exactly; anything else is a decision about what every org member may read")
}

// TestBootConfig_ReportsThePolicyTheHandlersEnforce is the assertion that ties
// the endpoint to the behaviour it describes.
//
// Half (a) alone would be vacuous: newTestServer builds its handlers on the
// permissive zero policy, so a handler hard-coding false would satisfy it. Half
// (b) uses the required-mode harness and, on that same server, sends a
// reference-less mutation — so the test fails both if the endpoint reports the
// wrong value and if the endpoint and the enforcement ever come apart.
func TestBootConfig_ReportsThePolicyTheHandlersEnforce(t *testing.T) {
	t.Run("permissive deployment", func(t *testing.T) {
		ts := newTestServer(t)

		r := ts.get(t, bootConfigPath(ts), true)
		require.Equal(t, http.StatusOK, r.StatusCode, "boot config: %s", r.Body)

		var cfg struct {
			TicketRefRequired bool `json:"ticket_ref_required"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &cfg))
		require.False(t, cfg.TicketRefRequired)

		// ...and a reference-less mutation really is accepted here.
		created := ticketRefAuditCreateTeam(t, ts, "permissive-team", "")
		require.Equal(t, http.StatusCreated, created.StatusCode,
			"the endpoint said references are optional; the mutation must agree: %s", created.Body)
	})

	t.Run("required deployment", func(t *testing.T) {
		ts := newTicketRefRequiredServer(t)

		r := ts.get(t, bootConfigPath(ts), true)
		require.Equal(t, http.StatusOK, r.StatusCode, "boot config: %s", r.Body)

		var cfg struct {
			TicketRefRequired bool `json:"ticket_ref_required"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &cfg))
		require.True(t, cfg.TicketRefRequired)

		// ...and on the very same server the mutation refuses. One truth.
		refused := ticketRefAuditCreateTeam(t, ts, "required-team", "")
		ticketRefAuditRequireRejected(t, refused, ticketRefAuditMissingMessage)
	})
}

// TestBootConfig_EndpointMatrix is spec section 2.6 for an org-member route.
//
// There is no capability to gate here, so a low-capability member proves
// nothing and no such case is written. The meaningful negative for this class
// is the NON-MEMBER, and case 3 — a plain member holding no grants and no
// admin — is what pins the class as org-member rather than org-admin.
func TestBootConfig_EndpointMatrix(t *testing.T) {
	ts := newTestServer(t)

	t.Run("no credentials", func(t *testing.T) {
		r := ts.get(t, bootConfigPath(ts), false)
		requireErrorCode(t, r, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("a member of another org gets 404, never 403", func(t *testing.T) {
		otherOrg := testutil.CreateTestOrg(t, ts.DB.Pool)
		stranger := testutil.CreateTestUser(t, ts.DB.Pool, otherOrg.ID)
		tok := ts.tokenFor(t, stranger.ID, stranger.Email)

		r := ts.getAs(t, tok, bootConfigPath(ts))
		requireAPINotFound(t, r)
	})

	t.Run("a plain member with no grants and no admin gets the flags", func(t *testing.T) {
		// The load-bearing case. If someone later "hardens" this route with
		// the org-admin guard, this fails — and so does
		// TestReadPathSweep_GuardClassMatchesMiddleware, from the other side.
		member := testutil.CreateTestUserWithRole(t, ts.DB.Pool, ts.OrgID, "member")
		tok := ts.tokenFor(t, member.ID, member.Email)

		r := ts.getAs(t, tok, bootConfigPath(ts))
		require.Equal(t, http.StatusOK, r.StatusCode, "an ordinary member must be able to read the flags: %s", r.Body)
		requireSnakeCaseKeys(t, r.Body)
	})

	t.Run("a malformed org id does not 500", func(t *testing.T) {
		r := ts.get(t, "/api/v1/orgs/not-a-uuid/config", true)
		require.Less(t, r.StatusCode, http.StatusInternalServerError,
			"a malformed org id must be a client error, got %d: %s", r.StatusCode, r.Body)
	})
}
