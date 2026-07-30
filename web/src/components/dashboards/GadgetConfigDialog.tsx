import { useMemo, useState } from 'react';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { FieldLabel, FieldHint } from '../ui/field';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '../ui/dialog';
import { SegmentedControl } from '../ui/segmented';
import { ViewScopeChip } from '../views/ViewChips';
import { useSavedViews, type GadgetRequest, type SavedView } from '../../lib/api';
import { vectorOnlyFieldsAllowed } from '../../lib/views/query';
import {
  BREAKDOWN_FIELDS,
  GADGET_LIMITS,
  GADGET_SPANS,
  type GadgetDefinition,
} from '../../lib/dashboards/registry';

interface GadgetConfigDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  def: GadgetDefinition;
  orgId: string;
  /** The gadget being edited, or undefined when one is being added. */
  initial?: GadgetRequest;
  onSave: (gadget: GadgetRequest) => void;
}

const FIELD_LABELS: Record<(typeof BREAKDOWN_FIELDS)[number], string> = {
  status: 'Status',
  priority: 'Priority',
  assignee: 'Assignee',
  kind: 'Type',
};

/**
 * Configures one gadget.
 *
 * WHAT IT OFFERS COMES FROM THE REGISTRY. `def.configKeys` decides which
 * controls appear, so a gadget that carries no limit shows no limit field and
 * a note shows no view picker. A form that offered every control and ignored
 * the irrelevant ones would let somebody set a value the server then refuses.
 *
 * The view picker lists `useSavedViews`, which returns exactly the views whose
 * definition reaches the caller — so a person cannot attach a view they cannot
 * open, which is the same refusal the server makes on the write.
 */
export function GadgetConfigDialog({
  open,
  onOpenChange,
  def,
  orgId,
  initial,
  onSave,
}: GadgetConfigDialogProps) {
  const viewsQuery = useSavedViews(orgId, { enabled: open && def.requiresSavedView });
  const [edited, setEdited] = useState<GadgetRequest | null>(null);

  const base: GadgetRequest = useMemo(
    () =>
      initial ?? {
        gadget_key: def.key,
        col_span: def.defaultSpan,
        saved_view_id: null,
        config: {},
      },
    [initial, def],
  );
  const form = edited ?? base;
  const config = form.config ?? {};

  function update(patch: Partial<GadgetRequest>) {
    setEdited({ ...form, ...patch });
  }
  function updateConfig(patch: Partial<NonNullable<GadgetRequest['config']>>) {
    setEdited({ ...form, config: { ...config, ...patch } });
  }

  const selectedView: SavedView | undefined = (viewsQuery.data ?? []).find(
    (v) => v.id === form.saved_view_id,
  );

  // The Vector-only rule, answered by the ONE function that answers it in the
  // frontend. A breakdown by type over a view that includes Beacon can never
  // be right — tickets have no type column — so the option is disabled with
  // the reason rather than offered and then refused by the server.
  const typeAllowed = selectedView ? vectorOnlyFieldsAllowed(selectedView.query.filter.modules) : true;

  const needsView = def.requiresSavedView && !form.saved_view_id;
  const needsField = def.configKeys.includes('group_by') && !config.group_by;
  const blocked =
    needsView || needsField || (config.group_by === 'kind' && !typeAllowed);

  function save() {
    if (blocked) return;
    onSave(form);
    setEdited(null);
    onOpenChange(false);
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setEdited(null);
        onOpenChange(next);
      }}
    >
      <DialogContent data-testid="gadget-config">
        <DialogHeader>
          <DialogTitle>{initial ? 'Configure gadget' : `Add ${def.name}`}</DialogTitle>
          <DialogDescription>{def.description}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          {def.requiresSavedView && (
            <div>
              <FieldLabel htmlFor="gadget-view">Saved view</FieldLabel>
              <FieldHint>
                The gadget shows this view resolved against each reader&apos;s own access.
              </FieldHint>
              <select
                id="gadget-view"
                data-testid="gadget-view-select"
                value={form.saved_view_id ?? ''}
                onChange={(e) => update({ saved_view_id: e.target.value || null })}
                className="mt-1 h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]"
              >
                <option value="">Choose a view…</option>
                {(viewsQuery.data ?? []).map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.name}
                  </option>
                ))}
              </select>
              {selectedView && !selectedView.is_valid && (
                <div className="mt-2">
                  <ViewScopeChip />
                </div>
              )}
              {viewsQuery.data?.length === 0 && (
                <p className="mt-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  You have no saved views yet. Save one from any list first.
                </p>
              )}
            </div>
          )}

          {def.configKeys.includes('group_by') && (
            <div>
              <FieldLabel htmlFor="gadget-group-by">Group by</FieldLabel>
              <select
                id="gadget-group-by"
                data-testid="gadget-group-by"
                value={config.group_by ?? ''}
                onChange={(e) => updateConfig({ group_by: e.target.value })}
                className="mt-1 h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]"
              >
                <option value="">Choose a field…</option>
                {BREAKDOWN_FIELDS.map((f) => (
                  <option key={f} value={f} disabled={f === 'kind' && !typeAllowed}>
                    {FIELD_LABELS[f]}
                  </option>
                ))}
              </select>
              {!typeAllowed && (
                <FieldHint>
                  Type is a Vector field. A view that also reads Beacon cannot be grouped by it —
                  tickets have no type.
                </FieldHint>
              )}
            </div>
          )}

          {def.configKeys.includes('limit') && (
            <div>
              <FieldLabel htmlFor="gadget-limit">Rows to show</FieldLabel>
              <Input
                id="gadget-limit"
                data-testid="gadget-limit"
                type="number"
                min={GADGET_LIMITS.minLimit}
                max={GADGET_LIMITS.maxLimit}
                value={config.limit ?? GADGET_LIMITS.defaultLimit}
                onChange={(e) => updateConfig({ limit: Number(e.target.value) })}
                className="w-24"
              />
            </div>
          )}

          {def.configKeys.includes('body') && (
            <div>
              <FieldLabel htmlFor="gadget-body">Note</FieldLabel>
              <FieldHint>Markdown. Headings, lists and links work; raw HTML does not.</FieldHint>
              <textarea
                id="gadget-body"
                data-testid="gadget-body"
                rows={6}
                maxLength={GADGET_LIMITS.maxNote}
                value={config.body ?? ''}
                onChange={(e) => updateConfig({ body: e.target.value })}
                className="mt-1 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] p-3 text-[var(--text-sm)] text-[var(--color-text)]"
              />
            </div>
          )}

          <div>
            <FieldLabel id="gadget-span-label">Width</FieldLabel>
            <SegmentedControl
              testId="gadget-span"
              aria-label="Gadget width"
              value={String(form.col_span ?? def.defaultSpan)}
              onChange={(v) => update({ col_span: Number(v) })}
              options={GADGET_SPANS.map((n) => ({
                value: String(n),
                label: n === 4 ? 'Full width' : `${n} column${n === 1 ? '' : 's'}`,
              }))}
            />
          </div>

          <div>
            <FieldLabel htmlFor="gadget-title" optional>
              Title
            </FieldLabel>
            <FieldHint>
              Leave it empty to use the view&apos;s name, so renaming the view renames the gadget.
            </FieldHint>
            <Input
              id="gadget-title"
              data-testid="gadget-title-input"
              maxLength={GADGET_LIMITS.maxTitle}
              value={config.title ?? ''}
              onChange={(e) => updateConfig({ title: e.target.value })}
              placeholder={selectedView?.name ?? def.name}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button data-testid="gadget-config-save" onClick={save} disabled={blocked}>
            {initial ? 'Save gadget' : 'Add gadget'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
