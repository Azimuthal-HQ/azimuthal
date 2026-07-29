import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useNavigate } from 'react-router-dom';
import { AlertCircle, LayoutGrid, Plus, Star } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge } from '../../components/ui/badge';
import { Input } from '../../components/ui/input';
import { FieldLabel, FieldHint } from '../../components/ui/field';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '../../components/ui/dialog';
import { SegmentedControl } from '../../components/ui/segmented';
import { EmptyState } from '../../shell/EmptyState';
import { ModuleChip } from '../../shell/ModuleChip';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useCreateDashboard,
  useDashboards,
  type Dashboard,
  type DashboardModule,
} from '../../lib/api';

const MODULE_LABEL: Record<DashboardModule, string> = {
  home: 'Home',
  beacon: 'Beacon',
  vector: 'Vector',
};

/**
 * Every dashboard whose definition reaches the reader, grouped by module.
 *
 * Own and shared in ONE list, as the saved-views list does, with the
 * provenance on the row rather than in a tab. Two lists would make "mine" and
 * "shared with me" feel like different kinds of thing, and a shared dashboard
 * is exactly as usable as your own.
 */
export function DashboardsListPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const navigate = useNavigate();
  const { data: dashboards, isLoading, error } = useDashboards(orgId);
  const [creating, setCreating] = useState(false);

  const grouped = groupByModule(dashboards ?? []);

  return (
    <div className="space-y-[var(--space-6)]" data-testid="dashboards-list">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[19px] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            Dashboards
          </h1>
          <p className="mt-0.5 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            Composable grids of gadgets. Every one resolves against your own access.
          </p>
        </div>
        <Button data-testid="new-dashboard" onClick={() => setCreating(true)}>
          <Plus className="mr-2 h-4 w-4" />
          New dashboard
        </Button>
      </div>

      {isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading your dashboards…
        </div>
      )}

      {error && (
        <div
          data-testid="dashboards-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(error, 'Your dashboards are unavailable right now.')}
          </p>
        </div>
      )}

      {dashboards && dashboards.length === 0 && !isLoading && (
        <EmptyState
          icon={LayoutGrid}
          title="No dashboards yet"
          description="A dashboard arranges gadgets over your saved views. Build one for a team, a sprint or yourself."
          action={<Button onClick={() => setCreating(true)}>Create a dashboard</Button>}
        />
      )}

      {grouped.map(([module, rows]) => (
        <section key={module}>
          <h2 className="mb-[var(--space-2)] text-[10px] font-medium uppercase tracking-wider text-[var(--color-text-muted)]">
            {MODULE_LABEL[module]}
          </h2>
          <ul className="overflow-hidden rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)]">
            {rows.map((d) => (
              <li
                key={d.id}
                data-testid="dashboard-row"
                data-valid={String(d.is_valid)}
                className="border-b border-[var(--color-border)] last:border-b-0"
              >
                <Link
                  to={`/dashboards/${d.id}`}
                  className="flex flex-wrap items-center gap-2 px-3 py-3 transition-colors hover:bg-[var(--color-surface-hover)]"
                >
                  <ModuleChip module={d.module === 'home' ? 'vector' : d.module} />
                  <span className="text-[13px] text-[var(--color-text)]">{d.name}</span>
                  {d.is_default && (
                    <Badge variant="secondary" data-testid="dashboard-default-chip">
                      <Star className="mr-1 h-3 w-3" />
                      On Home
                    </Badge>
                  )}
                  <span className="ml-auto flex items-center gap-2">
                    <Badge variant="outline" data-testid="dashboard-row-visibility" data-visibility={d.visibility}>
                      {d.visibility === 'org'
                        ? 'Organisation'
                        : d.visibility === 'team'
                          ? (d.team_name ?? 'Team')
                          : 'Private'}
                    </Badge>
                    <Badge
                      variant="secondary"
                      data-testid="dashboard-row-owner"
                      data-owner={d.is_owner ? 'me' : 'other'}
                    >
                      {d.is_owner ? 'Yours' : (d.owner_name ?? 'Shared')}
                    </Badge>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </section>
      ))}

      <CreateDashboardDialog
        open={creating}
        onOpenChange={setCreating}
        orgId={orgId}
        onCreated={(id) => navigate(`/dashboards/${id}`)}
      />
    </div>
  );
}

/** Home first, then the modules, so the list reads in the order the product does. */
function groupByModule(rows: Dashboard[]): Array<[DashboardModule, Dashboard[]]> {
  const order: DashboardModule[] = ['home', 'beacon', 'vector'];
  return order
    .map((m) => [m, rows.filter((d) => d.module === m)] as [DashboardModule, Dashboard[]])
    .filter(([, list]) => list.length > 0);
}

function CreateDashboardDialog({
  open,
  onOpenChange,
  orgId,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  orgId: string;
  onCreated: (id: string) => void;
}) {
  const create = useCreateDashboard(orgId);
  const [name, setName] = useState('');
  const [module, setModule] = useState<DashboardModule>('home');

  async function submit() {
    if (!name.trim()) return;
    try {
      const created = await create.mutateAsync({
        name: name.trim(),
        description: '',
        module,
        visibility: 'private',
        visibility_team_id: null,
      });
      setName('');
      onOpenChange(false);
      onCreated(created.id);
    } catch {
      // Rendered from the mutation's error state below.
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="create-dashboard-dialog">
        <DialogHeader>
          <DialogTitle>New dashboard</DialogTitle>
          <DialogDescription>
            It starts empty and private to you. Add gadgets, then decide who else should see the
            arrangement.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div>
            <FieldLabel htmlFor="dashboard-name-input">Name</FieldLabel>
            <Input
              id="dashboard-name-input"
              data-testid="dashboard-name-input"
              placeholder="e.g. Support health"
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
          </div>
          <div>
            <FieldLabel id="dashboard-module-label">Where it belongs</FieldLabel>
            <FieldHint>Home dashboards can be shown as your landing page.</FieldHint>
            <SegmentedControl
              testId="dashboard-module"
              aria-label="Where this dashboard belongs"
              value={module}
              onChange={(v) => setModule(v as DashboardModule)}
              options={[
                { value: 'home', label: 'Home' },
                { value: 'beacon', label: 'Beacon' },
                { value: 'vector', label: 'Vector' },
              ]}
            />
          </div>
          {create.error && (
            <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(create.error, 'The dashboard could not be created.')}
            </p>
          )}
        </div>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" type="button">
              Cancel
            </Button>
          </DialogClose>
          <Button
            data-testid="create-dashboard-submit"
            onClick={submit}
            disabled={!name.trim() || create.isPending}
          >
            {create.isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
