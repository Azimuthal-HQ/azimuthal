package dashboards

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// Home: what a person sees when they land, and the starter layout that gets
// them there on their first visit.
//
// P5 replaces the interim Home — a client-side count of the caller's spaces —
// with a dashboard. A person who has made one sees theirs; a person who has
// not gets a starter created for them, once, on the visit itself.
//
// WHY LAZILY AND NOT AS A BACKFILL. The same argument migrations 035 and 039
// make about seeding, and the spec makes about this feature specifically ("on
// first login a user's Home dashboard is seeded"). A backfill would touch
// every existing user in one unreviewable step, would create rows for people
// who never open Home, and would have to reproduce these filter documents in
// SQL where they could drift from the Go that produces them everywhere else.
//
// WHY EXACTLY ONCE. Re-seeding destroys customisation, which is the one thing
// a dashboard is for. `is_seeded` records that the row came from here, and the
// dashboards_one_default partial unique index makes the insert idempotent BY
// CONSTRUCTION: the starter is written ON CONFLICT DO NOTHING, so two tabs
// opening Home at the same moment cannot produce two starters, and nothing
// re-seeds a dashboard that already exists — whatever else changes about the
// person, including which team they belong to.

// StarterName is what a seeded Home dashboard is called.
const StarterName = "My work"

// StarterNote is the getting-started tile's markdown source.
//
// It is markdown because the note gadget renders markdown, and it is stored on
// the row rather than special-cased in the client so that it is editable —
// somebody's first act on their own dashboard can be to delete this.
const StarterNote = `### This is your Home dashboard

It was created on your first visit and it is yours to change — add a gadget,
remove one, or point one at a saved view.

- **My work** and **Recently updated** resolve against your own access, so
  everybody sees their own rows even on a dashboard that is shared.
- Save a view from any list first if you want a gadget scoped to something
  in particular.`

// StarterLayout is the gadget collection a seeded Home dashboard begins with.
//
// Three tiles, in the order they are useful: what is assigned to you, what
// moved recently, and an explanation of both. Every one of them resolves per
// viewer, so the starter is meaningful the moment it appears rather than after
// the person has configured something.
//
// It deliberately does NOT include an activity feed. The gadget set has none:
// see the phase report — the audit log carries no space id, so an
// actor-attributed feed scoped to readable containers would need a new derived
// query and a new member-visible read path over an admin-only table, and the
// standing instruction was to flag that rather than invent a feed. "Recently
// updated" is the nearest thing that costs no new data path, and it is a
// different gadget wearing its own name rather than an activity feed wearing a
// borrowed one.
func StarterLayout() []Gadget {
	body := StarterNote
	return []Gadget{
		{Key: string(GadgetMyWork), Position: 0, ColSpan: 2},
		{Key: string(GadgetRecentWork), Position: 1, ColSpan: 2},
		{Key: string(GadgetNote), Position: 2, ColSpan: 4, Config: Config{Body: body}},
	}
}

// ResolveHome returns the caller's Home dashboard, seeding the starter layout
// if they have none.
//
// It is a READ that may write once, and the write is idempotent by database
// constraint rather than by a check the handler performs. The alternative —
// making the client POST before it can GET — puts a mutation on the landing
// path of every session, which is worse for exactly the same effect.
func (s *Service) ResolveHome(ctx context.Context, orgID uuid.UUID, a views.Actor) (Detail, error) {
	d, err := s.store.DefaultFor(ctx, orgID, a.UserID, string(ModuleHome))
	switch {
	case err == nil:
		d.markValidity()
		return s.detail(ctx, orgID, d, a)
	case !errors.Is(err, ErrNotFound):
		return Detail{}, fmt.Errorf("loading your Home dashboard: %w", err)
	}

	starter := Dashboard{
		OrgID: orgID, OwnerID: a.UserID,
		Name: StarterName, Module: ModuleHome,
		IsDefault: true, IsSeeded: true,
		Visibility: views.VisibilityPrivate,
	}
	if _, err := s.store.CreateStarter(ctx, starter, StarterLayout()); err != nil {
		return Detail{}, fmt.Errorf("preparing your Home dashboard: %w", err)
	}
	// Re-read rather than trusting what was inserted. If a concurrent request
	// won the conflict, the row that exists is theirs and is the one to serve;
	// if this request won, the read returns what it just wrote. Either way the
	// caller gets the single row the unique index permits.
	d, err = s.store.DefaultFor(ctx, orgID, a.UserID, string(ModuleHome))
	if err != nil {
		return Detail{}, fmt.Errorf("loading your Home dashboard: %w", err)
	}
	d.markValidity()
	return s.detail(ctx, orgID, d, a)
}
