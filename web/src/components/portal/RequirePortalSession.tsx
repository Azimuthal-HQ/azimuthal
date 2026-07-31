import { Navigate, Outlet, useParams } from 'react-router-dom';
import { getPortalToken } from '../../lib/portalSession';
import { portalSignInHref } from '../../pages/portal/portalLinks';

/**
 * The portal's session guard, mirroring `components/auth/RequireAuth.tsx`:
 * it reads storage directly rather than a context, so a route is protected by
 * being declared under it and nothing else.
 *
 * TWO DIFFERENCES FROM `RequireAuth`, and both matter.
 *
 * It reads the PORTAL token, keyed by portal. A requester may hold sessions to
 * two portals in the same organisation and a session is bound to one `pid`;
 * presenting portal A's session to portal B answers 404, not 401. So the guard
 * asks for this portal's token specifically. `getPortalToken` also returns null
 * for an expired session rather than a token the server will refuse, which
 * turns a round trip that looks like an outage into a local redirect.
 *
 * It redirects to the PORTAL's sign-in page, never to `/login`. An external
 * requester has no internal account and cannot create one, so `/login` is a
 * dead end that also discloses that an internal product lives on this domain.
 * It is rendered as a layout route so that the redirect decision is made once,
 * above the pages, instead of being repeated — and forgotten — in each of them.
 */
export function RequirePortalSession() {
  const { portalKey = '' } = useParams();

  if (!getPortalToken(portalKey)) {
    return <Navigate to={portalSignInHref(portalKey)} replace />;
  }

  return <Outlet />;
}
