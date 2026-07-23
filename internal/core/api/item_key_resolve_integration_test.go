package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestItemKey_WireContractAndResolve exercises the full HTTP path: item_key is
// present on the create response and the list read path, and the resolve
// endpoint maps a key back to its item.
func TestItemKey_WireContractAndResolve(t *testing.T) {
	ts := newTestServer(t)
	spaceID := createScopedSpace(t, ts, "Keys Proj", "keys-proj", "vector")
	base := fmt.Sprintf("/api/v1/orgs/%s/spaces/%s/projects", ts.OrgID, spaceID)

	// Create — the response must carry the assigned item_key (snake_case wire).
	r := ts.post(t, base+"/items",
		map[string]string{"title": "First", "kind": "task", "priority": "medium"}, true)
	require.Equal(t, http.StatusCreated, r.StatusCode, "create: %s", r.Body)
	var created struct {
		ID      string `json:"id"`
		ItemKey string `json:"item_key"`
		Number  int    `json:"number"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &created))
	require.NotEmpty(t, created.ItemKey, "create response must include item_key")
	require.Equal(t, 1, created.Number)
	require.Regexp(t, `^[A-Z0-9]+-1$`, created.ItemKey)

	// List read path also carries item_key.
	r = ts.get(t, base+"/items", true)
	require.Equal(t, http.StatusOK, r.StatusCode)
	var list []struct {
		ItemKey string `json:"item_key"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &list))
	require.Len(t, list, 1)
	require.Equal(t, created.ItemKey, list[0].ItemKey)

	// Resolve the key back to the item.
	r = ts.get(t, base+"/items/resolve?key="+created.ItemKey, true)
	require.Equal(t, http.StatusOK, r.StatusCode, "resolve: %s", r.Body)
	var resolved struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &resolved))
	require.Equal(t, created.ID, resolved.ID)

	// Missing key parameter → 400.
	r = ts.get(t, base+"/items/resolve", true)
	require.Equal(t, http.StatusBadRequest, r.StatusCode)

	// Unknown key → 404.
	r = ts.get(t, base+"/items/resolve?key=NOPE-404", true)
	require.Equal(t, http.StatusNotFound, r.StatusCode)
}
