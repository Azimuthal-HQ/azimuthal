package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Share entity types. entity_shares.entity_type is CHECK-constrained to
// exactly these three values (migration 026).
const (
	ShareEntityPage        = "page"
	ShareEntityTicket      = "ticket"
	ShareEntityProjectItem = "project_item"
)

// ValidShareEntityType reports whether s names a shareable entity type.
func ValidShareEntityType(s string) bool {
	switch s {
	case ShareEntityPage, ShareEntityTicket, ShareEntityProjectItem:
		return true
	}
	return false
}

// ShareAudience is who a share is visible to: the whole org, or one team
// (subject-side expanded, exactly like grants).
type ShareAudience string

// Share audiences per ADR-0008.
const (
	AudienceOrg  ShareAudience = "org"
	AudienceTeam ShareAudience = "team"
)

// ShareRow is one active share reaching the caller, as returned by the
// resolution query. RootPath/RootSpaceID are set only for page shares —
// they carry the shared page's CURRENT path so cascade coverage is a
// per-request prefix check, never a stored snapshot.
type ShareRow struct {
	EntityType  string
	EntityID    uuid.UUID
	Cascade     bool
	RootPath    *string
	RootSpaceID *uuid.UUID
}

// ShareResolutionStore is the persistence contract for share resolution.
type ShareResolutionStore interface {
	// ResolveShareRows returns every active (unrevoked, unexpired) share in
	// the org whose audience includes the user — org-audience rows plus
	// team-audience rows matching the user's effective teams. One query,
	// constant regardless of how many shares exist.
	ResolveShareRows(ctx context.Context, orgID, userID uuid.UUID) ([]ShareRow, error)
}

// cascadeRoot is one cascading page share: the root page and its current
// materialized path.
type cascadeRoot struct {
	pageID  uuid.UUID
	spaceID uuid.UUID
	path    string
}

// SharedEntities is the caller's resolved share coverage for one request
// (spec §5 readable_entity_ids): direct shares and cascade roots, kept
// separate. Like Resolution it is cached on the request context and never
// shared across requests — which is what makes revocation and expiry
// immediate (ADR-0008 rules 8 and 11).
type SharedEntities struct {
	// direct maps entity type → the exact entity ids shared to the caller.
	// A cascading share's root page appears here too: the root itself is
	// covered, not only its descendants.
	direct map[string]map[uuid.UUID]struct{}
	// cascadeRoots are the cascading page shares, for subtree coverage.
	cascadeRoots []cascadeRoot
}

// NewSharedEntities builds the coverage set from resolved rows. Exported
// for the middleware and for tests; handlers only ever read the Covers*
// methods.
func NewSharedEntities(rows []ShareRow) *SharedEntities {
	se := &SharedEntities{direct: make(map[string]map[uuid.UUID]struct{})}
	for _, row := range rows {
		byID, ok := se.direct[row.EntityType]
		if !ok {
			byID = make(map[uuid.UUID]struct{})
			se.direct[row.EntityType] = byID
		}
		byID[row.EntityID] = struct{}{}
		if row.Cascade && row.EntityType == ShareEntityPage &&
			row.RootPath != nil && row.RootSpaceID != nil {
			se.cascadeRoots = append(se.cascadeRoots, cascadeRoot{
				pageID:  row.EntityID,
				spaceID: *row.RootSpaceID,
				path:    *row.RootPath,
			})
		}
	}
	return se
}

// Empty reports whether no share reaches the caller.
func (se *SharedEntities) Empty() bool {
	return len(se.direct) == 0 && len(se.cascadeRoots) == 0
}

// CoversEntity reports whether a direct share covers the entity. This is
// the whole check for flat entities (tickets, project items).
func (se *SharedEntities) CoversEntity(entityType string, id uuid.UUID) bool {
	_, ok := se.direct[entityType][id]
	return ok
}

// CoversPage reports whether the caller's shares cover the page: a direct
// share on the page itself, or a cascading share on an ancestor. Subtree
// membership is an exact-segment prefix check on the dot-separated path —
// a cascade on "a.b" covers "a.b.c" and never "a.bc" — and the candidate
// must live in the root's own space, so a path coincidence across spaces
// can never widen coverage.
func (se *SharedEntities) CoversPage(id uuid.UUID, spaceID uuid.UUID, path string) bool {
	if se.CoversEntity(ShareEntityPage, id) {
		return true
	}
	for _, root := range se.cascadeRoots {
		if root.spaceID != spaceID {
			continue
		}
		if PathWithinSubtree(path, root.path) {
			return true
		}
	}
	return false
}

// DirectIDs returns the directly shared entity ids of one type — the
// $shared_direct input for cross-space read queries (P4 saved views, P6
// search union these in; P3's shared read route checks membership instead).
func (se *SharedEntities) DirectIDs(entityType string) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(se.direct[entityType]))
	for id := range se.direct[entityType] {
		ids = append(ids, id)
	}
	return ids
}

// CascadeRootPaths returns the cascading roots' current paths — the
// $shared_subtree_path_prefixes input for cross-space read queries. Callers
// interpolating these into LIKE must run each through EscapeLike first.
func (se *SharedEntities) CascadeRootPaths() []string {
	paths := make([]string, 0, len(se.cascadeRoots))
	for _, root := range se.cascadeRoots {
		paths = append(paths, root.path)
	}
	return paths
}

// PathWithinSubtree reports whether path sits at or below root in a
// dot-separated materialized path: equal, or extending root by whole
// segments. "a.b" covers "a.b.c" but never the sibling "a.bc" — the naive
// prefix check both this function and every LIKE pattern must not become.
func PathWithinSubtree(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+".")
}

// EscapeLike escapes the three LIKE metacharacters (backslash first) so a
// stored path interpolates into a LIKE pattern as literal text. Paths are
// dotted UUIDs today, but pages.path is unconstrained TEXT — a '%' or '_'
// that ever appeared there must match itself, never widen the pattern.
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// SubtreeLikePattern builds the LIKE pattern matching strict descendants of
// root: EscapeLike(root) + ".%". Match root itself separately (path = root
// or id = root's id) — the pattern deliberately excludes it.
func SubtreeLikePattern(root string) string {
	return EscapeLike(root) + ".%"
}

// WithShareStore attaches the share resolution store to the resolver.
// Share resolution fails closed but loudly when the store is missing —
// a wiring mistake must surface as an error, not as silent denial.
func (r *Resolver) WithShareStore(s ShareResolutionStore) *Resolver {
	r.shareStore = s
	return r
}

// ResolveShares computes the caller's share coverage for one request: one
// query, constant regardless of how many shares exist. Resolved only on
// the routes that read through shares (the /shared/ family), cached on the
// request context by the ResolveShares middleware — space-scoped routes
// never pay for it, so the P2 per-request query budget is untouched.
func (r *Resolver) ResolveShares(ctx context.Context, orgID, userID uuid.UUID) (*SharedEntities, error) {
	if r.shareStore == nil {
		return nil, fmt.Errorf("access: share store not configured")
	}
	rows, err := r.shareStore.ResolveShareRows(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("resolving shares: %w", err)
	}
	return NewSharedEntities(rows), nil
}

// WithSharedEntities stores the resolved share coverage on the context.
func WithSharedEntities(ctx context.Context, se *SharedEntities) context.Context {
	return context.WithValue(ctx, contextKeySharedEntities, se)
}

// SharedEntitiesFromContext returns the request's share coverage, or nil
// when none was resolved. Callers treat nil as no coverage — fail closed.
func SharedEntitiesFromContext(ctx context.Context) *SharedEntities {
	se, _ := ctx.Value(contextKeySharedEntities).(*SharedEntities)
	return se
}
