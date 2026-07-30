package views

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Filter document v2 — the record, the token grammar and the versioning rule.
//
// docV2 is doc's counterpart. The two exist side by side so that every case
// below can be run at BOTH versions, which is what the refusal tests need: the
// same filter must be refused at v1 and accepted at v2, and a test that only
// showed one half would not distinguish "refused for the right reason" from
// "refused for any reason at all".
func docV2(body string) []byte {
	return []byte(`{"v":2,"sort":{"field":"updated_at","dir":"desc"},"filter":{` + body + `}}`)
}

// TestParseDateBound_ClosedGrammar pins the token vocabulary in both
// directions. The refusals are the half that matters: a grammar that accepts
// what it does not understand is not closed.
func TestParseDateBound_ClosedGrammar(t *testing.T) {
	for _, ok := range []string{
		"-7d", "+7d", "-1w", "+52w", "-1mo", "+12mo", "-999d", "now",
		"2026-01-31T00:00:00Z", "2026-01-31T12:30:00+01:00",
	} {
		if _, err := ParseDateBound(ok); err != nil {
			t.Errorf("expected %q to parse, got %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"7d",     // the sign is required: a bound must say which way it points
		"-7",     // no unit
		"-7y",    // not a unit here
		"-1m",    // MINUTES in JQL; ours is "mo", and the collision is the reason
		"-2h",    // finer than the smallest unit
		"-0d",    // zero units is not a period
		"-1000d", // past MaxRelativeUnits
		"--7d",
		"-7 d",
		"2026-01-31",          // a date is not an instant
		"2026-01-31T00:00:00", // RFC3339 requires the offset
		"NOW",
		"yesterday",
	} {
		if _, err := ParseDateBound(bad); err == nil {
			t.Errorf("expected %q to be refused", bad)
		}
	}
}

// TestDateBound_MonthArithmeticClamps is the calendar-edge regression test.
//
// time.Time.AddDate does not clamp, it normalises: 31 March minus one month is
// 31 February, which it reports as 3 March. A view called "changed in the last
// month" would then skip the last days of February every March — a count
// slightly wrong, once a year, that nobody would ever report. PostgreSQL's
// interval arithmetic clamps, and so does this.
//
// Fails-before: replace addMonths with now.AddDate(0, b.Units, 0).
func TestDateBound_MonthArithmeticClamps(t *testing.T) {
	b, err := ParseDateBound("-1mo")
	if err != nil {
		t.Fatal(err)
	}
	got := b.Resolve(time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC))
	want := time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("31 March minus one month = %s, want %s (clamped to the last day of February)", got, want)
	}
	// A leap year clamps to the 29th rather than the 28th.
	if got, want := b.Resolve(time.Date(2028, 3, 31, 12, 0, 0, 0, time.UTC)),
		time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("leap year: got %s, want %s", got, want)
	}
	// A month with room needs no clamping at all.
	if got, want := b.Resolve(time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)),
		time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("mid-month: got %s, want %s", got, want)
	}
}

// TestDateBound_ResolveUsesTheInstantItIsGiven is the single-now property at
// the unit level: the same bound resolved twice against one instant gives one
// answer, and against a different instant gives a different one.
func TestDateBound_ResolveUsesTheInstantItIsGiven(t *testing.T) {
	b, err := ParseDateBound("-7d")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if !b.Resolve(at).Equal(b.Resolve(at)) {
		t.Error("one instant must give one answer")
	}
	if b.Resolve(at).Equal(b.Resolve(at.Add(time.Hour))) {
		t.Error("the boundary must move with the instant, or the token is not being resolved at all")
	}
	// "now" resolves to the instant itself, which is what makes an "overdue"
	// filter expressible without a stored timestamp.
	nowBound, err := ParseDateBound(DateNow)
	if err != nil {
		t.Fatal(err)
	}
	if !nowBound.Resolve(at).Equal(at) {
		t.Errorf("the %q token must resolve to the evaluation instant", DateNow)
	}
	// An absolute bound ignores it entirely.
	abs, err := ParseDateBound("2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !abs.Resolve(at).Equal(abs.Resolve(at.AddDate(1, 0, 0))) {
		t.Error("an absolute bound must not depend on the evaluation instant")
	}
}

// TestValidate_V2KeysAreRefusedInsideAV1Document is the versioning promise.
//
// A v1 reader that met a date range would drop it and return a WIDER set of
// rows than the document asks for, which is the failure nobody sees. So a
// document must declare what it uses.
func TestValidate_V2KeysAreRefusedInsideAV1Document(t *testing.T) {
	for _, body := range []string{
		`"modules":["beacon"],"updated_at":{"after":"-7d"}`,
		`"modules":["beacon"],"created_at":{"before":"now"}`,
		`"modules":["beacon"],"due_at":{"after":"-1d"}`,
		`"modules":["beacon"],"resolved_at":{"after":"-1d"}`,
		`"modules":["beacon"],"statuses":["x"],"not":{"statuses":true}`,
	} {
		if _, err := ParseQuery(doc(body)); err == nil {
			t.Errorf("expected a v1 document to refuse the v2 filter %s", body)
		}
		// The identical filter is accepted once the document says v2, so the
		// refusal above is about the VERSION and not about the filter.
		if _, err := ParseQuery(docV2(body)); err != nil {
			t.Errorf("expected v2 to accept %s, got %v", body, err)
		}
	}
}

// TestValidate_DateNegationHasNoSpelling is the structural refusal.
//
// Negation on a date field is refused because Negate names only the six
// membership fields — there is no key to write it with. The unknown-field rule
// does the work, so there is no special case that could be forgotten, and the
// assertion is on ErrUnknownField rather than on "some error" to say so.
func TestValidate_DateNegationHasNoSpelling(t *testing.T) {
	for _, f := range []string{"created_at", "updated_at", "due_at", "resolved_at", "text", "modules"} {
		_, err := ParseQuery(docV2(`"modules":["beacon"],"not":{"` + f + `":true}`))
		if err == nil {
			t.Errorf("expected negation on %q to be refused", f)
			continue
		}
		if !errors.Is(err, ErrUnknownField) {
			t.Errorf("negation on %q should be refused as an unknown field, got %v", f, err)
		}
	}
}

// TestValidate_V2Refusals covers the remaining rules, each with the reason it
// exists.
func TestValidate_V2Refusals(t *testing.T) {
	cases := map[string]string{
		// "Everything except nothing" is everything, which the filter already
		// says by leaving the field out.
		"negation with no values":    `"modules":["beacon"],"not":{"statuses":true}`,
		"empty range":                `"modules":["beacon"],"updated_at":{}`,
		"unknown token":              `"modules":["beacon"],"updated_at":{"after":"-7 fortnights"}`,
		"unknown key inside a range": `"modules":["beacon"],"updated_at":{"since":"-7d"}`,
		"inverted absolute bounds":   `"modules":["beacon"],"updated_at":{"after":"2026-02-01T00:00:00Z","before":"2026-01-01T00:00:00Z"}`,
		"inverted relative bounds":   `"modules":["beacon"],"updated_at":{"after":"-1d","before":"-7d"}`,
		"equal absolute bounds":      `"modules":["beacon"],"updated_at":{"after":"2026-02-01T00:00:00Z","before":"2026-02-01T00:00:00Z"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseQuery(docV2(body)); err == nil {
				t.Fatalf("expected refusal for %s", body)
			}
		})
	}

	// A MIXED pair is accepted. Which bound comes first depends on when the
	// query runs, so there is nothing to check that would still be true
	// tomorrow — and checking it against the wall clock would let a stored view
	// become invalid without anyone touching it.
	if _, err := ParseQuery(docV2(`"modules":["beacon"],"updated_at":{"after":"-7d","before":"2026-01-01T00:00:00Z"}`)); err != nil {
		t.Errorf("a mixed absolute/relative pair is not statically orderable and must be accepted: %v", err)
	}
	// A negation flag beside values is fine, which is what makes the refusal
	// above about the emptiness rather than about negation.
	if _, err := ParseQuery(docV2(`"modules":["beacon"],"statuses":["closed"],"not":{"statuses":true}`)); err != nil {
		t.Errorf("a negation with values must be accepted: %v", err)
	}
}

// TestQuery_V1DocumentsRoundTripByteIdentically is the compatibility guarantee.
//
// A stored v1 document must survive a v2 build unchanged, or every saved view
// silently rewrites the first time anything touches it.
//
// The corpus is generated BY Encode rather than hand-written, and that is not
// laziness: hand-written JSON containing "space_ids":[] never round-trips,
// because omitempty drops an empty slice — so a hand-written corpus would fail
// for a reason that has nothing to do with v2 and would prove nothing.
func TestQuery_V1DocumentsRoundTripByteIdentically(t *testing.T) {
	corpus := []Query{
		{V: 1, Filter: Filter{Modules: []Module{ModuleBeacon}}, Sort: DefaultSort()},
		{V: 1, Filter: Filter{
			Modules:  []Module{ModuleBeacon, ModuleVector},
			Statuses: []string{"open", "in_progress"},
		}, Sort: Sort{Field: "created_at", Dir: "asc"}},
		{V: 1, Filter: Filter{
			Modules:    []Module{ModuleVector},
			Kinds:      []string{"bug"},
			Priorities: []string{"urgent"},
		}, Sort: DefaultSort()},
		{V: 1, Filter: Filter{
			Modules:   []Module{ModuleBeacon},
			Assignees: []string{AssigneeMe, AssigneeUnassigned},
			Text:      "gateway",
		}, Sort: Sort{Field: "title", Dir: "asc"}},
		{V: 1, Filter: Filter{
			Modules:  []Module{ModuleVector},
			SpaceIDs: []uuid.UUID{uuid.MustParse("11111111-2222-3333-4444-555555555555")},
		}, Sort: DefaultSort()},
	}
	for i := range corpus {
		q := corpus[i]
		stored, err := q.Encode()
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		reparsed, err := ParseQuery(stored)
		if err != nil {
			t.Fatalf("case %d: a v1 document must still parse: %v", i, err)
		}
		if reparsed.V != 1 {
			t.Errorf("case %d: version rewritten to %d — v1 documents are never upgraded", i, reparsed.V)
		}
		again, err := reparsed.Encode()
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if string(again) != string(stored) {
			t.Errorf("case %d: a v1 document did not round-trip byte-identically\n  before: %s\n   after: %s",
				i, stored, again)
		}
		// The specific way it would break: omitempty does not omit a zero
		// STRUCT, so a Negate modelled as a value field without omitzero would
		// stamp "not":{} onto every stored document the first time it was read.
		//
		// Matched as `"key":{` rather than as the bare name: `updated_at` is
		// also the DEFAULT SORT FIELD, so a substring search for the name alone
		// fires on every document and tests nothing.
		for _, leaked := range []string{`"not":{`, `"created_at":{`, `"updated_at":{`, `"due_at":{`, `"resolved_at":{`} {
			if strings.Contains(string(again), leaked) {
				t.Errorf("case %d: the v2 key %s leaked into a v1 document: %s", i, leaked, again)
			}
		}
	}
}

// TestQuery_RequiredVersion states the rule the whole scheme rests on.
func TestQuery_RequiredVersion(t *testing.T) {
	v1 := Query{V: 1, Filter: Filter{
		Modules:  []Module{ModuleBeacon},
		Statuses: []string{"open"},
	}, Sort: DefaultSort()}
	if got := v1.RequiredVersion(); got != MinVersion {
		t.Errorf("a filter using nothing v2 added requires version %d, want %d", got, MinVersion)
	}
	// An all-false negation record is not a v2 capability — otherwise a
	// document would need v2 in order to say nothing at all.
	v1.Filter.Not = Negate{}
	if got := v1.RequiredVersion(); got != MinVersion {
		t.Errorf("an all-false negation record still requires %d", got)
	}
	for _, f := range []Filter{
		{Modules: []Module{ModuleBeacon}, UpdatedAt: &DateRange{After: "-7d"}},
		{Modules: []Module{ModuleBeacon}, CreatedAt: &DateRange{Before: "now"}},
		{Modules: []Module{ModuleBeacon}, DueAt: &DateRange{After: "-1d"}},
		{Modules: []Module{ModuleBeacon}, ResolvedAt: &DateRange{After: "-1d"}},
		{Modules: []Module{ModuleBeacon}, Statuses: []string{"x"}, Not: Negate{Statuses: true}},
	} {
		q := Query{V: 2, Filter: f, Sort: DefaultSort()}
		if got := q.RequiredVersion(); got != 2 {
			t.Errorf("filter %+v requires version %d, want 2", f, got)
		}
	}
}

// TestQueueQuery_DeclaresTheLowestVersionItNeeds guards the persisted defaults.
//
// Default queues are written once per Beacon space and use nothing v2 added, so
// stamping them 2 would make a rollback fail to read every queue in the product
// for no benefit whatever.
func TestQueueQuery_DeclaresTheLowestVersionItNeeds(t *testing.T) {
	for _, dq := range DefaultQueues {
		q := dq.build(uuid.New(), []string{"open"}, []string{"closed"})
		if q.V != MinVersion {
			t.Errorf("default queue %q is stamped v%d; it uses nothing v2 added and should declare v%d",
				dq.Name, q.V, MinVersion)
		}
		if err := q.Validate(); err != nil {
			t.Errorf("default queue %q does not validate: %v", dq.Name, err)
		}
	}
}
