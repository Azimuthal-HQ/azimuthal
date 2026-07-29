import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { AlertCircle, ArrowLeft, ListFilter, Pencil } from 'lucide-react';
import { Button } from '../../../components/ui/button';
import { EmptyState } from '../../../shell/EmptyState';
import { spacePath } from '../../../shell/modules';
import { ViewResultList } from '../../../components/views/ViewResultList';
import { useAuth } from '../../../lib/auth';
import {
  friendlyErrorMessage,
  useQueueResults,
  useQueues,
  type ViewResultPage,
} from '../../../lib/api';

/**
 * /beacon/{spaceId}/queues/{queueId} — one queue, resolved for whoever opens it.
 *
 * # The rows differ per reader, and that is the product
 *
 * A queue stores a question. "Assigned to me" is stored as the literal token
 * `me` and resolved against the calling agent, so this page legitimately shows
 * two agents different work at the same moment. Nothing here presents that as
 * stale data, a cache problem or a sync failure, and there is no refresh
 * affordance implying otherwise.
 *
 * The rows themselves are `ViewResultList` — the same component the saved-view
 * detail page and the view builder's preview use. A queue is a saved view, so a
 * second results list would be a second answer to a question that already has
 * one (`docs/design/shared-surfaces.md`).
 */
export function QueueDetailPage() {
  const { spaceId = '', queueId = '' } = useParams<{ spaceId: string; queueId: string }>();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  // There is no single-queue GET: the list is the definition source, and it is
  // already cached by the sidebar, so this costs nothing extra.
  const queuesQuery = useQueues(orgId, spaceId);
  const queue = queuesQuery.data?.queues.find((q) => q.id === queueId);

  // Keyset paging: the stack of cursors that led here, so Previous is a pop
  // rather than a second query the API has no way to express.
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors.length > 0 ? cursors[cursors.length - 1] : undefined;

  const resultsQuery = useQueueResults(orgId, spaceId, queueId, cursor, undefined, {
    placeholderData: (prev: ViewResultPage | undefined) => prev,
  });

  if (queuesQuery.error) {
    return (
      <div className="space-y-4">
        <BackLink spaceId={spaceId} />
        <div
          data-testid="queue-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(queuesQuery.error, 'This queue is unavailable right now.')}
          </p>
        </div>
      </div>
    );
  }

  if (!queue) {
    // Loaded, and this id is not among the space's queues — it was deleted, or
    // the URL names a queue from elsewhere. Content, not an error panel.
    if (queuesQuery.isSuccess) {
      return (
        <div className="space-y-4">
          <BackLink spaceId={spaceId} />
          <EmptyState
            icon={ListFilter}
            title="That queue is no longer here"
            description="It may have been deleted, or it belongs to another space. The queues this space does have are in the sidebar."
          />
        </div>
      );
    }
    return (
      <div className="space-y-4">
        <BackLink spaceId={spaceId} />
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading this queue…
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <BackLink spaceId={spaceId} />

      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1
            data-testid="queue-name"
            className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]"
          >
            {queue.name}
          </h1>
          {queue.description && (
            <p className="mt-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {queue.description}
            </p>
          )}
          <p className="mt-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            Resolved against your access, every time it is opened.
          </p>
        </div>
        {queue.can_manage && (
          <Button asChild variant="outline" data-testid="edit-queue">
            <Link to={spacePath('beacon', spaceId, `queues/${queue.id}/edit`)}>
              <Pencil className="mr-2 h-4 w-4" />
              Edit queue
            </Link>
          </Button>
        )}
      </div>

      <ViewResultList
        testId="queue-results"
        page={resultsQuery.data}
        isLoading={resultsQuery.isLoading || resultsQuery.isFetching}
        error={resultsQuery.error}
        errorFallback="The tickets in this queue are unavailable right now."
        emptyTitle="Nothing in this queue right now"
        emptyDescription="Every filter here is combined, and the rows are resolved against your own access — so a colleague opening the same queue may see tickets you cannot."
        meId={user?.id}
        canPrev={cursors.length > 0}
        onPrev={() => setCursors((prev) => prev.slice(0, -1))}
        onNext={() => {
          const next = resultsQuery.data?.next_cursor;
          if (next) setCursors((prev) => [...prev, next]);
        }}
      />
    </div>
  );
}

function BackLink({ spaceId }: { spaceId: string }) {
  return (
    <Link
      to={spacePath('beacon', spaceId, 'queues')}
      className="inline-flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
    >
      <ArrowLeft className="h-4 w-4" />
      All queues
    </Link>
  );
}
