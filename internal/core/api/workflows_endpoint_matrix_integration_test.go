package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIntegration_WorkflowEndpoints_AuthAndValidationMatrix applies the
// mandatory per-endpoint matrix (spec §2.6) to the org-scoped workflow CRUD
// surface: no credentials → 401, and a syntactically invalid workflow ID →
// 400 with the documented error shape. These are the default-workflow
// endpoints that back the per-module assignment path.
func TestIntegration_WorkflowEndpoints_AuthAndValidationMatrix(t *testing.T) {
	ts := newTestServer(t)
	base := fmt.Sprintf("/api/v1/orgs/%s/workflows", ts.OrgID)

	endpoints := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"list workflows", http.MethodGet, base, nil},
		{"create workflow", http.MethodPost, base, map[string]any{"name": "wf", "applies_to": "tickets"}},
		{"get workflow", http.MethodGet, base + "/not-a-uuid", nil},
		{"update workflow", http.MethodPut, base + "/not-a-uuid", map[string]any{"name": "wf"}},
		{"delete workflow", http.MethodDelete, base + "/not-a-uuid", nil},
		{"list states", http.MethodGet, base + "/not-a-uuid/states", nil},
		{"create state", http.MethodPost, base + "/not-a-uuid/states", map[string]any{"name": "open"}},
		{"delete state", http.MethodDelete, base + "/not-a-uuid/states/also-not-a-uuid", nil},
		{"list transitions", http.MethodGet, base + "/not-a-uuid/transitions", nil},
		{"create transition", http.MethodPost, base + "/not-a-uuid/transitions", map[string]any{}},
		{"delete transition", http.MethodDelete, base + "/not-a-uuid/transitions/also-not-a-uuid", nil},
	}

	do := func(method, path string, body map[string]any, authed bool) httpResult {
		switch method {
		case http.MethodGet:
			return ts.get(t, path, authed)
		case http.MethodPost:
			return ts.post(t, path, body, authed)
		case http.MethodPut:
			return ts.put(t, path, body, authed)
		case http.MethodDelete:
			return ts.delete(t, path, authed)
		default:
			t.Fatalf("unhandled method %s", method)
			return httpResult{}
		}
	}

	for _, ep := range endpoints {
		t.Run(ep.name+" unauthenticated", func(t *testing.T) {
			r := do(ep.method, ep.path, ep.body, false)
			require.Equalf(t, http.StatusUnauthorized, r.StatusCode, "body: %s", r.Body)
		})
	}

	for _, ep := range endpoints {
		if ep.path == base {
			continue // no ID segment to invalidate on the collection routes
		}
		t.Run(ep.name+" invalid id", func(t *testing.T) {
			r := do(ep.method, ep.path, ep.body, true)
			require.Equalf(t, http.StatusBadRequest, r.StatusCode, "body: %s", r.Body)

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(r.Body, &body))
			require.NotEmpty(t, body.Error.Code, "error responses carry the documented shape")
		})
	}
}
