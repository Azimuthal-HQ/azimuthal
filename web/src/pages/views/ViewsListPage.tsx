import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { AlertCircle, Bookmark, Pencil, Plus, Trash2 } from 'lucide-react';
import { Button } from '../../components/ui/button';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { EmptyState } from '../../shell/EmptyState';
import {
  ViewOwnerChip,
  ViewScopeChip,
  ViewVisibilityChip,
  scopeUnavailableReason,
} from '../../components/views/ViewChips';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import { friendlyErrorMessage, useDeleteView, useSavedViews, type SavedView } from '../../lib/api';

/**
 * /views — every saved view whose definition reaches you (P4, ADR-0009).
 *
 * Your own views and views shared with you are ONE list. They are the same
 * kind of object and they open the same way; splitting them into two sections
 * would imply that a shared view is a lesser thing you visit rather than one
 * you use. Provenance is carried by a chip instead, and edit and delete are
 * offered only where `is_owner` says they will work — the server decides
 * regardless, this is presentation.
 */
export function ViewsListPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const navigate = useNavigate();

  const viewsQuery = useSavedViews(orgId);
  const deleteView = useDeleteView(orgId);
  const [pendingDelete, setPendingDelete] = useState<SavedView | null>(null);

  const views = viewsQuery.data ?? [];

  async function confirmDelete() {
    if (!pendingDelete) return;
    try {
      await deleteView.mutateAsync(pendingDelete.id);
      setPendingDelete(null);
    } catch {
      // Surfaced in the dialog through friendlyErrorMessage.
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            Views
          </h1>
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            A saved view stores a question, not an answer. It is resolved against your access every
            time it is opened, so two people can share one view and each see their own work.
          </p>
        </div>
        <Button onClick={() => navigate('/views/new')} data-testid="new-view">
          <Plus className="mr-2 h-4 w-4" />
          New view
        </Button>
      </div>

      {viewsQuery.isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading your views…
        </div>
      )}

      {viewsQuery.error && (
        <div
          data-testid="views-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(viewsQuery.error, 'Your saved views are unavailable right now.')}
          </p>
        </div>
      )}

      {!viewsQuery.isLoading && !viewsQuery.error && views.length === 0 && (
        <EmptyState
          icon={Bookmark}
          title="No saved views yet"
          description="A view collects work from every space you can read — across Beacon and Vector at once. Build one here, or save the filters you already have from a ticket list or a backlog."
          action={
            <Button onClick={() => navigate('/views/new')} data-testid="new-view-empty">
              <Plus className="mr-2 h-4 w-4" />
              New view
            </Button>
          }
        />
      )}

      {views.length > 0 && (
        <ul
          data-testid="views-list"
          className="overflow-hidden rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)]"
        >
          {views.map((view) => (
            <ViewRow key={view.id} view={view} onDelete={() => setPendingDelete(view)} />
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
            <DialogTitle>Delete this view?</DialogTitle>
            <DialogDescription>
              “{pendingDelete?.name}” will be removed for everyone it is shared with. Nothing it
              matched is deleted — a view holds a question, never the work itself.
            </DialogDescription>
          </DialogHeader>

          {deleteView.error && (
            <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(deleteView.error, 'This view was not deleted.')}
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
              disabled={deleteView.isPending}
              data-testid="confirm-delete-view"
            >
              {deleteView.isPending ? 'Deleting…' : 'Delete view'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/**
 * One row. An invalid view renders as a full row with a neutral chip and its
 * reason — not greyed out, not an error, and never hidden. Hiding it would
 * leave its owner with a view they cannot find in order to fix.
 */
function ViewRow({ view, onDelete }: { view: SavedView; onDelete: () => void }) {
  return (
    <li
      data-testid="view-row"
      data-view-id={view.id}
      data-valid={view.is_valid}
      className={cn(
        'flex items-start gap-3 border-b border-[var(--color-border)] px-4 py-3 last:border-b-0',
        'transition-colors hover:bg-[var(--color-surface-hover)]',
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <Link
            to={`/views/${view.id}`}
            className="text-[var(--text-sm)] font-medium text-[var(--color-text)] hover:underline"
          >
            {view.name}
          </Link>
          <ViewOwnerChip view={view} />
          <ViewVisibilityChip visibility={view.visibility} teamName={view.team_name} />
          {!view.is_valid && <ViewScopeChip />}
        </div>

        {view.description && (
          <p className="mt-1 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {view.description}
          </p>
        )}

        {!view.is_valid && (
          <p
            data-testid="view-invalid-reason"
            className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]"
          >
            {scopeUnavailableReason(view)}{' '}
            {view.is_owner ? (
              <Link to={`/views/${view.id}/edit`} className="text-[var(--color-primary)] hover:underline">
                Re-scope it
              </Link>
            ) : (
              'Its owner can re-scope it.'
            )}
          </p>
        )}
      </div>

      {view.is_owner && (
        <div className="flex shrink-0 items-center gap-1">
          <Button asChild variant="ghost" size="sm" data-testid="edit-view">
            <Link to={`/views/${view.id}/edit`} aria-label={`Edit ${view.name}`}>
              <Pencil className="h-4 w-4" />
            </Link>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={onDelete}
            aria-label={`Delete ${view.name}`}
            data-testid="delete-view"
          >
            <Trash2 className="h-4 w-4 text-[var(--color-danger)]" />
          </Button>
        </div>
      )}
    </li>
  );
}
