import { useState } from 'react';
import { AlertCircle, Plus, Archive, ArchiveRestore, Trash2 } from 'lucide-react';
import { useAuth } from '../../lib/auth';
import {
  useCustomFields,
  useCreateCustomField,
  useUpdateCustomField,
  useDeleteCustomField,
  friendlyErrorMessage,
  type CustomFieldDef,
  type CustomFieldType,
} from '../../lib/api';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import { Badge } from '../../components/ui/badge';
import { cn } from '../../lib/utils';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
  DialogClose,
} from '../../components/ui/dialog';

const TYPE_LABEL: Record<CustomFieldType, string> = {
  text: 'Text',
  number: 'Number',
  date: 'Date',
  single_select: 'Single select',
};

const selectClass = cn(
  'flex h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

/**
 * CustomFieldsAdminPage manages org custom field definitions. Slugs and types
 * are immutable; the name and (for single-select) options are editable. Deleting
 * a field leaves any stored values as legacy read-only data — no silent loss.
 */
export function CustomFieldsAdminPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const { data: fields, isLoading, isError, error } = useCustomFields(orgId);
  const createMut = useCreateCustomField(orgId);
  const deleteMut = useDeleteCustomField(orgId);

  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState('');
  const [type, setType] = useState<CustomFieldType>('text');
  const [optionsText, setOptionsText] = useState('');
  const [actionError, setActionError] = useState<string | null>(null);

  function submitCreate() {
    setActionError(null);
    const options = optionsText.split(',').map((o) => o.trim()).filter(Boolean);
    createMut.mutate(
      { name: name.trim(), field_type: type, options: type === 'single_select' ? options : undefined },
      {
        onSuccess: () => {
          setName('');
          setType('text');
          setOptionsText('');
          setCreateOpen(false);
        },
        onError: (e) => setActionError(friendlyErrorMessage(e, 'Something went wrong.')),
      },
    );
  }

  const sorted = (fields ?? []).slice().sort((a, b) => a.position - b.position);

  return (
    <div data-testid="custom-fields-admin-page">
      <div className="mb-[var(--space-4)] flex items-start justify-between gap-[var(--space-4)]">
        <div>
          <h2 className="text-[var(--text-md)] font-semibold text-[var(--color-text)]">Custom fields</h2>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            Fields shown on Vector items. Deleting or archiving a field keeps existing values as read-only legacy data.
          </p>
        </div>
        <Button data-testid="custom-field-create-button" onClick={() => { setActionError(null); setCreateOpen(true); }}>
          <Plus className="mr-1 h-4 w-4" /> New field
        </Button>
      </div>

      {(actionError || isError) && (
        <div className="mb-[var(--space-4)] flex items-center gap-2 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger-muted)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-danger)]">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {actionError ?? friendlyErrorMessage(error, 'Failed to load custom fields.')}
        </div>
      )}

      {isLoading ? (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
      ) : sorted.length === 0 ? (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">No custom fields yet.</p>
      ) : (
        <Card>
          <CardContent className="p-0">
            <ul>
              {sorted.map((f) => (
                <CustomFieldRow
                  key={f.id}
                  field={f}
                  orgId={orgId}
                  onError={setActionError}
                  onDelete={() =>
                    deleteMut.mutate(f.id, { onError: (e) => setActionError(friendlyErrorMessage(e, 'Something went wrong.')) })
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
            <DialogTitle>New custom field</DialogTitle>
            <DialogDescription>The field type is fixed once created. A stable identifier is derived from the name.</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="cf-name">Name</FieldLabel>
            <Input id="cf-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Story points" autoFocus />
          </Field>
          <Field>
            <FieldLabel htmlFor="cf-type">Type</FieldLabel>
            <select id="cf-type" value={type} onChange={(e) => setType(e.target.value as CustomFieldType)} className={selectClass}>
              <option value="text">Text</option>
              <option value="number">Number</option>
              <option value="date">Date</option>
              <option value="single_select">Single select</option>
            </select>
          </Field>
          {type === 'single_select' && (
            <Field>
              <FieldLabel htmlFor="cf-options">Options (comma-separated)</FieldLabel>
              <Input id="cf-options" value={optionsText} onChange={(e) => setOptionsText(e.target.value)} placeholder="gold, silver, bronze" />
            </Field>
          )}
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="secondary">Cancel</Button>
            </DialogClose>
            <Button
              data-testid="custom-field-create-submit"
              disabled={name.trim() === '' || createMut.isPending || (type === 'single_select' && optionsText.trim() === '')}
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

function CustomFieldRow({
  field,
  orgId,
  onError,
  onDelete,
}: {
  field: CustomFieldDef;
  orgId: string;
  onError: (msg: string | null) => void;
  onDelete: () => void;
}) {
  const updateMut = useUpdateCustomField(orgId);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const archived = !!field.archived_at;

  function toggleArchive() {
    onError(null);
    updateMut.mutate(
      { fieldId: field.id, req: { archived: !archived } },
      { onError: (e) => onError(friendlyErrorMessage(e, 'Something went wrong.')) },
    );
  }

  return (
    <li
      data-testid="custom-field-row"
      className="flex items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-3 last:border-b-0"
    >
      <div className="flex items-center gap-2">
        <span className={archived ? 'text-[var(--color-text-muted)]' : 'text-[var(--color-text)]'}>{field.name}</span>
        <Badge variant="secondary">{TYPE_LABEL[field.field_type]}</Badge>
        {field.field_type === 'single_select' && field.options.length > 0 && (
          <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">{field.options.join(', ')}</span>
        )}
        {archived && <Badge variant="secondary">Archived</Badge>}
      </div>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="sm" aria-label={archived ? 'Unarchive' : 'Archive'} onClick={toggleArchive}>
          {archived ? <ArchiveRestore className="h-4 w-4" /> : <Archive className="h-4 w-4" />}
        </Button>
        {confirmingDelete ? (
          <>
            <Button variant="destructive" size="sm" data-testid="custom-field-confirm-delete" onClick={onDelete}>
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
    </li>
  );
}
