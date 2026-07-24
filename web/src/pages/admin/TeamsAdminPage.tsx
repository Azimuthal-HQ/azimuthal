import { useMemo, useState } from 'react';
import {
  ChevronDown,
  ChevronRight,
  Pencil,
  Plus,
  Star,
  Trash2,
  Users,
  X,
} from 'lucide-react';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { Input } from '../../components/ui/input';
import { PersonTeamPicker, type PickedSubject } from '../../components/PersonTeamPicker';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useCreateTeamWithSpaces,
  useDeleteTeam,
  usePutTeamMember,
  useRemoveTeamMember,
  useTeamMembers,
  useTeams,
  useUpdateTeam,
  type SpaceType,
  type Team,
  type TeamRole,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const selectClass = cn(
  'h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

/** slugify derives a lowercase-kebab slug from a team name. */
function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
}

interface TeamNode {
  team: Team;
  depth: number;
}

/**
 * orderTeams flattens the team list into depth-first display order,
 * roots first, siblings alphabetical. Depth comes from the walk itself
 * (path.length would work too — the path ends in the team's own id — but
 * the walk also survives a row whose parent is missing from the list).
 */
function orderTeams(teams: Team[]): TeamNode[] {
  const byId = new Map(teams.map((t) => [t.id, t]));
  const children = new Map<string, Team[]>();
  const roots: Team[] = [];
  for (const t of teams) {
    const parentId = t.parent_id ?? null;
    if (parentId && byId.has(parentId)) {
      const list = children.get(parentId) ?? [];
      list.push(t);
      children.set(parentId, list);
    } else {
      roots.push(t);
    }
  }
  const byName = (a: Team, b: Team) => a.name.localeCompare(b.name);
  const out: TeamNode[] = [];
  const walk = (team: Team, depth: number) => {
    out.push({ team, depth });
    for (const child of (children.get(team.id) ?? []).sort(byName)) walk(child, depth + 1);
  };
  for (const root of roots.sort(byName)) walk(root, 0);
  return out;
}

/**
 * isInSubtree reports whether candidate sits inside rootId's subtree
 * (including rootId itself) — the client-side guard for parent selectors.
 * The materialised path ends in the team's own id, so containment is a
 * straight lookup; the server re-checks and rejects cycles regardless.
 */
function isInSubtree(candidate: Team, rootId: string): boolean {
  return candidate.id === rootId || (candidate.path ?? []).includes(rootId);
}

/** Options for a parent <select>: every team outside the excluded subtree. */
function ParentOptions({ teams, excludeSubtreeOf }: { teams: Team[]; excludeSubtreeOf?: string }) {
  const eligible = excludeSubtreeOf
    ? teams.filter((t) => !isInSubtree(t, excludeSubtreeOf))
    : teams;
  return (
    <>
      <option value="">No parent (root team)</option>
      {eligible.map((t) => (
        <option key={t.id} value={t.id}>
          {t.name}
        </option>
      ))}
    </>
  );
}

// ---------------------------------------------------------------------------
// Member panel
// ---------------------------------------------------------------------------

function TeamMemberPanel({ orgId, team }: { orgId: string; team: Team }) {
  const membersQuery = useTeamMembers(orgId, team.id);
  const putMember = usePutTeamMember(orgId, team.id);
  const removeMember = useRemoveTeamMember(orgId, team.id);

  const [newMember, setNewMember] = useState<PickedSubject | null>(null);
  const [newRole, setNewRole] = useState<TeamRole>('member');

  const members = membersQuery.data ?? [];
  const mutationError = putMember.error
    ? friendlyErrorMessage(putMember.error, 'The member could not be added.')
    : removeMember.error
      ? friendlyErrorMessage(removeMember.error, 'The member could not be removed.')
      : null;

  function handleAdd() {
    if (!newMember) return;
    putMember.mutate(
      { userId: newMember.id, role: newRole },
      { onSuccess: () => setNewMember(null) },
    );
  }

  return (
    <div className="mt-[var(--space-2)] rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-bg)] p-[var(--space-3)]">
      {membersQuery.isLoading && (
        <p className="py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading members…</p>
      )}
      {membersQuery.error && (
        <p className="py-2 text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(membersQuery.error, 'The member list could not be loaded.')}
        </p>
      )}

      {!membersQuery.isLoading && !membersQuery.error && members.length === 0 && (
        <p className="py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">No members yet.</p>
      )}

      {members.map((m) => (
        <div
          key={m.user_id}
          data-testid="team-member-row"
          className="flex items-center gap-3 rounded-[var(--radius-md)] px-2 py-1.5 hover:bg-[var(--color-surface-hover)]"
        >
          <div className="min-w-0 flex-1">
            <p className="truncate text-[var(--text-sm)] text-[var(--color-text)]">
              {m.display_name || m.user_id}
              {m.is_primary && (
                <span className="ml-2 inline-flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-warning)]">
                  <Star className="h-3 w-3 fill-current" />
                  primary
                </span>
              )}
            </p>
            {m.email && (
              <p className="truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">{m.email}</p>
            )}
          </div>
          <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">{m.role}</span>
          <Button
            variant="outline"
            size="sm"
            data-testid="team-member-role-toggle"
            disabled={putMember.isPending}
            onClick={() =>
              putMember.mutate({ userId: m.user_id, role: m.role === 'lead' ? 'member' : 'lead' })
            }
          >
            {m.role === 'lead' ? 'Make member' : 'Make lead'}
          </Button>
          {!m.is_primary && (
            <Button
              variant="outline"
              size="sm"
              data-testid="team-member-make-primary"
              disabled={putMember.isPending}
              onClick={() => putMember.mutate({ userId: m.user_id, role: m.role, is_primary: true })}
            >
              Make primary
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon"
            data-testid="team-member-remove"
            aria-label={`Remove ${m.display_name || m.user_id} from ${team.name}`}
            disabled={removeMember.isPending}
            onClick={() => removeMember.mutate(m.user_id)}
            className="h-8 w-8 text-[var(--color-text-muted)] hover:text-[var(--color-danger)]"
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      ))}

      <div className="mt-[var(--space-2)] flex items-center gap-2 border-t border-[var(--color-border)] pt-[var(--space-2)]">
        <PersonTeamPicker
          orgId={orgId}
          subjects="user"
          value={newMember}
          onChange={setNewMember}
          testId="team-member-picker"
        />
        <select
          value={newRole}
          onChange={(e) => setNewRole(e.target.value as TeamRole)}
          className={cn(selectClass, 'h-8 w-28')}
          aria-label="Role for new member"
        >
          <option value="member">member</option>
          <option value="lead">lead</option>
        </select>
        <Button
          size="sm"
          data-testid="team-member-add-button"
          disabled={putMember.isPending || !newMember}
          onClick={handleAdd}
        >
          Add
        </Button>
      </div>
      {mutationError && (
        <p className="mt-2 text-[var(--text-xs)] text-[var(--color-danger)]">{mutationError}</p>
      )}
      <p className="mt-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        Removing a user from their last team re-adds them to the org default team — no one is ever teamless.
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Team row
// ---------------------------------------------------------------------------

interface TeamRowProps {
  orgId: string;
  node: TeamNode;
  onEdit: (team: Team) => void;
  onDelete: (team: Team) => void;
  deletePending: boolean;
}

function TeamRow({ orgId, node, onEdit, onDelete, deletePending }: TeamRowProps) {
  const { team, depth } = node;
  const [expanded, setExpanded] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  return (
    <div data-testid="team-row" style={{ paddingLeft: depth * 24 }}>
      <div className="group flex items-center gap-2 rounded-[var(--radius-md)] px-2 py-2 hover:bg-[var(--color-surface-hover)]">
        <button
          type="button"
          data-testid="team-expand-button"
          aria-label={expanded ? `Collapse ${team.name}` : `Expand ${team.name} members`}
          onClick={() => setExpanded((p) => !p)}
          className="text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
        >
          {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        </button>
        <Users className="h-4 w-4 shrink-0 text-[var(--color-primary)]" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              {team.name}
            </span>
            <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">{team.slug}</span>
            {team.is_default && (
              <Badge variant="secondary" data-testid="team-default-badge">
                default
              </Badge>
            )}
          </div>
          {team.description && (
            <p className="truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">
              {team.description}
            </p>
          )}
        </div>

        <Button
          variant="ghost"
          size="sm"
          data-testid="team-edit-button"
          aria-label={`Edit ${team.name}`}
          onClick={() => onEdit(team)}
        >
          <Pencil className="mr-1 h-3.5 w-3.5" />
          Edit
        </Button>
        {confirmingDelete ? (
          <>
            <Button
              variant="destructive"
              size="sm"
              data-testid="team-delete-confirm"
              disabled={deletePending}
              onClick={() => {
                setConfirmingDelete(false);
                onDelete(team);
              }}
            >
              Confirm delete
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setConfirmingDelete(false)}>
              Cancel
            </Button>
          </>
        ) : (
          <Button
            variant="ghost"
            size="sm"
            data-testid="team-delete-button"
            aria-label={`Delete ${team.name}`}
            disabled={team.is_default || deletePending}
            title={team.is_default ? 'The org default team cannot be deleted' : undefined}
            onClick={() => setConfirmingDelete(true)}
            className="text-[var(--color-text-muted)] hover:text-[var(--color-danger)]"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>

      {expanded && <TeamMemberPanel orgId={orgId} team={team} />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// TeamsAdminPage
// ---------------------------------------------------------------------------

/** Org-admin surface for the team tree: create, rename, reparent, delete, membership. */
export function TeamsAdminPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const teamsQuery = useTeams(orgId);
  const createTeam = useCreateTeamWithSpaces(orgId);
  const updateTeam = useUpdateTeam(orgId);
  const deleteTeam = useDeleteTeam(orgId);

  const teams = useMemo(() => teamsQuery.data ?? [], [teamsQuery.data]);
  const ordered = useMemo(() => orderTeams(teams), [teams]);

  // Create dialog state. The slug tracks the name as lowercase-kebab until
  // the user edits it directly.
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState('');
  const [createSlug, setCreateSlug] = useState('');
  const [slugTouched, setSlugTouched] = useState(false);
  const [createParent, setCreateParent] = useState('');
  // Opt-in per-module spaces for the new team (default unchecked).
  const [createSpaces, setCreateSpaces] = useState<Record<SpaceType, boolean>>({
    beacon: false,
    codex: false,
    vector: false,
  });

  // Edit dialog state (rename + reparent).
  const [editing, setEditing] = useState<Team | null>(null);
  const [editName, setEditName] = useState('');
  const [editParent, setEditParent] = useState('');

  function openCreate() {
    createTeam.reset();
    setCreateName('');
    setCreateSlug('');
    setSlugTouched(false);
    setCreateParent('');
    setCreateSpaces({ beacon: false, codex: false, vector: false });
    setCreateOpen(true);
  }

  function handleCreate() {
    const name = createName.trim();
    const slug = createSlug.trim();
    if (!name || !slug) return;
    const modules = (['beacon', 'codex', 'vector'] as SpaceType[]).filter((m) => createSpaces[m]);
    createTeam.mutate(
      { name, slug, modules, ...(createParent ? { parent_id: createParent } : {}) },
      { onSuccess: () => setCreateOpen(false) },
    );
  }

  function openEdit(team: Team) {
    updateTeam.reset();
    setEditing(team);
    setEditName(team.name);
    setEditParent(team.parent_id ?? '');
  }

  function handleEditSave() {
    if (!editing) return;
    const name = editName.trim();
    if (!name) return;
    const req: { teamId: string; name?: string; parent_id?: string | null } = {
      teamId: editing.id,
    };
    if (name !== editing.name) req.name = name;
    const currentParent = editing.parent_id ?? '';
    if (editParent !== currentParent) {
      // '' means root — the wire encoding for "move to root" is null.
      req.parent_id = editParent === '' ? null : editParent;
    }
    if (req.name === undefined && !('parent_id' in req)) {
      setEditing(null);
      return;
    }
    updateTeam.mutate(req, { onSuccess: () => setEditing(null) });
  }

  if (teamsQuery.isLoading) {
    return (
      <div className="flex h-48 items-center justify-center text-[var(--color-text-muted)]">
        Loading teams…
      </div>
    );
  }

  if (teamsQuery.error) {
    return (
      <div className="rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-4 text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(teamsQuery.error, 'Teams could not be loaded.')}
      </div>
    );
  }

  return (
    <div className="space-y-6" data-testid="teams-admin-page">
      {/* Section header only — the page renders inside AdminLayout, which
          owns the Administration page header and tabs (P2.5 relocation). */}
      <div className="flex items-center justify-between">
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          The org team tree. Teams own spaces and receive grants; nesting is capped at 5 levels.
        </p>
        <Button data-testid="team-create-button" onClick={openCreate}>
          <Plus className="mr-2 h-4 w-4" />
          New team
        </Button>
      </div>

      {deleteTeam.error && (
        <div
          data-testid="team-error-banner"
          className="rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[var(--color-danger)]/10 p-3 text-[var(--text-sm)] text-[var(--color-danger)]"
        >
          {friendlyErrorMessage(deleteTeam.error, 'The team could not be deleted.')}
        </div>
      )}

      {ordered.length === 0 ? (
        <div className="flex min-h-[200px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
          <p className="text-[var(--color-text-muted)]">No teams found for this organization.</p>
        </div>
      ) : (
        <Card>
          <CardContent className="p-3">
            {ordered.map((node) => (
              <TeamRow
                key={node.team.id}
                orgId={orgId}
                node={node}
                onEdit={openEdit}
                onDelete={(team) => deleteTeam.mutate(team.id)}
                deletePending={deleteTeam.isPending}
              />
            ))}
          </CardContent>
        </Card>
      )}

      {/* Create team dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create a team</DialogTitle>
            <DialogDescription>
              Teams group people for space ownership and access grants.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <label htmlFor="team-name" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Name
              </label>
              <Input
                id="team-name"
                data-testid="team-create-name"
                placeholder="e.g. Platform"
                value={createName}
                autoFocus
                onChange={(e) => {
                  setCreateName(e.target.value);
                  if (!slugTouched) setCreateSlug(slugify(e.target.value));
                }}
              />
            </div>
            <div className="space-y-2">
              <label htmlFor="team-slug" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Slug
              </label>
              <Input
                id="team-slug"
                data-testid="team-create-slug"
                placeholder="e.g. platform"
                value={createSlug}
                className="font-mono"
                onChange={(e) => {
                  setSlugTouched(true);
                  setCreateSlug(e.target.value);
                }}
              />
            </div>
            <div className="space-y-2">
              <label htmlFor="team-parent" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Parent team
              </label>
              <select
                id="team-parent"
                data-testid="team-create-parent"
                value={createParent}
                onChange={(e) => setCreateParent(e.target.value)}
                className={selectClass}
              >
                <ParentOptions teams={teams} />
              </select>
            </div>
            <fieldset className="space-y-[var(--space-2)]">
              <legend className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                Create spaces for this team (optional)
              </legend>
              {(['beacon', 'codex', 'vector'] as SpaceType[]).map((module) => (
                <label
                  key={module}
                  className="flex items-center gap-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text)]"
                >
                  <input
                    type="checkbox"
                    data-testid={`team-create-space-${module}`}
                    checked={createSpaces[module]}
                    onChange={(e) => setCreateSpaces((s) => ({ ...s, [module]: e.target.checked }))}
                  />
                  <span className="capitalize">{module}</span>
                </label>
              ))}
              <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                Each checked module gets a space named for the team, with the team granted access.
              </p>
            </fieldset>
            {createTeam.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(createTeam.error, 'The team could not be created.')}
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
              data-testid="team-create-submit"
              onClick={handleCreate}
              disabled={createTeam.isPending || !createName.trim() || !createSlug.trim()}
            >
              {createTeam.isPending ? 'Creating…' : 'Create team'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit team dialog (rename + reparent) */}
      <Dialog open={!!editing} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit team</DialogTitle>
            <DialogDescription>
              Rename the team or move it elsewhere in the tree.
            </DialogDescription>
          </DialogHeader>
          {editing && (
            <div className="space-y-4 py-2">
              <div className="space-y-2">
                <label htmlFor="team-edit-name" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                  Name
                </label>
                <Input
                  id="team-edit-name"
                  data-testid="team-edit-name"
                  value={editName}
                  autoFocus
                  onChange={(e) => setEditName(e.target.value)}
                />
              </div>
              <div className="space-y-2">
                <label htmlFor="team-edit-parent" className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                  Parent team
                </label>
                <select
                  id="team-edit-parent"
                  data-testid="team-parent-select"
                  value={editParent}
                  disabled={editing.is_default}
                  onChange={(e) => setEditParent(e.target.value)}
                  className={selectClass}
                >
                  <ParentOptions teams={teams} excludeSubtreeOf={editing.id} />
                </select>
                {editing.is_default && (
                  <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    The org default team cannot be reparented.
                  </p>
                )}
              </div>
              {updateTeam.error && (
                <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                  {friendlyErrorMessage(updateTeam.error, 'The change could not be saved.')}
                </p>
              )}
            </div>
          )}
          <DialogFooter>
            <DialogClose asChild>
              <Button variant="outline" type="button">
                Cancel
              </Button>
            </DialogClose>
            <Button
              data-testid="team-edit-submit"
              onClick={handleEditSave}
              disabled={updateTeam.isPending || !editName.trim()}
            >
              {updateTeam.isPending ? 'Saving…' : 'Save'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
