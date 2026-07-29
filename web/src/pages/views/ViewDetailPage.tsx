import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { AlertCircle, ArrowLeft, Pencil } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { ScopeUnavailable } from '../../components/views/ScopeUnavailable';
import { ViewResultList } from '../../components/views/ViewResultList';
import {
  ViewOwnerChip,
  ViewScopeChip,
  ViewVisibilityChip,
} from '../../components/views/ViewChips';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useSavedView,
  useViewResults,
  type ViewResultPage,
} from '../../lib/api';

/**
 * /views/{id} — one saved view, resolved for whoever is reading it.
 *
 * Two behaviours here are the feature rather than faults to smooth over:
 *
 *  - The rows are resolved against the READER's access, so two people opening
 *    this page legitimately see different results and a reader with less access
 *    sees fewer. Nothing on this page presents that as a sync problem.
 *  - An invalid view still opens. Its definition loaded fine; it is its scope
 *    that has gone, so the results area becomes the scope-unavailable state and
 *    the results request is never made.
 */
export function ViewDetailPage() {
  const { viewId = '' } = useParams<{ viewId: string }>();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const viewQuery = useSavedView(orgId, viewId);
  const view = viewQuery.data;

  // Keyset paging: the stack of cursors that led here, so Previous is a pop
  // rather than a second query the API has no way to express.
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.length > 0 ? cursors[cursors.length - 1] : undefined;

  const resultsQuery = useViewResults(orgId, viewId, cursor, undefined, {
    enabled: !!orgId && !!viewId && view?.is_valid === true,
    placeholderData: (prev: ViewResultPage | undefined) => prev,
  });

  if (viewQuery.error) {
    return (
      <div className="space-y-4">
        <BackLink />
        <div
          data-testid="view-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(viewQuery.error, 'This view is unavailable right now.')}
          </p>
        </div>
      </div>
    );
  }

  if (!view) {
    return (
      <div className="space-y-4">
        <BackLink />
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading this view…
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <BackLink />

      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            {view.name}
          </h1>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <ViewOwnerChip view={view} />
            <ViewVisibilityChip visibility={view.visibility} teamName={view.team_name} />
            {!view.is_valid && <ViewScopeChip />}
          </div>
          {view.description && (
            <p className="mt-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {view.description}
            </p>
          )}
        </div>
        {view.is_owner && (
          <Button asChild variant="outline" data-testid="edit-view">
            <Link to={`/views/${view.id}/edit`}>
              <Pencil className="mr-2 h-4 w-4" />
              Edit view
            </Link>
          </Button>
        )}
      </div>

      {view.is_valid ? (
        <ViewResultList
          page={resultsQuery.data}
          isLoading={resultsQuery.isLoading || resultsQuery.isFetching}
          error={resultsQuery.error}
          errorFallback="The results of this view are unavailable right now."
          emptyTitle="Nothing matches this view"
          emptyDescription="Every filter here is combined, and results are resolved against your own access — so a colleague opening the same view may see rows you cannot."
          meId={user?.id}
          canPrev={cursors.length > 0}
          onPrev={() => setCursors((prev) => prev.slice(0, -1))}
          onNext={() => {
            const next = resultsQuery.data?.next_cursor;
            if (next) setCursors((prev) => [...prev, next]);
          }}
        />
      ) : (
        <ScopeUnavailable view={view} />
      )}
    </div>
  );
}

function BackLink() {
  return (
    <Link
      to="/views"
      className="inline-flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
    >
      <ArrowLeft className="h-4 w-4" />
      All views
    </Link>
  );
}
