/**
 * Portal session storage.
 *
 * Storage ONLY — no network. Every portal request goes through
 * `lib/api.ts`'s `apiFetch` with `credential: 'portal'`, which reads
 * `getPortalToken` from here. CLAUDE.md §1 permits exactly one network client
 * and this is not it.
 *
 * ## Why a separate store, and not `azimuthal_access_token`
 *
 * The internal session and a portal session are different credential families
 * signed with the SAME RSA key; the `aud` claim is the whole boundary
 * (`internal/core/portal/token.go`). Sharing one localStorage key would be
 * actively destructive rather than merely untidy:
 *
 *  1. `apiFetch` attaches the stored token to every internal request, so a
 *     portal token would ride along to `/auth/me`, be refused on its audience,
 *     and trip the 401 handler — which clears BOTH tokens and hard-navigates to
 *     `/login`. An agent who opened their own portal in the same browser would
 *     silently lose their internal session, and vice versa.
 *  2. `getCurrentOrgId()` in `lib/auth.ts` decodes `org_id` off whatever token
 *     it finds, and the portal token really does carry `org_id` (it mirrors
 *     `auth.Claims`' wire shape; it is not an authorisation input). So
 *     `spaceBase()` would build plausible-looking internal URLs from a portal
 *     token and 401 with no clue why.
 *  3. `AuthProvider`'s expiry interval would call `handleLogout()` on it.
 *
 * The key is scoped per portal because one requester may hold sessions to two
 * portals in the same org, and a session is bound to one `pid` — presenting a
 * session for portal A to portal B answers 404, not 401.
 *
 * The `:` segment cannot collide with the internal keys (`_`-separated) or the
 * shell preference keys (`-`-separated).
 */

const PORTAL_SESSION_PREFIX = 'azimuthal_portal_session:';

/**
 * What the redeem response gives us, kept because nothing re-serves it: there
 * is no `GET /my/me` and no refresh endpoint. A 401 means sign in again.
 */
export interface PortalSession {
  session_token: string;
  /** ms epoch, computed from the response's `expires_in` (SECONDS). */
  expires_at: number;
  requester: { email: string; name: string };
  portal: { name: string; intro: string };
}

function keyFor(portalKey: string): string {
  return `${PORTAL_SESSION_PREFIX}${portalKey}`;
}

export function getPortalSession(portalKey: string): PortalSession | null {
  if (!portalKey) return null;
  const raw = localStorage.getItem(keyFor(portalKey));
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as PortalSession;
    if (!parsed?.session_token) return null;
    return parsed;
  } catch {
    // A corrupt record is indistinguishable from no session, and the recovery
    // is identical: sign in again.
    return null;
  }
}

/**
 * The token, or null. Returns null for an EXPIRED session rather than sending
 * a token the server will refuse — the redirect to the sign-in page is then a
 * local decision instead of a round trip that looks like an outage.
 */
export function getPortalToken(portalKey: string): string | null {
  const session = getPortalSession(portalKey);
  if (!session) return null;
  if (session.expires_at && session.expires_at <= Date.now()) return null;
  return session.session_token;
}

export function setPortalSession(portalKey: string, session: PortalSession): void {
  localStorage.setItem(keyFor(portalKey), JSON.stringify(session));
}

export function clearPortalSession(portalKey: string): void {
  localStorage.removeItem(keyFor(portalKey));
}

/**
 * Build the stored record from the redeem response.
 *
 * `expires_in` is in SECONDS on the wire; storing an absolute ms epoch means a
 * page loaded days later does not treat a stale relative value as fresh.
 */
export function sessionFromRedeem(res: {
  session_token: string;
  expires_in: number;
  requester: { email: string; name: string };
  portal: { name: string; intro: string };
}): PortalSession {
  return {
    session_token: res.session_token,
    expires_at: Date.now() + res.expires_in * 1000,
    requester: res.requester,
    portal: res.portal,
  };
}
