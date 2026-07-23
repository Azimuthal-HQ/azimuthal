import { useState } from 'react';
import { AlertCircle, Plus, Archive, ArchiveRestore, Trash2, Pencil } from 'lucide-react';
import { useAuth } from '../../lib/auth';
import {
  useItemTypes,
  useCreateItemType,
  useUpdateItemType,
  useDeleteItemType,
  friendlyErrorMessage,
  type ItemType,
} from '../../lib/api';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import { Badge } from '../../components/ui/badge';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from '../../components/ui/dialog';

/**
 * ItemTypesAdminPage manages the org's Vector item types (task, story, bug,
 * epic, and any additions). Slugs are immutable; renames change the display
 * name only. A referenced type cannot be hard-deleted (the API answers 409) —
 * archive it instead.
 */
export function ItemTypesAdminPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const { data: types, isLoading, isError, error } = useItemTypes(orgId);
  const createMut = useCreateItemType(orgId);
  const deleteMut = useDeleteItemType(orgId);

  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [actionError, setActionError] = useState<string | null>(null);

  function submitCreate() {
    setActionError(null);
    createMut.mutate(
      { name: name.trim() },
      {
        onSuccess: () => {
          setName('');
          setCreateOpen(false);
        },
        onError: (e) => setActionError(friendlyErrorMessage(e, 'Something went wrong.')),
      },
    );
  }

  const sorted = (types ?? []).slice().sort((a, b) => a.position - b.position);

  return (
    <div data-testid="item-types-admin-page">
      <div className="mb-[var(--space-4)] flex items-start justify-between gap-[var(--space-4)]">
        <div>
          <h2 className="text-[var(--text-md)] font-semibold text-[var(--color-text)]">Item types</h2>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            The types available when creating Vector items. Renaming keeps the underlying identity, so existing items are unaffected.
          </p>
        </div>
        <Button data-testid="item-type-create-button" onClick={() => { setActionError(null); setCreateOpen(true); }}>
          <Plus className="mr-1 h-4 w-4" /> New type
        </Button>
      </div>

      {(actionError || isError) && (
        <div className="mb-[var(--space-4)] flex items-center gap-2 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger-muted)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-danger)]">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {actionError ?? friendlyErrorMessage(error, 'Failed to load item types.')}
        </div>
      )}

      {isLoading ? (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
      ) : sorted.length === 0 ? (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">No item types yet.</p>
      ) : (
        <Card>
          <CardContent className="p-0">
            <ul>
              {sorted.map((t) => (
                <ItemTypeRow
                  key={t.id}
                  type={t}
                  orgId={orgId}
                  onError={setActionError}
                  onDelete={() =>
                    deleteMut.mutate(t.id, {
                      onError: (e) => setActionError(friendlyErrorMessage(e, 'Something went wrong.')),
                    })
                  }
                />
              ))}
            </ul>
          </CardContent>
        </Card>
      )}

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New item type</DialogTitle>
            <DialogDescription>Give the type a name. A stable identifier is derived from it automatically.</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="new-type-name">Name</FieldLabel>
            <Input
              id="new-type-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Spike"
              autoFocus
            />
          </Field>
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="secondary">Cancel</Button>
            </DialogClose>
            <Button
              data-testid="item-type-create-submit"
              disabled={name.trim() === '' || createMut.isPending}
              onClick={submitCreate}
            >
              Create
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function ItemTypeRow({
  type,
  orgId,
  onError,
  onDelete,
}: {
  type: ItemType;
  orgId: string;
  onError: (msg: string | null) => void;
  onDelete: () => void;
}) {
  const updateMut = useUpdateItemType(orgId);
  const [editOpen, setEditOpen] = useState(false);
  const [name, setName] = useState(type.name);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const archived = !!type.archived_at;

  function submitRename() {
    onError(null);
    updateMut.mutate(
      { typeId: type.id, req: { name: name.trim() } },
      {
        onSuccess: () => setEditOpen(false),
        onError: (e) => onError(friendlyErrorMessage(e, 'Something went wrong.')),
      },
    );
  }

  function toggleArchive() {
    onError(null);
    updateMut.mutate(
      { typeId: type.id, req: { archived: !archived } },
      { onError: (e) => onError(friendlyErrorMessage(e, 'Something went wrong.')) },
    );
  }

  return (
    <li
      data-testid="item-type-row"
      className="flex items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3 last:border-b-0"
    >
      <div className="flex items-center gap-2">
        <span className={archived ? 'text-[var(--color-text-muted)]' : 'text-[var(--color-text)]'}>{type.name}</span>
        <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]" style={{ fontFamily: 'var(--font-mono)' }}>
          {type.slug}
        </span>
        {archived && <Badge variant="secondary">Archived</Badge>}
      </div>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="sm" aria-label="Rename" onClick={() => { setName(type.name); setEditOpen(true); }}>
          <Pencil className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="sm" aria-label={archived ? 'Unarchive' : 'Archive'} onClick={toggleArchive}>
          {archived ? <ArchiveRestore className="h-4 w-4" /> : <Archive className="h-4 w-4" />}
        </Button>
        {confirmingDelete ? (
          <>
            <Button variant="destructive" size="sm" data-testid="item-type-confirm-delete" onClick={onDelete}>
              Delete
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setConfirmingDelete(false)}>
              Cancel
            </Button>
          </>
        ) : (
          <Button variant="ghost" size="sm" aria-label="Delete" onClick={() => { onError(null); setConfirmingDelete(true); }}>
            <Trash2 className="h-4 w-4" />
          </Button>
        )}
      </div>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rename item type</DialogTitle>
            <DialogDescription>
              Only the display name changes. The identifier <code style={{ fontFamily: 'var(--font-mono)' }}>{type.slug}</code> stays the same, so existing items keep their type.
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor={`rename-${type.id}`}>Name</FieldLabel>
            <Input id={`rename-${type.id}`} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          </Field>
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="secondary">Cancel</Button>
            </DialogClose>
            <Button disabled={name.trim() === '' || updateMut.isPending} onClick={submitRename}>
              Save
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </li>
  );
}
