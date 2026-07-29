package dashboards

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// GadgetDraft is one tile as a caller submits it.
//
// There is no Position: the server numbers the collection from the order of
// the slice it was sent. That is not a convenience — it is what makes gaps,
// duplicates and permutation checks structurally impossible. Migration 039's
// queue reorder has to validate a permutation precisely because it renumbers
// in place; this one does not.
type GadgetDraft struct {
	Key         GadgetKey
	ColSpan     int32
	SavedViewID *uuid.UUID
	// Config arrives as raw JSON so the registry's own strict parser sees the
	// caller's exact bytes — decoding it into a Go struct here would silently
	// drop the unknown keys the parser must refuse.
	Config json.RawMessage
}

// SetGadgets replaces a dashboard's whole gadget collection.
//
// Owner only (org admin bypasses): arranging somebody else's dashboard is a
// change to their work, and the audience decides who may SEE a dashboard, not
// who may rearrange it.
//
// The write is one transaction (Store.ReplaceGadgets) per spec §6 — "gadget
// layout saves as a whole collection, never per gadget, to avoid partial
// states". A half-applied layout is worse than a rejected one: it is a
// dashboard nobody asked for that looks deliberate.
func (s *Service) SetGadgets(ctx context.Context, orgID, id uuid.UUID, a views.Actor, drafts []GadgetDraft) (Detail, error) {
	d, err := s.load(ctx, orgID, id, a)
	if err != nil {
		return Detail{}, err
	}
	if !d.CanEdit(a) {
		return Detail{}, ErrNotOwner
	}
	if len(drafts) > MaxGadgets {
		return Detail{}, ErrTooManyGadgets
	}

	// Every referenced view is fetched once, before anything is validated, so
	// the visibility check below costs one query for the whole collection
	// rather than one per gadget.
	visible, err := s.visibleViews(ctx, orgID, drafts, a)
	if err != nil {
		return Detail{}, err
	}

	rows := make([]Gadget, 0, len(drafts))
	for i, dr := range drafts {
		g, err := s.validateGadget(d.Module, dr, visible)
		if err != nil {
			return Detail{}, fmt.Errorf("gadget %d: %w", i+1, err)
		}
		g.DashboardID = d.ID
		g.Position = int32(i)
		rows = append(rows, g)
	}

	written, err := s.store.ReplaceGadgets(ctx, d.ID, rows)
	if err != nil {
		return Detail{}, fmt.Errorf("saving the layout: %w", err)
	}
	resolved, err := s.resolveGadgets(ctx, orgID, written, a)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Dashboard: d, Gadgets: resolved}, nil
}

// visibleViews returns the drafts' referenced views that this actor may see,
// keyed by id. Anything absent from the map is deleted, or not theirs to see —
// on a WRITE both are refused, because attaching a view you cannot open
// produces a tile that can only ever say "not available to you".
func (s *Service) visibleViews(ctx context.Context, orgID uuid.UUID, drafts []GadgetDraft, a views.Actor) (map[uuid.UUID]views.View, error) {
	ids := make([]uuid.UUID, 0, len(drafts))
	seen := map[uuid.UUID]struct{}{}
	for _, dr := range drafts {
		if dr.SavedViewID == nil {
			continue
		}
		if _, dup := seen[*dr.SavedViewID]; dup {
			continue
		}
		seen[*dr.SavedViewID] = struct{}{}
		ids = append(ids, *dr.SavedViewID)
	}
	out := map[uuid.UUID]views.View{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.views.ByIDs(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("checking the views this layout names: %w", err)
	}
	for _, v := range rows {
		if v.CanSee(a) {
			out[v.ID] = v
		}
	}
	return out, nil
}

// validateGadget applies the registry to one submitted tile.
//
// Strict on write: an unregistered key, a key that may not sit on this
// dashboard's module, a span outside the CHECK, a saved view the kind does not
// take (or needs and lacks), and any configuration key outside the kind's own
// vocabulary are all refused rather than normalised away.
//
//nolint:cyclop // one linear rule per registry field; splitting it hides what a gadget may be
func (s *Service) validateGadget(module Module, dr GadgetDraft, visible map[uuid.UUID]views.View) (Gadget, error) {
	def, known := Lookup(dr.Key)
	if !known {
		return Gadget{}, fmt.Errorf("%w %q", ErrUnknownGadget, dr.Key)
	}
	if !def.AllowsModule(module) {
		return Gadget{}, fmt.Errorf("%w: %q on a %q dashboard", ErrGadgetModule, dr.Key, module)
	}

	span := dr.ColSpan
	if span == 0 {
		span = def.DefaultSpan
	}
	if span != 1 && span != 2 && span != 4 {
		return Gadget{}, ErrSpanInvalid
	}

	// Parsed once, before the view checks, because the breakdown rule below
	// needs the group field and re-parsing would be two chances for the two
	// reads to disagree.
	cfg, err := ParseConfig(def, dr.Config)
	if err != nil {
		return Gadget{}, err
	}

	switch {
	case def.RequiresSavedView && dr.SavedViewID == nil:
		return Gadget{}, ErrViewRequired
	case !def.RequiresSavedView && dr.SavedViewID != nil:
		// Refused rather than dropped: silently discarding a view the author
		// chose would leave them looking at a gadget that ignores it.
		return Gadget{}, ErrViewNotAllowed
	case dr.SavedViewID != nil:
		v, ok := visible[*dr.SavedViewID]
		if !ok {
			// One message for "no such view" and for "not yours to see", so
			// the endpoint does not confirm that somebody else's private view
			// exists.
			return Gadget{}, ErrViewNotVisible
		}
		// A breakdown over a field the view's modules cannot answer is refused
		// here as well as at resolution time. The vocabulary's own rule, in
		// the vocabulary's own words. ParseConfig has already established that
		// GroupBy is in the vocabulary, so this is only the module question.
		if def.Key == GadgetBreakdown && !views.GroupField(cfg.GroupBy).AllowedFor(v.Query.Filter) {
			return Gadget{}, views.ErrGroupFieldModule
		}
	}

	return Gadget{
		Key: string(def.Key), ColSpan: span,
		SavedViewID: dr.SavedViewID, Config: cfg,
	}, nil
}
