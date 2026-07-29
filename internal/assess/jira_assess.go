package assess

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Azimuthal-HQ/azimuthal/internal/assess/jira"
	"github.com/Azimuthal-HQ/azimuthal/internal/assess/jql"
)

// SourceJira names the Jira export in collision reports and ledger notes.
const SourceJira = "Jira project"

// jiraCollector accumulates the rows a Jira assessment needs.
//
// Only the fields that decide a verdict are kept, and only for the entity
// classes that produce one. Everything else in the export is counted by the
// scanner and never materialised, which is what keeps a multi-gigabyte
// entities.xml inside a bounded footprint.
type jiraCollector struct {
	projects    []jiraProject
	issueTypes  map[string]int // issue type name -> issue count
	statuses    map[string]int
	priorities  map[string]int
	fieldTypes  map[string]int // custom field type key -> definition count
	fieldValues int
	actions     int // every Action row; Action is general activity, not only comments
	comments    int
	restricted  int // comments carrying a group or role visibility restriction
	attachments int
	users       int
	groups      int
	memberships int
	links       int
	worklogs    int
	filters     []jql.Query
	issueKeys   map[string]int // project id -> issue count
	issues      int

	// Jira stores an issue's type, status and priority as ids referencing
	// lookup rows elsewhere in the same file. Resolving them matters for
	// correctness, not presentation: without it the assessor classifies the
	// literal strings "1" and "3" as though they were type names, and reports
	// that none of them match a seeded kind.
	typeNames     map[string]string
	statusNames   map[string]string
	priorityNames map[string]string
	lookupRows    int
}

type jiraProject struct {
	id   string
	key  string
	name string
}

func newJiraCollector() *jiraCollector {
	return &jiraCollector{
		issueTypes:    map[string]int{},
		statuses:      map[string]int{},
		priorities:    map[string]int{},
		fieldTypes:    map[string]int{},
		issueKeys:     map[string]int{},
		typeNames:     map[string]string{},
		statusNames:   map[string]string{},
		priorityNames: map[string]string{},
	}
}

// assessJiraStream reads a Jira entities.xml and folds it into the ledger.
//
// The census is the authority on how many entities exist; the collectors decide
// where each lands. Reconcile then checks the two agree, so a classifier that
// forgets a case fails a test rather than shrinking the totals.
func assessJiraStream(r io.Reader, l *Ledger, keys *KeyRegistry) (*jira.Census, []jql.Query, error) {
	c := newJiraCollector()
	census, err := c.scan(r)
	if err != nil {
		return census, nil, err
	}
	c.fold(l, keys, census)
	return census, c.filters, nil
}

func (c *jiraCollector) scan(r io.Reader) (*jira.Census, error) {
	s := jira.NewScanner().
		On("Project", c.onProject).
		On("Issue", c.onIssue).
		On("Action", c.onAction).
		On("CustomField", c.onCustomField).
		On("CustomFieldValue", func(jira.Row) error { c.fieldValues++; return nil }).
		On("FileAttachment", func(jira.Row) error { c.attachments++; return nil }).
		On("ApplicationUser", func(jira.Row) error { c.users++; return nil }).
		On("OSGroup", func(jira.Row) error { c.groups++; return nil }).
		On("OSMembership", func(jira.Row) error { c.memberships++; return nil }).
		On("IssueLink", func(jira.Row) error { c.links++; return nil }).
		On("Worklog", func(jira.Row) error { c.worklogs++; return nil }).
		On("SearchRequest", c.onSearchRequest).
		On("IssueType", c.lookupInto(c.typeNames)).
		On("Status", c.lookupInto(c.statusNames)).
		On("Priority", c.lookupInto(c.priorityNames))

	census, err := s.Scan(r)
	if err != nil {
		return census, fmt.Errorf("scanning Jira export: %w", err)
	}
	return census, nil
}

func (c *jiraCollector) onProject(r jira.Row) error {
	c.projects = append(c.projects, jiraProject{id: r.Get("id"), key: r.Get("key"), name: r.Get("name")})
	return nil
}

func (c *jiraCollector) onIssue(r jira.Row) error {
	c.issues++
	c.issueKeys[r.Get("project")]++
	countIfSet(c.issueTypes, r.Get("type"))
	countIfSet(c.statuses, r.Get("status"))
	countIfSet(c.priorities, r.Get("priority"))
	return nil
}

// onAction counts comments and, separately, the ones carrying a visibility
// restriction. Action is a general activity row, so filtering on type is what
// stops non-comment rows being counted as comments.
func (c *jiraCollector) onAction(r jira.Row) error {
	c.actions++
	if !strings.EqualFold(r.Get("type"), "comment") {
		return nil
	}
	c.comments++
	if r.Get("level") != "" || r.Get("rolelevel") != "" {
		c.restricted++
	}
	return nil
}

func (c *jiraCollector) onCustomField(r jira.Row) error {
	key := r.Get("customfieldtypekey")
	if key == "" {
		key = r.Get("customfieldtype")
	}
	countIfSet(c.fieldTypes, key)
	return nil
}

// onSearchRequest classifies a saved filter's JQL.
func (c *jiraCollector) onSearchRequest(r jira.Row) error {
	q := r.Get("request")
	if q == "" {
		q = r.Get("query")
	}
	c.filters = append(c.filters, jql.Classify(q))
	return nil
}

// lookupInto records an id -> name row from one of the reference tables.
func (c *jiraCollector) lookupInto(dst map[string]string) jira.RowFunc {
	return func(r jira.Row) error {
		c.lookupRows++
		if id, name := r.Get("id"), r.Get("name"); id != "" && name != "" {
			dst[id] = name
		}
		return nil
	}
}

// resolve turns a counter keyed by id into one keyed by name, leaving an
// unresolvable id as itself so it is still reported rather than dropped.
func resolve(counts map[string]int, names map[string]string) map[string]int {
	out := make(map[string]int, len(counts))
	for id, n := range counts {
		if name, ok := names[id]; ok {
			out[name] += n
			continue
		}
		out[id] += n
	}
	return out
}

func countIfSet(m map[string]int, key string) {
	if key = strings.TrimSpace(key); key != "" {
		m[key]++
	}
}

// fold turns the collected counts into ledger classes.
func (c *jiraCollector) fold(l *Ledger, keys *KeyRegistry, census *jira.Census) {
	// The lookup rows are interleaved with the issues that reference them, so
	// resolution happens here, after the whole file has been read.
	c.issueTypes = resolve(c.issueTypes, c.typeNames)
	c.statuses = resolve(c.statuses, c.statusNames)
	c.priorities = resolve(c.priorities, c.priorityNames)

	c.foldProjects(l, keys)
	c.foldIssues(l)
	c.foldCustomFields(l)
	c.foldComments(l)
	c.foldPeople(l)
	c.foldRemainder(l, census)
	c.foldFilters(l)
}

func (c *jiraCollector) foldProjects(l *Ledger, keys *KeyRegistry) {
	cl := l.Class("Jira projects → spaces")
	cl.Observed = len(c.projects)
	clean, coerced := 0, 0
	var coercedNames []string

	for _, p := range c.projects {
		o := keys.Add(SourceJira, p.key, c.issueKeys[p.id])
		if o.Coercion {
			coerced++
			coercedNames = append(coercedNames, fmt.Sprintf("%s → %s", p.key, o.Coerced))
			continue
		}
		clean++
	}
	cl.Add(VerdictClean, clean, "the project key already satisfies the space key format ^[A-Z0-9]{1,10}$")
	sort.Strings(coercedNames)
	cl.Add(VerdictApproximated, coerced,
		"the project key does not satisfy ^[A-Z0-9]{1,10}$ and must be coerced, which changes every item key derived from it",
		coercedNames...)
}

func (c *jiraCollector) foldIssues(l *Ledger) {
	cl := l.Class("Jira issues → project items")
	cl.Observed = c.issues
	cl.Notes = append(cl.Notes,
		"item_key is <SPACE_KEY>-<number> and assigned by the database at insert time; CreateProjectItemParams has no number or item_key field, so an import cannot preserve original Jira keys through the service path",
		"kind is validated by the item-types service, not by a CHECK or FK (D49, migration 032) — an importer writing straight to the repository would create items whose type does not exist")

	// Issues themselves land wholesale: every issue becomes an item. What
	// varies is how much of it survives, which is carried by the type, status
	// and priority classes below.
	cl.Add(VerdictClean, c.issues, "every issue becomes a project item; the fidelity questions are its type, status, priority and custom fields, assessed separately")

	c.foldIssueTypes(l)
	c.foldStatuses(l)
	c.foldPriorities(l)
}

func (c *jiraCollector) foldIssueTypes(l *Ledger) {
	cl := l.Class("Jira issue types → item kinds")
	cl.Derived = true // distinct types, not rows
	cl.Observed = len(c.issueTypes)
	seeded, coerced, plain := 0, 0, 0
	var coercedNames []string

	for _, name := range sortedKeys(c.issueTypes) {
		slug, changed := CoerceSlug(name)
		switch {
		case IsSeededKind(slug):
			seeded++
		case changed:
			coerced++
			coercedNames = append(coercedNames, fmt.Sprintf("%s → %s", name, slug))
		default:
			plain++
		}
	}
	cl.Add(VerdictClean, seeded, "matches one of the four seeded kinds (task, story, bug, epic)")
	cl.Add(VerdictApproximated, plain,
		"not a seeded kind, so an admin must create it before the import; the slug itself is accepted as written")
	cl.Add(VerdictApproximated, coerced,
		"the name does not satisfy the slug format ^[a-z0-9][a-z0-9_]*$ and must be coerced — the hyphen is the usual cause, so \"Sub-task\" becomes \"sub_task\"",
		coercedNames...)
}

func (c *jiraCollector) foldStatuses(l *Ledger) {
	cl := l.Class("Jira statuses → workflow states")
	cl.Derived = true // distinct statuses, not rows
	cl.Observed = len(c.statuses)
	cl.Notes = append(cl.Notes,
		"status is free text on both tickets and project_items (migration 016), so any status name carries across; what does not carry is Jira's separate resolution field, which Azimuthal does not model")
	cl.Add(VerdictClean, len(c.statuses),
		"workflow states are user-defined per space and status is stored as free text, so a status name arrives as written")
}

func (c *jiraCollector) foldPriorities(l *Ledger) {
	cl := l.Class("Jira priorities → priority")
	cl.Derived = true // distinct priorities, not rows
	cl.Observed = len(c.priorities)
	clean, coerced := 0, 0
	var names []string
	for _, p := range sortedKeys(c.priorities) {
		if IsPriority(p) {
			clean++
			continue
		}
		coerced++
		names = append(names, p)
	}
	cl.Add(VerdictClean, clean, "already one of the four CHECK-constrained values")
	cl.Add(VerdictApproximated, coerced,
		"priority is CHECK-constrained to urgent, high, medium and low on both tables, so any other Jira priority must be mapped onto one of them by hand",
		names...)
}

func (c *jiraCollector) foldCustomFields(l *Ledger) {
	// Row-based, and counted per definition rather than per distinct type: a
	// reader needs "9 of your 40 custom fields are unmappable", not "3 of your
	// 8 field types". It also has to be row-based for the arithmetic to close,
	// since each CustomField definition is its own row in entities.xml — this
	// class carrying Derived while claimedRows counted its rows is exactly the
	// four-row shortfall ReconcileRows caught.
	cl := l.Class("Jira custom fields → custom field defs")
	cl.Observed = sumInts(c.fieldTypes)
	buckets := map[Verdict][]string{}
	counts := map[Verdict]int{}

	for _, ft := range sortedKeys(c.fieldTypes) {
		n := c.fieldTypes[ft]
		_, v, _ := ClassifyJiraCustomFieldType(ft)
		counts[v] += n
		buckets[v] = append(buckets[v], fmt.Sprintf("%s (%d)", jiraCustomFieldKey(ft), n))
	}
	cl.Add(VerdictClean, counts[VerdictClean], "the Jira type has a direct equivalent among text, number, date and single_select", buckets[VerdictClean]...)
	cl.Add(VerdictApproximated, counts[VerdictApproximated], "the value survives but its type changes — multi-value pickers collapse to text, a datetime loses its time of day", buckets[VerdictApproximated]...)
	cl.Add(VerdictUnmappable, counts[VerdictUnmappable], "no implemented type covers it; cascading selects and app-provided calculated or scripted fields have no equivalent", buckets[VerdictUnmappable]...)

	values := l.Class("Jira custom field values")
	values.Observed = c.fieldValues
	values.Add(VerdictApproximated, c.fieldValues,
		"a value survives exactly as well as its field definition does, and definitions are assessed above; values attached to an unmappable field are lost with it")
}

func (c *jiraCollector) foldComments(l *Ledger) {
	cl := l.Class("Jira comments")
	cl.Observed = c.comments
	cl.Notes = append(cl.Notes,
		"comments.body is TEXT and Jira comment bodies are wiki markup or ADF; either way the body arrives as text rather than as a rendered document")

	cl.Add(VerdictClean, c.comments-c.restricted, "an unrestricted comment maps onto the comments table, including its author, timestamps and threading")
	cl.Add(VerdictUnmappable, c.restricted,
		"the comment carries a Jira group or project-role visibility restriction; the comments table has no visibility column at all, so importing it would make a restricted comment readable by everyone who can read the item")

	// Action is a general activity row, so the non-comment ones are their own
	// class rather than being swept into the remainder — otherwise a reader
	// would see "comments: 40" beside an entities.xml holding 400 Action rows
	// and have no way to tell what the other 360 were.
	act := l.Class("Jira activity rows (non-comment)")
	act.Observed = maxInt(c.actions-c.comments, 0)
	act.Add(VerdictUnmappable, act.Observed,
		"Action rows that are not comments are Jira's own activity records; Azimuthal writes its own audit log and has no place to put another system's")

	att := l.Class("Jira attachments")
	att.Observed = c.attachments
	att.Add(VerdictClean, c.attachments,
		"attachments hang off a project_item (migration 027 allows page, ticket and project_item), and the blobs travel in the archive's data/attachments tree")
}

func (c *jiraCollector) foldPeople(l *Ledger) {
	users := l.Class("Jira users → people")
	users.Observed = c.users
	users.Add(VerdictApproximated, c.users,
		"users match on email; an export whose users carry Cloud accountIds rather than addresses cannot be matched at all, and unmatched users must be invited before their authorship resolves")

	groups := l.Class("Jira groups → teams")
	groups.Observed = c.groups
	groups.Add(VerdictApproximated, c.groups,
		"a group becomes a team, but Jira group membership grants permission directly while an Azimuthal team reaches spaces through grants, so the permissions themselves must be re-expressed")

	m := l.Class("Jira group memberships")
	m.Observed = c.memberships
	m.Add(VerdictApproximated, c.memberships,
		"a membership survives only for a user who matched by email; the rest are dropped with their user")
}

func (c *jiraCollector) foldRemainder(l *Ledger, census *jira.Census) {
	links := l.Class("Jira issue links")
	links.Observed = c.links
	links.Add(VerdictUnmappable, c.links,
		"Azimuthal models a parent/child hierarchy on project_items but has no typed link graph, so blocks/relates-to/duplicates links have nowhere to go")

	w := l.Class("Jira worklogs")
	w.Observed = c.worklogs
	w.Add(VerdictUnmappable, c.worklogs, "there is no time-tracking model, so worklogs are lost entirely")

	lookups := l.Class("Jira reference data (issue types, statuses, priorities)")
	lookups.Observed = c.lookupRows
	lookups.Add(VerdictClean, c.lookupRows,
		"these rows are the names behind an issue's type, status and priority ids; they are not imported as entities but are read to resolve the three classes above")

	// Everything the scanner counted that no class above claimed. This is the
	// row that stops the report from quietly omitting the parts of an export
	// nobody thought about.
	//
	// A negative remainder is not clamped: it means two classes claimed the
	// same rows, which inflates every percentage in the report. It is recorded
	// as an over-count so ReconcileRows fails rather than the arithmetic
	// silently closing on a wrong number.
	other := l.Class("Other Jira entities")
	remaining := census.Rows - c.claimedRows()
	other.Observed = remaining
	other.Add(VerdictUnmappable, remaining,
		"entity types this assessment does not classify — configuration, schemes, history and plugin rows. They are counted so the totals reconcile, and named below so nothing is silently omitted",
		c.unclassifiedNames(census)...)
}

// claimedRows is the number of entities.xml rows the classes above account for.
//
// Every term here must be a count of rows read from the file. Distinct-value
// tallies (issue types, statuses, priorities) are deliberately absent: their
// classes are marked Derived and are excluded from the row arithmetic.
func (c *jiraCollector) claimedRows() int {
	return len(c.projects) + c.issues + c.actions + c.attachments +
		c.users + c.groups + c.memberships + c.links + c.worklogs +
		c.fieldValues + sumInts(c.fieldTypes) + len(c.filters) + c.lookupRows
}

// unclassifiedNames lists the entity types no class above claimed, capped so a
// drifting export cannot produce an unbounded report.
func (c *jiraCollector) unclassifiedNames(census *jira.Census) []string {
	claimed := map[string]bool{
		"Project": true, "Issue": true, "Action": true, "CustomField": true,
		"CustomFieldValue": true, "FileAttachment": true, "ApplicationUser": true,
		"OSGroup": true, "OSMembership": true, "IssueLink": true, "Worklog": true,
		"SearchRequest": true, "IssueType": true, "Status": true, "Priority": true,
	}
	var out []string
	for _, name := range census.SortedEntityNames() {
		if !claimed[name] {
			out = append(out, fmt.Sprintf("%s (%d)", name, census.Entities[name]))
		}
	}
	const limit = 40
	if len(out) > limit {
		rest := len(out) - limit
		out = append(out[:limit], fmt.Sprintf("… and %d more entity types", rest))
	}
	return out
}

func (c *jiraCollector) foldFilters(l *Ledger) {
	cl := l.Class("Jira saved filters (JQL)")
	cl.Observed = len(c.filters)
	counts := map[jql.Expressibility]int{}
	for _, q := range c.filters {
		counts[q.Verdict]++
	}
	cl.Add(VerdictClean, counts[jql.Expressible],
		"every clause maps onto the saved-view filter vocabulary and the query's shape is flat")
	cl.Add(VerdictApproximated, counts[jql.Partial],
		"the filter translates but narrows — a text clause becomes a title-only substring match, or a type/sprint clause restricts the view to Vector")
	cl.Add(VerdictUnmappable, counts[jql.NotExpressible],
		"at least one clause or the query's shape has no representation: date predicates, negation, comparison operators, history operators, cross-field OR and grouping are all outside the vocabulary")
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sumInts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
