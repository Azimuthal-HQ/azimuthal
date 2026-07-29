package dashboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/views"
)

// Config is a gadget's stored configuration document.
//
// One struct across every gadget kind, with the registry deciding which keys
// each kind may set. The alternative — a document type per kind — needs a
// discriminated union in the database, in Go, in the wire format and in
// TypeScript, to express four optional keys.
//
// Every field is optional and every zero value is a real default. Nothing here
// is a query: see the note on Definition.Query and ADR-0009 decision 2.
type Config struct {
	// Title overrides the tile heading. Empty means "use the view's name, or
	// the gadget's own name" — resolved at read time so that renaming a view
	// renames every untitled gadget that shows it.
	Title string `json:"title,omitempty"`
	// Limit is how many rows a list gadget shows. Nil means DefaultGadgetLimit.
	// A pointer rather than an int because 0 is not a legal limit and must be
	// distinguishable from "not set" — the tri-state that a bare int collapses.
	Limit *int `json:"limit,omitempty"`
	// GroupBy is the breakdown field, from the views vocabulary.
	GroupBy string `json:"group_by,omitempty"`
	// Body is a note's markdown source. It is the one genuinely user-authored
	// string in this package, stored and returned verbatim; it is rendered by
	// the frontend's markdown renderer with raw HTML left escaped.
	Body string `json:"body,omitempty"`
}

// ErrUnknownConfigKey reports a configuration key the vocabulary does not
// define, or one this gadget kind does not carry. Refused rather than dropped,
// for the reason views.ErrUnknownField is: storing a key this build does not
// understand means a later build silently changes what the gadget means.
var ErrUnknownConfigKey = errors.New("unknown gadget configuration key")

// invalid builds a views.ValidationError from this package.
//
// A local wrapper rather than calling views.Invalid at each site: these
// messages are written to be READ — they name the bound the caller exceeded —
// and wrapping them to satisfy the error-chain linter would prefix every one
// of them with a package name the reader does not need. The type is shared
// with saved views on purpose, so both surfaces answer 422 through one branch.
//
//nolint:wrapcheck // the whole point of the wrapper is that the message reaches the caller unprefixed
func invalid(format string, a ...any) error { return views.Invalid(format, a...) }

// ParseConfig decodes and validates a stored or submitted config document
// against one gadget definition.
//
// Two passes, and both are needed. The first reads the raw object's keys so a
// key that is in the vocabulary but not in THIS kind's ConfigKeys can be named
// in the error — a note carrying a group_by is a mistake worth reporting
// rather than silently ignoring. The second decodes with DisallowUnknownFields
// so a key in no kind's vocabulary is refused by the decoder itself.
//
// There is no trailing-content check on the second pass, unlike
// views.ParseQuery. It would be unreachable: the first pass is a plain
// json.Unmarshal, which already refuses anything after the top-level value.
func ParseConfig(d Definition, raw []byte) (Config, error) {
	if len(raw) == 0 {
		return Config{}, nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return Config{}, fmt.Errorf("malformed gadget configuration: %w", err)
	}
	for k := range keys {
		if !d.AllowsConfigKey(ConfigKey(k)) {
			return Config{}, fmt.Errorf("%w: %q is not a setting of the %q gadget", ErrUnknownConfigKey, k, d.Key)
		}
	}

	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return Config{}, fmt.Errorf("%w: %w", ErrUnknownConfigKey, err)
		}
		return Config{}, fmt.Errorf("malformed gadget configuration: %w", err)
	}
	if err := c.validate(d); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c *Config) validate(d Definition) error {
	c.Title = strings.TrimSpace(c.Title)
	if len([]rune(c.Title)) > MaxTitleLen {
		return invalid("a gadget title may be at most %d characters", MaxTitleLen)
	}
	if c.Limit != nil {
		if *c.Limit < MinGadgetLimit || *c.Limit > MaxGadgetLimit {
			return invalid("a gadget may show between %d and %d rows (got %d)",
				MinGadgetLimit, MaxGadgetLimit, *c.Limit)
		}
	}
	if c.GroupBy != "" {
		// Re-raised as this package's own validation error rather than passed
		// through: the message is already the one to show, and the caller's
		// fix is the same either way.
		if _, err := views.ParseGroupField(c.GroupBy); err != nil {
			return invalid("%s", err)
		}
	}
	if d.Key == GadgetBreakdown && c.GroupBy == "" {
		// A breakdown with no field is not a defaulted breakdown, it is an
		// unanswerable one. Refuse at write time rather than render a tile
		// that can only ever say "nothing to show".
		return invalid("a breakdown gadget needs a field to group by")
	}
	if len([]rune(c.Body)) > MaxNoteLen {
		return invalid("a note may be at most %d characters (got %d)", MaxNoteLen, len([]rune(c.Body)))
	}
	return nil
}

// Encode serialises a validated config for storage. Storage always goes
// through this rather than through the caller's original bytes, so a document
// in the table is by construction one this build produced and understands —
// the same rule views.Query.Encode enforces on the filter document.
func (c Config) Encode(d Definition) ([]byte, error) {
	if err := c.validate(d); err != nil {
		return nil, err
	}
	out, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encoding gadget configuration: %w", err)
	}
	return out, nil
}

// RowLimit is the number of rows a list gadget should fetch.
func (c Config) RowLimit() int {
	if c.Limit == nil {
		return DefaultGadgetLimit
	}
	return *c.Limit
}
