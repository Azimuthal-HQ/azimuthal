import { useState } from 'react';
import { AlertCircle, Plus, Archive, ArchiveRestore, Trash2, ChevronRight, ChevronUp, ChevronDown } from 'lucide-react';
import { useAuth } from '../../lib/auth';
import {
  useCustomFields,
  useCreateCustomField,
  useUpdateCustomField,
  useDeleteCustomField,
  useSpaces,
  useFieldScopes,
  useSetFieldScope,
  useRemoveFieldScope,
  useFormFieldScopes,
  useReorderFormFields,
  friendlyErrorMessage,
  type CustomFieldDef,
  type CustomFieldType,
  type Space,
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
            Fields appear on the item and ticket forms they are attached to — expand a field to choose spaces and
            mark it required there. Deleting or archiving a field keeps existing values as read-only legacy data.
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

      <FormOrderPanel orgId={orgId} defs={fields ?? []} />

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
  const [expanded, setExpanded] = useState(false);
  const archived = !!field.archived_at;

  function toggleArchive() {
    onError(null);
    updateMut.mutate(
      { fieldId: field.id, req: { archived: !archived } },
      { onError: (e) => onError(friendlyErrorMessage(e, 'Something went wrong.')) },
    );
  }

  return (
    <li data-testid="custom-field-row" className="border-b border-[var(--color-border)] last:border-b-0">
      <div className="flex items-center justify-between gap-3 px-4 py-3">
        <div className="flex items-center gap-2">
          <button
            type="button"
            aria-expanded={expanded}
            aria-label={`Attachments for ${field.name}`}
            data-testid="custom-field-expand"
            onClick={() => setExpanded((v) => !v)}
            className="rounded p-0.5 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
          >
            <ChevronRight className={cn('h-4 w-4 transition-transform', expanded && 'rotate-90')} />
          </button>
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
      </div>
      {/* Scopes load only when opened — the list stays one request for N
          fields, and the sub-queries fire per expansion (TransitionList's
          rationale). */}
      {expanded && (
        <div className="border-t border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3">
          <FieldScopesPanel orgId={orgId} fieldId={field.id} />
        </div>
      )}
    </li>
  );
}

/**
 * FieldScopesPanel edits one field's attachments: which spaces' item or ticket
 * forms carry it, and whether it is required there. Requiredness lives on the
 * attachment — a field is never required org-wide — and marking it required
 * only governs writes from then on; entities saved before keep reading back.
 */
function FieldScopesPanel({ orgId, fieldId }: { orgId: string; fieldId: string }) {
  const spacesQuery = useSpaces(orgId);
  const scopesQuery = useFieldScopes(orgId, fieldId);
  const setScope = useSetFieldScope(orgId, fieldId);
  const removeScope = useRemoveFieldScope(orgId, fieldId);
  const [error, setError] = useState<string | null>(null);
  const [bulkPending, setBulkPending] = useState(false);
  const [bulkFailure, setBulkFailure] = useState<string | null>(null);

  if (spacesQuery.isLoading || scopesQuery.isLoading) {
    return <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">Loading attachments…</p>;
  }
  if (spacesQuery.isError || scopesQuery.isError) {
    return (
      <p data-testid="field-scopes-error" className="text-[var(--text-xs)] text-[var(--color-danger)]">
        {friendlyErrorMessage(spacesQuery.error ?? scopesQuery.error, 'Attachments could not be loaded.')}
      </p>
    );
  }

  const spaces = spacesQuery.data ?? [];
  const scopes = scopesQuery.data ?? [];
  const groups: { label: string; entityType: 'project_item' | 'ticket'; spaces: Space[] }[] = [
    { label: 'Item forms (Vector)', entityType: 'project_item', spaces: spaces.filter((s) => s.type === 'vector') },
    { label: 'Ticket forms (Beacon)', entityType: 'ticket', spaces: spaces.filter((s) => s.type === 'beacon') },
  ];
  const scopeFor = (spaceId: string, entityType: string) =>
    scopes.find((sc) => sc.space_id === spaceId && sc.entity_type === entityType);
  const onError = (e: unknown) => setError(friendlyErrorMessage(e, 'The attachment could not be saved.'));

  /**
   * Bulk attach/detach: a loop over the existing per-scope routes, targeting
   * only the spaces whose state has to change. That targeting is what makes
   * the buttons safe and convergent, not an optimisation:
   *
   * - Attach-all skips spaces already attached. Re-PUTting one would reset
   *   its required flag to false (the PUT body carries required), so the
   *   skip is what keeps a re-run from clobbering flags an admin has set.
   * - Detach-all skips spaces with no attachment. DELETE on an absent scope
   *   answers 404, so the skip is what lets a re-run after partial failure
   *   converge instead of reporting the already-done spaces as new failures.
   *
   * Partial failure is never silent: the message names each space that
   * failed, and re-running retries exactly those, because by then they are
   * the only ones still in the wrong state.
   */
  async function bulk(g: { label: string; entityType: 'project_item' | 'ticket'; spaces: Space[] }, direction: 'attach' | 'detach') {
    setError(null);
    setBulkFailure(null);
    setBulkPending(true);
    const targets = g.spaces.filter((s) =>
      direction === 'attach' ? !scopeFor(s.id, g.entityType) : !!scopeFor(s.id, g.entityType),
    );
    const results = await Promise.allSettled(
      targets.map((s) =>
        direction === 'attach'
          ? setScope.mutateAsync({ spaceId: s.id, entityType: g.entityType, required: false })
          : removeScope.mutateAsync({ spaceId: s.id, entityType: g.entityType }),
      ),
    );
    setBulkPending(false);
    const failed = targets.filter((_, i) => results[i].status === 'rejected').map((s) => s.name);
    if (failed.length > 0) {
      setBulkFailure(
        `Could not ${direction} ${failed.length} of ${targets.length} space${targets.length === 1 ? '' : 's'}: ` +
          `${failed.join(', ')}. Running it again retries only what is still ${direction === 'attach' ? 'missing' : 'attached'}.`,
      );
    }
  }

  return (
    <div data-testid="field-scopes-panel" className="space-y-3">
      {groups.map((g) => (
        <div key={g.entityType}>
          <div className="mb-1 flex items-center gap-3">
            <p className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
              {g.label}
            </p>
            {g.spaces.length > 0 && (
              <>
                <button
                  type="button"
                  data-testid={`bulk-attach-${g.entityType}`}
                  disabled={bulkPending}
                  onClick={() => void bulk(g, 'attach')}
                  className="text-[var(--text-xs)] text-[var(--color-primary)] hover:underline disabled:opacity-50"
                >
                  Attach all
                </button>
                <button
                  type="button"
                  data-testid={`bulk-detach-${g.entityType}`}
                  disabled={bulkPending}
                  onClick={() => void bulk(g, 'detach')}
                  className="text-[var(--text-xs)] text-[var(--color-text-muted)] hover:text-[var(--color-danger)] hover:underline disabled:opacity-50"
                >
                  Detach all
                </button>
              </>
            )}
          </div>
          {g.spaces.length === 0 ? (
            <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">No spaces of this kind yet.</p>
          ) : (
            <ul className="space-y-1">
              {g.spaces.map((s) => {
                const scope = scopeFor(s.id, g.entityType);
                return (
                  <li key={s.id} className="flex items-center gap-4 text-[var(--text-sm)] text-[var(--color-text)]">
                    <label className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        data-testid={`scope-attach-${s.id}-${g.entityType}`}
                        checked={!!scope}
                        onChange={(e) => {
                          setError(null);
                          if (e.target.checked) {
                            setScope.mutate({ spaceId: s.id, entityType: g.entityType, required: false }, { onError });
                          } else {
                            removeScope.mutate({ spaceId: s.id, entityType: g.entityType }, { onError });
                          }
                        }}
                      />
                      {s.name}
                    </label>
                    {scope && (
                      <label className="flex items-center gap-1.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        <input
                          type="checkbox"
                          data-testid={`scope-required-${s.id}-${g.entityType}`}
                          checked={scope.required}
                          onChange={(e) => {
                            setError(null);
                            setScope.mutate({ spaceId: s.id, entityType: g.entityType, required: e.target.checked }, { onError });
                          }}
                        />
                        Required
                      </label>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      ))}
      {bulkFailure && (
        <p data-testid="bulk-scope-failure" className="text-[var(--text-xs)] text-[var(--color-danger)]">
          {bulkFailure}
        </p>
      )}
      {error && (
        <p data-testid="field-scopes-action-error" className="text-[var(--text-xs)] text-[var(--color-danger)]">
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * FormOrderPanel edits one FORM's field order. The rows above are
 * field-centric — one field expanded into its spaces — but an order is a
 * property of one form: one (space, entity type), across fields. So this
 * panel pivots the other way: pick a form, see its attached fields in the
 * order entities render them, move them.
 *
 * A space names its form outright — Vector spaces host item forms, Beacon
 * spaces ticket forms — so the picker is a space picker and the entity type
 * follows from the module. Each move submits the WHOLE order (the route
 * takes a permutation of the form, refused if stale), and the required flag
 * is untouched by construction: reordering writes position only, the exact
 * converse of the required toggle keeping position.
 */
function FormOrderPanel({ orgId, defs }: { orgId: string; defs: CustomFieldDef[] }) {
  const spacesQuery = useSpaces(orgId);
  const [spaceId, setSpaceId] = useState('');
  const [error, setError] = useState<string | null>(null);

  const spaces = (spacesQuery.data ?? []).filter((s) => s.type === 'vector' || s.type === 'beacon');
  const selected = spaces.find((s) => s.id === spaceId);
  const entityType: 'project_item' | 'ticket' = selected?.type === 'beacon' ? 'ticket' : 'project_item';

  const scopesQuery = useFormFieldScopes(orgId, spaceId, entityType, { enabled: !!spaceId });
  const reorderMut = useReorderFormFields(orgId, spaceId, entityType);

  const nameOf = (fieldId: string) => defs.find((d) => d.id === fieldId)?.name ?? fieldId;
  const scopes = scopesQuery.data ?? [];

  function move(index: number, delta: -1 | 1) {
    setError(null);
    const ids = scopes.map((sc) => sc.field_id);
    const target = index + delta;
    [ids[index], ids[target]] = [ids[target], ids[index]];
    reorderMut.mutate(ids, {
      onError: (e) => setError(friendlyErrorMessage(e, 'The order could not be saved.')),
    });
  }

  return (
    <div data-testid="form-order-panel" className="mt-[var(--space-6)]">
      <h3 className="text-[var(--text-md)] font-semibold text-[var(--color-text)]">Form order</h3>
      <p className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
        The order fields appear in on one form. Pick a space; moving a field saves immediately and
        never changes whether it is required there.
      </p>
      <div className="max-w-md">
        <select
          aria-label="Form"
          data-testid="form-order-space"
          value={spaceId}
          onChange={(e) => {
            setError(null);
            setSpaceId(e.target.value);
          }}
          className={selectClass}
        >
          <option value="">Choose a form…</option>
          {spaces.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} — {s.type === 'beacon' ? 'ticket form' : 'item form'}
            </option>
          ))}
        </select>
      </div>
      {spaceId && (
        <div className="mt-[var(--space-3)] max-w-md">
          {scopesQuery.isLoading ? (
            <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
          ) : scopesQuery.isError ? (
            <p data-testid="form-order-error" className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(scopesQuery.error, 'The form could not be loaded.')}
            </p>
          ) : scopes.length === 0 ? (
            <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No fields are attached to this form yet — attach some above first.
            </p>
          ) : (
            <Card>
              <CardContent className="p-0">
                <ul>
                  {scopes.map((sc, i) => (
                    <li
                      key={sc.field_id}
                      data-testid="form-order-row"
                      className="flex items-center justify-between gap-3 border-b border-[var(--color-border)] px-4 py-2 last:border-b-0"
                    >
                      <span className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text)]">
                        {nameOf(sc.field_id)}
                        {sc.required && (
                          <Badge variant="secondary" data-testid="form-order-required">
                            Required
                          </Badge>
                        )}
                      </span>
                      <span className="flex items-center gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Move ${nameOf(sc.field_id)} up`}
                          data-testid={`form-order-up-${sc.field_id}`}
                          disabled={i === 0 || reorderMut.isPending}
                          onClick={() => move(i, -1)}
                        >
                          <ChevronUp className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Move ${nameOf(sc.field_id)} down`}
                          data-testid={`form-order-down-${sc.field_id}`}
                          disabled={i === scopes.length - 1 || reorderMut.isPending}
                          onClick={() => move(i, 1)}
                        >
                          <ChevronDown className="h-4 w-4" />
                        </Button>
                      </span>
                    </li>
                  ))}
                </ul>
              </CardContent>
            </Card>
          )}
          {error && (
            <p data-testid="form-order-action-error" className="mt-2 text-[var(--text-xs)] text-[var(--color-danger)]">
              {error}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
