package views

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// Aggregates over a saved view's results (P5, ADR-0009).
//
// WHY THIS IS IN THE VIEWS PACKAGE AND NOT IN DASHBOARDS. A count is the same
// question a results page asks, answered with COUNT instead of a row set: the
// same filter vocabulary, the same fan-out, the same per-viewer access union,
// the same ADR-0008 exception. Putting it beside Resolve is what keeps those
// five things from being reimplemented one layer up — a gadget that fetched
// pages and counted them in the client would resolve a different set on page
// two and would be wrong in a way nobody notices.
//
// THE GROUPING HAPPENS IN SQL. Both fan-outs GROUP BY inside the database and
// return one row per bucket. Fetch-all-then-count is prohibited: it is bounded
// by MaxPageSize and would silently under-count any view with more than two
// hundred results, which is exactly the view somebody puts a count gadget on.

// GroupField is the closed set of fields a breakdown may group by.
//
// It is a SUBSET of the filter vocabulary, not a second vocabulary: every
// value here is a field internal/core/views/filter.go already names, and
// nothing may be added to it that the filter document cannot also filter on.
// Sort's exclusion of status does not apply — grouping needs no total order,
// only equality, which free text has.
type GroupField string

// The four groupable fields. Text and space are deliberately absent: text is a
// substring term rather than a value, and a per-space breakdown is the space
// picker's job rather than a chart's.
const (
	GroupNone     GroupField = ""
	GroupStatus   GroupField = "status"
	GroupPriority GroupField = "priority"
	GroupAssignee GroupField = "assignee"
	// GroupKind is project_items.kind — the item type. Vector only, for the
	// same reason the `kinds` filter is: tickets have no type column.
	GroupKind GroupField = "kind"
)

// MaxBuckets bounds what a breakdown returns.
//
// The database groups without a limit — the distinct values of a status, a
// priority or an item type are bounded by the space's own configuration, and
// GROUP BY caps memory at the distinct count rather than the row count. The
// cap is applied AFTER the merge, on presentation grounds: twenty slices is
// already more than a tile can read out, and an assignee breakdown across a
// large organisation would otherwise render a legend nobody can use.
//
// Nothing is dropped silently. Everything past the cap is rolled into one
// explicit bucket whose Other flag is set and whose count is exact, so
// Total still equals the sum of what is returned.
const MaxBuckets = 20

// ErrGroupFieldModule reports a breakdown asking tickets for a column they do
// not have. Same rule, same reasoning and the same refusal as naming `kinds`
// in a filter alongside Beacon: a breakdown that reported every ticket as
// untyped would be a defect its author cannot see.
var ErrGroupFieldModule = errors.New(
	"the \"kind\" breakdown applies to Vector items only — Beacon tickets have no type, " +
		"so a view naming both modules cannot be grouped by it")

// ErrUnknownGroupField reports a group-by field the vocabulary does not define.
var ErrUnknownGroupField = errors.New("unknown breakdown field")

var validGroupFields = map[GroupField]struct{}{
	GroupStatus: {}, GroupPriority: {}, GroupAssignee: {}, GroupKind: {},
}

// ParseGroupField validates a caller-supplied breakdown field. The empty
// string is legal and means "no breakdown, count only".
func ParseGroupField(s string) (GroupField, error) {
	if s == "" {
		return GroupNone, nil
	}
	f := GroupField(s)
	if _, ok := validGroupFields[f]; !ok {
		return GroupNone, fmt.Errorf("%w %q (allowed: %s, %s, %s, %s)",
			ErrUnknownGroupField, s, GroupStatus, GroupPriority, GroupAssignee, GroupKind)
	}
	return f, nil
}

// AllowedFor reports whether this field can be grouped on across the given
// module set. The one place that question is answered.
func (f GroupField) AllowedFor(filter Filter) bool {
	if f == GroupKind && filter.HasModule(ModuleBeacon) {
		return false
	}
	return true
}

// Bucket is one group of a breakdown.
type Bucket struct {
	// Key identifies the bucket. Empty means the absence of a value —
	// unassigned, or an item with no type — and is a real bucket, not a null
	// to drop: work with nobody on it is exactly what a breakdown is for.
	Key string
	// Label is the human form when the key is an opaque id (an assignee), and
	// equal to the key otherwise. Empty when the row named a user the join
	// could not find, which the UI renders as the short id rather than as
	// nothing.
	Label string
	Count int64
	// Other marks the rollup bucket that carries everything past MaxBuckets.
	Other bool
	// OtherBuckets is how many buckets were rolled up. Zero unless Other.
	OtherBuckets int
}

// AggregateResult is one resolved aggregate.
type AggregateResult struct {
	// Total is the exact number of rows the view resolves to for this viewer,
	// counted in the database and never bounded by a page size.
	Total int64
	// Buckets is empty when no breakdown was asked for.
	Buckets []Bucket
	// Truncated reports that a rollup bucket is present.
	Truncated bool
}

// AggregateStore is the read seam for the two grouped fan-outs.
//
// Separate from ResultStore for the reason ResultStore is separate from the
// ticket and project-item repositories: these read across many spaces at once,
// which no other read of either table does.
type AggregateStore interface {
	CountTickets(ctx context.Context, p FanoutParams) (int64, error)
	CountProjectItems(ctx context.Context, p FanoutParams) (int64, error)
	BreakdownTickets(ctx context.Context, p FanoutParams) ([]Bucket, error)
	BreakdownProjectItems(ctx context.Context, p FanoutParams) ([]Bucket, error)
}

// Aggregate counts a view's results for one viewer, optionally grouped.
//
// PER VIEWER, ALWAYS — the same contract Resolve carries, and the reason a
// count gadget on a shared dashboard shows each person a different number.
// Nothing here consults the view's owner.
//
//nolint:cyclop // the two module halves and the group check belong in one place: the access union and the merge contract are only checkable together
func Aggregate(ctx context.Context, store AggregateStore, q Query, v Viewer, group GroupField) (AggregateResult, error) {
	if err := q.Validate(); err != nil {
		return AggregateResult{}, err
	}
	if !group.AllowedFor(q.Filter) {
		return AggregateResult{}, ErrGroupFieldModule
	}

	base, err := buildParams(q, v, cursorPos{}, DefaultPageSize)
	if err != nil {
		return AggregateResult{}, err
	}
	base.GroupBy = string(group)
	// A count has no page and no order. Zeroing them keeps the parameters
	// honest about what the grouped queries actually read.
	base.CursorKey, base.CursorID, base.Limit = "", uuid.Nil, 0

	var out AggregateResult
	merged := map[string]Bucket{}

	// A viewer who can read nothing and holds no share is answered with zero
	// without a round trip, exactly as Resolve is. The short-circuit is the
	// same one, in the same shape, for the same reason.
	if q.Filter.HasModule(ModuleBeacon) && v.canReachModule(ModuleBeacon) {
		p := base
		p.SharedIDs = v.SharedTicketIDs
		// Vector-only fields never reach the ticket fan-out. Validation
		// already refuses them alongside Beacon, so this is belt and braces
		// rather than the enforcement — the same line Resolve carries.
		p.Kinds, p.SprintIDs = nil, nil
		p.NotKinds, p.NotSprintIDs = false, false
		n, err := aggregateModule(ctx, p, group, merged, store.CountTickets, store.BreakdownTickets)
		if err != nil {
			return AggregateResult{}, fmt.Errorf("beacon: %w", err)
		}
		out.Total += n
	}
	if q.Filter.HasModule(ModuleVector) && v.canReachModule(ModuleVector) {
		p := base
		p.SharedIDs = v.SharedItemIDs
		n, err := aggregateModule(ctx, p, group, merged, store.CountProjectItems, store.BreakdownProjectItems)
		if err != nil {
			return AggregateResult{}, fmt.Errorf("vector: %w", err)
		}
		out.Total += n
	}

	if group == GroupNone {
		out.Buckets = []Bucket{}
		return out, nil
	}
	out.Buckets, out.Truncated = capBuckets(merged)
	return out, nil
}

// aggregateModule runs one module's half and folds its buckets into the merge.
// Both halves take the identical shape, so they take the identical function.
func aggregateModule(
	ctx context.Context,
	p FanoutParams,
	group GroupField,
	merged map[string]Bucket,
	count func(context.Context, FanoutParams) (int64, error),
	breakdown func(context.Context, FanoutParams) ([]Bucket, error),
) (int64, error) {
	n, err := count(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("counting results: %w", err)
	}
	if group == GroupNone {
		return n, nil
	}
	rows, err := breakdown(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("grouping results: %w", err)
	}
	mergeBuckets(merged, rows)
	return n, nil
}

// canReachModule reports whether this viewer could match anything in a module
// at all. It is the "no access, no round trip" test, not the access control:
// the access control is the two arrays inside FanoutParams.
func (v Viewer) canReachModule(m Module) bool {
	if len(v.ReadableSpaceIDs) > 0 {
		return true
	}
	if m == ModuleBeacon {
		return len(v.SharedTicketIDs) > 0
	}
	return len(v.SharedItemIDs) > 0
}

// mergeBuckets folds one module's buckets into the cross-module total.
//
// ADR-0009 requires cross-module results to be merged in the API layer rather
// than by unifying the tables; a breakdown merges by summing per key. A label
// is taken from the first module that supplies a non-empty one, because the
// two halves agree on it wherever both have one.
func mergeBuckets(into map[string]Bucket, rows []Bucket) {
	for _, b := range rows {
		existing, ok := into[b.Key]
		if !ok {
			into[b.Key] = b
			continue
		}
		existing.Count += b.Count
		if existing.Label == "" {
			existing.Label = b.Label
		}
		into[b.Key] = existing
	}
}

// capBuckets orders the merged buckets and rolls everything past MaxBuckets
// into one explicit Other bucket, so the returned counts still sum to Total.
func capBuckets(merged map[string]Bucket) ([]Bucket, bool) {
	all := make([]Bucket, 0, len(merged))
	for _, b := range merged {
		all = append(all, b)
	}
	// Largest first, then by key, so the order is stable between runs and
	// between the two module halves arriving in either order.
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].Key < all[j].Key
	})
	if len(all) <= MaxBuckets {
		return all, false
	}
	rest := all[MaxBuckets:]
	other := Bucket{Other: true, OtherBuckets: len(rest)}
	for _, b := range rest {
		other.Count += b.Count
	}
	return append(all[:MaxBuckets:MaxBuckets], other), true
}
