package spaces_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	spacesapi "github.com/Azimuthal-HQ/azimuthal/internal/core/api/spaces"
)

// TestBootConfig_JSONTagsAreTheAllowlist is the real guard on what
// GET /orgs/{orgID}/config publishes, and it is a guard the integration test
// cannot be.
//
// An integration test asserting the response's key set passes vacuously for a
// field added as `json:"database_url,omitempty"` — the value is zero in the
// harness, the key never reaches the wire, the assertion sees the allowlist it
// expected, and the field is live in production. It passes just as vacuously
// for a field with NO json tag at all, if the check only looks at tag names:
// encoding/json serialises that field under its Go name.
//
// So this reads the TYPE, not a response, and treats both dodges as failures.
func TestBootConfig_JSONTagsAreTheAllowlist(t *testing.T) {
	// The published contract. Adding to this list is a disclosure decision;
	// read the doc comment on spaces.BootConfig before you do.
	allowed := []string{"ticket_ref_required"}

	typ := reflect.TypeOf(spacesapi.BootConfig{})
	require.Equal(t, reflect.Struct, typ.Kind())

	var names []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		tag, ok := field.Tag.Lookup("json")

		// An untagged field is NOT skipped: encoding/json publishes it under
		// the Go field name, so skipping it here would be the same hole the
		// test exists to close.
		require.True(t, ok,
			"BootConfig.%s has no json tag, so it would be published under its Go name — every field here is a deliberate disclosure and must say so",
			field.Name)

		name, opts, _ := strings.Cut(tag, ",")
		require.NotContains(t, opts, "omitempty",
			"BootConfig.%s uses omitempty, which lets a zero value vanish from the wire in tests while shipping in production",
			field.Name)
		require.NotEqual(t, "-", name,
			"BootConfig.%s is json:\"-\"; a field that is not published does not belong on this struct at all", field.Name)
		require.NotEmpty(t, name, "BootConfig.%s has an empty json name", field.Name)
		require.Equal(t, strings.ToLower(name), name,
			"the wire format is lowercase snake_case (BootConfig.%s)", field.Name)

		names = append(names, name)
	}

	require.ElementsMatch(t, allowed, names,
		"the boot-config wire contract is exactly %v — adding a key is a decision about what every org member may read", allowed)
}
