package doc_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/wiki/doc"
)

// The fourth link in ADR-0012's schema chain, and the one that guards a
// different failure.
//
// The first three links guard TYPE names, whose failure is loud in one
// direction and catastrophic in the other. This one guards ATTRIBUTE names, and
// their failure is neither: rename `panel.kind` on one side and the page still
// publishes, the document still stores correctly, and the panel's kind quietly
// stops reaching the generated search_vector. Nobody finds out until somebody
// searches for it and does not find it.
//
// `internal/core/wiki/doc/schema.json` names the attributes the projection
// reads. This test asserts the projection actually reads each one, by
// projecting a document that carries it and looking for its effect. Its case
// table's key set must EQUAL the manifest's, so:
//
//   - an attribute added to the manifest with no projection behind it fails
//     here rather than shipping as a promise;
//   - an attribute the projection reads but the manifest does not name fails
//     too, because the manifest is what the TypeScript side checks against.
//
// The corresponding editor-side check is in
// `web/src/components/codex/extensions/extensions.test.ts`, which asserts the
// real ProseMirror schema declares every attribute this same manifest names.

// projectionCase is one manifest entry and what its attribute does to the
// markdown projection.
type projectionCase struct {
	// doc is a whole document carrying the node or mark under test.
	doc string
	// wants are substrings the projection must contain.
	wants []string
	// notWants are substrings it must not — used where the interesting
	// behaviour is an absence, as it is for an unresolved link.
	notWants []string
}

const (
	sampleAttachmentID = "3f1b1d3a-0000-4000-8000-00000000aaaa"
	samplePageID       = "3f1b1d3a-0000-4000-8000-00000000bbbb"
)

// projectionCases is keyed "<kind>.<type>.<attr>", matching the manifest.
var projectionCases = map[string]projectionCase{
	"node.heading.level": {
		doc:   docWith(`{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Escalation"}]}`),
		wants: []string{"### Escalation"},
	},
	"node.codeBlock.language": {
		doc:   docWith(`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]}`),
		wants: []string{"```go", "x := 1"},
	},
	"node.image.attachment_id": {
		doc:   docWith(fmt.Sprintf(`{"type":"image","attrs":{"attachment_id":%q,"alt":"A diagram"}}`, sampleAttachmentID)),
		wants: []string{"![A diagram](attachment:" + sampleAttachmentID + ")"},
	},
	"node.panel.kind": {
		doc:   docWith(`{"type":"panel","attrs":{"kind":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Mind the gap"}]}]}`),
		wants: []string{"**WARNING**", "Mind the gap"},
	},
	"node.expand.title": {
		doc:   docWith(`{"type":"expand","attrs":{"title":"How the rota works"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Body"}]}]}`),
		wants: []string{"How the rota works", "Body"},
	},
	"node.statusLozenge.text": {
		doc:   docWith(`{"type":"paragraph","content":[{"type":"statusLozenge","attrs":{"text":"IN REVIEW","color":"blue"}}]}`),
		wants: []string{"`IN REVIEW`"},
	},
	"node.pageInclude.page_id": {
		doc:   docWith(fmt.Sprintf(`{"type":"pageInclude","attrs":{"page_id":%q}}`, samplePageID)),
		wants: []string{"[Included page: " + samplePageID + "]"},
	},
	"node.inlineTag.label": {
		doc:   docWith(`{"type":"paragraph","content":[{"type":"text","text":"see "},{"type":"inlineTag","attrs":{"label":"design_docs"}}]}`),
		wants: []string{"#design_docs"},
	},
	"mark.link.href": {
		doc:   docWith(`{"type":"paragraph","content":[{"type":"text","text":"the site","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}]}]}`),
		wants: []string{"[the site](https://example.com/x)"},
	},
	"mark.link.page_id": {
		doc:   docWith(fmt.Sprintf(`{"type":"paragraph","content":[{"type":"text","text":"the rota","marks":[{"type":"link","attrs":{"href":null,"page_id":%q}}]}]}`, samplePageID)),
		wants: []string{"[the rota](page:" + samplePageID + ")"},
	},
	"mark.link.target_title": {
		// An unresolved wikilink names a page that does not exist yet, so it has
		// no destination to project — never `[text]()`, which would render as a
		// broken link in every legacy reader and claim a destination that is not
		// there.
		//
		// The ALIASED form is the case that proves the attribute is read: with
		// `[[Runbook|the rota]]` the stored text is "the rota", so the target
		// title exists nowhere else and a projection of the text alone would
		// make the page unfindable by the name it explicitly references.
		doc:      docWith(`{"type":"paragraph","content":[{"type":"text","text":"the rota","marks":[{"type":"link","attrs":{"href":null,"page_id":null,"target_title":"Runbook"}}]}]}`),
		wants:    []string{"the rota", "[New page: Runbook]"},
		notWants: []string{"]("},
	},
}

func docWith(node string) string {
	return `{"type":"doc","content":[` + node + `]}`
}

func TestProjectedAttrs_ManifestAndProjectionAgree(t *testing.T) {
	manifestKeys := manifestAttrKeys()
	caseKeys := make([]string, 0, len(projectionCases))
	for key := range projectionCases {
		caseKeys = append(caseKeys, key)
	}
	sort.Strings(caseKeys)

	// Set equality, both directions, and named so a failure says which side is
	// missing rather than dumping two lists.
	for _, key := range manifestKeys {
		if _, ok := projectionCases[key]; !ok {
			t.Errorf("schema.json names %s as a projected attribute, but no case here proves the projection reads it. "+
				"Add one, or remove it from the manifest — an unproven entry is a promise, not a guard.", key)
		}
	}
	inManifest := make(map[string]bool, len(manifestKeys))
	for _, key := range manifestKeys {
		inManifest[key] = true
	}
	for _, key := range caseKeys {
		if !inManifest[key] {
			t.Errorf("this test proves the projection reads %s, but schema.json does not name it. "+
				"The TypeScript side checks against the manifest, so an attribute missing from it is unguarded there.", key)
		}
	}
}

func TestProjectedAttrs_ProjectionReadsEachOne(t *testing.T) {
	for key, tc := range projectionCases {
		t.Run(key, func(t *testing.T) {
			markdown, err := doc.ToMarkdown(json.RawMessage(tc.doc))
			if err != nil {
				t.Fatalf("projecting: %v", err)
			}
			for _, want := range tc.wants {
				if !strings.Contains(markdown, want) {
					t.Errorf("the projection lost %s.\nwanted to find: %q\ngot:\n%s", key, want, markdown)
				}
			}
			for _, notWant := range tc.notWants {
				if strings.Contains(markdown, notWant) {
					t.Errorf("the projection of %s should not contain %q.\ngot:\n%s", key, notWant, markdown)
				}
			}
		})
	}
}

// TestProjectedAttrs_RenamingAnAttributeIsCaught is the mutation check: it
// proves each case would actually fail if the projection stopped reading the
// attribute, rather than passing because the value happened to appear in the
// text anyway.
//
// It does that by projecting the same node with the attribute REMOVED and
// requiring the expectation to stop holding. A case that still passes without
// its attribute is asserting nothing about that attribute.
func TestProjectedAttrs_RenamingAnAttributeIsCaught(t *testing.T) {
	for key, tc := range projectionCases {
		t.Run(key, func(t *testing.T) {
			attr := key[strings.LastIndex(key, ".")+1:]
			stripped := stripAttr(t, tc.doc, attr)

			markdown, err := doc.ToMarkdown(json.RawMessage(stripped))
			if err != nil {
				// A node that will not project at all without its attribute is
				// an even stronger form of "the attribute is load-bearing".
				return
			}
			for _, want := range tc.wants {
				if !strings.Contains(markdown, want) {
					return // The expectation broke, which is what we wanted to see.
				}
			}
			for _, notWant := range tc.notWants {
				if strings.Contains(markdown, notWant) {
					return
				}
			}
			t.Errorf("removing %q changed nothing about the projection of %s, so the case above "+
				"would pass with the projection deleted. It asserts nothing.\ngot:\n%s", attr, key, markdown)
		})
	}
}

// TestUnresolvedLink_ProjectsAsItsTextWhenTheTargetIsTheText covers the
// unaliased form, which the manifest case above cannot: with `[[Runbook]]` the
// text and the target are the same word, so repeating it would put the same
// term in the index twice and read as a stutter to anybody looking at the
// markdown.
func TestUnresolvedLink_ProjectsAsItsTextWhenTheTargetIsTheText(t *testing.T) {
	document := docWith(`{"type":"paragraph","content":[{"type":"text","text":"Runbook","marks":[{"type":"link","attrs":{"href":null,"page_id":null,"target_title":"Runbook"}}]}]}`)
	markdown, err := doc.ToMarkdown(json.RawMessage(document))
	if err != nil {
		t.Fatalf("projecting: %v", err)
	}
	if strings.TrimSpace(markdown) != "Runbook" {
		t.Errorf("an unresolved link whose target is its own text projects as that text alone, got %q", markdown)
	}
}

// TestLink_WithNoDestinationAtAllProjectsAsItsText pins the malformed case
// alongside the unresolved one. It used to emit `[text]()`, which is a link a
// reader can click and go nowhere.
func TestLink_WithNoDestinationAtAllProjectsAsItsText(t *testing.T) {
	document := docWith(`{"type":"paragraph","content":[{"type":"text","text":"orphan","marks":[{"type":"link","attrs":{"href":""}}]}]}`)
	markdown, err := doc.ToMarkdown(json.RawMessage(document))
	if err != nil {
		t.Fatalf("projecting: %v", err)
	}
	if strings.TrimSpace(markdown) != "orphan" {
		t.Errorf("a link with no destination projects as its text alone, got %q", markdown)
	}
}

// stripAttr removes one attribute wherever it appears in a document, which is
// the closest a JSON-level test can get to "the projection looked for a
// different name".
func stripAttr(t *testing.T, document, attr string) string {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		t.Fatalf("decoding the case document: %v", err)
	}
	stripped := stripAttrValue(value, attr)
	out, err := json.Marshal(stripped)
	if err != nil {
		t.Fatalf("re-encoding the case document: %v", err)
	}
	return string(out)
}

func stripAttrValue(value any, attr string) any {
	switch v := value.(type) {
	case map[string]any:
		if attrs, ok := v["attrs"].(map[string]any); ok {
			delete(attrs, attr)
		}
		for key, member := range v {
			v[key] = stripAttrValue(member, attr)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = stripAttrValue(item, attr)
		}
		return v
	default:
		return value
	}
}

// manifestAttrKeys flattens the manifest into "<kind>.<type>.<attr>" keys.
func manifestAttrKeys() []string {
	var out []string
	for nodeType, attrs := range doc.ProjectedNodeAttrs() {
		for _, attr := range attrs {
			out = append(out, "node."+nodeType+"."+attr)
		}
	}
	for markType, attrs := range doc.ProjectedMarkAttrs() {
		for _, attr := range attrs {
			out = append(out, "mark."+markType+"."+attr)
		}
	}
	sort.Strings(out)
	return out
}
