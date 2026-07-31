import { Link, useParams } from 'react-router-dom';
import { Compass } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { EmptyState } from '../../shell/EmptyState';
import { portalSignInHref } from './portalLinks';

/**
 * The portal subtree's own catch-all.
 *
 * IT EXISTS BECAUSE THE SHELL'S CATCH-ALL IS INSIDE THE SHELL ROUTE. Without a
 * `path="*"` declared here, an unknown `/portal/...` URL — a truncated magic
 * link, a stale bookmark, a typo — falls through to
 * `<Route element={<RequireAuth><AppShell/></RequireAuth>}>`, where an external
 * requester with no internal account is redirected to `/login` and, on the way,
 * shown the internal product's chrome. That is the zero-context guarantee being
 * broken by the router rather than by a component, which is exactly the kind of
 * leak no page-level review catches.
 *
 * The recovery link goes to this portal's sign-in page, never to `/login`.
 */
export function PortalNotFoundPage() {
  const { portalKey = '' } = useParams();
  return (
    <div data-testid="portal-not-found">
      <EmptyState
        icon={Compass}
        title="This page isn’t here"
        description="The address may be incomplete, or the page may have moved. Start again from the sign-in page and you’ll get back to your requests."
        className="bg-[var(--color-surface)]"
        action={
          <Button asChild>
            <Link to={portalSignInHref(portalKey)}>Go to sign in</Link>
          </Button>
        }
      />
    </div>
  );
}
