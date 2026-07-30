package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// THE WIKI HALF OF known-issues #24: routes that answered 500 for a mistake the
// caller made, while their own annotations promised a 4xx.
//
// Every case here is a request that is well formed, authorised, and wrong. None
// of them had a test, which is how five of them stayed open across four phases:
// a 500 on a path nothing exercises looks exactly like a path that works.
//
// Each fails before its fix with `500 INTERNAL_ERROR`, and the two families
// fail for different reasons — the tree refusals because handleWikiError had no
// arm for four sentinels the domain was already raising, the document refusals
// because doc.Validate's shape errors reached no arm either. Both are asserted
// on the STATUS and on the code, because a fix that answered 400 with
// INTERNAL_ERROR would be half a fix.

// errorEnvelope is the standard error body every one of these must carry.
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decodeEnvelope(t *testing.T, body []byte) errorEnvelope {
	t.Helper()
	var out errorEnvelope
	require.NoError(t, json.Unmarshal(body, &out), "error body must be the standard envelope: %s", body)
	return out
}

// A parent_id that names no page is the caller's mistake. It answered
// `500 {"code":"INTERNAL_ERROR","message":"wiki operation failed: fetching
// parent page: no rows in result set"}` — the wrong class, and the internal
// wording with it.
func TestWikiErrors_CreatePageWithUnknownParentIs404(t *testing.T) {
	f := newDocFixture(t)

	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.wikiPath("/"),
		map[string]any{"title": "Orphan", "content": "", "parent_id": uuid.New().String()})

	require.Equal(t, http.StatusNotFound, r.StatusCode,
		"a parent that does not exist is 404, not 500: %s", r.Body)
	env := decodeEnvelope(t, r.Body)
	require.Equal(t, "NOT_FOUND", env.Error.Code)
	require.NotContains(t, env.Error.Message, "no rows in result set",
		"the pgx wording must not reach the caller")

	// The successful shape is unchanged: a real parent still nests.
	child := f.createPage(t, f.contribTok, "Child", "")
	r = f.ts.requestAs(t, f.contribTok, http.MethodPost, f.wikiPath("/"),
		map[string]any{"title": "Grandchild", "content": "", "parent_id": child})
	require.Equal(t, http.StatusCreated, r.StatusCode, "%s", r.Body)
}

// The move route raises four tree sentinels and handleWikiError matched none of
// them, so all four answered 500 while the route annotates 400 and 404.
//
// ErrTargetSpaceNotFound is deliberately NOT tested here: an unknown
// target_space_id is refused earlier by the destination's edit_any guard
// (moveInputFromRequest), which already answers 404, so a test naming a random
// space uuid would pass with the new arm deleted. Its arm is reachable only by
// losing a race with a space deletion.
func TestWikiErrors_MoveRefusalsCarryTheirDocumentedClass(t *testing.T) {
	f := newDocFixture(t)
	otherSpace := createScopedSpace(t, f.ts, "Elsewhere", "elsewhere-codex", "codex")

	// The org owner holds edit_any everywhere, which is what the move needs in
	// both spaces.
	movePath := func(pageID string) string {
		return f.pagePath(pageID, "/move")
	}

	t.Run("a parent that does not exist is 404", func(t *testing.T) {
		r := f.ts.requestAs(t, f.ts.Token, http.MethodPost, movePath(f.pageID),
			map[string]any{"parent_id": uuid.New().String(), "position": 0})
		require.Equal(t, http.StatusNotFound, r.StatusCode, "%s", r.Body)
		require.Equal(t, "NOT_FOUND", decodeEnvelope(t, r.Body).Error.Code)
	})

	t.Run("a parent in another space is 400", func(t *testing.T) {
		// Both pages exist and the caller may edit both; it is the COMBINATION
		// that is wrong, which is a fact about the request.
		foreign := f.ts.requestAs(t, f.ts.Token, http.MethodPost,
			"/api/v1/orgs/"+f.ts.OrgID.String()+"/spaces/"+otherSpace+"/wiki/",
			map[string]any{"title": "Foreign", "content": ""})
		require.Equal(t, http.StatusCreated, foreign.StatusCode, "%s", foreign.Body)
		var page struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(foreign.Body, &page))

		r := f.ts.requestAs(t, f.ts.Token, http.MethodPost, movePath(f.pageID),
			map[string]any{"parent_id": page.ID, "position": 0})
		require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
		require.Equal(t, "VALIDATION_ERROR", decodeEnvelope(t, r.Body).Error.Code)
	})

	t.Run("a page moved beneath itself is 400", func(t *testing.T) {
		r := f.ts.requestAs(t, f.ts.Token, http.MethodPost, movePath(f.pageID),
			map[string]any{"parent_id": f.pageID, "position": 0})
		require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
		require.Equal(t, "VALIDATION_ERROR", decodeEnvelope(t, r.Body).Error.Code)
	})

	t.Run("a legitimate reparent still succeeds", func(t *testing.T) {
		// Without this the three refusals above would all pass against a handler
		// that refused every move.
		parent := f.createPage(t, f.ts.Token, "New parent", "")
		r := f.ts.requestAs(t, f.ts.Token, http.MethodPost, movePath(f.pageID),
			map[string]any{"parent_id": parent, "position": 0})
		require.Equal(t, http.StatusOK, r.StatusCode, "%s", r.Body)
	})
}

// A document that is valid JSON but not a ProseMirror document. Both routes
// annotate 400 "Malformed document" and both answered 500.
//
// The four shapes are the whole of doc.Validate's refusal surface, and the
// fourth is the reason the fix wraps the validation rather than enumerating
// sentinels: a "content" member that is not an array is a bare fmt.Errorf with
// no sentinel to enumerate, so a switch over the three exported ones would have
// answered 400 for three shapes of one mistake and 500 for the fourth.
func TestWikiErrors_MalformedDocumentIs400OnDraftAndPublish(t *testing.T) {
	f := newDocFixture(t)
	base := f.openDocument(t, f.contribTok, f.pageID).BaseVersion

	malformed := map[string]json.RawMessage{
		"not an object":              json.RawMessage(`["paragraph"]`),
		"not rooted at doc":          json.RawMessage(`{"type":"paragraph"}`),
		"a node with no type":        json.RawMessage(`{"type":"doc","content":[{"content":[]}]}`),
		"content that is not a list": json.RawMessage(`{"type":"doc","content":"nope"}`),
	}

	for name, document := range malformed {
		t.Run("draft: "+name, func(t *testing.T) {
			r := f.ts.requestAs(t, f.contribTok, http.MethodPut, f.pagePath(f.pageID, "/draft"),
				map[string]any{"title": "Runbook", "doc": document, "base_version": base})
			require.Equal(t, http.StatusBadRequest, r.StatusCode,
				"a malformed document is the caller's to fix: %s", r.Body)
			require.Equal(t, "VALIDATION_ERROR", decodeEnvelope(t, r.Body).Error.Code)
		})

		t.Run("publish: "+name, func(t *testing.T) {
			r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
				map[string]any{"title": "Runbook", "doc": document, "base_version": base})
			require.Equal(t, http.StatusBadRequest, r.StatusCode, "%s", r.Body)
			require.Equal(t, "VALIDATION_ERROR", decodeEnvelope(t, r.Body).Error.Code)
		})
	}

	// The message is written server-side and says nothing about the caller's
	// bytes — the wrapped cause is a JSON decoder's wording.
	r := f.ts.requestAs(t, f.contribTok, http.MethodPost, f.pagePath(f.pageID, "/publish"),
		map[string]any{"title": "Runbook", "doc": json.RawMessage(`{"type":"paragraph"}`), "base_version": base})
	require.NotContains(t, decodeEnvelope(t, r.Body).Error.Message, "paragraph")

	// And a well-formed document still publishes, so the refusals above are not
	// a handler that refuses everything.
	f.publish(t, f.contribTok, f.pageID, "Runbook",
		json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"}]}`), base, nil)
}
