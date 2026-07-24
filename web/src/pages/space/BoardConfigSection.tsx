import { useEffect, useMemo, useState } from 'react';
import { ArrowDown, ArrowUp, Plus, RotateCcw, Trash2 } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogClose,
} from '../../components/ui/dialog';
import { cn } from '../../lib/utils';
import {
  friendlyErrorMessage,
  useBoardConfig,
  useDeleteBoardColumn,
  useResetBoardConfig,
  useSaveBoardConfig,
  useWorkflowStates,
  type BoardColumn,
} from '../../lib/api';

// Draft shape while editing. Columns keep their server id where they have one
// so a save preserves identity rather than recreating every column.
interface DraftColumn {
  id?: string;
  name: string;
  wipLimit: string;
  statuses: string[];
}

const selectClass = cn(
  'h-8 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

function toDraft(columns: BoardColumn[]): DraftColumn[] {
  return columns.map((c) => ({
    id: c.id,
    name: c.name,
    wipLimit: c.wip_limit === null ? '' : String(c.wip_limit),
    statuses: [...c.statuses],
  }));
}

/**
 * Per-space board configuration: columns renamed, reordered, added and
 * removed, each mapping one or more statuses, with an optional soft WIP limit.
 *
 * Removing a column requires re-homing its statuses — the dialog asks where
 * they go. That is not a nicety: every status must remain mapped, or its items
 * would have no column to appear in.
 */
export function BoardConfigSection({ spaceId }: { spaceId: string }) {
  const configQuery = useBoardConfig(spaceId);
  const { data: workflowStates } = useWorkflowStates(spaceId);
  const saveMutation = useSaveBoardConfig(spaceId);
  const resetMutation = useResetBoardConfig(spaceId);
  const deleteMutation = useDeleteBoardColumn(spaceId);

  const [draft, setDraft] = useState<DraftColumn[]>([]);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [removing, setRemoving] = useState<DraftColumn | null>(null);
  const [remapTo, setRemapTo] = useState('');

  // Seed the draft from the server, but never stomp unsaved edits.
  useEffect(() => {
    if (configQuery.data && !dirty) {
      setDraft(toDraft(configQuery.data.columns));
    }
  }, [configQuery.data, dirty]);

  const forbidden = configQuery.error?.status === 403;

  // Every status the space knows about, so the editor can show which are
  // mapped and which a new column may claim.
  const vocabulary = useMemo(() => {
    const fromWorkflow = (workflowStates ?? []).map((s) => s.name);
    if (fromWorkflow.length > 0) return fromWorkflow;
    return (configQuery.data?.columns ?? []).flatMap((c) => c.statuses);
  }, [workflowStates, configQuery.data]);

  const mapped = useMemo(() => new Set(draft.flatMap((c) => c.statuses)), [draft]);
  const unmapped = vocabulary.filter((s) => !mapped.has(s));

  function edit(fn: (next: DraftColumn[]) => DraftColumn[]) {
    setDraft((prev) => fn([...prev]));
    setDirty(true);
    setError(null);
  }

  function move(index: number, delta: number) {
    const target = index + delta;
    if (target < 0 || target >= draft.length) return;
    edit((next) => {
      [next[index], next[target]] = [next[target], next[index]];
      return next;
    });
  }

  function addColumn() {
    edit((next) => [...next, { name: `Column ${next.length + 1}`, wipLimit: '', statuses: [] }]);
  }

  // Moving a status is a move, not a copy: it leaves whichever column held it.
  function assignStatus(status: string, columnIndex: number) {
    edit((next) =>
      next.map((c, i) => ({
        ...c,
        statuses: i === columnIndex
          ? Array.from(new Set([...c.statuses, status]))
          : c.statuses.filter((s) => s !== status),
      })),
    );
  }

  function unassignStatus(status: string, columnIndex: number) {
    edit((next) =>
      next.map((c, i) => (i === columnIndex ? { ...c, statuses: c.statuses.filter((s) => s !== status) } : c)),
    );
  }

  async function handleSave() {
    setError(null);
    try {
      await saveMutation.mutateAsync({
        columns: draft.map((c) => ({
          id: c.id,
          name: c.name.trim(),
          wip_limit: c.wipLimit.trim() === '' ? null : Number(c.wipLimit),
          statuses: c.statuses,
        })),
      });
      setDirty(false);
    } catch (e) {
      setError(friendlyErrorMessage(e, 'The board configuration could not be saved.'));
    }
  }

  async function handleReset() {
    setError(null);
    try {
      const cfg = await resetMutation.mutateAsync();
      setDraft(toDraft(cfg.columns));
      setDirty(false);
    } catch (e) {
      setError(friendlyErrorMessage(e, 'The board configuration could not be reset.'));
    }
  }

  function openRemove(column: DraftColumn) {
    setRemoving(column);
    setRemapTo(draft.find((c) => c !== column)?.id ?? '');
    setError(null);
  }

  async function handleRemove() {
    if (!removing) return;
    setError(null);

    // A column that was never saved has no statuses on the server; drop it
    // from the draft locally rather than calling an endpoint about a row that
    // does not exist.
    if (!removing.id) {
      edit((next) => next.filter((c) => c !== removing));
      setRemoving(null);
      return;
    }

    try {
      // A space still on the derived default has no stored columns either —
      // its ids are computed, so DELETE would 404. Removing one there means
      // materialising the layout: save what is on screen, minus this column,
      // with its statuses folded into the target.
      if (!configQuery.data?.customized) {
        const next = draft
          .filter((c) => c !== removing)
          .map((c) => (c.id === remapTo
            ? { ...c, statuses: Array.from(new Set([...c.statuses, ...removing.statuses])) }
            : c));
        const cfg = await saveMutation.mutateAsync({
          columns: next.map((c) => ({
            id: c.id,
            name: c.name.trim(),
            wip_limit: c.wipLimit.trim() === '' ? null : Number(c.wipLimit),
            statuses: c.statuses,
          })),
        });
        setDraft(toDraft(cfg.columns));
        setDirty(false);
        setRemoving(null);
        return;
      }

      const cfg = await deleteMutation.mutateAsync({ columnId: removing.id, remapTo });
      setDraft(toDraft(cfg.columns));
      setDirty(false);
      setRemoving(null);
    } catch (e) {
      setError(friendlyErrorMessage(e, 'The column could not be removed.'));
    }
  }

  const removalTargets = draft.filter((c) => c.id && c !== removing);
  const canRemove = draft.length > 1;

  return (
    <Card data-testid="board-config-section">
      <CardHeader>
        <CardTitle>Board columns</CardTitle>
      </CardHeader>
      <CardContent>
        {forbidden ? (
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            You need space admin to change the board layout.
          </p>
        ) : configQuery.isLoading ? (
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
        ) : (
          <>
            <p className="mb-3 text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Columns appear on the board in this order. Every status must belong to a
              column — a status with no column would leave its items nowhere to appear.
              {configQuery.data && !configQuery.data.customized && (
                <> This space uses the default layout.</>
              )}
            </p>

            <div className="space-y-2">
              {draft.map((column, index) => (
                <div
                  key={column.id ?? `new-${index}`}
                  data-testid="board-config-column"
                  className="rounded-[var(--radius-lg)] border border-[var(--color-border)] p-3"
                >
                  <div className="flex flex-wrap items-end gap-2">
                    <Field className="min-w-[180px] flex-1">
                      <FieldLabel htmlFor={`col-name-${index}`}>Name</FieldLabel>
                      <Input
                        id={`col-name-${index}`}
                        aria-label={`Column ${index + 1} name`}
                        value={column.name}
                        onChange={(e) => edit((next) => {
                          next[index] = { ...next[index], name: e.target.value };
                          return next;
                        })}
                      />
                    </Field>
                    <Field className="w-28">
                      <FieldLabel htmlFor={`col-wip-${index}`} optional>WIP limit</FieldLabel>
                      <Input
                        id={`col-wip-${index}`}
                        aria-label={`WIP limit for ${column.name}`}
                        type="number"
                        min={1}
                        placeholder="None"
                        value={column.wipLimit}
                        onChange={(e) => edit((next) => {
                          next[index] = { ...next[index], wipLimit: e.target.value };
                          return next;
                        })}
                      />
                    </Field>
                    <div className="flex items-center gap-1 pb-1">
                      <Button
                        size="sm" variant="outline"
                        aria-label={`Move ${column.name} earlier`}
                        disabled={index === 0}
                        onClick={() => move(index, -1)}
                      >
                        <ArrowUp className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        size="sm" variant="outline"
                        aria-label={`Move ${column.name} later`}
                        disabled={index === draft.length - 1}
                        onClick={() => move(index, 1)}
                      >
                        <ArrowDown className="h-3.5 w-3.5" />
                      </Button>
                      <Button
                        size="sm" variant="outline"
                        aria-label={`Remove ${column.name}`}
                        disabled={!canRemove}
                        onClick={() => openRemove(column)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                  </div>

                  <div className="mt-2 flex flex-wrap items-center gap-1.5">
                    <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">Statuses:</span>
                    {column.statuses.length === 0 && (
                      <span className="text-[var(--text-xs)] italic text-[var(--color-text-muted)]">none</span>
                    )}
                    {column.statuses.map((status) => (
                      <button
                        key={status}
                        type="button"
                        aria-label={`Remove ${status} from ${column.name}`}
                        onClick={() => unassignStatus(status, index)}
                        className="rounded-full border border-[var(--color-border)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-text)] hover:border-[var(--color-danger)] hover:text-[var(--color-danger)]"
                      >
                        {status} ×
                      </button>
                    ))}
                    <select
                      aria-label={`Add a status to ${column.name}`}
                      value=""
                      onChange={(e) => { if (e.target.value) assignStatus(e.target.value, index); }}
                      className={selectClass}
                    >
                      <option value="">Add status…</option>
                      {vocabulary
                        .filter((s) => !column.statuses.includes(s))
                        .map((s) => <option key={s} value={s}>{s}</option>)}
                    </select>
                  </div>
                </div>
              ))}
            </div>

            {unmapped.length > 0 && (
              <p
                data-testid="board-config-unmapped"
                className="mt-3 text-[var(--text-sm)] text-[var(--color-warning)]"
              >
                Unmapped: {unmapped.join(', ')}. Assign {unmapped.length === 1 ? 'it' : 'them'} to a
                column before saving.
              </p>
            )}

            {error && (
              <p className="mt-3 text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>
            )}

            <div className="mt-4 flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" onClick={addColumn}>
                <Plus className="mr-1.5 h-3.5 w-3.5" />Add column
              </Button>
              <Button
                size="sm"
                data-testid="board-config-save"
                onClick={handleSave}
                disabled={saveMutation.isPending || unmapped.length > 0 || !dirty}
              >
                {saveMutation.isPending ? 'Saving…' : 'Save layout'}
              </Button>
              <Button
                size="sm" variant="outline"
                onClick={handleReset}
                disabled={resetMutation.isPending}
              >
                <RotateCcw className="mr-1.5 h-3.5 w-3.5" />
                {resetMutation.isPending ? 'Resetting…' : 'Reset to default'}
              </Button>
            </div>
          </>
        )}
      </CardContent>

      <Dialog open={removing !== null} onOpenChange={(o) => { if (!o) setRemoving(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Remove {removing?.name}</DialogTitle>
            <DialogDescription>
              {removing?.statuses.length
                ? 'This column’s statuses need a new home. Items in them will appear in the column you choose.'
                : 'This column has no statuses mapped to it.'}
            </DialogDescription>
          </DialogHeader>
          {removing?.id && removing.statuses.length > 0 && (
            <div className="py-2">
              <Field>
                <FieldLabel htmlFor="remap-target">Move its statuses to</FieldLabel>
                <select
                  id="remap-target"
                  aria-label="Re-mapping target"
                  value={remapTo}
                  onChange={(e) => setRemapTo(e.target.value)}
                  className={cn(selectClass, 'h-9 w-full')}
                >
                  {removalTargets.map((c) => (
                    <option key={c.id} value={c.id}>{c.name}</option>
                  ))}
                </select>
              </Field>
            </div>
          )}
          <DialogFooter>
            <DialogClose asChild><Button variant="outline">Cancel</Button></DialogClose>
            <Button
              data-testid="board-config-confirm-remove"
              onClick={handleRemove}
              disabled={deleteMutation.isPending || (!!removing?.id && !remapTo)}
            >
              {deleteMutation.isPending ? 'Removing…' : 'Remove column'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
