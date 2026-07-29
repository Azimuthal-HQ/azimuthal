package assess

import (
	"fmt"
	"io"
	"sort"

	"github.com/Azimuthal-HQ/azimuthal/internal/assess/confluence"
)

// SourceConfluence names the Confluence export in collision reports.
const SourceConfluence = "Confluence space"

// confluenceCollector accumulates what a Confluence assessment needs.
type confluenceCollector struct {
	spaces      []confluenceSpace
	pages       int // Page objects, revisions and trash included
	livePages   int // Page objects with contentStatus = current
	blogPosts   int
	comments    int
	attachs     int
	users       int
	groups      int
	memberships int
	labels      int
	bodies      int
	// macros is the whole-space macro census, accumulated body by body and
	// never retaining a body.
	macros *confluence.BodyCensus
	// cdataEscapes counts bodies carrying Confluence's ambiguous "]] >"
	// rewrite of a CDATA terminator.
	cdataEscapes    int
	truncatedBodies int
}

type confluenceSpace struct {
	key  string
	name string
}

func newConfluenceCollector() *confluenceCollector {
	return &confluenceCollector{macros: confluence.NewBodyCensus()}
}

// AssessConfluence reads a Confluence entities.xml and folds it into the
// ledger.
func AssessConfluence(r io.Reader, l *Ledger, keys *KeyRegistry) (*confluence.Census, error) {
	c := newConfluenceCollector()
	census, err := c.scan(r)
	if err != nil {
		return census, err
	}
	c.fold(l, keys, census)
	return census, nil
}

func (c *confluenceCollector) scan(r io.Reader) (*confluence.Census, error) {
	s := confluence.NewScanner().
		On("Space", c.onSpace).
		On("Page", c.onPage).
		On("BlogPost", func(confluence.Object) error { c.blogPosts++; return nil }).
		On("BodyContent", c.onBody).
		On("Comment", func(confluence.Object) error { c.comments++; return nil }).
		On("Attachment", func(confluence.Object) error { c.attachs++; return nil }).
		On("ConfluenceUserImpl", func(confluence.Object) error { c.users++; return nil }).
		On("InternalGroup", func(confluence.Object) error { c.groups++; return nil }).
		On("HibernateMembership", func(confluence.Object) error { c.memberships++; return nil }).
		On("Labelling", func(confluence.Object) error { c.labels++; return nil })

	census, err := s.Scan(r)
	if err != nil {
		return census, fmt.Errorf("scanning Confluence export: %w", err)
	}
	return census, nil
}

func (c *confluenceCollector) onSpace(o confluence.Object) error {
	c.spaces = append(c.spaces, confluenceSpace{key: o.Prop("key"), name: o.Prop("name")})
	return nil
}

func (c *confluenceCollector) onPage(o confluence.Object) error {
	c.pages++
	if o.Prop("contentStatus") == confluence.ContentStatusCurrent {
		c.livePages++
	}
	return nil
}

// onBody censuses one page body and releases it.
//
// This is the memory-critical path: a body holds a whole page of storage
// format, and a large space has thousands. The census is merged and the body
// itself goes out of scope immediately, so peak footprint is one body rather
// than the whole space.
func (c *confluenceCollector) onBody(o confluence.Object) error {
	body := o.Prop("body")
	c.bodies++
	if n := confluence.CountCDATATerminatorEscapes(body); n > 0 {
		c.cdataEscapes += n
	}
	census := confluence.ScanBodyString(body)
	if census.Truncated {
		c.truncatedBodies++
	}
	c.macros.Merge(census)
	return nil
}

func (c *confluenceCollector) fold(l *Ledger, keys *KeyRegistry, census *confluence.Census) {
	c.foldSpaces(l, keys)
	c.foldPages(l)
	c.foldMacros(l)
	c.foldPeople(l)
	c.foldRemainder(l, census)
}

func (c *confluenceCollector) foldSpaces(l *Ledger, keys *KeyRegistry) {
	cl := l.Class("Confluence spaces → spaces")
	cl.Observed = len(c.spaces)
	clean, coerced := 0, 0
	var names []string

	for _, s := range c.spaces {
		o := keys.Add(SourceConfluence, s.key, 0)
		if o.Coercion {
			coerced++
			names = append(names, fmt.Sprintf("%s → %s", s.key, o.Coerced))
			continue
		}
		clean++
	}
	sort.Strings(names)
	cl.Add(VerdictClean, clean, "the space key already satisfies ^[A-Z0-9]{1,10}$")
	cl.Add(VerdictApproximated, coerced,
		"Confluence allows lowercase, punctuated and longer space keys than ^[A-Z0-9]{1,10}$ accepts, so the key must be coerced — and two keys that differed only in the stripped characters stop being distinct",
		names...)
}

func (c *confluenceCollector) foldPages(l *Ledger) {
	cl := l.Class("Confluence pages → Codex pages")
	cl.Observed = c.pages
	cl.Notes = append(cl.Notes,
		fmt.Sprintf("%d of these are live pages (contentStatus=current); the rest are historical revisions and trashed content, which a space export carries as separate Page objects", c.livePages),
		"a page whose document has no matching page_revisions row refuses overwrite, so an import must write the revision alongside the document rather than after it")

	cl.Add(VerdictClean, c.livePages,
		"a live page maps onto a Codex page; how much of its body survives is assessed in the macro class below")
	cl.Add(VerdictApproximated, maxInt(c.pages-c.livePages, 0),
		"historical revisions and trashed pages are separate Page objects; Azimuthal keeps page_revisions, so history can be carried, but the revision model is not Confluence's and version comments and per-revision authorship may not line up")

	blogs := l.Class("Confluence blog posts")
	blogs.Observed = c.blogPosts
	blogs.Add(VerdictApproximated, c.blogPosts,
		"there is no blog model; a blog post can only become an ordinary page, losing its date-based addressing")

	comments := l.Class("Confluence comments")
	comments.Observed = c.comments
	comments.Add(VerdictClean, c.comments,
		"page comments map onto the comments table, which accepts a page as its entity and models threading through parent_id")

	att := l.Class("Confluence attachments")
	att.Observed = c.attachs
	att.Add(VerdictClean, c.attachs,
		"attachments hang off a page, which migration 027 allows as an attachment entity type")

	labels := l.Class("Confluence labels")
	labels.Observed = c.labels
	labels.Add(VerdictUnmappable, c.labels,
		"pages carry no labels; project_items.labels exists but pages have no equivalent column, so page labels have nowhere to go")
}

// foldMacros is the Confluence half's centre of gravity: what a page body is
// actually made of, and how much of it the Codex vocabulary understands.
func (c *confluenceCollector) foldMacros(l *Ledger) {
	cl := l.Class("Confluence macros → Codex nodes")
	cl.Derived = true // macro occurrences inside bodies, not exported rows
	cl.Observed = c.macros.MacroTotal()

	counts := map[Verdict]int{}
	names := map[Verdict][]string{}
	for _, name := range confluence.SortedNames(c.macros.Macros) {
		n := c.macros.Macros[name]
		_, v, _ := ClassifyConfluenceMacro(name)
		counts[v] += n
		names[v] = append(names[v], fmt.Sprintf("%s (%d)", name, n))
	}

	cl.Add(VerdictClean, counts[VerdictClean],
		"the macro has a first-class Codex node and arrives as native content", names[VerdictClean]...)
	cl.Add(VerdictApproximated, counts[VerdictApproximated],
		"a Codex node holds it but not everything about it — a panel's custom title and colour, an excerpt include's partial transclusion", names[VerdictApproximated]...)
	cl.Add(VerdictPreserved, counts[VerdictPreserved],
		"no implemented node covers it, so ADR-0012 keeps it verbatim in an unknownContent carrier: it survives a round trip and can be rendered later, but nothing understands it today",
		names[VerdictPreserved]...)

	if c.cdataEscapes > 0 {
		cl.Notes = append(cl.Notes, fmt.Sprintf(
			"%d body fragments contain Confluence's \"]] >\" rewrite of a CDATA terminator; the rewrite is ambiguous, so content that genuinely contained \"]] >\" cannot be told apart from content that contained \"]]>\"", c.cdataEscapes))
	}
	if c.truncatedBodies > 0 {
		cl.Notes = append(cl.Notes, fmt.Sprintf(
			"%d page bodies could not be parsed to the end; what they did contain is counted above", c.truncatedBodies))
	}
}

func (c *confluenceCollector) foldPeople(l *Ledger) {
	users := l.Class("Confluence users → people")
	users.Observed = c.users
	users.Add(VerdictApproximated, c.users,
		"users match on email; a ConfluenceUserImpl carrying only a user key and no address cannot be matched, and unmatched authors must be invited before their authorship resolves")

	groups := l.Class("Confluence groups → teams")
	groups.Observed = c.groups
	groups.Add(VerdictApproximated, c.groups,
		"a group becomes a team, but space permissions must be re-expressed as grants — Azimuthal reaches spaces through grants, never through group rows")

	m := l.Class("Confluence group memberships")
	m.Observed = c.memberships
	m.Add(VerdictApproximated, c.memberships,
		"a membership survives only for a user who matched by email")
}

func (c *confluenceCollector) foldRemainder(l *Ledger, census *confluence.Census) {
	bodies := l.Class("Confluence body content")
	bodies.Observed = c.bodies
	bodies.Add(VerdictClean, c.bodies,
		"a BodyContent object is the page body itself; it is not a separate entity in Azimuthal, where the document lives on the page row")

	other := l.Class("Other Confluence objects")
	remaining := census.Total - c.claimedObjects()
	other.Observed = remaining
	other.Add(VerdictUnmappable, remaining,
		"object classes this assessment does not classify — permissions, content properties, templates and plugin objects. They are counted so the totals reconcile, and named below so nothing is silently omitted",
		c.unclassifiedNames(census)...)
}

func (c *confluenceCollector) claimedObjects() int {
	return len(c.spaces) + c.pages + c.blogPosts + c.comments + c.attachs +
		c.users + c.groups + c.memberships + c.labels + c.bodies
}

func (c *confluenceCollector) unclassifiedNames(census *confluence.Census) []string {
	claimed := map[string]bool{
		"Space": true, "Page": true, "BlogPost": true, "BodyContent": true,
		"Comment": true, "Attachment": true, "ConfluenceUserImpl": true,
		"InternalGroup": true, "HibernateMembership": true, "Labelling": true,
	}
	var out []string
	for _, name := range census.SortedClassNames() {
		if !claimed[name] {
			out = append(out, fmt.Sprintf("%s (%d)", name, census.Objects[name]))
		}
	}
	const limit = 40
	if len(out) > limit {
		rest := len(out) - limit
		out = append(out[:limit], fmt.Sprintf("… and %d more object classes", rest))
	}
	return out
}
