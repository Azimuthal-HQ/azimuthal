import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { Plus, Play, CheckCircle, Clock, AlertCircle } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogDescription, DialogFooter, DialogClose,
} from '../../components/ui/dialog';
import { cn, formatUTCDate } from '../../lib/utils';
import {
  useSprints, useActiveSprint, useCreateSprint, useStartSprint, useCompleteSprint,
  type Sprint,
} from '../../lib/api';

const STATUS_STYLES: Record<string, string> = {
  planning:  'bg-[var(--color-surface-hover)] text-[var(--color-text-muted)]',
  active:    'bg-[var(--color-primary-muted)] text-[var(--color-primary)]',
  completed: 'bg-[var(--color-success)]/15 text-[var(--color-success)]',
};

function SprintRow({ sprint, spaceId, isActive }: { sprint: Sprint; spaceId: string; isActive: boolean }) {
  const startMutation = useStartSprint(spaceId);
  const completeMutation = useCompleteSprint(spaceId);
  const [startError, setStartError] = useState<string | null>(null);

  async function handleStart() {
    setStartError(null);
    try { await startMutation.mutateAsync(sprint.id); }
    catch (e) { setStartError(e instanceof Error ? e.message : 'Failed to start sprint'); }
  }

  async function handleComplete() {
    await completeMutation.mutateAsync(sprint.id);
  }

  return (
    <div className="flex items-center gap-4 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3">
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--color-text)] truncate">{sprint.name}</span>
          {isActive && (
            <span className="rounded-full bg-[var(--color-primary)] px-2 py-0.5 text-[var(--text-xs)] text-white font-semibold">Active</span>
          )}
          <span className={cn('rounded-full px-2 py-0.5 text-[var(--text-xs)] font-medium capitalize', STATUS_STYLES[sprint.status] ?? STATUS_STYLES.planning)}>
            {sprint.status}
          </span>
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
        {sprint.status === 'planning' && (
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
    const order = { active: 0, planning: 1, completed: 2 };
    return (order[a.status as keyof typeof order] ?? 3) - (order[b.status as keyof typeof order] ?? 3);
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">Sprints</h1>
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
          <div className="space-y-3 py-2">
            <div className="space-y-1">
              <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Name *</label>
              <Input placeholder="Sprint 1" value={name} onChange={e => setName(e.target.value)} autoFocus />
            </div>
            <div className="space-y-1">
              <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Goal</label>
              <Input placeholder="What do we want to achieve?" value={goal} onChange={e => setGoal(e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Start date</label>
                <Input type="date" value={startsAt} onChange={e => setStartsAt(e.target.value)} />
              </div>
              <div className="space-y-1">
                <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">End date</label>
                <Input type="date" value={endsAt} onChange={e => setEndsAt(e.target.value)} />
              </div>
            </div>
            {createMutation.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{createMutation.error.message}</p>
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
