package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_SpaceCreate_RejectsPreRebrandTypeLiterals_Regression pins the
// space-type rebrand contract (migration 021): the API accepts only the
// rebranded type values and rejects the pre-rebrand literals with a clean 400.
// Before the handler allowlist existed, an old literal fell through to the
// spaces_type_valid CHECK constraint and surfaced as a 500.
func TestIntegration_SpaceCreate_RejectsPreRebrandTypeLiterals_Regression(t *testing.T) {
	ts := newTestServer(t)
	spacesPath := fmt.Sprintf("/api/v1/orgs/%s/spaces", ts.OrgID)

	for i, typ := range []string{"service_desk", "wiki", "project", "not-a-type", ""} {
		r := ts.post(t, spacesPath, map[string]any{
			"name": "Rejected Space",
			"slug": fmt.Sprintf("rejected-space-%d", i),
			"type": typ,
		}, true)

		require.Equalf(t, http.StatusBadRequest, r.StatusCode,
			"type %q must be rejected with 400, got %d: %s", typ, r.StatusCode, r.Body)

		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &body))
		require.Equalf(t, "VALIDATION_ERROR", body.Error.Code, "type %q error code", typ)
	}

	for i, typ := range []string{"beacon", "codex", "vector"} {
		r := ts.post(t, spacesPath, map[string]any{
			"name": fmt.Sprintf("Accepted %s", typ),
			"slug": fmt.Sprintf("accepted-space-%d", i),
			"type": typ,
		}, true)

		require.Equalf(t, http.StatusCreated, r.StatusCode,
			"type %q must be accepted, got %d: %s", typ, r.StatusCode, r.Body)

		var space struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(r.Body, &space))
		require.Equal(t, typ, space.Type)
	}
}
