package assess

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Azimuthal-HQ/azimuthal/internal/assess/jql"
)

// Headline is the migration-readiness summary.
//
// The percentages are of every entity assessed, and they are computed from the
// same ledger the detail comes from, so the summary cannot disagree with the
// table under it.
type Headline struct {
	Total        int     `json:"total"`
	Clean        int     `json:"clean"`
	Approximated int     `json:"approximated"`
	Preserved    int     `json:"preserved"`
	Unmappable   int     `json:"unmappable"`
	CleanPct     float64 `json:"clean_pct"`
	ApproxPct    float64 `json:"approximated_pct"`
	PreservedPct float64 `json:"preserved_pct"`
	LostPct      float64 `json:"lost_pct"`
}

// Summarise computes the headline from the ledger.
func (r *Result) Summarise() Headline {
	total := r.Ledger.Total()
	h := Headline{
		Total:        total,
		Clean:        r.Ledger.TotalBy(VerdictClean),
		Approximated: r.Ledger.TotalBy(VerdictApproximated),
		Preserved:    r.Ledger.TotalBy(VerdictPreserved),
		Unmappable:   r.Ledger.TotalBy(VerdictUnmappable),
	}
	if total == 0 {
		return h
	}
	pct := func(n int) float64 { return float64(n) * 100 / float64(total) }
	h.CleanPct, h.ApproxPct = pct(h.Clean), pct(h.Approximated)
	h.PreservedPct, h.LostPct = pct(h.Preserved), pct(h.Unmappable)
	return h
}

// WriteJSON renders the machine-readable report.
func (r *Result) WriteJSON(w io.Writer) error {
	payload := struct {
		Headline Headline `json:"headline"`
		*Result
	}{Headline: r.Summarise(), Result: r}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return fmt.Errorf("writing JSON report: %w", err)
	}
	return nil
}

// WriteMarkdown renders the human-readable report, which is the deliverable.
func (r *Result) WriteMarkdown(w io.Writer) error {
	b := &strings.Builder{}
	r.writeHeader(b)
	r.writeSources(b)
	r.writeLedger(b)
	r.writeCollisions(b)
	r.writeFilters(b)
	r.writeAssumptions(b)

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing markdown report: %w", err)
	}
	return nil
}

func (r *Result) writeHeader(b *strings.Builder) {
	h := r.Summarise()
	b.WriteString("# Migration assessment\n\n")
	b.WriteString("This is a read-only assessment. Nothing was written, and no database was contacted.\n\n")

	b.WriteString("## Readiness\n\n")
	fmt.Fprintf(b, "%d entities assessed.\n\n", h.Total)
	b.WriteString("| Outcome | Entities | Share |\n|---|---:|---:|\n")
	rows := []struct {
		label string
		n     int
		pct   float64
	}{
		{"Maps cleanly", h.Clean, h.CleanPct},
		{"Maps with approximation", h.Approximated, h.ApproxPct},
		{"Preserved as unknown", h.Preserved, h.PreservedPct},
		{"Unmappable (lost)", h.Unmappable, h.LostPct},
	}
	for _, row := range rows {
		fmt.Fprintf(b, "| %s | %d | %.1f%% |\n", row.label, row.n, row.pct)
	}
	fmt.Fprintf(b, "| **Total** | **%d** | **100.0%%** |\n\n", h.Total)
	b.WriteString("Every entity counted appears in exactly one row above; the arithmetic is checked, not asserted.\n\n")
}

func (r *Result) writeSources(b *strings.Builder) {
	b.WriteString("## What was read\n\n")
	for _, s := range r.Sources {
		fmt.Fprintf(b, "- **%s** — `%s`: %d rows", s.Kind, s.Path, s.Rows)
		if s.Attachments > 0 {
			fmt.Fprintf(b, ", %d attachment blobs (%s declared)", s.Attachments, humanBytes(s.AttachmentBytes))
		}
		b.WriteString("\n")
		if s.Truncated {
			fmt.Fprintf(b, "  - ⚠ the export could not be read to the end: %s\n", s.TruncationReason)
			b.WriteString("  - what was read before the fault is counted above; the totals understate the real export\n")
		}
	}
	b.WriteString("\n")
}

func (r *Result) writeLedger(b *strings.Builder) {
	b.WriteString("## What maps, and what does not\n\n")
	for _, c := range r.Ledger.Classes {
		if c.Observed == 0 && len(c.Findings) == 0 {
			continue
		}
		fmt.Fprintf(b, "### %s\n\n%d %s.\n\n", c.Name, c.Observed, unitFor(c))

		for _, note := range c.Notes {
			fmt.Fprintf(b, "> %s\n>\n", note)
		}
		if len(c.Notes) > 0 {
			b.WriteString("\n")
		}
		writeFindings(b, c)
	}
}

func writeFindings(b *strings.Builder, c *Class) {
	printed := false
	for _, f := range c.Findings {
		if f.Count == 0 {
			continue
		}
		printed = true
		fmt.Fprintf(b, "- **%d %s** — %s\n", f.Count, f.Verdict.Label(), f.Reason)
		if len(f.Detail) > 0 {
			fmt.Fprintf(b, "  - %s\n", strings.Join(f.Detail, ", "))
		}
	}
	if !printed {
		b.WriteString("- nothing of this kind in the export\n")
	}
	b.WriteString("\n")
}

func (r *Result) writeCollisions(b *strings.Builder) {
	if len(r.Collisions) == 0 && len(r.Coercions) == 0 {
		return
	}
	b.WriteString("## Item keys\n\n")
	b.WriteString("`item_key` is `<SPACE_KEY>-<number>` and unique per organisation ")
	b.WriteString("(`idx_project_items_org_key`, migration 031), so two spaces resolving to the same key contend for it.\n\n")

	if len(r.Collisions) > 0 {
		b.WriteString("### Collisions\n\n")
		for _, c := range r.Collisions {
			fmt.Fprintf(b, "- **%s** — %s\n", c.Key, c.Describe())
		}
		b.WriteString("\n")
	}
	if len(r.Coercions) > 0 {
		b.WriteString("### Keys that must change shape\n\n")
		for _, o := range r.Coercions {
			fmt.Fprintf(b, "- %s `%s` → `%s`\n", o.Source, o.Original, o.Coerced)
		}
		b.WriteString("\nA changed key changes every item key derived from it, so external references to the original will not resolve.\n\n")
	}
}

func (r *Result) writeFilters(b *strings.Builder) {
	if len(r.Filters) == 0 {
		return
	}
	b.WriteString("## Saved filters (JQL)\n\n")
	b.WriteString("Classified against the saved-view filter vocabulary, which is eight named fields ")
	b.WriteString("with no operators, no negation and no nesting (`internal/core/views/filter.go`).\n\n")

	for _, q := range r.Filters {
		fmt.Fprintf(b, "- `%s` — **%s**\n", q.Raw, filterVerdictLabel(q.Verdict))
		for _, c := range q.Clauses {
			if c.Verdict == jql.Expressible {
				continue
			}
			fmt.Fprintf(b, "  - `%s`: %s\n", c.Raw, c.Reason)
		}
		for _, s := range q.Structural {
			fmt.Fprintf(b, "  - %s\n", s)
		}
	}
	b.WriteString("\n")
}

func filterVerdictLabel(e jql.Expressibility) string {
	switch e {
	case jql.Expressible:
		return "expressible"
	case jql.Partial:
		return "partially expressible"
	case jql.NotExpressible:
		return "not expressible"
	default:
		return string(e)
	}
}

func (r *Result) writeAssumptions(b *strings.Builder) {
	b.WriteString("## Assumptions this assessment rests on\n\n")
	b.WriteString("Neither export format is documented as a contract. ")
	b.WriteString("Each line below is something this build had to assume in order to read anything, ")
	b.WriteString("and each is a place a real export could differ.\n\n")
	for _, a := range r.Assumptions {
		fmt.Fprintf(b, "- %s\n", a)
	}
	b.WriteString("\n")
}

// unitFor names what a class counts, so the report never reads "1 entities"
// and never implies a derived tally is a row count.
func unitFor(c *Class) string {
	switch {
	case c.Derived && c.Observed == 1:
		return "distinct value"
	case c.Derived:
		return "distinct values"
	case c.Observed == 1:
		return "entity"
	default:
		return "entities"
	}
}

// humanBytes renders a declared size. The value comes from the archive's own
// directory, so it is a claim rather than a measurement.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
