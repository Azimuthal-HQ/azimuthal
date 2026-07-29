// Package assess implements the read-only migration assessor behind
// "azimuthal assess": it parses a Jira Cloud export and/or a Confluence space
// export and reports what a future import would map, approximate, or preserve.
//
// # It never writes anything
//
// Nothing in this package or its subpackages opens a database, accepts a DSN,
// or imports internal/config, internal/db or a driver. That is not a convention
// to remember — TestNoDatabaseReachability walks this package's transitive
// import graph and fails by name on any of them. The assessor exists precisely
// so a self-hoster can evaluate a migration before trusting it with data, and a
// tool that could write is a tool they have to trust first.
//
// # The ledger is the honest part
//
// A migration report is only worth reading if its arithmetic reconciles. Every
// entity the parsers observe lands in exactly one Verdict bucket, and the totals
// are checked rather than asserted: Ledger.Reconcile compares what was counted
// against what was classified and returns an error when they disagree. A class
// whose classification is incomplete is a bug that shows up as a failed
// reconciliation, not as a report that quietly totals to less than the input.
//
// The buckets are deliberately four rather than two. "Maps" and "does not map"
// would force approximations to pick a side, and an approximation reported as
// clean is exactly the surprise this tool exists to prevent.
package assess

import (
	"errors"
	"fmt"
	"sort"
)

// Verdict is what a future import would do with one entity.
//
// The four are ordered by decreasing fidelity, and String/Rank depend on that
// order, so new verdicts go in the right place rather than at the end.
type Verdict string

const (
	// VerdictClean means the entity has a direct representation in Azimuthal's
	// model and would arrive intact.
	VerdictClean Verdict = "clean"

	// VerdictApproximated means the entity would arrive, but changed in a way
	// the owner should know about before committing: a renamed key, a coerced
	// slug, a collapsed field type.
	VerdictApproximated Verdict = "approximated"

	// VerdictPreserved means the entity has no native representation but is
	// carried verbatim in an ADR-0012 unknown-content carrier: it survives a
	// round trip and can be rendered later, but nothing understands it today.
	VerdictPreserved Verdict = "preserved"

	// VerdictUnmappable means the entity would not survive the import at all.
	// This is the bucket that must never be silently empty.
	VerdictUnmappable Verdict = "unmappable"
)

// AllVerdicts lists the four buckets in fidelity order. Report rendering and
// the reconciliation both iterate this rather than a literal, so a new verdict
// cannot be added without appearing in the totals.
var AllVerdicts = []Verdict{VerdictClean, VerdictApproximated, VerdictPreserved, VerdictUnmappable}

// Rank orders verdicts by decreasing fidelity, for stable sorting.
func (v Verdict) Rank() int {
	for i, known := range AllVerdicts {
		if known == v {
			return i
		}
	}
	return len(AllVerdicts)
}

// Valid reports whether v is one of the four defined buckets.
func (v Verdict) Valid() bool { return v.Rank() < len(AllVerdicts) }

// Label is the human-readable form used in the markdown report.
func (v Verdict) Label() string {
	switch v {
	case VerdictClean:
		return "maps cleanly"
	case VerdictApproximated:
		return "maps with approximation"
	case VerdictPreserved:
		return "preserved as unknown"
	case VerdictUnmappable:
		return "unmappable"
	default:
		return string(v)
	}
}

// ErrUnreconciled reports that a class's bucketed counts do not sum to the
// number of entities the parser actually observed. It is returned rather than
// logged because a report whose arithmetic does not close is not a report.
var ErrUnreconciled = errors.New("assessment does not reconcile")

// Finding is one bucketed group within an entity class.
//
// Count is the number of entities this finding covers, not the number of
// distinct reasons: twelve Jira issue types that all coerce their slug are one
// finding with Count 12, so the report can say "12 of 40" without the reader
// summing rows.
type Finding struct {
	// Verdict is the bucket. Required and validated.
	Verdict Verdict `json:"verdict"`
	// Count is how many entities of the class land here. May be zero; a
	// zero-count finding is dropped from the report but not from validation.
	Count int `json:"count"`
	// Reason states why, in terms of Azimuthal's real model. It is the whole
	// value of the tool and is required for every finding.
	Reason string `json:"reason"`
	// Detail optionally names the specific things counted — macro names, field
	// types, colliding keys — so "14 unmappable" is followed by which 14.
	Detail []string `json:"detail,omitempty"`
}

// Class is one entity class's complete assessment: what was seen, and where all
// of it went.
type Class struct {
	// Name is the entity class as the source format calls it.
	Name string `json:"name"`
	// Observed is the number of entities the parser counted, independently of
	// classification. This is the number Reconcile checks the findings against,
	// and it is why a forgotten entity fails a test instead of vanishing.
	Observed int `json:"observed"`
	// Findings partition Observed. Order is normalised by Reconcile.
	Findings []Finding `json:"findings"`
	// Derived marks a class that counts distinct values rather than source
	// rows — "how many issue types are there", not "how many rows were read".
	//
	// The distinction is what makes the ledger's arithmetic checkable. Only
	// row-based classes may be summed against the parser's row total, because
	// counting three issue types alongside four hundred issues and comparing
	// the sum to the file's row count would compare two different things and
	// fail for a reason that is not a defect.
	Derived bool `json:"derived,omitempty"`
	// Notes carry context that is not a bucket — format assumptions, or a
	// pointer to the substrate that decided the mapping.
	Notes []string `json:"notes,omitempty"`
}

// Add records count entities in a bucket with a reason. A zero or negative
// count is ignored so callers can add unconditionally from a counter map.
func (c *Class) Add(v Verdict, count int, reason string, detail ...string) {
	if count <= 0 {
		return
	}
	c.Findings = append(c.Findings, Finding{Verdict: v, Count: count, Reason: reason, Detail: detail})
}

// Classified sums every finding, regardless of bucket.
func (c *Class) Classified() int {
	total := 0
	for _, f := range c.Findings {
		total += f.Count
	}
	return total
}

// CountBy sums the findings in one bucket.
func (c *Class) CountBy(v Verdict) int {
	total := 0
	for _, f := range c.Findings {
		if f.Verdict == v {
			total += f.Count
		}
	}
	return total
}

// Ledger is the whole assessment: every class, in report order.
type Ledger struct {
	Classes []*Class `json:"classes"`
}

// Class returns the named class, creating it if absent. Classes keep insertion
// order so the report reads in the order the assessor considered things.
func (l *Ledger) Class(name string) *Class {
	for _, c := range l.Classes {
		if c.Name == name {
			return c
		}
	}
	c := &Class{Name: name}
	l.Classes = append(l.Classes, c)
	return c
}

// Total sums Observed across every class.
func (l *Ledger) Total() int {
	total := 0
	for _, c := range l.Classes {
		total += c.Observed
	}
	return total
}

// RowTotal sums Observed across the row-based classes only.
//
// This is the number that must equal what the parser counted. It is the ledger's
// strongest claim: every row read out of the export landed in exactly one class,
// and none was counted twice.
func (l *Ledger) RowTotal() int {
	total := 0
	for _, c := range l.Classes {
		if !c.Derived {
			total += c.Observed
		}
	}
	return total
}

// ReconcileRows checks the row-based classes against what the parser actually
// read, and is the check a remainder class cannot satisfy vacuously.
//
// A class computed as "everything left over" always sums to itself, so
// Reconcile alone would pass however wrong the classification was. Comparing
// against the parser's independent row count is what makes the arithmetic real:
// under-counting means rows vanished, over-counting means a row was claimed by
// two classes and the headline percentages are inflated.
func (l *Ledger) ReconcileRows(parsedRows int) error {
	if got := l.RowTotal(); got != parsedRows {
		return fmt.Errorf(
			"%w: the parser read %d rows but the ledger's row-based classes account for %d",
			ErrUnreconciled, parsedRows, got)
	}
	return nil
}

// TotalBy sums one bucket across every class.
func (l *Ledger) TotalBy(v Verdict) int {
	total := 0
	for _, c := range l.Classes {
		total += c.CountBy(v)
	}
	return total
}

// Reconcile validates the whole ledger and normalises finding order.
//
// It enforces the invariant the report's credibility rests on: for every class,
// the findings sum exactly to the number of entities observed. Under-counting
// means something was dropped silently; over-counting means something was
// counted twice and the headline percentages are inflated. Both are returned as
// errors, and both are reachable from a test that deliberately breaks a
// classifier.
func (l *Ledger) Reconcile() error {
	for _, c := range l.Classes {
		if err := c.reconcile(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Class) reconcile() error {
	if c.Observed < 0 {
		return fmt.Errorf("%w: class %q observed a negative count (%d)", ErrUnreconciled, c.Name, c.Observed)
	}
	for _, f := range c.Findings {
		if !f.Verdict.Valid() {
			return fmt.Errorf("%w: class %q has a finding with unknown verdict %q", ErrUnreconciled, c.Name, f.Verdict)
		}
		if f.Count < 0 {
			return fmt.Errorf("%w: class %q has a finding with a negative count (%d)", ErrUnreconciled, c.Name, f.Count)
		}
		if f.Reason == "" {
			return fmt.Errorf("%w: class %q has a %s finding with no reason", ErrUnreconciled, c.Name, f.Verdict)
		}
	}
	if got := c.Classified(); got != c.Observed {
		return fmt.Errorf(
			"%w: class %q observed %d entities but classified %d — every entity must land in exactly one bucket",
			ErrUnreconciled, c.Name, c.Observed, got)
	}
	c.sortFindings()
	return nil
}

// sortFindings orders by fidelity then by descending count, so the report leads
// with the largest group in each bucket. Ties break on reason for determinism —
// golden-file tests depend on this being total.
func (c *Class) sortFindings() {
	sort.SliceStable(c.Findings, func(i, j int) bool {
		a, b := c.Findings[i], c.Findings[j]
		if a.Verdict != b.Verdict {
			return a.Verdict.Rank() < b.Verdict.Rank()
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Reason < b.Reason
	})
}
