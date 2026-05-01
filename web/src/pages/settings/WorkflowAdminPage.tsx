import { useState } from 'react';
import { ChevronDown, ChevronRight, Circle, Workflow } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { useAuth } from '../../lib/auth';
import { useQuery } from '@tanstack/react-query';
import { queryKeys, type WorkflowState } from '../../lib/api';

// ---------------------------------------------------------------------------
// Types & helpers
// ---------------------------------------------------------------------------

interface Workflow {
  id: string;
  name: string;
  description?: string;
  is_default: boolean;
  applies_to: string;
}


async function apiFetch<T>(url: string): Promise<T> {
  const token = localStorage.getItem('azimuthal_token') ?? '';
  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

const CATEGORY_COLOR: Record<string, string> = {
  todo: 'text-blue-500',
  in_progress: 'text-amber-500',
  done: 'text-emerald-500',
};

// ---------------------------------------------------------------------------
// WorkflowCard
// ---------------------------------------------------------------------------

function WorkflowCard({ wf, states }: { wf: Workflow; states: WorkflowState[] }) {
  const [expanded, setExpanded] = useState(true);

  return (
    <Card>
      <CardHeader className="cursor-pointer select-none pb-2" onClick={() => setExpanded((p) => !p)}>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            {expanded ? (
              <ChevronDown className="h-4 w-4 text-[var(--color-text-muted)]" />
            ) : (
              <ChevronRight className="h-4 w-4 text-[var(--color-text-muted)]" />
            )}
            <CardTitle className="text-[var(--text-base)]">{wf.name}</CardTitle>
            {wf.is_default && (
              <span className="rounded-full bg-[var(--color-primary)]/10 px-2 py-0.5 text-[var(--text-xs)] font-medium text-[var(--color-primary)]">
                default
              </span>
            )}
          </div>
          <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
            {wf.applies_to.replace('_', ' ')}
          </span>
        </div>
        {wf.description && (
          <p className="mt-1 pl-6 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {wf.description}
          </p>
        )}
      </CardHeader>

      {expanded && (
        <CardContent className="pt-0">
          <div className="space-y-1 pl-6">
            {states.map((s, i) => (
              <div key={s.id} className="flex items-center gap-3 rounded-[var(--radius-md)] px-2 py-1.5 hover:bg-[var(--color-surface-hover)]">
                <span className="text-[var(--text-xs)] w-4 text-center text-[var(--color-text-muted)]">
                  {i + 1}
                </span>
                <Circle
                  className="h-3 w-3 shrink-0"
                  style={{ color: s.color, fill: s.color }}
                />
                <span className="flex-1 text-[var(--text-sm)] text-[var(--color-text)]">
                  {s.name}
                </span>
                <span className={`text-[var(--text-xs)] ${CATEGORY_COLOR[s.category] ?? ''}`}>
                  {s.category}
                </span>
                {s.is_initial && (
                  <span className="rounded-full bg-[var(--color-surface-hover)] px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    initial
                  </span>
                )}
              </div>
            ))}
            {states.length === 0 && (
              <p className="py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                No states defined.
              </p>
            )}
          </div>
        </CardContent>
      )}
    </Card>
  );
}

// ---------------------------------------------------------------------------
// WorkflowAdminPage
// ---------------------------------------------------------------------------

/** Read-only workflow overview for org admins. */
export function WorkflowAdminPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const { data: workflows, isLoading, error } = useQuery<Workflow[]>({
    queryKey: ['workflows', orgId],
    queryFn: () => apiFetch<Workflow[]>(`/api/v1/orgs/${orgId}/workflows`),
    enabled: !!orgId,
  });

  const [statesCache, setStatesCache] = useState<Record<string, WorkflowState[]>>({});

  // Fetch states for each workflow using individual queries (declaratively via
  // a helper component to avoid hook-in-loop issues).
  if (isLoading) {
    return (
      <div className="flex h-48 items-center justify-center text-[var(--color-text-muted)]">
        Loading workflows…
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4 text-[var(--text-sm)] text-[var(--color-danger)]">
        Failed to load workflows.
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-3">
        <Workflow className="h-6 w-6 text-[var(--color-primary)]" />
        <div>
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            Workflows
          </h1>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            State machines that govern how tickets and project items move through your process.
          </p>
        </div>
      </div>

      {(workflows ?? []).length === 0 ? (
        <div className="flex min-h-[200px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
          <p className="text-[var(--color-text-muted)]">No workflows found for this organization.</p>
        </div>
      ) : (
        <div className="space-y-4">
          {(workflows ?? []).map((wf) => (
            <WorkflowStatesLoader
              key={wf.id}
              wf={wf}
              orgId={orgId}
              onStatesLoaded={(id, states) =>
                setStatesCache((prev) => ({ ...prev, [id]: states }))
              }
              states={statesCache[wf.id] ?? []}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// Thin wrapper that fetches states for one workflow and calls onStatesLoaded.
function WorkflowStatesLoader({
  wf,
  orgId,
  states,
  onStatesLoaded,
}: {
  wf: Workflow;
  orgId: string;
  states: WorkflowState[];
  onStatesLoaded: (id: string, states: WorkflowState[]) => void;
}) {
  useQuery<WorkflowState[]>({
    queryKey: queryKeys.workflowStates(wf.id),
    queryFn: async () => {
      const data = await apiFetch<WorkflowState[]>(
        `/api/v1/orgs/${orgId}/workflows/${wf.id}/states`,
      );
      onStatesLoaded(wf.id, data);
      return data;
    },
    enabled: !!orgId && !!wf.id,
    staleTime: 5 * 60 * 1000,
  });

  return <WorkflowCard wf={wf} states={states} />;
}
