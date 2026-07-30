package search

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParse_ClosedVocabulary covers the whole operator grammar and, more
// importantly, everything that is deliberately NOT part of it.
//
// The negative half is the point. Every case whose expectation keeps the token
// in Text would pass a parser that recognised no operators at all, so the
// positive cases carry the operator assertions and the negative cases carry the
// "still literal text" ones; a parser that greedily treated every colon as an
// operator fails the second half, and one that treated none as an operator fails
// the first.
func TestParse_ClosedVocabulary(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantText    string
		wantModules []Module
		wantTag     string
	}{
		{
			name:        "bare text fans out to every module",
			raw:         "payment gateway",
			wantText:    "payment gateway",
			wantModules: AllModules(),
		},
		{
			name:        "type: by module name",
			raw:         "type:beacon outage",
			wantText:    "outage",
			wantModules: []Module{ModuleBeacon},
		},
		{
			name:        "type: by entity name, the brief's spelling",
			raw:         "type:page runbook",
			wantText:    "runbook",
			wantModules: []Module{ModuleCodex},
		},
		{
			name:        "module: is the same operator",
			raw:         "module:vector rollout",
			wantText:    "rollout",
			wantModules: []Module{ModuleVector},
		},
		{
			name:        "two type: terms union, and repeats collapse",
			raw:         "type:page type:ticket type:page kernel",
			wantText:    "kernel",
			wantModules: []Module{ModuleCodex, ModuleBeacon},
		},
		{
			name:        "tag: slugifies through the one slug helper",
			raw:         `tag:"Design Docs" latency`,
			wantText:    "latency",
			wantModules: []Module{ModuleCodex},
			wantTag:     "design_docs",
		},
		{
			name:        "tag: hyphens and underscores are the same tag",
			raw:         "tag:design-docs",
			wantText:    "",
			wantModules: []Module{ModuleCodex},
			wantTag:     "design_docs",
		},
		{
			name: "a tag filter narrows to Codex even against an explicit type:",
			// Tags exist only on pages, so type:ticket + tag: can only ever
			// return nothing. Codex alone is the useful answer.
			raw:         "type:ticket tag:runbooks disk",
			wantText:    "disk",
			wantModules: []Module{ModuleCodex},
			wantTag:     "runbooks",
		},
		{
			name:        "quoted phrase survives, quotes included",
			raw:         `"payment gateway" timeout`,
			wantText:    `"payment gateway" timeout`,
			wantModules: AllModules(),
		},
		{
			name:        "a colon INSIDE a phrase is literal, not an operator",
			raw:         `"type:beacon"`,
			wantText:    `"type:beacon"`,
			wantModules: AllModules(),
		},
		{
			name:        "an unknown operator is literal text, not an error",
			raw:         "status:open widget",
			wantText:    "status:open widget",
			wantModules: AllModules(),
		},
		{
			name:        "an unknown type: VALUE is literal text",
			raw:         "type:sprint widget",
			wantText:    "type:sprint widget",
			wantModules: AllModules(),
		},
		{
			name:        "a URL is not an operator",
			raw:         "https://example.com/runbook",
			wantText:    "https://example.com/runbook",
			wantModules: AllModules(),
		},
		{
			name:        "a bare colon and a trailing colon are not operators",
			raw:         ":1 tag: type:",
			wantText:    ":1 tag: type:",
			wantModules: AllModules(),
		},
		{
			name:        "a non-alphabetic field is not an operator",
			raw:         "v1.2:beta",
			wantText:    "v1.2:beta",
			wantModules: AllModules(),
		},
		{
			name:        "operators alone leave the text empty",
			raw:         "type:beacon",
			wantText:    "",
			wantModules: []Module{ModuleBeacon},
		},
		{
			name:        "an unterminated quote closes at end of input",
			raw:         `"payment gateway`,
			wantText:    `"payment gateway"`,
			wantModules: AllModules(),
		},
		{
			name:        "case is not significant for operators",
			raw:         "TYPE:Beacon Outage",
			wantText:    "Outage",
			wantModules: []Module{ModuleBeacon},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.raw)
			require.Equal(t, tc.wantText, got.Text, "free text")
			require.Equal(t, tc.wantModules, got.Modules, "effective module fan-out")
			require.Equal(t, tc.wantTag, got.TagSlug, "tag slug")
		})
	}
}

// TestParse_OperatorsNeverReachTheTsquery is the guard for the failure that has
// no symptom: an operator left in the text becomes an ordinary lexeme, so
// `type:beacon widget` searches for the WORDS "type" and "beacon" and returns a
// plausible wrong set instead of erroring.
//
// Fails-before: delete the `case "type", "module"` arm and this fails, while a
// test that only asserted Modules would still pass because the default fan-out
// is every module anyway.
func TestParse_OperatorsNeverReachTheTsquery(t *testing.T) {
	for _, raw := range []string{
		"type:beacon widget",
		"module:codex widget",
		"tag:runbooks widget",
		"type:page tag:runbooks widget",
	} {
		got := Parse(raw)
		for _, banned := range []string{"type:", "module:", "tag:", "beacon", "codex", "runbooks"} {
			require.NotContains(t, strings.ToLower(got.Text), banned,
				"%q: operator residue in the tsquery text (%q)", raw, got.Text)
		}
		require.Equal(t, "widget", got.Text)
	}
}

// TestParse_IsTotal proves parsing never fails and never returns an empty module
// set, for inputs chosen to break a hand-rolled lexer. An empty Modules slice
// would make the fan-out silently search nothing while reporting success.
//
// This is the useful half of "fuzz the parser": the tsquery builder itself
// cannot be fuzzed for errors, because websearch_to_tsquery does not have an
// error path — stopwords, 3000 characters of one letter and pure punctuation all
// yield an empty tsquery and at most a NOTICE. What can break is this lexer.
func TestParse_IsTotal(t *testing.T) {
	inputs := []string{
		"", " ", "\t\n", `"`, `""`, `"""`, ":", "::", ":::", "a:", ":a",
		"tag:", "tag::", "tag:::", "type:", "type::", `tag:"`, `tag:""`,
		"&|!():*", "<->", "!!! &&& ||| (((", "the of a",
		strings.Repeat("z", 3000), strings.Repeat("tag:x ", 500),
		strings.Repeat(`"`, 100), "type:beacon type:codex type:vector",
		"\x00", "🙂 tag:🙂", "tag:🙂",
	}
	for _, raw := range inputs {
		got := Parse(raw)
		require.NotEmpty(t, got.Modules, "input %q produced an empty fan-out", raw)
		for _, m := range got.Modules {
			require.Contains(t, AllModules(), m, "input %q produced an unknown module %q", raw, m)
		}
	}
}

// TestParse_TagOnlyEmojiSlugifiesAway records what happens when a tag operator's
// value slugifies to nothing: the token stays literal text rather than becoming
// a tag filter for the empty slug, which would match every page carrying no tag
// at all.
func TestParse_TagOnlyEmojiSlugifiesAway(t *testing.T) {
	got := Parse("tag:🙂")
	require.Empty(t, got.TagSlug, "a slug that empties out must not become a tag filter")
	require.Equal(t, "tag:🙂", got.Text, "the unusable operator stays literal text")
	require.Equal(t, AllModules(), got.Modules, "and it must not narrow to Codex")
}
