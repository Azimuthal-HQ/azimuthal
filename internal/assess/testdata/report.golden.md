# Migration assessment

This is a read-only assessment. Nothing was written, and no database was contacted.

## Readiness

77 entities assessed.

| Outcome | Entities | Share |
|---|---:|---:|
| Maps cleanly | 34 | 44.2% |
| Maps with approximation | 22 | 28.6% |
| Preserved as unknown | 2 | 2.6% |
| Unmappable (lost) | 19 | 24.7% |
| **Total** | **77** | **100.0%** |

Every entity counted appears in exactly one row above; the arithmetic is checked, not asserted.

## What was read

- **jira** — `(stream)`: 46 rows
- **confluence** — `(stream)`: 18 rows

## What maps, and what does not

### Jira projects → spaces

2 entities.

- **2 maps cleanly** — the project key already satisfies the space key format ^[A-Z0-9]{1,10}$

### Jira issues → project items

3 entities.

> item_key is <SPACE_KEY>-<number> and assigned by the database at insert time; CreateProjectItemParams has no number or item_key field, so an import cannot preserve original Jira keys through the service path
>
> kind is validated by the item-types service, not by a CHECK or FK (D49, migration 032) — an importer writing straight to the repository would create items whose type does not exist
>

- **3 maps cleanly** — every issue becomes a project item; the fidelity questions are its type, status, priority and custom fields, assessed separately

### Jira issue types → item kinds

3 distinct values.

- **2 maps cleanly** — matches one of the four seeded kinds (task, story, bug, epic)
- **1 maps with approximation** — the name does not satisfy the slug format ^[a-z0-9][a-z0-9_]*$ and must be coerced — the hyphen is the usual cause, so "Sub-task" becomes "sub_task"
  - Sub-task → sub_task

### Jira statuses → workflow states

2 distinct values.

> status is free text on both tickets and project_items (migration 016), so any status name carries across; what does not carry is Jira's separate resolution field, which Azimuthal does not model
>

- **2 maps cleanly** — workflow states are user-defined per space and status is stored as free text, so a status name arrives as written

### Jira priorities → priority

2 distinct values.

- **1 maps cleanly** — already one of the four CHECK-constrained values
- **1 maps with approximation** — priority is CHECK-constrained to urgent, high, medium and low on both tables, so any other Jira priority must be mapped onto one of them by hand
  - Blocker

### Jira custom fields → custom field defs

4 entities.

- **1 maps cleanly** — the Jira type has a direct equivalent among text, number, date and single_select
  - float (1)
- **1 maps with approximation** — the value survives but its type changes — multi-value pickers collapse to text, a datetime loses its time of day
  - multiuserpicker (1)
- **2 unmappable** — no implemented type covers it; cascading selects and app-provided calculated or scripted fields have no equivalent
  - cascadingselect (1), scripted-field (1)

### Jira custom field values

3 entities.

- **3 maps with approximation** — a value survives exactly as well as its field definition does, and definitions are assessed above; values attached to an unmappable field are lost with it

### Jira comments

3 entities.

> comments.body is TEXT and Jira comment bodies are wiki markup or ADF; either way the body arrives as text rather than as a rendered document
>

- **2 maps cleanly** — an unrestricted comment maps onto the comments table, including its author, timestamps and threading
- **1 unmappable** — the comment carries a Jira group or project-role visibility restriction; the comments table has no visibility column at all, so importing it would make a restricted comment readable by everyone who can read the item

### Jira activity rows (non-comment)

1 entity.

- **1 unmappable** — Action rows that are not comments are Jira's own activity records; Azimuthal writes its own audit log and has no place to put another system's

### Jira attachments

2 entities.

- **2 maps cleanly** — attachments hang off a project_item (migration 027 allows page, ticket and project_item), and the blobs travel in the archive's data/attachments tree

### Jira users → people

3 entities.

- **3 maps with approximation** — users match on email; an export whose users carry Cloud accountIds rather than addresses cannot be matched at all, and unmatched users must be invited before their authorship resolves

### Jira groups → teams

2 entities.

- **2 maps with approximation** — a group becomes a team, but Jira group membership grants permission directly while an Azimuthal team reaches spaces through grants, so the permissions themselves must be re-expressed

### Jira group memberships

3 entities.

- **3 maps with approximation** — a membership survives only for a user who matched by email; the rest are dropped with their user

### Jira issue links

2 entities.

- **2 unmappable** — Azimuthal models a parent/child hierarchy on project_items but has no typed link graph, so blocks/relates-to/duplicates links have nowhere to go

### Jira worklogs

1 entity.

- **1 unmappable** — there is no time-tracking model, so worklogs are lost entirely

### Jira reference data (issue types, statuses, priorities)

7 entities.

- **7 maps cleanly** — these rows are the names behind an issue's type, status and priority ids; they are not imported as entities but are read to resolve the three classes above

### Other Jira entities

7 entities.

- **7 unmappable** — entity types this assessment does not classify — configuration, schemes, history and plugin rows. They are counted so the totals reconcile, and named below so nothing is silently omitted
  - ColumnLayout (1), Component (1), FieldLayoutScheme (1), NodeAssociation (1), OSPropertyEntry (2), Version (1)

### Jira saved filters (JQL)

3 entities.

- **1 maps cleanly** — every clause maps onto the saved-view filter vocabulary and the query's shape is flat
- **1 maps with approximation** — the filter translates but narrows — a text clause becomes a title-only substring match, or a type/sprint clause restricts the view to Vector
- **1 unmappable** — at least one clause or the query's shape has no representation: date predicates, negation, comparison operators, history operators, cross-field OR and grouping are all outside the vocabulary

### Confluence spaces → spaces

2 entities.

- **1 maps cleanly** — the space key already satisfies ^[A-Z0-9]{1,10}$
- **1 maps with approximation** — Confluence allows lowercase, punctuated and longer space keys than ^[A-Z0-9]{1,10}$ accepts, so the key must be coerced — and two keys that differed only in the stripped characters stop being distinct
  - my-team-notes → MYTEAMNOTE

### Confluence pages → Codex pages

3 entities.

> 2 of these are live pages (contentStatus=current); the rest are historical revisions and trashed content, which a space export carries as separate Page objects
>
> a page whose document has no matching page_revisions row refuses overwrite, so an import must write the revision alongside the document rather than after it
>

- **2 maps cleanly** — a live page maps onto a Codex page; how much of its body survives is assessed in the macro class below
- **1 maps with approximation** — historical revisions and trashed pages are separate Page objects; Azimuthal keeps page_revisions, so history can be carried, but the revision model is not Confluence's and version comments and per-revision authorship may not line up

### Confluence comments

2 entities.

- **2 maps cleanly** — page comments map onto the comments table, which accepts a page as its entity and models threading through parent_id

### Confluence attachments

1 entity.

- **1 maps cleanly** — attachments hang off a page, which migration 027 allows as an attachment entity type

### Confluence labels

1 entity.

- **1 unmappable** — pages carry no labels; project_items.labels exists but pages have no equivalent column, so page labels have nowhere to go

### Confluence macros → Codex nodes

6 distinct values.

> 1 body fragments contain Confluence's "]] >" rewrite of a CDATA terminator; the rewrite is ambiguous, so content that genuinely contained "]] >" cannot be told apart from content that contained "]]>"
>
> 1 page bodies could not be parsed to the end; what they did contain is counted above
>

- **3 maps cleanly** — the macro has a first-class Codex node and arrives as native content
  - code (1), info (1), toc (1)
- **1 maps with approximation** — a Codex node holds it but not everything about it — a panel's custom title and colour, an excerpt include's partial transclusion
  - tip (1)
- **2 preserved as unknown** — no implemented node covers it, so ADR-0012 keeps it verbatim in an unknownContent carrier: it survives a round trip and can be rendered later, but nothing understands it today
  - drawio (1), jira (1)

### Confluence users → people

2 entities.

- **2 maps with approximation** — users match on email; a ConfluenceUserImpl carrying only a user key and no address cannot be matched, and unmatched authors must be invited before their authorship resolves

### Confluence groups → teams

1 entity.

- **1 maps with approximation** — a group becomes a team, but space permissions must be re-expressed as grants — Azimuthal reaches spaces through grants, never through group rows

### Confluence group memberships

1 entity.

- **1 maps with approximation** — a membership survives only for a user who matched by email

### Confluence body content

2 entities.

- **2 maps cleanly** — a BodyContent object is the page body itself; it is not a separate entity in Azimuthal, where the document lives on the page row

### Other Confluence objects

3 entities.

- **3 unmappable** — object classes this assessment does not classify — permissions, content properties, templates and plugin objects. They are counted so the totals reconcile, and named below so nothing is silently omitted
  - ContentProperty (1), PageTemplate (1), SpacePermission (1)

## Item keys

`item_key` is `<SPACE_KEY>-<number>` and unique per organisation (`idx_project_items_org_key`, migration 031), so two spaces resolving to the same key contend for it.

### Collisions

- **DOCS** — DOCS is claimed by Confluence space "DOCS" and Jira project "DOCS" (across the two exports, which neither could reveal alone); 2 keyed entities are affected

### Keys that must change shape

- Confluence space `my-team-notes` → `MYTEAMNOTE`

A changed key changes every item key derived from it, so external references to the original will not resolve.

## Saved filters (JQL)

Classified against the saved-view filter vocabulary, which is eight named fields with no operators, no negation and no nesting (`internal/core/views/filter.go`).

- `project = DOCS AND status = Open AND assignee = currentUser()` — **expressible**
- `text ~ "pool"` — **partially expressible**
  - `text ~ "pool"`: text searches the title only — Jira's text search also covers description, comments and attachments, so the translated filter matches strictly fewer rows
- `project IN (DOCS, PLAT) AND created >= -30d ORDER BY created DESC` — **not expressible**
  - `created >= -30d`: the vocabulary has no comparison operators — fields hold value lists matched for equality, so >, <, >= and <= have nothing to translate to

## Assumptions this assessment rests on

Neither export format is documented as a contract. Each line below is something this build had to assume in order to read anything, and each is a place a real export could differ.

- Jira: the entity export is XML (entities.xml with an <entity-engine-xml> root), not JSON. Atlassian publishes the archive layout but explicitly declines to document the XML, so entity and field names here follow the OfBiz entity model and may differ on a Cloud instance, which runs a fork.
- Jira: field names are OfBiz field-names, not SQL column names — Action.type not actiontype, Issue.key not pkey. A parser written from the database schema documentation reads nothing.
- Jira: fields are read from both XML attributes and child elements, because a value carrying newlines cannot live in an attribute. Which form a given field takes has not been verified against a real export.
- Jira: issue type, status and priority are id references, resolved here against the IssueType, Status and Priority rows in the same file; an id with no matching row is reported as the id itself rather than dropped. Resolution is Jira's own separate field and has no Azimuthal equivalent at all.
- Jira: comment visibility is read from Action.level (group) and Action.rolelevel (project role). Both empty means unrestricted.
- Jira: saved filters are read from SearchRequest rows. The JQL text is taken from a "request" field, falling back to "query".
- Confluence: the root element <hibernate-generic> is established by the parsers that read this format in production, not by Atlassian documentation.
- Confluence: a page's body is reached through its bodyContents collection to a BodyContent object. Historical revisions and trashed pages are separate Page objects, separated here by contentStatus.
- Confluence: storage-format bodies carry the ac: and ri: prefixes without declaring them, so they are matched by prefix as well as by namespace URI.
- Confluence: a body's own "]]>" is rewritten to "]] >" because nested CDATA is illegal. The rewrite is ambiguous and is counted rather than undone.
- Both: attachment blobs are counted from the zip directory by path prefix, not matched to their metadata rows. An attachment whose blob is missing from the archive would not be detected here.
- Both: an export is read as bytes whatever encoding it declares, so element and attribute names are trusted to be ASCII. Non-ASCII values from a legacy-encoded export may be mojibake.

