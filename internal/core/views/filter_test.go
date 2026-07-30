package views

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// doc builds a filter document from a body fragment, so each case reads as the
// one thing it is testing rather than as a wall of JSON.
func doc(body string) []byte {
	return []byte(`{"v":1,"sort":{"field":"updated_at","dir":"desc"},"filter":{` + body + `}}`)
}

// TestParseQuery_RejectsUnknownField is the load-bearing one.
//
// The rule is that an unknown field is REFUSED, never stored and never
// dropped. Dropping would be the quiet failure: the caller believes the view
// filters on something it does not, and the view returns too many rows
// forever. Delete DisallowUnknownFields and every case here fails.
func TestParseQuery_RejectsUnknownField(t *testing.T) {
	cases := map[string]string{
		"top level":       `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"},"limit":50}`,
		"inside filter":   `{"v":1,"filter":{"modules":["beacon"],"assignee":"me"},"sort":{"field":"updated_at","dir":"desc"}}`,
		"inside sort":     `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc","nulls":"first"}}`,
		"sketch operator": `{"v":1,"filter":{"modules":["beacon"],"filters":[{"field":"status","op":"in","value":["open"]}]},"sort":{"field":"updated_at","dir":"desc"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseQuery([]byte(raw))
			if err == nil {
				t.Fatal("expected an unknown field to be refused, got nil error")
			}
			if !errors.Is(err, ErrUnknownField) {
				t.Fatalf("expected ErrUnknownField, got %v", err)
			}
		})
	}
}

// TestParseQuery_RejectsUnsupportedVersion pins the versioning promise: a
// document from a future build is refused, not guessed at.
//
// v2 widened the accepted set from {1} to [MinVersion, Version]. The promise
// itself did not move — anything outside the range is still refused — so this
// names both ends and the values just outside them. It deliberately still
// refuses 3: a build that guessed at a document from a newer vocabulary would
// silently drop whatever it did not recognise and return a wider set of rows
// than the document asks for.
func TestParseQuery_RejectsUnsupportedVersion(t *testing.T) {
	for _, raw := range []string{
		`{"v":3,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`,
		`{"v":-1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`,
		`{"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`, // v absent decodes as 0
	} {
		if _, err := ParseQuery([]byte(raw)); err == nil {
			t.Fatalf("expected version refusal for %s", raw)
		}
	}
	// Both supported versions are accepted, and a v2 document is not obliged to
	// use anything v2 added.
	for _, raw := range []string{
		`{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`,
		`{"v":2,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}}`,
	} {
		if _, err := ParseQuery([]byte(raw)); err != nil {
			t.Fatalf("expected %s to be accepted, got %v", raw, err)
		}
	}
}

func TestParseQuery_RejectsTrailingContent(t *testing.T) {
	raw := `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"desc"}} {"v":1}`
	if _, err := ParseQuery([]byte(raw)); err == nil {
		t.Fatal("expected trailing content to be refused")
	}
}

func TestParseQuery_ModuleVocabulary(t *testing.T) {
	t.Run("codex is not queryable", func(t *testing.T) {
		// Codex pages are found through P6 search, which owns the page read
		// path. A view naming it must fail loudly rather than return nothing.
		if _, err := ParseQuery(doc(`"modules":["codex"]`)); err == nil {
			t.Fatal("expected codex to be refused as a view module")
		}
	})
	t.Run("empty module set", func(t *testing.T) {
		if _, err := ParseQuery(doc(`"modules":[]`)); err == nil {
			t.Fatal("expected an empty module set to be refused")
		}
	})
	t.Run("duplicate module", func(t *testing.T) {
		if _, err := ParseQuery(doc(`"modules":["beacon","beacon"]`)); err == nil {
			t.Fatal("expected a duplicated module to be refused")
		}
	})
	t.Run("both modules is legal", func(t *testing.T) {
		if _, err := ParseQuery(doc(`"modules":["beacon","vector"]`)); err != nil {
			t.Fatalf("expected a cross-module view to be legal, got %v", err)
		}
	})
}

// TestParseQuery_ModuleBoundFieldsRejectedAcrossModules is the asymmetry that
// cost this phase its most surprising finding: tickets have neither a kind
// column nor a sprint_id column. A filter naming either alongside Beacon can
// never match a ticket, so it is refused at write time rather than returning
// an empty Beacon half forever.
func TestParseQuery_ModuleBoundFieldsRejectedAcrossModules(t *testing.T) {
	sprint := uuid.NewString()
	cases := map[string]string{
		"kinds with beacon":      `"modules":["beacon"],"kinds":["bug"]`,
		"kinds across both":      `"modules":["beacon","vector"],"kinds":["bug"]`,
		"sprint_ids with beacon": `"modules":["beacon"],"sprint_ids":["` + sprint + `"]`,
		"sprint_ids across both": `"modules":["beacon","vector"],"sprint_ids":["` + sprint + `"]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseQuery(doc(body)); err == nil {
				t.Fatal("expected a Vector-only field to be refused when Beacon is in the module set")
			}
		})
	}
	t.Run("vector only is legal", func(t *testing.T) {
		if _, err := ParseQuery(doc(`"modules":["vector"],"kinds":["bug"],"sprint_ids":["` + sprint + `"]`)); err != nil {
			t.Fatalf("expected Vector-only fields to be legal on a Vector-only view, got %v", err)
		}
	})
}

// TestParseQuery_PriorityMatchesTheDatabaseEnum guards the pair that must not
// drift: both tickets and project_items CHECK priority against exactly these
// four values. A typo here becomes a view that silently matches nothing.
func TestParseQuery_PriorityMatchesTheDatabaseEnum(t *testing.T) {
	for _, p := range []string{"urgent", "high", "medium", "low"} {
		if _, err := ParseQuery(doc(`"modules":["beacon"],"priorities":["` + p + `"]`)); err != nil {
			t.Fatalf("priority %q is CHECK-constrained in the database and must be accepted: %v", p, err)
		}
	}
	for _, p := range []string{"critical", "P1", "", "URGENT"} {
		if _, err := ParseQuery(doc(`"modules":["beacon"],"priorities":["` + p + `"]`)); err == nil {
			t.Fatalf("priority %q is not in the database enum and must be refused", p)
		}
	}
}

func TestParseQuery_AssigneeTokens(t *testing.T) {
	user := uuid.NewString()
	t.Run("accepted", func(t *testing.T) {
		body := `"modules":["beacon"],"assignees":["me","unassigned","` + user + `"]`
		q, err := ParseQuery(doc(body))
		if err != nil {
			t.Fatalf("expected the three assignee forms to be accepted, got %v", err)
		}
		if len(q.Filter.Assignees) != 3 {
			t.Fatalf("expected 3 assignees, got %d", len(q.Filter.Assignees))
		}
	})
	t.Run("refused", func(t *testing.T) {
		for _, a := range []string{"you", "someone@example.com", "not-a-uuid", ""} {
			if _, err := ParseQuery(doc(`"modules":["beacon"],"assignees":["` + a + `"]`)); err == nil {
				t.Fatalf("assignee %q must be refused", a)
			}
		}
	})
	t.Run("me survives a store round trip unresolved", func(t *testing.T) {
		// The whole point of the token: it must still be "me" after storage,
		// so each viewer resolves it to themselves. Resolving at write time
		// would freeze the view to its author.
		q, err := ParseQuery(doc(`"modules":["beacon"],"assignees":["me"]`))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := q.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"me"`) {
			t.Fatalf("the me token must be stored verbatim, got %s", encoded)
		}
	})
}

func TestParseQuery_SortVocabulary(t *testing.T) {
	t.Run("status is not sortable", func(t *testing.T) {
		// Free text with no meaningful total order: sorting by it would order
		// alphabetically and read as a bug.
		raw := `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"status","dir":"asc"}}`
		if _, err := ParseQuery([]byte(raw)); err == nil {
			t.Fatal("expected status to be refused as a sort field")
		}
	})
	t.Run("arbitrary column is refused", func(t *testing.T) {
		// The negative case that matters: a caller must not be able to name a
		// column. This is the difference between a record and a query language.
		raw := `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"assignee_id","dir":"asc"}}`
		if _, err := ParseQuery([]byte(raw)); err == nil {
			t.Fatal("expected an unlisted column to be refused as a sort field")
		}
	})
	t.Run("direction is bounded", func(t *testing.T) {
		raw := `{"v":1,"filter":{"modules":["beacon"]},"sort":{"field":"updated_at","dir":"sideways"}}`
		if _, err := ParseQuery([]byte(raw)); err == nil {
			t.Fatal("expected an unknown sort direction to be refused")
		}
	})
	t.Run("absent sort defaults", func(t *testing.T) {
		q, err := ParseQuery([]byte(`{"v":1,"filter":{"modules":["beacon"]}}`))
		if err != nil {
			t.Fatal(err)
		}
		if q.Sort != DefaultSort() {
			t.Fatalf("expected the default sort, got %+v", q.Sort)
		}
	})
}

func TestParseQuery_Bounds(t *testing.T) {
	t.Run("text length", func(t *testing.T) {
		long, _ := json.Marshal(strings.Repeat("x", MaxTextLen+1))
		if _, err := ParseQuery(doc(`"modules":["beacon"],"text":` + string(long))); err == nil {
			t.Fatal("expected an over-long text term to be refused")
		}
	})
	t.Run("space id count", func(t *testing.T) {
		ids := make([]string, MaxSpaceIDs+1)
		for i := range ids {
			ids[i] = `"` + uuid.NewString() + `"`
		}
		body := `"modules":["beacon"],"space_ids":[` + strings.Join(ids, ",") + `]`
		if _, err := ParseQuery(doc(body)); err == nil {
			t.Fatal("expected an over-long space id list to be refused")
		}
	})
	t.Run("blank status", func(t *testing.T) {
		if _, err := ParseQuery(doc(`"modules":["beacon"],"statuses":["  "]`)); err == nil {
			t.Fatal("expected a blank status to be refused")
		}
	})
}

// TestQuery_EncodeIsThisBuildsSerialisation proves storage never carries the
// caller's bytes: whitespace, key order and formatting are all this build's.
// It is what makes "no unrecognised key reaches the table" structural rather
// than a promise.
func TestQuery_EncodeIsThisBuildsSerialisation(t *testing.T) {
	messy := []byte("{\n\t\"v\" : 1,\n\t\"filter\" : { \"modules\" : [ \"vector\" ] },\n\t\"sort\":{\"field\":\"title\",\"dir\":\"asc\"}\n}")
	q, err := ParseQuery(messy)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := q.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(encoded), "\n\t") {
		t.Fatalf("stored bytes must be this build's compact serialisation, got %s", encoded)
	}
	// And it must parse back to an identical document.
	back, err := ParseQuery(encoded)
	if err != nil {
		t.Fatalf("re-parsing our own encoding must succeed: %v", err)
	}
	if back.Sort != q.Sort || len(back.Filter.Modules) != 1 || back.Filter.Modules[0] != ModuleVector {
		t.Fatalf("round trip changed the document: %+v vs %+v", back, q)
	}
}

func TestFilter_HasModule(t *testing.T) {
	f := Filter{Modules: []Module{ModuleVector}}
	if f.HasModule(ModuleBeacon) {
		t.Fatal("vector-only filter must not report beacon")
	}
	if !f.HasModule(ModuleVector) {
		t.Fatal("vector-only filter must report vector")
	}
}
