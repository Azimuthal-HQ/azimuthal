import { useEffect, useRef, useState } from 'react';
import { Link, Navigate, useParams } from 'react-router-dom';
import { KeyRound } from 'lucide-react';
import { useRedeemPortalLink } from '../../lib/api';
import { sessionFromRedeem, setPortalSession } from '../../lib/portalSession';
import { Button } from '../../components/ui/button';
import { EmptyState } from '../../shell/EmptyState';
import { portalRequestsHref, portalSignInHref } from './portalLinks';

/**
 * Where the emailed magic link lands: `/portal/{portalKey}/signin/{linkToken}`.
 *
 * THE ROUTE SHAPE IS A CONTRACT WITH THE BACKEND. `internal/core/portal/service.go`
 * composes the emailed URL as `{APP_BASE_URL}/portal/{portalKey}/signin/{rawToken}`
 * — a path segment, not a `?token=` query — and no server route matches it; the
 * SPA handler serves index.html and this component is what gives the URL
 * meaning. See `portalLinks.ts`.
 *
 * THE SESSION IS STORED ONLY AFTER THE SERVER ACCEPTS THE TOKEN. Writing it
 * optimistically — before or alongside the request — would leave a stored
 * "session" whose token the server has already refused, and every later request
 * would 401 into a redirect loop that looks like an outage rather than an
 * expired link. `setPortalSession` is called from `onSuccess` and nowhere else.
 *
 * A REFUSED LINK NEVER GOES TO `/login`. That page is the internal product's
 * sign-in, and sending an external customer there both dead-ends them (they
 * have no account and cannot make one) and discloses that an internal product
 * is on the other side of this domain. The recovery is the portal's own
 * sign-in page, which can issue a fresh link.
 */
export function PortalRedeemPage() {
  const { portalKey = '', linkToken = '' } = useParams();
  const redeem = useRedeemPortalLink();
  const [signedIn, setSignedIn] = useState(false);
  const [failed, setFailed] = useState(false);

  // One attempt per mounted token. A magic link is single-use, so a second
  // redeem of the same token answers 401 — under StrictMode's double-invoked
  // effects that would turn every successful sign-in into a failure.
  const attempted = useRef('');

  useEffect(() => {
    if (!portalKey || !linkToken) {
      setFailed(true);
      return;
    }
    const attemptKey = `${portalKey}:${linkToken}`;
    if (attempted.current === attemptKey) return;
    attempted.current = attemptKey;

    redeem.mutate(
      { portalKey, token: linkToken },
      {
        onSuccess: (res) => {
          setPortalSession(portalKey, sessionFromRedeem(res));
          setSignedIn(true);
        },
        onError: () => setFailed(true),
      },
    );
    // `redeem` is a fresh object each render; the ref above is the real guard.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [portalKey, linkToken]);

  if (signedIn) {
    return <Navigate to={portalRequestsHref(portalKey)} replace />;
  }

  if (failed) {
    return (
      <div data-testid="portal-redeem-failed">
        <EmptyState
          icon={KeyRound}
          title="This sign-in link no longer works"
          description="Sign-in links can be used once and expire after a short time. Request a fresh one and it will arrive in a moment."
          className="bg-[var(--color-surface)]"
          action={
            <Button asChild>
              <Link to={portalSignInHref(portalKey)} data-testid="portal-redeem-retry">
                Request a new link
              </Link>
            </Button>
          }
        />
      </div>
    );
  }

  return (
    <p
      className="text-center text-[var(--text-sm)] text-[var(--color-text-muted)]"
      data-testid="portal-redeem-pending"
    >
      Signing you in…
    </p>
  );
}
