import { useState } from 'react';
import { Pencil, Trash2 } from 'lucide-react';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useDeleteSpace,
  useSpaceContentsSummary,
  useSpaces,
  useTeams,
  useUpdateSpace,
  type Space,
  type SpaceVisibility,
} from '../../lib/api';
import { ModuleChip } from '../../shell/ModuleChip';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { Input } from '../../components/ui/input';

/**
 * SpacesAdminPage (P2.5 W8): org-wide space governance — rename,
 * description, owner team, visibility, and soft delete behind a
 * confirmation that names the space and counts what it contains.
 */
export function SpacesAdminPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const spaces = useSpaces(orgId);
  const [editing, setEditing] = useState<Space | null>(null);
  const [deleting, setDeleting] = useState<Space | null>(null);

  if (spaces.isLoading) {
    return <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading spaces…</p>;
  }
  if (spaces.error) {
    return (
      <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(spaces.error, 'The space list could not be loaded.')}
      </p>
    );
  }

  const rows = spaces.data ?? [];

  return (
    <div data-testid="admin-spaces">
      <Card>
        <CardContent className="p-0">
          <div className="grid grid-cols-[minmax(200px,2fr)_auto_1fr_1fr_auto] items-center gap-x-[var(--space-3)] border-b border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-2)] text-[var(--text-xs)] font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
            <span>Space</span>
            <span>Module</span>
            <span>Owner team</span>
            <span>Visibility</span>
            <span aria-hidden="true" />
          </div>
          {rows.length === 0 && (
            <p className="px-[var(--space-4)] py-[var(--space-6)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No spaces yet.
            </p>
          )}
          {rows.map((s) => (
            <SpaceRow key={s.id} orgId={orgId} space={s} onEdit={() => setEditing(s)} onDelete={() => setDeleting(s)} />
          ))}
        </CardContent>
      </Card>

      {editing && (
        <EditSpaceDialog orgId={orgId} space={editing} onClose={() => setEditing(null)} />
      )}
      {deleting && (
        <DeleteSpaceDialog orgId={orgId} space={deleting} onClose={() => setDeleting(null)} />
      )}
    </div>
  );
}

function SpaceRow({ orgId, space, onEdit, onDelete }: {
  orgId: string;
  space: Space;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const teams = useTeams(orgId);
  const ownerName = (teams.data ?? []).find((t) => t.id === space.owner_team_id)?.name ?? '—';

  return (
    <div
      data-testid={`admin-space-row-${space.slug}`}
      className="grid grid-cols-[minmax(200px,2fr)_auto_1fr_1fr_auto] items-center gap-x-[var(--space-3)] border-b border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-2)] last:border-b-0 hover:bg-[var(--color-surface-hover)]"
    >
      <span className="min-w-0">
        <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">{space.name}</span>
        {space.description ? (
          <span className="block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">{space.description}</span>
        ) : null}
      </span>
      <ModuleChip module={space.type} />
      <span className="truncate text-[var(--text-sm)] text-[var(--color-text-muted)]">{ownerName}</span>
      <span>
        <Badge variant={space.visibility === 'org' ? 'secondary' : 'outline'}>
          {space.visibility ?? 'discoverable'}
        </Badge>
      </span>
      <span className="flex gap-[var(--space-1)]">
        <Button variant="ghost" size="icon" aria-label={`Edit ${space.name}`} data-testid={`admin-space-edit-${space.slug}`} onClick={onEdit}>
          <Pencil className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" aria-label={`Delete ${space.name}`} data-testid={`admin-space-delete-${space.slug}`} onClick={onDelete}>
          <Trash2 className="h-4 w-4 text-[var(--color-danger)]" />
        </Button>
      </span>
    </div>
  );
}

function EditSpaceDialog({ orgId, space, onClose }: { orgId: string; space: Space; onClose: () => void }) {
  const teams = useTeams(orgId);
  const update = useUpdateSpace(orgId, space.id);
  const [name, setName] = useState(space.name);
  const [description, setDescription] = useState(space.description ?? '');
  const [ownerTeamID, setOwnerTeamID] = useState(space.owner_team_id ?? '');
  const [visibility, setVisibility] = useState<SpaceVisibility>(space.visibility ?? 'discoverable');
  const [error, setError] = useState<string | null>(null);

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="admin-space-edit-dialog">
        <DialogHeader>
          <DialogTitle>Edit {space.name}</DialogTitle>
          <DialogDescription>Name, description, owner team, and visibility.</DialogDescription>
        </DialogHeader>
        <div className="space-y-[var(--space-3)]">
          <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
            Name
            <Input value={name} onChange={(e) => setName(e.target.value)} data-testid="admin-space-name" className="mt-1" />
          </label>
          <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
            Description
            <Input value={description} onChange={(e) => setDescription(e.target.value)} data-testid="admin-space-description" className="mt-1" />
          </label>
          <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
            Owner team
            <select
              value={ownerTeamID}
              onChange={(e) => setOwnerTeamID(e.target.value)}
              data-testid="admin-space-owner-team"
              className={cn(
                'mt-1 block h-9 w-full rounded-[var(--radius-md)] border border-[var(--color-border)]',
                'bg-[var(--color-surface)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
              )}
            >
              {(teams.data ?? []).map((t) => (
                <option key={t.id} value={t.id}>{t.name}</option>
              ))}
            </select>
          </label>
          <fieldset>
            <legend className="text-[var(--text-sm)] text-[var(--color-text)]">Visibility</legend>
            <div className="mt-1 flex gap-[var(--space-2)]">
              {(['hidden', 'discoverable', 'org'] as const).map((v) => (
                <button
                  key={v}
                  type="button"
                  data-testid={`admin-space-visibility-${v}`}
                  onClick={() => setVisibility(v)}
                  className={cn(
                    'rounded-[var(--radius-md)] border px-3 py-1.5 text-[var(--text-sm)]',
                    visibility === v
                      ? 'border-[var(--color-primary)] text-[var(--color-text)]'
                      : 'border-[var(--color-border)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]',
                  )}
                >
                  {v}
                </button>
              ))}
            </div>
          </fieldset>
          {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            disabled={update.isPending || !name.trim()}
            data-testid="admin-space-save"
            onClick={() => {
              setError(null);
              // PUT semantics: echo fields the dialog does not edit (icon,
              // is_private, key) so they survive the update.
              update.mutate(
                {
                  name: name.trim(),
                  key: space.key,
                  description: description || null,
                  icon: space.icon ?? null,
                  is_private: space.is_private,
                  owner_team_id: ownerTeamID || undefined,
                  visibility,
                },
                {
                  onSuccess: onClose,
                  onError: (err) => setError(friendlyErrorMessage(err, 'The space could not be saved.')),
                },
              );
            }}
          >
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * DeleteSpaceDialog: soft delete behind a confirmation that names the space
 * and counts what it contains — nobody deletes "a space", they delete
 * "Support (14 tickets, 3 pages)".
 */
function DeleteSpaceDialog({ orgId, space, onClose }: { orgId: string; space: Space; onClose: () => void }) {
  const summary = useSpaceContentsSummary(orgId, space.id);
  const del = useDeleteSpace(orgId);
  const [error, setError] = useState<string | null>(null);

  const counts = summary.data;
  const contents = counts
    ? [
        counts.tickets > 0 ? `${counts.tickets} ticket${counts.tickets === 1 ? '' : 's'}` : null,
        counts.pages > 0 ? `${counts.pages} page${counts.pages === 1 ? '' : 's'}` : null,
        counts.items > 0 ? `${counts.items} project item${counts.items === 1 ? '' : 's'}` : null,
      ].filter(Boolean)
    : null;

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="admin-space-delete-dialog">
        <DialogHeader>
          <DialogTitle>Delete “{space.name}”?</DialogTitle>
          <DialogDescription data-testid="admin-space-delete-summary">
            {summary.isLoading && 'Counting what it contains…'}
            {!summary.isLoading && contents && contents.length > 0 && (
              <>This space contains {contents.join(', ')}. Everything becomes unavailable when the space is deleted.</>
            )}
            {!summary.isLoading && contents && contents.length === 0 && 'This space is empty.'}
            {!summary.isLoading && !contents && 'Its contents could not be counted — it may still contain work.'}
          </DialogDescription>
        </DialogHeader>
        {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            variant="destructive"
            disabled={del.isPending}
            data-testid="admin-space-delete-confirm"
            onClick={() => {
              setError(null);
              del.mutate(space.id, {
                onSuccess: onClose,
                onError: (err) => setError(friendlyErrorMessage(err, 'The space could not be deleted.')),
              });
            }}
          >
            Delete {space.name}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
