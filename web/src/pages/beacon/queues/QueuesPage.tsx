import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { AlertCircle, ArrowDown, ArrowUp, ListFilter, Pencil, Plus, Trash2 } from 'lucide-react';
import { Button } from '../../../components/ui/button';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../../components/ui/dialog';
import { EmptyState } from '../../../shell/EmptyState';
import { spacePath } from '../../../shell/modules';
import { cn } from '../../../lib/utils';
import { useAuth } from '../../../lib/auth';
import { canMove, moveInOrder } from '../../../lib/queues/order';
import {
  friendlyErrorMessage,
  useCreateDefaultQueues,
  useDeleteQueue,
  useQueues,
  useReorderQueues,
  type Queue,
} from '../../../lib/api';

/**
 * /beacon/{spaceId}/queues — the space's queues, in order, with the management
 * surface for whoever may change them.
 *
 * # can_manage is the wire's answer, not a rule reproduced here
 *
 * The server gates every mutation on `manage_queue`, which ADR-0007 puts at the
 * agent role, and it sends the outcome back as `can_manage`. A contributor can
 * READ this page perfectly well and gets `can_manage: false`; the create,
 * reorder, edit and delete controls are then simply absent. This page never
 * inspects a role to decide that — one authority, server-side, reported once.
 *
 * # Reorder sends the whole order
 *
 * The up/down buttons do not send a swap. They rebuild the COMPLETE ordered id
 * list with one entry moved and PUT that, because the endpoint takes a
 * permutation of the space's live queues and refuses anything less with a 422
 * that changes nothing. `moveInOrder` is where that shape is guaranteed.
 */
export function QueuesPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const queuesQuery = useQueues(orgId, spaceId);
  const createDefaults = useCreateDefaultQueues(orgId, spaceId);
  const reorder = useReorderQueues(orgId, spaceId);
  const removeQueue = useDeleteQueue(orgId, spaceId);

  const [pendingDelete, setPendingDelete] = useState<Queue | null>(null);
  const [defaultsMessage, setDefaultsMessage] = useState<string | null>(null);

  const queues = queuesQuery.data?.queues ?? [];
  const canManage = queuesQuery.data?.can_manage ?? false;

  async function addDefaults() {
    setDefaultsMessage(null);
    try {
      const created = await createDefaults.mutateAsync();
      // The endpoint is idempotent, so "nothing happened" is a legitimate and
      // frequent outcome — pressing it twice must read as reassurance rather
      // than as a failure.
      setDefaultsMessage(
        created === 0
          ? 'This space already had all four. Nothing was duplicated.'
          : `Added ${created} ${created === 1 ? 'queue' : 'queues'}.`,
      );
    } catch {
      // Surfaced below through friendlyErrorMessage.
    }
  }

  function move(queue: Queue, direction: 'up' | 'down') {
    if (!canMove(queues, queue.id, direction)) return;
    reorder.mutate(moveInOrder(queues, queue.id, direction));
  }

  async function confirmDelete() {
    if (!pendingDelete) return;
    try {
      await removeQueue.mutateAsync(pendingDelete.id);
      setPendingDelete(null);
    } catch {
      // Surfaced in the dialog through friendlyErrorMessage.
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            Queues
          </h1>
          <p className="mt-1 max-w-2xl text-[var(--text-sm)] text-[var(--color-text-muted)]">
            A queue is a saved question about this space, kept in the order agents work it. Each one
            is resolved against whoever opens it — so “Assigned to me” means your own work, and your
            colleague’s means theirs.
          </p>
        </div>
        {canManage && (
          <Button asChild data-testid="new-queue">
            <Link to={spacePath('beacon', spaceId, 'queues/new')}>
              <Plus className="mr-2 h-4 w-4" />
              New queue
            </Link>
          </Button>
        )}
      </div>

      {queuesQuery.isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading this space’s queues…
        </div>
      )}

      {queuesQuery.error && (
        <ErrorPanel
          testId="queues-error"
          error={queuesQuery.error}
          fallback="This space’s queues are unavailable right now."
        />
      )}

      {reorder.error && (
        <ErrorPanel
          testId="queue-reorder-error"
          error={reorder.error}
          fallback="The queue order was not changed."
        />
      )}

      {createDefaults.error && (
        <ErrorPanel
          testId="queue-defaults-error"
          error={createDefaults.error}
          fallback="The default queues were not created."
        />
      )}

      {defaultsMessage && (
        <p data-testid="queue-defaults-message" className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {defaultsMessage}
        </p>
      )}

      {!queuesQuery.isLoading && !queuesQuery.error && queues.length === 0 && (
        <EmptyState
          icon={ListFilter}
          title="No queues in this space yet"
          description={
            canManage
              ? 'Start with the four every service desk wants — all open work, yours, unassigned, and recently resolved — then edit them into the shape this space actually works in.'
              : 'Nobody has set up queues for this space. An agent can add them from here.'
          }
          action={
            canManage ? (
              <Button
                onClick={addDefaults}
                disabled={createDefaults.isPending}
                data-testid="create-default-queues"
              >
                {createDefaults.isPending ? 'Creating…' : 'Create the four default queues'}
              </Button>
            ) : undefined
          }
        />
      )}

      {queues.length > 0 && (
        <ul
          data-testid="queues-list"
          className="overflow-hidden rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)]"
        >
          {queues.map((queue, index) => (
            <QueueRow
              key={queue.id}
              queue={queue}
              spaceId={spaceId}
              canManage={canManage}
              canMoveUp={index > 0}
              canMoveDown={index < queues.length - 1}
              reordering={reorder.isPending}
              onMove={(direction) => move(queue, direction)}
              onDelete={() => setPendingDelete(queue)}
            />
          ))}
        </ul>
      )}

      <Dialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete this queue?</DialogTitle>
            <DialogDescription>
              “{pendingDelete?.name}” will disappear from this space’s sidebar for everyone. No
              ticket is deleted — a queue holds a question, never the work itself.
            </DialogDescription>
          </DialogHeader>

          {removeQueue.error && (
            <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(removeQueue.error, 'This queue was not deleted.')}
            </p>
          )}

          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">
                Cancel
              </Button>
            </DialogClose>
            <Button
              variant="destructive"
              onClick={confirmDelete}
              disabled={removeQueue.isPending}
              data-testid="confirm-delete-queue"
            >
              {removeQueue.isPending ? 'Deleting…' : 'Delete queue'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function ErrorPanel({
  testId,
  error,
  fallback,
}: {
  testId: string;
  error: unknown;
  fallback: string;
}) {
  return (
    <div
      data-testid={testId}
      className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
    >
      <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
      <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(error, fallback)}
      </p>
    </div>
  );
}

/**
 * One row. Every management control is behind `canManage` — the list itself is
 * readable by anyone who can read the space, and that reader gets the name, the
 * description and the link, which is all a queue is for.
 */
function QueueRow({
  queue,
  spaceId,
  canManage,
  canMoveUp,
  canMoveDown,
  reordering,
  onMove,
  onDelete,
}: {
  queue: Queue;
  spaceId: string;
  canManage: boolean;
  canMoveUp: boolean;
  canMoveDown: boolean;
  reordering: boolean;
  onMove: (direction: 'up' | 'down') => void;
  onDelete: () => void;
}) {
  return (
    <li
      data-testid="queue-row"
      data-queue-id={queue.id}
      className={cn(
        'flex items-start gap-3 border-b border-[var(--color-border)] px-4 py-3 last:border-b-0',
        'transition-colors hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <div className="min-w-0 flex-1">
        <Link
          to={spacePath('beacon', spaceId, `queues/${queue.id}`)}
          className="text-[var(--text-sm)] font-medium text-[var(--color-text)] hover:underline"
        >
          {queue.name}
        </Link>
        {queue.description && (
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {queue.description}
          </p>
        )}
      </div>

      {canManage && (
        <div className="flex shrink-0 items-center gap-1">
          {/* Buttons rather than drag-and-drop: two clicks, keyboard-reachable,
              and no new dependency for an ordering that is rarely touched. */}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onMove('up')}
            disabled={!canMoveUp || reordering}
            aria-label={`Move ${queue.name} up`}
            data-testid="queue-move-up"
          >
            <ArrowUp className="h-4 w-4" />
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onMove('down')}
            disabled={!canMoveDown || reordering}
            aria-label={`Move ${queue.name} down`}
            data-testid="queue-move-down"
          >
            <ArrowDown className="h-4 w-4" />
          </Button>
          <Button asChild variant="ghost" size="sm" data-testid="edit-queue">
            <Link
              to={spacePath('beacon', spaceId, `queues/${queue.id}/edit`)}
              aria-label={`Edit ${queue.name}`}
            >
              <Pencil className="h-4 w-4" />
            </Link>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={onDelete}
            aria-label={`Delete ${queue.name}`}
            data-testid="delete-queue"
          >
            <Trash2 className="h-4 w-4 text-[var(--color-danger)]" />
          </Button>
        </div>
      )}
    </li>
  );
}
