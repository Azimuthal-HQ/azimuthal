// Package jql classifies JQL against Azimuthal's saved-view filter vocabulary.
//
// # This is an assessment, not an engine
//
// Nothing here evaluates a query, resolves an issue, or produces SQL. The only
// question asked is: could a saved view express this clause at all? That is a
// static question about vocabulary, and answering it needs just enough
// structure to split a query into clauses and read each one's field, operator
// and shape — not a grammar, not precedence, not a parse tree.
//
// The distinction matters because the target is deliberately not a query
// language. internal/core/views/filter.go states it directly: the filter
// document is a record, not a tree, with eight named fields, no operator set,
// and no nesting. Fields AND together and values within a field OR. So a JQL
// clause is expressible when it collapses into one of those eight fields, and
// the interesting work is naming precisely why the others do not.
package jql

import (
	"strings"
	"unicode"
)

// tokenKind is the lexical class of one token.
type tokenKind int

const (
	tokWord tokenKind = iota
	tokString
	tokOperator
	tokLParen
	tokRParen
	tokComma
)

// token is one lexeme, with the raw text and whether it was quoted.
//
// Quoting is retained because it is semantic here: a quoted "AND" is a value,
// not a connective, and a classifier that lost the distinction would split a
// clause in the middle of a status name.
type token struct {
	kind   tokenKind
	text   string
	quoted bool
}

// operatorRunes are the characters an operator can be built from.
const operatorRunes = "=!<>~"

// lex splits a JQL string into tokens.
//
// It is deliberately permissive: JQL this cannot lex is reported as
// unclassifiable rather than rejected, because the assessor's job is to tell a
// reader what would happen to their filters, and refusing to say anything about
// an odd one is the least useful possible answer.
func lex(input string) []token {
	var out []token
	runes := []rune(input)

	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case unicode.IsSpace(r):
			i++
		case r == '(':
			out = append(out, token{kind: tokLParen, text: "("})
			i++
		case r == ')':
			out = append(out, token{kind: tokRParen, text: ")"})
			i++
		case r == ',':
			out = append(out, token{kind: tokComma, text: ","})
			i++
		case r == '"' || r == '\'':
			tok, next := lexQuoted(runes, i)
			out = append(out, tok)
			i = next
		case strings.ContainsRune(operatorRunes, r):
			tok, next := lexOperator(runes, i)
			out = append(out, tok)
			i = next
		default:
			tok, next := lexWord(runes, i)
			out = append(out, tok)
			i = next
		}
	}
	return out
}

// lexQuoted reads a quoted value, honouring backslash escapes.
//
// An unterminated quote consumes to end of input rather than erroring: a
// truncated saved filter should still be classified on what it does say.
func lexQuoted(runes []rune, start int) (token, int) {
	quote := runes[start]
	var b strings.Builder
	i := start + 1
	for i < len(runes) {
		if runes[i] == '\\' && i+1 < len(runes) {
			b.WriteRune(runes[i+1])
			i += 2
			continue
		}
		if runes[i] == quote {
			i++
			break
		}
		b.WriteRune(runes[i])
		i++
	}
	return token{kind: tokString, text: b.String(), quoted: true}, i
}

// lexOperator reads a comparison operator, longest match first so ">=" does not
// lex as ">" followed by "=".
func lexOperator(runes []rune, start int) (token, int) {
	i := start
	for i < len(runes) && strings.ContainsRune(operatorRunes, runes[i]) {
		i++
	}
	return token{kind: tokOperator, text: string(runes[start:i])}, i
}

// lexWord reads a bare word: a field name, a keyword, a function call, an
// unquoted value, or a custom-field reference such as cf[10001].
func lexWord(runes []rune, start int) (token, int) {
	i := start
	depth := 0
	for i < len(runes) {
		next, done := stepWord(runes, start, i, &depth)
		if done {
			break
		}
		i = next
	}
	return token{kind: tokWord, text: string(runes[start:i])}, i
}

// stepWord advances one position inside a bare word, reporting whether the word
// has ended. depth tracks a cf[...] bracket.
func stepWord(runes []rune, start, i int, depth *int) (next int, done bool) {
	switch r := runes[i]; {
	case r == '[':
		*depth++
		return i + 1, false
	case r == ']' && *depth > 0:
		*depth--
		return i + 1, false
	case r == '(' && i > start:
		// A function's argument list is part of the word, so currentUser() and
		// membersOf("team") survive as single tokens.
		if consumed := consumeCall(runes, i); consumed > i {
			return consumed, false
		}
		return i, true
	case *depth == 0 && endsWord(r):
		return i, true
	default:
		return i + 1, false
	}
}

func endsWord(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("(),", r) ||
		strings.ContainsRune(operatorRunes, r)
}

// consumeCall consumes a balanced parenthesised argument list starting at open.
// Returns open unchanged when the parens do not balance, so a stray "(" is left
// for the caller to treat as grouping.
func consumeCall(runes []rune, open int) int {
	depth := 0
	for i := open; i < len(runes); i++ {
		switch runes[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		case '"', '\'':
			_, next := lexQuoted(runes, i)
			i = next - 1
		}
	}
	return open
}

// isConnective reports whether an unquoted word joins clauses.
func isConnective(t token) bool {
	if t.quoted {
		return false
	}
	switch strings.ToUpper(t.text) {
	case "AND", "OR", "&&", "||":
		return true
	default:
		return false
	}
}

// isNot reports whether a token is the negation keyword.
func isNot(t token) bool {
	return !t.quoted && (strings.EqualFold(t.text, "NOT") || t.text == "!")
}
