/**
 * Every URL the customer portal is allowed to produce, in one place.
 *
 * Separate from the components for the reason `components/search/searchLinks.ts`
 * is separate: these are plain functions, and a module exporting both functions
 * and components breaks fast refresh — which is what
 * react-refresh/only-export-components is protecting. It also makes every href
 * unit-testable without rendering anything, and this surface has an href rule
 * worth testing mechanically.
 *
 * THE RULE: the portal links only to the portal. An external requester must
 * learn nothing about the container their request lives in, and a link is the
 * easiest place to leak one — `/beacon/{spaceId}/tickets/{id}` names the
 * module, the space and the internal ticket in a single string. So no builder
 * here composes anything but `/portal/…`, and the only identifiers that reach a
 * URL are the two opaque ones the server already chose to publish: the portal
 * key (random, NOT derived from the space slug/key/name — see migration 044's
 * format CHECK) and the request reference (a bare ticket UUID, NOT the
 * `BEA-42` form, which is composed from the space key).
 *
 * `/portal/{portalKey}/signin/{linkToken}` is a HARD CONTRACT, not a choice.
 * The backend builds the emailed magic link as
 * `{APP_BASE_URL}/portal/{portalKey}/signin/{rawToken}` in
 * `internal/core/portal/service.go`, as a PATH rather than a `?token=` query.
 * No server route serves it — the SPA handler returns index.html — so the route
 * exists only because this frontend declares it. Changing the shape here
 * silently breaks every link already sitting in a customer's inbox.
 */

const PORTAL_ROOT = '/portal';

/**
 * One path segment, or null when there is nothing to build from.
 *
 * The literal strings are checked because a value that has already been
 * stringified through a template somewhere upstream arrives as the WORD
 * "undefined", which a bare `${value}` would happily bake into an href and
 * which reads as a real URL right up until it 404s.
 */
function segment(value: string | null | undefined): string | null {
  if (value === null || value === undefined) return null;
  const trimmed = String(value).trim();
  if (!trimmed || trimmed === 'undefined' || trimmed === 'null') return null;
  return encodeURIComponent(trimmed);
}

/**
 * The portal's sign-in page: the subtree's index route.
 *
 * The keyless fallback is unreachable through the router — every caller runs
 * inside `/portal/:portalKey`, which cannot match an empty key — and exists so
 * that a builder handed nothing produces a short, harmless path instead of a
 * plausible-looking one containing the word "undefined".
 */
export function portalSignInHref(portalKey: string): string {
  const key = segment(portalKey);
  return key ? `${PORTAL_ROOT}/${key}` : PORTAL_ROOT;
}

/**
 * Where the emailed magic link lands. The token is the credential, exactly as
 * it is for invite acceptance, so this route sits outside every auth wall.
 */
export function portalRedeemHref(portalKey: string, linkToken: string): string {
  const key = segment(portalKey);
  const token = segment(linkToken);
  if (!key) return PORTAL_ROOT;
  if (!token) return `${PORTAL_ROOT}/${key}`;
  return `${PORTAL_ROOT}/${key}/signin/${token}`;
}

/** The requester's own list of requests. */
export function portalRequestsHref(portalKey: string): string {
  const key = segment(portalKey);
  return key ? `${PORTAL_ROOT}/${key}/requests` : PORTAL_ROOT;
}

/** The compose form for a new request. */
export function portalNewRequestHref(portalKey: string): string {
  const key = segment(portalKey);
  return key ? `${PORTAL_ROOT}/${key}/requests/new` : PORTAL_ROOT;
}

/**
 * One request. `reference` is the server's opaque handle; it is passed through
 * untouched and never decomposed, decorated or prefixed.
 *
 * A missing reference degrades to the list rather than to
 * `/portal/{key}/requests/undefined`: the list is a real page the requester can
 * act on, and it discloses nothing the list route did not already.
 */
export function portalRequestHref(portalKey: string, reference: string): string {
  const key = segment(portalKey);
  const ref = segment(reference);
  if (!key) return PORTAL_ROOT;
  if (!ref) return `${PORTAL_ROOT}/${key}/requests`;
  return `${PORTAL_ROOT}/${key}/requests/${ref}`;
}
