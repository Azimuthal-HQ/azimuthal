import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { Plus, Play, CheckCircle, Clock, AlertCircle } from 'lucide-react';
import { Badge, type BadgeProps } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Field, FieldLabel } from '../../components/ui/field';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogClose,
} from '../../components/ui/dialog';
import { formatUTCDate } from '../../lib/utils';
import {
  useSprints, useActiveSprint, useCreateSprint, useStartSprint, useCompleteSprint,
  friendlyErrorMessage,
  type Sprint,
} from '../../lib/api';

// One status vocabulary: the shared tinted Badge, like every other status
// surface. Playwright's `text=planned` matching is case-insensitive, so the
// display labels stay compatible with the sprint status regression test.
const STATUS_VARIANT: Record<string, BadgeProps['variant']> = {
  planned: 'secondary',
  active: 'default',
  completed: 'success',
};
const STATUS_LABEL: Record<string, string> = {
  planned: 'Planned',
  active: 'Active',
  completed: 'Completed',
};

function SprintRow({ sprint, spaceId, isActive }: { sprint: Sprint; spaceId: string; isActive: boolean }) {
  const startMutation = useStartSprint(spaceId);
  const completeMutation = useCompleteSprint(spaceId);
  const [startError, setStartError] = useState<string | null>(null);

  async function handleStart() {
    setStartError(null);
    try { await startMutation.mutateAsync(sprint.id); }
    catch (e) { setStartError(friendlyErrorMessage(e, 'The sprint could not be started.')); }
  }

  async function handleComplete() {
    await completeMutation.mutateAsync(sprint.id);
  }

  return (
    <div className="flex items-center gap-4 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--color-text)] truncate">{sprint.name}</span>
          <Badge variant={STATUS_VARIANT[sprint.status] ?? 'secondary'}>
            {STATUS_LABEL[sprint.status] ?? sprint.status}
          </Badge>
          {isActive && sprint.status !== 'active' && (
            // Server truth can disagree with the list row's status; surface
            // it rather than showing two identical pills in the common case.
            <Badge variant="default">Active</Badge>
          )}
        </div>
        {sprint.goal && (
          <p className="mt-0.5 text-[var(--text-sm)] text-[var(--color-text-muted)] truncate">{sprint.goal}</p>
        )}
        {(sprint.starts_at || sprint.ends_at) && (
          <p className="mt-0.5 flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            <Clock className="h-3 w-3" />
            {sprint.starts_at ? formatUTCDate(sprint.starts_at) : '—'}
            {' → '}
            {sprint.ends_at ? formatUTCDate(sprint.ends_at) : '—'}
          </p>
        )}
        {startError && (
          <p className="mt-1 flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-danger)]">
            <AlertCircle className="h-3 w-3" />{startError}
          </p>
        )}
      </div>

      <div className="flex items-center gap-2 shrink-0">
        {sprint.status === 'planned' && (
          <Button size="sm" variant="outline" onClick={handleStart} disabled={startMutation.isPending}>
            <Play className="mr-1.5 h-3.5 w-3.5" />
            {startMutation.isPending ? 'Starting…' : 'Start'}
          </Button>
        )}
        {sprint.status === 'active' && (
          <Button size="sm" variant="outline" onClick={handleComplete} disabled={completeMutation.isPending}>
            <CheckCircle className="mr-1.5 h-3.5 w-3.5" />
            {completeMutation.isPending ? 'Completing…' : 'Complete'}
          </Button>
        )}
      </div>
    </div>
  );
}

export function SprintsPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: sprints = [], isLoading } = useSprints(spaceId);
  const { data: activeSprint } = useActiveSprint(spaceId);
  const createMutation = useCreateSprint(spaceId);

  const [dialogOpen, setDialogOpen] = useState(false);
  const [name, setName] = useState('');
  const [goal, setGoal] = useState('');
  const [startsAt, setStartsAt] = useState('');
  const [endsAt, setEndsAt] = useState('');

  function resetForm() { setName(''); setGoal(''); setStartsAt(''); setEndsAt(''); }

  // The API decodes starts_at/ends_at as RFC3339 timestamps; a bare
  // YYYY-MM-DD from <input type="date"> is rejected with 400.
  function toRFC3339(date: string): string | undefined {
    return date ? `${date}T00:00:00Z` : undefined;
  }

  async function handleCreate() {
    if (!name.trim()) return;
    await createMutation.mutateAsync({
      name: name.trim(),
      goal: goal.trim() || undefined,
      starts_at: toRFC3339(startsAt),
      ends_at: toRFC3339(endsAt),
    });
    setDialogOpen(false);
    resetForm();
  }

  if (isLoading) {
    return <div className="flex h-64 items-center justify-center text-[var(--color-text-muted)]">Loading sprints…</div>;
  }

  const sorted = [...sprints].sort((a, b) => {
    const order = { active: 0, planned: 1, completed: 2 };
    return (order[a.status as keyof typeof order] ?? 3) - (order[b.status as keyof typeof order] ?? 3);
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">Sprints</h1>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">{sprints.length} sprint{sprints.length !== 1 ? 's' : ''}</p>
        </div>
        <Button onClick={() => setDialogOpen(true)}>
          <Plus className="mr-1.5 h-4 w-4" />New Sprint
        </Button>
      </div>

      {sorted.length === 0 ? (
        <div className="flex h-48 items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
          <p className="text-[var(--color-text-muted)]">No sprints yet. Create one to get started.</p>
        </div>
      ) : (
        <div className="space-y-2">
          {sorted.map(s => (
            <SprintRow key={s.id} sprint={s} spaceId={spaceId} isActive={activeSprint?.id === s.id} />
          ))}
        </div>
      )}

      <Dialog open={dialogOpen} onOpenChange={(o) => { setDialogOpen(o); if (!o) resetForm(); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New Sprint</DialogTitle>
            <DialogDescription>Create a new sprint for this project.</DialogDescription>
          </DialogHeader>
          <div className="py-2">
            <Field>
              <FieldLabel htmlFor="sprint-name">Name</FieldLabel>
              <Input id="sprint-name" placeholder="Sprint 1" value={name} onChange={e => setName(e.target.value)} autoFocus />
            </Field>
            <Field>
              <FieldLabel htmlFor="sprint-goal" optional>Goal</FieldLabel>
              <Input id="sprint-goal" placeholder="What do we want to achieve?" value={goal} onChange={e => setGoal(e.target.value)} />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field>
                <FieldLabel htmlFor="sprint-starts">Start date</FieldLabel>
                <Input id="sprint-starts" type="date" value={startsAt} onChange={e => setStartsAt(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="sprint-ends">End date</FieldLabel>
                <Input id="sprint-ends" type="date" value={endsAt} onChange={e => setEndsAt(e.target.value)} />
              </Field>
            </div>
            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(createMutation.error, 'The sprint could not be created.')}
              </p>
            )}
          </div>
          <DialogFooter>
            <DialogClose asChild><Button variant="outline">Cancel</Button></DialogClose>
            <Button onClick={handleCreate} disabled={createMutation.isPending || !name.trim()}>
              {createMutation.isPending ? 'Creating…' : 'Create Sprint'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
