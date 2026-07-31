import { Link, useNavigate, useParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { AlertCircle, Inbox } from 'lucide-react';
import { friendlyErrorMessage, usePortalRequests, usePortalSignOut } from '../../lib/api';
import { clearPortalSession, getPortalSession } from '../../lib/portalSession';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { EmptyState } from '../../shell/EmptyState';
import { PortalStatus } from '../../components/portal/PortalStatus';
import { portalNewRequestHref, portalRequestHref, portalSignInHref } from './portalLinks';

/**
 * The requester's own requests.
 *
 * Each row carries a summary, a status and two dates — and nothing else,
 * because nothing else is on the wire. `requestView` has no space, no number,
 * no assignee, no priority, no labels and no workflow state, so there is no
 * field here to render carelessly. The link target is the opaque `reference`.
 *
 * SIGNING OUT IS THREE STEPS, and dropping any one of them leaves a usable
 * session behind:
 *
 *  1. the server call bumps the requester's session generation, which is what
 *     actually invalidates the token — everything else is local tidying;
 *  2. `clearPortalSession` removes the stored token, so this browser stops
 *     presenting it;
 *  3. `queryClient.clear()` drops the CACHE. localStorage and the in-memory
 *     query cache are separate stores, one QueryClient serves the whole app,
 *     and without this the next visitor to this machine sees the previous
 *     requester's summaries rendered from cache before any request is made.
 *
 * The wording is deliberately not "sign out of this portal": the session
 * generation lives on the REQUESTER, not on the portal membership, so signing
 * out ends every portal session that requester holds in this organisation.
 */
export function PortalRequestsPage() {
  const { portalKey = '' } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const session = getPortalSession(portalKey);
  const email = session?.requester.email ?? '';

  const requests = usePortalRequests(portalKey, email);
  const signOut = usePortalSignOut(portalKey);

  const finishSignOut = () => {
    clearPortalSession(portalKey);
    queryClient.clear();
    navigate(portalSignInHref(portalKey), { replace: true });
  };

  const doSignOut = () => {
    // The local half runs either way. A failed server call must not strand a
    // requester in a session they have asked to leave — and the stored token
    // is the only thing this browser can present.
    signOut.mutate(undefined, { onSuccess: finishSignOut, onError: finishSignOut });
  };

  return (
    <div data-testid="portal-requests-page">
      <div className="mb-[var(--space-4)] flex items-center justify-between gap-[var(--space-3)]">
        <div className="min-w-0">
          <h2 className="text-[var(--text-lg)] font-semibold text-[var(--color-text)]">
            Your requests
          </h2>
          {session?.requester.email && (
            <p className="truncate text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Signed in as {session.requester.email}
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-[var(--space-2)]">
          <Button asChild>
            <Link to={portalNewRequestHref(portalKey)} data-testid="portal-new-request">
              New request
            </Link>
          </Button>
          <Button
            variant="ghost"
            onClick={doSignOut}
            disabled={signOut.isPending}
            data-testid="portal-sign-out"
          >
            {signOut.isPending ? 'Signing out…' : 'Sign out'}
          </Button>
        </div>
      </div>

      <RequestsBody
        portalKey={portalKey}
        isLoading={requests.isLoading}
        error={requests.error}
        onRetry={() => void requests.refetch()}
        rows={requests.data}
      />
    </div>
  );
}

interface RequestsBodyProps {
  portalKey: string;
  isLoading: boolean;
  error: unknown;
  onRetry: () => void;
  rows?: Array<{
    reference: string;
    summary: string;
    status: string;
    created_at: string;
    updated_at: string;
  }>;
}

/**
 * Error, then loading, then empty, then content — the flat ordered model
 * `SearchBody` uses, for the same reason. "The request failed" and "you have
 * raised nothing yet" are different answers, and rendering a failure as an
 * empty list tells a customer their requests are gone.
 */
function RequestsBody({ portalKey, isLoading, error, onRetry, rows }: RequestsBodyProps) {
  if (error) {
    return (
      <EmptyState
        icon={AlertCircle}
        title="Your requests couldn’t be loaded"
        description={friendlyErrorMessage(error, 'Something went wrong fetching your requests.')}
        className="bg-[var(--color-surface)]"
        action={
          <Button variant="outline" onClick={onRetry} data-testid="portal-requests-retry">
            Try again
          </Button>
        }
      />
    );
  }

  if (isLoading || !rows) {
    return (
      <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]" data-testid="portal-requests-loading">
        Loading your requests…
      </p>
    );
  }

  if (rows.length === 0) {
    return (
      <EmptyState
        icon={Inbox}
        title="No requests yet"
        description="When you raise a request it will appear here, along with every reply from the team."
        className="bg-[var(--color-surface)]"
        action={
          <Button asChild>
            <Link to={portalNewRequestHref(portalKey)}>Raise your first request</Link>
          </Button>
        }
      />
    );
  }

  return (
    <ul className="flex flex-col gap-[var(--space-2)]">
      {rows.map((row) => (
        <li key={row.reference}>
          <Link
            to={portalRequestHref(portalKey, row.reference)}
            data-testid="portal-request-row"
            className="block rounded-[var(--radius-lg)] transition-colors hover:bg-[var(--color-surface-hover)]"
          >
            <Card>
              <CardContent className="flex items-start justify-between gap-[var(--space-3)] p-[var(--space-4)]">
                <div className="min-w-0">
                  <p className="truncate text-[var(--text-base)] font-medium text-[var(--color-text)]">
                    {row.summary}
                  </p>
                  <p className="mt-[var(--space-1)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    Raised {new Date(row.created_at).toLocaleDateString()} · Updated{' '}
                    {new Date(row.updated_at).toLocaleDateString()}
                  </p>
                </div>
                <PortalStatus status={row.status} />
              </CardContent>
            </Card>
          </Link>
        </li>
      ))}
    </ul>
  );
}
