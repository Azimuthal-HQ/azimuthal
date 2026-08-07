// Package search is the cross-module search read path (P6, spec §5 and §7).
//
// One query, ranked results across Codex pages, Beacon tickets and Vector
// project items, filtered per viewer. The fan-out is per module and the merge
// happens here rather than in SQL, which is what ADR-0009 requires.
//
// THE OPERATOR VOCABULARY IS CLOSED, AND UNKNOWN OPERATORS ARE NOT ERRORS
// ----------------------------------------------------------------------
// Two operators exist — `type:` (spelled `module:` too) and `tag:` — and there
// will not be a third without a decision to add one. Search operators get the
// same closed-vocabulary treatment as the saved-view filter fields, for the same
// reason: an open grammar is a promise to keep parsing it.
//
// It diverges from the saved-view precedent in one way, deliberately. Views
// REJECT an unknown filter field (filter.go returns ErrUnknownField), because a
// filter builder shows the user a closed list and an unknown field can only be a
// client bug. A search box takes free text, and free text contains colons — a
// URL, a Go package path, a `time:` in a log line pasted into the box. Refusing
// those would make ordinary searches fail, so an unrecognised `foo:bar` is
// treated as literal text and searched for.
//
// Operators must be stripped BEFORE the tsquery is built, not left for
// PostgreSQL. Unstripped they do not error; they become ordinary lexemes —
// websearch_to_tsquery('english', 'type:beacon widget') parses to
// 'type' & 'beacon' & 'widget' — so the endpoint would quietly search for the
// words "type" and "beacon" and return a plausible, wrong result set.
package search

import (
	"strings"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/tags"
)

// Module names one searchable module. The values are the space-type names the
// rest of the product uses, not new ones.
type Module string

// The three searchable modules.
const (
	ModuleCodex  Module = "codex"
	ModuleBeacon Module = "beacon"
	ModuleVector Module = "vector"
)

// AllModules is the default fan-out: everything the viewer can see.
func AllModules() []Module { return []Module{ModuleCodex, ModuleBeacon, ModuleVector} }

// moduleAliases maps every accepted spelling to its module. Both the module
// names and the entity names are accepted because a user typing into a search
// box reaches for whichever is in mind — "type:page" and "type:codex" mean the
// same thing, and refusing one of them teaches nothing.
var moduleAliases = map[string]Module{
	"codex":   ModuleCodex,
	"page":    ModuleCodex,
	"pages":   ModuleCodex,
	"wiki":    ModuleCodex,
	"beacon":  ModuleBeacon,
	"ticket":  ModuleBeacon,
	"tickets": ModuleBeacon,
	"vector":  ModuleVector,
	"item":    ModuleVector,
	"items":   ModuleVector,
}

// Query is a parsed search request: the free text with operators removed, plus
// whatever the operators narrowed.
type Query struct {
	// Text is what reaches websearch_to_tsquery. Quoted phrases survive intact.
	Text string
	// Modules is the effective fan-out, already resolved — never empty. A
	// caller iterates it and skips whole branches; it does not post-filter.
	Modules []Module
	// TagSlug is the resolved Codex tag slug, or "" for no tag filter.
	TagSlug string
	// modulesRequested records whether the user named modules explicitly, so
	// the response can distinguish "you asked for Codex" from "a tag filter
	// implied Codex".
	modulesRequested bool
}

// TagFiltered reports whether a tag narrowed the query.
func (q Query) TagFiltered() bool { return q.TagSlug != "" }

// ModulesExplicit reports whether the caller named the module set.
func (q Query) ModulesExplicit() bool { return q.modulesRequested }

// Parse splits a raw search string into its free text and its operators.
//
// Parsing is total: every input produces a Query and none produces an error.
// Whether the result is searchable at all is a separate question, answered
// against PostgreSQL's own parser rather than guessed at here — a query can
// consist entirely of stopwords and still look non-empty to any check written
// in Go.
func Parse(raw string) Query {
	q := Query{}
	var kept []string

	for _, tok := range tokenize(raw) {
		if tok.quoted {
			kept = append(kept, `"`+tok.text+`"`)
			continue
		}
		field, value, ok := splitOperator(tok.text)
		if !ok {
			kept = append(kept, tok.text)
			continue
		}
		switch field {
		case "type", "module":
			if m, known := moduleAliases[normaliseAlias(value)]; known {
				q.Modules = appendModule(q.Modules, m)
				q.modulesRequested = true
				continue
			}
			// An unknown module value is literal text, for the same reason an
			// unknown operator is: `type:foo` is more likely a pasted string
			// than a client bug.
			kept = append(kept, tok.text)
		case "tag":
			// Slugified with the repository's ONE slug helper. Never
			// client-side, and never compared raw: a tag's slug is its
			// identity, and `#Design Docs`, `design-docs` and `design_docs`
			// are all the same tag.
			if slug := tags.Slugify(value); slug != "" {
				q.TagSlug = slug
				continue
			}
			kept = append(kept, tok.text)
		default:
			kept = append(kept, tok.text)
		}
	}

	q.Text = strings.Join(kept, " ")
	q.Modules = resolveModules(q.Modules)
	return q
}

// resolveModules turns the requested set into the effective fan-out.
//
// A tag filter is NOT a module filter. Tags are entity-generic (migration
// 055): pages, tickets and project items all carry them, and every module's
// search query has a tag arm — so `tag:foo` is meaningful against any module
// and must not narrow the fan-out. This function narrowed `tag:` queries to
// Codex until v0.4.2, when that was true of the data model; a `tag:` search
// that silently returned only pages today would look like it worked and be
// wrong.
func resolveModules(requested []Module) []Module {
	if len(requested) == 0 {
		return AllModules()
	}
	return requested
}

// normaliseAlias lowercases and trims an operator value. It deliberately does
// NOT slugify: module aliases are a fixed list of words, and slugifying would
// silently accept "ticket_xs" as "ticketxs".
func normaliseAlias(v string) string { return strings.ToLower(strings.TrimSpace(v)) }

// splitOperator splits `field:value` on the FIRST colon. A leading colon, an
// empty value, or a field containing anything but letters is not an operator —
// which keeps "http://example.com" and "::1" out of the grammar.
func splitOperator(tok string) (field, value string, ok bool) {
	i := strings.IndexByte(tok, ':')
	if i <= 0 || i == len(tok)-1 {
		return "", "", false
	}
	field, value = strings.ToLower(tok[:i]), tok[i+1:]
	for _, r := range field {
		if r < 'a' || r > 'z' {
			return "", "", false
		}
	}
	return field, value, true
}

func appendModule(ms []Module, m Module) []Module {
	for _, existing := range ms {
		if existing == m {
			return ms
		}
	}
	return append(ms, m)
}

// token is one lexical unit: a bare word or a quoted phrase.
type token struct {
	text   string
	quoted bool
}

// tokenize splits on whitespace but keeps double-quoted runs together, so a
// colon inside a phrase stays literal and the phrase reaches
// websearch_to_tsquery still quoted — which is what makes it a phrase search
// there rather than a bag of words.
//
// An unterminated quote is closed at end of input rather than rejected.
// PostgreSQL does the same ("unclosed phrase → 'unclos' <-> 'phrase'"), and a
// search box in mid-typing is the common case, not an attack.
func tokenize(raw string) []token {
	var l lexer
	for _, r := range raw {
		l.step(r)
	}
	l.flush(l.inQuote && !l.attached)
	return l.out
}

// lexer carries the tokenizer's state. It exists as a type rather than a set of
// closures so the quote rules can live in their own method — inline, the single
// switch that handles them is past the complexity the linter allows, and the
// arms are genuinely three different situations rather than one.
type lexer struct {
	out     []token
	cur     strings.Builder
	inQuote bool
	// attached is set when the open quote followed a `field:` directly, as in
	// tag:"Design Docs". That quote belongs to the operator's VALUE, so the
	// phrase is absorbed into the current token instead of starting a new one.
	// Otherwise `tag:` flushes alone — a trailing colon, so splitOperator
	// rejects it — and the phrase becomes unrelated search text: a tag filter
	// silently turning into two ordinary words.
	attached bool
	last     rune
}

func (l *lexer) step(r rune) {
	switch {
	case r == '"':
		l.quote()
	case !l.inQuote && isSpace(r):
		l.flush(false)
	default:
		l.cur.WriteRune(r)
	}
	l.last = r
}

func (l *lexer) quote() {
	switch {
	case !l.inQuote && l.last == ':' && l.cur.Len() > 0:
		l.inQuote, l.attached = true, true
	case l.inQuote && l.attached:
		l.inQuote, l.attached = false, false
	default:
		l.flush(l.inQuote)
		l.inQuote = !l.inQuote
	}
}

func (l *lexer) flush(quoted bool) {
	if l.cur.Len() > 0 {
		l.out = append(l.out, token{text: l.cur.String(), quoted: quoted})
		l.cur.Reset()
	}
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
