import { useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { AlertCircle, ArrowLeft, ShieldOff } from 'lucide-react';
import { Button } from '../../../components/ui/button';
import { Input } from '../../../components/ui/input';
import { Field, FieldLabel } from '../../../components/ui/field';
import { EmptyState } from '../../../shell/EmptyState';
import { spacePath } from '../../../shell/modules';
import { QueryFilterBuilder } from '../../../components/views/QueryFilterBuilder';
import { cn } from '../../../lib/utils';
import { useAuth } from '../../../lib/auth';
import {
  friendlyErrorMessage,
  useCreateQueue,
  useQueues,
  useSpace,
  useUpdateQueue,
  type Queue,
  type QueueRequest,
} from '../../../lib/api';
import {
  QUERY_LIMITS,
  emptyQueryDoc,
  validateQueryDoc,
  type QueryDoc,
} from '../../../lib/views/query';

/**
 * /beacon/{spaceId}/queues/new and …/queues/{queueID}/edit.
 *
 * The filter half is `QueryFilterBuilder` — the same component the saved-view
 * builder uses, because a queue's `query` is the same `QueryDoc`. This page
 * owns only what a queue has besides its query: its name and its description.
 * A second filter builder for queues would be the drift `shared-surfaces.md`
 * exists to prevent.
 *
 * Two queue-specific facts show through:
 *
 *  - The document is BOUND to this space. The server's `scopeToSpace` rewrites
 *    `space_ids` to the queue's own space on every write, so the builder is
 *    told which space that is (`boundSpaceLabel`) and states the binding where
 *    the space picker would otherwise be.
 *  - Position is not editable here. It is moved by the reorder endpoint alone,
 *    from the queue list; a single-queue update that also moved it is how an
 *    ordering ends up with two rows claiming one slot.
 */
export function QueueBuilderPage() {
  const { spaceId = '', queueId } = useParams<{ spaceId: string; queueId: string }>();
  const isEdit = Boolean(queueId);
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const queuesQuery = useQueues(orgId, spaceId);
  const spaceQuery = useSpace(spaceId);
  const createQueue = useCreateQueue(orgId, spaceId);
  const updateQueue = useUpdateQueue(orgId, spaceId);

  const canManage = queuesQuery.data?.can_manage ?? false;
  const existing: Queue | undefined = isEdit
    ? queuesQuery.data?.queues.find((q) => q.id === queueId)
    : undefined;

  const initial = useMemo<QueueForm | null>(() => {
    if (isEdit) {
      return existing
        ? { name: existing.name, description: existing.description, query: existing.query }
        : null;
    }
    // Beacon only: a queue lives in a Beacon space and its one space is forced
    // server-side, so a Vector selection would produce a queue that can never
    // match. The module control stays available — the server permits it — but
    // this is the sensible place to start.
    return { name: '', description: '', query: emptyQueryDoc(['beacon']) };
  }, [isEdit, existing]);

  // Held only once something has been edited, so the loaded queue seeds the
  // form without an effect that would fight the user's own typing.
  const [edited, setEdited] = useState<QueueForm | null>(null);
  const form = edited ?? initial;

  function update(patch: Partial<QueueForm>) {
    if (!form) return;
    setEdited({ ...form, ...patch });
  }

  const trimmedName = form?.name.trim() ?? '';
  const queryProblem = form ? validateQueryDoc(form.query) : null;
  const saveProblem = !trimmedName ? 'Give this queue a name.' : queryProblem;
  const saveError = isEdit ? updateQueue.error : createQueue.error;
  const saving = createQueue.isPending || updateQueue.isPending;

  async function save() {
    if (!form || saveProblem) return;
    const req: QueueRequest = {
      name: trimmedName,
      description: form.description.trim(),
      query: form.query,
    };
    try {
      if (isEdit && queueId) {
        await updateQueue.mutateAsync({ queueId, req });
        navigate(spacePath('beacon', spaceId, `queues/${queueId}`));
      } else {
        const created = await createQueue.mutateAsync(req);
        navigate(spacePath('beacon', spaceId, `queues/${created.id}`));
      }
    } catch {
      // Surfaced below through friendlyErrorMessage.
    }
  }

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
            {friendlyErrorMessage(queuesQuery.error, 'This space’s queues are unavailable right now.')}
          </p>
        </div>
      </div>
    );
  }

  // A reader who cannot manage queues reached this URL directly. The server
  // would refuse the save with a 403; saying so before they fill the form in is
  // the same answer, arriving earlier. It is not an error state.
  if (queuesQuery.isSuccess && !canManage) {
    return (
      <div className="space-y-4">
        <BackLink spaceId={spaceId} />
        <EmptyState
          icon={ShieldOff}
          title="Queues here are managed by agents"
          description="You can open and work every queue in this space. Changing which queues exist, and the order they sit in, needs the agent role on this space."
        />
      </div>
    );
  }

  if (!form) {
    return (
      <div className="space-y-4">
        <BackLink spaceId={spaceId} />
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {queuesQuery.isSuccess ? 'That queue is no longer here.' : 'Loading this queue…'}
        </div>
      </div>
    );
  }

  const spaceLabel = spaceQuery.data
    ? spaceQuery.data.key
      ? `${spaceQuery.data.key} · ${spaceQuery.data.name}`
      : spaceQuery.data.name
    : 'this space';

  return (
    <div className="space-y-5">
      <BackLink spaceId={spaceId} />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
          {isEdit ? 'Edit queue' : 'New queue'}
        </h1>
        <div className="flex items-center gap-2">
          <Button asChild variant="outline">
            <Link
              to={spacePath(
                'beacon',
                spaceId,
                isEdit && queueId ? `queues/${queueId}` : 'queues',
              )}
            >
              Cancel
            </Link>
          </Button>
          <Button onClick={save} disabled={saving || Boolean(saveProblem)} data-testid="save-queue">
            {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Create queue'}
          </Button>
        </div>
      </div>

      <section className="space-y-4 rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
        <Field>
          <FieldLabel htmlFor="queue-name">Name</FieldLabel>
          <Input
            id="queue-name"
            data-testid="queue-name-input"
            maxLength={QUERY_LIMITS.name}
            placeholder="e.g. Waiting on customer"
            value={form.name}
            onChange={(e) => update({ name: e.target.value })}
          />
        </Field>

        <Field>
          <FieldLabel htmlFor="queue-description" optional>
            Description
          </FieldLabel>
          <textarea
            id="queue-description"
            data-testid="queue-description-input"
            rows={2}
            maxLength={QUERY_LIMITS.description}
            placeholder="What this queue is for, in a sentence"
            value={form.description}
            onChange={(e) => update({ description: e.target.value })}
            className={cn(
              'flex w-full resize-y rounded-[var(--radius-lg)] border border-[var(--color-border)]',
              'bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
              'placeholder:text-[var(--color-text-muted)] transition-colors',
              'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
            )}
          />
        </Field>
      </section>

      <section className="rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
        <QueryFilterBuilder
          orgId={orgId}
          value={form.query}
          onChange={(query) => update({ query })}
          lockedModuleLabel="Beacon tickets"
            boundSpaceLabel={spaceLabel}
        />
      </section>

      {saveProblem && (
        <p data-testid="queue-save-problem" className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {saveProblem}
        </p>
      )}

      {saveError && (
        <div
          data-testid="queue-save-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(
              saveError,
              isEdit ? 'The changes to this queue were not saved.' : 'This queue was not saved.',
            )}
          </p>
        </div>
      )}
    </div>
  );
}

interface QueueForm {
  name: string;
  description: string;
  query: QueryDoc;
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
