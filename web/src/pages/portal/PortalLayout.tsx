import { Outlet, useParams } from 'react-router-dom';
import { AlertCircle } from 'lucide-react';
import { usePortalDescribe } from '../../lib/api';
import { ErrorBoundary } from '../../components/ErrorBoundary';
import { Logo } from '../../components/layout/Logo';
import { EmptyState } from '../../shell/EmptyState';

/**
 * The customer portal's own frame.
 *
 * It is modelled on `pages/auth/InviteAcceptPage.tsx` — a centred `min-h-screen`
 * main, the logo, a card — and NOT on `shell/AppShell.tsx`, for three reasons
 * that are each load-bearing rather than cosmetic:
 *
 *  1. There is no TopBar here, so there is no `pt-[var(--topnav-height)]`. That
 *     padding exists solely to clear the fixed navigation bar; kept without the
 *     bar it is an unexplained gap at the top of every portal page.
 *  2. It never calls `useAuth` or `useOrganization`. A requester has no users
 *     row, no membership and no grant (migration 044) — those hooks would
 *     resolve to nothing at best, and at worst would push an internal token
 *     into a surface that must not have one.
 *  3. It imports nothing from `shell/` except `EmptyState`. Every other shell
 *     component exists to render container context — space switcher, module
 *     tabs, breadcrumbs — which is precisely what this surface withholds. The
 *     import list is the enforcement: a component that is not imported cannot
 *     render a space name by accident.
 *
 * The heading uses the portal's own public name and blurb from
 * `GET /portal/{portalKey}`, which is all an anonymous visitor is told: not the
 * space it belongs to, not the organisation, not how many requests exist.
 */
export function PortalLayout() {
  const { portalKey = '' } = useParams();
  const describe = usePortalDescribe(portalKey);

  const portalName = describe.data?.name?.trim() || 'Support';
  const intro = describe.data?.intro?.trim() || '';

  return (
    <main
      className="flex min-h-screen justify-center bg-[var(--color-bg)] px-[var(--space-4)] py-[var(--space-8)]"
      data-testid="portal-layout"
    >
      <div className="w-full max-w-2xl">
        <div className="mb-[var(--space-4)] flex justify-center">
          <Logo />
        </div>

        {/* The describe call is public and cheap; while it is in flight the
            frame renders with a neutral name rather than flashing a wrong one. */}
        {!describe.isLoading && (
          <header className="mb-[var(--space-5)] text-center">
            <h1
              className="text-[var(--text-2xl)] font-semibold text-[var(--color-text)]"
              data-testid="portal-name"
            >
              {portalName}
            </h1>
            {intro && (
              <p className="mt-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
                {intro}
              </p>
            )}
          </header>
        )}

        {/* A portal that does not resolve — wrong key, or switched off — is a
            dead end for everybody, signed in or not, so the frame answers for
            it once instead of every child page answering separately. The copy
            says nothing about WHY: "no such portal" and "that portal is
            disabled" are the same sentence to an outsider, and the server
            already refuses to distinguish them. */}
        {describe.error ? (
          <EmptyState
            icon={AlertCircle}
            title="This portal isn’t available"
            description="The link may be incomplete, or this request portal may no longer be open. Check the address you were given, or contact the team you were working with."
            className="bg-[var(--color-surface)]"
          />
        ) : (
          <ErrorBoundary
            fallback={
              <EmptyState
                icon={AlertCircle}
                title="Something went wrong"
                description="This page could not be displayed. Reload and try again."
                className="bg-[var(--color-surface)]"
              />
            }
          >
            <Outlet />
          </ErrorBoundary>
        )}
      </div>
    </main>
  );
}
