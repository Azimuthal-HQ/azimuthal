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
import { RadioCardGroup } from '../../components/ui/radio-card';
import { formatUTCDate, toRFC3339Date } from '../../lib/utils';
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

type Disposition = 'backlog' | 'next';

function SprintRow({
  sprint, spaceId, isActive, carryOverTargets,
}: {
  sprint: Sprint;
  spaceId: string;
  isActive: boolean;
  // Sprints this one's incomplete items may carry over to on completion:
  // every non-completed sprint in the space except this one.
  carryOverTargets: Sprint[];
}) {
  const startMutation = useStartSprint(spaceId);
  const completeMutation = useCompleteSprint(spaceId);
  const [startError, setStartError] = useState<string | null>(null);

  const [completeOpen, setCompleteOpen] = useState(false);
  const [disposition, setDisposition] = useState<Disposition>('backlog');
  const [nextSprintId, setNextSprintId] = useState<string>('');
  const [completeError, setCompleteError] = useState<string | null>(null);

  async function handleStart() {
    setStartError(null);
    try { await startMutation.mutateAsync(sprint.id); }
    catch (e) { setStartError(friendlyErrorMessage(e, 'The sprint could not be started.')); }
  }

  function openComplete() {
    // Default the carry-over target to the first candidate so choosing "next"
    // is a single click when a destination exists.
    setDisposition('backlog');
    setNextSprintId(carryOverTargets[0]?.id ?? '');
    setCompleteError(null);
    setCompleteOpen(true);
  }

  async function handleComplete() {
    setCompleteError(null);
    const target = disposition === 'next' ? nextSprintId : null;
    try {
      await completeMutation.mutateAsync({ sprintId: sprint.id, nextSprintId: target });
      setCompleteOpen(false);
    } catch (e) {
      setCompleteError(friendlyErrorMessage(e, 'The sprint could not be completed.'));
    }
  }

  const dispositionOptions = [
    {
      value: 'backlog' as const,
      title: 'Return to backlog',
      description: 'Incomplete items go back to the backlog.',
    },
    {
      value: 'next' as const,
      title: 'Move to another sprint',
      description: carryOverTargets.length
        ? 'Incomplete items carry over to the sprint you choose.'
        : 'No other sprint is available to carry work over to.',
      disabled: carryOverTargets.length === 0,
    },
  ];

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
          <Button size="sm" variant="outline" onClick={openComplete} disabled={completeMutation.isPending}>
            <CheckCircle className="mr-1.5 h-3.5 w-3.5" />
            {completeMutation.isPending ? 'Completing…' : 'Complete'}
          </Button>
        )}
      </div>

      <Dialog open={completeOpen} onOpenChange={setCompleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Complete {sprint.name}</DialogTitle>
            <DialogDescription>
              Done items stay on this sprint. Choose where its unfinished items go.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <RadioCardGroup
              aria-label="What happens to incomplete items"
              testId="complete-disposition"
              options={dispositionOptions}
              value={disposition}
              onChange={setDisposition}
            />
            {disposition === 'next' && carryOverTargets.length > 0 && (
              <Field>
                <FieldLabel htmlFor={`next-sprint-${sprint.id}`}>Carry over to</FieldLabel>
                <select
                  id={`next-sprint-${sprint.id}`}
                  aria-label="Carry-over sprint"
                  value={nextSprintId}
                  onChange={e => setNextSprintId(e.target.value)}
                  className="flex h-9 w-full items-center rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)] focus:border-[var(--color-primary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-primary)]"
                >
                  {carryOverTargets.map(t => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </Field>
            )}
            {completeError && (
              <p className="flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-danger)]">
                <AlertCircle className="h-3.5 w-3.5" />{completeError}
              </p>
            )}
          </div>
          <DialogFooter>
            <DialogClose asChild><Button variant="outline">Cancel</Button></DialogClose>
            <Button onClick={handleComplete} disabled={completeMutation.isPending}>
              {completeMutation.isPending ? 'Completing…' : 'Complete Sprint'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
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

  async function handleCreate() {
    if (!name.trim()) return;
    // toRFC3339Date was a local helper here until the due-date controls on the
    // two detail pages needed the same conversion. It now lives in lib/utils
    // beside its inverse, formatUTCDate, rather than in a second copy.
    await createMutation.mutateAsync({
      name: name.trim(),
      goal: goal.trim() || undefined,
      starts_at: toRFC3339Date(startsAt),
      ends_at: toRFC3339Date(endsAt),
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
            <SprintRow
              key={s.id}
              sprint={s}
              spaceId={spaceId}
              isActive={activeSprint?.id === s.id}
              carryOverTargets={sprints.filter(t => t.id !== s.id && t.status !== 'completed')}
            />
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
