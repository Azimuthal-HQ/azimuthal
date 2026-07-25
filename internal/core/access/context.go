package access

import (
	"context"

	"github.com/google/uuid"
)

type contextKey int

const (
	contextKeyResolution contextKey = iota
	contextKeySharedEntities
)

// WithResolution stores a Resolution on the context. Called once per request
// by the resolution middleware; the value lives for that request only.
func WithResolution(ctx context.Context, res *Resolution) context.Context {
	return context.WithValue(ctx, contextKeyResolution, res)
}

// FromContext returns the request's Resolution, or nil when none was
// resolved (unauthenticated or non-org-scoped requests).
func FromContext(ctx context.Context) *Resolution {
	res, _ := ctx.Value(contextKeyResolution).(*Resolution)
	return res
}

// Can is the capability check helper (ADR-0007): fail closed when no
// resolution is on the context.
func Can(ctx context.Context, c Capability, spaceID uuid.UUID) bool {
	res := FromContext(ctx)
	return res != nil && res.Can(c, spaceID)
}

// CanOrgWide is the capability check for a mutation with no space to check
// against — space creation, where the space does not exist yet. Fails closed
// when no resolution is on the context, exactly like Can.
func CanOrgWide(ctx context.Context, c Capability) bool {
	res := FromContext(ctx)
	return res != nil && res.CanOrgWide(c)
}

// CanEditEntity implements the edit_own/edit_any split: editing an entity you
// created needs edit_own_items; editing anyone else's needs edit_any_item.
func CanEditEntity(ctx context.Context, spaceID, createdBy uuid.UUID) bool {
	res := FromContext(ctx)
	if res == nil {
		return false
	}
	if res.Can(CapEditAnyItem, spaceID) {
		return true
	}
	return createdBy == res.UserID && res.Can(CapEditOwnItems, spaceID)
}
