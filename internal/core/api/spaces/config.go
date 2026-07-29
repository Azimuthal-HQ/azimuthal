package spaces

import (
	"net/http"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/api/respond"
)

// BootConfig is the deployment's boot-time behaviour flags, as the browser
// sees them.
//
// # This struct IS the allowlist. Read this before adding a field.
//
// Every field here is published to every member of the organisation. There is
// no filtering step between this struct and the wire, and that is deliberate:
// a filter is a thing to get wrong, whereas a hand-written struct is a
// decision someone had to type.
//
// What may go here: a boot-time flag that changes how the UI must BEHAVE, and
// whose value a member could already infer by using the product. ticket_ref_required
// qualifies on both counts — an operator who omits a reference already learns
// the answer from the 400.
//
// What may never go here, under any framing: connection strings, bucket names,
// endpoints, credentials, key paths, SMTP hosts, or anything else on
// config.Config that is not a feature-behaviour flag. config.Config holds
// DatabaseURL, StorageSecretKey, StorageAccessKey and JWTPrivateKeyPath a few
// struct fields away from TicketRefRequired; the distance between them on the
// wire is this comment and the two tests below it.
//
// Do NOT populate this by reflecting over config.Config, and do NOT give any
// field `omitempty`. Reflection turns an allowlist into a deny-list, which
// publishes whatever the next person adds to config. And omitempty would let a
// field whose value happens to be zero in the test fixture pass the wire-shape
// test while being live in production —
// TestBootConfig_JSONTagsAreTheAllowlist bans it for exactly that reason.
type BootConfig struct {
	// TicketRefRequired mirrors AZIMUTHAL_TICKET_REF_REQUIRED. The UI marks
	// its ticket-reference fields required and refuses to submit without one,
	// so an operator meets the requirement before the request rather than
	// after the 400.
	TicketRefRequired bool `json:"ticket_ref_required"`
}

// BootConfig returns the safe boot-time flags.
//
// It reads the SAME ticketref.Policy value the mutating handlers enforce,
// rather than a second copy of the flag. That is the whole reason it lives on
// this handler: a separate WithBootConfig builder would let the endpoint and
// the enforcement disagree after one forgotten wiring line in main.go, and an
// endpoint that reports "not required" while the mutations refuse is worse
// than no endpoint at all.
//
// The orgID in the path authorises the read; it does not select the values.
// These flags are process-wide, so every org on this deployment gets the same
// answer. The URL is org-scoped because that is where ResolveAccess is
// mounted, which is what makes the route 404 for a non-member — not because
// the configuration is per-org. If it ever becomes per-org, that is a schema
// change and a different endpoint.
//
// @Summary      Deployment flags
// @Description  Boot-time flags the UI needs, on an explicit allowlist in code — never secrets, connection strings or credentials. Process-wide: the organization in the path authorises the read, it does not scope the values.
// @Tags         spaces
// @Produce      json
// @Security     BearerAuth
// @Param        orgID  path      string  true  "Organization ID (UUID)"
// @Success      200    {object}  spaces.BootConfig         "Deployment flags"
// @Failure      401    {object}  api.SwaggerErrorResponse  "Not authenticated"
// @Failure      404    {object}  api.SwaggerErrorResponse  "Not a member of this organization"
// @Router       /orgs/{orgID}/config [get]
func (h *Handler) BootConfig(w http.ResponseWriter, r *http.Request) {
	// No orgID parse and no query: membership was settled by ResolveAccess
	// before this handler ran, and the values do not depend on which org
	// asked.
	respond.JSON(w, http.StatusOK, BootConfig{
		TicketRefRequired: h.ticketRef.Required,
	})
}
