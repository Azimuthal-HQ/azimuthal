import { useMemo, useState } from 'react';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useAccessMatrix,
  useBulkApplyGrants,
  useBulkPreviewGrants,
  type BulkChange,
  type BulkResult,
  type GrantRole,
  type MatrixSpace,
  type MatrixTeam,
  type SpaceType,
} from '../../lib/api';
import { ModuleChip } from '../../shell/ModuleChip';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { Input } from '../../components/ui/input';

const ROLES: GrantRole[] = ['viewer', 'contributor', 'agent', 'space_admin'];
const ROLE_SHORT: Record<GrantRole, string> = {
  viewer: 'V',
  contributor: 'C',
  agent: 'A',
  space_admin: 'SA',
};

/** Staged edits keyed `${teamId}:${spaceId}`; null stages a revoke. */
type Staged = Map<string, GrantRole | null>;

const cellKey = (teamId: string, spaceId: string) => `${teamId}:${spaceId}`;

/**
 * AccessMatrixPage (P2.5 W6): teams × spaces, every cell at most one grant
 * row. Solid = direct grant; ghosted = access inherited from a team below
 * (ADR-0007 subject-side expansion) — it has NO grant row, and acting on it
 * creates a direct grant, never edits the descendant's. Bulk edits stage
 * locally, preview server-side, and apply as one transaction with one
 * batch_id.
 */
export function AccessMatrixPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const matrix = useAccessMatrix(orgId);

  const [teamFilter, setTeamFilter] = useState('');
  const [spaceFilter, setSpaceFilter] = useState('');
  const [moduleFilter, setModuleFilter] = useState<SpaceType | 'all'>('all');
  const [staged, setStaged] = useState<Staged>(new Map());
  const [editing, setEditing] = useState<{ team: MatrixTeam; space: MatrixSpace } | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [applied, setApplied] = useState<BulkResult | null>(null);

  const data = matrix.data;

  // Direct grants by cell; inherited access derived from team paths: team T
  // inherits on S when a DESCENDANT of T (T in its path, not itself) holds
  // a grant there. Highest descendant role wins for display.
  const { directByCell, inheritedByCell, inheritedFrom } = useMemo(() => {
    const direct = new Map<string, GrantRole>();
    const inherited = new Map<string, GrantRole>();
    const from = new Map<string, string>();
    if (!data) return { directByCell: direct, inheritedByCell: inherited, inheritedFrom: from };
    const teamsByID = new Map(data.teams.map((t) => [t.id, t]));
    const rank = (r: GrantRole) => ROLES.indexOf(r);
    for (const g of data.grants) {
      direct.set(cellKey(g.team_id, g.space_id), g.role);
    }
    for (const g of data.grants) {
      const holder = teamsByID.get(g.team_id);
      if (!holder) continue;
      for (const ancestorID of holder.path) {
        if (ancestorID === g.team_id) continue;
        const key = cellKey(ancestorID, g.space_id);
        const existing = inherited.get(key);
        if (!existing || rank(g.role) > rank(existing)) {
          inherited.set(key, g.role);
          from.set(key, holder.name);
        }
      }
    }
    return { directByCell: direct, inheritedByCell: inherited, inheritedFrom: from };
  }, [data]);

  const teams = useMemo(() => {
    if (!data) return [];
    const ordered = [...data.teams].sort((a, b) => {
      const ap = a.path.join('/');
      const bp = b.path.join('/');
      if (ap < bp) return -1;
      if (ap > bp) return 1;
      return 0;
    });
    const q = teamFilter.trim().toLowerCase();
    if (!q) return ordered;
    const matches = new Set(ordered.filter((t) => t.name.toLowerCase().includes(q)).map((t) => t.id));
    // Keep ancestors of matches so the tree stays coherent.
    return ordered.filter(
      (t) => matches.has(t.id) || ordered.some((m) => matches.has(m.id) && m.path.includes(t.id)),
    );
  }, [data, teamFilter]);

  const spaces = useMemo(() => {
    if (!data) return [];
    const q = spaceFilter.trim().toLowerCase();
    return data.spaces.filter((s) => {
      if (moduleFilter !== 'all' && s.type !== moduleFilter) return false;
      return !q || s.name.toLowerCase().includes(q);
    });
  }, [data, spaceFilter, moduleFilter]);

  if (matrix.isLoading) {
    return <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading access matrix…</p>;
  }
  if (matrix.error || !data) {
    return (
      <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(matrix.error, 'The access matrix could not be loaded.')}
      </p>
    );
  }

  const stageCell = (teamId: string, spaceId: string, role: GrantRole | null | undefined) => {
    setStaged((prev) => {
      const next = new Map(prev);
      const key = cellKey(teamId, spaceId);
      if (role === undefined) {
        next.delete(key);
      } else {
        next.set(key, role);
      }
      return next;
    });
  };

  const stageMany = (cells: Array<{ teamId: string; spaceId: string }>, role: GrantRole | null) => {
    setStaged((prev) => {
      const next = new Map(prev);
      for (const c of cells) next.set(cellKey(c.teamId, c.spaceId), role);
      return next;
    });
  };

  const changes: BulkChange[] = [...staged.entries()].map(([key, role]) => {
    const [team_id, space_id] = key.split(':');
    return { team_id, space_id, role };
  });

  return (
    <div data-testid="admin-access-matrix">
      <p className="mb-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
        Every cell is one grant: solid = direct, ghosted = inherited from a team below. Select
        cells, rows, or columns, stage a role, then preview before anything applies.
      </p>

      <div className="mb-[var(--space-3)] flex flex-wrap items-center gap-[var(--space-2)]">
        <Input
          value={teamFilter}
          onChange={(e) => setTeamFilter(e.target.value)}
          placeholder="Filter teams…"
          data-testid="matrix-filter-team"
          className="h-8 w-48"
        />
        <Input
          value={spaceFilter}
          onChange={(e) => setSpaceFilter(e.target.value)}
          placeholder="Filter spaces…"
          data-testid="matrix-filter-space"
          className="h-8 w-48"
        />
        <select
          value={moduleFilter}
          onChange={(e) => setModuleFilter(e.target.value as SpaceType | 'all')}
          data-testid="matrix-filter-module"
          className="h-8 rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-[var(--text-sm)] text-[var(--color-text)]"
        >
          <option value="all">All modules</option>
          <option value="beacon">Beacon</option>
          <option value="codex">Codex</option>
          <option value="vector">Vector</option>
        </select>
      </div>

      <Card>
        <CardContent className="overflow-x-auto p-0">
          <table className="w-full border-collapse">
            <thead>
              <tr>
                <th className="sticky left-0 z-10 min-w-[220px] border-b border-r border-[var(--color-border)] bg-[var(--color-surface)] px-[var(--space-3)] py-[var(--space-2)] text-left text-[var(--text-xs)] font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
                  Team
                </th>
                {spaces.map((s) => (
                  <th
                    key={s.id}
                    className="min-w-[7.5rem] border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-2)] text-left align-bottom"
                  >
                    <button
                      type="button"
                      title={`Stage a role for every visible team on ${s.name}`}
                      data-testid={`matrix-col-${s.id}`}
                      onClick={() => setEditing({ team: COLUMN_SENTINEL, space: s })}
                      className="block w-full text-left"
                    >
                      <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                        {s.name}
                      </span>
                      <span className="mt-0.5 flex items-center gap-1">
                        <ModuleChip module={s.type} />
                        <span className="text-[10px] text-[var(--color-text-muted)]">{s.visibility}</span>
                      </span>
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {teams.map((t) => (
                <tr key={t.id} className="hover:bg-[var(--color-surface-hover)]">
                  <td className="sticky left-0 z-10 border-r border-[var(--color-border)] bg-[var(--color-surface)] px-[var(--space-3)] py-[var(--space-1)]">
                    <button
                      type="button"
                      title={`Stage a role for ${t.name} on every visible space`}
                      data-testid={`matrix-row-${t.id}`}
                      onClick={() => setEditing({ team: t, space: ROW_SENTINEL })}
                      className="flex w-full items-center gap-1 text-left"
                      style={{ paddingLeft: (t.path.length - 1) * 16 }}
                    >
                      <span className="truncate text-[var(--text-sm)] text-[var(--color-text)]">{t.name}</span>
                      <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        · {t.member_count}
                      </span>
                    </button>
                  </td>
                  {spaces.map((s) => (
                    <MatrixCell
                      key={s.id}
                      team={t}
                      space={s}
                      direct={directByCell.get(cellKey(t.id, s.id))}
                      inherited={inheritedByCell.get(cellKey(t.id, s.id))}
                      stagedRole={staged.has(cellKey(t.id, s.id)) ? staged.get(cellKey(t.id, s.id)) : undefined}
                      onClick={() => setEditing({ team: t, space: s })}
                    />
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {teams.length === 0 && (
            <p className="px-[var(--space-4)] py-[var(--space-6)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No teams match the filter.
            </p>
          )}
        </CardContent>
      </Card>

      {applied && (
        <p
          data-testid="matrix-applied-note"
          className="mt-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-success)]"
        >
          Applied: {applied.creates} new grants, {applied.updates} role changes, {applied.revokes} revocations.
        </p>
      )}

      {staged.size > 0 && (
        <div
          data-testid="matrix-staged-bar"
          className="sticky bottom-0 mt-[var(--space-3)] flex items-center gap-[var(--space-3)] rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-[var(--space-4)] py-[var(--space-3)] shadow-[var(--shadow-lg)]"
        >
          <span className="text-[var(--text-sm)] text-[var(--color-text)]">
            {staged.size} staged change{staged.size === 1 ? '' : 's'}
          </span>
          <div className="ml-auto flex gap-[var(--space-2)]">
            <Button variant="outline" size="sm" onClick={() => setStaged(new Map())} data-testid="matrix-discard-button">
              Discard
            </Button>
            <Button size="sm" onClick={() => setPreviewing(true)} data-testid="matrix-preview-button">
              Preview changes
            </Button>
          </div>
        </div>
      )}

      {editing && (
        <CellEditor
          state={editing}
          visibleSpaces={spaces}
          visibleTeams={teams}
          direct={directByCell}
          inheritedFrom={inheritedFrom}
          onStage={(role) => {
            if (editing.space === ROW_SENTINEL) {
              stageMany(spaces.map((s) => ({ teamId: editing.team.id, spaceId: s.id })), role);
            } else if (editing.team === COLUMN_SENTINEL) {
              stageMany(teams.map((t) => ({ teamId: t.id, spaceId: editing.space.id })), role);
            } else {
              stageCell(editing.team.id, editing.space.id, role);
            }
            setEditing(null);
          }}
          onUnstage={() => {
            if (editing.space !== ROW_SENTINEL && editing.team !== COLUMN_SENTINEL) {
              stageCell(editing.team.id, editing.space.id, undefined);
            }
            setEditing(null);
          }}
          onClose={() => setEditing(null)}
          isStaged={
            editing.space !== ROW_SENTINEL &&
            editing.team !== COLUMN_SENTINEL &&
            staged.has(cellKey(editing.team.id, editing.space.id))
          }
        />
      )}

      {previewing && (
        <PreviewDialog
          orgId={orgId}
          changes={changes}
          teams={data.teams}
          spaces={data.spaces}
          onClose={() => setPreviewing(false)}
          onApplied={(result) => {
            setPreviewing(false);
            setStaged(new Map());
            setApplied(result);
          }}
        />
      )}
    </div>
  );
}

// Sentinels marking "whole row" / "whole column" targets for the editor.
const ROW_SENTINEL = { id: '__row__' } as unknown as MatrixSpace;
const COLUMN_SENTINEL = { id: '__column__' } as unknown as MatrixTeam;

function MatrixCell({ team, space, direct, inherited, stagedRole, onClick }: {
  team: MatrixTeam;
  space: MatrixSpace;
  direct?: GrantRole;
  inherited?: GrantRole;
  /** undefined = not staged; null = staged revoke; role = staged set. */
  stagedRole?: GrantRole | null;
  onClick: () => void;
}) {
  const isStaged = stagedRole !== undefined;
  const state = direct ? 'direct' : inherited ? 'inherited' : 'none';

  let content: React.ReactNode = null;
  if (isStaged) {
    content = stagedRole === null ? (
      <span className="text-[var(--text-xs)] text-[var(--color-danger)] line-through">
        {direct ? ROLE_SHORT[direct] : '—'}
      </span>
    ) : (
      <span className="rounded-[var(--radius-full)] bg-[var(--color-primary)] px-2 py-0.5 text-[10px] font-medium text-[var(--color-text-inverse)]">
        {ROLE_SHORT[stagedRole]}
      </span>
    );
  } else if (direct) {
    content = (
      <span
        title={direct}
        className="rounded-[var(--radius-full)] bg-[var(--color-primary-muted)] px-2 py-0.5 text-[10px] font-medium text-[var(--color-primary)]"
      >
        {ROLE_SHORT[direct]}
      </span>
    );
  } else if (inherited) {
    content = (
      <span
        title={`Inherited (${inherited}) — a team below holds this grant. Click to create a direct grant.`}
        className="rounded-[var(--radius-full)] border border-dashed border-[var(--color-border)] px-2 py-0.5 text-[10px] font-medium text-[var(--color-text-muted)] opacity-50"
      >
        {ROLE_SHORT[inherited]}
      </span>
    );
  }

  return (
    <td className="border-b border-[var(--color-border)] px-[var(--space-2)] py-[var(--space-1)]">
      <button
        type="button"
        aria-label={`${team.name} on ${space.name}`}
        data-testid={`matrix-cell-${team.id}-${space.id}`}
        data-state={state}
        data-staged={isStaged || undefined}
        onClick={onClick}
        className={cn(
          'flex h-8 w-full items-center justify-center rounded-[var(--radius-md)] transition-colors',
          'hover:bg-[var(--color-surface-hover)]',
          isStaged && 'ring-1 ring-[var(--color-primary)]',
        )}
      >
        {content}
      </button>
    </td>
  );
}

function CellEditor({ state, direct, inheritedFrom, onStage, onUnstage, onClose, isStaged, visibleSpaces, visibleTeams }: {
  state: { team: MatrixTeam; space: MatrixSpace };
  direct: Map<string, GrantRole>;
  inheritedFrom: Map<string, string>;
  onStage: (role: GrantRole | null) => void;
  onUnstage: () => void;
  onClose: () => void;
  isStaged: boolean;
  visibleSpaces: MatrixSpace[];
  visibleTeams: MatrixTeam[];
}) {
  const isRow = state.space === ROW_SENTINEL;
  const isColumn = state.team === COLUMN_SENTINEL;
  const single = !isRow && !isColumn;
  const key = single ? cellKey(state.team.id, state.space.id) : '';
  const directRole = single ? direct.get(key) : undefined;
  const inheritedHolder = single && !directRole ? inheritedFrom.get(key) : undefined;

  let title: string;
  if (isRow) {
    title = `${state.team.name} — every visible space (${visibleSpaces.length})`;
  } else if (isColumn) {
    title = `${state.space.name} — every visible team (${visibleTeams.length})`;
  } else {
    title = `${state.team.name} on ${state.space.name}`;
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="matrix-cell-editor">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {single && inheritedHolder && (
            <DialogDescription data-testid="matrix-inherited-note">
              This access is inherited from {inheritedHolder}. Setting a role here creates a direct
              grant for {state.team.name} — the inherited grant is never edited.
            </DialogDescription>
          )}
          {single && directRole && <DialogDescription>Current direct role: {directRole}.</DialogDescription>}
          {(isRow || isColumn) && (
            <DialogDescription>
              The chosen role is staged for every visible cell; nothing applies until you preview
              and confirm.
            </DialogDescription>
          )}
        </DialogHeader>
        <div className="flex flex-wrap gap-[var(--space-2)]">
          {ROLES.map((r) => (
            <Button
              key={r}
              variant="outline"
              size="sm"
              data-testid={`matrix-editor-role-${r}`}
              onClick={() => onStage(r)}
            >
              {r}
            </Button>
          ))}
          {(directRole || isRow || isColumn) && (
            <Button variant="destructive" size="sm" data-testid="matrix-editor-revoke" onClick={() => onStage(null)}>
              Revoke
            </Button>
          )}
          {isStaged && (
            <Button variant="ghost" size="sm" data-testid="matrix-editor-unstage" onClick={onUnstage}>
              Remove staged change
            </Button>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PreviewDialog({ orgId, changes, teams, spaces, onClose, onApplied }: {
  orgId: string;
  changes: BulkChange[];
  teams: MatrixTeam[];
  spaces: MatrixSpace[];
  onClose: () => void;
  onApplied: (result: BulkResult) => void;
}) {
  const preview = useBulkPreviewGrants(orgId);
  const apply = useBulkApplyGrants(orgId);
  const [ticketRef, setTicketRef] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [previewData, setPreviewData] = useState<BulkResult | null>(null);
  const [previewStarted, setPreviewStarted] = useState(false);

  // Compute the diff once when the dialog opens.
  if (!previewStarted) {
    setPreviewStarted(true);
    preview.mutate(changes, {
      onSuccess: setPreviewData,
      onError: (err) =>
        setError(friendlyErrorMessage(err, 'The preview could not be computed. Nothing was changed.')),
    });
  }

  const teamName = (id: string) => teams.find((t) => t.id === id)?.name ?? id;
  const spaceName = (id: string) => spaces.find((s) => s.id === id)?.name ?? id;

  const interesting = previewData?.actions.filter((a) => a.action !== 'noop') ?? [];

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="matrix-preview-dialog">
        <DialogHeader>
          <DialogTitle>Review before applying</DialogTitle>
          {previewData && (
            <DialogDescription data-testid="matrix-preview-summary">
              {previewData.creates} new grants, {previewData.updates} role changes, {previewData.revokes} revocations
              {previewData.noops > 0 ? ` (${previewData.noops} already as requested)` : ''}. Applied
              as one transaction — a failure anywhere changes nothing.
            </DialogDescription>
          )}
          {!previewData && !error && <DialogDescription>Computing the diff…</DialogDescription>}
        </DialogHeader>

        {previewData && (
          <div className="max-h-64 space-y-1 overflow-y-auto" data-testid="matrix-preview-items">
            {interesting.map((a) => (
              <div
                key={`${a.team_id}:${a.space_id}`}
                className="flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)] bg-[var(--color-surface-hover)] px-[var(--space-2)] py-1 text-[var(--text-sm)]"
              >
                <span className="min-w-0 flex-1 truncate text-[var(--color-text)]">
                  {teamName(a.team_id)} × {spaceName(a.space_id)}
                </span>
                <span
                  className={cn(
                    'shrink-0 text-[var(--text-xs)]',
                    a.action === 'revoke' ? 'text-[var(--color-danger)]' : 'text-[var(--color-text-muted)]',
                  )}
                >
                  {a.action === 'create' && `+ ${a.to_role}`}
                  {a.action === 'update' && `${a.from_role} → ${a.to_role}`}
                  {a.action === 'revoke' && `revoke ${a.from_role}`}
                </span>
              </div>
            ))}
            {interesting.length === 0 && (
              <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
                Everything is already as requested — nothing to apply.
              </p>
            )}
          </div>
        )}

        {previewData && interesting.length > 0 && (
          <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
            Ticket reference (optional)
            <Input
              value={ticketRef}
              onChange={(e) => setTicketRef(e.target.value)}
              placeholder="e.g. BEA-42"
              maxLength={200}
              data-testid="matrix-ticket-ref"
              className="mt-1"
            />
            <span className="mt-0.5 block text-[var(--text-xs)] text-[var(--color-text-muted)]">
              Recorded on every audit event of this batch.
            </span>
          </label>
        )}

        {error && (
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]" data-testid="matrix-preview-error">
            {error}
          </p>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            disabled={!previewData || interesting.length === 0 || apply.isPending}
            data-testid="matrix-apply-button"
            onClick={() => {
              setError(null);
              apply.mutate(
                { changes, ticketRef: ticketRef || undefined },
                {
                  onSuccess: onApplied,
                  onError: (err) =>
                    setError(friendlyErrorMessage(err, 'The changes could not be applied. Nothing was changed.')),
                },
              );
            }}
          >
            {apply.isPending ? 'Applying…' : `Apply ${interesting.length} change${interesting.length === 1 ? '' : 's'}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
