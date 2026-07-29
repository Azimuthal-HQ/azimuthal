package assess

import (
	"fmt"
	"sort"
	"strings"
)

// KeyRegistry detects the collisions an import would hit on
// idx_project_items_org_key, the UNIQUE (org_id, item_key) index migration 031
// created.
//
// # Why this is cross-export and not per-export
//
// item_key is <SPACE_KEY>-<number> and unique per organisation, not per space.
// Two Jira projects cannot collide inside one Jira instance because Jira
// enforces its own key uniqueness — but a Jira project and a Confluence space
// can, because nothing coordinates the two, and both become spaces in the same
// org. So a collision is invisible in either export alone and only appears when
// they are assessed together. That is the whole reason this tool accepts both
// at once.
//
// Coercion makes it worse rather than better: the space key format is
// ^[A-Z0-9]{1,10}$, so "my-docs" and "MY DOCS" both coerce to MYDOCS, and two
// keys that were distinct in Confluence stop being distinct in Azimuthal.
type KeyRegistry struct {
	// byCoerced maps the coerced space key to every origin that produced it.
	byCoerced map[string][]KeyOrigin
}

// KeyOrigin is one export's claim on a space key.
type KeyOrigin struct {
	// Source names which export it came from, for the report.
	Source string
	// Original is the key as the export spells it.
	Original string
	// Coerced is what the spaces table would accept.
	Coerced string
	// Coercion records that the key had to change shape.
	Coercion bool
	// Items counts entities that would carry keys under this space, so the
	// report can say how much rides on a collision.
	Items int
}

// NewKeyRegistry returns an empty registry.
func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{byCoerced: make(map[string][]KeyOrigin)}
}

// Add records one project or space key.
func (r *KeyRegistry) Add(source, original string, items int) KeyOrigin {
	coerced, changed := CoerceSpaceKey(original)
	o := KeyOrigin{
		Source:   source,
		Original: strings.TrimSpace(original),
		Coerced:  coerced,
		Coercion: changed,
		Items:    items,
	}
	r.byCoerced[coerced] = append(r.byCoerced[coerced], o)
	return o
}

// Collision is one coerced key claimed by more than one origin.
type Collision struct {
	// Key is the coerced space key being contended.
	Key string `json:"key"`
	// Origins are the claimants, in stable order.
	Origins []KeyOrigin `json:"origins"`
	// CrossExport records that the claimants came from different exports,
	// which is the case neither export could have revealed alone.
	CrossExport bool `json:"cross_export"`
	// Items is the total number of keyed entities at stake.
	Items int `json:"items"`
}

// Describe renders a collision for the report.
func (c Collision) Describe() string {
	parts := make([]string, 0, len(c.Origins))
	for _, o := range c.Origins {
		parts = append(parts, fmt.Sprintf("%s %q", o.Source, o.Original))
	}
	scope := "within one export"
	if c.CrossExport {
		scope = "across the two exports, which neither could reveal alone"
	}
	return fmt.Sprintf("%s is claimed by %s (%s); %d keyed entities are affected",
		c.Key, strings.Join(parts, " and "), scope, c.Items)
}

// Collisions returns every contended key, in stable order.
//
// An empty coerced key is reported as a collision too when more than one origin
// produces it: a key made entirely of characters the format strips leaves
// nothing to key on at all.
func (r *KeyRegistry) Collisions() []Collision {
	var out []Collision
	for key, origins := range r.byCoerced {
		if len(origins) < 2 {
			continue
		}
		out = append(out, buildCollision(key, origins))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func buildCollision(key string, origins []KeyOrigin) Collision {
	sorted := make([]KeyOrigin, len(origins))
	copy(sorted, origins)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Source != sorted[j].Source {
			return sorted[i].Source < sorted[j].Source
		}
		return sorted[i].Original < sorted[j].Original
	})

	c := Collision{Key: key, Origins: sorted}
	first := sorted[0].Source
	for _, o := range sorted {
		c.Items += o.Items
		if o.Source != first {
			c.CrossExport = true
		}
	}
	return c
}

// Coercions returns every origin whose key had to change shape, in stable
// order. These are reportable even without a collision: a key silently changing
// is how a cross-reference stops resolving.
func (r *KeyRegistry) Coercions() []KeyOrigin {
	var out []KeyOrigin
	for _, origins := range r.byCoerced {
		for _, o := range origins {
			if o.Coercion {
				out = append(out, o)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Original < out[j].Original
	})
	return out
}

// Count is how many keys were registered.
func (r *KeyRegistry) Count() int {
	n := 0
	for _, origins := range r.byCoerced {
		n += len(origins)
	}
	return n
}
