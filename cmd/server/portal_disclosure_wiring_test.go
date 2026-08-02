package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The disclosure rule is stated in config.Config.PortalLinkDisclosureAllowed
// and it is worth nothing unless this package calls it.
//
// That is not a hypothetical. The rule this replaced was an EXPRESSION at the
// wiring site — `cfg.PortalLinkDelivery == config.PortalLinkDeliveryLink &&
// !cfg.IsProduction()` — and the reason it survived with both operands set to
// their defaults is that it lived in cmd/server/main.go, a file with no test.
// A truth table in internal/config would have passed against that code too,
// because the truth table does not know who calls it. So the wiring itself is
// asserted here.
//
// Reading the source rather than the behaviour is deliberate, and it is the
// same technique dockerfile_parity_test.go in this package already uses. The
// alternative — booting a server and probing the portal endpoint — needs a
// database, a portal fixture and a requester to assert something that is
// fundamentally a question about one line of wiring.

const mainSource = "main.go"

// discloseAssignment matches a `DiscloseLink:` field in a composite literal and
// captures everything up to the end of the line, so a re-inlined expression is
// visible rather than merely different.
var discloseAssignment = regexp.MustCompile(`(?m)^\s*DiscloseLink:\s*(.+?),?\s*$`)

// wantDisclosureRHS is the only acceptable right-hand side.
//
// Note what this rejects besides a re-inlined expression: `cfg.PortalDiscloseLink`,
// the RAW FLAG, which differs from the method by one word and returns true in
// production. That near-miss is the reason this test compares against an exact
// string instead of merely requiring the word "Disclose".
const wantDisclosureRHS = "cfg.PortalLinkDisclosureAllowed()"

func readMainSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(mainSource) //nolint:gosec // G304 — path is this file's package constant, repo-relative
	if err != nil {
		t.Fatalf("reading %s: %v", mainSource, err)
	}
	// This repository is developed on Windows as well as Linux, and a CRLF
	// checkout would otherwise fail every comparison below for a reason that
	// has nothing to do with the wiring.
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestMainWiresPortalDisclosureToTheConfigRule fails if the wiring site stops
// delegating: if the expression is inlined back, if the raw flag is read
// instead of the rule, or if the helper is renamed without updating main.go.
func TestMainWiresPortalDisclosureToTheConfigRule(t *testing.T) {
	matches := discloseAssignment.FindAllStringSubmatch(readMainSource(t), -1)

	// A guard that matches nothing passes everything. If the field is renamed
	// or the literal restructured, this must fail loudly rather than go quiet.
	if len(matches) == 0 {
		t.Fatalf("no `DiscloseLink:` assignment found in %s. Either the portal wiring "+
			"moved — in which case move this test with it — or the field was renamed. "+
			"Do not delete this guard to make it pass: it is the only thing asserting "+
			"that the disclosure rule in internal/config is the rule the server uses.",
			mainSource)
	}
	if len(matches) > 1 {
		t.Errorf("%s has %d `DiscloseLink:` assignments; the rule must have exactly one "+
			"call site, or one of them can drift from the other", mainSource, len(matches))
	}

	for _, m := range matches {
		if got := strings.TrimSpace(m[1]); got != wantDisclosureRHS {
			t.Errorf("DiscloseLink is wired to %q, want %q.\n\n"+
				"The rule belongs in config.Config.PortalLinkDisclosureAllowed and nowhere "+
				"else. An expression here is how the previous defect shipped: it read "+
				"`cfg.PortalLinkDelivery == config.PortalLinkDeliveryLink && !cfg.IsProduction()`, "+
				"both operands were defaults, and a stock install returned a sign-in "+
				"credential to an unauthenticated caller.",
				got, wantDisclosureRHS)
		}
	}
}

// TestMainDoesNotRederiveTheProductionCheckForDisclosure is the narrower half:
// even a CORRECT second copy of the rule is a defect, because the two copies
// are what drift.
//
// IsProduction() is not banned from this file — a future non-portal use is
// legitimate — so this asserts only that it does not appear on a line that also
// mentions disclosure, which is exactly the re-inlining shape.
func TestMainDoesNotRederiveTheProductionCheckForDisclosure(t *testing.T) {
	for i, line := range strings.Split(readMainSource(t), "\n") {
		code := line
		if idx := strings.Index(code, "//"); idx >= 0 {
			code = code[:idx] // the comments above the wiring quote the old rule on purpose
		}
		if !strings.Contains(code, "IsProduction()") {
			continue
		}
		if strings.Contains(code, "Disclose") {
			t.Errorf("%s line %d re-derives the disclosure rule: %q\n"+
				"Call cfg.PortalLinkDisclosureAllowed() instead — one statement of the rule, "+
				"in internal/config, where the truth table can reach it.",
				mainSource, i+1, strings.TrimSpace(line))
		}
	}
}
